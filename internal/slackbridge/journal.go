package slackbridge

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/fileutil"
)

const (
	journalVersion      = 1
	journalRetention    = 24 * time.Hour
	journalMaxRecords   = 2000
	journalMaxBytes     = 8 << 20
	recipientPending    = "pending"
	recipientDelivering = "delivering"
	recipientFailed     = "failed"
	recipientDelivered  = "delivered"
	recipientExpired    = "expired"
)

var ErrJournalFull = errors.New("Slack event journal capacity reached")

type journalRecipient struct {
	SessionID      string    `json:"session_id"`
	InstallationID string    `json:"installation_id"`
	State          string    `json:"state"`
	Attempts       int       `json:"attempts,omitempty"`
	LastErrorClass string    `json:"last_error_class,omitempty"`
	NextAttemptAt  time.Time `json:"next_attempt_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type journalRecord struct {
	Sequence   uint64             `json:"sequence"`
	Event      Event              `json:"event"`
	Recipients []journalRecipient `json:"recipients"`
	AcceptedAt time.Time          `json:"accepted_at"`
	ExpiredAt  time.Time          `json:"expired_at,omitempty"`
}

type journalDocument struct {
	Version      int             `json:"version"`
	AppID        string          `json:"app_id"`
	NextSequence uint64          `json:"next_sequence"`
	Records      []journalRecord `json:"records"`
}

type JournalStats struct {
	Pending   int
	Failed    int
	Expired   int
	Delivered int
}

type journalBatch struct {
	AppID     string
	SessionID string
	EventIDs  []string
	Events    []conversation.PromptSlackEvent
}

// FileJournal persists one atomically-replaced document per app profile.
type FileJournal struct {
	baseDir string
	mu      sync.Mutex
	now     func() time.Time
}

func NewFileJournal(baseDir string) *FileJournal {
	return &FileJournal{baseDir: baseDir, now: func() time.Time { return time.Now().UTC() }}
}

func (j *FileJournal) dir() (string, error) {
	if j.baseDir != "" {
		return j.baseDir, nil
	}
	return appdir.SlackEventJournalDir()
}

func (j *FileJournal) path(appID string) (string, error) {
	dir, err := j.dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(appID))
	return filepath.Join(dir, fmt.Sprintf("%x.json", sum[:16])), nil
}

func (j *FileJournal) loadLocked(appID string) (*journalDocument, string, error) {
	path, err := j.path(appID)
	if err != nil {
		return nil, "", err
	}
	doc := &journalDocument{Version: journalVersion, AppID: appID, Records: []journalRecord{}}
	if err := fileutil.ReadJSON(path, doc); err != nil {
		if os.IsNotExist(err) {
			return doc, path, nil
		}
		return nil, path, fmt.Errorf("read Slack event journal: %w", err)
	}
	if doc.Version != journalVersion || doc.AppID != appID {
		return nil, path, errors.New("invalid Slack event journal identity or version")
	}
	return doc, path, nil
}

func (j *FileJournal) writeLocked(path string, doc *journalDocument) error {
	if err := fileutil.WriteJSONAtomic(path, doc, 0600); err != nil {
		return fmt.Errorf("write Slack event journal: %w", err)
	}
	return os.Chmod(path, 0600)
}

func (j *FileJournal) prune(doc *journalDocument, now time.Time) (newlyExpired int) {
	kept := doc.Records[:0]
	for i := range doc.Records {
		r := &doc.Records[i]
		if r.ExpiredAt.IsZero() && now.Sub(r.AcceptedAt) >= journalRetention {
			for n := range r.Recipients {
				if r.Recipients[n].State != recipientDelivered {
					r.Recipients[n].State = recipientExpired
					r.Recipients[n].UpdatedAt = now
					newlyExpired++
				}
			}
			r.Event.Text = ""
			r.ExpiredAt = now
		}
		terminal := allRecipientsTerminal(r.Recipients)
		anchor := r.AcceptedAt
		if !r.ExpiredAt.IsZero() {
			anchor = r.ExpiredAt
		}
		if terminal && now.Sub(anchor) >= journalRetention {
			continue
		}
		kept = append(kept, *r)
	}
	doc.Records = kept
	return newlyExpired
}

func allRecipientsTerminal(recipients []journalRecipient) bool {
	for _, recipient := range recipients {
		if recipient.State != recipientDelivered && recipient.State != recipientExpired {
			return false
		}
	}
	return true
}

func (j *FileJournal) Accept(appID string, event Event, recipients []journalRecipient) (bool, error) {
	if appID == "" || event.EventID == "" {
		return false, errors.New("Slack journal acceptance requires app_id and event_id")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	doc, path, err := j.loadLocked(appID)
	if err != nil {
		return false, err
	}
	now := j.now()
	changed := j.prune(doc, now) > 0
	for _, record := range doc.Records {
		if record.Event.EventID == event.EventID {
			if changed {
				err = j.writeLocked(path, doc)
			}
			return true, err
		}
	}
	if len(doc.Records) >= journalMaxRecords {
		return false, ErrJournalFull
	}
	sort.Slice(recipients, func(a, b int) bool { return recipients[a].SessionID < recipients[b].SessionID })
	for i := range recipients {
		recipients[i].State, recipients[i].UpdatedAt = recipientPending, now
	}
	if len(recipients) == 0 {
		event.Text = ""
	}
	doc.NextSequence++
	doc.Records = append(doc.Records, journalRecord{Sequence: doc.NextSequence, Event: event, Recipients: recipients, AcceptedAt: now})
	encoded, err := json.Marshal(doc)
	if err != nil {
		return false, err
	}
	if len(encoded) > journalMaxBytes {
		return false, ErrJournalFull
	}
	return false, j.writeLocked(path, doc)
}

func (j *FileJournal) ClaimBatch(appID, sessionID string, maxCount, maxBytes int) (journalBatch, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	doc, path, err := j.loadLocked(appID)
	if err != nil {
		return journalBatch{}, err
	}
	now := j.now()
	changed := j.prune(doc, now) > 0
	batch := journalBatch{AppID: appID, SessionID: sessionID}
	used := 0
	for ri := range doc.Records {
		record := &doc.Records[ri]
		for pi := range record.Recipients {
			recipient := &record.Recipients[pi]
			if recipient.SessionID != sessionID || (recipient.State != recipientPending && recipient.State != recipientFailed) || (!recipient.NextAttemptAt.IsZero() && now.Before(recipient.NextAttemptAt)) {
				continue
			}
			event := promptEvent(record.Event, recipient.InstallationID)
			size := slackEventBytes(event)
			if len(batch.Events) >= maxCount || (len(batch.Events) > 0 && used+size > maxBytes) {
				break
			}
			recipient.State, recipient.UpdatedAt = recipientDelivering, now
			batch.EventIDs = append(batch.EventIDs, record.Event.EventID)
			batch.Events = append(batch.Events, event)
			used += size
			changed = true
			break
		}
	}
	if changed {
		err = j.writeLocked(path, doc)
	}
	return batch, err
}

func promptEvent(event Event, installationID string) conversation.PromptSlackEvent {
	return conversation.PromptSlackEvent{InstallationID: installationID, EventID: event.EventID, ChannelID: event.ChannelID,
		Kind: event.Kind, AuthorID: event.AuthorID, Timestamp: event.Timestamp, ThreadTimestamp: event.ThreadTimestamp,
		Text: event.Text, Untrusted: true}
}

func slackEventBytes(event conversation.PromptSlackEvent) int {
	return len(event.InstallationID) + len(event.EventID) + len(event.ChannelID) + len(event.Kind) + len(event.AuthorID) + len(event.Timestamp) + len(event.ThreadTimestamp) + len(event.Text)
}

func (j *FileJournal) Complete(batch journalBatch, delivered, contention bool, errorClass string) error {
	if len(batch.EventIDs) == 0 {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	doc, path, err := j.loadLocked(batch.AppID)
	if err != nil {
		return err
	}
	now := j.now()
	wanted := make(map[string]bool, len(batch.EventIDs))
	for _, id := range batch.EventIDs {
		wanted[id] = true
	}
	for ri := range doc.Records {
		r := &doc.Records[ri]
		if !wanted[r.Event.EventID] {
			continue
		}
		for pi := range r.Recipients {
			recipient := &r.Recipients[pi]
			if recipient.SessionID != batch.SessionID || recipient.State != recipientDelivering {
				continue
			}
			recipient.UpdatedAt = now
			switch {
			case delivered:
				recipient.State, recipient.LastErrorClass = recipientDelivered, ""
			case contention:
				recipient.State, recipient.LastErrorClass = recipientPending, ""
			case errorClass != "":
				recipient.State, recipient.LastErrorClass = recipientFailed, errorClass
				recipient.Attempts++
				recipient.NextAttemptAt = now.Add(retryDelay(recipient.Attempts))
			default:
				recipient.State = recipientPending
			}
		}
		if allRecipientsTerminal(r.Recipients) {
			r.Event.Text = ""
		}
	}
	j.prune(doc, now)
	return j.writeLocked(path, doc)
}

func retryDelay(attempts int) time.Duration {
	delay := time.Minute
	for n := 1; n < attempts && delay < 15*time.Minute; n++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func (j *FileJournal) ProfilesForSession(sessionID string) ([]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	dir, err := j.dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var doc journalDocument
		if err := fileutil.ReadJSON(filepath.Join(dir, entry.Name()), &doc); err != nil {
			continue
		}
		for _, record := range doc.Records {
			for _, recipient := range record.Recipients {
				if recipient.SessionID == sessionID && (recipient.State == recipientPending || recipient.State == recipientFailed || recipient.State == recipientDelivering) {
					profiles = append(profiles, doc.AppID)
					goto nextFile
				}
			}
		}
	nextFile:
	}
	sort.Strings(profiles)
	return profiles, nil
}

func (j *FileJournal) PendingSessions(appID string) ([]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	doc, path, err := j.loadLocked(appID)
	if err != nil {
		return nil, err
	}
	if j.prune(doc, j.now()) > 0 {
		if err := j.writeLocked(path, doc); err != nil {
			return nil, err
		}
	}
	seen := make(map[string]bool)
	for _, record := range doc.Records {
		for _, recipient := range record.Recipients {
			if recipient.State == recipientPending || recipient.State == recipientFailed {
				seen[recipient.SessionID] = true
			}
		}
	}
	sessions := make([]string, 0, len(seen))
	for sessionID := range seen {
		sessions = append(sessions, sessionID)
	}
	sort.Strings(sessions)
	return sessions, nil
}

func (j *FileJournal) Recover() ([]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	dir, err := j.dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var doc journalDocument
		if err := fileutil.ReadJSON(path, &doc); err != nil || doc.Version != journalVersion || doc.AppID == "" {
			continue
		}
		now, changed := j.now(), false
		for ri := range doc.Records {
			for pi := range doc.Records[ri].Recipients {
				if doc.Records[ri].Recipients[pi].State == recipientDelivering {
					doc.Records[ri].Recipients[pi].State = recipientPending
					doc.Records[ri].Recipients[pi].UpdatedAt = now
					changed = true
				}
			}
		}
		if j.prune(&doc, now) > 0 {
			changed = true
		}
		if changed {
			if err := j.writeLocked(path, &doc); err != nil {
				return profiles, err
			}
		}
		profiles = append(profiles, doc.AppID)
	}
	sort.Strings(profiles)
	return profiles, nil
}

func (j *FileJournal) Stats(appID string) (JournalStats, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	doc, path, err := j.loadLocked(appID)
	if err != nil {
		return JournalStats{}, err
	}
	if j.prune(doc, j.now()) > 0 {
		if err := j.writeLocked(path, doc); err != nil {
			return JournalStats{}, err
		}
	}
	var stats JournalStats
	for _, record := range doc.Records {
		for _, recipient := range record.Recipients {
			switch recipient.State {
			case recipientPending, recipientDelivering:
				stats.Pending++
			case recipientFailed:
				stats.Failed++
			case recipientExpired:
				stats.Expired++
			case recipientDelivered:
				stats.Delivered++
			}
		}
	}
	return stats, nil
}
