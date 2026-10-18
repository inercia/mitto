package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelShortcut binds a numeric quick-switch key to an arbitrary model tag.
type ModelShortcut struct {
	Number   int    `json:"number" yaml:"number"`
	ModelTag string `json:"model_tag" yaml:"model_tag"`
}

// ModelShortcutValidationError identifies the first invalid indexed input field.
type ModelShortcutValidationError struct {
	Field   string
	Message string
}

func (e *ModelShortcutValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// ValidateModelShortcuts checks raw input without modifying or normalizing it.
func ValidateModelShortcuts(shortcuts []ModelShortcut) error {
	seen := make(map[int]bool)
	for i, shortcut := range shortcuts {
		field := fmt.Sprintf("model_shortcuts[%d]", i)
		if shortcut.Number < 1 || shortcut.Number > 5 {
			return &ModelShortcutValidationError{Field: field + ".number", Message: "must be an integer from 1 to 5"}
		}
		if seen[shortcut.Number] {
			return &ModelShortcutValidationError{Field: field + ".number", Message: "must be unique"}
		}
		if strings.TrimSpace(shortcut.ModelTag) == "" {
			return &ModelShortcutValidationError{Field: field + ".model_tag", Message: "must not be blank"}
		}
		seen[shortcut.Number] = true
	}
	return nil
}

// NormalizeModelShortcuts returns an independent slice of first-valid mappings,
// preserving input order, tag case/internal whitespace and nil versus empty.
func NormalizeModelShortcuts(shortcuts []ModelShortcut) []ModelShortcut {
	if shortcuts == nil {
		return nil
	}
	result := make([]ModelShortcut, 0, len(shortcuts))
	seen := make(map[int]bool)
	for _, shortcut := range shortcuts {
		shortcut.ModelTag = strings.TrimSpace(shortcut.ModelTag)
		if shortcut.Number < 1 || shortcut.Number > 5 || shortcut.ModelTag == "" || seen[shortcut.Number] {
			continue
		}
		seen[shortcut.Number] = true
		result = append(result, shortcut)
	}
	return result
}

// rawModelShortcut confines YAML type checks to this new collection. yaml.v3's
// normal int decoding would otherwise silently truncate floating-point scalars.
type rawModelShortcut ModelShortcut

func (s *rawModelShortcut) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Number   yaml.Node `yaml:"number"`
		ModelTag yaml.Node `yaml:"model_tag"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	if !raw.Number.IsZero() && raw.Number.Tag != "!!null" {
		if raw.Number.Kind != yaml.ScalarNode || raw.Number.Tag != "!!int" {
			return fmt.Errorf("model_shortcuts number must be an integer scalar")
		}
		if err := raw.Number.Decode(&s.Number); err != nil {
			return err
		}
	}
	if !raw.ModelTag.IsZero() && raw.ModelTag.Tag != "!!null" {
		if raw.ModelTag.Kind != yaml.ScalarNode || raw.ModelTag.Tag != "!!str" {
			return fmt.Errorf("model_shortcuts model_tag must be a string scalar")
		}
		if err := raw.ModelTag.Decode(&s.ModelTag); err != nil {
			return err
		}
	}
	return nil
}
