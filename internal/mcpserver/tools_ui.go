// Package mcpserver: this file houses the UI-prompter MCP tool handlers
// (mitto_ui_options / mitto_ui_textbox / mitto_ui_form / mitto_ui_notify)
// and the local unified-diff helper used by the textbox handler.
//
// Extracted from server.go as part of the file-decomposition epic
// (mitto-90f.2). Behavior-preserving: every log message, error text,
// control-flow branch, and return value is identical to the pre-extraction
// inline code. The handlers stay as methods on *Server so the existing
// registration in registerSessionScopedTools resolves unchanged.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// UIOptionsItem represents a single option in the unified options menu.
type UIOptionsItem struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// UIOptionsInput is the input for the mitto_ui_options tool.
type UIOptionsInput struct {
	SelfID              string          `json:"self_id"` // YOUR session ID (the caller)
	Question            string          `json:"question"`
	Options             []UIOptionsItem `json:"options"`
	AllowFreeText       bool            `json:"allow_free_text,omitempty"`
	FreeTextPlaceholder string          `json:"free_text_placeholder,omitempty"`
	TimeoutSeconds      int             `json:"timeout_seconds,omitempty"`
}

// UIOptionsOutput is the output for the mitto_ui_options tool.
type UIOptionsOutput struct {
	Selected string `json:"selected,omitempty"`
	Index    int    `json:"index"`
	FreeText string `json:"free_text,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

func (s *Server) handleUIOptions(ctx context.Context, req *mcp.CallToolRequest, input UIOptionsInput) (*mcp.CallToolResult, UIOptionsOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UIOptionsOutput{Index: -1}, permissionError("mitto_ui_options", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate input
	if len(input.Options) == 0 && !input.AllowFreeText {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("at least one option is required (or enable allow_free_text)")
	}
	if len(input.Options) > 10 {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("mitto_ui_options supports at most 10 options (got %d)", len(input.Options))
	}

	// Apply defaults
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}

	question := input.Question
	if question == "" {
		question = "Please select an option:"
	}
	const maxQuestionLen = 500
	if len([]rune(question)) > maxQuestionLen {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf(
			"the question text is too long (%d characters, max %d). Print the detailed context to the user as a regular message first, then call mitto_ui_options with a concise question",
			len([]rune(question)), maxQuestionLen)
	}

	// Generate unique internal request ID for UI prompt
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build options with IDs and descriptions, truncating long text
	const maxLabelLen = 80
	const maxDescLen = 200
	options := make([]UIPromptOption, len(input.Options))
	for i, item := range input.Options {
		label := []rune(item.Label)
		if len(label) > maxLabelLen {
			label = append(label[:maxLabelLen-1], '…')
		}
		desc := []rune(item.Description)
		if len(desc) > maxDescLen {
			desc = append(desc[:maxDescLen-1], '…')
		}
		options[i] = UIPromptOption{
			ID:          fmt.Sprintf("%d", i),
			Label:       string(label),
			Description: string(desc),
		}
	}

	promptReq := UIPromptRequest{
		RequestID:           uiRequestID,
		Type:                UIPromptTypeOptions,
		Question:            question,
		Options:             options,
		TimeoutSeconds:      timeout,
		Blocking:            true,
		AllowFreeText:       input.AllowFreeText,
		FreeTextPlaceholder: input.FreeTextPlaceholder,
	}

	s.logger.Debug("Sending UI options prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"option_count", len(input.Options),
		"allow_free_text", input.AllowFreeText,
		"timeout", timeout)

	defer s.startProgressHeartbeat(ctx, req)()
	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("failed to display UI prompt: %w", err)
	}

	if resp.TimedOut {
		s.logger.Debug("UI options prompt timed out", "session_id", realSessionID)
		return nil, UIOptionsOutput{Index: -1, TimedOut: true}, nil
	}

	// Handle free text response
	if resp.FreeText != "" {
		s.logger.Debug("UI options prompt answered with free text",
			"session_id", realSessionID,
			"free_text", resp.FreeText)
		return nil, UIOptionsOutput{
			Index:    -1,
			FreeText: resp.FreeText,
		}, nil
	}

	var selectedIndex int
	if _, err := fmt.Sscanf(resp.OptionID, "%d", &selectedIndex); err != nil {
		selectedIndex = -1
	}

	s.logger.Debug("UI options prompt answered",
		"session_id", realSessionID,
		"selected", resp.Label,
		"index", selectedIndex)

	return nil, UIOptionsOutput{
		Selected: resp.Label,
		Index:    selectedIndex,
	}, nil
}

func (s *Server) handleUITextbox(ctx context.Context, req *mcp.CallToolRequest, input UITextboxInput) (*mcp.CallToolResult, UITextboxOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UITextboxOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UITextboxOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UITextboxOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UITextboxOutput{}, permissionError("mitto_ui_textbox", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UITextboxOutput{}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate input
	if input.Title == "" {
		return nil, UITextboxOutput{}, fmt.Errorf("title is required")
	}
	if input.Text == "" {
		return nil, UITextboxOutput{}, fmt.Errorf("text is required")
	}
	const maxTextSize = 16 * 1024 // 16KB
	if len(input.Text) > maxTextSize {
		return nil, UITextboxOutput{}, fmt.Errorf("text exceeds maximum size of 16KB (got %d bytes)", len(input.Text))
	}

	// Validate and default result mode
	resultMode := input.ResultMode
	if resultMode == "" {
		resultMode = "text"
	}
	if resultMode != "text" && resultMode != "diff" {
		return nil, UITextboxOutput{}, fmt.Errorf("result must be 'text' or 'diff' (got '%s')", resultMode)
	}

	// Apply timeout default
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 600 // 10 minutes default for text editing
	}

	// Generate unique internal request ID
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build the prompt request
	promptReq := UIPromptRequest{
		RequestID:      uiRequestID,
		Type:           UIPromptTypeTextbox,
		Title:          input.Title,
		Question:       input.Title, // Use title as question for consistency
		Text:           input.Text,
		ResultMode:     resultMode,
		AllowAbort:     true, // Always allow abort
		TimeoutSeconds: timeout,
		Blocking:       true,
	}

	s.logger.Debug("Sending UI textbox prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"title", input.Title,
		"text_length", len(input.Text),
		"result_mode", resultMode,
		"timeout", timeout)

	// Send prompt and wait for response (blocks until user responds or timeout)
	defer s.startProgressHeartbeat(ctx, req)()
	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, UITextboxOutput{}, fmt.Errorf("failed to display UI textbox: %w", err)
	}

	// Handle timeout
	if resp.TimedOut {
		s.logger.Debug("UI textbox prompt timed out", "session_id", realSessionID)
		return nil, UITextboxOutput{TimedOut: true}, nil
	}

	// Handle abort
	if resp.Aborted || resp.OptionID == "abort" {
		s.logger.Debug("UI textbox prompt aborted", "session_id", realSessionID)
		return nil, UITextboxOutput{Aborted: true}, nil
	}

	// Get the edited text from the response
	editedText := resp.FreeText

	// Check if text was changed
	changed := editedText != input.Text

	if !changed {
		s.logger.Debug("UI textbox prompt submitted without changes", "session_id", realSessionID)
		return nil, UITextboxOutput{Changed: false}, nil
	}

	// Compute result based on mode
	var result string
	if resultMode == "diff" {
		result = computeUnifiedDiff(input.Text, editedText, "original", "edited")
	} else {
		result = editedText
	}

	s.logger.Debug("UI textbox prompt submitted with changes",
		"session_id", realSessionID,
		"result_mode", resultMode,
		"original_length", len(input.Text),
		"edited_length", len(editedText))

	return nil, UITextboxOutput{
		Changed: true,
		Result:  result,
	}, nil
}

func (s *Server) handleUIForm(ctx context.Context, req *mcp.CallToolRequest, input UIFormInput) (*mcp.CallToolResult, UIFormOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UIFormOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UIFormOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UIFormOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UIFormOutput{}, permissionError("mitto_ui_form", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UIFormOutput{}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate and sanitize HTML
	if input.Title == "" {
		return nil, UIFormOutput{}, fmt.Errorf("title is required")
	}
	sanitizedHTML, err := sanitizeFormHTML(input.HTML)
	if err != nil {
		return nil, UIFormOutput{}, fmt.Errorf("invalid form HTML: %w", err)
	}

	// Apply timeout default
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 600 // 10 minutes default
	}

	// Generate unique internal request ID
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build the prompt request
	promptReq := UIPromptRequest{
		RequestID:      uiRequestID,
		Type:           UIPromptTypeForm,
		Title:          input.Title,
		Question:       input.Title,
		FormHTML:       sanitizedHTML,
		TimeoutSeconds: timeout,
		Blocking:       true,
	}

	s.logger.Debug("Sending UI form prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"title", input.Title,
		"html_length", len(sanitizedHTML),
		"timeout", timeout)

	// Send prompt and wait for response (blocks until user responds or timeout)
	defer s.startProgressHeartbeat(ctx, req)()
	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, UIFormOutput{}, fmt.Errorf("failed to display UI form: %w", err)
	}

	// Handle timeout
	if resp.TimedOut {
		s.logger.Debug("UI form prompt timed out", "session_id", realSessionID)
		return nil, UIFormOutput{TimedOut: true}, nil
	}

	// Handle cancel
	if resp.Aborted || resp.OptionID == "cancel" {
		s.logger.Debug("UI form prompt cancelled", "session_id", realSessionID)
		return nil, UIFormOutput{Cancelled: true}, nil
	}

	// Parse form values from FreeText (JSON-encoded map[string]string)
	var values map[string]string
	if resp.FreeText != "" {
		if err := json.Unmarshal([]byte(resp.FreeText), &values); err != nil {
			s.logger.Error("Failed to parse form values", "session_id", realSessionID, "error", err)
			return nil, UIFormOutput{}, fmt.Errorf("failed to parse form values: %w", err)
		}
	}

	s.logger.Debug("UI form prompt submitted",
		"session_id", realSessionID,
		"field_count", len(values))

	return nil, UIFormOutput{
		Submitted: true,
		Values:    values,
	}, nil
}

func (s *Server) handleUINotify(_ context.Context, req *mcp.CallToolRequest, input UINotifyInput) (*mcp.CallToolResult, UINotifyOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UINotifyOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UINotifyOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UINotifyOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UINotifyOutput{}, permissionError("mitto_ui_notify", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UINotifyOutput{}, fmt.Errorf("UI notifications are not available (no UI connected)")
	}

	// Validate title
	if input.Title == "" {
		return nil, UINotifyOutput{}, fmt.Errorf("title is required")
	}

	// Validate and default style
	style := input.Style
	switch style {
	case "info", "success", "warning", "error":
		// valid
	case "":
		style = "info"
	default:
		return nil, UINotifyOutput{}, fmt.Errorf("style must be one of: 'info', 'success', 'warning', 'error' (got '%s')", style)
	}

	// Truncate fields to reasonable limits
	const maxTitleLen = 200
	const maxMessageLen = 1000
	title := []rune(input.Title)
	if len(title) > maxTitleLen {
		title = append(title[:maxTitleLen-1], '…')
	}
	message := []rune(input.Message)
	if len(message) > maxMessageLen {
		message = append(message[:maxMessageLen-1], '…')
	}

	notifyReq := UINotifyRequest{
		Title:   string(title),
		Message: string(message),
		Style:   style,
		Sound:   input.Sound,
		Native:  input.Native,
		Sticky:  input.Sticky,
	}

	s.logger.Debug("UI notify dispatched",
		"session_id", realSessionID,
		"title", notifyReq.Title,
		"style", style)

	// Fire-and-forget — UINotify is non-blocking
	if err := reg.uiPrompter.UINotify(notifyReq); err != nil {
		return nil, UINotifyOutput{}, fmt.Errorf("failed to send notification: %w", err)
	}

	return nil, UINotifyOutput{Success: true}, nil
}

// handleWorkspaceUINotify handles the mitto_workspace_ui_notify MCP tool.
// Unlike handleUINotify (which is scoped to a live registered session and its
// UIPrompter), this tool targets a workspace UUID directly and delivers the
// notification via the global events broadcaster. It exists so callers
// running in contexts without a registered MCP session — notably auxiliary
// sessions executing close-phase (conversationClosed) processors — can still
// surface toasts to the user (mitto-6bn).
//
// Delivery path: BroadcastWorkspaceUINotify emits WSMsgTypeNotification with
// a workspace_uuid field; the frontend filters by workspace so only clients
// currently viewing the matching workspace see the toast.
func (s *Server) handleWorkspaceUINotify(_ context.Context, req *mcp.CallToolRequest, input WorkspaceUINotifyInput) (*mcp.CallToolResult, WorkspaceUINotifyOutput, error) {
	// Validate self_id (used for audit/logging; not required to resolve to
	// a live registered session — auxiliary sessions have none).
	if input.SelfID == "" {
		return nil, WorkspaceUINotifyOutput{}, fmt.Errorf("self_id is required")
	}

	// Validate workspace_uuid and resolve workspace metadata.
	if input.WorkspaceUUID == "" {
		return nil, WorkspaceUINotifyOutput{}, fmt.Errorf("workspace_uuid is required")
	}
	if s.sessionManager == nil {
		return nil, WorkspaceUINotifyOutput{}, fmt.Errorf("session manager unavailable")
	}
	ws := s.sessionManager.GetWorkspaceByUUID(input.WorkspaceUUID)
	if ws == nil {
		return nil, WorkspaceUINotifyOutput{}, fmt.Errorf("unknown workspace UUID: %s", input.WorkspaceUUID)
	}

	// Permission check: keyed on the caller's session flags when resolvable.
	// If the caller has no registered session (aux session case — the
	// mitto-6bn motivating case), permission is granted — the workspace_uuid
	// requirement is the safety boundary (a caller cannot broadcast into a
	// workspace it was not spawned into).
	//
	// Deliberately avoid resolveSelfIDWithMCP's Phase-3 correlation wait
	// (up to pendingRequestTimeout, currently 5s): aux sessions are the
	// expected caller and never register a pending request, so paying that
	// stall on every close-phase notify would be a functional regression.
	// Use direct lookup + MCP-session cache only.
	realSessionID := ""
	if reg := s.getSession(input.SelfID); reg != nil {
		realSessionID = input.SelfID
	} else if req != nil && req.Session != nil {
		if cached := s.lookupMCPSession(req.Session.ID()); cached != "" {
			realSessionID = cached
		}
	}
	if realSessionID != "" {
		if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
			return nil, WorkspaceUINotifyOutput{}, permissionError("mitto_workspace_ui_notify", session.FlagCanPromptUser, "Can prompt user")
		}
	}

	// Validate title.
	if input.Title == "" {
		return nil, WorkspaceUINotifyOutput{}, fmt.Errorf("title is required")
	}

	// Validate and default style.
	style := input.Style
	switch style {
	case "info", "success", "warning", "error":
		// valid
	case "":
		style = "info"
	default:
		return nil, WorkspaceUINotifyOutput{}, fmt.Errorf("style must be one of: 'info', 'success', 'warning', 'error' (got '%s')", style)
	}

	// Truncate fields to reasonable limits (mirrors handleUINotify).
	const maxTitleLen = 200
	const maxMessageLen = 1000
	title := []rune(input.Title)
	if len(title) > maxTitleLen {
		title = append(title[:maxTitleLen-1], '…')
	}
	message := []rune(input.Message)
	if len(message) > maxMessageLen {
		message = append(message[:maxMessageLen-1], '…')
	}

	notifyReq := UINotifyRequest{
		Title:   string(title),
		Message: string(message),
		Style:   style,
		Sound:   input.Sound,
		Native:  input.Native,
		Sticky:  input.Sticky,
	}

	s.logger.Debug("Workspace UI notify dispatched",
		"caller_session_id", realSessionID,
		"workspace_uuid", input.WorkspaceUUID,
		"workspace_name", ws.Name,
		"title", notifyReq.Title,
		"style", style)

	s.sessionManager.BroadcastWorkspaceUINotify(input.WorkspaceUUID, ws.Name, ws.WorkingDir, notifyReq)

	return nil, WorkspaceUINotifyOutput{Success: true}, nil
}

// computeUnifiedDiff generates a simple unified diff between two texts.
func computeUnifiedDiff(original, edited, originalName, editedName string) string {
	originalLines := strings.Split(original, "\n")
	editedLines := strings.Split(edited, "\n")

	var result strings.Builder
	fmt.Fprintf(&result, "--- %s\n", originalName)
	fmt.Fprintf(&result, "+++ %s\n", editedName)

	m, n := len(originalLines), len(editedLines)

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if originalLines[i-1] == editedLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	// Backtrack to find the diff operations
	type diffOp struct {
		op   byte // ' ' = context, '-' = remove, '+' = add
		line string
	}
	var ops []diffOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && originalLines[i-1] == editedLines[j-1] {
			ops = append(ops, diffOp{' ', originalLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			ops = append(ops, diffOp{'+', editedLines[j-1]})
			j--
		} else if i > 0 {
			ops = append(ops, diffOp{'-', originalLines[i-1]})
			i--
		}
	}

	// Reverse ops (we built them backwards)
	for left, right := 0, len(ops)-1; left < right; left, right = left+1, right-1 {
		ops[left], ops[right] = ops[right], ops[left]
	}

	// Output all ops with unified diff markers
	for _, op := range ops {
		switch op.op {
		case ' ':
			fmt.Fprintf(&result, " %s\n", op.line)
		case '-':
			fmt.Fprintf(&result, "-%s\n", op.line)
		case '+':
			fmt.Fprintf(&result, "+%s\n", op.line)
		}
	}

	return result.String()
}
