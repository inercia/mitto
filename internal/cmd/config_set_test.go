package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configpath"
	"github.com/inercia/mitto/internal/config/configsvc"
	"github.com/inercia/mitto/pkg/api"
)

// withConfigSetFlags sets configSetFlags to f for the duration of the test,
// restoring the previous value on cleanup (mirrors withConfigGetFlags).
func withConfigSetFlags(t *testing.T, f serverFlags) {
	t.Helper()
	old := configSetFlags
	configSetFlags = f
	t.Cleanup(func() { configSetFlags = old })
}

// withConfigSetModes sets the config-set mode flags (--offline/--dry-run/
// --revision) for the duration of the test, restoring the prior values on
// cleanup. runConfigSet reads these package-level vars directly, the same
// way config_set.go's own init() binds them to cobra flags.
func withConfigSetModes(t *testing.T, offline, dryRun bool, revision string) {
	t.Helper()
	oldOffline, oldDryRun, oldRevision := configSetOffline, configSetDryRun, configSetRevision
	configSetOffline, configSetDryRun, configSetRevision = offline, dryRun, revision
	t.Cleanup(func() {
		configSetOffline, configSetDryRun, configSetRevision = oldOffline, oldDryRun, oldRevision
	})
}

// withConfigSetOrder seeds the shared cross-flag accumulator directly,
// simulating what pflag would have produced by calling Set() left-to-right
// across argv for a given sequence of --set/--set-string/--set-json/
// --set-file occurrences (see configSetOrderedValue.Set in config_set.go).
// This lets tests pin exact argument-order scenarios without going through
// cobra's flag parser. runConfigSet's expandConfigSetOrder consumes (nils
// out) the accumulator, so restoring on cleanup is only needed for tests
// that don't call runConfigSet at all.
func withConfigSetOrder(t *testing.T, entries ...configSetRawEntry) {
	t.Helper()
	old := configSetOrder
	configSetOrder = append([]configSetRawEntry(nil), entries...)
	t.Cleanup(func() { configSetOrder = old })
}

// withStdin temporarily replaces os.Stdin with a pipe pre-loaded with
// content, for exercising expandSetFileEntry's "-" (stdin) source — which
// reads os.Stdin directly, not cmd.InOrStdin().
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
	go func() {
		_, _ = w.WriteString(content)
		w.Close()
	}()
}

func typed(pathEqValue string) configSetRawEntry {
	return configSetRawEntry{mode: configpath.ModeTyped, raw: pathEqValue}
}

func jsonEntry(pathEqValue string) configSetRawEntry {
	return configSetRawEntry{mode: configpath.ModeJSON, raw: pathEqValue}
}

func fileEntry(pathEqSource string) configSetRawEntry {
	return configSetRawEntry{mode: configpath.ModeFile, raw: pathEqSource}
}

func genericErr(t *testing.T, err error) *exitCodeError {
	t.Helper()
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitGeneric {
		t.Errorf("ExitCode() = %d, want exitGeneric (%d)", ec.ExitCode(), exitGeneric)
	}
	return ec
}

func storedWebPort(t *testing.T) int64 {
	t.Helper()
	snap, err := configsvc.ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	fv, err := snap.Get(mustConfigSetPath(t, "web.port"))
	if err != nil {
		t.Fatalf("snap.Get(web.port): %v", err)
	}
	switch n := fv.Value.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		v, err := n.Int64()
		if err != nil {
			t.Fatalf("web.port stored as non-integer json.Number %q: %v", n, err)
		}
		return v
	default:
		t.Fatalf("web.port stored as unexpected type %T: %v", fv.Value, fv.Value)
		return 0
	}
}

func mustConfigSetPath(t *testing.T, s string) configpath.Path {
	t.Helper()
	p, err := configpath.ParsePath(s)
	if err != nil {
		t.Fatalf("ParsePath(%q): %v", s, err)
	}
	return p
}

// --- cross-flag argument-order precedence (mitto-4rz.5 Plan, decision 1) --

// TestConfigSet_ArgOrder_SetThenSetJSON_JSONWins pins that
// `--set web.port=9090 --set-json web.port=9091` resolves to the JSON value
// (last on the command line), regardless of which flag/mode produced it.
func TestConfigSet_ArgOrder_SetThenSetJSON_JSONWins(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withConfigSetOrder(t, typed("web.port=9090"), jsonEntry("web.port=9091"))

	cmd, _, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	if got := storedWebPort(t); got != 9091 {
		t.Errorf("stored web.port = %d, want 9091 (the later --set-json)", got)
	}
}

// TestConfigSet_ArgOrder_SetJSONThenSet_SetWins is the reverse of the above:
// reversing the flags must reverse the winner.
func TestConfigSet_ArgOrder_SetJSONThenSet_SetWins(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withConfigSetOrder(t, jsonEntry("web.port=9091"), typed("web.port=9090"))

	cmd, _, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	if got := storedWebPort(t); got != 9090 {
		t.Errorf("stored web.port = %d, want 9090 (the later --set)", got)
	}
}

// --- --set-file (mitto-4rz.5 Plan, decision 2) -----------------------------

func TestConfigSet_SetFile_FromRealFile(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})

	path := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(path, []byte("Review the PR"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	withConfigSetOrder(t, fileEntry("shortcuts.tasksList[0].prompt="+path))

	cmd, _, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}

	snap, err := configsvc.ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	fv, err := snap.Get(mustConfigSetPath(t, "shortcuts.tasksList[0].prompt"))
	if err != nil {
		t.Fatalf("snap.Get: %v", err)
	}
	if fv.Value != "Review the PR" {
		t.Errorf("stored prompt = %v, want %q", fv.Value, "Review the PR")
	}
}

func TestConfigSet_SetFile_FromStdin(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withStdin(t, "From stdin content")
	withConfigSetOrder(t, fileEntry("shortcuts.tasksList[0].prompt=-"))

	cmd, _, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}

	snap, err := configsvc.ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	fv, err := snap.Get(mustConfigSetPath(t, "shortcuts.tasksList[0].prompt"))
	if err != nil {
		t.Fatalf("snap.Get: %v", err)
	}
	if fv.Value != "From stdin content" {
		t.Errorf("stored prompt = %v, want %q", fv.Value, "From stdin content")
	}
}

// TestConfigSet_SetFile_MultipleStdin_UsageError pins the "at most one
// stdin source per invocation" guard: a second "-" source, even targeting a
// different path, is rejected before any second read is attempted.
func TestConfigSet_SetFile_MultipleStdin_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withStdin(t, "first")
	withConfigSetOrder(t,
		fileEntry("shortcuts.tasksList[0].prompt=-"),
		fileEntry("shortcuts.tasksList[0].icon=-"),
	)

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigSet(cmd, nil))
}

// TestConfigSet_SetFile_OversizeContent_Errors pins that content exceeding
// configpath.MaxFileValueBytes is a hard error, never a silent truncation.
func TestConfigSet_SetFile_OversizeContent_Errors(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})

	path := filepath.Join(t.TempDir(), "big.txt")
	big := strings.Repeat("x", configpath.MaxFileValueBytes+1)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	withConfigSetOrder(t, fileEntry("shortcuts.tasksList[0].prompt="+path))

	cmd, _, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err == nil {
		t.Fatalf("expected an error for oversize --set-file content")
	}
}

// --- --dry-run / usage validation -------------------------------------------

func TestConfigSet_DryRun_NoPersist_ReportsWouldApply(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, true, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withConfigSetOrder(t, typed("web.port=9090"))

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	if !strings.Contains(out.String(), "would_apply") {
		t.Errorf("stdout = %q, want it to report would_apply", out.String())
	}

	path, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("--dry-run must not persist; settings.json stat = %v", statErr)
	}
}

func TestConfigSet_NoAssignments_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withConfigSetOrder(t)

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigSet(cmd, nil))
}

func TestConfigSet_Offline_RealWrite_ReportsRestartRequiredAndRevision(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, true, false, "")
	withConfigSetFlags(t, serverFlags{Output: "json"})
	withConfigSetOrder(t, typed("web.port=9090"))

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "restart_required") {
		t.Errorf("stdout = %q, want restart_required (web.port's Liveness)", got)
	}
	if !strings.Contains(got, `"revision"`) {
		t.Errorf("stdout = %q, want a revision after a successful non-dry-run write", got)
	}
}

// --- live mode ---------------------------------------------------------------

func TestConfigSet_Live_SendsConvertedOpsAndDecodesResponse(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, false, false, "")
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/config/patch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.Contains(readConfigPatchBody(t, r), `"value":9090`) {
			t.Errorf("request body missing converted numeric value")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"dry_run":false,"applied":[{"path":"web.port","status":"restart_required"}],"revision":"789-1"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withConfigSetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})
	withConfigSetOrder(t, typed("web.port=9090"))

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigSet(cmd, nil); err != nil {
		t.Fatalf("runConfigSet: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "restart_required") || !strings.Contains(got, "789-1") {
		t.Errorf("stdout = %q, want the decoded restart_required status and revision", got)
	}
}

func readConfigPatchBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(body)
}

// --- exit-code classifiers (mitto-4rz.5 Plan, decision 6) -------------------

func TestClassifyConfigsvcErr_MapsKindsToExitCodes(t *testing.T) {
	cases := []struct {
		kind configsvc.ErrKind
		want int
	}{
		{configsvc.ErrKindUnknownField, exitUsage},
		{configsvc.ErrKindValidation, exitUsage},
		{configsvc.ErrKindConflict, exitUsage},
		{configsvc.ErrKindStructure, exitUsage},
		{configsvc.ErrKindRejected, exitUsage},
		{configsvc.ErrKindReadOnly, exitUsage},
		{configsvc.ErrKindNotFound, exitNotFound},
		{configsvc.ErrKindRevisionMismatch, exitGeneric},
		{configsvc.ErrKindLocked, exitGeneric},
		{configsvc.ErrKindIO, exitGeneric},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			err := &configsvc.Error{Kind: tc.kind, Msg: "boom"}
			got := classifyConfigsvcErr(err)
			var ec *exitCodeError
			if !errors.As(got, &ec) {
				t.Fatalf("expected *exitCodeError, got %T: %v", got, got)
			}
			if ec.ExitCode() != tc.want {
				t.Errorf("ExitCode() = %d, want %d", ec.ExitCode(), tc.want)
			}
		})
	}
}

func TestClassifyConfigSetErr_MapsAPIErrorsToExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"forbidden -> usage (rejected/read-only field)", api.ErrForbidden, exitUsage},
		{"bad request -> usage (validation/unknown field)", api.ErrBadRequest, exitUsage},
		{"conflict -> generic (stale revision)", api.ErrConflict, exitGeneric},
		{"unauthenticated -> falls through to classify()", api.ErrUnauthenticated, exitAuthFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyConfigSetErr(tc.err)
			var ec *exitCodeError
			if !errors.As(got, &ec) {
				t.Fatalf("expected *exitCodeError, got %T: %v", got, got)
			}
			if ec.ExitCode() != tc.want {
				t.Errorf("ExitCode() = %d, want %d", ec.ExitCode(), tc.want)
			}
		})
	}
}

func TestConfigSet_Live_RejectedField_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, false, false, "")
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/config/patch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":"forbidden","message":"rejected: field cannot be modified through this service"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withConfigSetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})
	withConfigSetOrder(t, typed("web.auth.shared_token=x"))

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigSet(cmd, nil))
}

func TestConfigSet_Live_RevisionConflict_GenericError(t *testing.T) {
	clearServerEnv(t)
	withConfigSetModes(t, false, false, "stale-rev")
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/config/patch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"conflict","message":"revision_mismatch: settings changed since snapshot was read"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withConfigSetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})
	withConfigSetOrder(t, typed("web.port=9090"))

	cmd, _, _ := newConfigGetTestCmd()
	genericErr(t, runConfigSet(cmd, nil))
}
