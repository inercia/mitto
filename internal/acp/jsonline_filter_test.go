package acp

import (
	"bytes"
	"encoding/json"
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

func TestJSONLineFilterReader_TransformRunsAfterFilterAndSanitizer(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","content":[{"type":"diff","oldText":{"text":"preserve me"}}]}}}` + "\n"
	var stages []string
	reader := NewJSONLineFilterReaderWithTransform(strings.NewReader(input), nil,
		func([]byte) bool {
			stages = append(stages, "filter")
			return false
		},
		func(line []byte) ([]byte, bool) {
			stages = append(stages, "transform")
			var message struct {
				Params struct {
					Update struct {
						Content []struct {
							OldText string `json:"oldText"`
						} `json:"content"`
					} `json:"update"`
				} `json:"params"`
			}
			if err := json.Unmarshal(line, &message); err != nil {
				t.Errorf("transform received invalid JSON: %v", err)
			} else if got := message.Params.Update.Content[0].OldText; got != "preserve me" {
				t.Errorf("transform ran before standard sanitation: oldText=%q", got)
			}
			return line, false
		})
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read filtered JSON: %v", err)
	}
	if got, want := strings.Join(stages, ","), "filter,transform"; got != want {
		t.Fatalf("hook order = %q, want %q", got, want)
	}
}

func TestJSONLineFilterReader_TransformHandlesPartialSourceReadsAndDiscards(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"before"}` + "\n" + `{"jsonrpc":"2.0","method":"drop"}` + "\n"
	reader := NewJSONLineFilterReaderWithTransform(&shortReadReader{reader: strings.NewReader(input), maxRead: 3}, nil, nil,
		func(line []byte) ([]byte, bool) {
			if bytes.Contains(line, []byte(`"drop"`)) {
				return nil, true
			}
			return bytes.Replace(line, []byte(`"before"`), []byte(`"after"`), 1), false
		})
	buf := make([]byte, 4)
	var output []byte
	for {
		n, err := reader.Read(buf)
		output = append(output, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("partial read: %v", err)
		}
	}
	if got, want := string(output), `{"jsonrpc":"2.0","method":"after"}`+"\n"; got != want {
		t.Fatalf("transformed output = %q, want %q", got, want)
	}
}

type shortReadReader struct {
	reader  io.Reader
	maxRead int
}

func (r *shortReadReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		p = p[:r.maxRead]
	}
	return r.reader.Read(p)
}
