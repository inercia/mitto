package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/instancefile"
)

// withAuthFlags sets the package-level authFlags var to f for the duration
// of the test, restoring the previous value on cleanup (mirrors
// withSendCmd's save/restore pattern in conversation_send_test.go, needed
// because authStatusCmd/authRotateCmd's RunE read the shared global rather
// than a value passed in).
func withAuthFlags(t *testing.T, f serverFlags) {
	t.Helper()
	old := authFlags
	authFlags = f
	t.Cleanup(func() { authFlags = old })
}

// --- tokenSource ------------------------------------------------------

func TestTokenSource_Flag(t *testing.T) {
	clearServerEnv(t)
	if got := tokenSource(&serverFlags{Token: "flag-token"}); got != "flag" {
		t.Errorf("tokenSource = %q, want %q", got, "flag")
	}
}

func TestTokenSource_Env(t *testing.T) {
	clearServerEnv(t)
	t.Setenv("MITTO_TOKEN", "env-token")
	if got := tokenSource(&serverFlags{}); got != "env" {
		t.Errorf("tokenSource = %q, want %q", got, "env")
	}
}

func TestTokenSource_InstanceFile(t *testing.T) {
	clearServerEnv(t)
	writeInstance(t, &instancefile.Instance{PID: os.Getpid(), URL: "http://inst:2", APIPrefix: "/mitto", Token: "inst-token"})
	if got := tokenSource(&serverFlags{}); got != "instance.json" {
		t.Errorf("tokenSource = %q, want %q", got, "instance.json")
	}
}

func TestTokenSource_None(t *testing.T) {
	clearServerEnv(t)
	if got := tokenSource(&serverFlags{}); got != "none" {
		t.Errorf("tokenSource = %q, want %q", got, "none")
	}
}

func TestTokenSource_FlagBeatsEnvAndInstanceFile(t *testing.T) {
	clearServerEnv(t)
	t.Setenv("MITTO_TOKEN", "env-token")
	writeInstance(t, &instancefile.Instance{PID: os.Getpid(), URL: "http://inst:2", APIPrefix: "/mitto", Token: "inst-token"})
	if got := tokenSource(&serverFlags{Token: "flag-token"}); got != "flag" {
		t.Errorf("tokenSource = %q, want %q (flag precedence)", got, "flag")
	}
}

// --- mitto auth status / rotate ---------------------------------------

// newFakeAuthServer builds an httptest server backing GET /mitto/api/health,
// GET /mitto/api/auth-info, GET /mitto/api/sessions (an authenticated probe)
// and POST /mitto/api/auth/rotate-token, mirroring the real server's
// responses closely enough to exercise runAuthStatus/runAuthRotate
// end-to-end without a real web.Server.
func newFakeAuthServer(t *testing.T, simpleAuth, cloudflare bool, rotateFingerprint string, rotateStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/mitto/api/auth-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"simple":` + boolJSON(simpleAuth) + `,"cloudflare":` + boolJSON(cloudflare) + `}`))
	})
	mux.HandleFunc("/mitto/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/mitto/api/auth/rotate-token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rotateStatus)
		if rotateStatus == http.StatusOK {
			w.Write([]byte(`{"fingerprint":"` + rotateFingerprint + `"}`))
		} else {
			w.Write([]byte(`{"error":{"code":"conflict","message":"the shared token is operator-configured and cannot be rotated via this endpoint"}}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestAuthStatus_TableAndJSONShapes_TokenNeverExposed covers the plan's
// TESTS item: `auth status` renders both table and json output, resolves
// the token's fingerprint/source, and never prints the raw token value
// anywhere in stdout or stderr.
func TestAuthStatus_TableAndJSONShapes_TokenNeverExposed(t *testing.T) {
	clearServerEnv(t)
	const secretToken = "super-secret-do-not-print-xyz"
	srv := newFakeAuthServer(t, true, false, "", 0)

	wantFingerprint := instancefile.Fingerprint(secretToken)

	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			withAuthFlags(t, serverFlags{
				URL:       srv.URL,
				Token:     secretToken,
				APIPrefix: "/mitto",
				Timeout:   5 * time.Second,
				Output:    format,
			})

			cmd := &cobra.Command{}
			var out, errOut strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)

			if err := runAuthStatus(cmd, nil); err != nil {
				t.Fatalf("runAuthStatus: %v", err)
			}

			combined := out.String() + errOut.String()
			if strings.Contains(combined, secretToken) {
				t.Fatalf("output leaked the raw token value: %q", combined)
			}
			if !strings.Contains(out.String(), wantFingerprint) {
				t.Errorf("output = %q, want it to contain the fingerprint %q", out.String(), wantFingerprint)
			}
			if !strings.Contains(out.String(), "flag") {
				t.Errorf("output = %q, want token_source %q", out.String(), "flag")
			}
		})
	}
}

// TestAuthStatus_AuthDisabled_SkipsAuthenticatedProbe verifies that when
// neither simple nor cloudflare auth is reported, `auth status` does not
// call the authenticated ListSessions probe (health/auth-info alone would
// report "reachable" even with a bad token, so that probe only makes sense
// when auth is actually enabled).
func TestAuthStatus_AuthDisabled_SkipsAuthenticatedProbe(t *testing.T) {
	clearServerEnv(t)
	probed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/mitto/api/auth-info", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"simple":false,"cloudflare":false}`))
	})
	mux.HandleFunc("/mitto/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		probed = true
		w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	withAuthFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})
	cmd := &cobra.Command{}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runAuthStatus(cmd, nil); err != nil {
		t.Fatalf("runAuthStatus: %v", err)
	}
	if probed {
		t.Error("expected the authenticated /api/sessions probe to be skipped when auth is not enabled")
	}
	if !strings.Contains(out.String(), `"auth_enabled": false`) {
		t.Errorf("output = %q, want auth_enabled=false", out.String())
	}
}

// TestAuthRotate_Success verifies `auth rotate` prints the new fingerprint,
// the every-other-client-rejected warning, and never the underlying token
// value used to authenticate the rotate request itself.
func TestAuthRotate_Success(t *testing.T) {
	clearServerEnv(t)
	const requestToken = "current-secret-token"
	const newFingerprint = "abcd1234"
	srv := newFakeAuthServer(t, true, false, newFingerprint, http.StatusOK)

	withAuthFlags(t, serverFlags{URL: srv.URL, Token: requestToken, APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})
	cmd := &cobra.Command{}
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := runAuthRotate(cmd, nil); err != nil {
		t.Fatalf("runAuthRotate: %v", err)
	}

	if !strings.Contains(out.String(), newFingerprint) {
		t.Errorf("stdout = %q, want the new fingerprint %q", out.String(), newFingerprint)
	}
	if !strings.Contains(errOut.String(), "rejected") {
		t.Errorf("stderr = %q, want a warning about rejected clients", errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), requestToken) {
		t.Errorf("output leaked the request's bearer token")
	}
}

// TestAuthRotate_RefusedForOperatorToken verifies a 409 from the server
// (operator-configured token, not rotatable) is surfaced as a command error
// rather than a false success, per classify()'s generic-error mapping.
func TestAuthRotate_RefusedForOperatorToken(t *testing.T) {
	clearServerEnv(t)
	srv := newFakeAuthServer(t, true, false, "", http.StatusConflict)

	withAuthFlags(t, serverFlags{URL: srv.URL, Token: "t", APIPrefix: "/mitto", Timeout: 5 * time.Second, Output: "json"})
	cmd := &cobra.Command{}
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err := runAuthRotate(cmd, nil)
	if err == nil {
		t.Fatal("expected an error for a 409 conflict response")
	}
	if !strings.Contains(err.Error(), "operator-configured") {
		t.Errorf("error = %v, want it to surface the server's refusal message", err)
	}
}
