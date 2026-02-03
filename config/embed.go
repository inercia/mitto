// Package config provides embedded default configuration for Mitto.
package config

import (
	"embed"
)

// DefaultConfigYAML contains the embedded default configuration in YAML format.
// This is used to create the initial settings.json file when Mitto starts for the first time.
//
//go:embed config.default.yaml
var DefaultConfigYAML []byte

// BuiltinPromptsFS contains the embedded builtin prompts directory.
// These prompts are deployed to MITTO_DIR/prompts/builtin/ on first run.
//
//go:embed prompts/builtin/*.md
var BuiltinPromptsFS embed.FS

// BuiltinPromptsDir is the path within the embedded filesystem where prompts are stored.
const BuiltinPromptsDir = "prompts/builtin"
