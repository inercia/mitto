package configsvc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configpath"
)

func setupTempMitto(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, dir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	return dir
}

func mustOpSet(t *testing.T, assignments ...configpath.Assignment) configpath.OpSet {
	t.Helper()
	ops, err := configpath.Resolve(assignments)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return ops
}

func mustAssignJSON(t *testing.T, path, jsonVal string) configpath.Assignment {
	t.Helper()
	set, err := configpath.ParseSetJSON(path + "=" + jsonVal)
	if err != nil {
		t.Fatalf("ParseSetJSON(%q=%q): %v", path, jsonVal, err)
	}
	if len(set) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(set))
	}
	return set[0]
}

func TestMutate_DryRun_NoFilesystemChanges(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{"web":{"port":8080}}`)
	settingsPath := filepath.Join(dir, "settings.json")
	before, _ := os.Stat(settingsPath)

	ops := mustOpSet(t, mustAssignJSON(t, "web.port", "9090"))
	res, err := Mutate(MutateRequest{Set: ops, DryRun: true})
	if err != nil {
		t.Fatalf("Mutate dry-run: %v", err)
	}
	if len(res.Applied) != 1 {
		t.Fatalf("Applied = %v", res.Applied)
	}

	after, _ := os.Stat(settingsPath)
	if before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		t.Fatalf("dry-run modified settings.json (before=%v/%d after=%v/%d)",
			before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}
}

func TestMutate_AtomicBatch_OneBadKeyRejectsAll(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{"web":{"port":8080},"custom_unrelated":"keep-me"}`)
	settingsPath := filepath.Join(dir, "settings.json")
	before, _ := os.ReadFile(settingsPath)

	ops := mustOpSet(t,
		mustAssignJSON(t, "web.port", "9090"),
		mustAssignJSON(t, "web.auth.shared_token", `"smuggled"`),
	)
	_, err := Mutate(MutateRequest{Set: ops})
	if err == nil {
		t.Fatalf("expected error for batch containing a rejected field")
	}
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindRejected {
		t.Fatalf("expected ErrKindRejected, got %v", err)
	}

	after, _ := os.ReadFile(settingsPath)
	if string(before) != string(after) {
		t.Fatalf("partial write occurred despite validation failure:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestMutate_PreservesUnknownFieldsAndSecrets(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{
		"web": {"port": 8080, "auth": {"simple": {"username": "admin", "password": "hunter2"}}},
		"some_future_field": {"nested": [1, 2, 3]}
	}`)

	ops := mustOpSet(t, mustAssignJSON(t, "web.port", "9090"))
	if _, err := Mutate(MutateRequest{Set: ops}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	whole, _ := snap.GetWhole()
	doc := whole.(map[string]interface{})
	future, ok := doc["some_future_field"].(map[string]interface{})
	if !ok {
		t.Fatalf("some_future_field lost: %#v", doc)
	}
	nested, ok := future["nested"].([]interface{})
	if !ok || len(nested) != 3 {
		t.Fatalf("nested array not preserved in order: %#v", future["nested"])
	}

	fv, err := snap.Get(mustPath(t, "web.auth.simple.password"))
	if err != nil {
		t.Fatalf("Get password: %v", err)
	}
	if !fv.Redacted {
		t.Fatalf("password should still be present (redacted) after unrelated write")
	}
}

func TestMutate_RestrictiveFileMode(t *testing.T) {
	dir := setupTempMitto(t)
	ops := mustOpSet(t, mustAssignJSON(t, "web.port", "9090"))
	if _, err := Mutate(MutateRequest{Set: ops}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("stat settings.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestMutate_UnknownField_FailsClearly(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{"web":{"port":8080}}`)
	settingsPath := filepath.Join(dir, "settings.json")
	before, _ := os.ReadFile(settingsPath)

	ops := mustOpSet(t, mustAssignJSON(t, "totally_unknown_field", `"x"`))
	_, err := Mutate(MutateRequest{Set: ops})
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindUnknownField {
		t.Fatalf("expected ErrKindUnknownField, got %v", err)
	}
	after, _ := os.ReadFile(settingsPath)
	if string(before) != string(after) {
		t.Fatalf("unknown-field write was partially applied")
	}
}

func TestMutate_ReadOnlyEffectiveField_Rejected(t *testing.T) {
	setupTempMitto(t)
	ops := mustOpSet(t, mustAssignJSON(t, "acp_servers", "[]"))
	_, err := Mutate(MutateRequest{Set: ops})
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindReadOnly {
		t.Fatalf("expected ErrKindReadOnly, got %v", err)
	}
}

func TestMutate_IdempotentNoOpWrite(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{"web":{"port":9090}}`)
	settingsPath := filepath.Join(dir, "settings.json")

	ops := mustOpSet(t, mustAssignJSON(t, "web.port", "9090"))
	if _, err := Mutate(MutateRequest{Set: ops}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	snap, err := readSnapshotFrom(settingsPath)
	if err != nil {
		t.Fatalf("readSnapshotFrom: %v", err)
	}
	fv, _ := snap.Get(mustPath(t, "web.port"))
	if n, ok := asInt(fv.Value); !ok || n != 9090 {
		t.Fatalf("web.port = %v (%T), want 9090", fv.Value, fv.Value)
	}
	if len(data) == 0 {
		t.Fatalf("settings.json empty after idempotent write")
	}
}

func TestMutate_TaskLabelColorsAndShortcuts_OrderPreserved(t *testing.T) {
	setupTempMitto(t)

	ops := mustOpSet(t,
		mustAssignJSON(t, "task_label_colors", `[{"label":"needs-human","color":"#ef4444"},{"label":"blocked","color":"#f59e0b"}]`),
		mustAssignJSON(t, "shortcuts", `{"tasksList":[{"icon":"","prompt":"Overview"}]}`),
	)
	if _, err := Mutate(MutateRequest{Set: ops}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	fv, err := snap.Get(mustPath(t, "task_label_colors"))
	if err != nil {
		t.Fatalf("Get task_label_colors: %v", err)
	}
	list, ok := fv.Value.([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("task_label_colors = %#v", fv.Value)
	}
	first := list[0].(map[string]interface{})
	if first["label"] != "needs-human" {
		t.Fatalf("order not preserved: first = %v", first)
	}
}

func TestMutate_RejectsUnknownNestedKey_ParentObjectReplacement(t *testing.T) {
	setupTempMitto(t)
	// "foo" is not a field of config.TaskLabelColor: DisallowUnknownFields
	// must catch it even though task_label_colors itself is settable.
	ops := mustOpSet(t, mustAssignJSON(t, "task_label_colors", `[{"label":"x","color":"#ffffff","foo":"bar"}]`))
	_, err := Mutate(MutateRequest{Set: ops})
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindValidation {
		t.Fatalf("expected ErrKindValidation for unknown nested key, got %v", err)
	}
}

func TestMutate_FailedWrite_LeavesPriorSettingsUsable(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{"web":{"port":8080}}`)

	ops := mustOpSet(t, mustAssignJSON(t, "web.port", "999999"))
	if _, err := Mutate(MutateRequest{Set: ops}); err == nil {
		t.Fatalf("expected validation error for out-of-range port")
	}

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot after failed write: %v", err)
	}
	if !snap.Exists {
		t.Fatalf("settings.json missing after failed write")
	}
	fv, _ := snap.Get(mustPath(t, "web.port"))
	if n, ok := asInt(fv.Value); !ok || n != 8080 {
		t.Fatalf("prior web.port lost after failed write: %v", fv.Value)
	}
}

func TestMutate_RevisionMismatch_Rejected(t *testing.T) {
	dir := setupTempMitto(t)
	writeRawSettings(t, dir, `{"web":{"port":8080}}`)

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}

	// Simulate a concurrent writer changing the file after the snapshot was
	// taken but before this caller's Mutate call.
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"web":{"port":8080},"extra":"changed"}`), 0o644); err != nil {
		t.Fatalf("simulate concurrent write: %v", err)
	}
	fi, statErr := os.Stat(settingsPath)
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}
	if revisionOf(fi.Size(), fi.ModTime().UnixNano()) == snap.Revision {
		t.Skip("filesystem revision granularity too coarse to distinguish writes in this test run")
	}

	ops := mustOpSet(t, mustAssignJSON(t, "web.port", "9090"))
	_, err = Mutate(MutateRequest{Set: ops, Revision: snap.Revision})
	cerr, ok := err.(*Error)
	if !ok || cerr.Kind != ErrKindRevisionMismatch {
		t.Fatalf("expected ErrKindRevisionMismatch, got %v", err)
	}
}
