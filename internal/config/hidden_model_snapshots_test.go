package config

import "testing"

func TestHiddenModelPoliciesPublication(t *testing.T) {
	var policies HiddenModelPolicies
	servers := []ACPServer{{Name: "one", HiddenModels: []string{"^Alpha$"}}, {Name: "two"}}
	older, err := policies.Prepare(servers)
	if err != nil {
		t.Fatal(err)
	}
	servers[0].HiddenModels[0] = ".*"
	if !older.Policy("one").Hides("a", "ALPHA") || older.Policy("one").Hides("b", "Beta") {
		t.Fatal("prepared policy retained mutable input or lost matching semantics")
	}
	if older.Policy("two") != nil || older.Policy("unknown") != nil {
		t.Fatal("no-rule policy must be nil")
	}
	newer, err := policies.Prepare([]ACPServer{{Name: "one", HiddenModels: []string{"^b$"}}})
	if err != nil {
		t.Fatal(err)
	}
	if newer.Version() <= older.Version() || !policies.Publish(newer) {
		t.Fatal("new publication failed")
	}
	if policies.Publish(older) || policies.Publish(newer) || policies.Publish(nil) {
		t.Fatal("stale publication accepted")
	}
	if _, err := policies.Prepare([]ACPServer{{Name: "one", HiddenModels: []string{"("}}}); err == nil {
		t.Fatal("invalid rules accepted")
	}
	if policies.Snapshot() != newer {
		t.Fatal("invalid preparation changed current policy")
	}
	if !older.Policy("one").Hides("a", "Alpha") {
		t.Fatal("new publication mutated old snapshot")
	}
}
