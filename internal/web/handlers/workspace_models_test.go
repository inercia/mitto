package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/modelcache"
)

func TestHandleWorkspaceModels_CachedCatalog(t *testing.T) {
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatalf("create model cache: %v", err)
	}
	description := "Fast and capable"
	if err := cache.Record("agent-a", []modelcache.Model{{
		ID:          "model-a",
		Name:        "Model A",
		Description: &description,
	}}); err != nil {
		t.Fatalf("record model cache: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{
		UUID:       "workspace-a",
		WorkingDir: "/workspace-a",
		ACPServer:  "agent-a",
	}})
	h := New(Deps{
		SessionManager: sm,
		ModelCache:     cache,
		HasLiveWorkspaceACP: func(uuid string) bool {
			return uuid == "workspace-a"
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
	req.SetPathValue("uuid", "workspace-a")
	w := httptest.NewRecorder()
	h.HandleWorkspaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var response struct {
		ACPServer string             `json:"acp_server"`
		Models    []modelcache.Model `json:"models"`
		Live      bool               `json:"live"`
		UpdatedAt *time.Time         `json:"updated_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ACPServer != "agent-a" || len(response.Models) != 1 || response.Models[0].ID != "model-a" ||
		response.Models[0].Name != "Model A" || response.Models[0].Description == nil || *response.Models[0].Description != description {
		t.Fatalf("unexpected model response: %+v", response)
	}
	if !response.Live {
		t.Fatal("live = false, want ACP process liveness callback result")
	}
	if response.UpdatedAt == nil || response.UpdatedAt.IsZero() {
		t.Fatalf("updated_at = %v, want cached timestamp", response.UpdatedAt)
	}
}

func TestHandleWorkspaceModels_EmptyCacheAndUnknownWorkspace(t *testing.T) {
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatalf("create model cache: %v", err)
	}
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{
		UUID:       "workspace-new",
		WorkingDir: "/workspace-new",
		ACPServer:  "agent-not-seen",
	}})
	h := New(Deps{
		SessionManager: sm,
		ModelCache:     cache,
		HasLiveWorkspaceACP: func(uuid string) bool {
			return uuid == "workspace-new"
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-new/models", nil)
	req.SetPathValue("uuid", "workspace-new")
	w := httptest.NewRecorder()
	h.HandleWorkspaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("empty-cache status = %d, want %d", w.Code, http.StatusOK)
	}
	var response struct {
		ACPServer string             `json:"acp_server"`
		Models    []modelcache.Model `json:"models"`
		Live      bool               `json:"live"`
		UpdatedAt *time.Time         `json:"updated_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode empty-cache response: %v", err)
	}
	if response.ACPServer != "agent-not-seen" || response.Models == nil || len(response.Models) != 0 || !response.Live || response.UpdatedAt != nil {
		t.Fatalf("unexpected empty-cache response: %+v", response)
	}

	unknown := httptest.NewRequest(http.MethodGet, "/api/workspaces/missing/models", nil)
	unknown.SetPathValue("uuid", "missing")
	unknownResponse := httptest.NewRecorder()
	h.HandleWorkspaceModels(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusNotFound {
		t.Fatalf("unknown-workspace status = %d, want %d", unknownResponse.Code, http.StatusNotFound)
	}
}
