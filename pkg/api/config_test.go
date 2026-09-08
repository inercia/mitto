package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_ConfigSnapshot_DecodesRedactedResponse(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/config/snapshot").RespondJSON(http.StatusOK,
		`{"exists":true,"revision":"123-456","config":{"web":{"port":8080,"auth":{"shared_token":"[REDACTED]"}}}}`)

	got, err := f.Client().ConfigSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshot: %v", err)
	}
	if !got.Exists || got.Revision != "123-456" {
		t.Fatalf("ConfigSnapshot = %+v, want Exists=true Revision=123-456", got)
	}
	web, ok := got.Config["web"].(map[string]interface{})
	if !ok {
		t.Fatalf("Config[web] not an object: %#v", got.Config["web"])
	}
	auth, ok := web["auth"].(map[string]interface{})
	if !ok || auth["shared_token"] != "[REDACTED]" {
		t.Fatalf("Config[web][auth] = %#v, want shared_token redacted", web["auth"])
	}
}

func TestClient_ConfigSnapshot_NotFound_ReturnsExistsFalse(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/config/snapshot").RespondJSON(http.StatusOK, `{"exists":false}`)

	got, err := f.Client().ConfigSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ConfigSnapshot: %v", err)
	}
	if got.Exists {
		t.Fatalf("Exists = true, want false")
	}
}

func TestClient_ConfigSnapshot_Unauthorized_ReturnsTypedError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/config/snapshot").
		Fail(http.StatusUnauthorized, "unauthenticated", "Missing or invalid instance bearer token", nil)

	_, err := f.Client().ConfigSnapshot(context.Background())
	assertAPIError(t, err, nil, http.StatusUnauthorized, "unauthenticated")
}

// TestClient_ConfigPatch_SendsRequestBodyAndDecodesResult confirms the
// request is marshaled with the exact field names the server expects
// (ops/path/value/revision/dry_run) and the typed per-key result decodes.
func TestClient_ConfigPatch_SendsRequestBodyAndDecodesResult(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/config/patch").RespondJSON(http.StatusOK,
		`{"dry_run":false,"applied":[{"path":"web.port","status":"restart_required"}],"revision":"789-1"}`)

	patch := ConfigPatchRequest{
		Ops:      []ConfigPatchOp{{Path: "web.port", Value: float64(9090)}},
		Revision: "123-456",
	}
	got, err := f.Client().ConfigPatch(context.Background(), patch)
	if err != nil {
		t.Fatalf("ConfigPatch: %v", err)
	}
	if got.DryRun {
		t.Error("DryRun = true, want false")
	}
	if len(got.Applied) != 1 || got.Applied[0].Path != "web.port" || got.Applied[0].Status != "restart_required" {
		t.Fatalf("Applied = %+v, want [{web.port restart_required}]", got.Applied)
	}
	if got.Revision != "789-1" {
		t.Errorf("Revision = %q, want 789-1", got.Revision)
	}

	req := f.LastRequest()
	if req == nil {
		t.Fatal("no request recorded")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	ops, ok := body["ops"].([]interface{})
	if !ok || len(ops) != 1 {
		t.Fatalf("sent body[ops] = %#v, want a 1-element array", body["ops"])
	}
	op := ops[0].(map[string]interface{})
	if op["path"] != "web.port" || op["value"] != float64(9090) {
		t.Errorf("sent op = %#v, want path=web.port value=9090", op)
	}
	if body["revision"] != "123-456" {
		t.Errorf("sent body[revision] = %#v, want 123-456", body["revision"])
	}
}

// TestClient_ConfigPatch_RevisionMismatch_ReturnsConflict pins the
// optimistic-concurrency error mapping documented on ConfigPatch.
func TestClient_ConfigPatch_RevisionMismatch_ReturnsConflict(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/config/patch").
		Fail(http.StatusConflict, "conflict", "revision_mismatch: settings changed since snapshot was read", nil)

	_, err := f.Client().ConfigPatch(context.Background(), ConfigPatchRequest{
		Ops: []ConfigPatchOp{{Path: "web.port", Value: float64(9090)}},
	})
	assertAPIError(t, err, ErrConflict, http.StatusConflict, "conflict")
}

// TestClient_ConfigPatch_RejectedField_ReturnsForbidden pins the
// rejected/read-only -> 403/ErrForbidden mapping documented on ConfigPatch.
func TestClient_ConfigPatch_RejectedField_ReturnsForbidden(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/config/patch").
		Fail(http.StatusForbidden, "forbidden", "rejected: field cannot be modified through this service", nil)

	_, err := f.Client().ConfigPatch(context.Background(), ConfigPatchRequest{
		Ops: []ConfigPatchOp{{Path: "web.auth.shared_token", Value: "x"}},
	})
	assertAPIError(t, err, ErrForbidden, http.StatusForbidden, "forbidden")
}

// TestClient_ConfigPatch_UnknownField_ReturnsBadRequest pins the
// unknown_field/validation/structure -> 400/ErrBadRequest mapping.
func TestClient_ConfigPatch_UnknownField_ReturnsBadRequest(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/config/patch").
		Fail(http.StatusBadRequest, "bad_request", "unknown_field: unknown or unsupported settings field", nil)

	_, err := f.Client().ConfigPatch(context.Background(), ConfigPatchRequest{
		Ops: []ConfigPatchOp{{Path: "totally.unknown", Value: 1}},
	})
	assertAPIError(t, err, ErrBadRequest, http.StatusBadRequest, "bad_request")
}

// TestClient_ConfigPatch_DryRun_SendsFlag confirms dry_run round-trips
// through the request body and the typed response.
func TestClient_ConfigPatch_DryRun_SendsFlag(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/config/patch").RespondJSON(http.StatusOK,
		`{"dry_run":true,"applied":[{"path":"web.port","status":"would_apply"}]}`)

	got, err := f.Client().ConfigPatch(context.Background(), ConfigPatchRequest{
		Ops:    []ConfigPatchOp{{Path: "web.port", Value: float64(9090)}},
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("ConfigPatch: %v", err)
	}
	if !got.DryRun || got.Applied[0].Status != "would_apply" {
		t.Fatalf("ConfigPatch result = %+v, want DryRun=true status=would_apply", got)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(f.LastRequest().Body, &body); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if body["dry_run"] != true {
		t.Errorf("sent body[dry_run] = %#v, want true", body["dry_run"])
	}
}
