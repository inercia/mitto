package slackbridge

import "testing"

func TestDedupeSet_SeenBefore(t *testing.T) {
	d := newDedupeSet(0)

	if d.SeenBefore("a") {
		t.Error("first SeenBefore(a) = true, want false")
	}
	if !d.SeenBefore("a") {
		t.Error("second SeenBefore(a) = false, want true (already seen)")
	}
	if d.SeenBefore("b") {
		t.Error("first SeenBefore(b) = true, want false")
	}
}

func TestDedupeSet_EmptyIDAlwaysSeen(t *testing.T) {
	d := newDedupeSet(0)
	if !d.SeenBefore("") {
		t.Error("SeenBefore(\"\") = false, want true (undedupable events are always dropped)")
	}
	if !d.SeenBefore("") {
		t.Error("SeenBefore(\"\") = false on second call, want true")
	}
}

func TestDedupeSet_BoundedEviction(t *testing.T) {
	d := newDedupeSet(2)

	if d.SeenBefore("1") {
		t.Fatal("SeenBefore(1) = true on first insert")
	}
	// "2" is checked twice in a row (no other insert in between) so its
	// second check observes the set unmutated by any other id.
	if d.SeenBefore("2") {
		t.Fatal("SeenBefore(2) = true on first insert")
	}
	if !d.SeenBefore("2") {
		t.Error("SeenBefore(2) = false on second check, want true (not yet evicted)")
	}
	// Capacity is 2 and the set already holds {"1","2"}; inserting a 3rd
	// evicts the oldest ("1") — each SeenBefore call that inserts a new id
	// counts against capacity, so checking "1" again here would itself
	// re-insert it and evict something else. Assert eviction from the
	// dedupeSet's own bookkeeping instead of chaining further checks.
	d.SeenBefore("3")
	if _, ok := d.seen["1"]; ok {
		t.Error("expected \"1\" to have been evicted once capacity (2) was exceeded")
	}
	if _, ok := d.seen["3"]; !ok {
		t.Error("expected \"3\" to be present after insert")
	}
}
