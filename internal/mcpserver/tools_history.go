// Package mcpserver: this file houses the mitto_conversation_history MCP tool
// handler and its private helpers, extracted from server.go for readability.
//
// Extracted from server.go as part of the file-decomposition epic
// (mitto-90f.2). Behavior-preserving: every log message, error text,
// control-flow branch, and return value is identical to the pre-extraction
// inline code. The handler stays as a method on *Server so the existing
// registration in registerSessionScopedTools resolves unchanged.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// =============================================================================
// Conversation History Handler
// =============================================================================

const (
	// historyDefaultLastN is the default number of events to return.
	historyDefaultLastN = 50
	// historyMaxLastN is the maximum number of events that can be requested.
	historyMaxLastN = 200
	// historyMaxDataStr is the maximum length of string fields in event data.
	historyMaxDataStr = 2048
)

// handleConversationHistory handles the mitto_conversation_history tool.
// It reads events from a session, applies filters, and returns a paginated result.
func (s *Server) handleConversationHistory(ctx context.Context, req *mcp.CallToolRequest, input ConversationHistoryInput) (*mcp.CallToolResult, ConversationHistoryOutput, error) {
	emptyOut := ConversationHistoryOutput{Events: []ConversationHistoryEvent{}}

	if input.SelfID == "" {
		emptyOut.Error = "self_id is required"
		return nil, emptyOut, nil
	}

	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		emptyOut.Error = fmt.Sprintf("session not found: self_id '%s' could not be resolved", input.SelfID)
		return nil, emptyOut, nil
	}

	reg := s.getSession(realSessionID)
	if reg == nil {
		emptyOut.Error = fmt.Sprintf("session not found or not running: %s", realSessionID)
		return nil, emptyOut, nil
	}

	targetID := input.ConversationID
	if targetID == "" {
		targetID = realSessionID
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		emptyOut.Error = "session store not available"
		return nil, emptyOut, nil
	}

	// Read all events from the target session.
	allEvents, err := store.ReadEvents(targetID)
	if err != nil {
		emptyOut.Error = fmt.Sprintf("failed to read events for session %s: %v", targetID, err)
		return nil, emptyOut, nil
	}

	totalEvents := len(allEvents)

	// Parse optional time-range filters (RFC 3339 absolute, or relative duration "ago").
	var since, until time.Time
	if input.Since != "" {
		t, err := session.ParseHistoryTime(input.Since)
		if err != nil {
			emptyOut.Error = fmt.Sprintf("invalid since: %v", err)
			return nil, emptyOut, nil
		}
		since = t
	}
	if input.Until != "" {
		t, err := session.ParseHistoryTime(input.Until)
		if err != nil {
			emptyOut.Error = fmt.Sprintf("invalid until: %v", err)
			return nil, emptyOut, nil
		}
		until = t
	}

	// Apply filters.
	filtered := historyFilterEvents(allEvents, input, since, until)

	// Apply pagination: last_n and offset.
	// "last_n" means we return the most recent N events.
	// "offset" pages backward: offset=0 returns the last N, offset=N returns the previous N.
	lastN := input.LastN
	if lastN <= 0 {
		lastN = historyDefaultLastN
	} else if lastN > historyMaxLastN {
		lastN = historyMaxLastN
	}

	offset := input.Offset
	if offset < 0 {
		offset = 0
	}

	totalFiltered := len(filtered)

	// Calculate window: from the end of filtered, skipping `offset` items.
	endIdx := totalFiltered - offset
	if endIdx < 0 {
		endIdx = 0
	}
	startIdx := endIdx - lastN
	if startIdx < 0 {
		startIdx = 0
	}
	hasMore := startIdx > 0

	paginated := filtered[startIdx:endIdx]

	// Determine whether to include full event data.
	includeData := input.IncludeData == nil || *input.IncludeData

	// Build response events.
	events := make([]ConversationHistoryEvent, 0, len(paginated))
	for _, e := range paginated {
		histEvent := historyBuildEvent(e, includeData)
		events = append(events, histEvent)
	}

	return nil, ConversationHistoryOutput{
		Success:        true,
		ConversationID: targetID,
		TotalEvents:    totalEvents,
		ReturnedEvents: len(events),
		Events:         events,
		HasMore:        hasMore,
	}, nil
}

// historyFilterEvents applies all configured filters to the event list.
// since and until are optional time-range bounds (zero value means unset):
// events before `since` or after `until` are excluded.
func historyFilterEvents(events []session.Event, input ConversationHistoryInput, since, until time.Time) []session.Event {
	// Build event type set for O(1) lookup.
	var typeSet map[string]bool
	if len(input.EventTypes) > 0 {
		typeSet = make(map[string]bool, len(input.EventTypes))
		for _, t := range input.EventTypes {
			typeSet[t] = true
		}
	}

	textContainsLower := strings.ToLower(input.TextContains)
	textExcludesLower := strings.ToLower(input.TextExcludes)
	toolNameLower := strings.ToLower(input.ToolName)

	var result []session.Event
	for _, e := range events {
		// Seq range filters.
		if input.AfterSeq > 0 && e.Seq <= input.AfterSeq {
			continue
		}
		if input.BeforeSeq > 0 && e.Seq >= input.BeforeSeq {
			continue
		}

		// Time range filters.
		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		if !until.IsZero() && e.Timestamp.After(until) {
			continue
		}

		// Event type filter.
		if typeSet != nil && !typeSet[string(e.Type)] {
			continue
		}

		// Decode event data (needed for text and tool_name filters).
		data, _ := session.DecodeEventData(e)

		// Tool name filter (only applies to tool_call events).
		if toolNameLower != "" {
			if e.Type != session.EventTypeToolCall {
				continue
			}
			if d, ok := data.(session.ToolCallData); ok {
				if !strings.Contains(strings.ToLower(d.Title), toolNameLower) {
					continue
				}
			}
		}

		// Text content filters.
		if textContainsLower != "" || textExcludesLower != "" {
			text := historyExtractText(e.Type, data)
			lower := strings.ToLower(text)
			if textContainsLower != "" && !strings.Contains(lower, textContainsLower) {
				continue
			}
			if textExcludesLower != "" && strings.Contains(lower, textExcludesLower) {
				continue
			}
		}

		result = append(result, e)
	}

	return result
}

// historyExtractText extracts all searchable text from a decoded event data value.
func historyExtractText(eventType session.EventType, data interface{}) string {
	var parts []string
	switch eventType {
	case session.EventTypeUserPrompt:
		if d, ok := data.(session.UserPromptData); ok {
			parts = append(parts, d.Message)
		}
	case session.EventTypeAgentMessage:
		if d, ok := data.(session.AgentMessageData); ok {
			parts = append(parts, d.Text) // HTML as-is per spec
		}
	case session.EventTypeAgentThought:
		if d, ok := data.(session.AgentThoughtData); ok {
			parts = append(parts, d.Text)
		}
	case session.EventTypeToolCall:
		if d, ok := data.(session.ToolCallData); ok {
			parts = append(parts, d.Title)
			if d.RawInput != nil {
				if b, err := json.Marshal(d.RawInput); err == nil {
					parts = append(parts, string(b))
				}
			}
			if d.RawOutput != nil {
				if b, err := json.Marshal(d.RawOutput); err == nil {
					parts = append(parts, string(b))
				}
			}
		}
	case session.EventTypeToolCallUpdate:
		if d, ok := data.(session.ToolCallUpdateData); ok {
			if d.Title != nil {
				parts = append(parts, *d.Title)
			}
			if d.Status != nil {
				parts = append(parts, *d.Status)
			}
		}
	case session.EventTypePlan:
		if d, ok := data.(session.PlanData); ok {
			for _, entry := range d.Entries {
				parts = append(parts, entry.Content)
			}
		}
	case session.EventTypePermission:
		if d, ok := data.(session.PermissionData); ok {
			parts = append(parts, d.Title, d.SelectedOption, d.Outcome)
		}
	case session.EventTypeFileRead, session.EventTypeFileWrite:
		if d, ok := data.(session.FileOperationData); ok {
			parts = append(parts, d.Path, d.Content)
		}
	case session.EventTypeError:
		if d, ok := data.(session.ErrorData); ok {
			parts = append(parts, d.Message)
		}
	case session.EventTypeSessionStart:
		if d, ok := data.(session.SessionStartData); ok {
			parts = append(parts, d.SessionID)
		}
	case session.EventTypeSessionEnd:
		if d, ok := data.(session.SessionEndData); ok {
			parts = append(parts, d.Reason)
		}
	}
	return strings.Join(parts, " ")
}

// historyBuildEvent constructs a ConversationHistoryEvent from a raw session event.
func historyBuildEvent(e session.Event, includeData bool) ConversationHistoryEvent {
	data, _ := session.DecodeEventData(e)

	hist := ConversationHistoryEvent{
		Seq:       e.Seq,
		Type:      string(e.Type),
		Timestamp: e.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		Summary:   historyBuildSummary(e.Type, data),
	}

	if includeData {
		hist.Data = historyTruncateData(e.Type, data)
	}

	return hist
}

// historyBuildSummary generates a short human-readable one-liner for an event.
func historyBuildSummary(eventType session.EventType, data interface{}) string {
	switch eventType {
	case session.EventTypeUserPrompt:
		if d, ok := data.(session.UserPromptData); ok {
			return historyTruncateStr(d.Message, 150)
		}
	case session.EventTypeAgentMessage:
		if d, ok := data.(session.AgentMessageData); ok {
			return historyTruncateStr(session.StripHTML(d.Text), 150)
		}
	case session.EventTypeAgentThought:
		if d, ok := data.(session.AgentThoughtData); ok {
			return historyTruncateStr(d.Text, 150)
		}
	case session.EventTypeToolCall:
		if d, ok := data.(session.ToolCallData); ok {
			return fmt.Sprintf("%s [%s]", d.Title, d.Status)
		}
	case session.EventTypeToolCallUpdate:
		if d, ok := data.(session.ToolCallUpdateData); ok {
			title := ""
			if d.Title != nil {
				title = *d.Title
			}
			return fmt.Sprintf("Update: %s", title)
		}
	case session.EventTypePlan:
		if d, ok := data.(session.PlanData); ok {
			return fmt.Sprintf("Plan: %d entries", len(d.Entries))
		}
	case session.EventTypePermission:
		if d, ok := data.(session.PermissionData); ok {
			return fmt.Sprintf("%s: %s", d.Title, d.Outcome)
		}
	case session.EventTypeFileRead:
		if d, ok := data.(session.FileOperationData); ok {
			return fmt.Sprintf("Read: %s", d.Path)
		}
	case session.EventTypeFileWrite:
		if d, ok := data.(session.FileOperationData); ok {
			return fmt.Sprintf("Write: %s", d.Path)
		}
	case session.EventTypeError:
		if d, ok := data.(session.ErrorData); ok {
			return historyTruncateStr(d.Message, 150)
		}
	case session.EventTypeSessionStart:
		return "Session started"
	case session.EventTypeSessionEnd:
		if d, ok := data.(session.SessionEndData); ok {
			return fmt.Sprintf("Session ended: %s", d.Reason)
		}
	}
	return string(eventType)
}

// historyTruncateData returns a copy of event data with long string fields truncated to historyMaxDataStr.
func historyTruncateData(eventType session.EventType, data interface{}) interface{} {
	switch eventType {
	case session.EventTypeUserPrompt:
		if d, ok := data.(session.UserPromptData); ok {
			d.Message = historyTruncateDataStr(d.Message)
			return d
		}
	case session.EventTypeAgentMessage:
		if d, ok := data.(session.AgentMessageData); ok {
			d.Text = historyTruncateDataStr(d.Text)
			return d
		}
	case session.EventTypeError:
		if d, ok := data.(session.ErrorData); ok {
			d.Message = historyTruncateDataStr(d.Message)
			return d
		}
	}
	return data
}

// historyTruncateStr truncates a string to maxLen chars, appending "..." if truncated.
func historyTruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// historyTruncateDataStr truncates a string to historyMaxDataStr, appending "..." if truncated.
func historyTruncateDataStr(s string) string {
	if len(s) <= historyMaxDataStr {
		return s
	}
	return s[:historyMaxDataStr-3] + "..."
}
