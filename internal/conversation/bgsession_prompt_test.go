package conversation

import (
	"strings"
	"testing"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

func TestBuildArgumentMetadata_Basic(t *testing.T) {
	names, arguments := buildArgumentMetadata(map[string]string{
		"greeting": "hello",
		"name":     "world",
	})

	// Sorted order: greeting, name
	if len(names) != 2 || names[0] != "greeting" || names[1] != "name" {
		t.Fatalf("unexpected names: %v", names)
	}
	if len(arguments) != 2 {
		t.Fatalf("unexpected arguments length: %d", len(arguments))
	}
	if arguments[0]["name"] != "greeting" || arguments[0]["value"] != "hello" {
		t.Errorf("unexpected first entry: %v", arguments[0])
	}
	if arguments[1]["name"] != "name" || arguments[1]["value"] != "world" {
		t.Errorf("unexpected second entry: %v", arguments[1])
	}
}

func TestBuildArgumentMetadata_SortedOrderMatchesNames(t *testing.T) {
	args := map[string]string{"z": "last", "a": "first", "m": "middle"}
	names, arguments := buildArgumentMetadata(args)

	for i, n := range names {
		if arguments[i]["name"] != n {
			t.Errorf("index %d: names[%d]=%q but arguments[%d][name]=%v", i, i, n, i, arguments[i]["name"])
		}
	}
}

func TestBuildArgumentMetadata_Truncation(t *testing.T) {
	longValue := strings.Repeat("x", 100)
	names, arguments := buildArgumentMetadata(map[string]string{"key": longValue})

	if len(names) != 1 || names[0] != "key" {
		t.Fatalf("unexpected names: %v", names)
	}
	val, ok := arguments[0]["value"].(string)
	if !ok {
		t.Fatalf("value is not a string: %T", arguments[0]["value"])
	}
	runes := []rune(val)
	// Truncated to 80 runes + 1 ellipsis rune = 81 runes
	if len(runes) != maxArgValueLen+1 {
		t.Errorf("truncated value has %d runes, want %d", len(runes), maxArgValueLen+1)
	}
	if !strings.HasSuffix(val, "…") {
		t.Errorf("truncated value missing ellipsis suffix: %q", val)
	}
}

func TestBuildArgumentMetadata_NoTruncationAtExactLimit(t *testing.T) {
	exactValue := strings.Repeat("y", maxArgValueLen)
	_, arguments := buildArgumentMetadata(map[string]string{"k": exactValue})

	val, _ := arguments[0]["value"].(string)
	if val != exactValue {
		t.Errorf("value at exact limit should be unmodified; got %q", val)
	}
}

func TestBuildArgumentMetadata_RedactionSensitiveNames(t *testing.T) {
	sensitiveNames := []string{
		"my_password", "MY_TOKEN", "api_key", "apikey", "secret",
		"ACCESS_KEY", "auth_key", "private_key", "credentials", "passwd",
	}
	for _, sn := range sensitiveNames {
		_, arguments := buildArgumentMetadata(map[string]string{sn: "super-secret-value"})
		val, _ := arguments[0]["value"].(string)
		if val != "***" {
			t.Errorf("name %q: expected redacted value \"***\", got %q", sn, val)
		}
	}
}

func TestBuildArgumentMetadata_NonSensitiveNamesNotRedacted(t *testing.T) {
	_, arguments := buildArgumentMetadata(map[string]string{"greeting": "hello world"})
	val, _ := arguments[0]["value"].(string)
	if val != "hello world" {
		t.Errorf("non-sensitive name: unexpected value %q", val)
	}
}

func TestBuildArgumentMetadata_Empty(t *testing.T) {
	names, arguments := buildArgumentMetadata(map[string]string{})
	if len(names) != 0 || len(arguments) != 0 {
		t.Errorf("expected empty slices for empty input; got names=%v arguments=%v", names, arguments)
	}
}

func TestPersistableArguments_Empty(t *testing.T) {
	if got := persistableArguments(nil); got != nil {
		t.Errorf("persistableArguments(nil) = %v, want nil", got)
	}
	if got := persistableArguments(map[string]string{}); got != nil {
		t.Errorf("persistableArguments(empty map) = %v, want nil", got)
	}
}

func TestPersistableArguments_ExactRoundTripNoRedaction(t *testing.T) {
	// Values are NOT truncated or redacted, unlike buildArgumentMetadata/redactArgValue —
	// they must be exactly replayable for retry.
	longValue := strings.Repeat("x", maxArgValueLen+20)
	args := map[string]string{"IssueID": "mitto-123", "Notes": longValue}
	got := persistableArguments(args)
	if got["IssueID"] != "mitto-123" {
		t.Errorf("IssueID = %q, want unmodified %q", got["IssueID"], "mitto-123")
	}
	if got["Notes"] != longValue {
		t.Errorf("Notes was modified; got %d runes, want %d runes untruncated", len([]rune(got["Notes"])), len([]rune(longValue)))
	}
}

func TestPersistableArguments_OmitsSensitiveKeysEntirely(t *testing.T) {
	args := map[string]string{
		"IssueID":  "mitto-123",
		"password": "hunter2",
		"apikey":   "sk-live-abc",
	}
	got := persistableArguments(args)
	if _, ok := got["password"]; ok {
		t.Errorf("sensitive key %q must be entirely absent, got %v", "password", got)
	}
	if _, ok := got["apikey"]; ok {
		t.Errorf("sensitive key %q must be entirely absent, got %v", "apikey", got)
	}
	if got["IssueID"] != "mitto-123" {
		t.Errorf("non-sensitive key IssueID = %q, want %q", got["IssueID"], "mitto-123")
	}
	// No "***" placeholder anywhere — the key is absent, not redacted in place.
	for _, v := range got {
		if v == "***" {
			t.Errorf("found a \"***\" placeholder value; persistableArguments must omit, not redact")
		}
	}
}

func TestPersistableArguments_AllSensitiveReturnsNil(t *testing.T) {
	args := map[string]string{"password": "hunter2", "secret": "shh"}
	if got := persistableArguments(args); got != nil {
		t.Errorf("persistableArguments(all sensitive) = %v, want nil", got)
	}
}

func TestRedactArgValue_Truncation(t *testing.T) {
	// Unicode-safe: 80 runes of multi-byte content
	unicodeVal := strings.Repeat("é", 90)
	result := redactArgValue("safe", unicodeVal)
	runes := []rune(result)
	if len(runes) != maxArgValueLen+1 {
		t.Errorf("expected %d runes (80 + ellipsis), got %d", maxArgValueLen+1, len(runes))
	}
}

// mcpInitFlagSharedProcess is a minimal SharedProcess stub (embeds
// alwaysFailSharedProcess, defined in background_session_test.go, for the
// other 14 interface methods) with a configurable MCPInitDone() return
// value, used to exercise pdSharedProcessHistory's cold/warm mapping
// (mitto-azk).
type mcpInitFlagSharedProcess struct {
	alwaysFailSharedProcess
	mcpInitDone bool
}

func (p *mcpInitFlagSharedProcess) MCPInitDone() bool { return p.mcpInitDone }

// TestBackgroundSession_PdSharedProcessHistory verifies the three-way mapping
// from BackgroundSession.sharedProcess state to mittoAcp.ProcessHistory
// (mitto-azk): no shared process -> Unknown (legacy per-session process
// ownership, no corroborating signal); a shared process that has completed
// at least one session RPC -> Warm; one that has not -> Cold.
func TestBackgroundSession_PdSharedProcessHistory(t *testing.T) {
	t.Run("no shared process returns Unknown", func(t *testing.T) {
		bs := &BackgroundSession{}
		if got := bs.pdSharedProcessHistory(); got != mittoAcp.ProcessHistoryUnknown {
			t.Errorf("pdSharedProcessHistory() = %v, want ProcessHistoryUnknown", got)
		}
	})

	t.Run("shared process with MCPInitDone=true returns Warm", func(t *testing.T) {
		bs := &BackgroundSession{sharedProcess: &mcpInitFlagSharedProcess{mcpInitDone: true}}
		if got := bs.pdSharedProcessHistory(); got != mittoAcp.ProcessHistoryWarm {
			t.Errorf("pdSharedProcessHistory() = %v, want ProcessHistoryWarm", got)
		}
	})

	t.Run("shared process with MCPInitDone=false returns Cold", func(t *testing.T) {
		bs := &BackgroundSession{sharedProcess: &mcpInitFlagSharedProcess{mcpInitDone: false}}
		if got := bs.pdSharedProcessHistory(); got != mittoAcp.ProcessHistoryCold {
			t.Errorf("pdSharedProcessHistory() = %v, want ProcessHistoryCold", got)
		}
	})
}

// TestLoopContinuation_Marker tests the peek/advance/reset lifecycle of the
// session-scoped loop continuation marker (mitto-5xjn).
func TestLoopContinuation_Marker(t *testing.T) {
	newBS := func() *BackgroundSession {
		bs := &BackgroundSession{}
		return bs
	}

	// (i) First scheduled run → peek returns false (no previous run recorded).
	t.Run("first-scheduled-peek-false", func(t *testing.T) {
		bs := newBS()
		if got := bs.peekLoopContinuation(true); got {
			t.Error("first scheduled run: peekLoopContinuation(true) should return false, got true")
		}
	})

	// (ii) Two back-to-back scheduled runs: advance true → next peek true.
	t.Run("back-to-back-scheduled", func(t *testing.T) {
		bs := newBS()
		bs.advanceLoopContinuation(true) // first run committed
		if got := bs.peekLoopContinuation(true); !got {
			t.Error("second scheduled run: peekLoopContinuation(true) should return true after advance(true)")
		}
	})

	// (iii) A user/non-scheduled dispatch between two scheduled runs resets the chain.
	t.Run("non-scheduled-breaks-chain", func(t *testing.T) {
		bs := newBS()
		bs.advanceLoopContinuation(true)  // scheduled run 1
		bs.advanceLoopContinuation(false) // user prompt (non-scheduled)
		if got := bs.peekLoopContinuation(true); got {
			t.Error("after non-scheduled advance(false): peekLoopContinuation(true) should return false")
		}
	})

	// (iv) Forced loop run (isScheduledLoop=false) → peek false and resets chain.
	t.Run("forced-run-breaks-chain", func(t *testing.T) {
		bs := newBS()
		bs.advanceLoopContinuation(true)  // scheduled run 1
		bs.advanceLoopContinuation(false) // forced run (LoopKindForced → isScheduledLoop=false)
		if got := bs.peekLoopContinuation(true); got {
			t.Error("after forced advance(false): peekLoopContinuation(true) should return false")
		}
		// peek with false also returns false
		if got := bs.peekLoopContinuation(false); got {
			t.Error("peekLoopContinuation(false) should always return false")
		}
	})

	// (v) FreshContext → isScheduledLoop is computed as false → peek false.
	t.Run("fresh-context-peek-false", func(t *testing.T) {
		bs := newBS()
		bs.advanceLoopContinuation(true)
		// FreshContext makes isScheduledLoop=false regardless of LoopKindScheduled
		isScheduledLoop := false // LoopKindScheduled && !FreshContext → false when FreshContext=true
		if got := bs.peekLoopContinuation(isScheduledLoop); got {
			t.Error("FreshContext: peekLoopContinuation(false) should return false")
		}
	})

	// (vi) ResetLoopContinuation makes the next peek false even after advance(true).
	t.Run("reset-makes-next-peek-false", func(t *testing.T) {
		bs := newBS()
		bs.advanceLoopContinuation(true)
		bs.ResetLoopContinuation()
		if got := bs.peekLoopContinuation(true); got {
			t.Error("after ResetLoopContinuation: peekLoopContinuation(true) should return false")
		}
	})
}
