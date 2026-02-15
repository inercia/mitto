package config

import (
	"testing"
)

func TestUserDataAttributeType_IsValid(t *testing.T) {
	tests := []struct {
		attrType UserDataAttributeType
		want     bool
	}{
		{UserDataTypeString, true},
		{UserDataTypeURL, true},
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
	err := schema.ValidateAttribute("any_name", "any_value")
	if err == nil {
		t.Error("Expected error with nil schema, got nil")
	}

	// Empty schema should reject any attribute
	emptySchema := &UserDataSchema{}
	err = emptySchema.ValidateAttribute("any_name", "any_value")
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

	err := schema.ValidateAttribute("", "value")
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

	err := schema.ValidateAttribute("not_allowed", "value")
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
		err := schema.ValidateAttribute("description", value)
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
		err := schema.ValidateAttribute("link", url)
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
		err := schema.ValidateAttribute("link", url)
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

	err := schema.ValidateAttribute("field", "any value")
	if err != nil {
		t.Errorf("Expected nil error for empty type (defaults to string), got %v", err)
	}
}
