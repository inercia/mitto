package conversation

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

type freshModelRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		SessionID  string             `json:"sessionId"`
		ConfigID   string             `json:"configId"`
		Value      string             `json:"value"`
		Cwd        string             `json:"cwd"`
		MCPServers []json.RawMessage  `json:"mcpServers"`
		Prompt     []acp.ContentBlock `json:"prompt"`
	} `json:"params"`
}

// Only the peer is fake: session creation, model selection and prompting travel
// through the real SDK connection. The test acknowledges each RPC explicitly.
type freshModelPeer struct {
	conn  net.Conn
	calls chan freshModelRequest
}

func newFreshModelSession(t *testing.T) (*BackgroundSession, *freshModelPeer) {
	t.Helper()
	bs, _ := newModelTurnSession(t)
	bs.sharedProcess = nil
	bs.cmSetBaselineAndClearOverride("manual")
	bs.cmSetCurrentModelID("manual")
	bs.cmUpdateConfigOptionValue(ConfigOptionCategoryModel, "manual")
	bs.modelConfigId = "old-model-selector"
	bs.markACPContextUnknown()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(session.Metadata{
		SessionID: bs.persistedID, ACPSessionID: bs.acpID, BaselineModel: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	bs.store = store
	client, server := net.Pipe()
	peer := &freshModelPeer{conn: server, calls: make(chan freshModelRequest, 16)}
	bs.acpConn = acp.NewClientSideConnection(&WebClient{}, client, client)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(peer.calls)
		decoder := json.NewDecoder(server)
		for {
			var call freshModelRequest
			if err := decoder.Decode(&call); err != nil {
				return
			}
			select {
			case peer.calls <- call:
			case <-bs.ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		bs.cancel()
		_ = client.Close()
		_ = server.Close()
		modelTurnReceive(t, done)
		modelTurnReceive(t, bs.acpConn.Done())
	})
	return bs, peer
}

func (p *freshModelPeer) expect(t *testing.T, method, id, model string) freshModelRequest {
	t.Helper()
	call := modelTurnReceive(t, p.calls)
	if call.Method != method || call.Params.SessionID != id || call.Params.Value != model {
		t.Fatalf("RPC = (%q, %q, %q), want (%q, %q, %q)",
			call.Method, call.Params.SessionID, call.Params.Value, method, id, model)
	}
	if model != "" && call.Params.ConfigID != string(ModelConfigId) {
		t.Fatalf("model RPC retained stale config ID %q", call.Params.ConfigID)
	}
	return call
}

func (p *freshModelPeer) respond(t *testing.T, call freshModelRequest, result any, rejection string) {
	t.Helper()
	response := map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result}
	if rejection != "" {
		delete(response, "result")
		response["error"] = map[string]any{"code": -32602, "message": rejection}
	}
	if err := p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(p.conn).Encode(response); err != nil {
		t.Fatal(err)
	}
}

func freshModelDispatch(t *testing.T, bs *BackgroundSession, preferred bool) <-chan error {
	t.Helper()
	complete := make(chan error, 1)
	meta := PromptMeta{FreshContext: true, OnComplete: func(err error) { complete <- err }}
	if preferred {
		meta.PreferredModels = []config.PromptPreferredModel{{ModelName: "Initial"}}
	}
	if err := bs.PromptWithMeta("fresh prompt", meta); err != nil {
		t.Fatal(err)
	}
	return complete
}

func freshModelCreate(t *testing.T, bs *BackgroundSession, peer *freshModelPeer, id string) freshModelRequest {
	t.Helper()
	call := peer.expect(t, "session/new", "", "")
	if call.Params.Cwd != bs.workingDir || call.Params.MCPServers == nil {
		t.Fatal("session/new must carry the working directory and a non-null MCP server list")
	}
	peer.respond(t, call, acp.NewSessionResponse{
		SessionId: acp.SessionId(id),
		Models: &acp.SessionModelState{
			CurrentModelId: "default",
			AvailableModels: []acp.ModelInfo{
				{ModelId: "default", Name: "Default"},
				{ModelId: "manual", Name: "Manual"},
				{ModelId: "initial", Name: "Initial"},
			},
		},
	}, "")
	baseline := peer.expect(t, "session/set_config_option", id, "manual")
	// The baseline RPC is deliberately still blocked: adoption and persistence
	// must precede its acknowledgement, not merely happen at turn completion.
	metadata, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatal(err)
	}
	if bs.acpID != id || metadata.ACPSessionID != id || metadata.BaselineModel != "manual" {
		t.Fatalf("fresh ID/baseline not adopted: active=%q persisted=%q baseline=%q",
			bs.acpID, metadata.ACPSessionID, metadata.BaselineModel)
	}
	return baseline
}

func TestFreshContext_DirectACPModelLifecycle(t *testing.T) {
	bs, peer := newFreshModelSession(t)
	modelOK := acp.SetSessionConfigOptionResponse{ConfigOptions: []acp.SessionConfigOption{}}
	for i, id := range []string{"fresh-1", "fresh-2"} {
		complete := freshModelDispatch(t, bs, i == 0)
		baseline := freshModelCreate(t, bs, peer, id)
		peer.respond(t, baseline, modelOK, "")
		if i == 0 {
			override := peer.expect(t, "session/set_config_option", id, "initial")
			peer.respond(t, override, modelOK, "")
		}
		prompt := peer.expect(t, "session/prompt", id, "")
		wantModel := "manual"
		if i == 0 {
			wantModel = "initial"
		}
		if bs.cmGetCurrentModelID() != wantModel || bs.GetBaselineModel() != "manual" {
			t.Fatalf("first prompt on %q used wrong active/baseline model", id)
		}
		if len(prompt.Params.Prompt) != 1 || prompt.Params.Prompt[0].Text == nil || prompt.Params.Prompt[0].Text.Text != "fresh prompt" {
			t.Fatalf("unexpected prompt blocks: %+v", prompt.Params.Prompt)
		}
		peer.respond(t, prompt, acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, "")
		if i == 0 {
			restore := peer.expect(t, "session/set_config_option", id, "manual")
			if !bs.IsPrompting() {
				t.Fatal("turn became idle before its baseline restore")
			}
			peer.respond(t, restore, modelOK, "")
		}
		if err := modelTurnReceive(t, complete); err != nil {
			t.Fatal(err)
		}
		modelTurnAssertIdle(t, bs, "manual")
		if bs.acpContextIsEmpty() {
			t.Fatal("dispatched prompt must make the next FreshContext create another session")
		}
	}
	select {
	case call := <-peer.calls:
		t.Fatalf("unexpected extra RPC: %s", call.Method)
	default:
	}
}

func TestFreshContext_DirectACPInitializationFailureAbortsPrompt(t *testing.T) {
	bs, peer := newFreshModelSession(t)
	complete := freshModelDispatch(t, bs, true)
	baseline := freshModelCreate(t, bs, peer, "fresh-rejected")
	peer.respond(t, baseline, nil, "baseline rejected")
	// Cleanup may restore the baseline, but must never dispatch the rejected turn
	// (nor apply its preference) on either the fresh session or the old one.
	restore := peer.expect(t, "session/set_config_option", "fresh-rejected", "manual")
	peer.respond(t, restore, acp.SetSessionConfigOptionResponse{ConfigOptions: []acp.SessionConfigOption{}}, "")
	if err := modelTurnReceive(t, complete); err == nil || !strings.Contains(err.Error(), "baseline rejected") {
		t.Fatalf("completion error = %v, want fresh-session initialization rejection", err)
	}
	modelTurnAssertIdle(t, bs, "manual")
	if bs.acpContextIsEmpty() {
		t.Fatal("failed fresh-session initialization must not mark context ready")
	}
	select {
	case call := <-peer.calls:
		t.Fatalf("initialization failure sent unexpected RPC: %s", call.Method)
	default:
	}
}
