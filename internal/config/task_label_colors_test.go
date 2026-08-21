package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

func TestGlobalTaskLabelColors_RoundTripPreservesUnrelatedSettings(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	original := &Settings{
		Web: WebConfig{Port: 9090},
		Shortcuts: map[string][]ShortcutButton{
			"tasksList": {{Prompt: "Triage tasks"}},
		},
	}
	if err := SaveSettings(original); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	want := []TaskLabelColor{
		{Label: "needs-human", Color: "#ef4444"},
		{Label: "blocked", Color: "#f59e0b"},
	}
	if err := SetGlobalTaskLabelColors(want); err != nil {
		t.Fatalf("SetGlobalTaskLabelColors: %v", err)
	}
	got := GlobalTaskLabelColors()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("GlobalTaskLabelColors = %+v, want %+v in order", got, want)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if cfg.Web.Port != 9090 {
		t.Errorf("Web.Port = %d, want 9090", cfg.Web.Port)
	}
	if got := cfg.Shortcuts["tasksList"]; len(got) != 1 || got[0].Prompt != "Triage tasks" {
		t.Errorf("Shortcuts changed: %+v", cfg.Shortcuts)
	}
	if len(cfg.TaskLabelColors) != 2 || cfg.TaskLabelColors[0] != want[0] {
		t.Errorf("Config.TaskLabelColors = %+v, want %+v", cfg.TaskLabelColors, want)
	}

	if err := SetGlobalTaskLabelColors([]TaskLabelColor{}); err != nil {
		t.Fatalf("SetGlobalTaskLabelColors(clear): %v", err)
	}
	if got := GlobalTaskLabelColors(); len(got) != 0 {
		t.Errorf("after clear = %+v, want empty", got)
	}
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	raw, err := os.ReadFile(filepath.Clean(settingsPath))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("Unmarshal settings: %v", err)
	}
	if _, ok := stored["task_label_colors"]; ok {
		t.Errorf("cleared task_label_colors should be omitted: %s", raw)
	}
}

func TestTaskLabelColors_ConfigSettingsRoundTrip(t *testing.T) {
	want := []TaskLabelColor{{Label: "needs-human", Color: "#ef4444"}}
	got := ConfigToSettings(&Config{TaskLabelColors: want}).ToConfig().TaskLabelColors
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
