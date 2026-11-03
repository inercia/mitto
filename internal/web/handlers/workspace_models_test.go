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
	if err := cache.Record(modelcache.Observation{Server: "agent-a", WorkingDir: "/workspace-a", Complete: true, Models: []modelcache.Model{{
		ID:          "model-a",
		Name:        "Model A",
		Description: &description,
	}}}); err != nil {
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

func fetchModelsPolicy(t *testing.T, record []modelcache.Model, hidden []string, overrides config.ModelOverrides) WorkspaceModelsResponse {
	t.Helper()
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if record != nil {
		if err := cache.Record(modelcache.Observation{Server: "agent-a", WorkingDir: "/workspace-a", Complete: true, Models: record}); err != nil {
			t.Fatal(err)
		}
	}
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{
		UUID: "workspace-a", WorkingDir: "/workspace-a", ACPServer: "agent-a", ModelOverrides: overrides,
	}})
	sm.SetMittoConfig(&config.Config{ACPServers: []config.ACPServer{{Name: "agent-a", HiddenModels: hidden}}})
	h := New(Deps{SessionManager: sm, ModelCache: cache})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
	req.SetPathValue("uuid", "workspace-a")
	w := httptest.NewRecorder()
	h.HandleWorkspaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp WorkspaceModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHandleWorkspaceModels_PolicyStates(t *testing.T) {
	raw := []modelcache.Model{{ID: "model-a"}, {ID: "model-b"}}

	t.Run("absent", func(t *testing.T) {
		r := fetchModelsPolicy(t, nil, []string{"model-a"}, nil)
		if r.CatalogState != "absent" || r.UpdatedAt != nil || r.Models == nil || len(r.Models) != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("available no rules", func(t *testing.T) {
		r := fetchModelsPolicy(t, raw, nil, nil)
		if r.CatalogState != "available" || len(r.Models) != 2 || r.HiddenCount != 0 || r.UpdatedAt == nil {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("available partially hidden", func(t *testing.T) {
		r := fetchModelsPolicy(t, raw, []string{"model-a"}, nil)
		if r.CatalogState != "available" || len(r.Models) != 1 || r.Models[0].ID != "model-b" || r.HiddenCount != 1 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("policy empty", func(t *testing.T) {
		r := fetchModelsPolicy(t, raw, []string{"model-.*"}, nil)
		if r.CatalogState != "policy_empty" || r.Models == nil || len(r.Models) != 0 || r.HiddenCount != 2 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("invalid rule fails closed", func(t *testing.T) {
		r := fetchModelsPolicy(t, raw, []string{"("}, nil)
		if r.CatalogState != "policy_empty" || len(r.Models) != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("saved overrides", func(t *testing.T) {
		ov := config.ModelOverrides{
			{ACPServer: "agent-a", Tag: "x", ModelID: "model-a"},
			{ACPServer: "agent-a", Tag: "y", ModelID: "gone"},
			{ACPServer: "agent-a", Tag: "z", ModelID: "model-b"},
			{ACPServer: "other", Tag: "x", ModelID: "ignored"},
		}
		r := fetchModelsPolicy(t, raw, []string{"model-a"}, ov)
		if len(r.HiddenSavedIDs) != 1 || r.HiddenSavedIDs[0] != "model-a" {
			t.Fatalf("hidden saved: %+v", r.HiddenSavedIDs)
		}
		if len(r.UnavailableSavedIDs) != 1 || r.UnavailableSavedIDs[0] != "gone" {
			t.Fatalf("unavailable saved: %+v", r.UnavailableSavedIDs)
		}
		for _, m := range r.Models {
			if m.ID == "model-a" {
				t.Fatal("hidden model resurrected")
			}
		}
	})
}

func TestHandleWorkspaceModels_HideThenUnhideSameCachedRaw(t *testing.T) {
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := []modelcache.Model{{ID: "model-a", Name: "A"}, {ID: "model-b", Name: "B"}, {ID: "model-c", Name: "C"}}
	if err := cache.Record(modelcache.Observation{Server: "agent-a", WorkingDir: "/workspace-a", Complete: true, Models: raw}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Record(modelcache.Observation{Server: "agent-other", WorkingDir: "/workspace-a", Complete: true, Models: []modelcache.Model{{ID: "model-b", Name: "B"}}}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() ([]modelcache.Model, time.Time) {
		t.Helper()
		c, ok := cache.Get("agent-a", "/workspace-a")
		if !ok {
			t.Fatal("agent-a cache record missing")
		}
		return c.Models, c.UpdatedAt
	}
	wantRaw, wantUpdated := snapshot()

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "workspace-a", WorkingDir: "/workspace-a", ACPServer: "agent-a"}})
	h := New(Deps{SessionManager: sm, ModelCache: cache})
	fetch := func(hidden []string) WorkspaceModelsResponse {
		t.Helper()
		sm.SetMittoConfig(&config.Config{ACPServers: []config.ACPServer{
			{Name: "agent-a", HiddenModels: hidden},
			{Name: "agent-other", HiddenModels: []string{"model-b"}},
		}})
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
		req.SetPathValue("uuid", "workspace-a")
		w := httptest.NewRecorder()
		h.HandleWorkspaceModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var resp WorkspaceModelsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	ids := func(models []modelcache.Model) []string {
		out := []string{}
		for _, m := range models {
			out = append(out, m.ID)
		}
		return out
	}
	checkRaw := func(stage string) {
		t.Helper()
		got, updated := snapshot()
		if len(got) != len(wantRaw) || !updated.Equal(wantUpdated) {
			t.Fatalf("%s: raw cache changed: %v %v", stage, got, updated)
		}
		for i := range got {
			if got[i].ID != wantRaw[i].ID {
				t.Fatalf("%s: raw cache changed: %v", stage, got)
			}
		}
	}

	hidden := fetch([]string{"model-b"})
	if got := ids(hidden.Models); len(got) != 2 || got[0] != "model-a" || got[1] != "model-c" || hidden.HiddenCount != 1 || hidden.CatalogState != "available" {
		t.Fatalf("hidden view = %v count=%d state=%q", got, hidden.HiddenCount, hidden.CatalogState)
	}
	checkRaw("after hide")

	shown := fetch(nil)
	if got := ids(shown.Models); len(got) != 3 || got[0] != "model-a" || got[1] != "model-b" || got[2] != "model-c" || shown.HiddenCount != 0 || shown.CatalogState != "available" {
		t.Fatalf("unhidden view = %v count=%d state=%q", got, shown.HiddenCount, shown.CatalogState)
	}
	checkRaw("after unhide")
}

func fetchScopedModels(t *testing.T, seed []modelcache.Observation) WorkspaceModelsResponse {
	t.Helper()
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range seed {
		if err := cache.Record(o); err != nil {
			t.Fatal(err)
		}
	}
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "workspace-a", WorkingDir: "/workspace-a", ACPServer: "agent-a"}})
	h := New(Deps{SessionManager: sm, ModelCache: cache})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
	req.SetPathValue("uuid", "workspace-a")
	w := httptest.NewRecorder()
	h.HandleWorkspaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp WorkspaceModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHandleWorkspaceModels_NoCrossScopeFallback(t *testing.T) {
	m := []modelcache.Model{{ID: "model-x", Name: "X"}}
	resp := fetchScopedModels(t, []modelcache.Observation{
		{Server: "agent-other", WorkingDir: "/workspace-a", Complete: true, Models: m},
		{Server: "agent-a", WorkingDir: "/workspace-b", Complete: true, Models: m},
	})
	if resp.CatalogState != "absent" || len(resp.Models) != 0 || resp.UpdatedAt != nil {
		t.Fatalf("other scope leaked: %+v", resp)
	}
	if resp.ACPServer != "agent-a" || resp.WorkingDir != "/workspace-a" {
		t.Fatalf("scope not echoed: %+v", resp)
	}
}

func TestHandleWorkspaceModels_CompleteEmptyIsNotAbsent(t *testing.T) {
	resp := fetchScopedModels(t, []modelcache.Observation{
		{Server: "agent-a", WorkingDir: "/workspace-a", Complete: true, Models: []modelcache.Model{}},
	})
	if resp.CatalogState != "empty" || resp.UpdatedAt == nil || len(resp.Models) != 0 {
		t.Fatalf("explicit-empty mishandled: %+v", resp)
	}
}

func TestHandleWorkspaceModels_ExplicitEmptyDoesNotResurrectProfilesOrHiddenOverride(t *testing.T) {
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Record(modelcache.Observation{
		Server: "agent-a", WorkingDir: "/workspace-a", Complete: true, Models: []modelcache.Model{},
	}); err != nil {
		t.Fatal(err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{
		UUID: "workspace-a", WorkingDir: "/workspace-a", ACPServer: "agent-a",
		ModelOverrides: config.ModelOverrides{{ACPServer: "agent-a", Tag: "coding", ModelID: "hidden-model"}},
	}})
	sm.SetMittoConfig(&config.Config{
		Models:     []config.ModelProfile{{Name: "Synthetic Profile"}},
		ACPServers: []config.ACPServer{{Name: "agent-a", HiddenModels: []string{"hidden-model"}}},
	})
	h := New(Deps{SessionManager: sm, ModelCache: cache})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
	req.SetPathValue("uuid", "workspace-a")
	w := httptest.NewRecorder()
	h.HandleWorkspaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var resp WorkspaceModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.CatalogState != "empty" || resp.UpdatedAt == nil || resp.UpdatedAt.IsZero() || len(resp.Models) != 0 {
		t.Fatalf("explicit-empty response = %+v", resp)
	}
	if len(resp.HiddenSavedIDs) != 1 || resp.HiddenSavedIDs[0] != "hidden-model" || len(resp.UnavailableSavedIDs) != 0 {
		t.Fatalf("hidden override was not retained as hidden: %+v", resp)
	}
}

// fetchUnionModels extends the policy harness with live workspace-level
// hidden-model rules (the helper above covers the agent level only).
func fetchUnionModels(t *testing.T, record []modelcache.Model, agentHidden, wsHidden []string, overrides config.ModelOverrides) WorkspaceModelsResponse {
	t.Helper()
	cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if record != nil {
		if err := cache.Record(modelcache.Observation{Server: "agent-a", WorkingDir: "/workspace-a", Complete: true, Models: record}); err != nil {
			t.Fatal(err)
		}
	}
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{
		UUID:           "workspace-a",
		WorkingDir:     "/workspace-a",
		ACPServer:      "agent-a",
		HiddenModels:   wsHidden,
		ModelOverrides: overrides,
	}})
	sm.SetMittoConfig(&config.Config{ACPServers: []config.ACPServer{{Name: "agent-a", HiddenModels: agentHidden}}})
	h := New(Deps{SessionManager: sm, ModelCache: cache})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
	req.SetPathValue("uuid", "workspace-a")
	w := httptest.NewRecorder()
	h.HandleWorkspaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp WorkspaceModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHandleWorkspaceModels_UnionAgentAndWorkspaceRules(t *testing.T) {
	raw := []modelcache.Model{{ID: "model-a", Name: "A"}, {ID: "model-b", Name: "B"}, {ID: "model-c", Name: "C"}}

	t.Run("disjoint levels hide both", func(t *testing.T) {
		r := fetchUnionModels(t, raw[:2], []string{"^model-a$"}, []string{"^model-b$"}, nil)
		if r.CatalogState != "policy_empty" || len(r.Models) != 0 || r.HiddenCount != 2 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("union leaves only the complement model", func(t *testing.T) {
		r := fetchUnionModels(t, raw, []string{"^model-a$"}, []string{"^model-b$"}, nil)
		if len(r.Models) != 1 || r.Models[0].ID != "model-c" || r.HiddenCount != 2 || r.CatalogState != "available" {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("workspace-only rules filter", func(t *testing.T) {
		r := fetchUnionModels(t, raw, nil, []string{"MODEL-B"}, nil)
		if r.CatalogState != "available" || len(r.Models) != 2 || r.HiddenCount != 1 {
			t.Fatalf("unexpected: %+v", r)
		}
		for _, m := range r.Models {
			if m.ID == "model-b" {
				t.Fatal("workspace-hidden model leaked (case-insensitive)")
			}
		}
	})
	t.Run("invalid workspace rule fails closed", func(t *testing.T) {
		r := fetchUnionModels(t, raw, nil, []string{"("}, nil)
		if r.CatalogState != "policy_empty" || len(r.Models) != 0 || r.HiddenCount != 3 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("invalid agent rule fails closed", func(t *testing.T) {
		r := fetchUnionModels(t, raw, []string{"("}, nil, nil)
		if r.CatalogState != "policy_empty" || len(r.Models) != 0 || r.HiddenCount != 3 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("no rules keeps all models", func(t *testing.T) {
		r := fetchUnionModels(t, raw, nil, nil, nil)
		if r.CatalogState != "available" || len(r.Models) != 3 || r.HiddenCount != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
}

func TestHandleWorkspaceModels_UnionSavedOverrides(t *testing.T) {
	raw := []modelcache.Model{{ID: "model-a", Name: "A"}, {ID: "model-b", Name: "B"}}
	ov := config.ModelOverrides{
		{ACPServer: "agent-a", Tag: "x", ModelID: "model-a"},
		{ACPServer: "agent-a", Tag: "y", ModelID: "gone"},
	}

	t.Run("workspace-hidden override surfaces", func(t *testing.T) {
		r := fetchUnionModels(t, raw, nil, []string{"^model-a$"}, ov)
		if len(r.HiddenSavedIDs) != 1 || r.HiddenSavedIDs[0] != "model-a" {
			t.Fatalf("hidden saved: %+v", r.HiddenSavedIDs)
		}
		if len(r.UnavailableSavedIDs) != 1 || r.UnavailableSavedIDs[0] != "gone" {
			t.Fatalf("unavailable saved: %+v", r.UnavailableSavedIDs)
		}
	})
	t.Run("agent-hidden override surfaces", func(t *testing.T) {
		r := fetchUnionModels(t, raw, []string{"^model-a$"}, nil, ov)
		if len(r.HiddenSavedIDs) != 1 || r.HiddenSavedIDs[0] != "model-a" {
			t.Fatalf("hidden saved: %+v", r.HiddenSavedIDs)
		}
	})
	t.Run("duplicate override rows dedupe", func(t *testing.T) {
		dup := config.ModelOverrides{
			{ACPServer: "agent-a", Tag: "x", ModelID: "model-a"},
			{ACPServer: "agent-a", Tag: "y", ModelID: "model-a"},
		}
		r := fetchUnionModels(t, raw, []string{"^model-a$"}, nil, dup)
		if len(r.HiddenSavedIDs) != 1 {
			t.Fatalf("expected deduped hidden saved, got: %+v", r.HiddenSavedIDs)
		}
	})
}

func TestHandleWorkspaceModels_ScopeEditStopsMatchingOldRecord(t *testing.T) {
	cases := []struct {
		name       string
		newServer  string
		newWorkDir string
	}{
		{"server change", "agent-b", "/workspace-a"},
		{"folder change", "agent-a", "/workspace-moved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache, err := modelcache.New(filepath.Join(t.TempDir(), "model-cache.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.Record(modelcache.Observation{
				Server: "agent-a", WorkingDir: "/workspace-a", Complete: true,
				Models: []modelcache.Model{{ID: "model-x", Name: "X"}},
			}); err != nil {
				t.Fatal(err)
			}
			sm := conversation.NewSessionManager("", "", false, nil)
			h := New(Deps{SessionManager: sm, ModelCache: cache})
			fetch := func() WorkspaceModelsResponse {
				req := httptest.NewRequest(http.MethodGet, "/api/workspaces/workspace-a/models", nil)
				req.SetPathValue("uuid", "workspace-a")
				w := httptest.NewRecorder()
				h.HandleWorkspaceModels(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				var resp WorkspaceModelsResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				return resp
			}

			sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "workspace-a", WorkingDir: "/workspace-a", ACPServer: "agent-a"}})
			if r := fetch(); r.CatalogState == "absent" || len(r.Models) != 1 {
				t.Fatalf("original scope should see record: %+v", r)
			}

			sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "workspace-a", WorkingDir: tc.newWorkDir, ACPServer: tc.newServer}})
			r := fetch()
			if r.CatalogState != "absent" || len(r.Models) != 0 || r.UpdatedAt != nil {
				t.Fatalf("old record leaked into edited scope: %+v", r)
			}
			if r.ACPServer != tc.newServer || r.WorkingDir != tc.newWorkDir {
				t.Fatalf("edited scope not echoed: %+v", r)
			}

			if c, ok := cache.Get("agent-a", "/workspace-a"); !ok || len(c.Models) != 1 {
				t.Fatalf("old-scope record lost: %+v ok=%v", c, ok)
			}
		})
	}
}
