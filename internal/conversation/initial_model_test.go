package conversation

import (
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestInitialModelSelection_AllPersistedEntryPoints(t *testing.T) {
	for _, tc := range []struct {
		name, workspaceTag, serverTag, promptTag, explicitTag, saved, want string
	}{
		{name: "MCP plain text uses workspace", workspaceTag: "bh-fast", want: "m-gamma"},
		{name: "queue resume uses workspace", workspaceTag: "bh-fast", want: "m-gamma"},
		{name: "server default when workspace unset", serverTag: "bh-deep", want: "m-alpha"},
		{name: "workspace beats server", workspaceTag: "bh-fast", serverTag: "bh-deep", want: "m-gamma"},
		{name: "named prompt beats workspace", workspaceTag: "bh-fast", promptTag: "bh-deep", want: "m-alpha"},
		{name: "unmatched prompt falls back", workspaceTag: "bh-fast", promptTag: "missing", want: "m-gamma"},
		{name: "auto child explicit preference", workspaceTag: "bh-fast", explicitTag: "bh-deep", want: "m-alpha"},
		{name: "saved model beats new defaults", workspaceTag: "bh-fast", explicitTag: "bh-deep", saved: "m-beta", want: "m-beta"},
		{name: "no preferences retains agent default", want: "m-beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := session.NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			const id = "20260101-000000-initial"
			if err := store.Create(session.Metadata{SessionID: id, BaselineModel: tc.saved, OriginPromptName: "review"}); err != nil {
				t.Fatal(err)
			}
			sm := NewSessionManager("", "test-server", false, nil)
			sm.SetStore(store)
			sm.mittoConfig = &config.Config{ACPServers: []config.ACPServer{{Name: "test-server", InitialModelTag: tc.serverTag}}}
			sm.SetPreferredModelsResolver(func(string, string) []config.PromptPreferredModel {
				if tc.promptTag == "" {
					return nil
				}
				return []config.PromptPreferredModel{{ModelTag: tc.promptTag}}
			})
			var explicit []config.PromptPreferredModel
			if tc.explicitTag != "" {
				explicit = []config.PromptPreferredModel{{ModelTag: tc.explicitTag}}
			}
			ws := &config.WorkspaceSettings{ACPServer: "test-server", InitialModelTag: tc.workspaceTag}
			prefs, err := sm.prepareInitialModelPreference(id, "/work", ws, "test-server", explicit)
			if err != nil {
				t.Fatal(err)
			}
			bs, _, _ := seedBaseline(t, prefs, "m-beta", tc.saved)
			if got := bs.GetBaselineModel(); got != tc.want {
				t.Fatalf("baseline = %q, want %q", got, tc.want)
			}
			// A generic retry must retain the requested preference, even if callers
			// omit it and workspace defaults have changed since the failed start.
			if len(prefs) > 0 {
				store.Close()
				store, err = session.NewStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				sm.SetStore(store)
				ws.InitialModelTag = "changed"
				retry, err := sm.prepareInitialModelPreference(id, "/work", ws, "test-server", nil)
				if err != nil || !reflect.DeepEqual(retry, prefs) {
					t.Fatalf("retry = %+v, %v; want %+v", retry, err, prefs)
				}
			}
		})
	}
}

func TestInitialModelSelection_PendingPreferenceConsumedByBaseline(t *testing.T) {
	prefs := []config.PromptPreferredModel{{ModelTag: "bh-fast"}}
	bs, store, id := seedBaseline(t, prefs, "m-beta", "")
	if err := store.UpdateMetadata(id, func(m *session.Metadata) { m.PendingInitialModelPreference = prefs }); err != nil {
		t.Fatal(err)
	}
	bs.cmPersistBaselineModel(bs.GetBaselineModel())
	meta, err := store.GetMetadata(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.BaselineModel != "m-gamma" || len(meta.PendingInitialModelPreference) != 0 {
		t.Fatalf("baseline=%q, pending=%+v", meta.BaselineModel, meta.PendingInitialModelPreference)
	}
}
