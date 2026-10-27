package config

import (
	"errors"
	"testing"
)

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

func TestHiddenModelPoliciesAdmitExcludesConcurrentPublication(t *testing.T) {
	var policies HiddenModelPolicies
	first, err := policies.Prepare([]ACPServer{{Name: "one"}})
	if err != nil || !policies.Publish(first) {
		t.Fatalf("publish first: %v", err)
	}
	next, err := policies.Prepare([]ACPServer{{Name: "one", HiddenModels: []string{"^hidden$"}}})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- policies.Admit(func(snapshot *HiddenModelSnapshot) error {
			close(entered)
			<-release
			if snapshot != first {
				return errors.New("admission observed a different snapshot")
			}
			return nil
		})
	}()
	<-entered
	published := make(chan bool, 1)
	go func() { published <- policies.Publish(next) }()
	select {
	case <-published:
		t.Fatal("publication passed a live admission")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if ok := <-published; !ok {
		t.Fatal("publication rejected after admission")
	}
}
