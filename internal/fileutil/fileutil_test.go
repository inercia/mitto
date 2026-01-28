package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

type testData struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestReadJSON(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantErr   bool
		wantName  string
		wantValue int
	}{
		{
			name:      "valid JSON",
			content:   `{"name": "test", "value": 42}`,
			wantErr:   false,
			wantName:  "test",
			wantValue: 42,
		},
		{
			name:    "invalid JSON",
			content: `{"name": "test", invalid}`,
			wantErr: true,
		},
		{
			name:      "empty object",
			content:   `{}`,
			wantErr:   false,
			wantName:  "",
			wantValue: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "test.json")

			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			var data testData
			err := ReadJSON(path, &data)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if data.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", data.Name, tt.wantName)
			}
			if data.Value != tt.wantValue {
				t.Errorf("Value = %d, want %d", data.Value, tt.wantValue)
			}
		})
	}
}

func TestReadJSON_FileNotFound(t *testing.T) {
	var data testData
	err := ReadJSON("/nonexistent/path/file.json", &data)
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestWriteJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "output.json")

	data := testData{Name: "hello", Value: 123}
	err := WriteJSON(path, &data, 0644)
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// Read back and verify
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	// Should be pretty-printed
	expected := "{\n  \"name\": \"hello\",\n  \"value\": 123\n}"
	if string(content) != expected {
		t.Errorf("content = %q, want %q", string(content), expected)
	}
}

func TestWriteJSON_InvalidData(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "output.json")

	// Channels cannot be marshaled to JSON
	data := make(chan int)
	err := WriteJSON(path, data, 0644)
	if err == nil {
		t.Error("expected error for unmarshalable data, got nil")
	}
}

func TestWriteJSONAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "atomic.json")

	data := testData{Name: "atomic", Value: 999}
	err := WriteJSONAtomic(path, &data, 0644)
	if err != nil {
		t.Fatalf("WriteJSONAtomic failed: %v", err)
	}

	// Verify temp file is cleaned up
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}

	// Read back and verify
	var readData testData
	if err := ReadJSON(path, &readData); err != nil {
		t.Fatalf("failed to read back: %v", err)
	}

	if readData.Name != data.Name || readData.Value != data.Value {
		t.Errorf("read data = %+v, want %+v", readData, data)
	}
}

func TestWriteJSONAtomic_InvalidData(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "atomic.json")

	data := make(chan int)
	err := WriteJSONAtomic(path, data, 0644)
	if err == nil {
		t.Error("expected error for unmarshalable data, got nil")
	}
}
