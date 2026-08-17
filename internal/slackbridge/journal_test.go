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
	duplicate, err := j.Accept("app", Event{EventID: "e1", Text: "secret"}, recipients)
	if err != nil || duplicate {
		t.Fatalf("first Accept() duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = j.Accept("app", Event{EventID: "e1", Text: "changed"}, []journalRecipient{{SessionID: "new"}})
	if err != nil || !duplicate {
		t.Fatalf("duplicate Accept() duplicate=%v err=%v", duplicate, err)
	}
	if _, err := j.Accept("app", Event{EventID: "e2"}, recipients[:1]); err != nil {
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
}

func TestFileJournalBatchLimitsPreserveIngestOrder(t *testing.T) {
	j, _ := newTestJournal(t)
	recipient := []journalRecipient{{SessionID: "s", InstallationID: "i"}}
	for n := 0; n < 25; n++ {
		if _, err := j.Accept("count", Event{EventID: fmt.Sprintf("e%02d", n), Text: "x"}, recipient); err != nil {
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

	for n := 0; n < 10; n++ {
		if _, err := j.Accept("bytes", Event{EventID: fmt.Sprintf("b%02d", n), Text: strings.Repeat("x", 4000)}, recipient); err != nil {
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
