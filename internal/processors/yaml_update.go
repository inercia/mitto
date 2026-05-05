package processors

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// UpdateProcessorFileEnabled updates the enabled field in a processor YAML file.
// If enabling (enabled=true): removes the enabled key (nil/missing means enabled by default).
// If disabling (enabled=false): sets enabled: false.
// All other fields are preserved exactly as-is.
func UpdateProcessorFileEnabled(filePath string, enabled bool) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read processor file: %w", err)
	}

	var content map[string]interface{}
	if err := yaml.Unmarshal(data, &content); err != nil {
		return fmt.Errorf("failed to parse processor YAML: %w", err)
	}
	if content == nil {
		content = make(map[string]interface{})
	}

	if enabled {
		// Remove the key entirely — absence means enabled by default
		delete(content, "enabled")
	} else {
		content["enabled"] = false
	}

	out, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to marshal processor YAML: %w", err)
	}

	if err := os.WriteFile(filePath, out, 0644); err != nil {
		return fmt.Errorf("failed to write processor file: %w", err)
	}

	return nil
}
