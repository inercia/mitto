package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

func newTaskLabelColorHandlers(t *testing.T) (*Handlers, *config.Config, *int) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	cfg := &config.Config{}
	broadcasts := 0
	h := New(Deps{
		MittoConfig:                     cfg,
		BroadcastTaskLabelColorsUpdated: func() { broadcasts++ },
	})
	return h, cfg, &broadcasts
}

func taskLabelColorRequest(t *testing.T, h *Handlers, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/global/task-label-colors", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.HandleGlobalTaskLabelColors(w, req)
	return w
}

func TestHandleGlobalTaskLabelColors_PutGetOrderNormalizeAndBroadcast(t *testing.T) {
	h, cfg, broadcasts := newTaskLabelColorHandlers(t)
	w := taskLabelColorRequest(t, h, http.MethodPut,
		`{"entries":[{"label":" needs-human ","color":"#EF4444"},{"label":"blocked","color":"#f59e0b"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", w.Code, w.Body.String())
	}
	if *broadcasts != 1 {
		t.Fatalf("broadcasts = %d, want 1", *broadcasts)
	}
	if len(cfg.TaskLabelColors) != 2 || cfg.TaskLabelColors[0].Label != "needs-human" || cfg.TaskLabelColors[0].Color != "#ef4444" {
		t.Fatalf("in-memory config = %+v", cfg.TaskLabelColors)
	}

	w = taskLabelColorRequest(t, h, http.MethodGet, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", w.Code, w.Body.String())
	}
	var got globalTaskLabelColorsBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if len(got.Entries) != 2 || got.Entries[0].Label != "needs-human" || got.Entries[1].Label != "blocked" {
		t.Fatalf("GET entries = %+v, want preserved order", got.Entries)
	}
}

func TestHandleGlobalTaskLabelColors_Clear(t *testing.T) {
	h, cfg, broadcasts := newTaskLabelColorHandlers(t)
	w := taskLabelColorRequest(t, h, http.MethodPut, `{"entries":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(cfg.TaskLabelColors) != 0 || *broadcasts != 1 {
		t.Fatalf("config=%+v broadcasts=%d", cfg.TaskLabelColors, *broadcasts)
	}
	if w.Body.String() != "{\"entries\":[]}\n" {
		t.Errorf("body = %q, want empty array", w.Body.String())
	}
}

func TestHandleGlobalTaskLabelColors_RejectsInvalidBodiesWithoutBroadcast(t *testing.T) {
	tests := map[string]string{
		"malformed":       `{`,
		"missing entries": `{}`,
		"null entries":    `{"entries":null}`,
		"empty label":     `{"entries":[{"label":"  ","color":"#ef4444"}]}`,
		"bad color":       `{"entries":[{"label":"needs-human","color":"red"}]}`,
		"unknown field":   `{"entries":[],"extra":true}`,
		"trailing body":   `{"entries":[]} {}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			h, cfg, broadcasts := newTaskLabelColorHandlers(t)
			w := taskLabelColorRequest(t, h, http.MethodPut, body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
			if *broadcasts != 0 || len(cfg.TaskLabelColors) != 0 {
				t.Errorf("invalid request changed state: broadcasts=%d config=%+v", *broadcasts, cfg.TaskLabelColors)
			}
		})
	}
}
