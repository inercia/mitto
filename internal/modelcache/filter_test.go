package modelcache

import (
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func pol(t *testing.T, server string, rules ...string) *config.HiddenModelPolicy {
	t.Helper()
	p, err := config.CompileHiddenModels(server, rules)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFilterModelCatalogStates(t *testing.T) {
	d := "desc"
	raw := []Model{{ID: "a-1", Name: "Alpha", Description: &d}, {ID: "b-2", Name: "Beta"}, {ID: "c-3", Name: "Gamma"}}
	snapshot := append([]Model(nil), raw...)

	if r := FilterModelCatalog(pol(t, "s", "x"), nil); r.State != CatalogAbsent || r.Selectable != nil || r.HiddenCount != 0 {
		t.Errorf("absent: %+v", r)
	}
	if r := FilterModelCatalog(nil, []Model{}); r.State != CatalogEmpty || len(r.Selectable) != 0 {
		t.Errorf("empty: %+v", r)
	}
	if r := FilterModelCatalog(pol(t, "s", "."), []Model{}); r.State != CatalogEmpty {
		t.Errorf("empty with policy: %+v", r)
	}
	r := FilterModelCatalog(nil, raw)
	if r.State != CatalogAvailable || !reflect.DeepEqual(r.Selectable, raw) || r.HiddenCount != 0 {
		t.Errorf("nil policy: %+v", r)
	}
	// ID match and name match (OR), order and metadata preserved.
	r = FilterModelCatalog(pol(t, "s", "^b-"), raw)
	if r.State != CatalogAvailable || r.HiddenCount != 1 || r.Selectable[0].ID != "a-1" || r.Selectable[1].ID != "c-3" || r.Selectable[0].Description != &d {
		t.Errorf("id hide: %+v", r)
	}
	r = FilterModelCatalog(pol(t, "s", "gamma", "^a-1$"), raw)
	if r.HiddenCount != 2 || len(r.Selectable) != 1 || r.Selectable[0].ID != "b-2" {
		t.Errorf("name/or hide: %+v", r)
	}
	r = FilterModelCatalog(pol(t, "s", "."), raw)
	if r.State != CatalogPolicyEmpty || len(r.Selectable) != 0 || r.HiddenCount != 3 {
		t.Errorf("policy_empty: %+v", r)
	}
	if !reflect.DeepEqual(raw, snapshot) {
		t.Errorf("input mutated: %+v", raw)
	}
}

func TestFilterModelCatalogPerAgentIsolation(t *testing.T) {
	raw := []Model{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	p1, p2 := pol(t, "one", "^a$"), pol(t, "two", "^b$")
	r1, r2 := FilterModelCatalog(p1, raw), FilterModelCatalog(p2, raw)
	if r1.Selectable[0].ID != "b" || r2.Selectable[0].ID != "a" {
		t.Errorf("isolation: %+v %+v", r1, r2)
	}
}

func TestHiddenSavedIDs(t *testing.T) {
	raw := []Model{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	p := pol(t, "s", "^a$")
	got := HiddenSavedIDs(p, raw, []string{"a", "a", "b", "gone"})
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("got %v", got)
	}
	if got := HiddenSavedIDs(nil, raw, []string{"a"}); len(got) != 0 {
		t.Errorf("nil policy: %v", got)
	}
}
