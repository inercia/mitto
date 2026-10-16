package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpDiagnosticRequestIDHeader = "X-Mitto-Internal-Request-ID"

// sharedCallerDiagnosticAttrs returns bounded, non-secret fields for shared
// caller rejection logs. The request ID is diagnostic only; it is never used
// to authenticate or resolve a caller.
func sharedCallerDiagnosticAttrs(req *mcp.CallToolRequest, hasMeta bool, metaState, reason string) []any {
	attrs := make([]any, 0, 8)
	if req != nil && req.Extra != nil && req.Extra.Header != nil {
		if requestID := req.Extra.Header.Get(mcpDiagnosticRequestIDHeader); isMCPDiagnosticRequestID(requestID) {
			attrs = append(attrs, "mcp_req_id", requestID)
		}
	}
	if req != nil && req.Params != nil {
		if toolName := boundedMCPDiagnosticToolName(req.Params.Name); toolName != "" {
			attrs = append(attrs, "tool_name", toolName)
		}
	}
	attrs = append(attrs,
		"has_caller_meta", hasMeta,
		"caller_meta_state", metaState,
		"reason", reason,
	)
	return attrs
}

func isMCPDiagnosticRequestID(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func boundedMCPDiagnosticToolName(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' && char != '.' {
			return ""
		}
	}
	return value
}
