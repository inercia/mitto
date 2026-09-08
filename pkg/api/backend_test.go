package api

import (
	"net/http"
	"testing"
)

// --- DecodeBackendDescriptor (mitto-lrt.12) ---

func TestDecodeBackendDescriptor_AbsentKeyReturnsNil(t *testing.T) {
	desc, err := DecodeBackendDescriptor(map[string]interface{}{"session_id": "s1"})
	if err != nil {
		t.Fatalf("DecodeBackendDescriptor: %v", err)
	}
	if desc != nil {
		t.Errorf("desc = %+v, want nil for a legacy payload with no \"backend\" key", desc)
	}
}

func TestDecodeBackendDescriptor_NullValueReturnsNil(t *testing.T) {
	desc, err := DecodeBackendDescriptor(map[string]interface{}{"backend": nil})
	if err != nil {
		t.Fatalf("DecodeBackendDescriptor: %v", err)
	}
	if desc != nil {
		t.Errorf("desc = %+v, want nil for an explicit JSON null", desc)
	}
}

func TestDecodeBackendDescriptor_FullPayload(t *testing.T) {
	raw := map[string]interface{}{
		"backend": map[string]interface{}{
			"agent_ref":   map[string]interface{}{"backend": "acp", "provider": "Auggie"},
			"session_ref": map[string]interface{}{"conversation_id": "conv-1", "provider": "Auggie", "provider_session": "upstream-1"},
			"capabilities": map[string]interface{}{
				"images": "supported", "files": "supported", "terminals": "unknown",
			},
			"model": map[string]interface{}{
				"current_id": "m-1",
				"available":  []interface{}{map[string]interface{}{"id": "m-1", "name": "Model 1"}},
			},
			"config_options": []interface{}{
				map[string]interface{}{"id": "mode", "category": "mode", "current": "default", "values": []interface{}{
					map[string]interface{}{"value": "default", "name": "Default"},
				}},
			},
		},
	}
	desc, err := DecodeBackendDescriptor(raw)
	if err != nil {
		t.Fatalf("DecodeBackendDescriptor: %v", err)
	}
	if desc == nil {
		t.Fatal("desc = nil, want a populated descriptor")
	}
	if desc.AgentRef == nil || desc.AgentRef.Backend != "acp" || desc.AgentRef.Provider != "Auggie" {
		t.Errorf("AgentRef = %+v, unexpected", desc.AgentRef)
	}
	if desc.SessionRef == nil || desc.SessionRef.ConversationID != "conv-1" || desc.SessionRef.ProviderSession != "upstream-1" {
		t.Errorf("SessionRef = %+v, unexpected", desc.SessionRef)
	}
	if desc.SessionRef.ConversationID == desc.SessionRef.ProviderSession {
		t.Errorf("ConversationID/ProviderSession collapsed: %+v", desc.SessionRef)
	}
	if desc.Capabilities["images"] != "supported" || desc.Capabilities["terminals"] != "unknown" {
		t.Errorf("Capabilities = %+v, unexpected", desc.Capabilities)
	}
	if desc.Model == nil || desc.Model.CurrentID != "m-1" || len(desc.Model.Available) != 1 || desc.Model.Available[0].ID != "m-1" {
		t.Errorf("Model = %+v, unexpected", desc.Model)
	}
	if len(desc.ConfigOptions) != 1 || desc.ConfigOptions[0].Current != "default" || len(desc.ConfigOptions[0].Values) != 1 {
		t.Errorf("ConfigOptions = %+v, unexpected", desc.ConfigOptions)
	}
}

func TestDecodeBackendDescriptor_MalformedShapeReturnsError(t *testing.T) {
	// "backend" present but not an object: must surface a decode error, not
	// silently coerce or panic.
	_, err := DecodeBackendDescriptor(map[string]interface{}{"backend": "not-an-object"})
	if err == nil {
		t.Error("expected a decode error for a non-object \"backend\" value")
	}
}

// --- Client.GetSession / ListSessions decode the optional field (mitto-lrt.12) ---

func TestClient_GetSession_ParsesBackendDescriptor(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1").RespondJSON(http.StatusOK,
		`{"session_id":"sess-1","acp_server":"Auggie","acp_session_id":"upstream-1","working_dir":"/tmp",`+
			`"backend":{"agent_ref":{"backend":"acp","provider":"Auggie"},"session_ref":{"conversation_id":"sess-1","provider":"Auggie","provider_session":"upstream-1"}}}`)

	got, err := f.Client().GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	// Legacy fields keep their current meaning.
	if got.ACPServer != "Auggie" || got.ACPSessionID != "upstream-1" {
		t.Errorf("legacy fields changed: ACPServer=%q ACPSessionID=%q", got.ACPServer, got.ACPSessionID)
	}
	if got.Backend == nil {
		t.Fatal("Backend = nil, want a populated descriptor")
	}
	if got.Backend.AgentRef == nil || got.Backend.AgentRef.Provider != "Auggie" {
		t.Errorf("Backend.AgentRef = %+v, unexpected", got.Backend.AgentRef)
	}
}

// TestClient_GetSession_LegacyPayloadOmitsBackend guards backward
// compatibility: a server predating mitto-lrt.12 (or a session with no
// computable identity) omits "backend" entirely, and SessionInfo.Backend
// must decode to nil rather than a zero-value struct or an error.
func TestClient_GetSession_LegacyPayloadOmitsBackend(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-2").RespondJSON(http.StatusOK,
		`{"session_id":"sess-2","acp_server":"Auggie","working_dir":"/tmp"}`)

	got, err := f.Client().GetSession("sess-2")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Backend != nil {
		t.Errorf("Backend = %+v, want nil for a legacy payload", got.Backend)
	}
	if got.ACPServer != "Auggie" {
		t.Errorf("ACPServer = %q, want Auggie (legacy field untouched)", got.ACPServer)
	}
}

func TestClient_ListSessions_ParsesBackendDescriptorPerSession(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions").RespondJSON(http.StatusOK,
		`[{"session_id":"sess-a","acp_server":"Auggie","backend":{"agent_ref":{"backend":"acp","provider":"Auggie"}}},`+
			`{"session_id":"sess-b","acp_server":"Auggie"}]`)

	got, err := f.Client().ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Backend == nil || got[0].Backend.AgentRef == nil || got[0].Backend.AgentRef.Provider != "Auggie" {
		t.Errorf("got[0].Backend = %+v, unexpected", got[0].Backend)
	}
	if got[1].Backend != nil {
		t.Errorf("got[1].Backend = %+v, want nil (mixed old/new payload in one list)", got[1].Backend)
	}
}
