package acp

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestJSONLineFilterReader_PassesValidJSON(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"test"}
{"jsonrpc":"2.0","result":null}
`
	reader := NewJSONLineFilterReader(strings.NewReader(input), nil)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(output) != input {
		t.Errorf("expected %q, got %q", input, string(output))
	}
}

func TestJSONLineFilterReader_FiltersNonJSON(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"test"}
 ╭──────────────────────────────────────────────────────────────────────────────╮
 │ Oops, something went wrong (╯°□°)╯︵ ┻━┻                                     │
 ╰──────────────────────────────────────────────────────────────────────────────╯
{"jsonrpc":"2.0","result":null}
`
	expected := `{"jsonrpc":"2.0","method":"test"}
{"jsonrpc":"2.0","result":null}
`
	reader := NewJSONLineFilterReader(strings.NewReader(input), nil)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}

func TestJSONLineFilterReader_FiltersANSIEscapes(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"init"}
` + "\x1b[?1004h\x1b[>1u\x1b[?1004l\x1b[<u" + `
{"jsonrpc":"2.0","result":null}
`
	expected := `{"jsonrpc":"2.0","method":"init"}
{"jsonrpc":"2.0","result":null}
`
	reader := NewJSONLineFilterReader(strings.NewReader(input), nil)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}

func TestJSONLineFilterReader_SkipsEmptyLines(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"test"}

   
{"jsonrpc":"2.0","result":null}
`
	expected := `{"jsonrpc":"2.0","method":"test"}
{"jsonrpc":"2.0","result":null}
`
	reader := NewJSONLineFilterReader(strings.NewReader(input), nil)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}

func TestJSONLineFilterReader_LogsFilteredLines(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	input := `{"jsonrpc":"2.0","method":"test"}
Some non-JSON output
{"jsonrpc":"2.0","result":null}
`
	reader := NewJSONLineFilterReader(strings.NewReader(input), logger)
	_, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "filtered non-JSON line") {
		t.Errorf("expected log message about filtered line, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "Some non-JSON output") {
		t.Errorf("expected filtered content in log, got: %s", logOutput)
	}
}

func TestJSONLineFilterReader_HandlesSmallBuffer(t *testing.T) {
	// Test reading with a small buffer to ensure chunked reading works
	input := `{"jsonrpc":"2.0","method":"test","params":{"message":"hello world"}}
`
	reader := NewJSONLineFilterReader(strings.NewReader(input), nil)

	buf := make([]byte, 10) // Small buffer
	var output []byte
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			output = append(output, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if string(output) != input {
		t.Errorf("expected %q, got %q", input, string(output))
	}
}

func TestJSONLineFilterReader_EmptyInput(t *testing.T) {
	reader := NewJSONLineFilterReader(strings.NewReader(""), nil)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(output) != 0 {
		t.Errorf("expected empty output, got %q", string(output))
	}
}
