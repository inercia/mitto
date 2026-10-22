package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHiddenModels_Policy(t *testing.T) {
	p, err := CompileHiddenModels("s", []string{".*Terra.*", "^gpt-4$", "", "a,b"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id, name string
		want     bool
	}{
		{"openrouter/x", "GPT Terra 5", true},
		{"terra-1", "", true},
		{"GPT-4", "other", true},
		{"gpt-4o", "x", false},
		{"a,b", "x", true},
		{"zzz", "yyy", false},
	}
	for _, c := range cases {
		if got := p.Hides(c.id, c.name); got != c.want {
			t.Errorf("Hides(%q,%q)=%v want %v", c.id, c.name, got, c.want)
		}
	}
	var nilP *HiddenModelPolicy
	if nilP.Hides("a", "b") {
		t.Error("nil policy hides")
	}
}

func TestHiddenModels_Filter(t *testing.T) {
	type m struct{ id, name string }
	in := []m{{"a", "Alpha"}, {"openrouter/t", "GPT Terra 5"}, {"c", "Gamma"}}
	p, _ := CompileHiddenModels("s", []string{".*Terra.*"})
	out := FilterHiddenModels(p, in, func(x m) (string, string) { return x.id, x.name })
	if !reflect.DeepEqual(out, []m{{"a", "Alpha"}, {"c", "Gamma"}}) {
		t.Errorf("got %v", out)
	}
	if len(in) != 3 || in[1].id != "openrouter/t" {
		t.Error("input mutated")
	}
}

func TestHiddenModels_ValidateAndNormalize(t *testing.T) {
	err := ValidateHiddenModels("srv", []string{"ok", "", "("})
	ve, ok := err.(*HiddenModelValidationError)
	if !ok {
		t.Fatalf("got %T %v", err, err)
	}
	if ve.Server != "srv" || ve.Index != 2 || ve.Rule != "(" {
		t.Errorf("bad error %+v", ve)
	}
	if ValidateHiddenModels("s", []string{"", "(?i)x"}) != nil {
		t.Error("expected valid")
	}
	if got := NormalizeHiddenModels([]string{" a ", "", "b"}); !reflect.DeepEqual(got, []string{" a ", "b"}) {
		t.Errorf("got %q", got)
	}
	if NormalizeHiddenModels(nil) != nil {
		t.Error("nil should stay nil")
	}
	if g := NormalizeHiddenModels([]string{}); g == nil || len(g) != 0 {
		t.Error("empty should stay empty")
	}
}

func TestHiddenModels_ParseRoundTrip(t *testing.T) {
	cfg, err := Parse([]byte("acp:\n  - s1:\n      command: x\n      hidden_models: [\"a.*\"]\n"))
	if err != nil || len(cfg.ACPServers) != 1 {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(cfg.ACPServers[0].HiddenModels, []string{"a.*"}) {
		t.Errorf("got %v", cfg.ACPServers[0].HiddenModels)
	}
	if _, err := Parse([]byte("acp:\n  - s1:\n      command: x\n      hidden_models: [\"(\"]\n")); err == nil {
		t.Error("expected invalid rule error")
	}
}

func TestHiddenModels_ConversionAndMerge(t *testing.T) {
	rc := []ACPServer{{Name: "rc", Command: "c", HiddenModels: []string{"r.*"}}}
	st := []ACPServer{{Name: "st", Command: "c", HiddenModels: []string{"s.*"}}}
	res := MergeACPServers(rc, st)
	got := map[string][]string{}
	for _, s := range res.Items {
		got[s.Name] = s.HiddenModels
	}
	if !reflect.DeepEqual(got["rc"], []string{"r.*"}) || !reflect.DeepEqual(got["st"], []string{"s.*"}) {
		t.Errorf("merge lost hidden models: %v", got)
	}

	cfg := &Config{ACPServers: st}
	settings := ConfigToSettings(cfg)
	if !reflect.DeepEqual(settings.ACPServers[0].HiddenModels, []string{"s.*"}) {
		t.Errorf("ConfigToSettings: %v", settings.ACPServers[0].HiddenModels)
	}
	back := settings.ToConfig()
	if !reflect.DeepEqual(back.ACPServers[0].HiddenModels, []string{"s.*"}) {
		t.Errorf("ToConfig: %v", back.ACPServers[0].HiddenModels)
	}
}

func TestHiddenModels_SettingsJSONRoundTrip(t *testing.T) {
	cfg := &Config{ACPServers: []ACPServer{
		{Name: "a", Command: "c", HiddenModels: []string{"x", "y "}},
		{Name: "b", Command: "c"},
	}}
	data, err := json.Marshal(ConfigToSettings(cfg))
	if err != nil {
		t.Fatal(err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	out := s.ToConfig()
	if !reflect.DeepEqual(out.ACPServers[0].HiddenModels, []string{"x", "y "}) {
		t.Errorf("a: %q", out.ACPServers[0].HiddenModels)
	}
	if len(out.ACPServers[1].HiddenModels) != 0 {
		t.Errorf("b: %q", out.ACPServers[1].HiddenModels)
	}
	parsed, err := ParseJSON(data)
	if err != nil || !reflect.DeepEqual(parsed.ACPServers[0].HiddenModels, []string{"x", "y "}) {
		t.Errorf("ParseJSON: %v %v", parsed, err)
	}
}

func TestHiddenModels_SameTypeDifferentNameIsolation(t *testing.T) {
	pa, err := CompileHiddenModels("a", []string{".*Terra.*"})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := CompileHiddenModels("b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pa.Hides("m", "GPT Terra") {
		t.Error("a should hide")
	}
	if pb.Hides("m", "GPT Terra") {
		t.Error("b must be unaffected")
	}
}

func TestHiddenModels_InvalidFileConfigErrors(t *testing.T) {
	dir := t.TempDir()
	yml := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(yml, []byte("acp:\n  - s1:\n      command: x\n      hidden_models: [\"(\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(yml); err == nil {
		t.Error("yaml: expected error")
	}
	js := filepath.Join(dir, "c.json")
	if err := os.WriteFile(js, []byte(`{"acp_servers":[{"name":"s1","command":"x","hidden_models":["("]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(js); err == nil {
		t.Error("json: expected error")
	}
}
