package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/instancefile"
	"github.com/inercia/mitto/pkg/api"
)

// clearServerEnv unsets the env vars resolveTarget consults, restoring the
// original values on test cleanup, and points instancefile at a fresh empty
// temp dir so no real developer machine state leaks into these tests.
func clearServerEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MITTO_URL", "MITTO_TOKEN", "MITTO_API_PREFIX"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
}

func writeInstance(t *testing.T, inst *instancefile.Instance) {
	t.Helper()
	if err := instancefile.Write(inst); err != nil {
		t.Fatalf("instancefile.Write: %v", err)
	}
}

// --- resolveTarget precedence -----------------------------------------

func TestResolveTarget_FlagBeatsEnvAndInstanceFile(t *testing.T) {
	clearServerEnv(t)
	t.Setenv("MITTO_URL", "http://env:1")
	t.Setenv("MITTO_TOKEN", "env-token")
	writeInstance(t, &instancefile.Instance{PID: os.Getpid(), URL: "http://inst:2", APIPrefix: "/mitto", Token: "inst-token"})

	f := &serverFlags{URL: "http://flag:3", Token: "flag-token", APIPrefix: "/mitto"}
	got, err := resolveTarget(f)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://flag:3" || got.Token != "flag-token" {
		t.Errorf("got %+v, want flag values to win", got)
	}
}

func TestResolveTarget_EnvBeatsInstanceFile(t *testing.T) {
	clearServerEnv(t)
	t.Setenv("MITTO_URL", "http://env:1")
	t.Setenv("MITTO_TOKEN", "env-token")
	writeInstance(t, &instancefile.Instance{PID: os.Getpid(), URL: "http://inst:2", APIPrefix: "/mitto", Token: "inst-token"})

	got, err := resolveTarget(&serverFlags{})
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://env:1" || got.Token != "env-token" {
		t.Errorf("got %+v, want env values to win over instance.json", got)
	}
}

func TestResolveTarget_FallsBackToInstanceFile(t *testing.T) {
	clearServerEnv(t)
	writeInstance(t, &instancefile.Instance{PID: os.Getpid(), URL: "http://inst:2", APIPrefix: "/mitto", Token: "inst-token"})

	got, err := resolveTarget(&serverFlags{})
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://inst:2" || got.Token != "inst-token" || got.APIPrefix != "/mitto" {
		t.Errorf("got %+v, want instance.json values", got)
	}
}

// TestResolveTarget_PerFieldIndependence: URL from flag, token from env,
// prefix from instance.json — each field resolves independently rather than
// "if any field is set, ignore the other sources entirely".
func TestResolveTarget_PerFieldIndependence(t *testing.T) {
	clearServerEnv(t)
	t.Setenv("MITTO_TOKEN", "env-token")
	writeInstance(t, &instancefile.Instance{PID: os.Getpid(), URL: "http://inst:2", APIPrefix: "/mitto", Token: "inst-token"})

	got, err := resolveTarget(&serverFlags{URL: "http://flag:3"})
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.URL != "http://flag:3" {
		t.Errorf("URL = %q, want flag value", got.URL)
	}
	if got.Token != "env-token" {
		t.Errorf("Token = %q, want env value", got.Token)
	}
	if got.APIPrefix != "/mitto" {
		t.Errorf("APIPrefix = %q, want instance.json value", got.APIPrefix)
	}
}

func TestResolveTarget_NoInstanceFile_MissingFieldsErrors(t *testing.T) {
	clearServerEnv(t)
	// No instance.json written, no env, no flags -> ErrNotFound path.
	if _, err := resolveTarget(&serverFlags{}); err == nil {
		t.Fatal("expected an error when nothing resolves url/token/prefix")
	}
}

func TestResolveTarget_StaleInstance_MissingFieldsErrors(t *testing.T) {
	clearServerEnv(t)
	// PID 0 is never a running process -> IsStale() true.
	writeInstance(t, &instancefile.Instance{PID: 0, URL: "http://stale:9", APIPrefix: "/mitto", Token: "stale-token"})

	_, err := resolveTarget(&serverFlags{})
	if err == nil {
		t.Fatal("expected an error for a stale instance file with nothing else resolved")
	}
	if !strings.Contains(err.Error(), "http://stale:9") {
		t.Errorf("error should surface the recorded URL for diagnosis: %v", err)
	}
}

// TestResolveTarget_TokenNeverLeaksInErrors is the token-leak assertion:
// none of resolveTarget's error paths may ever include a resolved token
// value, even when a token happens to be part of the (otherwise incomplete)
// resolution state.
func TestResolveTarget_TokenNeverLeaksInErrors(t *testing.T) {
	const secretToken = "super-secret-token-do-not-leak"

	t.Run("stale instance, token present via flag but url missing", func(t *testing.T) {
		clearServerEnv(t)
		writeInstance(t, &instancefile.Instance{PID: 0, URL: "http://stale:9", APIPrefix: "/mitto", Token: "instance-file-token"})
		_, err := resolveTarget(&serverFlags{Token: secretToken})
		if err == nil {
			t.Fatal("expected an error (URL still unresolved)")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Errorf("error leaked the flag token: %v", err)
		}
		if strings.Contains(err.Error(), "instance-file-token") {
			t.Errorf("error leaked the instance.json token: %v", err)
		}
	})

	t.Run("no instance file, token present via env but url missing", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("MITTO_TOKEN", secretToken)
		_, err := resolveTarget(&serverFlags{})
		if err == nil {
			t.Fatal("expected an error (URL still unresolved)")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Errorf("error leaked the env token: %v", err)
		}
	})
}

// --- newClient ----------------------------------------------------------

func TestNewClient_RejectsNonDefaultAPIPrefix(t *testing.T) {
	clearServerEnv(t)
	f := &serverFlags{URL: "http://example:8080", Token: "t", APIPrefix: "/other", Timeout: time.Second}
	_, err := newClient(f)
	if err == nil {
		t.Fatal("expected an error for a non-default --api-prefix")
	}
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitUsage {
		t.Errorf("ExitCode() = %d, want %d (usage)", ec.ExitCode(), exitUsage)
	}
	if !strings.Contains(err.Error(), "mitto-rwxq.7") {
		t.Errorf("error should reference the tracking issue: %v", err)
	}
}

func TestNewClient_DefaultPrefixSucceeds(t *testing.T) {
	clearServerEnv(t)
	f := &serverFlags{URL: "http://example:8080", Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second}
	c, err := newClient(f)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c == nil {
		t.Fatal("newClient returned a nil client with a nil error")
	}
}

func TestNewClient_UnresolvableTargetIsUnreachable(t *testing.T) {
	clearServerEnv(t)
	_, err := newClient(&serverFlags{})
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitUnreachable {
		t.Errorf("ExitCode() = %d, want %d (unreachable)", ec.ExitCode(), exitUnreachable)
	}
}

// --- classify -------------------------------------------------------------

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitOK},
		{"already classified passes through unchanged", newExitCodeError(exitNotFound, errors.New("x")), exitNotFound},
		{"unauthenticated", api.ErrUnauthenticated, exitAuthFailure},
		{"forbidden", api.ErrForbidden, exitAuthFailure},
		{"not found", api.ErrNotFound, exitNotFound},
		{"connection refused", syscall.ECONNREFUSED, exitUnreachable},
		{"deadline exceeded", context.DeadlineExceeded, exitUnreachable},
		{"dns error", &net.DNSError{Err: "no such host", Name: "x.invalid"}, exitUnreachable},
		{"timeout net.Error", fakeTimeoutErr{}, exitUnreachable},
		{"instancefile not found", instancefile.ErrNotFound, exitUnreachable},
		{"instancefile stale", instancefile.ErrStale, exitUnreachable},
		{"instancefile corrupt", instancefile.ErrCorrupt, exitUnreachable},
		{"generic error", errors.New("boom"), exitGeneric},
		{"other api error (conflict)", api.ErrConflict, exitGeneric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("classify(nil) = %v, want nil", got)
				}
				return
			}
			var ec *exitCodeError
			if !errors.As(got, &ec) {
				t.Fatalf("classify(%v) = %T, want *exitCodeError", tc.err, got)
			}
			if ec.ExitCode() != tc.want {
				t.Errorf("classify(%v).ExitCode() = %d, want %d", tc.err, ec.ExitCode(), tc.want)
			}
		})
	}
}

// fakeTimeoutErr is a minimal net.Error whose Timeout() reports true,
// exercising isUnreachable's generic-timeout branch independent of any
// concrete stdlib type.
type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "fake timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

// --- output rendering -----------------------------------------------------

func TestParseOutputFormat(t *testing.T) {
	for _, ok := range []string{"table", "json", "yaml"} {
		if _, err := parseOutputFormat(ok); err != nil {
			t.Errorf("parseOutputFormat(%q) = %v, want nil error", ok, err)
		}
	}

	_, err := parseOutputFormat("xml")
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.ExitCode() != exitUsage {
		t.Fatalf("parseOutputFormat(invalid) = %v, want a usage exitCodeError", err)
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := renderJSON(&buf, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, `"a": "b"`) {
		t.Errorf("renderJSON output = %q, want it to contain the indented field", got)
	}
}

func TestRenderYAML_UsesJSONFieldNames(t *testing.T) {
	type sample struct {
		SessionID string `json:"session_id"`
		Unwanted  string `json:"-"`
	}
	var buf bytes.Buffer
	if err := renderYAML(&buf, sample{SessionID: "abc", Unwanted: "hidden"}); err != nil {
		t.Fatalf("renderYAML: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "session_id: abc") {
		t.Errorf("renderYAML output = %q, want the json tag name session_id", got)
	}
	if strings.Contains(got, "hidden") || strings.Contains(got, "Unwanted") {
		t.Errorf("renderYAML output = %q, leaked a json:\"-\" field", got)
	}
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTable(&buf, []string{"ID", "NAME"}, [][]string{{"1", "foo"}}); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"ID", "NAME", "----", "foo"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderTable output = %q, want it to contain %q", got, want)
		}
	}
}

// TestNormalizeForEmptyCollections_NilSlice pins the fix for the case this
// helper exists for: a concrete-typed nil slice (as returned by a
// zero-result list command) must normalize to []any{}, not pass through as
// a nil interface that json.Marshal would render as `null`.
func TestNormalizeForEmptyCollections_NilSlice(t *testing.T) {
	var nilSlice []string
	got := normalizeForEmptyCollections(nilSlice)
	out, ok := got.([]any)
	if !ok || len(out) != 0 {
		t.Fatalf("normalizeForEmptyCollections(nil []string) = %#v, want []any{}", got)
	}

	var buf bytes.Buffer
	if err := renderJSON(&buf, got); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("renderJSON(normalized nil slice) = %q, want \"[]\"", buf.String())
	}
}

func TestNormalizeForEmptyCollections_NonSlicePassesThrough(t *testing.T) {
	in := map[string]string{"a": "b"}
	got := normalizeForEmptyCollections(in)
	if _, ok := got.(map[string]string); !ok {
		t.Fatalf("normalizeForEmptyCollections(map) = %#v (%T), want it unchanged", got, got)
	}
}

func TestNormalizeForEmptyCollections_NonNilSlicePassesThrough(t *testing.T) {
	in := []string{"a"}
	got := normalizeForEmptyCollections(in)
	out, ok := got.([]string)
	if !ok || len(out) != 1 || out[0] != "a" {
		t.Fatalf("normalizeForEmptyCollections(non-nil slice) = %#v, want it unchanged", got)
	}
}

// --- emit dispatcher --------------------------------------------------

func TestEmit_DispatchesByFormat(t *testing.T) {
	type row struct {
		ID string `json:"id"`
	}
	tableFn := func() ([]string, [][]string) { return []string{"ID"}, [][]string{{"1"}} }

	for _, tc := range []struct {
		format string
		want   string
	}{
		{"table", "ID"},
		{"json", `"id": "1"`},
		{"yaml", "id: \"1\""},
	} {
		t.Run(tc.format, func(t *testing.T) {
			cmd := &cobra.Command{}
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			f := &serverFlags{Output: tc.format}

			if err := emit(cmd, f, row{ID: "1"}, tableFn); err != nil {
				t.Fatalf("emit: %v", err)
			}
			if got := buf.String(); !strings.Contains(got, tc.want) {
				t.Errorf("emit(%s) output = %q, want it to contain %q", tc.format, got, tc.want)
			}
		})
	}
}

func TestEmit_InvalidOutputFormatIsUsageError(t *testing.T) {
	cmd := &cobra.Command{}
	f := &serverFlags{Output: "bogus"}
	err := emit(cmd, f, struct{}{}, func() ([]string, [][]string) { return nil, nil })
	var ec *exitCodeError
	if !errors.As(err, &ec) || ec.ExitCode() != exitUsage {
		t.Fatalf("emit(invalid format) = %v, want a usage exitCodeError", err)
	}
}

func TestEmit_EmptyListRendersEmptyJSONArray(t *testing.T) {
	var rows []struct{ ID string }
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	f := &serverFlags{Output: "json"}

	if err := emit(cmd, f, rows, func() ([]string, [][]string) { return nil, nil }); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("emit(nil list, json) = %q, want \"[]\" (DDR §4)", got)
	}
}
