package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// newBadgeClickHandlers builds a Handlers with a MittoConfig whose UI.Mac.OpenIn
// contains the given targets.
func newBadgeClickHandlers(t *testing.T, targets []config.OpenTarget) *Handlers {
	t.Helper()
	cfg := &config.Config{
		UI: config.UIConfig{
			Mac: &config.MacUIConfig{
				OpenIn: &config.OpenInConfig{Targets: targets},
			},
		},
	}
	return New(Deps{MittoConfig: cfg})
}

func doBadgeClick(t *testing.T, h *Handlers, body badgeClickRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/badge-click", bytes.NewReader(buf))
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	h.HandleBadgeClick(w, req)
	return w
}

func decodeBadgeClickResp(t *testing.T, w *httptest.ResponseRecorder) badgeClickResponse {
	t.Helper()
	var resp badgeClickResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	return resp
}

func TestBadgeClick_ActionOpen_Success(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	h := newBadgeClickHandlers(t, []config.OpenTarget{
		{ID: "test-ok", Label: "Test OK", Command: "true", Enabled: boolPtr(true)},
	})
	w := doBadgeClick(t, h, badgeClickRequest{
		WorkspacePath: t.TempDir(),
		Action:        "open",
		TargetID:      "test-ok",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	resp := decodeBadgeClickResp(t, w)
	if !resp.Success {
		t.Errorf("Success = false, want true; error=%q", resp.Error)
	}
}

func TestBadgeClick_ActionOpen_DisabledTarget(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	h := newBadgeClickHandlers(t, []config.OpenTarget{
		{ID: "test-disabled", Label: "Off", Command: "true", Enabled: boolPtr(false)},
	})
	w := doBadgeClick(t, h, badgeClickRequest{
		WorkspacePath: t.TempDir(),
		Action:        "open",
		TargetID:      "test-disabled",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	resp := decodeBadgeClickResp(t, w)
	if resp.Success {
		t.Errorf("Success = true, want false")
	}
	if !strings.Contains(strings.ToLower(resp.Error), "disabled") {
		t.Errorf("Error = %q, want to contain %q", resp.Error, "disabled")
	}
}

func TestBadgeClick_ActionOpen_UnknownTarget(t *testing.T) {
	h := newBadgeClickHandlers(t, nil)
	w := doBadgeClick(t, h, badgeClickRequest{
		WorkspacePath: t.TempDir(),
		Action:        "open",
		TargetID:      "definitely-does-not-exist",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "definitely-does-not-exist") {
		t.Errorf("body = %q, want to contain the requested target id", w.Body.String())
	}
}

func TestBadgeClick_ActionOpen_MissingTargetID(t *testing.T) {
	h := newBadgeClickHandlers(t, nil)
	w := doBadgeClick(t, h, badgeClickRequest{
		WorkspacePath: t.TempDir(),
		Action:        "open",
		TargetID:      "",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "target_id") {
		t.Errorf("body = %q, want to mention target_id", w.Body.String())
	}
}

func TestBadgeClick_MissingAction_Rejected(t *testing.T) {
	h := newBadgeClickHandlers(t, nil)
	// Action == "" is no longer accepted after the legacy branches were removed.
	w := doBadgeClick(t, h, badgeClickRequest{
		WorkspacePath: t.TempDir(),
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "open") {
		t.Errorf("body = %q, want to mention required action=open", w.Body.String())
	}
}

func TestBadgeClick_UnknownAction_Rejected(t *testing.T) {
	h := newBadgeClickHandlers(t, nil)
	w := doBadgeClick(t, h, badgeClickRequest{
		WorkspacePath: t.TempDir(),
		Action:        "terminal",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
