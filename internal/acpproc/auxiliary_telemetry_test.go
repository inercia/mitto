package acpproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

const (
	telemetryPromptSentinel   = "PROMPT-SENTINEL-héllo-世界"
	telemetryResponseSentinel = "RESPONSE-SENTINEL-secret-token"
)

func newTelemetryManager(buf *bytes.Buffer) *ACPProcessManager {
	return &ACPProcessManager{
		logger: slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func decodeTelemetryRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log record %q: %v", buf.String(), err)
	}
	return rec
}

func TestNewAuxPromptTrace_CountsRunesAndBytes(t *testing.T) {
	tr := newAuxPromptTrace("ws", "title", "héllo世界")
	if tr.promptChars != 7 {
		t.Errorf("promptChars = %d, want 7", tr.promptChars)
	}
	if tr.promptBytes != len("héllo世界") || tr.promptBytes <= tr.promptChars {
		t.Errorf("promptBytes = %d, want > chars", tr.promptBytes)
	}
	if tr.promptMessages != 1 {
		t.Errorf("promptMessages = %d, want 1", tr.promptMessages)
	}
}

func TestAuxiliaryClient_ResponseStatsCountsChunksAndResets(t *testing.T) {
	c := &auxiliaryClient{}
	for _, text := range []string{"  héllo", " 世界  "} {
		if err := c.OnSessionUpdate(context.Background(), acp.SessionNotification{
			Update: acp.UpdateAgentMessageText(text),
		}); err != nil {
			t.Fatalf("SessionUpdate: %v", err)
		}
	}
	runes, bytesLen, chunks := c.responseStats()
	if runes != 8 || bytesLen != len("héllo 世界") || chunks != 2 {
		t.Errorf("stats = (%d,%d,%d), want (8,%d,2)", runes, bytesLen, chunks, len("héllo 世界"))
	}
	c.reset()
	if r, b, n := c.responseStats(); r != 0 || b != 0 || n != 0 {
		t.Errorf("after reset stats = (%d,%d,%d), want zeros", r, b, n)
	}
}

func TestLogAuxPromptOutcome_SuccessWithModelAndNoContent(t *testing.T) {
	var buf bytes.Buffer
	m := newTelemetryManager(&buf)

	tr := newAuxPromptTrace("ws-1", "title-gen", telemetryPromptSentinel)
	state := &auxiliarySessionState{sessionID: "session-1", modelID: "model-x"}
	tr.beginAttempt(state, false)
	client := &auxiliaryClient{}
	_ = client.OnSessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.UpdateAgentMessageText(telemetryResponseSentinel),
	})
	tr.recordResponse(client)

	m.logAuxPromptOutcome(context.Background(), tr, nil)

	out := buf.String()
	if strings.Contains(out, "SENTINEL") {
		t.Fatalf("log leaked content: %s", out)
	}
	rec := decodeTelemetryRecord(t, &buf)
	if rec["msg"] != "Auxiliary prompt completed" || rec["level"] != "INFO" {
		t.Errorf("unexpected msg/level: %v %v", rec["msg"], rec["level"])
	}
	want := map[string]any{
		"workspace_uuid":  "ws-1",
		"purpose":         "title-gen",
		"success":         true,
		"async":           false,
		"attempts":        float64(1),
		"retry":           false,
		"prompt_messages": float64(1),
		"session_id":      "session-1",
		"model_id":        "model-x",
		"model_reported":  true,
		"response_chunks": float64(1),
		"response_chars":  float64(len(telemetryResponseSentinel)),
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %v, want %v", k, rec[k], v)
		}
	}
	if _, ok := rec["duration_ms"]; !ok {
		t.Error("duration_ms missing")
	}
	if _, ok := rec["error_class"]; ok {
		t.Error("error_class must be absent on success")
	}
}

func TestLogAuxPromptOutcome_UnknownModelIsNotInvented(t *testing.T) {
	var buf bytes.Buffer
	m := newTelemetryManager(&buf)
	tr := newAuxPromptTrace("ws", "p", "x")
	tr.beginAttempt(&auxiliarySessionState{}, true)
	m.logAuxPromptOutcome(context.Background(), tr, nil)

	rec := decodeTelemetryRecord(t, &buf)
	if rec["model_id"] != "unknown" || rec["model_reported"] != false || rec["retry"] != true {
		t.Errorf("unexpected model/retry fields: %v", rec)
	}
}

func TestLogAuxPromptOutcome_NoAttemptOmitsModelAndResponse(t *testing.T) {
	var buf bytes.Buffer
	m := newTelemetryManager(&buf)
	m.logAuxPromptOutcome(context.Background(), newAuxPromptTrace("ws", "p", "x"), errors.New("disabled"))

	rec := decodeTelemetryRecord(t, &buf)
	for _, k := range []string{"model_id", "model_reported", "response_chars"} {
		if _, ok := rec[k]; ok {
			t.Errorf("%s should be absent", k)
		}
	}
}

func TestLogAuxPromptOutcome_FailureClassesAndNoErrorText(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name      string
		ctx       context.Context
		err       error
		wantClass string
		wantLevel string
		wantCode  any
	}{
		{"rpc", context.Background(), fmt.Errorf("prompt failed: %w", &acp.RequestError{Code: -32603, Message: "SECRET-rpc"}), "rpc", "ERROR", float64(-32603)},
		{"cancelled", cancelled, errors.New("SECRET-other"), "cancelled", "WARN", nil},
		{"deadline", context.Background(), context.DeadlineExceeded, "timeout", "ERROR", nil},
		{"other", context.Background(), errors.New("SECRET-other"), "other", "ERROR", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			m := newTelemetryManager(&buf)
			tr := newAuxPromptTrace("ws", "p", telemetryPromptSentinel)
			tr.beginAttempt(&auxiliarySessionState{}, false)
			m.logAuxPromptOutcome(tt.ctx, tr, tt.err)

			if strings.Contains(buf.String(), "SECRET") || strings.Contains(buf.String(), "SENTINEL") {
				t.Fatalf("log leaked error/prompt text: %s", buf.String())
			}
			rec := decodeTelemetryRecord(t, &buf)
			if rec["msg"] != "Auxiliary prompt failed" || rec["success"] != false {
				t.Errorf("unexpected record: %v", rec)
			}
			if rec["error_class"] != tt.wantClass || rec["level"] != tt.wantLevel {
				t.Errorf("class/level = %v/%v, want %v/%v", rec["error_class"], rec["level"], tt.wantClass, tt.wantLevel)
			}
			if rec["rpc_code"] != tt.wantCode {
				t.Errorf("rpc_code = %v, want %v", rec["rpc_code"], tt.wantCode)
			}
		})
	}
}

func TestLogAuxPromptOutcome_NilSafe(t *testing.T) {
	var m *ACPProcessManager
	m.logAuxPromptOutcome(context.Background(), newAuxPromptTrace("w", "p", "x"), nil)
	(&ACPProcessManager{}).logAuxPromptOutcome(context.Background(), newAuxPromptTrace("w", "p", "x"), nil)
	var buf bytes.Buffer
	newTelemetryManager(&buf).logAuxPromptOutcome(context.Background(), nil, nil)
	var tr *auxPromptTrace
	tr.beginAttempt(nil, false)
	tr.recordResponse(nil)
}

func TestPromptAuxiliaryAsync_DisabledLogsDispatchFailure(t *testing.T) {
	var buf bytes.Buffer
	m := newTelemetryManager(&buf)
	m.DisableAuxiliary = true

	err := m.PromptAuxiliaryAsync(context.Background(), "ws-1", "title-gen", telemetryPromptSentinel)
	if err == nil {
		t.Fatal("PromptAuxiliaryAsync() error = nil, want disabled error")
	}
	if strings.Contains(buf.String(), "SENTINEL") {
		t.Fatalf("log leaked prompt content: %s", buf.String())
	}
	rec := decodeTelemetryRecord(t, &buf)
	if rec["msg"] != "Auxiliary prompt failed" || rec["async"] != true ||
		rec["success"] != false || rec["attempts"] != float64(0) {
		t.Errorf("unexpected async dispatch failure record: %v", rec)
	}
	if _, ok := rec["error_class"]; !ok {
		t.Error("error_class missing")
	}
}
