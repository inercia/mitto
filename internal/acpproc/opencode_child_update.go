package acpproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/conversation"
)

const (
	opencodeChildUpdateMethod = "opencode/session/child_update"
	mittoChildUpdateMethod    = "_mitto/opencode/session/child_update"
	reservedChildUpdateMethod = "_mitto/opencode/session/child_update/invalid_request"
	maxChildSessionIDBytes    = 256
	maxChildSessionDepth      = 32
)

var errInvalidChildUpdate = errors.New("invalid OpenCode child update")

type jsonObject struct {
	values     map[string]json.RawMessage
	duplicates map[string]bool
	methods    []jsonTokenSpan
}

type jsonTokenSpan struct {
	start int
	end   int
	value string
}

// adaptOpenCodeChildUpdate translates only validated OpenCode lifecycle
// notifications to the private method name supported by the pinned SDK.
// Requests to the reserved private method are redirected to an unhandled
// private method so they cannot invoke the notification callback.
func adaptOpenCodeChildUpdate(line []byte) ([]byte, bool) {
	obj, ok := parseJSONObject(line)
	if !ok {
		return line, false
	}

	methods := obj.methods
	if len(methods) == 0 {
		return line, false
	}
	containsMethod := func(method string) bool {
		for _, token := range methods {
			if token.value == method {
				return true
			}
		}
		return false
	}
	idPresent := obj.values["id"] != nil || obj.duplicates["id"]

	// The alias is private to the adapter. Never allow a direct notification to
	// reach the handler, and ensure any request with an ambiguous duplicate
	// method key still resolves to a method-not-found response.
	if containsMethod(mittoChildUpdateMethod) {
		if !idPresent {
			return nil, true
		}
		if obj.duplicates["method"] {
			return replaceMethodTokens(line, methods, reservedChildUpdateMethod), false
		}
		return replaceMethodTokens(line, []jsonTokenSpan{methods[len(methods)-1]}, reservedChildUpdateMethod), false
	}

	if !containsMethod(opencodeChildUpdateMethod) {
		return line, false
	}
	// A request (including id:null) is never adapted into a callback.
	if idPresent {
		return line, false
	}
	// Ambiguous routing keys cannot safely identify a notification. Drop only
	// the exact lifecycle notification; unrelated malformed JSON remains under
	// the reader's existing behavior.
	if obj.duplicates["jsonrpc"] || obj.duplicates["method"] || obj.duplicates["params"] {
		return nil, true
	}
	var version, method string
	if json.Unmarshal(obj.values["jsonrpc"], &version) != nil || version != "2.0" ||
		json.Unmarshal(obj.values["method"], &method) != nil || method != opencodeChildUpdateMethod {
		return nil, true
	}
	params := obj.values["params"]
	if !isJSONObjectRaw(params) {
		return nil, true
	}
	if _, err := decodeOpenCodeChildUpdate(params); err != nil {
		return nil, true
	}
	return replaceMethodTokens(line, []jsonTokenSpan{methods[len(methods)-1]}, mittoChildUpdateMethod), false
}

func replaceMethodTokens(line []byte, spans []jsonTokenSpan, method string) []byte {
	replacement, _ := json.Marshal(method)
	var out bytes.Buffer
	last := 0
	for _, span := range spans {
		if span.start < last || span.end > len(line) {
			return line
		}
		out.Write(line[last:span.start])
		out.Write(replacement)
		last = span.end
	}
	out.Write(line[last:])
	return out.Bytes()
}

func parseJSONObject(raw []byte) (jsonObject, bool) {
	result := jsonObject{values: make(map[string]json.RawMessage), duplicates: make(map[string]bool)}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return result, false
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return result, false
		}
		key, ok := keyToken.(string)
		if !ok {
			return result, false
		}
		start := skipJSONWhitespace(raw, int(decoder.InputOffset()))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return result, false
		}
		end := int(decoder.InputOffset())
		if _, exists := result.values[key]; exists {
			result.duplicates[key] = true
		}
		result.values[key] = value
		if key == "method" {
			var method string
			if json.Unmarshal(value, &method) == nil {
				result.methods = append(result.methods, jsonTokenSpan{start: start, end: end, value: method})
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return result, false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return result, false
	}
	return result, true
}

func skipJSONWhitespace(raw []byte, offset int) int {
	for offset < len(raw) {
		switch raw[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func isJSONObjectRaw(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func decodeOpenCodeChildUpdate(raw json.RawMessage) (conversation.ChildSessionUpdate, error) {
	var update conversation.ChildSessionUpdate
	envelope, ok := parseJSONObject(raw)
	if !ok || len(envelope.duplicates) > 0 {
		return update, errInvalidChildUpdate
	}
	readString := func(key string, required bool, maxBytes int) (string, bool) {
		value, present := envelope.values[key]
		if !present {
			return "", !required
		}
		var result string
		if json.Unmarshal(value, &result) != nil || (required && result == "") || (maxBytes > 0 && len(result) > maxBytes) {
			return "", false
		}
		return result, true
	}
	root, ok := readString("rootSessionId", true, maxChildSessionIDBytes)
	if !ok {
		return update, errInvalidChildUpdate
	}
	child, ok := readString("childSessionId", true, maxChildSessionIDBytes)
	if !ok {
		return update, errInvalidChildUpdate
	}
	parent, ok := readString("parentSessionId", true, maxChildSessionIDBytes)
	if !ok {
		return update, errInvalidChildUpdate
	}
	if root == child || child == parent {
		return update, errInvalidChildUpdate
	}
	var depth int
	if json.Unmarshal(envelope.values["depth"], &depth) != nil || depth < 1 || depth > maxChildSessionDepth ||
		(envelope.values["depth"] == nil) || (depth == 1 && parent != root) || (depth > 1 && parent == root) {
		return update, errInvalidChildUpdate
	}
	title, ok := readString("title", false, 0)
	if !ok {
		return update, errInvalidChildUpdate
	}
	var titlePtr *string
	if _, present := envelope.values["title"]; present {
		titlePtr = &title
	}
	eventType, ok := readString("type", true, 0)
	if !ok {
		return update, errInvalidChildUpdate
	}

	update.RootSessionID = acp.SessionId(root)
	update.ChildSessionID = acp.SessionId(child)
	update.ParentSessionID = acp.SessionId(parent)
	update.Depth = depth
	update.Title = titlePtr
	update.Type = eventType
	switch eventType {
	case "status":
		status, ok := readString("status", true, 0)
		if !ok || envelope.values["update"] != nil {
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		switch status {
		case "created", "running", "completed", "failed", "interrupted":
		default:
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		update.Status = status
		if rawError, present := envelope.values["error"]; present {
			decoded, err := decodeChildSessionError(rawError)
			if err != nil {
				return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
			}
			update.Error = decoded
		}
	case "update":
		if envelope.values["status"] != nil || envelope.values["error"] != nil || !isJSONObjectRaw(envelope.values["update"]) {
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		updateRaw := envelope.values["update"]
		updateObject, ok := parseJSONObject(updateRaw)
		if !ok || len(updateObject.duplicates) > 0 {
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		discriminator, ok := rawJSONString(updateObject.values["sessionUpdate"])
		if !ok || !supportedSessionUpdate(discriminator) {
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		var decoded acp.SessionUpdate
		if json.Unmarshal(updateRaw, &decoded) != nil {
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		if !validateChildUpdateMetadata(updateObject, child, parent, depth, titlePtr) {
			return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
		}
		update.Update = &decoded
	default:
		return conversation.ChildSessionUpdate{}, errInvalidChildUpdate
	}
	return update, nil
}

func decodeChildSessionError(raw json.RawMessage) (*conversation.ChildSessionUpdateError, error) {
	obj, ok := parseJSONObject(raw)
	if !ok || len(obj.duplicates) > 0 {
		return nil, errInvalidChildUpdate
	}
	typeName, typeOK := rawJSONString(obj.values["type"])
	message, messageOK := rawJSONString(obj.values["message"])
	if !typeOK || !messageOK {
		return nil, errInvalidChildUpdate
	}
	return &conversation.ChildSessionUpdateError{Type: typeName, Message: message}, nil
}

func rawJSONString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func supportedSessionUpdate(discriminator string) bool {
	switch discriminator {
	case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update", "plan", "available_commands_update", "current_mode_update", "config_option_update", "session_info_update", "usage_update":
		return true
	default:
		return false
	}
}

func validateChildUpdateMetadata(update jsonObject, child, parent string, depth int, title *string) bool {
	metaRaw, hasMeta := update.values["_meta"]
	if !hasMeta {
		return true
	}
	meta, ok := parseJSONObject(metaRaw)
	if !ok || len(meta.duplicates) > 0 {
		return false
	}
	childMetaRaw, hasChildMeta := meta.values["opencode/child-session"]
	if !hasChildMeta {
		return true
	}
	childMeta, ok := parseJSONObject(childMetaRaw)
	if !ok || len(childMeta.duplicates) > 0 {
		return false
	}
	id, idOK := rawJSONString(childMeta.values["id"])
	parentID, parentOK := rawJSONString(childMeta.values["parentID"])
	var metaDepth int
	if !idOK || !parentOK || json.Unmarshal(childMeta.values["depth"], &metaDepth) != nil || id != child || parentID != parent || metaDepth != depth {
		return false
	}
	if titleRaw, exists := childMeta.values["title"]; exists {
		metaTitle, ok := rawJSONString(titleRaw)
		if !ok || (title != nil && *title != metaTitle) {
			return false
		}
	}
	return true
}

func (mc *MultiplexClient) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != mittoChildUpdateMethod {
		return nil, acp.NewMethodNotFound(method)
	}
	update, err := decodeOpenCodeChildUpdate(params)
	if err != nil {
		return nil, errInvalidChildUpdate
	}
	callbacks := mc.getSession(update.RootSessionID)
	if callbacks == nil || callbacks.OnChildSessionUpdate == nil {
		return nil, nil
	}
	return nil, callbacks.OnChildSessionUpdate(ctx, update)
}

var _ acp.ExtensionMethodHandler = (*MultiplexClient)(nil)
