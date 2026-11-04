package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestModelFixture(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"flat", `{"initial_model":"terra","options":[{"value":"terra","name":"Terra"},{"value":"coding","name":"Coding"}]}`, true},
		{"grouped", `{"initial_model":"terra","options":[{"group":"g","name":"Group","options":[{"value":"terra","name":"Terra"},{"value":"coding","name":"Coding"}]}]}`, true},
		{"missing initial", `{"initial_model":"other","options":[{"value":"terra","name":"Terra"}]}`, false},
		{"mixed", `{"initial_model":"terra","options":[{"value":"terra","name":"Terra"},{"group":"g","name":"Group","options":[{"value":"coding","name":"Coding"}]}]}`, false},
		{"invalid json", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.json")
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			fixture, err := loadModelFixture(path)
			if (err == nil) != tc.valid {
				t.Fatalf("fixture error: %v", err)
			}
			if !tc.valid {
				return
			}
			wire, err := json.Marshal(fixture.Options)
			if err != nil {
				t.Fatal(err)
			}
			var options acp.SessionConfigSelectOptions
			if err := json.Unmarshal(wire, &options); err != nil {
				t.Fatalf("ACP options: %v", err)
			}
			if tc.name == "grouped" && options.Grouped == nil || tc.name == "flat" && options.Ungrouped == nil {
				t.Fatalf("wrong ACP options variant: %s", wire)
			}
			s := &MockACPServer{modelFixture: fixture, sessions: map[string]*SessionState{}}
			for _, id := range []string{"a", "b"} {
				s.sessions[id] = &SessionState{ConfigOptions: s.freshConfigOptionsWithModel(nil, s.initialModel())}
			}
			if applied, err := s.applyModelChange(JSONRPCRequest{}, "a", "coding", "set_model"); !applied || err != nil {
				t.Fatalf("applied=%v error=%v", applied, err)
			}
			if s.sessionModel("a") != "coding" || s.sessionModel("b") != "terra" {
				t.Fatal("model state is not session scoped")
			}
			if !s.validModel("terra") || s.validModel("unknown") {
				t.Fatal("model validation differs from fixture")
			}
		})
	}
}

func TestModelChangesAreSessionScoped(t *testing.T) {
	s := &MockACPServer{sessions: map[string]*SessionState{
		"a": {ConfigOptions: []SessionConfigOption{buildModelConfigOption(defaultModelId)}},
		"b": {ConfigOptions: []SessionConfigOption{buildModelConfigOption(defaultModelId)}},
	}}
	for _, tc := range []struct{ session, model, other string }{
		{"a", "claude-opus-4-6", "b"},
		{"b", "claude-haiku-4-5", "a"},
	} {
		before := s.sessionModel(tc.other)
		if applied, err := s.applyModelChange(JSONRPCRequest{}, tc.session, tc.model, "set_model"); !applied || err != nil {
			t.Fatalf("model change applied=%v error=%v", applied, err)
		}
		if got := s.sessionModel(tc.session); got != tc.model {
			t.Fatalf("session %s model=%q, want %q", tc.session, got, tc.model)
		}
		if got := s.sessionModel(tc.other); got != before {
			t.Fatalf("changing %s affected %s: %q -> %q", tc.session, tc.other, before, got)
		}
	}
}

func TestRejectedModelSwitchSendsOnlyError(t *testing.T) {
	for _, method := range []string{"session/set_model", "session/set_config_option"} {
		t.Run(method, func(t *testing.T) {
			for _, failFirst := range []bool{false, true} {
				var output bytes.Buffer
				s := &MockACPServer{writer: &output, sessions: map[string]*SessionState{
					"a": {ConfigOptions: []SessionConfigOption{buildModelConfigOption(defaultModelId)}},
				}}
				model := "not-advertised"
				if failFirst {
					s.setModelFailFirst = 1
					model = "claude-opus-4-6"
				}
				params, err := json.Marshal(map[string]string{"sessionId": "a", "modelId": model, "configId": "model", "value": model})
				if err != nil {
					t.Fatal(err)
				}
				req := JSONRPCRequest{ID: 1, Method: method, Params: params}
				if method == "session/set_model" {
					err = s.handleSetSessionModel(req)
				} else {
					err = s.handleSetSessionConfigOption(req)
				}
				if err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(&output)
				var response map[string]json.RawMessage
				if err := decoder.Decode(&response); err != nil || response["error"] == nil {
					t.Fatalf("expected error response, got %v, error %v", response, err)
				}
				if decoder.Decode(&response) == nil {
					t.Fatal("unexpected second response or notification")
				}
				if got := s.sessionModel("a"); got != defaultModelId {
					t.Fatalf("rejected switch changed model to %q", got)
				}
			}
		})
	}
}

func TestModelAttemptLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempts.jsonl")
	var output bytes.Buffer
	s := &MockACPServer{writer: &output, modelAttemptFile: path, forceLegacySetModel: true, sessions: map[string]*SessionState{
		"a": {ConfigOptions: []SessionConfigOption{buildModelConfigOption(defaultModelId)}},
	}}
	params := json.RawMessage(`{"sessionId":"a","modelId":"claude-opus-4-6"}`)
	if err := s.handleSetSessionModel(JSONRPCRequest{ID: 1, Params: params}); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	params = json.RawMessage(`{"sessionId":"a","configId":"model","value":"unknown"}`)
	if err := s.handleSetSessionConfigOption(JSONRPCRequest{ID: 2, Params: params}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for _, want := range []struct{ method, model, outcome string }{
		{"set_model", "claude-opus-4-6", "unsupported"},
		{"set_config_option", "unknown", "invalid"},
	} {
		var got map[string]string
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got["method"] != want.method || got["session_id"] != "a" || got["model"] != want.model || got["outcome"] != want.outcome {
			t.Fatalf("unexpected attempt: %v", got)
		}
	}
}

func TestPromptBarrier(t *testing.T) {
	dir := t.TempDir()
	s := &MockACPServer{promptBarrierMarker: "hold", promptBarrierReady: filepath.Join(dir, "ready"), promptBarrierRelease: filepath.Join(dir, "release")}
	if err := s.waitPromptBarrier("unrelated"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.waitPromptBarrier("please hold") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(s.promptBarrierReady); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("barrier did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(s.promptBarrierRelease, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("barrier did not release")
	}
}
