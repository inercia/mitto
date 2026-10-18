package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

func TestNormalizeModelShortcuts(t *testing.T) {
	tests := []struct {
		name string
		in   []ModelShortcut
		want []ModelShortcut
	}{
		{"nil", nil, nil},
		{"empty", []ModelShortcut{}, []ModelShortcut{}},
		{"all invalid", []ModelShortcut{{0, "Tag"}, {6, "Tag"}, {1, " \t"}}, []ModelShortcut{}},
		{"first valid and order", []ModelShortcut{
			{2, " "}, {0, "Invalid"}, {6, "Invalid"}, {-1, "Invalid"},
			{4, " Custom  TAG\t"}, {2, " Smart "}, {4, "Duplicate"}, {1, "Smart"},
		}, []ModelShortcut{{4, "Custom  TAG"}, {2, "Smart"}, {1, "Smart"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]ModelShortcut(nil), tt.in...)
			got := NormalizeModelShortcuts(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeModelShortcuts = %#v, want %#v", got, tt.want)
			}
			if len(tt.in) > 0 && !reflect.DeepEqual(tt.in, before) {
				t.Fatalf("normalization mutated input: %#v", tt.in)
			}
			if len(got) > 0 {
				got[0].ModelTag = "changed"
				if !reflect.DeepEqual(tt.in, before) {
					t.Fatal("normalized slice aliases input")
				}
			}
		})
	}
}

func TestValidateModelShortcuts(t *testing.T) {
	tests := []struct {
		name  string
		in    []ModelShortcut
		field string
	}{
		{"nil", nil, ""},
		{"empty", []ModelShortcut{}, ""},
		{"custom and shared tags", []ModelShortcut{{5, " Custom  TAG "}, {1, "Custom  TAG"}}, ""},
		{"zero", []ModelShortcut{{0, "Smart"}}, "model_shortcuts[0].number"},
		{"negative", []ModelShortcut{{-1, "Smart"}}, "model_shortcuts[0].number"},
		{"too high", []ModelShortcut{{6, "Smart"}}, "model_shortcuts[0].number"},
		{"blank", []ModelShortcut{{1, "Smart"}, {2, " \t\n"}}, "model_shortcuts[1].model_tag"},
		{"duplicate", []ModelShortcut{{1, "Smart"}, {2, "Fast"}, {1, "Custom"}}, "model_shortcuts[2].number"},
		{"first input error", []ModelShortcut{{1, " "}, {1, "Smart"}}, "model_shortcuts[0].model_tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]ModelShortcut(nil), tt.in...)
			err := ValidateModelShortcuts(tt.in)
			if tt.field == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var validationErr *ModelShortcutValidationError
				if !errors.As(err, &validationErr) || validationErr.Field != tt.field || !strings.Contains(err.Error(), tt.field) {
					t.Fatalf("error = %v, want structured error for %s", err, tt.field)
				}
			}
			if len(tt.in) > 0 && !reflect.DeepEqual(tt.in, before) {
				t.Fatal("validation mutated input")
			}
		})
	}
}

func TestModelShortcutsParsingAndRoundTrip(t *testing.T) {
	jsonInput := `{"model_shortcuts":[{"number":2,"model_tag":" "},{"number":4,"model_tag":" Custom  TAG "},{"number":2,"model_tag":" Smart "},{"number":4,"model_tag":"duplicate"},{"number":6,"model_tag":"invalid"},{"number":1,"model_tag":"Smart"},{"number":null,"model_tag":"null"},{"model_tag":"omitted"}],"models":[{"name":"Profile","tags":["Smart"],"shortcut_number":3}],"shortcuts":{"conversations":[{"prompt":"Keep toolbar"}]}}`
	yamlInput := `model_shortcuts:
  - {number: 2, model_tag: " "}
  - {number: 4, model_tag: " Custom  TAG "}
  - {number: 2, model_tag: " Smart "}
  - {number: 4, model_tag: duplicate}
  - {number: 6, model_tag: invalid}
  - {number: 1, model_tag: Smart}
  - {number: null, model_tag: "null"}
  - {model_tag: omitted}
models:
  - name: Profile
    tags: [Smart]
    shortcutNumber: 3
shortcuts:
  conversations:
    - prompt: Keep toolbar
`
	want := []ModelShortcut{{4, "Custom  TAG"}, {2, "Smart"}, {1, "Smart"}}
	for name, input := range map[string]string{"json": jsonInput, "yaml": yamlInput} {
		t.Run(name, func(t *testing.T) {
			parse := ParseJSON
			if name == "yaml" {
				parse = Parse
			}
			cfg, err := parse([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.ModelShortcuts, want) {
				t.Fatalf("parsed shortcuts = %#v, want %#v", cfg.ModelShortcuts, want)
			}
			settings := ConfigToSettings(cfg)
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("shortcut_number")) || bytes.Contains(data, []byte("shortcutNumber")) {
				t.Fatalf("legacy field serialized: %s", data)
			}
			reloaded, err := ParseJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reloaded.ModelShortcuts, want) || !reflect.DeepEqual(reloaded.Models, cfg.Models) || !reflect.DeepEqual(reloaded.Shortcuts, cfg.Shortcuts) {
				t.Fatalf("round-trip changed mappings, profiles or toolbar: %+v", reloaded)
			}
			settings.ModelShortcuts[0].ModelTag = "settings-only"
			if cfg.ModelShortcuts[0].ModelTag != want[0].ModelTag {
				t.Fatal("ConfigToSettings aliases Config shortcuts")
			}
			converted := settings.ToConfig()
			converted.ModelShortcuts[0].ModelTag = "config-only"
			if settings.ModelShortcuts[0].ModelTag != "settings-only" {
				t.Fatal("ToConfig aliases Settings shortcuts")
			}
		})
	}
}

func TestModelShortcutsConversionNormalizes(t *testing.T) {
	in := []ModelShortcut{{1, " "}, {1, " Custom "}, {1, "duplicate"}, {9, "invalid"}}
	want := []ModelShortcut{{1, "Custom"}}
	if got := (&Settings{ModelShortcuts: in}).ToConfig().ModelShortcuts; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToConfig = %#v, want %#v", got, want)
	}
	if got := ConfigToSettings(&Config{ModelShortcuts: in}).ModelShortcuts; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigToSettings = %#v, want %#v", got, want)
	}
	if in[1].ModelTag != " Custom " {
		t.Fatal("conversion mutated input")
	}
}

func TestModelShortcutsNilEmptyPersistence(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []ModelShortcut
		json string
	}{
		{"nil", nil, "null"},
		{"empty", []ModelShortcut{}, "[]"},
		{"invalid becomes empty", []ModelShortcut{{0, "invalid"}}, "[]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			settings := ConfigToSettings(&Config{ModelShortcuts: tt.in})
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["model_shortcuts"]) != tt.json {
				t.Fatalf("model_shortcuts = %s, want %s", fields["model_shortcuts"], tt.json)
			}
			cfg, err := ParseJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if (cfg.ModelShortcuts == nil) != (tt.in == nil) || len(cfg.ModelShortcuts) != 0 {
				t.Fatalf("nil/empty distinction lost: %#v", cfg.ModelShortcuts)
			}
		})
	}
	for _, tt := range []struct {
		input string
		nil   bool
	}{
		{"{}", true}, {"{\"model_shortcuts\":null}", true}, {"{\"model_shortcuts\":[]}", false},
		{"model_shortcuts: null", true}, {"model_shortcuts: []", false},
	} {
		parse := ParseJSON
		if !strings.HasPrefix(tt.input, "{") {
			parse = Parse
		}
		cfg, err := parse([]byte(tt.input))
		if err != nil || (cfg.ModelShortcuts == nil) != tt.nil {
			t.Fatalf("parse %s = %+v, %v; want nil=%t", tt.input, cfg, err, tt.nil)
		}
	}
}

func TestModelShortcutsRejectEncodedTypes(t *testing.T) {
	for _, number := range []string{"1.5", "1.0", `"1"`, "true", "[]", "{}", "9223372036854775808"} {
		t.Run("json/"+number, func(t *testing.T) {
			if _, err := ParseJSON([]byte(`{"model_shortcuts":[{"number":` + number + `,"model_tag":"Smart"}]}`)); err == nil {
				t.Fatal("expected JSON number decoding error")
			}
		})
		t.Run("yaml/"+number, func(t *testing.T) {
			if _, err := Parse([]byte("model_shortcuts: [{number: " + number + ", model_tag: Smart}]")); err == nil {
				t.Fatal("expected YAML number decoding error")
			}
		})
	}
	for _, tag := range []string{"1", "true", "[]", "{}"} {
		if _, err := ParseJSON([]byte(`{"model_shortcuts":[{"number":1,"model_tag":` + tag + `}]}`)); err == nil {
			t.Errorf("JSON model_tag %s: expected decoding error", tag)
		}
		if _, err := Parse([]byte("model_shortcuts: [{number: 1, model_tag: " + tag + "}]")); err == nil {
			t.Errorf("YAML model_tag %s: expected decoding error", tag)
		}
	}
	for _, input := range []string{`{"model_shortcuts":[`, `{"model_shortcuts":{}}`, `{"model_shortcuts":[1]}`} {
		if _, err := ParseJSON([]byte(input)); err == nil {
			t.Errorf("expected JSON decode error for %s", input)
		}
	}
	for _, input := range []string{"model_shortcuts: [", "model_shortcuts: {}", "model_shortcuts: [1]"} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("expected YAML decode error for %s", input)
		}
	}
}

func TestModelShortcutsLegacyNoInference(t *testing.T) {
	for name, input := range map[string]string{
		"json": `{"models":[{"name":"Single","tags":["Smart"],"shortcut_number":1},{"name":"Multiple","tags":["Smart","Fast"],"shortcut_number":2}]}`,
		"yaml": "models:\n  - {name: Single, tags: [Smart], shortcutNumber: 1}\n  - {name: Multiple, tags: [Smart, Fast], shortcutNumber: 2}\n",
	} {
		t.Run(name, func(t *testing.T) {
			parse := ParseJSON
			if name == "yaml" {
				parse = Parse
			}
			cfg, err := parse([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			want := []ModelProfile{{Name: "Single", Tags: []string{"Smart"}}, {Name: "Multiple", Tags: []string{"Smart", "Fast"}}}
			if cfg.ModelShortcuts != nil || !reflect.DeepEqual(cfg.Models, want) {
				t.Fatalf("legacy parsing changed profiles or inferred shortcuts: %+v", cfg)
			}
			data, err := json.Marshal(ConfigToSettings(cfg))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("shortcut_number")) || bytes.Contains(data, []byte("shortcutNumber")) {
				t.Fatalf("legacy key survived: %s", data)
			}
		})
	}
}

func TestModelShortcutsSettingsRCPrecedence(t *testing.T) {
	for _, withRC := range []bool{false, true} {
		for _, tt := range []struct {
			name  string
			field string
			want  []ModelShortcut
		}{
			{"missing", "", nil},
			{"null", `,"model_shortcuts":null`, nil},
			{"empty", `,"model_shortcuts":[]`, []ModelShortcut{}},
			{"populated", `,"model_shortcuts":[{"number":2,"model_tag":" Custom "}]`, []ModelShortcut{{2, "Custom"}}},
			{"invalid becomes clear", `,"model_shortcuts":[{"number":0,"model_tag":"Invalid"}]`, []ModelShortcut{}},
		} {
			t.Run(fmt.Sprintf("rc=%t/%s", withRC, tt.name), func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv(appdir.MittoDirEnv, dir)
				t.Setenv("HOME", dir)
				appdir.ResetCache()
				t.Cleanup(appdir.ResetCache)
				path := filepath.Join(dir, appdir.SettingsFileName)
				input := []byte(`{"acp_servers":[{"name":"settings-server","command":"settings-cmd"}],"models":[{"name":"SettingsProfile"}],"web":{"port":4321}` + tt.field + `}`)
				if err := os.WriteFile(path, input, 0o600); err != nil {
					t.Fatal(err)
				}
				if withRC {
					rc := "acp:\n  - rc-server:\n      command: rc-cmd\nmodels:\n  - name: RCProfile\nweb:\n  port: 1234\nmodel_shortcuts:\n  - {number: 5, model_tag: ' RC ' }\n"
					if err := os.WriteFile(filepath.Join(dir, ".mittorc"), []byte(rc), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				want := tt.want
				if withRC && want == nil {
					want = []ModelShortcut{{5, "RC"}}
				}
				result, err := LoadSettingsWithFallbackNoKeychain()
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result.Config.ModelShortcuts, want) {
					t.Fatalf("effective shortcuts = %#v, want %#v", result.Config.ModelShortcuts, want)
				}
				profile, port, servers := "SettingsProfile", 4321, 1
				if withRC {
					profile, port, servers = "RCProfile", 1234, 2
				}
				if len(result.Config.Models) != 1 || result.Config.Models[0].Name != profile || result.Config.Web.Port != port || len(result.Config.ACPServers) != servers {
					t.Fatalf("unrelated RC merge changed: %+v", result.Config)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(input, after) {
					t.Fatalf("load rewrote settings: %s, %v", after, err)
				}
				// Explicit empty survives disk serialization and another restart.
				if tt.name == "empty" {
					if err := SaveSettings(ConfigToSettings(result.Config)); err != nil {
						t.Fatal(err)
					}
					reloaded, err := LoadSettingsWithFallbackNoKeychain()
					if err != nil {
						t.Fatal(err)
					}
					if reloaded.Config.ModelShortcuts == nil || len(reloaded.Config.ModelShortcuts) != 0 {
						t.Fatalf("clear lost after restart: %#v", reloaded.Config.ModelShortcuts)
					}
				}
			})
		}
	}
}

func TestModelShortcutsUnrelatedRawSettingsWrite(t *testing.T) {
	for _, value := range []string{"null", "[]", `[{"number":3,"model_tag":" Custom  TAG "}]`} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, dir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)
			path := filepath.Join(dir, appdir.SettingsFileName)
			input := `{"model_shortcuts":` + value + `,"models":[{"name":"KeepProfile","tags":["Smart"]}],"web":{"port":1234}}`
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := SetGlobalShortcuts(map[string][]ShortcutButton{"conversations": {{Prompt: "Unrelated toolbar write"}}}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, fields["model_shortcuts"]); err != nil {
				t.Fatal(err)
			}
			if compact.String() != value {
				t.Fatalf("raw write changed mappings: %s, want %s", compact.String(), value)
			}
			var settings Settings
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			if len(settings.Models) != 1 || settings.Models[0].Name != "KeepProfile" || settings.Web.Port != 1234 {
				t.Fatalf("raw write changed unrelated settings: %+v", settings)
			}
		})
	}
}
