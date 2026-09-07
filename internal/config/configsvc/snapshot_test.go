package configsvc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config/configpath"
)

func mustPath(t *testing.T, s string) configpath.Path {
	t.Helper()
	p, err := configpath.ParsePath(s)
	if err != nil {
		t.Fatalf("ParsePath(%q): %v", s, err)
	}
	return p
}

func TestReadSnapshot_MissingFile_NoSideEffects(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, dir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(dir, "settings.json")

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snap.Exists {
		t.Fatalf("Exists = true for missing file")
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("ReadSnapshot created settings.json (pure read must not create it)")
	}
	if _, err := os.Stat(dir); err == nil {
		// dir itself was created by t.TempDir, that's fine; just make sure
		// no settings.json / .bak / sessions subdir appeared.
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatalf("ReadSnapshot left files in a fresh dir: %v", entries)
		}
	}
}

func writeRawSettings(t *testing.T, dir string, jsonBody string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(jsonBody), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

func TestSnapshot_StoredVsEffectiveProvenance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, dir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	writeRawSettings(t, dir, `{"web":{"port":9999}}`)

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}

	fv, err := snap.Get(mustPath(t, "web.port"))
	if err != nil {
		t.Fatalf("Get(web.port): %v", err)
	}
	if fv.Provenance != ProvenanceStored {
		t.Fatalf("web.port provenance = %v, want stored", fv.Provenance)
	}

	fv, err = snap.Get(mustPath(t, "web.external_port"))
	if err != nil {
		t.Fatalf("Get(web.external_port): %v", err)
	}
	if fv.Provenance != ProvenanceEffective {
		t.Fatalf("web.external_port provenance = %v, want effective (default)", fv.Provenance)
	}
	if fv.Value != int64(-1) {
		t.Fatalf("web.external_port default = %v, want -1", fv.Value)
	}

	fv, err = snap.Get(mustPath(t, "shortcuts"))
	if err != nil {
		t.Fatalf("Get(shortcuts): %v", err)
	}
	if fv.Provenance != ProvenanceUnset {
		t.Fatalf("shortcuts provenance = %v, want unset", fv.Provenance)
	}
}

func TestSnapshot_SecretRedaction_LeafParentAndWhole(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, dir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	writeRawSettings(t, dir, `{
		"web": {"port": 8080, "auth": {"simple": {"username": "admin", "password": "hunter2"}, "shared_token": "tok"}},
		"mcp": {"host": "127.0.0.1", "port": 5757}
	}`)

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}

	// Leaf read.
	fv, err := snap.Get(mustPath(t, "web.auth.simple.password"))
	if err != nil {
		t.Fatalf("Get password: %v", err)
	}
	if !fv.Redacted || fv.Value != RedactedPlaceholder {
		t.Fatalf("password leaf not redacted: %+v", fv)
	}

	// Parent-object read.
	fv, err = snap.Get(mustPath(t, "web.auth.simple"))
	if err != nil {
		t.Fatalf("Get simple: %v", err)
	}
	obj, ok := fv.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("web.auth.simple value not an object: %#v", fv.Value)
	}
	if obj["password"] != RedactedPlaceholder {
		t.Fatalf("nested password not redacted in parent-object read: %+v", obj)
	}
	if obj["username"] != "admin" {
		t.Fatalf("non-secret sibling field lost: %+v", obj)
	}

	// Whole-document read.
	whole, ok := snap.GetWhole()
	if !ok {
		t.Fatalf("GetWhole: !ok")
	}
	data, _ := whole.(map[string]interface{})
	web, _ := data["web"].(map[string]interface{})
	auth, _ := web["auth"].(map[string]interface{})
	if auth["shared_token"] != RedactedPlaceholder {
		t.Fatalf("shared_token not redacted in whole-doc read: %+v", auth)
	}
	if data["mcp"] != RedactedPlaceholder {
		t.Fatalf("mcp subtree not fully redacted in whole-doc read: %#v", data["mcp"])
	}
}
