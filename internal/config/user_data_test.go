package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserDataAttributeType_IsValid(t *testing.T) {
	tests := []struct {
		attrType UserDataAttributeType
		want     bool
	}{
		{UserDataTypeString, true},
		{UserDataTypeURL, true},
		{UserDataTypeFilename, true},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.attrType), func(t *testing.T) {
			if got := tt.attrType.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserDataAttributeType_DefaultType(t *testing.T) {
	tests := []struct {
		attrType UserDataAttributeType
		want     UserDataAttributeType
	}{
		{"", UserDataTypeString},
		{UserDataTypeString, UserDataTypeString},
		{UserDataTypeURL, UserDataTypeURL},
		{UserDataTypeFilename, UserDataTypeFilename},
	}

	for _, tt := range tests {
		t.Run(string(tt.attrType), func(t *testing.T) {
			if got := tt.attrType.DefaultType(); got != tt.want {
				t.Errorf("DefaultType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserDataSchema_ValidateAttribute_NoSchema(t *testing.T) {
	// Nil schema should reject any attribute
	var schema *UserDataSchema = nil
	err := schema.ValidateAttribute("any_name", "any_value", "")
	if err == nil {
		t.Error("Expected error with nil schema, got nil")
	}

	// Empty schema should reject any attribute
	emptySchema := &UserDataSchema{}
	err = emptySchema.ValidateAttribute("any_name", "any_value", "")
	if err == nil {
		t.Error("Expected error with empty schema, got nil")
	}
}

func TestUserDataSchema_ValidateAttribute_EmptyName(t *testing.T) {
	schema := &UserDataSchema{
		Fields: []UserDataSchemaField{
			{Name: "test", Type: UserDataTypeString},
		},
	}

	err := schema.ValidateAttribute("", "value", "")
	if err == nil {
		t.Error("Expected error for empty name")
	}
}

func TestUserDataSchema_ValidateAttribute_UnknownField(t *testing.T) {
	schema := &UserDataSchema{
		Fields: []UserDataSchemaField{
			{Name: "allowed", Type: UserDataTypeString},
		},
	}

	err := schema.ValidateAttribute("not_allowed", "value", "")
	if err == nil {
		t.Error("Expected error for unknown field")
	}
}

func TestUserDataSchema_ValidateAttribute_StringType(t *testing.T) {
	schema := &UserDataSchema{
		Fields: []UserDataSchemaField{
			{Name: "description", Type: UserDataTypeString},
		},
	}

	// String type should accept any value
	tests := []string{"", "hello", "with spaces", "special!@#$%"}
	for _, value := range tests {
		err := schema.ValidateAttribute("description", value, "")
		if err != nil {
			t.Errorf("Unexpected error for value %q: %v", value, err)
		}
	}
}

func TestUserDataSchema_ValidateAttribute_URLType(t *testing.T) {
	schema := &UserDataSchema{
		Fields: []UserDataSchemaField{
			{Name: "link", Type: UserDataTypeURL},
		},
	}

	validURLs := []string{
		"",
		"https://example.com",
		"http://example.com/path",
		"https://jira.example.com/browse/PROJ-123",
	}
	for _, url := range validURLs {
		err := schema.ValidateAttribute("link", url, "")
		if err != nil {
			t.Errorf("Unexpected error for valid URL %q: %v", url, err)
		}
	}

	invalidURLs := []string{
		"not a url",
		"example.com",
		"/path/without/scheme",
	}
	for _, url := range invalidURLs {
		err := schema.ValidateAttribute("link", url, "")
		if err == nil {
			t.Errorf("Expected error for invalid URL %q", url)
		}
	}
}

func TestUserDataSchema_ValidateAttribute_EmptyType(t *testing.T) {
	// Empty type should default to string
	schema := &UserDataSchema{
		Fields: []UserDataSchemaField{
			{Name: "field", Type: ""},
		},
	}

	err := schema.ValidateAttribute("field", "any value", "")
	if err != nil {
		t.Errorf("Expected nil error for empty type (defaults to string), got %v", err)
	}
}

func TestUserDataSchema_ValidateAttribute_FilenameType(t *testing.T) {
	schema := &UserDataSchema{
		Fields: []UserDataSchemaField{
			{Name: "report", Type: UserDataTypeFilename},
		},
	}

	// Set up a workspace dir with a readable file and a subdirectory.
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "report.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	subDir := filepath.Join(workDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("failed to write nested file: %v", err)
	}

	// Valid: empty value (field unset) is allowed.
	if err := schema.ValidateAttribute("report", "", workDir); err != nil {
		t.Errorf("Unexpected error for empty filename: %v", err)
	}

	// Valid: relative path to an existing readable file.
	if err := schema.ValidateAttribute("report", "report.md", workDir); err != nil {
		t.Errorf("Unexpected error for relative filename: %v", err)
	}
	if err := schema.ValidateAttribute("report", "sub/nested.txt", workDir); err != nil {
		t.Errorf("Unexpected error for nested relative filename: %v", err)
	}

	// Valid: absolute path to an existing readable file.
	abs := filepath.Join(workDir, "report.md")
	if err := schema.ValidateAttribute("report", abs, workDir); err != nil {
		t.Errorf("Unexpected error for absolute filename: %v", err)
	}

	// Invalid: nonexistent relative path.
	if err := schema.ValidateAttribute("report", "does-not-exist.md", workDir); err == nil {
		t.Error("Expected error for nonexistent relative filename")
	}

	// Invalid: nonexistent absolute path.
	if err := schema.ValidateAttribute("report", filepath.Join(workDir, "nope.md"), workDir); err == nil {
		t.Error("Expected error for nonexistent absolute filename")
	}

	// Invalid: value points to a directory, not a file.
	if err := schema.ValidateAttribute("report", "sub", workDir); err == nil {
		t.Error("Expected error for directory filename")
	}
}
