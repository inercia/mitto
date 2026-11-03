//go:build integration

package inprocess

// End-to-end coverage of the combined agent+workspace hidden-model policy
// (epic mitto-zu4, bead mitto-zu4.5). Product behavior is NOT re-tested per
// layer (internal/config, internal/conversation, internal/acpproc and
// internal/web each carry their own suites); these tests prove the pieces
// together over the real web server, registry, listing endpoint, shared ACP
// process and configuration-save path using the mock ACP RPC order file as
// the wire oracle.
//
// Product code is intentionally untouched by this bead: the whole epic's
// failure modes at integration level are contradictions between layers, not
// single-layer bugs.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

const (
	unionInitialModel = "terra-private"
	unionAgentHidden  = "coding-safe"
	unionWsHidden     = "general-safe"

	unionCatalog = `{"initial_model":"terra-private","options":[{"value":"terra-private","name":"Terra"},{"value":"coding-safe","name":"Coding Safe"},{"value":"general-safe","name":"General Safe"}]}`
)

// unionEnv bundles the per-test state shared by the helpers below.
type unionEnv struct {
	ts       *TestServer
	bs       *conversation.BackgroundSession
	order    string
	attempts string
}

func ptrUnionRules(rules ...string) *[]string { return &rules }

// unionMockBinaryOpt aligns the ACP command with an explicitly injected mock
// binary (see hidden_model_policy_test.go for the original pattern).
func unionMockBinaryOpt(c *web.Config) {
	if binary := os.Getenv("INPROCESS_MOCK_ACP_BINARY"); binary != "" {
		c.ACPCommand = binary
		c.MittoConfig.ACPServers[0].Command = binary
	}
}

// setupUnion starts an in-process server against the mock ACP with the shared
// three-model catalog, creates one background session on the harness default
// workspace, runs the first prompt and asserts the full initial catalog.
func setupUnion(t *testing.T, opts ...func(*web.Config)) *unionEnv {
	t.Helper()

	dir := t.TempDir()
	fixture := filepath.Join(dir, "models.json")
	order := filepath.Join(dir, "order.log")
	attempts := filepath.Join(dir, "attempts.jsonl")
	if err := os.WriteFile(fixture, []byte(unionCatalog), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOCK_MODEL_FIXTURE_FILE", fixture)
	t.Setenv("MOCK_RPC_ORDER_FILE", order)
	t.Setenv("MOCK_MODEL_ATTEMPT_FILE", attempts)

	ts := SetupTestServer(t, append([]func(*web.Config){unionMockBinaryOpt}, opts...)...)
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "workspace-hidden-models"})
	if err != nil {
		t.Fatal(err)
	}
	var bs *conversation.BackgroundSession
	waitFor(t, 15*time.Second, func() bool {
		bs = ts.Server.GetSessionManager().GetSession(sess.SessionID)
		return bs != nil
	}, "session created")

	promptOnce(t, bs)
	if bs.AgentModels() == nil || len(bs.AgentModels().AvailableModels) != 3 {
		t.Fatalf("initial catalog: %+v", bs.AgentModels())
	}
	return &unionEnv{ts: ts, bs: bs, order: order, attempts: attempts}
}

// promptOnce sends the seed prompt and waits for the turn to complete, mirroring
// hidden_model_policy_test.go.
func promptOnce(t *testing.T, bs *conversation.BackgroundSession) {
	t.Helper()
	done := make(chan error, 1)
	if err := bs.PromptWithMeta("hello", conversation.PromptMeta{OnComplete: func(err error) { done <- err }}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("prompt timeout")
	}
}

// defaultUnionWorkspaceDir is the working directory of the harness default
// workspace (tempDir/workspace).
func defaultUnionWorkspaceDir(u *unionEnv) string {
	return filepath.Join(u.ts.TempDir, "workspace")
}

// unionAPI builds an absolute URL for an /api/** endpoint using the server's
// runtime API prefix (all /api routes register under the prefix, default
// /mitto — see server.routes).
func unionAPI(u *unionEnv, path string) string {
	return u.ts.HTTPServer.URL + u.ts.Server.APIPrefix() + path
}

// getUnionConfig fetches the full runtime config as an untyped map, matching the
// save helper in hidden_model_policy_test.go. Untyped maps preserve empty-slice
// keys (`[]`) across marshal/unmarshal, which struct-based bodies would drop via
// omitempty — critical for the clear-rules scenarios.
func getUnionConfig(t *testing.T, u *unionEnv) map[string]any {
	t.Helper()
	resp, err := http.Get(u.ts.HTTPServer.URL + "/mitto/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cfg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// postUnionConfig POSTs a config body to the save endpoint and returns
// (status, body).
func postUnionConfig(t *testing.T, u *unionEnv, body map[string]any) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(u.ts.HTTPServer.URL+"/mitto/api/config", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	result, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(result)
}

// saveUnionRules saves both hidden-model levels at once. A nil pointer deletes
// the `hidden_models` key from the payload (server-side preservation
// semantics); a non-nil pointer (including an empty slice) sets/replaces it.
func saveUnionRules(t *testing.T, u *unionEnv, agentRules, wsRules *[]string) (int, string) {
	t.Helper()
	cfg := getUnionConfig(t, u)
	servers := cfg["acp_servers"].([]any)
	server0 := servers[0].(map[string]any)
	if agentRules == nil {
		delete(server0, "hidden_models")
	} else {
		server0["hidden_models"] = *agentRules
	}
	workspaces := cfg["workspaces"].([]any)
	ws0 := workspaces[0].(map[string]any)
	if wsRules == nil {
		delete(ws0, "hidden_models")
	} else {
		ws0["hidden_models"] = *wsRules
	}
	body := map[string]any{}
	for _, key := range []string{"workspaces", "acp_servers", "prompts", "ui", "models", "session", "conversations", "permissions"} {
		if value, ok := cfg[key]; ok {
			body[key] = value
		}
	}
	return postUnionConfig(t, u, body)
}

// fetchUnionWorkspaceUUID resolves the server-assigned workspace UUID for the
// harness default workspace via the registry-backed endpoint. UUID assignment
// happens on registry ingest (conversation.SetWorkspaces -> EnsureUUID), so
// this retries until the list endpoint exposes it.
func fetchUnionWorkspaceUUID(t *testing.T, u *unionEnv) string {
	t.Helper()
	var uuid string
	waitFor(t, 10*time.Second, func() bool {
		resp, err := http.Get(unionAPI(u, "/api/workspaces"))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var payload struct {
			Workspaces []struct {
				UUID       string `json:"uuid"`
				WorkingDir string `json:"working_dir"`
			} `json:"workspaces"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return false
		}
		for _, ws := range payload.Workspaces {
			if ws.WorkingDir == defaultUnionWorkspaceDir(u) && ws.UUID != "" {
				uuid = ws.UUID
				return true
			}
		}
		return false
	}, "workspaces endpoint exposes assigned uuid")
	return uuid
}

// unionListingShape mirrors the listing response's additive metadata contract.
type unionListingShape struct {
	Models []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"models"`
	CatalogState        string   `json:"catalog_state"`
	HiddenCount         int      `json:"hidden_count"`
	HiddenSavedIDs      []string `json:"hidden_saved_ids"`
	UnavailableSavedIDs []string `json:"unavailable_saved_ids"`
}

// fetchUnionListing returns the GET /api/workspaces/{uuid}/models response
// alongside the HTTP status. Errors are returned (not fatal) so callers can
// poll tolerantly.
func fetchUnionListing(u *unionEnv, uuid string) (*unionListingShape, int, error) {
	resp, err := http.Get(unionAPI(u, "/api/workspaces/"+uuid+"/models"))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	listing := &unionListingShape{}
	if err := json.Unmarshal(body, listing); err != nil {
		return nil, resp.StatusCode, err
	}
	return listing, resp.StatusCode, nil
}

// unionIDsFromListing extracts the listed model ids.
func unionIDsFromListing(listing *unionListingShape) []string {
	ids := make([]string, 0, len(listing.Models))
	for _, m := range listing.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

// unionIDsFromSession extracts the conversation-visible model ids via the
// BackgroundSession filtered view (the same view conversations dispatch with).
func unionIDsFromSession(bs *conversation.BackgroundSession) []string {
	ms := bs.AgentModels()
	if ms == nil {
		return nil
	}
	ids := make([]string, 0, len(ms.AvailableModels))
	for _, m := range ms.AvailableModels {
		ids = append(ids, m.ModelId)
	}
	return ids
}

// equalUnionSets is an order-insensitive set comparison.
func equalUnionSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		if seen[id] == 0 {
			return false
		}
		seen[id]--
	}
	return true
}

// waitUnionModelSet polls until BOTH views (conversation AgentModels and the
// listing endpoint under the given workspace UUID) show exactly wantIDs. The
// session view refreshes through the config-save push, the listing view is
// computed at request time, so they converge independently.
func waitUnionModelSet(t *testing.T, u *unionEnv, uuid string, wantIDs []string) {
	t.Helper()
	waitFor(t, 15*time.Second, func() bool {
		if !equalUnionSets(unionIDsFromSession(u.bs), wantIDs) {
			return false
		}
		listing, status, err := fetchUnionListing(u, uuid)
		if err != nil || status != http.StatusOK {
			return false
		}
		return equalUnionSets(unionIDsFromListing(listing), wantIDs)
	}, "union model set on session and listing")
	cur := unionIDsFromSession(u.bs)
	if !equalUnionSets(cur, wantIDs) {
		t.Fatalf("session set after wait: %v want %v", cur, wantIDs)
	}
}

// connectUnionWS dials the events socket and consumes the "connected" frame.
func connectUnionWS(t *testing.T, u *unionEnv) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(u.ts.HTTPServer.URL, "http") + "/mitto/api/events"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if err := ws.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message wsTestMessage
	if err := ws.ReadJSON(&message); err != nil || message.Type != "connected" {
		t.Fatalf("events connection: %v %s", err, message.Type)
	}
	return ws
}

// nextUnionModelOptionsEvent reads frames until a config_option_changed
// carrying a model-category option set arrives, and returns the visible model
// values from that event.
func nextUnionModelOptionsEvent(t *testing.T, u *unionEnv, conn *websocket.Conn, window time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(window)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("no model-category config_option_changed event within window")
		}
		if err := conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			t.Fatal(err)
		}
		var message wsTestMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("websocket read: %v", err)
		}
		if message.Type != "config_option_changed" {
			continue
		}
		var payload struct {
			ConfigOptions []conversation.SessionConfigOption `json:"config_options"`
		}
		if err := json.Unmarshal(message.Data, &payload); err != nil {
			t.Fatal(err)
		}
		for _, option := range payload.ConfigOptions {
			if option.Category != "model" || len(option.Options) == 0 {
				continue
			}
			values := make([]string, 0, len(option.Options))
			for _, value := range option.Options {
				values = append(values, value.Value)
			}
			return values
		}
	}
}

// nextUnionModelOptionsEventMatching keeps consuming config_option_changed
// frames until one carries a model-category option set that satisfies match.
// A combined agent+workspace save broadcasts per-level refreshes (agent
// publication and workspace push race), so the FIRST event can be an
// intermediate state; callers asserting the final union must match on the
// event that settles.
func nextUnionModelOptionsEventMatching(t *testing.T, u *unionEnv, conn *websocket.Conn, window time.Duration, match func(values []string) bool) []string {
	t.Helper()
	deadline := time.Now().Add(window)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("no model-category config_option_changed event satisfying the expectation within window")
		}
		if err := conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			t.Fatal(err)
		}
		var message wsTestMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("websocket read: %v", err)
		}
		if message.Type != "config_option_changed" {
			continue
		}
		var payload struct {
			ConfigOptions []conversation.SessionConfigOption `json:"config_options"`
		}
		if err := json.Unmarshal(message.Data, &payload); err != nil {
			t.Fatal(err)
		}
		for _, option := range payload.ConfigOptions {
			if option.Category != "model" || len(option.Options) == 0 {
				continue
			}
			values := make([]string, 0, len(option.Options))
			for _, value := range option.Options {
				values = append(values, value.Value)
			}
			if match(values) {
				return values
			}
		}
	}
}

// drainUnionWSModelEvents reports whether any config_option_changed carrying a
// non-empty model-category option set arrives within the window (timeout is a
// normal end-of-window condition, not an error).
func drainUnionWSModelEvents(conn *websocket.Conn, window time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(window))
	for {
		var message wsTestMessage
		if err := conn.ReadJSON(&message); err != nil {
			return false
		}
		if message.Type != "config_option_changed" {
			continue
		}
		var payload struct {
			ConfigOptions []conversation.SessionConfigOption `json:"config_options"`
		}
		if err := json.Unmarshal(message.Data, &payload); err != nil {
			continue
		}
		for _, option := range payload.ConfigOptions {
			if option.Category == "model" && len(option.Options) > 0 {
				return true
			}
		}
	}
}

// assertUnionListing asserts the listing response's exact additive metadata.
func assertUnionListing(t *testing.T, u *unionEnv, uuid string, wantIDs []string, wantState string, wantHidden int) *unionListingShape {
	t.Helper()
	listing, status, err := fetchUnionListing(u, uuid)
	if err != nil || status != http.StatusOK {
		t.Fatalf("listing: status %d err %v", status, err)
	}
	if !equalUnionSets(unionIDsFromListing(listing), wantIDs) {
		t.Fatalf("listing models: %v want %v", unionIDsFromListing(listing), wantIDs)
	}
	if listing.CatalogState != wantState {
		t.Fatalf("catalog_state: %q want %q", listing.CatalogState, wantState)
	}
	if listing.HiddenCount != wantHidden {
		t.Fatalf("hidden_count: %d want %d", listing.HiddenCount, wantHidden)
	}
	return listing
}

// unionPromptWireModels extracts the prompt_model lines for the given session.
func unionPromptWireModels(t *testing.T, u *unionEnv) []string {
	t.Helper()
	var models []string
	for _, line := range readRPCOrder(t, u.order) {
		if model, ok := strings.CutPrefix(line, "prompt_model\t"+u.bs.GetACPID()+"\t"); ok {
			models = append(models, model)
		}
	}
	return models
}

// readUnionAttempts returns the attempts file content (empty when missing).
func readUnionAttempts(t *testing.T, u *unionEnv) []byte {
	t.Helper()
	data, err := os.ReadFile(u.attempts)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}

// assertNoHiddenOnWireAnywhere scans the ENTIRE rpc-order history and the
// attempts file for a hidden id — suitable when that id was never
// legitimately dispatched at any point in the test (only the seed prompt's
// initial model is exempt from the baseline-scoped variant).
func assertNoHiddenOnWireAnywhere(t *testing.T, u *unionEnv, hidden ...string) {
	t.Helper()
	for _, line := range readRPCOrder(t, u.order) {
		if !strings.HasPrefix(line, "prompt_model\t") && !strings.HasPrefix(line, "set_model\t") {
			continue
		}
		for _, id := range hidden {
			if strings.HasSuffix(line, "\t"+id) {
				t.Fatalf("hidden model reached the agent over the wire: %s", line)
			}
		}
	}
	attempts := readUnionAttempts(t, u)
	for _, id := range hidden {
		if bytes.Contains(attempts, []byte(`"model":"`+id+`"`)) {
			t.Fatalf("hidden model in dispatch attempts: %s", attempts)
		}
	}
}

// unionWireBaseline caps the guard at the traffic produced before the hide
// landed (the seed prompt legitimately uses the eventual-hidden initial
// model), so only post-hide traffic is judged.
type unionWireBaseline struct {
	orderLines    int
	attemptsBytes int
}

func captureUnionWireBaseline(t *testing.T, u *unionEnv) unionWireBaseline {
	t.Helper()
	return unionWireBaseline{
		orderLines:    len(readRPCOrder(t, u.order)),
		attemptsBytes: len(readUnionAttempts(t, u)),
	}
}

// assertNoHiddenOnWireAfter applies the R1-wire guard to traffic beyond the
// captured baseline: after the hide, no set_model attempt or prompt_model
// line from ANY session may carry a hidden model, and no new dispatch
// attempt may target a hidden id.
func assertNoHiddenOnWireAfter(t *testing.T, u *unionEnv, baseline unionWireBaseline, hidden ...string) {
	t.Helper()
	lines := readRPCOrder(t, u.order)
	if len(lines) < baseline.orderLines {
		t.Fatal("rpc order file truncated under observation")
	}
	for _, line := range lines[baseline.orderLines:] {
		if !strings.HasPrefix(line, "prompt_model\t") && !strings.HasPrefix(line, "set_model\t") {
			continue
		}
		for _, id := range hidden {
			if strings.HasSuffix(line, "\t"+id) {
				t.Fatalf("hidden model reached the agent over the wire: %s", line)
			}
		}
	}
	attempts := readUnionAttempts(t, u)
	if len(attempts) < baseline.attemptsBytes {
		t.Fatalf("dispatch attempts file truncated under observation")
	}
	for _, id := range hidden {
		if bytes.Contains(attempts[baseline.attemptsBytes:], []byte(`"model":"`+id+`"`)) {
			t.Fatalf("hidden model in dispatch attempts: %s", attempts)
		}
	}
}

// ---------------------------------------------------------------------------
// T1: scenario (1) — workspace rules + agent rules on the same workspace are
// filtered as a union everywhere (session view, listing, and WS push event).
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsUnionModelSet(t *testing.T) {
	u := setupUnion(t)
	uuid := fetchUnionWorkspaceUUID(t, u)

	assertUnionListing(t, u, uuid, []string{unionInitialModel, unionAgentHidden, unionWsHidden}, "available", 0)

	agent := ptrUnionRules("terra")
	wsRules := ptrUnionRules("coding")

	// Dial the events socket BEFORE the save: the config_option_changed
	// broadcast fires while the save is processed and is read here; dialing
	// after the save (post-waitUnionModelSet) would miss the broadcast.
	conn := connectUnionWS(t, u)
	baseline := captureUnionWireBaseline(t, u)
	if status, body := saveUnionRules(t, u, agent, wsRules); status != http.StatusOK {
		t.Fatalf("combined save: %d %s", status, body)
	}

	want := []string{unionWsHidden}

	// The WS push settles on the union, not just one level. Combined saves
	// broadcast per-level refreshes; keep consuming until the event matches
	// the final union set (which also proves neither hidden id re-appears).
	nextUnionModelOptionsEventMatching(t, u, conn, 15*time.Second, func(values []string) bool {
		return equalUnionSets(values, want)
	})

	assertNoHiddenOnWireAfter(t, u, baseline, unionInitialModel, unionAgentHidden)
}

// ---------------------------------------------------------------------------
// T2: scenario (2) — the listing endpoint matches the conversation-visible set
// and hidden_count is raw - selectable even when levels overlap.
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsListingParity(t *testing.T) {
	u := setupUnion(t)
	uuid := fetchUnionWorkspaceUUID(t, u)

	// Disjoint levels: two hidden rows, one visible; hidden_count must be 2
	// (no cross-level double counting).
	agent := ptrUnionRules("terra")
	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, agent, wsRules); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	waitUnionModelSet(t, u, uuid, []string{unionWsHidden})
	listing := assertUnionListing(t, u, uuid, []string{unionWsHidden}, "available", 2)
	if listing.HiddenSavedIDs != nil || listing.UnavailableSavedIDs != nil {
		t.Fatalf("unexpected saved ids without overrides: %+v", listing)
	}

	// One level alone hiding everything the other leaves: policy_empty.
	wsRules = ptrUnionRules("coding", "^g.*")
	if status, body := saveUnionRules(t, u, nil, wsRules); status != http.StatusOK {
		t.Fatalf("save (overload): %d %s", status, body)
	}
	waitFor(t, 15*time.Second, func() bool {
		got := unionIDsFromSession(u.bs)
		if len(got) != 0 {
			return false
		}
		l, status, err := fetchUnionListing(u, uuid)
		return err == nil && status == http.StatusOK && l.CatalogState == "policy_empty" && l.HiddenCount == 3
	}, "union empties the catalog")
	assertUnionListing(t, u, uuid, nil, "policy_empty", 3)

	// A row hidden by BOTH levels at once counts once (no double filtering).
	agent = ptrUnionRules("^terra")
	wsRules = ptrUnionRules("^terra-private$")
	if status, body := saveUnionRules(t, u, agent, wsRules); status != http.StatusOK {
		t.Fatalf("save (overlap): %d %s", status, body)
	}
	want := []string{unionAgentHidden, unionWsHidden}
	waitUnionModelSet(t, u, uuid, want)
	assertUnionListing(t, u, uuid, want, "available", 1)
}

// ---------------------------------------------------------------------------
// T3: scenario (3) — an invalid rule is rejected at save time with the
// workspace-scoped error and the running session/view stays untouched; no
// refresh event and no hidden ids on the wire.
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsInvalidSaveRejected(t *testing.T) {
	u := setupUnion(t)
	uuid := fetchUnionWorkspaceUUID(t, u)

	agent := ptrUnionRules("terra")
	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, agent, wsRules); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	waitUnionModelSet(t, u, uuid, []string{unionWsHidden})
	before := readUnionAttempts(t, u)

	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(u.ts.HTTPServer.URL, "http")+"/mitto/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	if err := ws.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var connect wsTestMessage
	if err := ws.ReadJSON(&connect); err != nil || connect.Type != "connected" {
		t.Fatalf("events connect: %v %s", err, connect.Type)
	}

	// Invalid workspace-level rule.
	invalidWS := ptrUnionRules("(")
	status, body := saveUnionRules(t, u, nil, invalidWS)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid ws save: %d %s", status, body)
	}
	var errBody struct {
		Error struct {
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Details["field"] != "workspaces[0].hidden_models[0]" {
		t.Fatalf("workspace field detail: %s", body)
	}

	// Session and listing untouched, and no model refresh broadcast.
	waitUnionModelSet(t, u, uuid, []string{unionWsHidden})
	if drainUnionWSModelEvents(ws, 3*time.Second) {
		t.Fatalf("model refresh broadcast fired for invalid save")
	}

	// Invalid agent-level rule.
	invalidAgent := ptrUnionRules("(")
	status, body = saveUnionRules(t, u, invalidAgent, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid agent save: %d %s", status, body)
	}
	if err := json.Unmarshal([]byte(body), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Details["field"] != "acp_servers[0].hidden_models[0]" {
		t.Fatalf("agent field detail: %s", body)
	}
	waitUnionModelSet(t, u, uuid, []string{unionWsHidden})
	if after := readUnionAttempts(t, u); !bytes.Equal(before, after) {
		// Invalid saves must not reach the agent: any attempts delta is a failure.
		t.Fatalf("attempts mutated by invalid save: before %d bytes after %d bytes", len(before), len(after))
	}
	if err := u.bs.SetConfigOption(context.Background(), "model", unionAgentHidden); err == nil {
		t.Fatal("stale hidden selection accepted after invalid-save round")
	}
	assertNoHiddenOnWireAnywhere(t, u, unionAgentHidden)
}

// ---------------------------------------------------------------------------
// T4: scenario (4, live part) — changing workspace rules through a config save
// updates the running session and the listing without any restart, and a save
// that OMITS the hidden_models key preserves the existing rules.
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsLiveEditWithoutRestart(t *testing.T) {
	u := setupUnion(t)
	uuid := fetchUnionWorkspaceUUID(t, u)

	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, nil, wsRules); status != http.StatusOK {
		t.Fatalf("save (hide): %d %s", status, body)
	}
	waitUnionModelSet(t, u, uuid, []string{unionInitialModel, unionWsHidden})

	// Live edit: swap which row is hidden, no restart. The events socket is
	// dialed BEFORE the save so the push event is not missed.
	conn := connectUnionWS(t, u)
	wsRules = ptrUnionRules("general")
	if status, body := saveUnionRules(t, u, nil, wsRules); status != http.StatusOK {
		t.Fatalf("save (swap): %d %s", status, body)
	}
	want := []string{unionInitialModel, unionAgentHidden}
	waitUnionModelSet(t, u, uuid, want)

	// The WS push reflects the swap, not just one level.
	values := nextUnionModelOptionsEvent(t, u, conn, 15*time.Second)
	for _, hidden := range []string{unionWsHidden} {
		for _, value := range values {
			if value == hidden {
				t.Fatalf("post-edit WS options re-expose %q: %v", hidden, values)
			}
		}
	}
	if !equalUnionSets(values, want) {
		t.Fatalf("post-edit WS options: %v want %v", values, want)
	}

	// Omission probe: deleting the hidden_models KEY from the save body must
	// PRESERVE the running rules (nil != empty).
	if status, body := saveUnionRules(t, u, nil, nil); status != http.StatusOK {
		t.Fatalf("save (omit): %d %s", status, body)
	}
	waitUnionModelSet(t, u, uuid, want)
	assertUnionListing(t, u, uuid, want, "available", 1)
}

// ---------------------------------------------------------------------------
// T5: scenario (4, clear part) — explicit empty rules make the previously
// hidden row selectable again everywhere and keep the current model pinned.
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsClearRulesRestores(t *testing.T) {
	u := setupUnion(t)
	uuid := fetchUnionWorkspaceUUID(t, u)

	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, nil, wsRules); status != http.StatusOK {
		t.Fatalf("save (hide): %d %s", status, body)
	}
	waitUnionModelSet(t, u, uuid, []string{unionInitialModel, unionWsHidden})
	if current := u.bs.AgentModels().CurrentModelId; current != unionInitialModel {
		t.Fatalf("pre-clear current model drifted: %q", current)
	}

	// Explicit empty list (NOT a nil pointer): ptrUnionRules() with zero args
	// would be a nil slice and marshal as JSON `null`, which the server
	// interprets as "omitted → preserve". `&[]string{}` marshals as `[]`,
	// which is the clear instruction.
	cleared := &[]string{}
	if status, body := saveUnionRules(t, u, nil, cleared); status != http.StatusOK {
		t.Fatalf("save (clear): %d %s", status, body)
	}
	want := []string{unionInitialModel, unionAgentHidden, unionWsHidden}
	waitUnionModelSet(t, u, uuid, want)
	assertUnionListing(t, u, uuid, want, "available", 0)

	if current := u.bs.AgentModels().CurrentModelId; current != unionInitialModel {
		t.Fatalf("unhide switched current model: %q", current)
	}
}

// ---------------------------------------------------------------------------
// T6: scenario (5a) — workspace removal keeps the session on its last-known
// rules while the listing endpoint reports the workspace as gone.
//
// Spec deviation (documented): the HTTP deletion paths (config-save removal
// and DELETE /api/workspaces) are guarded by the conversation-in-use check at
// integration level — a live session on the directory makes removal a 409 by
// design. T6 first asserts that guard, then drives the same propagation
// channel production code uses (applyConfigChanges -> SessionManager
// .SetWorkspaces) directly with an empty workspace list. Sticky behavior of
// the session and the 404 of the listing are the same consequences either
// propagation path would produce.
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsWorkspaceDeletionSticky(t *testing.T) {
	u := setupUnion(t)
	uuid := fetchUnionWorkspaceUUID(t, u)

	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, nil, wsRules); status != http.StatusOK {
		t.Fatalf("save (hide): %d %s", status, body)
	}
	waitUnionModelSet(t, u, uuid, []string{unionInitialModel, unionWsHidden})

	// Removal with a live conversation is rejected (workspace-in-use guard).
	delReq, err := http.NewRequest(http.MethodDelete, unionAPI(u, "/api/workspaces?uuid="+uuid), nil)
	if err != nil {
		t.Fatal(err)
	}
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, delResp.Body)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusConflict {
		t.Fatalf("delete with live conversation: %d want 409", delResp.StatusCode)
	}

	// Propagate removal through the same channel applyConfigChanges uses.
	u.ts.Server.GetSessionManager().SetWorkspaces(nil)
	waitFor(t, 10*time.Second, func() bool {
		resp, err := http.Get(unionAPI(u, "/api/workspaces"))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var payload struct {
			Workspaces []struct {
				UUID string `json:"uuid"`
			} `json:"workspaces"`
		}
		if json.NewDecoder(resp.Body).Decode(&payload) != nil {
			return false
		}
		return len(payload.Workspaces) == 0
	}, "registry reports no workspaces")

	// Listing for the deleted workspace is gone.
	if _, status, err := fetchUnionListing(u, uuid); err != nil || status != http.StatusNotFound {
		t.Fatalf("listing for removed workspace: %d %v", status, err)
	}

	// The session keeps its last-known rules (sticky policy).
	time.Sleep(300 * time.Millisecond)
	if got := unionIDsFromSession(u.bs); !equalUnionSets(got, []string{unionInitialModel, unionWsHidden}) {
		t.Fatalf("session lost rules after workspace deletion: %v", got)
	}

	assertNoHiddenOnWireAnywhere(t, u, unionAgentHidden)
}

// ---------------------------------------------------------------------------
// T7: scenario (5b) — re-adding a workspace for the same directory+server
// gives it its own rules under a fresh UUID, and the session follows the NEW
// rules (previous rules are not stuck into the new workspace).
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsSameDirRebindGetsOwnRules(t *testing.T) {
	u := setupUnion(t)
	oldUUID := fetchUnionWorkspaceUUID(t, u)

	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, nil, wsRules); status != http.StatusOK {
		t.Fatalf("save (hide): %d %s", status, body)
	}
	waitUnionModelSet(t, u, oldUUID, []string{unionInitialModel, unionWsHidden})

	// Remove (same channel/rationale as T6).
	u.ts.Server.GetSessionManager().SetWorkspaces(nil)
	waitFor(t, 10*time.Second, func() bool {
		resp, err := http.Get(unionAPI(u, "/api/workspaces"))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var payload struct {
			Workspaces []struct {
				UUID string `json:"uuid"`
			} `json:"workspaces"`
		}
		if json.NewDecoder(resp.Body).Decode(&payload) != nil {
			return false
		}
		return len(payload.Workspaces) == 0
	}, "registry reports no workspaces")

	// Re-add the SAME directory+server with DIFFERENT rules and no UUID via
	// the real config-save path; the registry assigns a fresh UUID. The
	// config GET renders workspaces from the registry, which is empty after
	// the removal above, so the entry is built from scratch rather than
	// mutated from the fetched body.
	cfg := getUnionConfig(t, u)
	type unionWorkspaceEntry struct {
		ACPServer    string   `json:"acp_server"`
		WorkingDir   string   `json:"working_dir"`
		HiddenModels []string `json:"hidden_models"`
	}
	rebound := []unionWorkspaceEntry{{
		ACPServer:    "mock-acp",
		WorkingDir:   defaultUnionWorkspaceDir(u),
		HiddenModels: []string{"general"},
	}}
	body := map[string]any{
		"workspaces": rebound,
	}
	for _, key := range []string{"acp_servers", "prompts", "ui", "models", "session", "conversations", "permissions"} {
		if value, ok := cfg[key]; ok {
			body[key] = value
		}
	}
	if status, respBody := postUnionConfig(t, u, body); status != http.StatusOK {
		t.Fatalf("rebind save: %d %s", status, respBody)
	}

	var newUUID string
	waitFor(t, 15*time.Second, func() bool {
		resp, err := http.Get(unionAPI(u, "/api/workspaces"))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var payload struct {
			Workspaces []struct {
				UUID       string `json:"uuid"`
				WorkingDir string `json:"working_dir"`
				ACPServer  string `json:"acp_server"`
			} `json:"workspaces"`
		}
		if json.NewDecoder(resp.Body).Decode(&payload) != nil {
			return false
		}
		for _, ws := range payload.Workspaces {
			if ws.WorkingDir == defaultUnionWorkspaceDir(u) && ws.ACPServer == "mock-acp" && ws.UUID != "" && ws.UUID != oldUUID {
				newUUID = ws.UUID
				return true
			}
		}
		return false
	}, "rebound workspace exposes a fresh uuid")

	// New rules take effect on both views: coding-safe is visible again and
	// the hidden set follows the NEW workspace rules.
	want := []string{unionInitialModel, unionAgentHidden}
	waitUnionModelSet(t, u, newUUID, want)

	// Old UUID stays gone.
	if _, status, err := fetchUnionListing(u, oldUUID); err != nil || status != http.StatusNotFound {
		t.Fatalf("stale listing for old uuid: %d %v", status, err)
	}
}

// ---------------------------------------------------------------------------
// T8: scenario (6) — auxiliary sessions pick their model under the combined
// union: a model hidden by the workspace level is never dispatched, even
// though the agent level hid a DIFFERENT row.
// ---------------------------------------------------------------------------

func TestWorkspaceHiddenModelsAuxiliaryUnion(t *testing.T) {
	disabled := false
	u := setupUnion(t, func(c *web.Config) {
		// Re-enable aux (SetupTestServer disables ALL auxiliary features via
		// DisableAuxiliaryPrewarm) and disable action buttons, whose
		// follow-up auxiliary session can otherwise win the mock ACP
		// process's single-RPC-slot race (recipe from
		// knowledge_router_close_test.go).
		c.DisableAuxiliaryPrewarm = false
		c.MittoConfig.Conversations = &config.ConversationsConfig{
			ActionButtons: &config.ActionButtonsConfig{Enabled: &disabled},
		}
	})

	auxMgr := u.bs.GetAuxiliaryManager()
	if auxMgr == nil {
		t.Fatal("session carries no auxiliary manager; aux gating may have changed")
	}

	agent := ptrUnionRules("general")
	wsRules := ptrUnionRules("coding")
	if status, body := saveUnionRules(t, u, agent, wsRules); status != http.StatusOK {
		t.Fatalf("save (hide): %d %s", status, body)
	}
	uuid := fetchUnionWorkspaceUUID(t, u)
	waitUnionModelSet(t, u, uuid, []string{unionInitialModel})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	title, err := auxMgr.GenerateTitle(ctx, uuid, "Summarize what the work focused on")
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if strings.TrimSpace(title) == "" {
		t.Fatal("GenerateTitle returned an empty title")
	}

	// Negative oracle across ALL sessions: neither level's hidden id may
	// appear as a prompt model or a set_model target, and the hidden ids
	// never reached set_model attempts either.
	for _, line := range readRPCOrder(t, u.order) {
		if !strings.HasPrefix(line, "prompt_model\t") && !strings.HasPrefix(line, "set_model\t") {
			continue
		}
		for _, hidden := range []string{unionAgentHidden, unionWsHidden} {
			if strings.HasSuffix(line, "\t"+hidden) {
				t.Fatalf("aux dispatch used hidden model: %s", line)
			}
		}
	}
	attempts := readUnionAttempts(t, u)
	for _, hidden := range []string{unionAgentHidden, unionWsHidden} {
		if bytes.Contains(attempts, []byte(`"model":"`+hidden+`"`)) {
			t.Fatalf("hidden model in dispatch attempts: %s", attempts)
		}
	}
}
