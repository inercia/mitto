package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// recordRPCOrder appends a single line ("<method>\t<detail>") to the RPC-order file
// when MOCK_RPC_ORDER_FILE is set. The write is a single O_APPEND syscall so lines
// from concurrent mock processes (e.g. an auxiliary title-generation session sharing
// the same file) cannot interleave. Errors are logged but never fatal.
func (s *MockACPServer) recordRPCOrder(method, detail string) {
	if s.rpcOrderFile == "" {
		return
	}
	f, err := os.OpenFile(s.rpcOrderFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		s.log("Failed to open RPC order file %s: %v", s.rpcOrderFile, err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(method + "\t" + detail + "\n"); err != nil {
		s.log("Failed to write RPC order file %s: %v", s.rpcOrderFile, err)
	}
}

func (s *MockACPServer) handleMessage(line string) error {
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return fmt.Errorf("invalid JSON-RPC: %w", err)
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "session/new", "acp/newSession":
		return s.handleNewSession(req)
	// v0.13.5 renamed session/unstableResumeSession -> session/resume; the legacy
	// method name is kept as an alias so pre-0.13.5 shell fixtures (test_resume.py,
	// test_resume.sh) still exercise the same code path.
	case "session/resume", "session/unstableResumeSession":
		return s.handleResumeSession(req)
	case "session/prompt", "acp/prompt":
		return s.handlePrompt(req)
	case "session/cancel", "acp/cancelPrompt":
		return s.handleCancelPrompt(req)
	case "session/set_mode", "session/setMode", "acp/setSessionMode":
		return s.handleSetSessionMode(req)
	// ACP 0.13 reintroduced a dedicated `session/set_model` RPC (mitto-vd5); Mitto
	// now sends this as the primary path via ClientSideConnection.
	// UnstableSetSessionModel. `session/set_config_option` is retained as the
	// single-shot legacy fallback for pre-0.13-schema agents.
	case "session/set_model":
		return s.handleSetSessionModel(req)
	case "session/set_config_option":
		return s.handleSetSessionConfigOption(req)
	case "shutdown":
		return s.handleShutdown(req)
	default:
		s.log("Unknown method: %s", req.Method)
		return s.sendError(req.ID, -32601, "Method not found", nil)
	}
}

func (s *MockACPServer) handleInitialize(req JSONRPCRequest) error {
	s.initialized = true
	s.log("Initialized")

	result := InitializeResult{
		ProtocolVersion: 1, // ACP protocol version 1
	}
	result.ServerInfo.Name = "mock-acp-server"
	result.ServerInfo.Version = "1.0.0"
	result.Capabilities.Streaming = true
	result.AgentCapabilities.Streaming = true
	result.AgentCapabilities.PromptCapabilities.Image = true
	// Advertise session resume capability
	result.AgentCapabilities.SessionCapabilities = &SessionCapabilities{
		Resume: &SessionResumeCapabilities{},
	}

	return s.sendResponse(req.ID, result)
}

func (s *MockACPServer) handleNewSession(req JSONRPCRequest) error {
	var params NewSessionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	// Failure injection: the first N session/new calls return a JSON-RPC error whose
	// message contains "timeout" so the transient-retry check in PromptWithMeta matches it.
	// No session is created — the retry must redo the full handshake (mitto-8uz).
	s.newSessionCallCount++
	if s.newSessionCallCount <= s.newSessionFailFirst {
		s.log("Injecting session/new failure %d/%d", s.newSessionCallCount, s.newSessionFailFirst)
		return s.sendError(req.ID, -32603, "agent busy: request timeout", nil)
	}

	// MCP-init simulation (mitto-8ul.1). Fired based on env vars regardless of whether
	// the caller populated McpServers, because Mitto attaches MCP globally today so
	// session/new requests carry an empty mcpServers slice. The stderr progress line
	// is what internal/conversation's StartStderrMonitor watches for to invoke the
	// onMCPInitProgress callback; the timeout line triggers fail-fast.
	nServers := len(params.McpServers)
	if nServers == 0 {
		nServers = 1 // synthetic count for the stderr progress message
	}
	if s.mcpInitTimeoutAfterMs > 0 {
		fmt.Fprintf(os.Stderr, "Waiting for %d MCP servers to initialize\n", nServers)
		time.Sleep(time.Duration(s.mcpInitTimeoutAfterMs) * time.Millisecond)
		fmt.Fprintf(os.Stderr, "MCP initialization timed out after %ds\n", s.mcpInitTimeoutAfterMs/1000)
		// Return an error so any client that ignored the stderr signal still gets
		// a deterministic failure. Tests that rely on the stderr abort will have
		// already cancelled their context before we reach this line.
		return s.sendError(req.ID, -32603, "mcp initialization timed out", nil)
	}
	if s.mcpInitDelayMs > 0 {
		fmt.Fprintf(os.Stderr, "Waiting for %d MCP servers to initialize\n", nServers)
		time.Sleep(time.Duration(s.mcpInitDelayMs) * time.Millisecond)
	}

	// Use Cwd (new format) or fallback to WorkingDirectory (legacy)
	workdir := params.Cwd
	if workdir == "" {
		workdir = params.WorkingDirectory
	}

	s.sessionID = fmt.Sprintf("mock-session-%d", time.Now().UnixNano())
	s.currentMode = defaultModes.CurrentModeID // Reset to default mode
	s.currentModel = defaultModelId            // Reset to default model
	s.log("Created session: %s (workdir: %s, mode: %s, model: %s)", s.sessionID, workdir, s.currentMode, s.currentModel)

	// Create session state with modes and the v0.13.5 model config option.
	modes := &SessionModeState{
		CurrentModeID:  s.currentMode,
		AvailableModes: defaultModes.AvailableModes,
	}
	configOptions := []SessionConfigOption{buildModelConfigOption(s.currentModel)}

	// Store session state for resume support
	s.mu.Lock()
	s.sessions[s.sessionID] = &SessionState{
		SessionID:     s.sessionID,
		Modes:         modes,
		ConfigOptions: configOptions,
	}
	s.mu.Unlock()

	// Return session with modes + configOptions (model advertised via configOptions).
	result := NewSessionResult{
		SessionID:     s.sessionID,
		Modes:         modes,
		ConfigOptions: configOptions,
	}

	return s.sendResponse(req.ID, result)
}

// handleResumeSession handles v0.13.5 `session/resume` requests (and the legacy
// `session/unstableResumeSession` alias).
func (s *MockACPServer) handleResumeSession(req JSONRPCRequest) error {
	var params ResumeSessionRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	s.log("Resume session requested: %s (cwd: %s)", params.SessionID, params.Cwd)

	// Check if we have this session
	s.mu.Lock()
	sess, exists := s.sessions[string(params.SessionID)]
	s.mu.Unlock()

	if !exists {
		s.log("Session not found: %s", params.SessionID)
		return s.sendError(req.ID, -32000, fmt.Sprintf("session not found: %s (may have been garbage collected)", params.SessionID), nil)
	}

	// Restore the session as the current session
	s.sessionID = string(params.SessionID)
	if sess.Modes != nil {
		s.currentMode = sess.Modes.CurrentModeID
	}

	// Refresh the model config option with the currently-selected model id so
	// clients see the up-to-date state after a set_config_option round-trip.
	configOptions := freshConfigOptionsWithModel(sess.ConfigOptions, s.currentModel)
	s.log("Resumed session: %s (mode: %s, model: %s)", s.sessionID, s.currentMode, s.currentModel)

	// Return session state without replaying history. `models` (legacy field) is
	// gone; model state travels via configOptions.
	result := ResumeSessionResponse{
		Modes:         sess.Modes,
		ConfigOptions: configOptions,
	}

	return s.sendResponse(req.ID, result)
}

// freshConfigOptionsWithModel returns a copy of opts with the model entry's
// currentValue replaced by the given model id (or the entry appended if
// missing). It never mutates the input slice.
func freshConfigOptionsWithModel(opts []SessionConfigOption, modelId string) []SessionConfigOption {
	out := make([]SessionConfigOption, 0, len(opts)+1)
	foundModel := false
	for _, opt := range opts {
		if opt.Category == "model" || opt.ID == "model" {
			opt.CurrentValue = modelId
			foundModel = true
		}
		out = append(out, opt)
	}
	if !foundModel {
		out = append(out, buildModelConfigOption(modelId))
	}
	return out
}

// handleSetSessionMode handles session mode change requests.
func (s *MockACPServer) handleSetSessionMode(req JSONRPCRequest) error {
	var params SetSessionModeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	// Validate the mode exists
	validMode := false
	for _, mode := range defaultModes.AvailableModes {
		if mode.ID == params.ModeID {
			validMode = true
			break
		}
	}

	if !validMode {
		return s.sendError(req.ID, -32602, fmt.Sprintf("Invalid mode: %s", params.ModeID), nil)
	}

	// Update the current mode
	s.currentMode = params.ModeID
	s.recordRPCOrder("set_mode", params.ModeID)
	s.log("Session mode changed: %s -> %s", s.sessionID, s.currentMode)

	// Send success response
	if err := s.sendResponse(req.ID, SetSessionModeResult{}); err != nil {
		return err
	}

	// Send notification about mode change
	return s.sendCurrentModeUpdate(params.ModeID)
}

// sendCurrentModeUpdate sends a session update notification for mode change.
func (s *MockACPServer) sendCurrentModeUpdate(modeID string) error {
	notification := SessionNotification{
		JSONRPC: "2.0",
		Method:  "session/update",
	}
	notification.Params.SessionID = s.sessionID
	notification.Params.Update = SessionUpdate{
		CurrentModeUpdate: &SessionCurrentModeUpdate{
			SessionUpdate: "current_mode_update",
			CurrentModeID: modeID,
		},
	}

	return s.sendNotification(notification)
}

// handleSetSessionModel handles ACP 0.13 `session/set_model` — Mitto's primary
// model-switch RPC after mitto-vd5. When MOCK_SET_MODEL_FORCE_LEGACY is set,
// the handler returns JSON-RPC -32601 unconditionally to simulate a pre-0.13
// agent, letting integration tests exercise Mitto's legacy-fallback path
// (session/set_config_option) end-to-end. Otherwise the mock applies the same
// MOCK_SET_MODEL_FAIL_FIRST / MOCK_SET_MODEL_DELAY_MS knobs, validates the
// model id against defaultModelOptions, updates currentModel, and emits the
// same config_option_update notification handleSetSessionConfigOption sends so
// AgentModels().CurrentModelId stays wired up.
func (s *MockACPServer) handleSetSessionModel(req JSONRPCRequest) error {
	if s.forceLegacySetModel {
		s.log("session/set_model forced to legacy fallback (MOCK_SET_MODEL_FORCE_LEGACY)")
		return s.sendError(req.ID, -32601, "Method not found", nil)
	}

	var params SetSessionModelParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	if err := s.applyModelChange(req, params.ModelID, "set_model"); err != nil {
		return err
	}

	// SDK's UnstableSetSessionModelResponse carries only _meta; emit an empty
	// envelope so the client's future-typed unmarshal succeeds cleanly.
	if err := s.sendResponse(req.ID, SetSessionModelResult{}); err != nil {
		return err
	}

	// Emit the config_option_update notification so downstream state
	// (Mitto's AgentModels tracking) still transitions — mirrors the
	// legacy-path emission below.
	updated := freshConfigOptionsWithModel(nil, s.currentModel)
	notification := SessionNotification{JSONRPC: "2.0", Method: "session/update"}
	notification.Params.SessionID = s.sessionID
	notification.Params.Update = SessionUpdate{
		ConfigOptionUpdate: &SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: updated,
		},
	}
	return s.sendNotification(notification)
}

// applyModelChange runs the shared MOCK_SET_MODEL_FAIL_FIRST / _DELAY_MS
// failure-injection + validation + currentModel update used by both
// session/set_model (primary) and session/set_config_option(model) (legacy
// fallback). Returns a non-nil error only when the caller has already sent an
// error response and should stop (i.e. do not send a success response).
// rpcTag is the label used in the RPC-order log ("set_model" | "set_config_option").
func (s *MockACPServer) applyModelChange(req JSONRPCRequest, value, rpcTag string) error {
	// Optional delay to simulate a slow agent (MOCK_SET_MODEL_DELAY_MS).
	if s.setModelDelayMs > 0 {
		time.Sleep(time.Duration(s.setModelDelayMs) * time.Millisecond)
	}

	// Failure injection: first N calls return a "timeout" error so
	// isRetryableSetModelError matches. currentModel is NOT updated and no
	// notification is sent — the retry must redo it. Shared counter across
	// primary+legacy so a single MOCK_SET_MODEL_FAIL_FIRST budget is honoured
	// regardless of which wire the RPC arrives on.
	s.setModelCallCount++
	if s.setModelCallCount <= s.setModelFailFirst {
		s.log("Injecting %s(model) failure %d/%d for value %s",
			rpcTag, s.setModelCallCount, s.setModelFailFirst, value)
		return s.sendError(req.ID, -32603, "agent busy: request timeout", nil)
	}

	// Validate the value against the advertised options.
	validModel := false
	for _, opt := range defaultModelOptions {
		if opt.Value == value {
			validModel = true
			break
		}
	}
	if !validModel {
		return s.sendError(req.ID, -32602, fmt.Sprintf("Invalid model: %s", value), nil)
	}
	s.currentModel = value

	s.recordRPCOrder(rpcTag, value)
	s.log("Session model changed via %s: %s -> %s", rpcTag, s.sessionID, value)
	return nil
}

// handleSetSessionConfigOption handles v0.13.5 `session/set_config_option`
// requests. Only the ValueId (Select) variant is supported; the boolean variant
// is rejected. For the "model" category the mock retains the pre-0.13.5
// failure-injection / delay knobs (MOCK_SET_MODEL_FAIL_FIRST /
// MOCK_SET_MODEL_DELAY_MS) so TestConcurrentModelSetBurst still exercises the
// retry path. Other categories (e.g. custom config options) fall through with
// no side effects beyond echoing the value back.
func (s *MockACPServer) handleSetSessionConfigOption(req JSONRPCRequest) error {
	var params SetSessionConfigOptionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}
	if params.Type == "boolean" {
		return s.sendError(req.ID, -32602, "Boolean config-option variant is not supported by the mock", nil)
	}

	isModel := params.ConfigID == "model"

	if isModel {
		// Shared model-change path: MOCK_SET_MODEL_DELAY_MS / _FAIL_FIRST
		// knobs, model-id validation, currentModel update, RPC-order log.
		// Emits its own error response on failure/rejection.
		if err := s.applyModelChange(req, params.Value, "set_config_option"); err != nil {
			return err
		}
	} else {
		// Non-model config options: just record the arrival and echo back.
		s.recordRPCOrder("set_config_option", params.Value)
		s.log("Session config option changed: %s (%s -> %s)", s.sessionID, params.ConfigID, params.Value)
	}

	// Build the updated ConfigOptions snapshot to include in both the response
	// and the config_option_update notification.
	updated := freshConfigOptionsWithModel(nil, s.currentModel)

	// Send success response (SDK's SetSessionConfigOptionResponse carries the
	// full configOptions snapshot).
	if err := s.sendResponse(req.ID, SetSessionConfigOptionResult{ConfigOptions: updated}); err != nil {
		return err
	}

	// Send the config_option_update notification (v0.13.5 SessionUpdate variant).
	notification := SessionNotification{
		JSONRPC: "2.0",
		Method:  "session/update",
	}
	notification.Params.SessionID = s.sessionID
	notification.Params.Update = SessionUpdate{
		ConfigOptionUpdate: &SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: updated,
		},
	}
	return s.sendNotification(notification)
}

func (s *MockACPServer) handlePrompt(req JSONRPCRequest) error {
	s.log("Prompt raw params: %s", string(req.Params))

	var params PromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.log("Unmarshal error: %v", err)
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	// Count image blocks in the prompt
	var imageBlockCount int
	var imageMimeTypes []string
	for _, block := range params.Prompt {
		if block.IsImage() {
			imageBlockCount++
			imageMimeTypes = append(imageMimeTypes, block.GetMimeType())
		}
	}

	// Extract text message from prompt content blocks
	message := params.Message // Use legacy message field as fallback
	s.log("Prompt blocks: %d, image blocks: %d, legacy message: %q", len(params.Prompt), imageBlockCount, message)
	for _, block := range params.Prompt {
		text := block.GetText()
		if text != "" {
			message = text
			break
		}
	}

	s.log("Prompt received (session=%s): %s", params.SessionID, message)
	s.recordRPCOrder("prompt", message)

	// Route notifications to the correct session.
	// When multiple sessions share this ACP process (e.g. main + auxiliary sessions),
	// s.sessionID may point to the last-created session rather than the prompting one.
	// Use the session ID from the prompt params to ensure correct routing.
	if params.SessionID != "" {
		s.sessionID = params.SessionID
	}

	// If image blocks are present, respond acknowledging them
	if imageBlockCount > 0 {
		response := fmt.Sprintf("I received %d image(s) with types: %s. Text: %s",
			imageBlockCount, strings.Join(imageMimeTypes, ", "), message)
		s.sendSessionUpdate(SessionUpdate{
			AgentMessageChunk: &AgentMessageChunk{
				Content: ContentBlock{Type: "text", Text: response},
			},
		})
		return s.sendResponse(req.ID, PromptResponse{StopReason: "end_turn"})
	}

	// Check for special CRASH command to simulate process crash
	// This is used for testing restart logic
	if strings.HasPrefix(message, "CRASH") {
		s.log("CRASH command received - simulating process crash")
		// Send a brief message before crashing
		s.sendSessionUpdate(SessionUpdate{
			AgentMessageChunk: &AgentMessageChunk{
				Content: ContentBlock{Type: "text", Text: "Simulating crash..."},
			},
		})
		// Exit with non-zero code to simulate crash
		os.Exit(1)
	}

	// Check for special REPLAY: prefix to replay events from a file
	// Format: REPLAY:filename.jsonl
	if strings.HasPrefix(message, "REPLAY:") {
		filename := strings.TrimPrefix(message, "REPLAY:")
		filename = strings.TrimSpace(filename)
		s.log("Replay requested: %s", filename)
		s.executeReplay(filename, s.defaultDelay)
		// Note: end_turn is sent after executeReplay returns, which already
		// includes delays between chunks. No additional delay needed.
		return s.sendResponse(req.ID, PromptResponse{StopReason: "end_turn"})
	}

	// Find matching scenario response
	matched := s.findMatchingResponse(message)
	var actions []Action
	if matched != nil {
		// Apply response-level delay before sending any actions.
		// This allows fixtures to simulate "slow to start" agents, e.g. to test
		// client disconnection mid-stream or timeout behaviours.
		if matched.DelayMs > 0 {
			time.Sleep(time.Duration(matched.DelayMs) * time.Millisecond)
		}
		actions = matched.Actions
	} else {
		// Default response
		actions = []Action{
			{
				Type:   "agent_message",
				Chunks: []string{"I received your message: ", message, "\n\nThis is a mock response."},
			},
		}
	}

	// Execute actions synchronously - streaming happens BEFORE the prompt response
	// This is the ACP protocol: notifications first, then response
	for _, action := range actions {
		s.executeAction(action)
	}

	// If an rpc_error action fired, send an error response instead of end_turn.
	if s.pendingRPCError != "" {
		errMsg := s.pendingRPCError
		s.pendingRPCError = ""
		s.log("Returning RPC error: %s", errMsg)
		return s.sendError(req.ID, -32000, errMsg, nil)
	}

	// Return proper PromptResponse with stopReason (matches SDK's PromptResponse type)
	return s.sendResponse(req.ID, PromptResponse{StopReason: "end_turn"})
}

func (s *MockACPServer) handleCancelPrompt(req JSONRPCRequest) error {
	s.log("Prompt cancelled")
	return s.sendResponse(req.ID, map[string]bool{"success": true})
}

func (s *MockACPServer) handleShutdown(req JSONRPCRequest) error {
	s.log("Shutdown requested")
	return s.sendResponse(req.ID, nil)
}

// findMatchingResponse selects the scripted response for a prompt.
//
// Mitto prepends guidance preambles to prompts (e.g. "[Session Context]", a
// "[Mitto MCP Tools Check]" block, a Beads reminder). Those preambles contain
// incidental phrases — e.g. "...do NOT use markdown TODO lists or ad-hoc task
// files" — that match broad natural-language scenario patterns such as
// file-list's "(?i)(list|show|what).*(files|directory|folder)". A given prompt
// therefore frequently matches MULTIPLE scenarios at once.
//
// Two problems followed from the original implementation: it iterated
// s.scenarios (a Go map, so iteration order is random) and returned the first
// match, making the chosen scenario nondeterministic — the source of flaky UI
// tests where, e.g., a "TEST:code-block-split" prompt intermittently rendered
// the file-list response.
//
// Fix: iterate scenarios in a deterministic (sorted) order and choose the MOST
// SPECIFIC match — the scenario whose pattern matches the SHORTEST span of the
// message. Precise markers like "TEST:code-block-split" match a short exact
// span, whereas greedy ".*" fallbacks match a longer span of boilerplate, so
// the precise scenario wins. Ties are broken by the (sorted) scenario name.
func (s *MockACPServer) findMatchingResponse(message string) *Response {
	names := make([]string, 0, len(s.scenarios))
	for name := range s.scenarios {
		names = append(names, name)
	}
	sort.Strings(names)

	var best *Response
	bestName := ""
	bestSpan := -1
	for _, name := range names {
		scenario := s.scenarios[name]
		for i := range scenario.Responses {
			resp := &scenario.Responses[i]
			if resp.Trigger.Type != "prompt" {
				continue
			}
			re, err := regexp.Compile(resp.Trigger.Pattern)
			if err != nil {
				continue
			}
			loc := re.FindStringIndex(message)
			if loc == nil {
				continue
			}
			span := loc[1] - loc[0]
			if best == nil || span < bestSpan {
				best = resp
				bestName = name
				bestSpan = span
			}
		}
	}
	if best != nil {
		s.log("Matched scenario: %s (span=%d)", bestName, bestSpan)
		return best
	}
	s.log("No matching scenario found")
	return nil
}

func (s *MockACPServer) executeAction(action Action) {
	delay := time.Duration(action.DelayMs) * time.Millisecond
	if delay == 0 {
		delay = s.defaultDelay
	}

	switch action.Type {
	case "agent_message":
		for _, chunk := range action.Chunks {
			s.sendSessionUpdate(SessionUpdate{
				AgentMessageChunk: &AgentMessageChunk{
					Content: ContentBlock{Type: "text", Text: chunk},
				},
			})
			time.Sleep(delay)
		}

	case "agent_thought":
		s.sendSessionUpdate(SessionUpdate{
			AgentThoughtChunk: &AgentThoughtChunk{
				Content: ContentBlock{Type: "text", Text: action.Text},
			},
		})
		time.Sleep(delay)

	case "tool_call":
		s.sendSessionUpdate(SessionUpdate{
			ToolCall: &ToolCall{
				ToolCallID: action.ID,
				Title:      action.Title,
				Status:     action.Status,
				RawInput:   action.RawInput,
			},
		})
		time.Sleep(delay)

	case "tool_update":
		status := action.Status
		s.sendSessionUpdate(SessionUpdate{
			ToolCallUpdate: &ToolCallUpdate{
				ToolCallID: action.ID,
				Status:     &status,
			},
		})
		time.Sleep(delay)

	case "delay":
		time.Sleep(time.Duration(action.DelayMs) * time.Millisecond)

	case "error":
		s.log("Simulating error (no-op log only): %s", action.Message)

	case "rpc_error":
		// Set the pending RPC error; handlePrompt will send an error response after all actions.
		msg := action.Message
		if msg == "" {
			msg = "Simulated error for testing"
		}
		s.pendingRPCError = msg
		s.log("Queued RPC error response: %s", msg)

	case "replay":
		s.executeReplay(action.File, delay)
	}
}

// executeReplay replays events from a JSONL file.
// The file should contain events in the same format as events.jsonl files.
// Only agent_message events are replayed as streaming chunks.
func (s *MockACPServer) executeReplay(filename string, delay time.Duration) {
	// Try to find the file in various locations
	paths := []string{
		filename,
		filepath.Join(s.scenarioDir, filename),
		filepath.Join(s.scenarioDir, "..", "replay", filename),
		filepath.Join("tests/fixtures/replay", filename),
	}

	var file *os.File
	var err error
	for _, path := range paths {
		file, err = os.Open(path)
		if err == nil {
			s.log("Replaying events from: %s", path)
			break
		}
	}
	if file == nil {
		s.log("Could not find replay file %s in any location", filename)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event ReplayEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			s.log("Error parsing replay event: %v", err)
			continue
		}

		// Only replay agent_message events as streaming chunks
		if event.Type == "agent_message" {
			var data ReplayAgentMessageData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				s.log("Error parsing agent_message data: %v", err)
				continue
			}

			// Send the text as a single chunk (preserving exact content)
			s.sendSessionUpdate(SessionUpdate{
				AgentMessageChunk: &AgentMessageChunk{
					Content: ContentBlock{Type: "text", Text: data.Text},
				},
			})
			time.Sleep(delay)
		}
	}

	if err := scanner.Err(); err != nil {
		s.log("Error reading replay file: %v", err)
	}
}
