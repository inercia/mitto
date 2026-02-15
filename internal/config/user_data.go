package config

import (
	"fmt"
	"net/url"
)

// UserDataAttributeType represents the type of a user data attribute.
type UserDataAttributeType string

const (
	// UserDataTypeString is a plain text string attribute.
	UserDataTypeString UserDataAttributeType = "string"
	// UserDataTypeURL is a URL attribute (validated as a valid URL).
	UserDataTypeURL UserDataAttributeType = "url"
)

// IsValid returns true if the attribute type is a known valid type.
func (t UserDataAttributeType) IsValid() bool {
	switch t {
	case UserDataTypeString, UserDataTypeURL:
		return true
	default:
		return false
	}
}

// DefaultType returns the default type to use if no type is specified.
func (t UserDataAttributeType) DefaultType() UserDataAttributeType {
	if t == "" {
		return UserDataTypeString
	}
	return t
}

// UserDataSchemaField defines a single field in the user data schema.
type UserDataSchemaField struct {
	// Name is the field name.
	Name string `json:"name" yaml:"name"`
	// Type is the field type (string, url, etc.).
	Type UserDataAttributeType `json:"type" yaml:"type"`
}

// UserDataSchema defines the allowed user data fields for a workspace.
type UserDataSchema struct {
	// Fields is the list of allowed fields.
	Fields []UserDataSchemaField `json:"fields,omitempty"`
}

// ValidateAttribute validates a single attribute value against the schema.
// If schema is nil or empty, no attributes are allowed.
// Returns an error if validation fails.
func (s *UserDataSchema) ValidateAttribute(name, value string) error {
	if name == "" {
		return fmt.Errorf("attribute name cannot be empty")
	}

	// If no schema defined, no attributes are allowed
	if s == nil || len(s.Fields) == 0 {
		return fmt.Errorf("attribute %q not allowed: no user data schema defined for this workspace", name)
	}

	// Find the schema field for this attribute
	var schemaField *UserDataSchemaField
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			schemaField = &s.Fields[i]
			break
		}
	}

	// If schema is defined but field not found, reject
	if schemaField == nil {
		return fmt.Errorf("unknown attribute %q: not defined in schema", name)
	}

	// Validate based on type
	return validateAttributeValue(value, schemaField.Type.DefaultType())
}

// validateAttributeValue validates a value based on its type.
func validateAttributeValue(value string, attrType UserDataAttributeType) error {
	switch attrType {
	case UserDataTypeURL:
		if value == "" {
			return nil // Empty URL is allowed
		}
		if _, err := url.ParseRequestURI(value); err != nil {
			return fmt.Errorf("invalid URL: %w", err)
		}
		// Additionally check for scheme
		u, _ := url.Parse(value)
		if u.Scheme == "" {
			return fmt.Errorf("invalid URL: missing scheme (e.g., https://)")
		}
	case UserDataTypeString:
		// String type accepts any value
	default:
		// Unknown types are treated as string
	}
	return nil
}
