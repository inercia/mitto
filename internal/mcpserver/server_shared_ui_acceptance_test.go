package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// blockingPrompter records the prompts it receives and blocks until released.
type blockingPrompter struct {
	name    string
	arrived chan UIPromptRequest
	release chan struct{}
	mu      sync.Mutex
	got     []UIPromptRequest
}

func newBlockingPrompter(name string) *blockingPrompter {
	return &blockingPrompter{name: name, arrived: make(chan UIPromptRequest, 4), release: make(chan struct{})}
}

func (p *blockingPrompter) UIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	p.mu.Lock()
	p.got = append(p.got, req)
	p.mu.Unlock()
	p.arrived <- req
	select {
	case <-p.release:
	case <-ctx.Done():
		return UIPromptResponse{}, ctx.Err()
	}
	return UIPromptResponse{RequestID: req.RequestID, OptionID: "0", Label: p.name}, nil
}
func (p *blockingPrompter) DismissPrompt(string)           {}
func (p *blockingPrompter) UINotify(UINotifyRequest) error { return nil }

type secretRoundTripper struct{ secret string }

func (rt secretRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(SharedMCPHeaderName, rt.secret)
	return http.DefaultTransport.RoundTrip(r)
}

// Two conversations share ONE MCP protocol session over real Streamable HTTP
// (through the production request middleware). Per-call
// _meta["ai.opencode/sessionID"] must route each mitto_ui_options prompt to
// only its own conversation, and the protocol session must stay ownerless.
func TestSharedMCPSession_UIOptionsRoutePerConversation(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	convA, convB := "20261014-000000-aaaaaaaa", "20261014-000000-bbbbbbbb"
	pa, pb := newBlockingPrompter("A"), newBlockingPrompter("B")
	for id, p := range map[string]*blockingPrompter{convA: pa, convB: pb} {
		if err := store.Create(session.Metadata{
			SessionID: id, Name: id, ACPServer: "opencode", WorkingDir: t.TempDir(),
			AdvancedSettings: map[string]bool{session.FlagCanPromptUser: true},
		}); err != nil {
			t.Fatal(err)
		}
		if err := srv.RegisterSession(id, p, slog.Default()); err != nil {
			t.Fatal(err)
		}
	}
	if !srv.BindACPSession(convA, "ses_A") || !srv.BindACPSession(convB, "ses_B") {
		t.Fatal("BindACPSession failed")
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv.mcpServer }, mcpStreamableHTTPOptions())
	ts := httptest.NewServer(srv.mcpRequestLoggingMiddleware(handler))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: secretRoundTripper{secret: srv.SharedMCPSecret()}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	type result struct {
		label string
		err   error
	}
	results := make(chan result, 2)
	call := func(acpID, question string) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Meta: mcp.Meta{OpenCodeCallerMetaKey: acpID},
			Name: "mitto_ui_options",
			Arguments: map[string]any{
				"self_id":  "init", // placeholder; attested identity must win
				"question": question,
				"options":  []map[string]any{{"label": "ok"}},
			},
		})
		if err == nil && res.IsError {
			err = context.Canceled
		}
		label := ""
		if err == nil {
			if m, ok := res.StructuredContent.(map[string]any); ok {
				label, _ = m["selected"].(string)
			}
		}
		results <- result{label, err}
	}
	go call("ses_A", "question for A")
	go call("ses_B", "question for B")

	wait := func(p *blockingPrompter) UIPromptRequest {
		select {
		case r := <-p.arrived:
			return r
		case <-time.After(10 * time.Second):
			t.Fatalf("prompter %s never received a prompt", p.name)
			return UIPromptRequest{}
		}
	}
	ra, rb := wait(pa), wait(pb)
	if ra.Question != "question for A" || rb.Question != "question for B" {
		t.Fatalf("misrouted prompts: A got %q, B got %q", ra.Question, rb.Question)
	}

	// Both calls are in flight on the same protocol session: it must be ownerless.
	if owner := srv.lookupMCPSession(cs.ID()); owner != "" {
		t.Fatalf("shared protocol session %q must not have an owner, got %q", cs.ID(), owner)
	}

	close(pa.release)
	close(pb.release)
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("tool call failed: %v", r.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("tool call did not return")
		}
	}
	for _, p := range []*blockingPrompter{pa, pb} {
		p.mu.Lock()
		n := len(p.got)
		p.mu.Unlock()
		if n != 1 {
			t.Fatalf("prompter %s received %d prompts, want exactly 1", p.name, n)
		}
	}
	if owner := srv.lookupMCPSession(cs.ID()); owner != "" {
		t.Fatalf("protocol session gained owner %q", owner)
	}
}
