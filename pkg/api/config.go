package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// --- Config snapshot/patch API (mitto-4rz.3) ---

// ConfigSnapshot is the response from (*Client).ConfigSnapshot: a
// point-in-time, fully-redacted view of the server's settings.json
// (mirrors internal/config/configsvc.Snapshot). Secrets (e.g.
// web.auth.simple.password, web.auth.shared_token, the mcp subtree) are
// never present unredacted.
type ConfigSnapshot struct {
	Exists   bool                   `json:"exists"`
	Revision string                 `json:"revision,omitempty"`
	Config   map[string]interface{} `json:"config,omitempty"`
}

// ConfigPatchOp is one {path, value} write operation for ConfigPatch, using
// the same dotted/indexed path syntax as `mitto config set` (e.g.
// "web.port", "task_label_colors[0].color").
type ConfigPatchOp struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// ConfigPatchRequest is the request body for (*Client).ConfigPatch.
type ConfigPatchRequest struct {
	Ops []ConfigPatchOp `json:"ops"`
	// Revision, when set, must match a previously-read ConfigSnapshot.Revision
	// (optimistic concurrency); a stale value fails the whole request with a
	// 409 Conflict instead of silently clobbering a concurrent writer.
	Revision string `json:"revision,omitempty"`
	// DryRun validates (and reports) the batch without persisting anything.
	DryRun bool `json:"dry_run,omitempty"`
}

// ConfigPatchKeyResult reports one op's outcome. Status is one of
// "applied", "restart_required", "apply_failed", or "would_apply" (dry-run).
type ConfigPatchKeyResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// ConfigPatchResult is the response from (*Client).ConfigPatch.
type ConfigPatchResult struct {
	DryRun   bool                   `json:"dry_run"`
	Applied  []ConfigPatchKeyResult `json:"applied"`
	Revision string                 `json:"revision,omitempty"`
}

// ConfigSnapshot fetches a redacted, point-in-time snapshot of the server's
// settings.json via GET /api/config/snapshot. This endpoint requires a
// valid instance-bearer token (see WithBearerToken/WithTokenSupplier) —
// it is gated independently of any other configured server authentication.
func (c *Client) ConfigSnapshot(ctx context.Context) (*ConfigSnapshot, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/config/snapshot"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("config snapshot: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("config snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("config snapshot", resp)
	}

	var result ConfigSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("config snapshot: decode: %w", err)
	}
	return &result, nil
}

// ConfigPatch applies a validated, batched settings.json write via
// POST /api/config/patch. See ConfigPatchRequest for the request shape and
// ConfigPatchResult for per-key outcomes. A revision mismatch surfaces as a
// 409 Conflict (errors.Is(err, ErrConflict)); a rejected/read-only field as
// 403 Forbidden (ErrForbidden); a validation/unknown-field/structure error
// as 400 Bad Request (ErrBadRequest).
func (c *Client) ConfigPatch(ctx context.Context, patch ConfigPatchRequest) (*ConfigPatchResult, error) {
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("config patch: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost, c.apiURL("/api/config/patch"), "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("config patch: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("config patch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("config patch", resp)
	}

	var result ConfigPatchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("config patch: decode: %w", err)
	}
	return &result, nil
}
