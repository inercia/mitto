package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/instancefile"
)

// setupInstanceAuthTempMitto isolates MITTO_DIR for one test and writes a
// valid instance.json recording this test process's own PID (mirrors
// writeRunningInstance in internal/config/configsvc/mutate_test.go). Returns
// the token that validateInstanceBearer must accept.
func setupInstanceAuthTempMitto(t *testing.T) string {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	inst := &instancefile.Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}
	if err := instancefile.Write(inst); err != nil {
		t.Fatalf("instancefile.Write: %v", err)
	}
	got, err := instancefile.Read()
	if err != nil {
		t.Fatalf("instancefile.Read: %v", err)
	}
	return got.Token
}

func TestValidateInstanceBearer_AcceptsMatchingToken(t *testing.T) {
	token := setupInstanceAuthTempMitto(t)

	r := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	if !validateInstanceBearer(r) {
		t.Fatal("validateInstanceBearer = false, want true for the current instance token")
	}
}

func TestValidateInstanceBearer_RejectsWrongToken(t *testing.T) {
	setupInstanceAuthTempMitto(t)

	r := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	r.Header.Set("Authorization", "Bearer not-the-real-token")
	if validateInstanceBearer(r) {
		t.Fatal("validateInstanceBearer = true, want false for a wrong token")
	}
}

func TestValidateInstanceBearer_RejectsMissingHeader(t *testing.T) {
	setupInstanceAuthTempMitto(t)

	r := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	if validateInstanceBearer(r) {
		t.Fatal("validateInstanceBearer = true, want false with no Authorization header")
	}
}

// TestValidateInstanceBearer_RejectsCookieOnly pins the "no cookie/session
// fallback" requirement (mitto-4rz.3 plan §Layer 2): a session cookie must
// never substitute for the instance bearer, however it authenticates
// elsewhere in the app.
func TestValidateInstanceBearer_RejectsCookieOnly(t *testing.T) {
	token := setupInstanceAuthTempMitto(t)

	r := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	r.AddCookie(&http.Cookie{Name: "mitto_session", Value: token})
	if validateInstanceBearer(r) {
		t.Fatal("validateInstanceBearer = true, want false: a cookie must never substitute for the bearer header")
	}
}

func TestValidateInstanceBearer_RejectsWrongScheme(t *testing.T) {
	token := setupInstanceAuthTempMitto(t)

	r := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	r.Header.Set("Authorization", "Basic "+token)
	if validateInstanceBearer(r) {
		t.Fatal("validateInstanceBearer = true, want false for a non-Bearer scheme")
	}
}

// TestValidateInstanceBearer_NoInstanceFile_FailsClosed covers the case
// where instance.json does not exist at all (e.g. before the server has
// finished starting up): any token must be rejected, never accepted.
func TestValidateInstanceBearer_NoInstanceFile_FailsClosed(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	r := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	r.Header.Set("Authorization", "Bearer anything")
	if validateInstanceBearer(r) {
		t.Fatal("validateInstanceBearer = true, want false when instance.json does not exist")
	}
}

func TestExtractInstanceBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"standard", "Bearer abc123", "abc123"},
		{"case-insensitive scheme", "bearer abc123", "abc123"},
		{"extra whitespace trimmed", "Bearer   abc123  ", "abc123"},
		{"missing", "", ""},
		{"wrong scheme", "Basic abc123", ""},
		{"scheme with no token", "Bearer ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			if got := extractInstanceBearerToken(r); got != tt.want {
				t.Errorf("extractInstanceBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}
