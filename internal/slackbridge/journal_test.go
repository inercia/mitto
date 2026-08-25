package slackbridge

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
)

func newTestJournal(t *testing.T) (*FileJournal, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	j := NewFileJournal(t.TempDir())
	j.now = func() time.Time { return now }
	return j, &now
}

func readJournalDocument(t *testing.T, j *FileJournal, appID string) *journalDocument {
	t.Helper()
	j.mu.Lock()
	doc, _, err := j.loadLocked(appID)
	j.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestFileJournalAcceptDeduplicatesAndPersistsOrderedRecipients(t *testing.T) {
	j, _ := newTestJournal(t)
	recipients := []journalRecipient{{SessionID: "z", InstallationID: "iz"}, {SessionID: "a", InstallationID: "ia"}}
	duplicate, err := j.Accept("app", Event{EventID: "e1", Text: "secret", AuthorizationScopeKnown: true,
		Authorizations: []EventAuthorization{{UserID: "U-AUTHORIZATION-CANARY"}}}, recipients)
	if err != nil || duplicate {
		t.Fatalf("first Accept() duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = j.Accept("app", Event{EventID: "e1", Text: "changed"}, []journalRecipient{{SessionID: "new"}})
	if err != nil || !duplicate {
		t.Fatalf("duplicate Accept() duplicate=%v err=%v", duplicate, err)
	}
	// Distinct ChannelID: e2 is an unrelated event on a different
	// conversation surface, so it must not coalesce with e1's recipients
	// (see coalesceSupersededLocked, mitto-7vk).
	if _, err := j.Accept("app", Event{EventID: "e2", ChannelID: "other"}, recipients[:1]); err != nil {
		t.Fatal(err)
	}
	doc := readJournalDocument(t, j, "app")
	if len(doc.Records) != 2 || doc.Records[0].Sequence != 1 || doc.Records[1].Sequence != 2 {
		t.Fatalf("records=%#v, want two monotonic records", doc.Records)
	}
	got := doc.Records[0].Recipients
	if len(got) != 2 || got[0].SessionID != "a" || got[1].SessionID != "z" || doc.Records[0].Event.Text != "secret" {
		t.Fatalf("first record=%#v", doc.Records[0])
	}
	path, _ := j.path("app")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal mode=%v err=%v, want 0600", info.Mode().Perm(), err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "U-AUTHORIZATION-CANARY") || strings.Contains(string(raw), "Authorizations") {
		t.Fatalf("journal persisted transient authorization metadata: %s", raw)
	}
}

func TestFileJournalBatchLimitsPreserveIngestOrder(t *testing.T) {
	j, _ := newTestJournal(t)
	recipient := []journalRecipient{{SessionID: "s", InstallationID: "i"}}
	// Distinct ChannelID per event: these are 25 unrelated conversation
	// surfaces, so none should coalesce with another (see
	// coalesceSupersededLocked, mitto-7vk) -- this test exercises
	// ClaimBatch's count/order limits, not coalescing.
	for n := 0; n < 25; n++ {
		if _, err := j.Accept("count", Event{EventID: fmt.Sprintf("e%02d", n), ChannelID: fmt.Sprintf("c%02d", n), Text: "x"}, recipient); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := j.ClaimBatch("count", "s", conversation.MaxSlackEventsPerDispatch, conversation.MaxSlackEventBatchBytes)
	if err != nil || len(batch.Events) != 20 || batch.Events[0].EventID != "e00" || batch.Events[19].EventID != "e19" {
		t.Fatalf("count batch len=%d first/last=%q/%q err=%v", len(batch.Events), batch.Events[0].EventID, batch.Events[len(batch.Events)-1].EventID, err)
	}
	if err := j.Complete(batch, true, false, ""); err != nil {
		t.Fatal(err)
	}
	next, err := j.ClaimBatch("count", "s", conversation.MaxSlackEventsPerDispatch, conversation.MaxSlackEventBatchBytes)
	if err != nil || len(next.Events) != 5 || next.Events[0].EventID != "e20" {
		t.Fatalf("overflow batch=%#v err=%v", next.Events, err)
	}

	// Distinct ChannelID per event, same reasoning as above.
	for n := 0; n < 10; n++ {
		if _, err := j.Accept("bytes", Event{EventID: fmt.Sprintf("b%02d", n), ChannelID: fmt.Sprintf("c%02d", n), Text: strings.Repeat("x", 4000)}, recipient); err != nil {
			t.Fatal(err)
		}
	}
	byteBatch, err := j.ClaimBatch("bytes", "s", 20, conversation.MaxSlackEventBatchBytes)
	if err != nil || len(byteBatch.Events) < 2 || len(byteBatch.Events) >= 10 {
		t.Fatalf("byte batch len=%d err=%v", len(byteBatch.Events), err)
	}
	used := 0
	for _, event := range byteBatch.Events {
		used += slackEventBytes(event)
	}
	if used > conversation.MaxSlackEventBatchBytes {
		t.Fatalf("byte batch uses %d bytes, max %d", used, conversation.MaxSlackEventBatchBytes)
	}
}

func TestFileJournalRecipientProgressIsIndependentAndScrubsContent(t *testing.T) {
	j, _ := newTestJournal(t)
	recipients := []journalRecipient{{SessionID: "a", InstallationID: "ia"}, {SessionID: "b", InstallationID: "ib"}}
	if _, err := j.Accept("app", Event{EventID: "e1", Text: "sensitive"}, recipients); err != nil {
		t.Fatal(err)
	}
	a, _ := j.ClaimBatch("app", "a", 20, 32<<10)
	if err := j.Complete(a, true, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := readJournalDocument(t, j, "app").Records[0].Event.Text; got != "sensitive" {
		t.Fatalf("text scrubbed before all recipients terminal: %q", got)
	}
	b, _ := j.ClaimBatch("app", "b", 20, 32<<10)
	if err := j.Complete(b, false, true, ""); err != nil {
		t.Fatal(err)
	}
	stats, _ := j.Stats("app")
	if stats.Delivered != 1 || stats.Pending != 1 {
		t.Fatalf("stats after contention=%+v", stats)
	}
	b, _ = j.ClaimBatch("app", "b", 20, 32<<10)
	if err := j.Complete(b, true, false, ""); err != nil {
		t.Fatal(err)
	}
	doc := readJournalDocument(t, j, "app")
	if doc.Records[0].Event.Text != "" {
		t.Fatal("event text retained after all recipients were delivered")
	}
}

func TestFileJournalRecoversDeliveringAndExpiresPending(t *testing.T) {
	j, now := newTestJournal(t)
	recipient := []journalRecipient{{SessionID: "s", InstallationID: "i"}}
	if _, err := j.Accept("recover", Event{EventID: "e1", Text: "text"}, recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := j.ClaimBatch("recover", "s", 20, 32<<10); err != nil {
		t.Fatal(err)
	}
	restarted := NewFileJournal(j.baseDir)
	restarted.now = func() time.Time { return *now }
	profiles, err := restarted.Recover()
	if err != nil || len(profiles) != 1 || profiles[0] != "recover" {
		t.Fatalf("Recover() profiles=%v err=%v", profiles, err)
	}
	batch, err := restarted.ClaimBatch("recover", "s", 20, 32<<10)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].EventID != "e1" {
		t.Fatalf("recovered batch=%#v err=%v", batch, err)
	}
	duplicate, err := restarted.Accept("recover", Event{EventID: "e1"}, recipient)
	if err != nil || !duplicate {
		t.Fatalf("post-restart duplicate=%v err=%v", duplicate, err)
	}

	if _, err := j.Accept("expire", Event{EventID: "old", Text: "remove me"}, recipient); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(journalRetention)
	stats, err := j.Stats("expire")
	if err != nil || stats.Expired != 1 {
		t.Fatalf("expired stats=%+v err=%v", stats, err)
	}
	doc := readJournalDocument(t, j, "expire")
	if doc.Records[0].Event.Text != "" || doc.Records[0].Recipients[0].State != recipientExpired {
		t.Fatalf("expired record=%#v", doc.Records[0])
	}
	*now = now.Add(journalRetention)
	stats, _ = j.Stats("expire")
	if stats != (JournalStats{}) || len(readJournalDocument(t, j, "expire").Records) != 0 {
		t.Fatalf("expired tombstone not pruned: %+v", stats)
	}
}

func TestFileJournalRejectsCapacityUntilTerminalRecordsPrune(t *testing.T) {
	j, now := newTestJournal(t)
	doc := &journalDocument{Version: journalVersion, AppID: "app", NextSequence: journalMaxRecords}
	for n := 0; n < journalMaxRecords; n++ {
		doc.Records = append(doc.Records, journalRecord{Sequence: uint64(n + 1), Event: Event{EventID: fmt.Sprintf("e%d", n)},
			Recipients: []journalRecipient{{SessionID: "s", State: recipientDelivered, UpdatedAt: *now}}, AcceptedAt: *now})
	}
	path, _ := j.path("app")
	if err := j.writeLocked(path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Accept("app", Event{EventID: "overflow"}, nil); !errors.Is(err, ErrJournalFull) {
		t.Fatalf("Accept() error=%v, want ErrJournalFull", err)
	}
	*now = now.Add(journalRetention)
	if duplicate, err := j.Accept("app", Event{EventID: "after-prune"}, nil); err != nil || duplicate {
		t.Fatalf("Accept after prune duplicate=%v err=%v", duplicate, err)
	}
}

// TestFileJournalZeroRecipientEventsMustNotExhaustCapacity reproduces mitto-d8y:
// a busy Slack channel with no subscribed session floods the journal with
// empty-recipient records. Those records are vacuously "terminal"
// (allRecipientsTerminal returns true for an empty slice) but prune only
// drops terminal records once the 24h retention window has elapsed, so they
// pin the 2000-record hard cap for a full day and reject every subsequent
// event -- including ones that DO have a recipient.
func TestFileJournalZeroRecipientEventsMustNotExhaustCapacity(t *testing.T) {
	j, _ := newTestJournal(t)
	for n := 0; n < journalMaxRecords+50; n++ {
		if _, err := j.Accept("app", Event{EventID: fmt.Sprintf("empty-%d", n)}, nil); err != nil && !errors.Is(err, ErrJournalFull) {
			t.Fatalf("unexpected Accept() error for zero-recipient event %d: %v", n, err)
		}
	}
	doc := readJournalDocument(t, j, "app")
	if len(doc.Records) >= journalMaxRecords {
		t.Fatalf("zero-recipient events pinned the journal at/above cap: %d records (want a small/empty bound); "+
			"a channel with no subscribed session must never exhaust journal capacity", len(doc.Records))
	}
	if _, err := j.Accept("app", Event{EventID: "with-recipient"}, []journalRecipient{{SessionID: "s"}}); err != nil {
		t.Fatalf("event WITH a recipient was rejected after a zero-recipient flood: %v "+
			"(journal must never reject real events because of empty-recipient noise)", err)
	}
}

func TestFileJournalFullPersistsExpirationBeforeRejecting(t *testing.T) {
	j, now := newTestJournal(t)
	doc := &journalDocument{Version: journalVersion, AppID: "app", NextSequence: journalMaxRecords}
	for n := 0; n < journalMaxRecords; n++ {
		doc.Records = append(doc.Records, journalRecord{Sequence: uint64(n + 1), Event: Event{EventID: fmt.Sprintf("e%d", n), Text: "erase"},
			Recipients: []journalRecipient{{SessionID: "s", State: recipientPending, UpdatedAt: *now}}, AcceptedAt: *now})
	}
	path, _ := j.path("app")
	if err := j.writeLocked(path, doc); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(journalRetention)
	if _, err := j.Accept("app", Event{EventID: "still-full"}, nil); !errors.Is(err, ErrJournalFull) {
		t.Fatalf("Accept() error=%v, want ErrJournalFull", err)
	}
	persisted := readJournalDocument(t, j, "app")
	if persisted.Records[0].Recipients[0].State != recipientExpired || persisted.Records[0].Event.Text != "" || persisted.Records[0].ExpiredAt.IsZero() {
		t.Fatalf("expiration was not persisted before capacity rejection: %#v", persisted.Records[0])
	}
	*now = now.Add(journalRetention)
	if duplicate, err := j.Accept("app", Event{EventID: "after-expired-prune"}, nil); err != nil || duplicate {
		t.Fatalf("Accept after expired tombstone prune duplicate=%v err=%v", duplicate, err)
	}
}

func TestFileJournalDeliveredTombstoneRetainsFromDeliveryTime(t *testing.T) {
	j, now := newTestJournal(t)
	if _, err := j.Accept("app", Event{EventID: "slow", Text: "erase"}, []journalRecipient{{SessionID: "s"}}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(23 * time.Hour)
	batch, err := j.ClaimBatch("app", "s", 20, 32<<10)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(batch, true, false, ""); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Hour)
	stats, err := j.Stats("app")
	if err != nil || stats.Delivered != 1 {
		t.Fatalf("delivered tombstone pruned before 24h from delivery: stats=%+v err=%v", stats, err)
	}
	*now = now.Add(22 * time.Hour)
	stats, err = j.Stats("app")
	if err != nil || stats != (JournalStats{}) {
		t.Fatalf("delivered tombstone retained beyond 24h from delivery: stats=%+v err=%v", stats, err)
	}
}

// findRecord returns the record with the given EventID, or nil.
func findRecord(doc *journalDocument, eventID string) *journalRecord {
	for i := range doc.Records {
		if doc.Records[i].Event.EventID == eventID {
			return &doc.Records[i]
		}
	}
	return nil
}

// recipientByID returns the recipient with the given SessionID within
// recipients, or nil.
func recipientByID(recipients []journalRecipient, sessionID string) *journalRecipient {
	for i := range recipients {
		if recipients[i].SessionID == sessionID {
			return &recipients[i]
		}
	}
	return nil
}

// TestCoalesceSupersededBurstKeepsOnlyNewestPending covers the primary
// mitto-7vk acceptance criterion: a burst of N plain "message" events in one
// thread for one subscribed session leaves at most ONE pending record for
// that (session, channel, thread), and the retained record is the NEWEST.
func TestCoalesceSupersededBurstKeepsOnlyNewestPending(t *testing.T) {
	j, _ := newTestJournal(t)
	for n := 0; n < 50; n++ {
		if _, err := j.Accept("app", Event{EventID: fmt.Sprintf("m%02d", n), ChannelID: "c1", ThreadTimestamp: "t1",
			Kind: "message", Text: "x"}, []journalRecipient{{SessionID: "s"}}); err != nil {
			t.Fatalf("Accept(m%02d) err=%v", n, err)
		}
	}
	doc := readJournalDocument(t, j, "app")
	if len(doc.Records) != 1 {
		t.Fatalf("records=%#v, want exactly one surviving record after burst coalescing", doc.Records)
	}
	if doc.Records[0].Event.EventID != "m49" {
		t.Fatalf("retained record=%q, want newest event m49", doc.Records[0].Event.EventID)
	}
	if len(doc.Records[0].Recipients) != 1 || doc.Records[0].Recipients[0].State != recipientPending {
		t.Fatalf("retained record recipients=%#v, want one pending recipient", doc.Records[0].Recipients)
	}
}

// TestCoalesceNeverDropsPendingAppMention covers the mention carve-out: a
// pending app_mention must never be superseded by a later plain message in
// the same thread, while later plain messages still coalesce among
// themselves.
func TestCoalesceNeverDropsPendingAppMention(t *testing.T) {
	j, _ := newTestJournal(t)
	if _, err := j.Accept("app", Event{EventID: "mention", ChannelID: "c1", ThreadTimestamp: "t1",
		Kind: "app_mention", Text: "@bot help"}, []journalRecipient{{SessionID: "s"}}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		if _, err := j.Accept("app", Event{EventID: fmt.Sprintf("m%02d", n), ChannelID: "c1", ThreadTimestamp: "t1",
			Kind: "message", Text: "x"}, []journalRecipient{{SessionID: "s"}}); err != nil {
			t.Fatalf("Accept(m%02d) err=%v", n, err)
		}
	}
	doc := readJournalDocument(t, j, "app")
	mention := findRecord(doc, "mention")
	if mention == nil {
		t.Fatal("pending app_mention was dropped by a later plain message")
	}
	if len(mention.Recipients) != 1 || mention.Recipients[0].State != recipientPending || mention.Event.Text != "@bot help" {
		t.Fatalf("mention record mutated by coalescing: %#v", *mention)
	}
	newest := findRecord(doc, "m04")
	if newest == nil {
		t.Fatal("newest message m04 missing")
	}
	if len(doc.Records) != 2 {
		t.Fatalf("records=%#v, want exactly [mention, newest message]", doc.Records)
	}
}

// TestCoalesceDistinctThreadNotCoalesced covers thread isolation: events
// sharing a channel and session but with distinct ThreadTimestamp values
// must not coalesce with each other.
func TestCoalesceDistinctThreadNotCoalesced(t *testing.T) {
	j, _ := newTestJournal(t)
	if _, err := j.Accept("app", Event{EventID: "e1", ChannelID: "c1", ThreadTimestamp: "t1", Kind: "message"},
		[]journalRecipient{{SessionID: "s"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Accept("app", Event{EventID: "e2", ChannelID: "c1", ThreadTimestamp: "t2", Kind: "message"},
		[]journalRecipient{{SessionID: "s"}}); err != nil {
		t.Fatal(err)
	}
	doc := readJournalDocument(t, j, "app")
	if len(doc.Records) != 2 {
		t.Fatalf("records=%#v, want both threads' events preserved uncoalesced", doc.Records)
	}
	for _, id := range []string{"e1", "e2"} {
		r := findRecord(doc, id)
		if r == nil || len(r.Recipients) != 1 || r.Recipients[0].State != recipientPending {
			t.Fatalf("record %q=%#v, want a surviving pending recipient", id, r)
		}
	}
}

// TestCoalesceSessionIndependenceAsymmetricRecipients covers per-recipient
// coalescing: two sessions subscribed to the same channel/thread each retain
// their own pending event, and coalescing triggered by one session's newer
// event must not affect a different session's still-pending entry sharing
// the same older record (asymmetric recipient sets).
func TestCoalesceSessionIndependenceAsymmetricRecipients(t *testing.T) {
	j, _ := newTestJournal(t)
	if _, err := j.Accept("app", Event{EventID: "e1", ChannelID: "c1", ThreadTimestamp: "t1", Kind: "message"},
		[]journalRecipient{{SessionID: "a"}, {SessionID: "b"}}); err != nil {
		t.Fatal(err)
	}
	// e2 supersedes only session "a"'s pending entry in e1; session "b" is
	// not a recipient of e2 and must keep its own pending delivery of e1.
	if _, err := j.Accept("app", Event{EventID: "e2", ChannelID: "c1", ThreadTimestamp: "t1", Kind: "message"},
		[]journalRecipient{{SessionID: "a"}}); err != nil {
		t.Fatal(err)
	}
	doc := readJournalDocument(t, j, "app")
	e1 := findRecord(doc, "e1")
	if e1 == nil {
		t.Fatal("e1 was dropped entirely, but session b's pending delivery must survive")
	}
	if len(e1.Recipients) != 1 || e1.Recipients[0].SessionID != "b" || e1.Recipients[0].State != recipientPending {
		t.Fatalf("e1 recipients=%#v, want only session b still pending", e1.Recipients)
	}
	e2 := findRecord(doc, "e2")
	if e2 == nil || len(e2.Recipients) != 1 || e2.Recipients[0].SessionID != "a" || e2.Recipients[0].State != recipientPending {
		t.Fatalf("e2 recipients=%#v, want session a pending", e2)
	}
}

// TestCoalesceNeverDropsNonPendingRecipients covers the state-protection
// invariant: delivering/delivered/failed/expired recipient entries are never
// dropped by coalescing, even when a newer event shares the same
// (session, channel, thread) surface -- coalescing must not race an
// in-flight claim or discard delivery bookkeeping.
func TestCoalesceNeverDropsNonPendingRecipients(t *testing.T) {
	for _, state := range []string{recipientDelivering, recipientDelivered, recipientFailed, recipientExpired} {
		t.Run(state, func(t *testing.T) {
			j, now := newTestJournal(t)
			doc := &journalDocument{Version: journalVersion, AppID: "app", NextSequence: 1, Records: []journalRecord{{
				Sequence:   1,
				Event:      Event{EventID: "old", ChannelID: "c1", ThreadTimestamp: "t1", Kind: "message", Text: "keep"},
				Recipients: []journalRecipient{{SessionID: "s", State: state, UpdatedAt: *now}},
				AcceptedAt: *now,
			}}}
			path, _ := j.path("app")
			if err := j.writeLocked(path, doc); err != nil {
				t.Fatal(err)
			}
			if _, err := j.Accept("app", Event{EventID: "new", ChannelID: "c1", ThreadTimestamp: "t1", Kind: "message"},
				[]journalRecipient{{SessionID: "s"}}); err != nil {
				t.Fatal(err)
			}
			persisted := readJournalDocument(t, j, "app")
			old := findRecord(persisted, "old")
			if old == nil {
				t.Fatalf("record in non-pending state %q was dropped by coalescing", state)
			}
			recipient := recipientByID(old.Recipients, "s")
			if recipient == nil || recipient.State != state {
				t.Fatalf("recipient state mutated by coalescing: got %#v, want state=%q untouched", recipient, state)
			}
			if findRecord(persisted, "new") == nil {
				t.Fatal("new event was not accepted")
			}
		})
	}
}

// TestCoalesceFirehosePreventsJournalFull covers the capacity acceptance
// criterion: a single-thread firehose that would previously have hit the
// journalMaxRecords cap must never return ErrJournalFull once superseded
// pending records are coalesced away as they arrive.
func TestCoalesceFirehosePreventsJournalFull(t *testing.T) {
	j, _ := newTestJournal(t)
	for n := 0; n < journalMaxRecords+50; n++ {
		if _, err := j.Accept("app", Event{EventID: fmt.Sprintf("f%d", n), ChannelID: "c1", ThreadTimestamp: "t1", Kind: "message"},
			[]journalRecipient{{SessionID: "s"}}); err != nil {
			t.Fatalf("Accept(f%d) unexpected error (want no ErrJournalFull for a coalesced single-thread firehose): %v", n, err)
		}
	}
	doc := readJournalDocument(t, j, "app")
	if len(doc.Records) != 1 {
		t.Fatalf("records=%#v, want the firehose bounded to a single surviving record", doc.Records)
	}
	if doc.Records[0].Event.EventID != fmt.Sprintf("f%d", journalMaxRecords+49) {
		t.Fatalf("retained record=%q, want the newest event", doc.Records[0].Event.EventID)
	}
}
