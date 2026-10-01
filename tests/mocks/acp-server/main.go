// Package main implements a mock ACP server for testing Mitto.
// It communicates via stdin/stdout using JSON-RPC, implementing the ACP protocol.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	scenarioDir  string
	defaultDelay time.Duration
	verbose      bool
)

func main() {
	flag.StringVar(&scenarioDir, "scenarios", "", "Directory containing scenario JSON files")
	flag.DurationVar(&defaultDelay, "delay", 50*time.Millisecond, "Default delay between response chunks")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging to stderr")
	flag.Parse()

	// Find scenarios directory
	if scenarioDir == "" {
		// Try to find it relative to the binary or working directory
		candidates := []string{
			"tests/fixtures/responses",
			"../fixtures/responses",
			"../../fixtures/responses",
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				scenarioDir = c
				break
			}
		}
	}

	server := NewMockACPServer(scenarioDir, defaultDelay, verbose)
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// SessionState stores the state of a session for resume support.
type SessionState struct {
	SessionID     string
	Modes         *SessionModeState
	ConfigOptions []SessionConfigOption
}

// MockACPServer implements a mock ACP server
type MockACPServer struct {
	mu              sync.Mutex // Protects sessions map
	sessions        map[string]*SessionState
	scenarios       map[string]*Scenario
	scenarioDir     string
	defaultDelay    time.Duration
	verbose         bool
	sessionID       string
	initialized     bool
	currentMode     string // Current session mode ID
	currentModel    string // Current session model ID
	pendingRPCError string // Set by rpc_error action; causes handlePrompt to return an error response
	reader          *bufio.Reader
	writer          io.Writer

	// setModelFailFirst: the first N set_config_option(model) requests return a
	// JSON-RPC error whose message contains "timeout" to exercise the retry
	// path in SetSessionModel (mitto-3q9). Controlled by env var
	// MOCK_SET_MODEL_FAIL_FIRST (default 0 = no failures injected). The env var
	// name is preserved from the pre-0.13.5 set_model wire for test back-compat.
	// The server's read loop is single-threaded so this counter needs no mutex.
	setModelFailFirst int
	setModelCallCount int

	// setModelDelayMs: time.Sleep before responding to set_config_option(model),
	// simulating slowness. Controlled by env var MOCK_SET_MODEL_DELAY_MS
	// (default 0 = no delay); name preserved for test back-compat.
	setModelDelayMs int

	// forceLegacySetModel: when true, session/set_model returns JSON-RPC
	// -32601 Method not found unconditionally, simulating a pre-0.13-schema
	// agent that only implements the legacy session/set_config_option RPC.
	// Used to exercise Mitto's legacy-fallback path in SharedACPProcess.
	// SetSessionModel (mitto-vd5). Controlled by env var
	// MOCK_SET_MODEL_FORCE_LEGACY (default false).
	forceLegacySetModel bool

	// newSessionFailFirst: the first N session/new requests return a JSON-RPC error whose
	// message contains "timeout" to exercise the deferred-handshake retry path (mitto-8uz).
	// Controlled by env var MOCK_NEW_SESSION_FAIL_FIRST (default 0 = no failures injected).
	newSessionFailFirst int
	newSessionCallCount int

	// rpcOrderFile: when set (env var MOCK_RPC_ORDER_FILE), the server appends one
	// line per relevant inbound RPC ("prompt", "set_config_option", "set_mode")
	// in arrival order, as "<method>\t<detail>". Used by deferred-config tests to
	// assert the relative ordering of prompts and config RPCs. Each line is
	// written with a single O_APPEND write so concurrent mock processes sharing
	// the file (e.g. an auxiliary title-generation session) cannot interleave
	// within a line.
	rpcOrderFile string

	// mcpInitDelayMs: sleep before responding to session/new when the request
	// carries at least one MCP server, and emit the "Waiting for N MCP servers to
	// initialize" progress line on stderr just before the delay. Simulates an
	// agent that blocks session/new until MCP init completes (mitto-8ul.1).
	mcpInitDelayMs int

	// mcpInitTimeoutAfterMs: after this many ms, emit an "MCP initialization
	// timed out after Ns" line on stderr AND return a JSON-RPC error from the
	// pending session/new. Used to prove the client aborts promptly rather than
	// waiting the full RPC deadline (mitto-8ul.1). 0 disables.
	mcpInitTimeoutAfterMs int

	// rejectConfigOption: when set (env var MOCK_REJECT_CONFIG_OPTION, format
	// "<configID>=<value>"), a session/set_config_option request matching that
	// exact id+value pair is rejected with a JSON-RPC error instead of being
	// applied. Used to exercise Mitto's setter-rejection handling for generic
	// (non-model) select options such as thought_level (mitto-3wd). The
	// unchanged snapshot is re-broadcast by the caller on rejection.
	rejectConfigOption string
}

// Default modes provided by the mock server
var defaultModes = &SessionModeState{
	CurrentModeID: "code",
	AvailableModes: []SessionMode{
		{ID: "ask", Name: "Ask", Description: strPtr("Ask questions and get answers without making changes")},
		{ID: "code", Name: "Code", Description: strPtr("Make code changes and modifications")},
		{ID: "architect", Name: "Architect", Description: strPtr("Plan and design system architecture")},
	},
}

// defaultModelOptions is the ungrouped set of models the mock advertises via
// configOptions (category=model). Mitto's ModelStateFromConfigOptions parses
// this on session/new, session/load and session/resume responses.
var defaultModelOptions = []SessionConfigSelectOption{
	{Value: "claude-haiku-4-5", Name: "Haiku 4.5", Description: strPtr("Fast and efficient")},
	{Value: "claude-sonnet-4-6", Name: "Sonnet 4.6", Description: strPtr("Balanced performance")},
	{Value: "claude-opus-4-6", Name: "Opus 4.6", Description: strPtr("Most capable model")},
}

// defaultModelId is the initially-selected model id (matches the pre-0.13.5
// CurrentModelId in the removed SessionModelState).
const defaultModelId = "claude-sonnet-4-6"

// buildModelConfigOption returns the Select-variant ConfigOption representing
// the current model selection, using currentValue as the currentValue field.
func buildModelConfigOption(currentValue string) SessionConfigOption {
	return SessionConfigOption{
		Type:         "select",
		ID:           "model",
		Name:         "Model",
		Description:  "AI model for this session",
		Category:     "model",
		CurrentValue: currentValue,
		Options:      defaultModelOptions,
	}
}

func strPtr(s string) *string { return &s }

// thoughtLevelID and thoughtLevelCategory are deliberately DIFFERENT from any
// real agent's reasoning-effort option id/category (e.g. GitHub Copilot's
// "reasoning_effort"). This proves Mitto's generic select-config-option
// support does not hardcode any specific agent's id — only the "thought_level"
// category string from the ACP spec is a known value, and even that is only
// used by the mock for its own bookkeeping (Mitto itself must not special-case
// it either).
const (
	thoughtLevelID       = "deliberation_depth"
	thoughtLevelCategory = "thought_level"
)

// thoughtLevelOptionsForModel returns the allowed select options and default
// current value for the "deliberation_depth" config option, varying by model
// to exercise full-snapshot add/remove/changed-values semantics:
//   - claude-haiku-4-5: no options at all (nil) -- mimics a real agent (e.g.
//     Claude Haiku under Copilot) dropping the reasoning-effort option
//     entirely for a smaller/faster model.
//   - claude-opus-4-6: a REDUCED 2-value list (no "shallow") with a different
//     default -- exercises the "allowed values changed" snapshot case.
//   - everything else (default/sonnet): the full 3-value list.
func thoughtLevelOptionsForModel(modelId string) ([]SessionConfigSelectOption, string) {
	switch modelId {
	case "claude-haiku-4-5":
		return nil, ""
	case "claude-opus-4-6":
		return []SessionConfigSelectOption{
			{Value: "standard", Name: "Standard", Description: strPtr("Default deliberation depth")},
			{Value: "deep", Name: "Deep", Description: strPtr("Maximum deliberation depth")},
		}, "standard"
	default:
		return []SessionConfigSelectOption{
			{Value: "shallow", Name: "Shallow", Description: strPtr("Minimal deliberation depth")},
			{Value: "standard", Name: "Standard", Description: strPtr("Default deliberation depth")},
			{Value: "deep", Name: "Deep", Description: strPtr("Maximum deliberation depth")},
		}, "standard"
	}
}

// buildThoughtLevelConfigOption returns the Select-variant ConfigOption for
// "deliberation_depth" given the current model and a preferred current value
// (typically carried over from a prior snapshot). Returns nil when the model
// has no deliberation_depth option at all (e.g. claude-haiku-4-5), so callers
// must omit the option from the snapshot entirely in that case.
func buildThoughtLevelConfigOption(modelId string, preferredValue string) *SessionConfigOption {
	options, defaultValue := thoughtLevelOptionsForModel(modelId)
	if options == nil {
		return nil
	}

	value := defaultValue
	for _, opt := range options {
		if opt.Value == preferredValue {
			value = preferredValue
			break
		}
	}

	return &SessionConfigOption{
		Type:         "select",
		ID:           thoughtLevelID,
		Name:         "Deliberation depth",
		Description:  "How much the agent deliberates before responding",
		Category:     thoughtLevelCategory,
		CurrentValue: value,
		Options:      options,
	}
}

// isThoughtLevelOption reports whether opt is the deliberation-depth config
// option, matching on EITHER id or category so that any future rename of the
// mock's id (while keeping the same "thought_level" category) is still
// recognized -- mirroring how a real agent swap could happen without Mitto
// code changes.
func isThoughtLevelOption(opt SessionConfigOption) bool {
	return opt.ID == thoughtLevelID || opt.Category == thoughtLevelCategory
}

// NewMockACPServer creates a new mock ACP server
func NewMockACPServer(scenarioDir string, defaultDelay time.Duration, verbose bool) *MockACPServer {
	server := &MockACPServer{
		sessions:     make(map[string]*SessionState),
		scenarios:    make(map[string]*Scenario),
		scenarioDir:  scenarioDir,
		defaultDelay: defaultDelay,
		verbose:      verbose,
		currentMode:  defaultModes.CurrentModeID, // Initialize with default mode
		currentModel: defaultModelId,             // Initialize with default model
		reader:       bufio.NewReader(os.Stdin),
		writer:       os.Stdout,
	}

	// MOCK_SET_MODEL_FAIL_FIRST: inject failures for the first N
	// set_config_option(model) requests. Used by TestConcurrentModelSetBurst to
	// deterministically exercise the retry path. Env var name kept from the
	// pre-0.13.5 set_model wire for test back-compat.
	if v := os.Getenv("MOCK_SET_MODEL_FAIL_FIRST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			server.setModelFailFirst = n
		}
	}

	// MOCK_SET_MODEL_DELAY_MS: sleep before responding to set_config_option(model),
	// simulating slowness. Name preserved for test back-compat.
	if v := os.Getenv("MOCK_SET_MODEL_DELAY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			server.setModelDelayMs = n
		}
	}

	// MOCK_SET_MODEL_FORCE_LEGACY: when set to a truthy value (1/true/yes),
	// session/set_model returns JSON-RPC -32601 unconditionally so the mock
	// behaves like a pre-0.13-schema agent. Used by
	// TestSetSessionModel_LegacyFallback_PreSchema013 to prove Mitto's
	// legacy-fallback path in SetSessionModel still lands the model change
	// via session/set_config_option (mitto-vd5).
	if v := strings.ToLower(os.Getenv("MOCK_SET_MODEL_FORCE_LEGACY")); v == "1" || v == "true" || v == "yes" {
		server.forceLegacySetModel = true
	}

	// MOCK_NEW_SESSION_FAIL_FIRST: inject failures for the first N session/new requests.
	// Used by TestDeferredHandshake* to exercise the retry path in PromptWithMeta (mitto-8uz).
	if v := os.Getenv("MOCK_NEW_SESSION_FAIL_FIRST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			server.newSessionFailFirst = n
		}
	}

	// MOCK_RPC_ORDER_FILE: append-only log of inbound RPC arrival order.
	server.rpcOrderFile = os.Getenv("MOCK_RPC_ORDER_FILE")

	// MOCK_MCP_INIT_DELAY_MS: delay session/new by this many ms and emit an MCP-init
	// progress line on stderr. Only applies when the request carries MCP servers.
	// Simulates Auggie-style agents that block session/new on MCP handshake (mitto-8ul.1).
	if v := os.Getenv("MOCK_MCP_INIT_DELAY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			server.mcpInitDelayMs = n
		}
	}

	// MOCK_MCP_INIT_TIMEOUT_MS: after this many ms, emit an MCP-init-timeout stderr
	// line and fail the pending session/new. Used to prove fail-fast on the signal
	// (mitto-8ul.1).
	if v := os.Getenv("MOCK_MCP_INIT_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			server.mcpInitTimeoutAfterMs = n
		}
	}

	// MOCK_REJECT_CONFIG_OPTION: reject a specific session/set_config_option
	// request with a JSON-RPC error, in the format "<configID>=<value>"
	// (e.g. "deliberation_depth=deep"). Used to exercise Mitto's handling of a
	// setter rejection: the unchanged config_options snapshot must still be
	// re-broadcast alongside the error.
	server.rejectConfigOption = os.Getenv("MOCK_REJECT_CONFIG_OPTION")

	server.loadScenarios()
	return server
}

func (s *MockACPServer) log(format string, args ...interface{}) {
	if s.verbose {
		fmt.Fprintf(os.Stderr, "[mock-acp] "+format+"\n", args...)
	}
}

func (s *MockACPServer) loadScenarios() {
	if s.scenarioDir == "" {
		s.log("No scenario directory specified, using default responses")
		return
	}

	files, err := filepath.Glob(filepath.Join(s.scenarioDir, "*.json"))
	if err != nil {
		s.log("Error loading scenarios: %v", err)
		return
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			s.log("Error reading %s: %v", file, err)
			continue
		}

		var scenario Scenario
		if err := json.Unmarshal(data, &scenario); err != nil {
			s.log("Error parsing %s: %v", file, err)
			continue
		}

		s.scenarios[scenario.Name] = &scenario
		s.log("Loaded scenario: %s", scenario.Name)
	}
}

// Run starts the server main loop
func (s *MockACPServer) Run() error {
	s.log("Mock ACP server starting...")

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.log("EOF received, shutting down")
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		s.log("Received: %s", line)

		if err := s.handleMessage(line); err != nil {
			s.log("Error handling message: %v", err)
		}
	}
}
