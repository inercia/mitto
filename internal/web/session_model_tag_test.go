package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
)

func modelTagTestSession() *conversation.BackgroundSession {
	return conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:   "socket-session",
		AgentModels: &conversation.SessionModelState{CurrentModelId: "sonnet", AvailableModels: []conversation.ModelInfo{{ModelId: "sonnet", Name: "Claude Sonnet 5"}}},
	})
}

func TestModelTagDispatchValidation(t *testing.T) {
	for _, tc := range []struct{ raw, status, tag, id string }{
		{``, "invalid_request", "", ""},
		{`null`, "invalid_request", "", ""},
		{`[]`, "invalid_request", "", ""},
		{`"Coding"`, "invalid_request", "", ""},
		{`42`, "invalid_request", "", ""},
		{`{`, "invalid_request", "", ""},
		{`{}`, "invalid_tag", "", ""},
		{`{"model_tag":null}`, "invalid_tag", "", ""},
		{`{"model_tag":""}`, "invalid_tag", "", ""},
		{`{"model_tag":" \t\n","request_id":" id "}`, "invalid_tag", "", " id "},
		{`{"model_tag":5}`, "invalid_request", "", ""},
		{`{"model_tag":true}`, "invalid_request", "", ""},
		{`{"model_tag":[]}`, "invalid_request", "", ""},
		{`{"model_tag":{}}`, "invalid_request", "", ""},
		{`{"model_tag":"Coding","request_id":1}`, "invalid_request", "", ""},
		{`{"model_tag":"Coding","request_id":false}`, "invalid_request", "", ""},
		{`{"model_tag":" Coding ","request_id":"opaque","session_id":"other","unknown":true}`, "already_active", "Coding", "opaque"},
		{`{"model_tag":"coding","request_id":null}`, "already_active", "coding", ""},
		{`{"model_tag":"custom  tag"}`, "no_match", "custom  tag", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			mock := newMockWSConn()
			bs := modelTagTestSession()
			client := &SessionWSClient{sessionID: "socket-session", bgSession: bs, wsConn: &WSConn{send: mock.send}}
			client.handleMessage(WSMessage{Type: WSMsgTypeApplyModelTag, Data: json.RawMessage(tc.raw)})
			var result ModelTagAppliedResult
			count := 0
			for len(mock.send) > 0 {
				var msg WSMessage
				if err := json.Unmarshal(<-mock.send, &msg); err != nil {
					t.Fatal(err)
				}
				if msg.Type == WSMsgTypeModelTagApplied {
					count++
					if err := json.Unmarshal(msg.Data, &result); err != nil {
						t.Fatal(err)
					}
					var fields map[string]interface{}
					_ = json.Unmarshal(msg.Data, &fields)
					for _, key := range []string{"session_id", "request_id", "model_tag", "status"} {
						if _, ok := fields[key]; !ok {
							t.Fatalf("missing %s", key)
						}
					}
					if tc.status == "already_active" {
						if fields["model_id"] != "sonnet" || fields["message"] != nil {
							t.Fatalf("success fields: %s", msg.Data)
						}
					} else if fields["model_id"] != nil || fields["message"] == nil {
						t.Fatalf("error fields: %s", msg.Data)
					}
				} else if msg.Type != conversation.WSMsgTypeConfigOptionChanged {
					t.Fatalf("duplicate or unexpected message: %s", msg.Type)
				}
			}
			if count != 1 || result.SessionID != "socket-session" || result.Status != tc.status || result.ModelTag != tc.tag || result.RequestID != tc.id {
				t.Fatalf("count=%d result=%+v", count, result)
			}
			if tc.status != "already_active" && bs.GetBaselineModel() != "" {
				t.Fatal("invalid/unmatched request mutated baseline")
			}
		})
	}
}

func TestModelTagDispatchMissingAndCatalog(t *testing.T) {
	closed := modelTagTestSession()
	closed.SimulateClose()
	for _, tc := range []struct {
		name   string
		bs     *conversation.BackgroundSession
		status string
	}{
		{"missing", nil, "missing_session"},
		{"closed", closed, "missing_session"},
		{"catalog", conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{}), "catalog_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockWSConn()
			client := &SessionWSClient{sessionID: "bound", bgSession: tc.bs, wsConn: &WSConn{send: mock.send}}
			client.handleMessage(WSMessage{Type: WSMsgTypeApplyModelTag, Data: json.RawMessage(`{"model_tag":"Coding"}`)})
			count := 0
			for len(mock.send) > 0 {
				var msg WSMessage
				_ = json.Unmarshal(<-mock.send, &msg)
				if msg.Type == WSMsgTypeModelTagApplied {
					count++
					var r ModelTagAppliedResult
					_ = json.Unmarshal(msg.Data, &r)
					if r.Status != tc.status {
						t.Fatalf("%+v", r)
					}
				}
			}
			if count != 1 {
				t.Fatalf("results = %d", count)
			}
		})
	}
}

func TestModelTagDispatchReattach(t *testing.T) {
	manager := conversation.NewSessionManager("", "", false, nil)
	bs := modelTagTestSession()
	manager.AddSessionForTest(bs)
	mock := newMockWSConn()
	client := &SessionWSClient{sessionID: "socket-session", server: &Server{sessionManager: manager}, wsConn: &WSConn{send: mock.send}}
	client.handleMessage(WSMessage{Type: WSMsgTypeApplyModelTag, Data: json.RawMessage(`{"model_tag":"Coding"}`)})
	count := 0
	for len(mock.send) > 0 {
		var msg WSMessage
		_ = json.Unmarshal(<-mock.send, &msg)
		if msg.Type == WSMsgTypeModelTagApplied {
			count++
			var r ModelTagAppliedResult
			_ = json.Unmarshal(msg.Data, &r)
			if r.Status != "already_active" {
				t.Fatalf("%+v", r)
			}
		}
	}
	if count != 1 || client.bgSession != bs {
		t.Fatal("did not attach to existing running session")
	}
}

func TestModelTagFailureLogsAndRefreshesCapturedSession(t *testing.T) {
	var logs bytes.Buffer
	mock := newMockWSConn()
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		AgentModels:   &conversation.SessionModelState{CurrentModelId: "other", AvailableModels: []conversation.ModelInfo{{ModelId: "sonnet", Name: "Claude Sonnet 5"}}},
		ConfigOptions: []conversation.SessionConfigOption{{ID: string(conversation.ModelConfigId), Category: conversation.ConfigOptionCategoryModel, CurrentValue: "other"}},
	})
	client := &SessionWSClient{sessionID: "bound", bgSession: bs, logger: slog.New(slog.NewTextHandler(&logs, nil)), wsConn: &WSConn{send: mock.send}}
	client.handleMessage(WSMessage{Type: WSMsgTypeApplyModelTag, Data: json.RawMessage(`{"model_tag":"Coding"}`)})
	if logs.Len() == 0 {
		t.Fatal("canonical failure was not logged")
	}
	var result ModelTagAppliedResult
	count, snapshots := 0, 0
	for len(mock.send) > 0 {
		var msg WSMessage
		_ = json.Unmarshal(<-mock.send, &msg)
		switch msg.Type {
		case WSMsgTypeModelTagApplied:
			count++
			_ = json.Unmarshal(msg.Data, &result)
		case conversation.WSMsgTypeConfigOptionChanged:
			snapshots++
		default:
			t.Fatalf("unexpected %s", msg.Type)
		}
	}
	if count != 1 || snapshots != 1 || result.Status != "internal_error" || result.Message != "Failed to apply model tag" {
		t.Fatalf("result=%+v count=%d snapshots=%d", result, count, snapshots)
	}
	// The extracted sender must not reread the detached pointer. Exercise the
	// captured snapshot concurrently with lifecycle pointer replacement.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			client.initialLoadMu.Lock()
			client.bgSession = nil
			client.initialLoadMu.Unlock()
		}
	}()
	for i := 0; i < 20; i++ {
		client.broadcastConfigSnapshotForSession(bs, string(conversation.ModelConfigId))
	}
	wg.Wait()
}

type fakeModelTagApplier struct {
	calls    int
	result   conversation.ModelTagApplyResult
	err      error
	deadline time.Time
	tag      string
}

func (f *fakeModelTagApplier) ApplyModelTagWithResult(ctx context.Context, tag string) (conversation.ModelTagApplyResult, error) {
	f.calls++
	f.tag = tag
	f.deadline, _ = ctx.Deadline()
	return f.result, f.err
}

func TestModelTagOutcomeMapping(t *testing.T) {
	for _, tc := range []struct {
		status, message string
		result          conversation.ModelTagApplyResult
		err             error
	}{
		{"applied", "", conversation.ModelTagApplyResult{ModelID: "chosen"}, nil},
		{"already_active", "", conversation.ModelTagApplyResult{ModelID: "chosen", AlreadyActive: true}, nil},
		{"no_match", "No available model matches this tag", conversation.ModelTagApplyResult{}, fmt.Errorf("wrapped: %w", conversation.ErrModelTagNoMatch)},
		{"catalog_unavailable", "Agent model catalog is unavailable", conversation.ModelTagApplyResult{}, fmt.Errorf("wrapped: %w", conversation.ErrModelTagCatalogUnavailable)},
		{"internal_error", "Failed to apply model tag", conversation.ModelTagApplyResult{}, errors.New("secret server path")},
		{"internal_error", "Failed to apply model tag", conversation.ModelTagApplyResult{}, context.DeadlineExceeded},
	} {
		t.Run(tc.status, func(t *testing.T) {
			fake := &fakeModelTagApplier{result: tc.result, err: tc.err}
			ctx, cancel := context.WithTimeout(context.Background(), modelConfigOptionCallerBudget)
			defer cancel()
			r := ModelTagAppliedResult{ModelTag: "Coding"}
			err := applyModelTag(ctx, fake, &r)
			if !errors.Is(err, tc.err) || fake.calls != 1 || fake.tag != "Coding" || r.Status != tc.status || r.Message != tc.message || r.ModelID != tc.result.ModelID {
				t.Fatalf("result=%+v calls=%d err=%v", r, fake.calls, err)
			}
			if remaining := time.Until(fake.deadline); remaining < 89*time.Second || remaining > 90*time.Second {
				t.Fatalf("budget=%s", remaining)
			}
		})
	}
}
