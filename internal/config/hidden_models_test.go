package config

import (
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
