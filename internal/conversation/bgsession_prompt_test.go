package conversation

import (
	"strings"
	"testing"
)

func TestBuildArgumentMetadata_Basic(t *testing.T) {
	names, arguments := buildArgumentMetadata(map[string]string{
		"greeting": "hello",
		"name":     "world",
	})

	// Sorted order: greeting, name
	if len(names) != 2 || names[0] != "greeting" || names[1] != "name" {
		t.Fatalf("unexpected names: %v", names)
	}
	if len(arguments) != 2 {
		t.Fatalf("unexpected arguments length: %d", len(arguments))
	}
	if arguments[0]["name"] != "greeting" || arguments[0]["value"] != "hello" {
		t.Errorf("unexpected first entry: %v", arguments[0])
	}
	if arguments[1]["name"] != "name" || arguments[1]["value"] != "world" {
		t.Errorf("unexpected second entry: %v", arguments[1])
	}
}

func TestBuildArgumentMetadata_SortedOrderMatchesNames(t *testing.T) {
	args := map[string]string{"z": "last", "a": "first", "m": "middle"}
	names, arguments := buildArgumentMetadata(args)

	for i, n := range names {
		if arguments[i]["name"] != n {
			t.Errorf("index %d: names[%d]=%q but arguments[%d][name]=%v", i, i, n, i, arguments[i]["name"])
		}
	}
}

func TestBuildArgumentMetadata_Truncation(t *testing.T) {
	longValue := strings.Repeat("x", 100)
	names, arguments := buildArgumentMetadata(map[string]string{"key": longValue})

	if len(names) != 1 || names[0] != "key" {
		t.Fatalf("unexpected names: %v", names)
	}
	val, ok := arguments[0]["value"].(string)
	if !ok {
		t.Fatalf("value is not a string: %T", arguments[0]["value"])
	}
	runes := []rune(val)
	// Truncated to 80 runes + 1 ellipsis rune = 81 runes
	if len(runes) != maxArgValueLen+1 {
		t.Errorf("truncated value has %d runes, want %d", len(runes), maxArgValueLen+1)
	}
	if !strings.HasSuffix(val, "…") {
		t.Errorf("truncated value missing ellipsis suffix: %q", val)
	}
}

func TestBuildArgumentMetadata_NoTruncationAtExactLimit(t *testing.T) {
	exactValue := strings.Repeat("y", maxArgValueLen)
	_, arguments := buildArgumentMetadata(map[string]string{"k": exactValue})

	val, _ := arguments[0]["value"].(string)
	if val != exactValue {
		t.Errorf("value at exact limit should be unmodified; got %q", val)
	}
}

func TestBuildArgumentMetadata_RedactionSensitiveNames(t *testing.T) {
	sensitiveNames := []string{
		"my_password", "MY_TOKEN", "api_key", "apikey", "secret",
		"ACCESS_KEY", "auth_key", "private_key", "credentials", "passwd",
	}
	for _, sn := range sensitiveNames {
		_, arguments := buildArgumentMetadata(map[string]string{sn: "super-secret-value"})
		val, _ := arguments[0]["value"].(string)
		if val != "***" {
			t.Errorf("name %q: expected redacted value \"***\", got %q", sn, val)
		}
	}
}

func TestBuildArgumentMetadata_NonSensitiveNamesNotRedacted(t *testing.T) {
	_, arguments := buildArgumentMetadata(map[string]string{"greeting": "hello world"})
	val, _ := arguments[0]["value"].(string)
	if val != "hello world" {
		t.Errorf("non-sensitive name: unexpected value %q", val)
	}
}

func TestBuildArgumentMetadata_Empty(t *testing.T) {
	names, arguments := buildArgumentMetadata(map[string]string{})
	if len(names) != 0 || len(arguments) != 0 {
		t.Errorf("expected empty slices for empty input; got names=%v arguments=%v", names, arguments)
	}
}

func TestRedactArgValue_Truncation(t *testing.T) {
	// Unicode-safe: 80 runes of multi-byte content
	unicodeVal := strings.Repeat("é", 90)
	result := redactArgValue("safe", unicodeVal)
	runes := []rune(result)
	if len(runes) != maxArgValueLen+1 {
		t.Errorf("expected %d runes (80 + ellipsis), got %d", maxArgValueLen+1, len(runes))
	}
}
