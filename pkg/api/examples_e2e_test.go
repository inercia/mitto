package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// buildExample compiles examples/go-client/<name> into a temp binary and
// returns its path. Building the real program (rather than re-testing pkg/api
// in isolation) pins the bead's core acceptance criterion: the runnable
// examples must actually work as external Go programs, not just compile.
func buildExample(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./examples/go-client/"+name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build examples/go-client/%s: %v\n%s", name, err, out)
	}
	return bin
}

// TestExample_ListConversations runs the list-conversations example against
// a fake REST server and asserts it prints the session fields it advertises
// (id, status, name) per its README/-h usage.
func TestExample_ListConversations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"session_id": "sess-1", "status": "idle", "name": "demo-session"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bin := buildExample(t, "list-conversations")
	out, err := exec.Command(bin, "-url", srv.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("list-conversations: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"sess-1", "idle", "demo-session"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
}

// TestExample_PromptStream runs the prompt-stream example end-to-end against
// a fake REST+WebSocket server: create session -> connect -> send prompt ->
// stream agent_message -> prompt_complete. This is the bead's headline
// acceptance criterion ("the CLI example is the proof that the library is
// genuinely usable from an external Go program").
func TestExample_PromptStream(t *testing.T) {
	const agentReply = "Hello from the mock agent"
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"session_id": "sess-42"})
	})
	mux.HandleFunc("/mitto/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/ws"):
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			// Wait for the client's "prompt" message before replying, mirroring
			// the real server's request/response ordering.
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "agent_message", "seq": 1, "max_seq": 1,
				"data": map[string]string{"html": agentReply},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "prompt_complete", "seq": 2, "max_seq": 2,
				"data": map[string]int{"event_count": 1},
			})
			time.Sleep(100 * time.Millisecond) // let the client drain before the handler returns and closes the conn
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bin := buildExample(t, "prompt-stream")
	out, err := exec.Command(bin,
		"-url", srv.URL,
		"-dir", ".",
		"-prompt", "What does this project do?",
		"-timeout", "10s",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("prompt-stream: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), agentReply) {
		t.Errorf("output %q does not contain streamed agent reply %q", out, agentReply)
	}
}
