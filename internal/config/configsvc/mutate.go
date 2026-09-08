package configsvc

import (
	"fmt"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configpath"
	"github.com/inercia/mitto/internal/fileutil"
)

// settingsFileMode is the permission mode this service writes settings.json,
// its temp file, and its backup with — intentionally more restrictive than
// config.SaveSettings's 0644 (mitto-4rz.2 requirement).
const settingsFileMode = 0o600

// MutateRequest is one transactional write: apply every Op in Set as a
// single all-or-nothing batch. Revision, when non-empty, must match the
// Snapshot.Revision the caller last read (optimistic concurrency); a
// mismatch fails the whole request with ErrKindRevisionMismatch instead of
// silently clobbering a concurrent writer. DryRun validates the batch
// without touching disk at all.
type MutateRequest struct {
	Set      configpath.OpSet
	Revision string
	DryRun   bool
	// InProcess marks this call as coming from WITHIN the same process that
	// owns the currently-running Mitto server (e.g. a live web handler), so
	// Mutate must not refuse merely because a running server was detected —
	// that refusal exists to stop a separate, OFFLINE process from racing a
	// live server's settings.json, not to stop the live server itself from
	// mutating its own settings. The advisory flock (see lock.go) is still
	// acquired either way, so concurrent callers — offline or in-process —
	// remain serialized against each other.
	InProcess bool
}

// AppliedField pairs one applied op's literal path (as supplied by the
// caller, e.g. "task_label_colors[0].color") with the canonical registry
// field it belongs to (Field, e.g. "task_label_colors" — the FieldEntry.Path
// of whichever exact or dynamic-root entry matched) and that field's
// declared Liveness. A live caller (e.g. the web API) uses Field to decide
// WHAT to re-read/apply in memory, independent of how deep the caller's op
// path reached into a dynamic field's subtree.
type AppliedField struct {
	Path     string
	Field    string
	Liveness Liveness
}

// MutateResult reports what would be (DryRun) or was written.
type MutateResult struct {
	// Applied lists the canonical paths that were validated (and, unless
	// DryRun, persisted).
	Applied []string
	// AppliedFields mirrors Applied with each op's registry Field and
	// Liveness attached (mitto-4rz.3), so a live caller can classify
	// application without re-consulting the registry itself.
	AppliedFields []AppliedField
}

// Mutate validates every op in req.Set against the registry and, unless
// req.DryRun, persists the result atomically at 0600. Validation runs for
// the WHOLE batch before anything is written: a single invalid op fails
// the entire request and settings.json is left completely untouched.
//
// Mutate reads settings.json itself (raw, unredacted) rather than
// accepting a caller-supplied Snapshot, so validators always see real
// values — never a redacted GET result.
func Mutate(req MutateRequest) (MutateResult, error) {
	reg := NewRegistry()

	path, err := appdir.SettingsPath()
	if err != nil {
		return MutateResult{}, wrapErr(ErrKindIO, "", "failed to resolve settings path", err)
	}

	var unlock func()
	if req.InProcess {
		unlock, err = acquireInProcessLock()
	} else {
		unlock, err = acquireOfflineLock()
	}
	if err != nil {
		return MutateResult{}, err
	}
	defer unlock()

	snap, err := readSnapshotFrom(path)
	if err != nil {
		return MutateResult{}, err
	}

	if req.Revision != "" && snap.Exists && req.Revision != snap.Revision {
		return MutateResult{}, newErr(ErrKindRevisionMismatch, "", "settings changed since snapshot was read")
	}

	doc := map[string]interface{}{}
	if snap.Exists {
		doc = snap.raw
	}

	applied := make([]string, 0, len(req.Set.Ops))
	appliedFields := make([]AppliedField, 0, len(req.Set.Ops))
	for _, op := range req.Set.Ops {
		if err := validateOp(reg, doc, op); err != nil {
			return MutateResult{}, err
		}
		applied = append(applied, op.Path.String())
		if entry, ok := reg.Lookup(op.Path); ok {
			appliedFields = append(appliedFields, AppliedField{
				Path:     op.Path.String(),
				Field:    entry.Path,
				Liveness: entry.Liveness,
			})
		}
	}

	// All ops validated; apply them together to a fresh copy so a later
	// failure (there shouldn't be one, having just validated) can never
	// leave doc partially mutated.
	var newDocAny interface{} = doc
	for _, op := range req.Set.Ops {
		val, err := valueToJSON(op.Value)
		if err != nil {
			return MutateResult{}, wrapErr(ErrKindValidation, op.Path.String(), "invalid value", err)
		}
		newDocAny, err = setAt(newDocAny, op.Path, val)
		if err != nil {
			return MutateResult{}, wrapErr(ErrKindStructure, op.Path.String(), "failed to apply operation", err)
		}
	}
	// Every op's path is rooted at a top-level object key (settings.json's
	// root is always an object), so the result is always still an object.
	newDoc, ok := newDocAny.(map[string]interface{})
	if !ok {
		return MutateResult{}, newErr(ErrKindStructure, "", "settings document root is not an object")
	}

	if req.DryRun {
		return MutateResult{Applied: applied, AppliedFields: appliedFields}, nil
	}

	if err := fileutil.WriteJSONAtomic(path, newDoc, settingsFileMode); err != nil {
		return MutateResult{}, wrapErr(ErrKindIO, "", "failed to write settings file", err)
	}
	return MutateResult{Applied: applied, AppliedFields: appliedFields}, nil
}

// validateOp checks one op against the registry and, for settable-v1
// fields, runs the field's validator against the value that WOULD result
// from applying the op — without mutating doc.
func validateOp(reg *Registry, doc map[string]interface{}, op configpath.Op) error {
	entry, ok := reg.Lookup(op.Path)
	if !ok {
		return newErr(ErrKindUnknownField, op.Path.String(), "unknown or unsupported settings field")
	}
	switch entry.Mutability {
	case MutRejected:
		return newErr(ErrKindRejected, op.Path.String(), "field cannot be modified through this service")
	case MutReadOnlyEffective:
		return newErr(ErrKindReadOnly, op.Path.String(), "field is read-only (effective/RC-owned), not settable")
	case MutSettableV1:
		// fallthrough to validation below
	default:
		return newErr(ErrKindUnknownField, op.Path.String(), "unknown or unsupported settings field")
	}

	val, err := valueToJSON(op.Value)
	if err != nil {
		return wrapErr(ErrKindValidation, op.Path.String(), "invalid value", err)
	}
	if containsRedactedPlaceholder(val) {
		return newErr(ErrKindValidation, op.Path.String(), "value must not contain a redaction placeholder")
	}

	if entry.Validate == nil {
		return nil
	}

	// Dynamic (map/list/object-shaped) fields validate the candidate WHOLE
	// root subtree, so a struct-typed validator (DisallowUnknownFields) can
	// catch unknown nested keys smuggled in via a parent-object
	// replacement. Exact leaf fields (e.g. "web.port") validate just the
	// new leaf value: Lookup only exact-matches when op.Path fully equals
	// entry.Path, so no navigation is needed.
	candidate := val
	if entry.Dynamic {
		var current interface{}
		if v, present := doc[op.Path[0].Key]; present {
			current = v
		}
		var err error
		candidate, err = setAt(current, op.Path[1:], val)
		if err != nil {
			return wrapErr(ErrKindStructure, op.Path.String(), "operation does not fit the existing document shape", err)
		}
	}
	if err := entry.Validate(candidate); err != nil {
		return wrapErr(ErrKindValidation, entry.Path, "value failed validation", err)
	}
	return nil
}

// containsRedactedPlaceholder reports whether v (recursively) contains the
// literal redaction marker string anywhere, guarding against a caller
// round-tripping a redacted GET result back into a write.
func containsRedactedPlaceholder(v interface{}) bool {
	switch t := v.(type) {
	case string:
		return t == RedactedPlaceholder
	case map[string]interface{}:
		for _, vv := range t {
			if containsRedactedPlaceholder(vv) {
				return true
			}
		}
	case []interface{}:
		for _, vv := range t {
			if containsRedactedPlaceholder(vv) {
				return true
			}
		}
	}
	return false
}

// valueToJSON converts a resolved configpath.Value into a plain
// JSON-shaped Go value (nil/bool/int64/float64/string/[]interface{}).
func valueToJSON(v configpath.Value) (interface{}, error) {
	switch v.Kind {
	case configpath.KindNull:
		return nil, nil
	case configpath.KindBool:
		return v.Bool, nil
	case configpath.KindInt:
		return v.Int, nil
	case configpath.KindFloat:
		return v.Float, nil
	case configpath.KindString:
		return v.Str, nil
	case configpath.KindList:
		out := make([]interface{}, len(v.List))
		for i, el := range v.List {
			cv, err := valueToJSON(el)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	case configpath.KindJSON:
		return v.JSON, nil
	default:
		return nil, fmt.Errorf("unknown value kind %d", v.Kind)
	}
}
