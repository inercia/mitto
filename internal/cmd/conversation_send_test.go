package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/pkg/api"
)

// withSendCmd builds a throwaway *cobra.Command carrying only the "prompt"
// and "prompt-name" flags resolveSendBody inspects via Changed(), and sets
// conversationSendFlags to f for the duration of the test (restored on
// cleanup). Using a fresh command (rather than the conversationSendCmd
// singleton) avoids cross-test Changed()/flag-value leakage.
func withSendCmd(t *testing.T, f sendFlags, promptChanged, promptNameChanged bool, stdin string) *cobra.Command {
	t.Helper()
	old := conversationSendFlags
	conversationSendFlags = f
	t.Cleanup(func() { conversationSendFlags = old })

	cmd := &cobra.Command{}
	cmd.Flags().String("prompt", "", "")
	cmd.Flags().String("prompt-name", "", "")
	if promptChanged {
		if err := cmd.Flags().Set("prompt", "x"); err != nil {
			t.Fatalf("Set(prompt): %v", err)
		}
	}
	if promptNameChanged {
		if err := cmd.Flags().Set("prompt-name", "x"); err != nil {
			t.Fatalf("Set(prompt-name): %v", err)
		}
	}
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	return cmd
}

func usageErr(t *testing.T, err error) *exitCodeError {
	t.Helper()
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitUsage {
		t.Errorf("ExitCode() = %d, want exitUsage (%d)", ec.ExitCode(), exitUsage)
	}
	return ec
}

func TestResolveSendBody_NoSourceIsUsageError(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{}, false, false, "")
	_, _, _, _, err := resolveSendBody(cmd, []string{"conv-id"})
	usageErr(t, err)
}

func TestResolveSendBody_TwoSourcesIsUsageError(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{Prompt: "hi"}, true, false, "")
	_, _, _, _, err := resolveSendBody(cmd, []string{"conv-id", "positional text"})
	usageErr(t, err)
}

func TestResolveSendBody_PositionalText(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{}, false, false, "")
	text, usingName, name, args, err := resolveSendBody(cmd, []string{"conv-id", "hello there"})
	if err != nil {
		t.Fatalf("resolveSendBody: %v", err)
	}
	if usingName || name != "" || args != nil {
		t.Errorf("expected free-text result, got usingName=%v name=%q args=%v", usingName, name, args)
	}
	if text != "hello there" {
		t.Errorf("text = %q, want %q", text, "hello there")
	}
}

func TestResolveSendBody_PromptFlag(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{Prompt: "from flag"}, true, false, "")
	text, usingName, _, _, err := resolveSendBody(cmd, []string{"conv-id"})
	if err != nil {
		t.Fatalf("resolveSendBody: %v", err)
	}
	if usingName {
		t.Error("expected free-text result")
	}
	if text != "from flag" {
		t.Errorf("text = %q, want %q", text, "from flag")
	}
}

func TestResolveSendBody_PromptNameWithArgs(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{PromptName: "greet", Args: []string{"a=1", "b=2"}}, false, true, "")
	text, usingName, name, args, err := resolveSendBody(cmd, []string{"conv-id"})
	if err != nil {
		t.Fatalf("resolveSendBody: %v", err)
	}
	if !usingName || name != "greet" || text != "" {
		t.Fatalf("got usingName=%v name=%q text=%q, want a named-prompt result", usingName, name, text)
	}
	if args["a"] != "1" || args["b"] != "2" || len(args) != 2 {
		t.Errorf("args = %v, want {a:1 b:2}", args)
	}
}

func TestResolveSendBody_ArgWithoutPromptNameIsUsageError(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{Args: []string{"a=1"}}, false, false, "")
	_, _, _, _, err := resolveSendBody(cmd, []string{"conv-id", "text"})
	ec := usageErr(t, err)
	if !strings.Contains(ec.Error(), "--arg requires --prompt-name") {
		t.Errorf("error = %q, want it to mention --arg requires --prompt-name", ec.Error())
	}
}

func TestResolveSendBody_ImageWithPromptNameIsUsageError(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{PromptName: "greet", Images: []string{"/tmp/x.png"}}, false, true, "")
	_, _, _, _, err := resolveSendBody(cmd, []string{"conv-id"})
	ec := usageErr(t, err)
	if !strings.Contains(ec.Error(), "--image cannot be combined with --prompt-name") {
		t.Errorf("error = %q, want it to mention the image/prompt-name conflict", ec.Error())
	}
}

func TestResolveSendBody_MalformedArgIsUsageError(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{PromptName: "greet", Args: []string{"noequals"}}, false, true, "")
	_, _, _, _, err := resolveSendBody(cmd, []string{"conv-id"})
	ec := usageErr(t, err)
	if !strings.Contains(ec.Error(), "invalid --arg") {
		t.Errorf("error = %q, want it to mention the malformed --arg", ec.Error())
	}
}

func TestResolveSendBody_PositionalDashReadsStdin(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{}, false, false, "from stdin\n")
	text, _, _, _, err := resolveSendBody(cmd, []string{"conv-id", "-"})
	if err != nil {
		t.Fatalf("resolveSendBody: %v", err)
	}
	if text != "from stdin\n" {
		t.Errorf("text = %q, want the full stdin content", text)
	}
}

func TestResolveSendBody_PromptDashReadsStdin(t *testing.T) {
	cmd := withSendCmd(t, sendFlags{Prompt: "-"}, true, false, "piped text")
	text, _, _, _, err := resolveSendBody(cmd, []string{"conv-id"})
	if err != nil {
		t.Fatalf("resolveSendBody: %v", err)
	}
	if text != "piped text" {
		t.Errorf("text = %q, want the stdin content", text)
	}
}

// --- parseSendArgs ---------------------------------------------------------

func TestParseSendArgs_Empty(t *testing.T) {
	got, err := parseSendArgs(nil)
	if err != nil || got != nil {
		t.Fatalf("parseSendArgs(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestParseSendArgs_ValidPairs(t *testing.T) {
	got, err := parseSendArgs([]string{"a=1", "b=two", "c=with=equals"})
	if err != nil {
		t.Fatalf("parseSendArgs: %v", err)
	}
	want := map[string]string{"a": "1", "b": "two", "c": "with=equals"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseSendArgs_MissingEquals(t *testing.T) {
	_, err := parseSendArgs([]string{"noequals"})
	if err == nil || !strings.Contains(err.Error(), "must be key=value") {
		t.Fatalf("parseSendArgs(malformed) = %v, want a key=value error", err)
	}
}

func TestParseSendArgs_EmptyKey(t *testing.T) {
	_, err := parseSendArgs([]string{"=value"})
	if err == nil {
		t.Fatal("parseSendArgs(empty key) expected an error")
	}
}

// --- uploadSendImages -------------------------------------------------------

func TestUploadSendImages_Empty(t *testing.T) {
	ids, err := uploadSendImages(nil, "conv", nil)
	if err != nil || ids != nil {
		t.Fatalf("uploadSendImages(no paths) = (%v, %v), want (nil, nil)", ids, err)
	}
}

func TestUploadSendImages_UploadsInOrder(t *testing.T) {
	var uploadedNames []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/images") {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		file, hdr, err := r.FormFile("image")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		uploadedNames = append(uploadedNames, hdr.Filename)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"img-` + hdr.Filename + `","url":"/x","name":"` + hdr.Filename + `","mime_type":"image/png"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p1 := filepath.Join(dir, "one.png")
	p2 := filepath.Join(dir, "two.png")
	if err := os.WriteFile(p1, []byte("fake-png-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte("fake-png-2"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := api.New(srv.URL)
	ids, err := uploadSendImages(c, "conv", []string{p1, p2})
	if err != nil {
		t.Fatalf("uploadSendImages: %v", err)
	}
	if len(ids) != 2 || ids[0] != "img-one.png" || ids[1] != "img-two.png" {
		t.Fatalf("ids = %v, want [img-one.png img-two.png] in order", ids)
	}
	if len(uploadedNames) != 2 || uploadedNames[0] != "one.png" || uploadedNames[1] != "two.png" {
		t.Fatalf("uploadedNames = %v, want sequential upload order", uploadedNames)
	}
}

func TestUploadSendImages_MissingFileIsError(t *testing.T) {
	c := api.New("http://unused.invalid")
	_, err := uploadSendImages(c, "conv", []string{filepath.Join(t.TempDir(), "missing.png")})
	if err == nil || !strings.Contains(err.Error(), "reading image") {
		t.Fatalf("uploadSendImages(missing file) = %v, want a 'reading image' error", err)
	}
}

// --- enqueueSend -------------------------------------------------------------

func TestEnqueueSend_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/queue") {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body := readAll(t, r)
		if !strings.Contains(body, `"message":"hello"`) || strings.Contains(body, "prompt_name") {
			t.Fatalf("request body = %q, want a plain-text enqueue", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"q-1","message":"hello","queued_at":"now"}`))
	}))
	defer srv.Close()

	c := api.New(srv.URL)
	msg, err := enqueueSend(c, "conv", false, "", nil, "hello", nil)
	if err != nil {
		t.Fatalf("enqueueSend: %v", err)
	}
	if msg.ID != "q-1" || msg.Message != "hello" {
		t.Errorf("msg = %+v, want id q-1 / message hello", msg)
	}
}

func TestEnqueueSend_WithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		if !strings.Contains(body, `"image_ids":["img-1","img-2"]`) {
			t.Fatalf("request body = %q, want image_ids to be forwarded", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"q-2","message":"with images","image_ids":["img-1","img-2"],"queued_at":"now"}`))
	}))
	defer srv.Close()

	c := api.New(srv.URL)
	msg, err := enqueueSend(c, "conv", false, "", nil, "with images", []string{"img-1", "img-2"})
	if err != nil {
		t.Fatalf("enqueueSend: %v", err)
	}
	if len(msg.ImageIDs) != 2 {
		t.Errorf("msg.ImageIDs = %v, want 2 ids", msg.ImageIDs)
	}
}

func TestEnqueueSend_NamedPromptWithArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		if !strings.Contains(body, `"prompt_name":"greet"`) || !strings.Contains(body, `"arguments":{"who":"world"}`) {
			t.Fatalf("request body = %q, want prompt_name+arguments", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"q-3","queued_at":"now"}`))
	}))
	defer srv.Close()

	c := api.New(srv.URL)
	msg, err := enqueueSend(c, "conv", true, "greet", map[string]string{"who": "world"}, "", nil)
	if err != nil {
		t.Fatalf("enqueueSend: %v", err)
	}
	if msg.ID != "q-3" {
		t.Errorf("msg.ID = %q, want q-3", msg.ID)
	}
}

func readAll(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading request body: %v", err)
	}
	return string(data)
}

// --- sendTableFn -------------------------------------------------------------

func TestSendTableFn(t *testing.T) {
	q := &api.QueuedMessage{ID: "q-1", QueuedAt: "2026-01-01T00:00:00Z", Title: "My Title"}
	headers, rows := sendTableFn(q)()
	if len(headers) != 3 || headers[0] != "ID" || headers[1] != "QUEUED AT" || headers[2] != "TITLE" {
		t.Errorf("headers = %v, want ID/QUEUED AT/TITLE", headers)
	}
	if len(rows) != 1 || rows[0][0] != "q-1" || rows[0][1] != "2026-01-01T00:00:00Z" || rows[0][2] != "My Title" {
		t.Errorf("rows = %v, want the queued message's fields", rows)
	}
}
