package stats

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// fixtureEvent mirrors the shape of ACP replay events emitted by
// internal/conversation/testdata/acp/*.jsonl. Only the fields we exercise for
// classification are decoded (Title / Status / Kind, plus tool_call_id).
type fixtureEvent struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
}

type fixtureToolCall struct {
	ToolCallID string `json:"tool_call_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Kind       string `json:"kind,omitempty"`
}

// TestIsMCPCall_ACPFixtures satisfies the bead's acceptance criterion
// "Unit tests with a corpus of real tool_call events (grab from
// internal/conversation/testdata/acp/)". Every current fixture uses non-MCP
// titles ("Read file: main.go", "Write file: main.go"), so the classifier
// must classify all of them as non-MCP — pinning false-positive rate to 0
// for known real-shaped data.
func TestIsMCPCall_ACPFixtures(t *testing.T) {
	dir := filepath.Join("..", "conversation", "testdata", "acp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixture dir %q: %v", dir, err)
	}

	type perFile struct {
		total, mcp int
	}
	summary := map[string]perFile{}
	files := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		files++
		path := filepath.Join(dir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}

		var stats perFile
		sc := bufio.NewScanner(f)
		// ACP fixtures include lines up to a few KB — expand the buffer.
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			raw := strings.TrimSpace(sc.Text())
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}
			var ev fixtureEvent
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				t.Fatalf("%s:%d: parse event: %v", path, lineNo, err)
			}
			if ev.Type != "tool_call" {
				continue
			}
			var tc fixtureToolCall
			if err := json.Unmarshal(ev.Data, &tc); err != nil {
				t.Fatalf("%s:%d: parse tool_call data: %v", path, lineNo, err)
			}
			td := session.ToolCallData{
				ToolCallID: tc.ToolCallID,
				Title:      tc.Title,
				Status:     tc.Status,
				Kind:       tc.Kind,
			}
			stats.total++
			got := IsMCPCall(td)
			if got {
				stats.mcp++
				t.Errorf("%s:%d: IsMCPCall({Title:%q, Kind:%q}) = true, want false (no MCP-shaped tool_call titles in current fixtures)",
					path, lineNo, td.Title, td.Kind)
			}
		}
		if err := sc.Err(); err != nil {
			f.Close()
			t.Fatalf("scan %s: %v", path, err)
		}
		f.Close()
		summary[entry.Name()] = stats
	}

	if files == 0 {
		t.Fatalf("no .jsonl fixtures found under %s", dir)
	}

	// Deterministic per-file summary for future extension.
	names := make([]string, 0, len(summary))
	for n := range summary {
		names = append(names, n)
	}
	sort.Strings(names)
	var total, totalMCP int
	for _, n := range names {
		s := summary[n]
		total += s.total
		totalMCP += s.mcp
		t.Logf("%s: tool_calls=%d mcp=%d", n, s.total, s.mcp)
	}
	t.Logf("corpus summary: files=%d tool_calls=%d mcp=%d", files, total, totalMCP)
}
