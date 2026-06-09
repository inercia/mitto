package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// UserDataAttributeType represents the type of a user data attribute.
type UserDataAttributeType string

const (
	// UserDataTypeString is a plain text string attribute.
	UserDataTypeString UserDataAttributeType = "string"
	// UserDataTypeURL is a URL attribute (validated as a valid URL).
	UserDataTypeURL UserDataAttributeType = "url"
	// UserDataTypeFilename is a workspace-relative file path, clickable to open in the internal viewer.
	UserDataTypeFilename UserDataAttributeType = "filename"
)

// IsValid returns true if the attribute type is a known valid type.
func (t UserDataAttributeType) IsValid() bool {
	switch t {
	case UserDataTypeString, UserDataTypeURL, UserDataTypeFilename:
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
	// Description is an optional human-readable description of this field.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
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
// workingDir is the conversation's working directory, used to resolve relative
// paths for filename-typed attributes; it may be empty for non-filename types.
// Returns an error if validation fails.
func (s *UserDataSchema) ValidateAttribute(name, value, workingDir string) error {
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

	// Validate based on type. Prefix type-validation failures with the field name
	// so the agent can tell which attribute was rejected.
	if err := validateAttributeValue(value, schemaField.Type.DefaultType(), workingDir); err != nil {
		return fmt.Errorf("field %q: %w", name, err)
	}
	return nil
}

// validateAttributeValue validates a value based on its type.
// workingDir resolves relative paths for filename-typed values.
func validateAttributeValue(value string, attrType UserDataAttributeType, workingDir string) error {
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
	case UserDataTypeFilename:
		return validateFilenameValue(value, workingDir)
	case UserDataTypeString:
		// String type accepts any value
	default:
		// Unknown types are treated as string
	}
	return nil
}

// validateFilenameValue checks that a filename attribute points to an existing,
// readable file. The value may be an absolute path or a path relative to the
// workspace working directory. An empty value is allowed (the field is unset).
func validateFilenameValue(value, workingDir string) error {
	if value == "" {
		return nil // Empty filename is allowed (field unset)
	}

	resolved := value
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workingDir, resolved)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			if filepath.IsAbs(value) {
				return fmt.Errorf("file does not exist: %s", value)
			}
			return fmt.Errorf("file does not exist: %s (resolved to %s relative to the workspace)", value, resolved)
		}
		return fmt.Errorf("cannot access file %s: %v", value, err)
	}

	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", value)
	}

	// Confirm the file is readable by opening it.
	f, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("file is not readable: %s (%v)", value, err)
	}
	_ = f.Close()

	return nil
}
