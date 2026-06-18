package config

// KnownPromptParameterTypes is the canonical registry of supported parameter types
// for the structured `parameters:` field in .prompt.yaml files.
//
// This slice is the SINGLE SOURCE OF TRUTH for backend type validation.
// It is mirrored by the frontend type picker (sibling bead .3) and surfaced
// via MCP tool schemas (sibling bead .2). When adding a new type, add it here
// only — all downstream consumers reference this slice.
//
// Type semantics:
//   - beadsId        — a beads issue ID (e.g. "mitto-42")
//   - beadsTitle     — a beads issue title (free text, typically auto-filled)
//   - sessionId      — a Mitto conversation/session UUID
//   - workspaceId    — a Mitto workspace UUID
//   - workspaceFolder — an absolute path to the workspace root directory
//   - text           — generic free-form text (the catch-all type)
var KnownPromptParameterTypes = []string{
	"beadsId",
	"beadsTitle",
	"sessionId",
	"workspaceId",
	"workspaceFolder",
	"text",
}

// IsKnownPromptParameterType reports whether t is a recognised parameter type.
func IsKnownPromptParameterType(t string) bool {
	for _, known := range KnownPromptParameterTypes {
		if t == known {
			return true
		}
	}
	return false
}
