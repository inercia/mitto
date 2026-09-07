package configsvc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configpath"
)

// revisionOf builds an opaque optimistic-concurrency token from a file's
// size and modification time. It is not a cryptographic hash: it is cheap
// to compute and good enough to detect "did settings.json change since I
// last read it", which is all Mutate needs it for.
func revisionOf(size, modNanos int64) string {
	return fmt.Sprintf("%d-%d", size, modNanos)
}

// RedactedPlaceholder replaces any registry-flagged secret value in a
// snapshot read. It is never a real value and mutate.go rejects any
// incoming write whose leaf value equals it, so it can never be persisted
// back as a "real" secret by accident.
const RedactedPlaceholder = "[REDACTED]"

// Provenance classifies where a resolved value came from.
type Provenance string

const (
	// ProvenanceStored: present verbatim in settings.json today.
	ProvenanceStored Provenance = "stored"
	// ProvenanceEffective: not stored; this is a compile-time default that
	// would apply at runtime. Virtual — never seeded back to disk by a read.
	ProvenanceEffective Provenance = "effective"
	// ProvenanceUnset: no stored value and no known default for this path.
	ProvenanceUnset Provenance = "unset"
)

// FieldValue is the result of resolving one path against a snapshot.
type FieldValue struct {
	Path       string
	Provenance Provenance
	// Redacted is true when Value has been replaced by RedactedPlaceholder.
	Redacted bool
	Value    interface{}
}

// Snapshot is a pure, point-in-time view of settings.json plus a purely
// re-derived "effective" layer for the handful of fields this service knows
// a compile-time default for. It never creates, migrates, or mutates
// anything on disk, and never touches secure storage.
type Snapshot struct {
	// Exists is false when settings.json does not exist on disk.
	Exists bool
	// Revision is an opaque conflict token (mtime+size based) identifying
	// the exact on-disk state this snapshot was read from. Mutate rejects a
	// write whose caller-supplied Revision no longer matches (see mutate.go).
	Revision string
	// raw is the unredacted decoded document (nil if !Exists). Never
	// exposed directly — always go through Get/GetWhole so redaction and
	// provenance are applied consistently.
	raw map[string]interface{}
	reg *Registry
}

// ReadSnapshot reads settings.json exactly as it is on disk, with no
// first-run creation, migration, dedup-save, backup, or Keychain access. A
// missing file yields a valid, empty (Exists=false) Snapshot, not an error.
func ReadSnapshot() (*Snapshot, error) {
	path, err := appdir.SettingsPath()
	if err != nil {
		return nil, wrapErr(ErrKindIO, "", "failed to resolve settings path", err)
	}
	return readSnapshotFrom(path)
}

func readSnapshotFrom(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Snapshot{Exists: false, reg: NewRegistry()}, nil
		}
		return nil, wrapErr(ErrKindIO, "", "failed to read settings file", err)
	}
	info, statErr := os.Stat(path)
	rev := ""
	if statErr == nil {
		rev = revisionOf(info.Size(), info.ModTime().UnixNano())
	}
	doc, err := decodeRawDoc(data)
	if err != nil {
		return nil, wrapErr(ErrKindIO, "", "failed to parse settings file", err)
	}
	return &Snapshot{Exists: true, Revision: rev, raw: doc, reg: NewRegistry()}, nil
}

// decodeRawDoc decodes JSON bytes into a map using json.Number for numeric
// leaves so untouched values round-trip with their original precision.
func decodeRawDoc(data []byte) (map[string]interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]interface{}
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// Get resolves one path against the snapshot: stored value if present,
// else a purely-derived effective default if this service knows one for
// that path, else Unset. Secret paths are redacted per the registry,
// including when the path resolves inside a parent-object or the whole
// document (recursive redaction).
func (s *Snapshot) Get(path configpath.Path) (FieldValue, error) {
	if len(path) == 0 {
		return FieldValue{}, newErr(ErrKindStructure, "", "empty path")
	}
	fv := FieldValue{Path: path.String()}

	if s.Exists {
		if v, ok := navigate(s.raw, path); ok {
			out, redacted := redactTree(v, path, s.reg)
			fv.Provenance = ProvenanceStored
			fv.Value = out
			fv.Redacted = redacted
			return fv, nil
		}
	}

	if def, ok := effectiveDefault(path); ok {
		out, redacted := redactTree(def, path, s.reg)
		fv.Provenance = ProvenanceEffective
		fv.Value = out
		fv.Redacted = redacted
		return fv, nil
	}

	fv.Provenance = ProvenanceUnset
	return fv, nil
}

// GetWhole returns the entire stored document, fully (recursively)
// redacted. Returns ok=false if settings.json does not exist.
func (s *Snapshot) GetWhole() (interface{}, bool) {
	if !s.Exists {
		return nil, false
	}
	out, _ := redactTree(s.raw, nil, s.reg)
	return out, true
}

// effectiveDefault returns the purely compile-time-known default for a
// handful of settable-v1 fields, mirroring the values seeded by
// config/config.default.yaml and internal/config's documented defaults.
// It intentionally does NOT reproduce internal/config's RC-merge logic
// (out of scope for v1; acp_servers stays ProvenanceUnset when absent).
func effectiveDefault(path configpath.Path) (interface{}, bool) {
	switch path.String() {
	case "web.port":
		return int64(8080), true
	case "web.external_port":
		return int64(-1), true
	case "mcp.host":
		return "127.0.0.1", true
	case "mcp.port":
		return int64(5757), true
	}
	return nil, false
}
