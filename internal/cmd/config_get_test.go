package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configsvc"
)

// withConfigGetFlags sets configGetFlags to f for the duration of the test,
// restoring the previous value on cleanup (mirrors withAuthFlags in
// auth_test.go).
func withConfigGetFlags(t *testing.T, f serverFlags) {
	t.Helper()
	old := configGetFlags
	configGetFlags = f
	t.Cleanup(func() { configGetFlags = old })
}

// withConfigGetModes sets the config-get mode flags (--offline/--effective/
// --explain/--raw) for the duration of the test, restoring the prior values
// on cleanup. runConfigGet reads these package-level vars directly, the same
// way config_get.go's own init() binds them to cobra flags.
func withConfigGetModes(t *testing.T, offline, effective, explain, raw bool) {
	t.Helper()
	oldOffline, oldEffective, oldExplain, oldRaw := configGetOffline, configGetEffective, configGetExplain, configGetRaw
	configGetOffline, configGetEffective, configGetExplain, configGetRaw = offline, effective, explain, raw
	t.Cleanup(func() {
		configGetOffline, configGetEffective, configGetExplain, configGetRaw = oldOffline, oldEffective, oldExplain, oldRaw
	})
}

// newConfigGetTestCmd builds a throwaway *cobra.Command with buffered
// stdout/stderr for exercising runConfigGet directly (mirrors auth_test.go's
// runAuthStatus(cmd, nil) pattern).
func newConfigGetTestCmd() (*cobra.Command, *strings.Builder, *strings.Builder) {
	cmd := &cobra.Command{}
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	return cmd, &out, &errOut
}

// writeConfigGetSettings writes body as settings.json under the current
// (test-scoped, via clearServerEnv) MITTO_DIR.
func writeConfigGetSettings(t *testing.T, body string) {
	t.Helper()
	path, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("appdir.SettingsPath: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

func notFoundErr(t *testing.T, err error) *exitCodeError {
	t.Helper()
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitNotFound {
		t.Errorf("ExitCode() = %d, want exitNotFound (%d)", ec.ExitCode(), exitNotFound)
	}
	return ec
}

// --- offline mode ---------------------------------------------------------

func TestConfigGet_Offline_MissingSettingsFile_WholeDoc_NotFound(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	notFoundErr(t, runConfigGet(cmd, nil))
}

func TestConfigGet_Offline_WholeDoc(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"port":9999}}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, nil); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if !strings.Contains(out.String(), "9999") {
		t.Errorf("stdout = %q, want it to contain the stored web.port", out.String())
	}
}

func TestConfigGet_Offline_PathLookup_Stored(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"port":9999}}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"web.port"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if strings.TrimSpace(out.String()) != "9999" {
		t.Errorf("stdout = %q, want 9999", out.String())
	}
}

func TestConfigGet_Offline_Raw_Scalar(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, true)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"port":9999}}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"web.port"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if strings.TrimSpace(out.String()) != "9999" {
		t.Errorf("--raw stdout = %q, want bare 9999 (no JSON quoting)", out.String())
	}
}

func TestConfigGet_Offline_Raw_RejectsWholeDoc(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, true)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"port":9999}}`)

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, nil))
}

func TestConfigGet_Offline_Effective_Default(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, true, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"mcp.port"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	// mcp is a redacted dynamic root: even the compile-time default must
	// come back hidden, never the raw 5757.
	if !strings.Contains(out.String(), configsvc.RedactedPlaceholder) {
		t.Errorf("stdout = %q, want the redacted placeholder for the mcp.port default", out.String())
	}
}

func TestConfigGet_Offline_Effective_Default_NonSecret(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, true, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"web.external_port"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if strings.TrimSpace(out.String()) != "-1" {
		t.Errorf("stdout = %q, want the compile-time default -1", out.String())
	}
}

func TestConfigGet_Offline_NotStored_WithoutEffective_NotFound(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{}`)

	cmd, _, _ := newConfigGetTestCmd()
	notFoundErr(t, runConfigGet(cmd, []string{"web.external_port"}))
}

// TestConfigGet_Offline_Explain_DynamicRootSubpath_Redacted pins the
// mitto-4rz.4 Implement-phase fix at the CLI layer: a path strictly beneath
// a redacted dynamic root ("mcp") must be reported as redacted via
// --explain, and its value must never leak the raw stored number.
func TestConfigGet_Offline_Explain_DynamicRootSubpath_Redacted(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, true, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"mcp":{"host":"127.0.0.1","port":5757}}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"mcp.port"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if !strings.Contains(out.String(), `"redacted": true`) {
		t.Errorf("stdout = %q, want redacted: true for mcp.port", out.String())
	}
	if !strings.Contains(out.String(), configsvc.RedactedPlaceholder) {
		t.Errorf("stdout = %q, want the redacted placeholder value", out.String())
	}
	if strings.Contains(out.String(), "5757") {
		t.Errorf("stdout = %q, leaked the raw stored mcp.port value", out.String())
	}
}

func TestConfigGet_Offline_Redaction_Password(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"auth":{"simple":{"password":"hunter2"}}}}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"web.auth.simple.password"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if strings.Contains(out.String(), "hunter2") {
		t.Errorf("stdout leaked the raw password: %q", out.String())
	}
	if !strings.Contains(out.String(), configsvc.RedactedPlaceholder) {
		t.Errorf("stdout = %q, want the redacted placeholder", out.String())
	}
}

func TestConfigGet_Offline_ArrayIndexPath(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, true)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"task_label_colors":[{"label":"bug","color":"#ff0000"}]}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"task_label_colors[0].color"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if strings.TrimSpace(out.String()) != "#ff0000" {
		t.Errorf("stdout = %q, want #ff0000", out.String())
	}
}

func TestConfigGet_Offline_MissingPath_NotFound(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"port":9999}}`)

	cmd, _, _ := newConfigGetTestCmd()
	notFoundErr(t, runConfigGet(cmd, []string{"totally.unknown.path"}))
}

// TestConfigGet_Offline_StoredNull_IsFoundNotMissing pins the plan's
// "missing vs. stored-null" distinction: a path present in settings.json
// with a JSON null value is a successful read (exit 0, prints "null"), not
// exit 5 (not-found).
func TestConfigGet_Offline_StoredNull_IsFoundNotMissing(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})
	writeConfigGetSettings(t, `{"web":{"external_port":null}}`)

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"web.external_port"}); err != nil {
		t.Fatalf("runConfigGet: %v, want success for a stored null (present, not missing)", err)
	}
	if strings.TrimSpace(out.String()) != "null" {
		t.Errorf("stdout = %q, want \"null\"", out.String())
	}
}

// --- usage errors (mode/flag validation) -----------------------------------

func TestConfigGet_Effective_WithoutOffline_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, true, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, []string{"web.port"}))
}

func TestConfigGet_Explain_WithoutOffline_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, true, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, []string{"web.port"}))
}

func TestConfigGet_Explain_WithoutPath_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, true, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, nil))
}

func TestConfigGet_RawAndExplain_MutuallyExclusive_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, true, true)
	withConfigGetFlags(t, serverFlags{Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, []string{"web.port"}))
}

func TestConfigGet_InvalidPath_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, true, false, false, false)
	withConfigGetFlags(t, serverFlags{Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, []string{"..bad..path"}))
}

// --- live mode --------------------------------------------------------------

func newFakeConfigSnapshotServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/config/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestConfigGet_Live_WholeDoc(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, false, false)
	srv := newFakeConfigSnapshotServer(t, http.StatusOK, `{"exists":true,"revision":"1-1","config":{"web":{"port":8080}}}`)
	withConfigGetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, nil); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if !strings.Contains(out.String(), "8080") {
		t.Errorf("stdout = %q, want it to contain 8080", out.String())
	}
}

func TestConfigGet_Live_PathLookup(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, false, true)
	srv := newFakeConfigSnapshotServer(t, http.StatusOK, `{"exists":true,"revision":"1-1","config":{"web":{"port":8080}}}`)
	withConfigGetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})

	cmd, out, _ := newConfigGetTestCmd()
	if err := runConfigGet(cmd, []string{"web.port"}); err != nil {
		t.Fatalf("runConfigGet: %v", err)
	}
	if strings.TrimSpace(out.String()) != "8080" {
		t.Errorf("stdout = %q, want 8080", out.String())
	}
}

func TestConfigGet_Live_PathNotFound(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, false, false)
	srv := newFakeConfigSnapshotServer(t, http.StatusOK, `{"exists":true,"revision":"1-1","config":{"web":{"port":8080}}}`)
	withConfigGetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	notFoundErr(t, runConfigGet(cmd, []string{"totally.unknown"}))
}

func TestConfigGet_Live_SnapshotDoesNotExist_NotFound(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, false, false)
	srv := newFakeConfigSnapshotServer(t, http.StatusOK, `{"exists":false}`)
	withConfigGetFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	notFoundErr(t, runConfigGet(cmd, nil))
}

func TestConfigGet_Live_ServerAuthError_Classified(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, false, false)
	srv := newFakeConfigSnapshotServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthenticated","message":"bad token"}}`)
	withConfigGetFlags(t, serverFlags{URL: srv.URL, Token: "bad", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	err := runConfigGet(cmd, nil)
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitAuthFailure {
		t.Errorf("ExitCode() = %d, want exitAuthFailure (%d)", ec.ExitCode(), exitAuthFailure)
	}
}

// TestConfigGet_Live_ExplicitURLWithoutToken_UsageError pins the
// mitto-4rz.4 Plan's decision 7: an explicit --url given without an
// explicit --token must be refused rather than silently attaching this
// machine's local instance.json bearer token to an unrelated remote host.
func TestConfigGet_Live_ExplicitURLWithoutToken_UsageError(t *testing.T) {
	clearServerEnv(t)
	withConfigGetModes(t, false, false, false, false)
	withConfigGetFlags(t, serverFlags{URL: "http://example.invalid", Timeout: 5 * time.Second, Output: "json"})

	cmd, _, _ := newConfigGetTestCmd()
	usageErr(t, runConfigGet(cmd, []string{"web.port"}))
}
