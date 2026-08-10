//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// sdkContractTrace mirrors the JSON document tests/integration/sdkcontract/driver.js
// prints on stdout: an ordered, curated observation trace plus the sorted
// top-level key set of every raw REST response it saw (mitto-7gta.25).
type sdkContractTrace struct {
	Trace   []map[string]interface{} `json:"trace"`
	KeySets map[string][]string      `json:"keySets"`
}

const sharedTestToken = "sdk-contract-smoke-token"

// runGoSDKContractScenario drives pkg/api through the same create/prompt/
// stream/queue/loop scenario as the JS driver, returning a curated trace in
// the identical shape (see sdkContractTrace) so the two can be compared.
func runGoSDKContractScenario(t *testing.T, c *api.Client, workingDir string) sdkContractTrace {
	t.Helper()
	var trace []map[string]interface{}
	record := func(kind string, fields map[string]interface{}) {
		rec := map[string]interface{}{"kind": kind}
		for k, v := range fields {
			rec[k] = v
		}
		trace = append(trace, rec)
	}

	created, err := c.CreateSession(api.CreateSessionRequest{WorkingDir: workingDir})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	record("session_created", map[string]interface{}{"hasId": created.SessionID != ""})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var agentText string
	var haveEventCount bool
	opened := make(chan struct{})
	completed := make(chan struct{})
	sess, err := c.Connect(ctx, created.SessionID, api.SessionCallbacks{
		OnConnected: func(string, string, string) { close(opened) },
		OnAgentMessage: func(html string) {
			agentText += html
		},
		OnPromptComplete: func(int) {
			haveEventCount = true
			close(completed)
		},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("timed out waiting for connected")
	}
	record("ws_connected", nil)

	if err := sess.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := sess.SendPrompt("Hello"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("timed out waiting for prompt_complete")
	}
	record("agent_message", map[string]interface{}{"text": agentText})
	record("prompt_complete", map[string]interface{}{"hasEventCount": haveEventCount})

	queued, err := c.AddToQueue(created.SessionID, "queued message")
	if err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}
	record("queue_added", map[string]interface{}{"hasId": queued.ID != ""})

	listed, err := c.ListQueue(created.SessionID)
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}
	record("queue_listed", map[string]interface{}{"count": float64(listed.Count)})

	if err := c.ClearQueue(created.SessionID); err != nil {
		t.Fatalf("ClearQueue: %v", err)
	}
	record("queue_cleared", nil)

	loopSet, err := c.SetLoop(created.SessionID, api.SetLoopRequest{
		Prompt:    "contract smoke loop",
		Frequency: api.LoopFrequency{Value: 1, Unit: "hours"},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("SetLoop: %v", err)
	}
	// The server omits "trigger" from the response entirely when unset (Go
	// json:",omitempty"), so default to "schedule" here — the same implicit
	// default session.LoopPrompt.EffectiveTriggers() applies server-side —
	// rather than recording the field's mere on-wire presence.
	trigger := loopSet.Trigger
	if trigger == "" {
		trigger = "schedule"
	}
	record("loop_set", map[string]interface{}{"trigger": trigger})

	loopGot, err := c.GetLoop(created.SessionID)
	if err != nil {
		t.Fatalf("GetLoop: %v", err)
	}
	record("loop_get", map[string]interface{}{"enabled": loopGot.Enabled})

	if err := c.DeleteLoop(created.SessionID); err != nil {
		t.Fatalf("DeleteLoop: %v", err)
	}
	record("loop_detached", nil)

	sess.Close()
	if err := c.DeleteSession(created.SessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	record("session_removed", nil)

	return sdkContractTrace{Trace: trace}
}

// runJSSDKContractScenario shells out to `bun run driver.js`, driving the
// identical scenario via web/static/sdk against the same live server.
func runJSSDKContractScenario(t *testing.T, baseURL, wsBaseURL, workingDir string) sdkContractTrace {
	t.Helper()
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found in PATH; skipping JS-SDK side of the contract smoke")
	}
	driverPath := findRepoFile(t, filepath.Join("tests", "integration", "sdkcontract", "driver.js"),
		"sdkcontract/driver.js not found")

	cmd := exec.Command(bunPath, "run", driverPath)
	cmd.Env = append(os.Environ(),
		"MITTO_BASE_URL="+baseURL,
		"MITTO_WS_BASE_URL="+wsBaseURL,
		"MITTO_API_PREFIX=/mitto",
		"MITTO_TOKEN="+sharedTestToken,
		"MITTO_WORKING_DIR="+workingDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("JS SDK driver failed: %v\noutput:\n%s", err, out)
	}

	var result sdkContractTrace
	if jerr := json.Unmarshal(out, &result); jerr != nil {
		t.Fatalf("failed to decode JS driver output: %v\noutput:\n%s", jerr, out)
	}
	return result
}

// TestSDKContract_GoAndJSAgree runs the same create/prompt/stream/queue/loop
// scenario through pkg/api and web/static/sdk against one in-process server
// with the mock ACP agent, and asserts the two curated traces match — the
// shared-contract guarantee from the mitto-rwxq decision that the two
// clients (not just each client and the backend) never drift apart.
func TestSDKContract_GoAndJSAgree(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	mockACPCmd := findMockACPServer(t)

	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: mockACPCmd}},
		Web: config.WebConfig{
			Auth: &config.WebAuth{
				Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
				SharedToken: sharedTestToken,
			},
		},
	}
	webConfig := web.Config{
		Workspaces:              []config.WorkspaceSettings{{ACPServer: "mock-acp", WorkingDir: workspaceDir}},
		ACPCommand:              mockACPCmd,
		ACPServer:               "mock-acp",
		DefaultWorkingDir:       workspaceDir,
		AutoApprove:             true,
		Debug:                   true,
		FromCLI:                 true,
		MittoConfig:             mittoConfig,
		DisableAuxiliaryPrewarm: true,
	}

	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	externalHandler := web.ExternalConnectionMiddleware(srv.Handler())
	httpServer := httptest.NewServer(externalHandler)
	t.Cleanup(httpServer.Close)

	goClient := api.New(httpServer.URL, api.WithBearerToken(sharedTestToken))
	goTrace := runGoSDKContractScenario(t, goClient, workspaceDir)

	wsBaseURL := "ws" + httpServer.URL[len("http"):]
	jsTrace := runJSSDKContractScenario(t, httpServer.URL, wsBaseURL, workspaceDir)

	assertContractTracesMatch(t, goTrace, jsTrace)
	assertResponseShapeSuperset(t, jsTrace.KeySets)
}

// contractStructKeys returns the `json` tag names a Go struct REQUIRES from a
// decoded response — i.e. fields whose tag has no ",omitempty" option, since
// those are the only ones guaranteed present regardless of value. A field
// tagged ",omitempty" is legitimately absent from the wire response when its
// value is the zero value, so it is not a shape-drift signal and must not be
// asserted here. Ignores tags with "-" or unspecified names.
func contractStructKeys(v interface{}) []string {
	t := reflect.TypeOf(v)
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "" {
			continue
		}
		omitempty := false
		for _, opt := range parts[1:] {
			if opt == "omitempty" {
				omitempty = true
				break
			}
		}
		if !omitempty {
			keys = append(keys, name)
		}
	}
	return keys
}

// assertResponseShapeSuperset checks that every field the Go SDK's structs
// require is present in the corresponding raw REST response's key set, as
// observed by the JS driver. This is the asymmetric shape-drift guard from
// the plan: additive backend fields are fine (JS's set can be a strict
// superset); a renamed/removed field the Go struct depends on is not.
func assertResponseShapeSuperset(t *testing.T, keySets map[string][]string) {
	t.Helper()
	checks := map[string]interface{}{
		"session_create": api.SessionInfo{},
		"queue_add":      api.QueuedMessage{},
		"queue_list":     api.QueueListResponse{},
		"loop_set":       api.LoopConfig{},
		"loop_get":       api.LoopConfig{},
	}
	for label, sample := range checks {
		observed := keySets[label]
		if observed == nil {
			t.Errorf("assertResponseShapeSuperset: no observed key set for %q", label)
			continue
		}
		observedSet := make(map[string]bool, len(observed))
		for _, k := range observed {
			observedSet[k] = true
		}
		for _, required := range contractStructKeys(sample) {
			if !observedSet[required] {
				t.Errorf("assertResponseShapeSuperset: %q response is missing field %q required by %T (observed keys: %v)",
					label, required, sample, observed)
			}
		}
	}
}

// assertContractTracesMatch compares the Go and JS traces record-by-record.
// Both traces are curated projections (plan decision 4): volatile fields
// (ids, timestamps, seq) are never recorded, so a plain deep-equal of the
// "kind" sequence plus each record's remaining fields is the right
// comparison — this is what catches the two clients observing different
// backend behaviour for an identical scenario.
func assertContractTracesMatch(t *testing.T, goTrace, jsTrace sdkContractTrace) {
	t.Helper()
	if len(goTrace.Trace) != len(jsTrace.Trace) {
		t.Fatalf("trace length mismatch: go=%d js=%d\ngo=%v\njs=%v",
			len(goTrace.Trace), len(jsTrace.Trace), goTrace.Trace, jsTrace.Trace)
	}
	for i := range goTrace.Trace {
		g, j := goTrace.Trace[i], jsTrace.Trace[i]
		if g["kind"] != j["kind"] {
			t.Errorf("trace[%d].kind mismatch: go=%v js=%v", i, g["kind"], j["kind"])
			continue
		}
		for k, gv := range g {
			jv, ok := j[k]
			if !ok {
				t.Errorf("trace[%d] (%v): js missing field %q (go=%v)", i, g["kind"], k, gv)
				continue
			}
			if !reflect.DeepEqual(gv, jv) {
				t.Errorf("trace[%d] (%v): field %q mismatch: go=%v js=%v", i, g["kind"], k, gv, jv)
			}
		}
	}
}
