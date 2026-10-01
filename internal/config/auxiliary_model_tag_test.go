package config

import (
	"encoding/json"
	"testing"

	defaultConfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/appdir"
)

func TestConversationsConfig_GetAuxiliaryModelTag(t *testing.T) {
	var nilCfg *ConversationsConfig
	if got := nilCfg.GetAuxiliaryModelTag(); got != "" {
		t.Errorf("nil receiver = %q, want empty", got)
	}
	if got := (&ConversationsConfig{}).GetAuxiliaryModelTag(); got != "" {
		t.Errorf("unset = %q, want empty", got)
	}
	if got := (&ConversationsConfig{AuxiliaryModelTag: "  Cheap "}).GetAuxiliaryModelTag(); got != "Cheap" {
		t.Errorf("trimmed = %q, want Cheap", got)
	}
}

func TestAuxiliaryModelTag_SettingsConfigRoundTrip(t *testing.T) {
	in := &Settings{Conversations: &ConversationsConfig{AuxiliaryModelTag: "Cheap"}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Settings
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.ToConfig().Conversations.GetAuxiliaryModelTag(); got != "Cheap" {
		t.Errorf("Settings->Config = %q, want Cheap", got)
	}
	back := ConfigToSettings(out.ToConfig())
	if got := back.Conversations.GetAuxiliaryModelTag(); got != "Cheap" {
		t.Errorf("Config->Settings = %q, want Cheap", got)
	}
}

func TestAuxiliaryModelTag_SavedSettingsRoundTrip(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	if err := SaveSettings(&Settings{Conversations: &ConversationsConfig{AuxiliaryModelTag: "Fast"}}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := loaded.Conversations.GetAuxiliaryModelTag(); got != "Fast" {
		t.Errorf("loaded = %q, want Fast", got)
	}
}

func TestAuxiliaryModelTag_YAMLParse(t *testing.T) {
	cfg, err := Parse([]byte("acp:\n  - test:\n      command: echo\nconversations:\n  auxiliary_model_tag: Cheap\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Conversations == nil || cfg.Conversations.AuxiliaryModelTag != "Cheap" {
		t.Fatalf("Conversations = %+v, want AuxiliaryModelTag=Cheap", cfg.Conversations)
	}

	cfg, err = Parse([]byte("acp:\n  - test:\n      command: echo\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Conversations != nil {
		t.Errorf("Conversations = %+v, want nil when nothing set", cfg.Conversations)
	}
}

func TestAuxiliaryModelTag_DefaultYAML(t *testing.T) {
	cfg, err := Parse(defaultConfig.DefaultConfigYAML)
	if err != nil {
		t.Fatalf("Parse default: %v", err)
	}
	tag := cfg.Conversations.GetAuxiliaryModelTag()
	if tag != "Cheap" {
		t.Fatalf("default auxiliary_model_tag = %q, want Cheap", tag)
	}
	found := false
	for _, p := range cfg.EffectiveModelProfiles() {
		for _, pt := range p.Tags {
			if pt == tag {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no model profile carries default auxiliary tag %q", tag)
	}
}
