// Package config provides embedded default configuration for Mitto.
package config

import (
	_ "embed"
)

// DefaultConfigYAML contains the embedded default configuration in YAML format.
// This is used to create the initial settings.json file when Mitto starts for the first time.
//
//go:embed config.default.yaml
var DefaultConfigYAML []byte
