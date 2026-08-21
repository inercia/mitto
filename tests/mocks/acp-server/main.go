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
