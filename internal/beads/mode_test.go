package beads

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/workspaces"
)

type fakeRemoteInspector struct {
	hasRemote bool
	err       error
	calls     int
}

func (f *fakeRemoteInspector) HasDoltRemote(context.Context, string) (bool, error) {
	f.calls++
	return f.hasRemote, f.err
}

func setupModeTestDir(t *testing.T) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
}

func TestResolveDatabaseMode_ExplicitPolicyWins(t *testing.T) {
	for _, mode := range []workspaces.BeadsDatabaseMode{
		workspaces.BeadsDatabaseModeLocal,
		workspaces.BeadsDatabaseModeShared,
	} {
		t.Run(string(mode), func(t *testing.T) {
			setupModeTestDir(t)
			if err := workspaces.SetFolderBeadsDatabaseMode("/proj", mode); err != nil {
				t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
			}
			inspector := &fakeRemoteInspector{hasRemote: mode == workspaces.BeadsDatabaseModeLocal}
			got, err := ResolveDatabaseMode(context.Background(), inspector, "/proj")
			if err != nil || got != mode {
				t.Fatalf("ResolveDatabaseMode() = (%q, %v), want (%q, nil)", got, err, mode)
			}
			if inspector.calls != 0 {
				t.Errorf("HasDoltRemote calls = %d, want 0 for explicit policy", inspector.calls)
			}
		})
	}
}

func TestResolveDatabaseMode_InfersAndPersistsLegacyPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hasRemote bool
		want      workspaces.BeadsDatabaseMode
	}{
		{"without remote", false, workspaces.BeadsDatabaseModeLocal},
		{"with remote", true, workspaces.BeadsDatabaseModeShared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupModeTestDir(t)
			inspector := &fakeRemoteInspector{hasRemote: tc.hasRemote}
			got, err := ResolveDatabaseMode(context.Background(), inspector, "/proj")
			if err != nil || got != tc.want {
				t.Fatalf("ResolveDatabaseMode() = (%q, %v), want (%q, nil)", got, err, tc.want)
			}
			persisted, configured, err := workspaces.ConfiguredFolderBeadsDatabaseMode("/proj")
			if err != nil || !configured || persisted != tc.want {
				t.Fatalf("persisted mode = (%q, %v, %v), want (%q, true, nil)", persisted, configured, err, tc.want)
			}
			inspector.hasRemote = !tc.hasRemote
			got, err = ResolveDatabaseMode(context.Background(), inspector, "/proj")
			if err != nil || got != tc.want {
				t.Fatalf("second ResolveDatabaseMode() = (%q, %v), want persisted %q", got, err, tc.want)
			}
			if inspector.calls != 1 {
				t.Errorf("HasDoltRemote calls = %d, want 1 after persisted inference", inspector.calls)
			}
		})
	}
}

func TestResolveDatabaseMode_ProbeFailureDoesNotPersist(t *testing.T) {
	setupModeTestDir(t)
	probeErr := errors.New("probe failed")
	_, err := ResolveDatabaseMode(context.Background(), &fakeRemoteInspector{err: probeErr}, "/proj")
	if !errors.Is(err, probeErr) {
		t.Fatalf("ResolveDatabaseMode() error = %v, want wrapped probe error", err)
	}
	mode, configured, loadErr := workspaces.ConfiguredFolderBeadsDatabaseMode("/proj")
	if loadErr != nil || configured || mode != "" {
		t.Fatalf("mode after failed probe = (%q, %v, %v), want no persisted policy", mode, configured, loadErr)
	}
}

func TestDoltRemoteConfigured_UnsupportedClient(t *testing.T) {
	_, err := DoltRemoteConfigured(context.Background(), struct{}{}, "/proj")
	if !errors.Is(err, ErrRemoteInspectionUnsupported) {
		t.Fatalf("DoltRemoteConfigured() error = %v, want ErrRemoteInspectionUnsupported", err)
	}
}

func TestCLIClientHasDoltRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   bool
	}{
		{"empty", `[]`, false},
		{"configured", `[{"name":"origin","url":"opaque"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := initializedDir(t)
			runner := &recordingRunner{responses: []runnerResp{{stdout: []byte(tc.output)}}}
			got, err := newClient(runner).HasDoltRemote(context.Background(), dir)
			if err != nil || got != tc.want {
				t.Fatalf("HasDoltRemote() = (%v, %v), want (%v, nil)", got, err, tc.want)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("runner calls = %d, want 1", len(runner.calls))
			}
			wantArgs := []string{"--readonly", "dolt", "remote", "list", "--json"}
			if len(runner.calls[0].args) != len(wantArgs) {
				t.Fatalf("args = %v, want %v", runner.calls[0].args, wantArgs)
			}
			for i := range wantArgs {
				if runner.calls[0].args[i] != wantArgs[i] {
					t.Fatalf("args = %v, want %v", runner.calls[0].args, wantArgs)
				}
			}
		})
	}
}

func TestCLIClientHasDoltRemote_UninitializedDoesNotInvokeBD(t *testing.T) {
	runner := &recordingRunner{}
	got, err := newClient(runner).HasDoltRemote(context.Background(), t.TempDir())
	if err != nil || got {
		t.Fatalf("HasDoltRemote(uninitialized) = (%v, %v), want (false, nil)", got, err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner calls = %d, want 0", len(runner.calls))
	}
}

func TestCLIClientHasDoltRemote_RejectsUnexpectedJSONShape(t *testing.T) {
	dir := initializedDir(t)
	runner := &recordingRunner{responses: []runnerResp{{stdout: []byte(`{"origin":{}}`)}}}
	if _, err := newClient(runner).HasDoltRemote(context.Background(), dir); err == nil {
		t.Fatal("HasDoltRemote(object JSON) error = nil, want unexpected-format error")
	}
}
