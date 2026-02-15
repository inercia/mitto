package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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
	case "session/prompt", "acp/prompt":
		return s.handlePrompt(req)
	case "session/cancel", "acp/cancelPrompt":
		return s.handleCancelPrompt(req)
	case "session/set_mode", "session/setMode", "acp/setSessionMode":
		return s.handleSetSessionMode(req)
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

	return s.sendResponse(req.ID, result)
}

func (s *MockACPServer) handleNewSession(req JSONRPCRequest) error {
	var params NewSessionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	// Use Cwd (new format) or fallback to WorkingDirectory (legacy)
	workdir := params.Cwd
	if workdir == "" {
		workdir = params.WorkingDirectory
	}

	s.sessionID = fmt.Sprintf("mock-session-%d", time.Now().UnixNano())
	s.currentMode = defaultModes.CurrentModeID // Reset to default mode
	s.log("Created session: %s (workdir: %s, mode: %s)", s.sessionID, workdir, s.currentMode)

	// Return session with modes
	result := NewSessionResult{
		SessionID: s.sessionID,
		Modes: &SessionModeState{
			CurrentModeID:  s.currentMode,
			AvailableModes: defaultModes.AvailableModes,
		},
	}

	return s.sendResponse(req.ID, result)
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

func (s *MockACPServer) handlePrompt(req JSONRPCRequest) error {
	s.log("Prompt raw params: %s", string(req.Params))

	var params PromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.log("Unmarshal error: %v", err)
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	// Extract text message from prompt content blocks
	message := params.Message // Use legacy message field as fallback
	s.log("Prompt blocks: %d, legacy message: %q", len(params.Prompt), message)
	for _, block := range params.Prompt {
		text := block.GetText()
		if text != "" {
			message = text
			break
		}
	}

	s.log("Prompt received: %s", message)

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

	// Find matching scenario
	actions := s.findMatchingActions(message)
	if len(actions) == 0 {
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

func (s *MockACPServer) findMatchingActions(message string) []Action {
	for _, scenario := range s.scenarios {
		for _, resp := range scenario.Responses {
			if resp.Trigger.Type == "prompt" {
				re, err := regexp.Compile(resp.Trigger.Pattern)
				if err != nil {
					s.log("Invalid pattern %s: %v", resp.Trigger.Pattern, err)
					continue
				}
				if re.MatchString(message) {
					s.log("Matched scenario: %s", scenario.Name)
					return resp.Actions
				}
			}
		}
	}
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
		s.log("Simulating error: %s", action.Message)

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
