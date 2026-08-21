// Package instancefile reads and writes $MITTO_DIR/instance.json, the
// running-instance discovery file written by `mitto web` and the macOS app
// after their HTTP listener(s) bind (mitto-pscc.2). It lets local clients
// (e.g. the CLI, see mitto-pscc epic) find a running server and its bearer
// token without extra configuration.
//
// The file is a secret: it carries a bearer token, so callers must never log
// or print an Instance's Token field. Staleness is determined purely by PID
// liveness (never used to signal or kill the process).
package instancefile

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// Version is the current instance.json schema version written by this
// package. Read rejects files whose Version is greater than this, leaving
// room for a future format change without guessing at unknown fields.
const Version = 1

// filePerm is the permission mode for instance.json: owner read/write only,
// since the file carries a bearer token.
const filePerm = 0600

// Instance is the persisted shape of instance.json.
type Instance struct {
	// Version is the schema version of this record.
	Version int `json:"version"`
	// PID is the process ID of the server that wrote this record. Used only
	// for staleness detection — never to signal or kill the process.
	PID int `json:"pid"`
	// URL is the local base URL of the server (e.g. "http://127.0.0.1:8080").
	URL string `json:"url"`
	// APIPrefix is the URL path prefix for API endpoints (e.g. "/mitto").
	APIPrefix string `json:"api_prefix"`
	// ExternalURL is the base URL of the external (0.0.0.0) listener, if any.
	ExternalURL string `json:"external_url,omitempty"`
	// Token is the bearer token accepted by the shared-token auth middleware.
	// NEVER log or print this value.
	Token string `json:"token"`
	// StartedAt is when the server (re)wrote this record.
	StartedAt time.Time `json:"started_at"`
}

// Sentinel errors returned by Read/ReadFrom. Callers (e.g. the CLI) map
// these to distinct exit codes / messages instead of a raw connection-refused.
var (
	// ErrNotFound means no instance.json exists.
	ErrNotFound = errors.New("instance file not found")
	// ErrStale means the file parsed but its recorded PID is no longer
	// running. The parsed *Instance is still returned alongside this error
	// so callers can report the recorded url/pid.
	ErrStale = errors.New("instance file is stale (recorded process is not running)")
	// ErrCorrupt means the file could not be parsed, has an unsupported
	// version, or is missing required fields.
	ErrCorrupt = errors.New("instance file is corrupt")
)

// Path returns the full path to instance.json.
func Path() (string, error) {
	return appdir.InstancePath()
}

// Read loads and validates instance.json at the default path (appdir.InstancePath()).
// See ReadFrom for the exact semantics.
func Read() (*Instance, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return ReadFrom(path)
}

// ReadFrom loads and validates the instance file at path.
//
// Returns ErrNotFound if the file does not exist, ErrCorrupt if it cannot be
// parsed or fails validation, or (inst, ErrStale) if it parses cleanly but
// its recorded PID is no longer running — the parsed record is still
// returned in that case so callers can report the recorded url/pid instead
// of a raw connection-refused.
func ReadFrom(path string) (*Instance, error) {
	var inst Instance
	if err := fileutil.ReadJSON(path, &inst); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}

	if err := inst.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}

	if inst.IsStale() {
		return &inst, ErrStale
	}

	return &inst, nil
}

// Validate checks that inst has a supported version and all required fields.
func (i *Instance) Validate() error {
	if i.Version <= 0 || i.Version > Version {
		return fmt.Errorf("unsupported instance file version %d", i.Version)
	}
	if i.URL == "" {
		return errors.New("missing url")
	}
	if i.Token == "" {
		return errors.New("missing token")
	}
	return nil
}

// IsStale reports whether the process recorded in inst.PID is no longer
// running. It never signals or kills the process — it only probes liveness.
func (i *Instance) IsStale() bool {
	return !isPIDRunning(i.PID)
}

// isPIDRunning checks if a process with the given PID is still running.
// Mirrors internal/session/cleanup.go's isPIDRunning (kept as a separate,
// unexported copy here so instancefile does not import internal/session).
func isPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so send signal 0 to probe liveness.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// Write persists inst to the default path (appdir.InstancePath()), reusing
// the existing token when a valid prior record is present. See WriteTo.
func Write(inst *Instance) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return WriteTo(path, inst)
}

// WriteTo persists inst to path atomically with 0600 permissions.
//
// If inst.Token is empty, WriteTo first attempts a best-effort read of any
// existing record at path: if it parses (even if stale), its token is
// reused; otherwise a fresh token is generated. inst.Version and
// inst.StartedAt are always set/overwritten by this call.
func WriteTo(path string, inst *Instance) error {
	if inst.Token == "" {
		if existing, err := ReadFrom(path); existing != nil && (err == nil || errors.Is(err, ErrStale)) {
			inst.Token = existing.Token
		}
		if inst.Token == "" {
			tok, err := GenerateToken()
			if err != nil {
				return fmt.Errorf("failed to generate instance token: %w", err)
			}
			inst.Token = tok
		}
	}

	inst.Version = Version
	inst.StartedAt = time.Now()

	if err := fileutil.WriteJSONAtomic(path, inst, filePerm); err != nil {
		return fmt.Errorf("failed to write instance file: %w", err)
	}
	// Belt-and-braces: enforce 0600 regardless of umask, since the file
	// carries a bearer token.
	if err := os.Chmod(path, filePerm); err != nil {
		return fmt.Errorf("failed to set instance file permissions: %w", err)
	}
	return nil
}

// GenerateToken returns a new random bearer token: 32 bytes from
// crypto/rand, base64url-encoded (no padding).
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ResolveToken returns the token that Write would persist for the default
// instance-file path right now: the token from a prior (even stale) record
// if one exists, otherwise a freshly generated one (mitto-pscc.9). It lets a
// caller that needs to seed another consumer (e.g. the shared-token auth
// manager) with the SAME value Write will later store — pass the result
// back via Instance.Token so WriteTo's own reuse-or-generate branch is
// skipped and both stay in sync from a single resolution.
func ResolveToken() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if existing, rerr := ReadFrom(path); existing != nil && existing.Token != "" && (rerr == nil || errors.Is(rerr, ErrStale)) {
		return existing.Token, nil
	}
	return GenerateToken()
}

// Fingerprint returns a short, non-secret identifier for a bearer token: the
// first 8 hex characters of its SHA-256 hash. Lets operators/tools confirm
// two token values match (e.g. before/after rotation) without ever printing,
// logging, or transmitting the token itself.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}

// Remove deletes instance.json at the default path. See RemoveFrom.
func Remove() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return RemoveFrom(path)
}

// RemoveFrom deletes the instance file at path.
//
// It is a no-op (returns nil) if the file does not exist. If the file exists
// and its recorded PID is a different, still-running process, RemoveFrom
// leaves the file untouched (a newer instance's record must not be deleted
// by an older one shutting down) and returns nil.
func RemoveFrom(path string) error {
	existing, err := ReadFrom(path)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil && !errors.Is(err, ErrStale):
		// Corrupt file: still attempt removal, there is nothing else to protect.
	case err == nil && existing.PID != os.Getpid():
		// A different, live process owns this record — leave it alone.
		return nil
	}

	if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
		return fmt.Errorf("failed to remove instance file: %w", rmErr)
	}
	return nil
}
