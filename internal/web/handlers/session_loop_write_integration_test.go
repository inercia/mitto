package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The only fake boundary is the agent transport. Everything between a rendered
// Play button, SDK HTTP, MCP update, persistence, runner admission and final
// outgoing prompt is production code. Channels hold completions, not clocks.
type integratedLoopTransport struct {
	conversation.SharedProcess // unused methods must not be called by this seam
	calls                      chan integratedLoopCall
	done                       chan struct{}
}

type integratedLoopCall struct {
	text   string
	finish chan error
}

func (p *integratedLoopTransport) Capabilities() agentbackend.Capabilities   { return nil }
func (p *integratedLoopTransport) ProcessDone() <-chan struct{}              { return p.done }
func (p *integratedLoopTransport) Done() <-chan struct{}                     { return p.done }
func (p *integratedLoopTransport) MCPInitDone() bool                         { return true }
func (p *integratedLoopTransport) Generation() int                           { return 0 }
func (p *integratedLoopTransport) RecommendedLoadTimeout(bool) time.Duration { return 0 }
func (p *integratedLoopTransport) NewSession(context.Context, string, []agentbackend.MCPServerDescriptor) (*conversation.SessionHandle, error) {
	return &conversation.SessionHandle{SessionID: "integrated-agent"}, nil
}
func (p *integratedLoopTransport) RegisterSession(string, *conversation.SessionCallbacks) {}
func (p *integratedLoopTransport) UnregisterSession(string)                               {}
func (p *integratedLoopTransport) Cancel(context.Context, string) error                   { return nil }
func (p *integratedLoopTransport) Prompt(ctx context.Context, _ string, blocks []agentbackend.ContentBlock) (agentbackend.PromptOutcome, error) {
	var text strings.Builder
	for _, b := range blocks {
		if b.Text != nil {
			text.WriteString(b.Text.Text)
		}
	}
	c := integratedLoopCall{text: text.String(), finish: make(chan error, 1)}
	select {
	case p.calls <- c:
	case <-ctx.Done():
		return agentbackend.PromptOutcome{}, ctx.Err()
	}
	select {
	case err := <-c.finish:
		return agentbackend.PromptOutcome{}, err
	case <-ctx.Done():
		return agentbackend.PromptOutcome{}, ctx.Err()
	}
}

type integratedBindingTransport struct{ binding mcpserver.SessionHTTPBinding }

func (tr integratedBindingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(tr.binding.HeaderName, tr.binding.HeaderValue)
	return http.DefaultTransport.RoundTrip(r)
}

func TestLoopRestartIntegratedPlay(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Fatal("integrated Play proof requires bun: ", err)
	}
	for _, surface := range []string{"compact", "panel"} {
		for _, kind := range []string{"duration", "iterations", "global", "remaining", "stale", "bootstrap"} {
			t.Run(surface+"/"+kind, func(t *testing.T) { testLoopRestartIntegratedPlay(t, surface, kind) })
		}
	}
}

func testLoopRestartIntegratedPlay(t *testing.T, surface, kind string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ms, err := mcpserver.NewServer(mcpserver.Config{Port: 0}, mcpserver.Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer ms.Stop()
	proc := &integratedLoopTransport{calls: make(chan integratedLoopCall, 4), done: make(chan struct{})}
	bs, err := conversation.NewBackgroundSession(conversation.BackgroundSessionConfig{PersistedID: "integrated-play", ACPServer: "test", WorkingDir: t.TempDir(), Store: store, SharedProcess: proc, GlobalMCPServer: ms})
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close("test")
	id := bs.GetSessionID()
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	sm.AddSessionForTest(bs)
	r := conversation.NewLoopRunner(store, sm, nil)
	const cap = 3
	r.SetMaxLoopIterations(cap)
	ls := store.Loop(id)
	p := &session.LoopPrompt{Prompt: "preserve eligible task", Arguments: map[string]string{"TASK": "preserved"}, Enabled: true, Triggers: []session.LoopTrigger{session.TriggerOnCompletion, session.TriggerOnTasks}, DelaySeconds: 30, MaxIterations: cap, MaxDurationSeconds: 14400}
	if err := ls.Set(p); err != nil {
		t.Fatal(err)
	}
	// Capture a genuine old runner callback before its allowance expires.
	var old integratedLoopCall
	if kind != "bootstrap" {
		if err := r.TriggerNow(id, true); err != nil {
			t.Fatal(err)
		}
		old = integratedNextCall(t, ctx, proc)
	}
	anchor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	p, _ = ls.Get()
	p.IterationCount = 2
	p.FirstRunAt = &anchor
	p.LastSentAt = &anchor
	switch kind {
	case "duration", "bootstrap":
		p.MaxDurationSeconds = 60
	case "iterations":
		p.IterationCount = cap
		p.MaxDurationSeconds = 0
	case "global":
		p.MaxIterations = 0
		p.IterationCount = config.GlobalMaxLoopIterations
		p.MaxDurationSeconds = 0
	default:
		p.FirstRunAt = nil
		p.MaxDurationSeconds = 0
	}
	if err := ls.RestoreSnapshot(p); err != nil {
		t.Fatal(err)
	}
	reason := p.ExhaustedReason(time.Now(), config.EffectiveMaxLoopIterations(p.MaxIterations, cap))
	if reason != "" {
		if err := ls.MarkStopped(reason); err != nil {
			t.Fatal(err)
		}
	}
	// Real bound MCP request, not a copied handler or direct store disable.
	binding, ok := ms.SessionHTTPBinding(id)
	if !ok {
		t.Fatal("no authenticated MCP binding")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "integrated-loop", Version: "1"}, nil)
	mcs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: binding.URL, HTTPClient: &http.Client{Transport: integratedBindingTransport{binding}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mcs.Close()
	res, err := mcs.CallTool(ctx, &mcp.CallToolParams{Name: "mitto_conversation_update", Arguments: map[string]any{"self_id": id, "conversation_id": "self", "loop_enabled": false}})
	if err != nil || res.IsError {
		t.Fatalf("MCP disable: %+v, %v", res, err)
	}
	encoded, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(encoded), `"success":true`) {
		t.Fatalf("MCP disable rejected: %s", encoded)
	}
	paused, _ := ls.Get()
	if reason != "" && paused.StoppedReason != reason {
		t.Fatalf("MCP overwrote %s with %s", reason, paused.StoppedReason)
	}
	if paused.Enabled {
		t.Fatal("MCP did not disable")
	}
	history, err := store.ReadEvents(id)
	if err != nil || len(history) == 0 {
		t.Fatalf("missing pre-restart history: %v", err)
	}
	// Invalid binding credentials are rejected by the real MCP middleware and
	// must leave disk intact. A valid binding itself owns self identity.
	beforeAuth, _ := json.Marshal(paused)
	badRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, binding.URL, strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"mitto_conversation_update","arguments":{"self_id":"integrated-play","conversation_id":"self","loop_enabled":true}}}`))
	badRequest.Header.Set(binding.HeaderName, "invalid-test-binding")
	badRequest.Header.Set("Content-Type", "application/json")
	denied, err := http.DefaultClient.Do(badRequest)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid binding status=%d", denied.StatusCode)
	}
	afterAuth, _ := ls.Get()
	afterAuthJSON, _ := json.Marshal(afterAuth)
	if string(beforeAuth) != string(afterAuthJSON) {
		t.Fatal("denied MCP update mutated loop")
	}
	var patches, posts, rejected atomic.Int32
	bootstrap := r.BootstrapOnCompletion
	if kind == "bootstrap" {
		bootstrap = func(id string) {
			// At the existing post-persistence seam, race actual runner bootstrap
			// and run-now entrypoints, rather than replacing admission with a mock.
			start := make(chan struct{})
			var wg sync.WaitGroup
			for _, fire := range []func(){func() { r.BootstrapOnCompletion(id) }, func() {
				err := r.TriggerNow(id, true)
				if err != nil && !errors.Is(err, conversation.ErrSessionBusy) && !errors.Is(err, conversation.ErrLoopDispatchCoalesced) {
					t.Errorf("racing run-now: %v", err)
				}
			}} {
				wg.Add(1)
				go func(f func()) { defer wg.Done(); <-start; f() }(fire)
			}
			close(start)
			wg.Wait()
		}
	}
	h := New(Deps{Store: store, LoopMaxIterations: r.MaxLoopIterations, BootstrapOnCompletion: bootstrap, TriggerLoopNow: r.TriggerNow, ErrSessionBusy: conversation.ErrSessionBusy, ErrLoopNotEnabled: conversation.ErrLoopNotEnabled})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/sessions/"+id+"/loop") && !strings.HasSuffix(req.URL.Path, "/sessions/"+id+"/loop/run-now") {
			http.NotFound(w, req)
			return
		}
		if req.Method == http.MethodPatch {
			patches.Add(1)
			if kind == "stale" && patches.Load() == 1 {
				// Time/counters change after the real GET, with no display-reason hint.
				latest, _ := store.Loop(id).Get()
				latest.IterationCount = cap
				if err := store.Loop(id).RestoreSnapshot(latest); err != nil {
					t.Error(err)
				}
			}
		}
		if req.Method == http.MethodPost {
			posts.Add(1)
			h.handleRunLoopNow(w, req, id)
			return
		}
		capture := httptest.NewRecorder()
		h.HandleSessionLoop(capture, req, id, "")
		if capture.Code == http.StatusConflict {
			rejected.Add(1)
		}
		for k, v := range capture.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(capture.Code)
		_, _ = w.Write(capture.Body.Bytes())
	}))
	defer server.Close()
	root, _ := filepath.Abs("../../..")
	cmd := exec.CommandContext(ctx, "bun", "test", "web/static/components/LoopPlay.integration.test.js")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MITTO_LOOP_INTEGRATION_URL="+server.URL, "MITTO_LOOP_INTEGRATION_SURFACE="+surface, "MITTO_LOOP_INTEGRATION_KIND="+kind)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real Play: %v\n%s", err, out)
	}
	wantPatches := int32(1)
	if kind == "stale" {
		wantPatches = 2
	}
	if patches.Load() != wantPatches || posts.Load() != 1 || (kind == "stale" && rejected.Load() != 1) {
		t.Fatalf("requests patches=%d posts=%d rejected=%d", patches.Load(), posts.Load(), rejected.Load())
	}
	fresh, _ := ls.Get()
	if kind == "remaining" {
		if fresh.LifecycleGeneration != 0 || fresh.IterationCount != 2 || fresh.PendingRestart {
			t.Fatalf("resume reset budget: %+v", fresh)
		}
	} else if fresh.LifecycleGeneration != 1 || fresh.IterationCount != 0 || fresh.FirstRunAt != nil || fresh.LastSentAt != nil || !fresh.PendingRestart || !fresh.Enabled {
		t.Fatalf("non-atomic restart: %+v", fresh)
	}
	if fresh.Prompt != p.Prompt || !reflect.DeepEqual(fresh.Arguments, p.Arguments) || !reflect.DeepEqual(fresh.Triggers, p.Triggers) || fresh.MaxIterations != p.MaxIterations || fresh.MaxDurationSeconds != p.MaxDurationSeconds {
		t.Fatal("restart changed task/config")
	}
	afterHistory, err := store.ReadEvents(id)
	if err != nil || len(afterHistory) < len(history) || !reflect.DeepEqual(afterHistory[:len(history)], history) {
		t.Fatal("Play removed or rewrote persisted history")
	}
	var next integratedLoopCall
	if kind == "bootstrap" {
		next = integratedNextCall(t, ctx, proc)
		select {
		case <-proc.calls:
			t.Fatal("bootstrap/run-now dispatched twice")
		default:
		}
	} else {
		select {
		case <-proc.calls:
			t.Fatal("bootstrap/run-now duplicated blocked old delivery")
		default:
		}
		// Complete the OLD transport after restart: it must neither charge nor stop
		// the new generation. For ordinary resume it still belongs to that budget.
		if kind == "iterations" {
			old.finish <- nil
		} else {
			old.finish <- errors.New("old transport failed")
		}
		integratedTrigger(t, ctx, r, id)
		next = integratedNextCall(t, ctx, proc)
	}
	if kind != "remaining" {
		afterOld, _ := ls.Get()
		if afterOld.IterationCount != 0 || !afterOld.Enabled || !afterOld.PendingRestart || afterOld.StoppedReason != "" {
			t.Fatalf("old callback changed new lifecycle: %+v", afterOld)
		}
		if !strings.Contains(next.text, "<mitto_loop_restart>") || !strings.Contains(next.text, "prior budget") || !strings.Contains(next.text, "preserve eligible task") {
			t.Fatalf("missing actual fresh context: %s", next.text)
		}
		for _, field := range []string{"budget generation 1", "Current iteration index: 0 (zero-based)", "effective iteration limit: " + strconv.Itoa(config.EffectiveMaxLoopIterations(fresh.MaxIterations, cap))} {
			if !strings.Contains(next.text, field) {
				t.Fatalf("non-authoritative outgoing context, missing %q: %s", field, next.text)
			}
		}
		if fresh.MaxDurationSeconds > 0 && !strings.Contains(next.text, "fresh, not yet anchored") {
			t.Fatal("unanchored budget fabricated elapsed duration")
		}
		// Failed delivery must retain the persisted marker across a runner reload.
		next.finish <- errors.New("fresh delivery failed")
		r = conversation.NewLoopRunner(store, sm, nil)
		r.SetMaxLoopIterations(cap)
		// The BackgroundSession still excludes the old transport during reload.
		integratedTrigger(t, ctx, r, id)
		next = integratedNextCall(t, ctx, proc)
		if !strings.Contains(next.text, "<mitto_loop_restart>") {
			t.Fatal("reload lost fresh notice")
		}
	} else if strings.Contains(next.text, "<mitto_loop_restart>") {
		t.Fatal("ordinary resume announces restart")
	}
	next.finish <- nil
	integratedWait(t, ctx, func() bool {
		q, _ := ls.Get()
		return q != nil && !q.PendingRestart && q.IterationCount == fresh.IterationCount+1
	})
	if kind != "remaining" {
		integratedTrigger(t, ctx, r, id)
		ordinary := integratedNextCall(t, ctx, proc)
		if strings.Contains(ordinary.text, "<mitto_loop_restart>") {
			t.Fatal("ordinary later delivery repeats restart notice")
		}
		ordinary.finish <- nil
		integratedWait(t, ctx, func() bool { q, _ := ls.Get(); return q != nil && q.IterationCount == 2 })
	}
}

func integratedNextCall(t *testing.T, ctx context.Context, p *integratedLoopTransport) integratedLoopCall {
	t.Helper()
	select {
	case c := <-p.calls:
		return c
	case <-ctx.Done():
		t.Fatal("agent transport did not receive prompt: ", ctx.Err())
		return integratedLoopCall{}
	}
}
func integratedWait(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			runtime.Gosched()
		}
	}
}
func integratedTrigger(t *testing.T, ctx context.Context, r *conversation.LoopRunner, id string) {
	t.Helper()
	integratedWait(t, ctx, func() bool {
		err := r.TriggerNow(id, true)
		if err == nil {
			return true
		}
		if !errors.Is(err, conversation.ErrSessionBusy) && !errors.Is(err, conversation.ErrLoopDispatchCoalesced) {
			t.Fatalf("retry admission: %v", err)
		}
		return false
	})
}

// Keep the test's bound transport type checked if the SDK changes its surface.
var _ http.RoundTripper = integratedBindingTransport{}
