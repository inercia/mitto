//go:build integration

package inprocess

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/pkg/api"
)

// The mock agent advertises a generic `thought_level` select with an opaque id that
// differs from Copilot's (`reasoning_effort`), so these tests also prove that Mitto
// never hardcodes the id or the levels (mitto-3wd).
const (
	mockEffortID      = "deliberation_depth"
	mockEffortSetLine = "set_config_option\t" + mockEffortID + "="
)

type effortSession struct {
	ts        *TestServer
	orderFile string
	id        string
	ws        *api.Session
	bs        *conversation.BackgroundSession
	mu        sync.Mutex
	completes int
	errs      []string
}

func (e *effortSession) completed() int { e.mu.Lock(); defer e.mu.Unlock(); return e.completes }
func (e *effortSession) errors() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.errs...)
}

// newEffortSession creates a conversation, connects, runs one prompt (session/new is
// deferred until the first prompt) and returns once the agent advertises the effort option.
func newEffortSession(t *testing.T, ts *TestServer, orderFile, name string) *effortSession {
	t.Helper()
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: name})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	e := &effortSession{ts: ts, orderFile: orderFile, id: sess.SessionID}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnPromptComplete: func(int) { e.mu.Lock(); e.completes++; e.mu.Unlock() },
		OnError:          func(m string) { e.mu.Lock(); e.errs = append(e.errs, m); e.mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	e.ws = ws
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	waitFor(t, 30*time.Second, func() bool { return e.completed() >= 1 }, "first turn complete")
	e.bs = ts.Server.GetSessionManager().GetSession(sess.SessionID)
	if e.bs == nil {
		t.Fatalf("no background session for %s", sess.SessionID)
	}
	waitFor(t, 10*time.Second, func() bool { return e.effort() != nil }, "agent-advertised effort option")
	return e
}

func (e *effortSession) option(id string) *conversation.SessionConfigOption {
	for _, o := range e.bs.ConfigOptions() {
		if o.ID == id {
			o := o
			return &o
		}
	}
	return nil
}

func (e *effortSession) effort() *conversation.SessionConfigOption { return e.option(mockEffortID) }

func (e *effortSession) effortValues() []string {
	o := e.effort()
	if o == nil {
		return nil
	}
	var vals []string
	for _, v := range o.Options {
		vals = append(vals, v.Value)
	}
	return vals
}

func (e *effortSession) set(t *testing.T, id, value string) {
	t.Helper()
	if err := e.bs.SetConfigOption(context.Background(), id, value); err != nil {
		t.Fatalf("SetConfigOption(%s=%s): %v", id, value, err)
	}
}

func (e *effortSession) prompt(t *testing.T, text string) {
	t.Helper()
	before := e.completed()
	if err := e.ws.SendPrompt(text); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	waitFor(t, 30*time.Second, func() bool { return e.completed() > before }, "turn complete: "+text)
}

// indexOfLine returns the index of the first order-file line with the prefix at or
// after start, or -1.
func indexOfLine(lines []string, prefix string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], prefix) {
			return i
		}
	}
	return -1
}

func hasValue(vals []string, v string) bool {
	for _, x := range vals {
		if x == v {
			return true
		}
	}
	return false
}

func TestThoughtLevel_AdvertisedAsGenericSelect(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-advertised")

	o := e.effort()
	if o.Category != "thought_level" {
		t.Errorf("category = %q, want thought_level", o.Category)
	}
	if o.CurrentValue != "standard" {
		t.Errorf("default = %q, want standard", o.CurrentValue)
	}
	if got := e.effortValues(); strings.Join(got, ",") != "shallow,standard,deep" {
		t.Errorf("values = %v", got)
	}
	// Legacy model option is still advertised alongside it.
	if am := e.bs.AgentModels(); am == nil || string(am.CurrentModelId) != "claude-sonnet-4-6" {
		t.Errorf("legacy model state changed: %+v", am)
	}
}

// An idle selection is acked via session/set_config_option (exact id/value) BEFORE
// the next session/prompt.
func TestThoughtLevel_IdleSelectionAckedBeforeNextPrompt(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-idle")

	e.set(t, mockEffortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return indexOfLine(readRPCOrder(t, order), mockEffortSetLine+"deep", 0) >= 0 }, "set_config_option deliberation_depth=deep")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "deep" }, "agent-confirmed deep")

	e.prompt(t, "NEXT_TURN marker")

	lines := readRPCOrder(t, order)
	set := indexOfLine(lines, mockEffortSetLine+"deep", 0)
	next := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, "NEXT_TURN") {
			next = i
		}
	}
	if set < 0 || next < 0 || set > next {
		t.Fatalf("expected set_config_option before NEXT_TURN prompt; lines=%q", lines)
	}
	if errs := e.errors(); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

// A change made during a long first turn does not touch that turn, and is acked
// before the next prompt (last write wins; superseded value never sent).
func TestThoughtLevel_DeferredDuringStreamingAppliesToNextTurn(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-deferred")

	if err := e.ws.SendPrompt("Simulate a slow response"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	waitFor(t, 15*time.Second, e.bs.IsPrompting, "slow turn prompting")
	e.set(t, mockEffortID, "shallow")
	e.set(t, mockEffortID, "deep")

	if indexOfLine(readRPCOrder(t, order), mockEffortSetLine, 0) >= 0 {
		t.Fatalf("set_config_option issued mid-turn; must be deferred: %q", readRPCOrder(t, order))
	}
	if !e.bs.IsPrompting() {
		t.Fatalf("slow turn ended before the deferral could be asserted")
	}
	if _, err := ts.Client.AddToQueue(e.id, "QUEUED_FOLLOWUP marker"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}
	waitFor(t, 30*time.Second, func() bool { return e.completed() >= 3 }, "slow + queued turns complete")

	lines := readRPCOrder(t, order)
	slow, queued := -1, -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, "slow response") {
			slow = i
		}
		if strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, "QUEUED_FOLLOWUP") {
			queued = i
		}
	}
	set := indexOfLine(lines, mockEffortSetLine+"deep", 0)
	if !(slow >= 0 && set > slow && queued > set) {
		t.Fatalf("want slow prompt < set(deep) < queued prompt; lines=%q", lines)
	}
	if indexOfLine(lines, mockEffortSetLine+"shallow", 0) >= 0 {
		t.Fatalf("superseded value was sent to the agent: %q", lines)
	}
	if got := e.effort().CurrentValue; got != "deep" {
		t.Errorf("effort = %q, want deep", got)
	}
}

// Two conversations keep independent selections.
func TestThoughtLevel_ConversationsAreIsolated(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	a := newEffortSession(t, ts, order, "effort-a")
	b := newEffortSession(t, ts, order, "effort-b")

	a.set(t, mockEffortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return a.effort().CurrentValue == "deep" }, "a=deep")
	b.set(t, mockEffortID, "shallow")
	waitFor(t, 10*time.Second, func() bool { return b.effort().CurrentValue == "shallow" }, "b=shallow")

	if got := a.effort().CurrentValue; got != "deep" {
		t.Errorf("a = %q, want deep (leaked from b?)", got)
	}
	if got := b.effort().CurrentValue; got != "shallow" {
		t.Errorf("b = %q, want shallow", got)
	}
}

// Replacement snapshots: switching to a model that drops the option removes it, and
// switching to a model with different allowed values replaces them.
func TestThoughtLevel_SnapshotReplacementOnModelChange(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-snapshot")

	e.set(t, mockEffortID, "shallow")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "shallow" }, "shallow")

	// Opus narrows the allowed values; the now-invalid "shallow" must not linger.
	e.set(t, string(conversation.ModelConfigId), "claude-opus-4-6")
	waitFor(t, 10*time.Second, func() bool {
		return strings.Join(e.effortValues(), ",") == "standard,deep"
	}, "opus values standard,deep")
	if cur := e.effort().CurrentValue; !hasValue(e.effortValues(), cur) {
		t.Errorf("current %q not among allowed %v", cur, e.effortValues())
	}

	// Haiku drops the option entirely (like Copilot's Haiku).
	e.set(t, string(conversation.ModelConfigId), "claude-haiku-4-5")
	waitFor(t, 10*time.Second, func() bool { return e.effort() == nil }, "effort option removed on haiku")

	// And it comes back with the full set on a model that supports it.
	e.set(t, string(conversation.ModelConfigId), "claude-sonnet-4-6")
	waitFor(t, 10*time.Second, func() bool {
		return strings.Join(e.effortValues(), ",") == "shallow,standard,deep"
	}, "effort option restored on sonnet")
}

// A setter rejection leaves the advertised snapshot unchanged and reports an error;
// the conversation keeps working and other values are still accepted.
func TestThoughtLevel_SetterRejectionKeepsSnapshot(t *testing.T) {
	t.Setenv("MOCK_REJECT_CONFIG_OPTION", mockEffortID+"=deep")
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-reject")

	if err := e.bs.SetConfigOption(context.Background(), mockEffortID, "deep"); err == nil {
		// Some paths report asynchronously; either way the snapshot must not change.
		t.Log("SetConfigOption returned nil; asserting snapshot unchanged")
	}
	time.Sleep(500 * time.Millisecond)
	if got := e.effort().CurrentValue; got != "standard" {
		t.Fatalf("effort = %q after rejected set, want unchanged standard", got)
	}
	if strings.Join(e.effortValues(), ",") != "shallow,standard,deep" {
		t.Errorf("allowed values changed after rejection: %v", e.effortValues())
	}

	e.set(t, mockEffortID, "shallow")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "shallow" }, "other values still accepted")
	e.prompt(t, "still works")
}

// Resume keeps a still-valid selection (the agent re-advertises the option).
func TestThoughtLevel_ResumeKeepsValidSelection(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-resume")

	e.set(t, mockEffortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "deep" }, "deep")
	e.prompt(t, "before restart")

	sm := ts.Server.GetSessionManager()
	meta, err := ts.Store.GetMetadata(e.id)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	sm.CloseSession(e.id, "test_resume_effort")
	bs, err := sm.ResumeSession(e.id, meta.Name, meta.WorkingDir)
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	e.bs = bs
	waitFor(t, 20*time.Second, func() bool {
		o := e.effort()
		return o != nil && hasValue(e.effortValues(), o.CurrentValue)
	}, "effort option valid after resume")
	if len(e.effortValues()) == 0 {
		t.Fatalf("no effort values after resume")
	}
}

// Legacy model/mode behavior is unchanged by the generic select support.
func TestThoughtLevel_LegacyModeAndModelUnchanged(t *testing.T) {
	ts, order := setupDeferredConfigServer(t)
	e := newEffortSession(t, ts, order, "effort-legacy")

	e.set(t, "mode", "architect")
	waitFor(t, 10*time.Second, func() bool { return e.bs.GetConfigValue("mode") == "architect" }, "mode architect")
	e.set(t, string(conversation.ModelConfigId), "claude-opus-4-6")
	waitFor(t, 10*time.Second, func() bool {
		am := e.bs.AgentModels()
		return am != nil && string(am.CurrentModelId) == "claude-opus-4-6"
	}, "model opus")
	if indexOfLine(readRPCOrder(t, order), "set_model\t", 0) < 0 {
		t.Errorf("model change did not use set_model: %q", readRPCOrder(t, order))
	}
	if indexOfLine(readRPCOrder(t, order), "set_mode\t", 0) < 0 {
		t.Errorf("mode change did not use set_mode: %q", readRPCOrder(t, order))
	}
}
