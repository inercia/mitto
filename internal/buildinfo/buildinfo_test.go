package buildinfo

import (
	"errors"
	"runtime/debug"
	"testing"
)

func TestReadBuildIdentity(t *testing.T) {
	got := read(
		func() (string, error) { return "/Applications/Mitto.app/Contents/MacOS/mitto-app", nil },
		func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123"},
				{Key: "vcs.time", Value: "2026-08-14T07:00:00Z"},
				{Key: "vcs.modified", Value: "true"},
			}}, true
		},
	)
	if got.Executable != "/Applications/Mitto.app/Contents/MacOS/mitto-app" ||
		got.Revision != "abc123" || got.BuildTime != "2026-08-14T07:00:00Z" || !got.Modified {
		t.Fatalf("read identity = %+v", got)
	}
}

func TestReadBuildIdentityFallbacks(t *testing.T) {
	got := read(
		func() (string, error) { return "", errors.New("unavailable") },
		func() (*debug.BuildInfo, bool) { return nil, false },
	)
	if got.Executable != unknown || got.Revision != unknown || got.BuildTime != unknown || got.Modified {
		t.Fatalf("fallback identity = %+v", got)
	}
}
