package configpath

import (
	"strings"
	"testing"
)

// helper to assert an error is a *Error of the given kind and never echoes
// any of the given seeded secret substrings.
func assertSanitizedError(tb interface {
	Helper()
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
}, err error, kind ErrKind, secrets ...string) {
	tb.Helper()
	if err == nil {
		tb.Fatalf("expected error, got nil")
	}
	pe, ok := err.(*Error)
	if !ok {
		tb.Fatalf("error is not *Error: %T: %v", err, err)
	}
	if pe.Kind != kind {
		tb.Errorf("error kind = %v, want %v (msg=%q)", pe.Kind, kind, pe.Msg)
	}
	full := err.Error()
	for _, s := range secrets {
		if s != "" && strings.Contains(full, s) {
			tb.Errorf("error message leaked seeded secret %q: %s", s, full)
		}
	}
}

func TestParseSet_MultipleAssignments(t *testing.T) {
	as, err := ParseSet("a=1,b=true,c=hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(as) != 3 {
		t.Fatalf("got %d assignments, want 3", len(as))
	}
	if as[0].Path.String() != "a" || as[0].Value.Kind != KindInt || as[0].Value.Int != 1 {
		t.Errorf("a = %+v", as[0])
	}
	if as[1].Path.String() != "b" || as[1].Value.Kind != KindBool || !as[1].Value.Bool {
		t.Errorf("b = %+v", as[1])
	}
	if as[2].Path.String() != "c" || as[2].Value.Kind != KindString || as[2].Value.Str != "hello" {
		t.Errorf("c = %+v", as[2])
	}
}

func TestParseSet_IndexedObject(t *testing.T) {
	as, err := ParseSet("task_label_colors[0].color=#ef4444")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(as) != 1 {
		t.Fatalf("got %d assignments, want 1", len(as))
	}
	if as[0].Path.String() != "task_label_colors[0].color" {
		t.Errorf("path = %q", as[0].Path.String())
	}
	if as[0].Value.Kind != KindString || as[0].Value.Str != "#ef4444" {
		t.Errorf("value = %+v", as[0].Value)
	}
}

func TestParseSet_QuotedEscapedSpecialKeys(t *testing.T) {
	as, err := ParseSet(`nodeSelector.kubernetes\.io/role=master`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if as[0].Path.String() != `nodeSelector.kubernetes\.io/role` {
		t.Errorf("path = %q", as[0].Path.String())
	}
	if as[0].Value.Str != "master" {
		t.Errorf("value = %+v", as[0].Value)
	}
}

func TestParseSetString_CommaContainingColor(t *testing.T) {
	as, err := ParseSetString(`shortcuts.color=rgb(0\,128\,255)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if as[0].Value.Kind != KindString || as[0].Value.Str != "rgb(0,128,255)" {
		t.Errorf("value = %+v", as[0].Value)
	}
}

func TestParseSetJSON_ObjectsAndArrays(t *testing.T) {
	as, err := ParseSetJSON(`shortcuts={"icon":"star","prompt":"review"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if as[0].Value.Kind != KindJSON {
		t.Fatalf("kind = %v", as[0].Value.Kind)
	}
	m, ok := as[0].Value.JSON.(map[string]interface{})
	if !ok || m["icon"] != "star" {
		t.Errorf("json = %v", as[0].Value.JSON)
	}
}

func TestParseSetJSON_MultipleAssignmentsWithNestedCommas(t *testing.T) {
	as, err := ParseSetJSON(`a={"x":1,"y":2},b=[1,2,3]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(as) != 2 {
		t.Fatalf("got %d assignments, want 2", len(as))
	}
}

func TestParseSetJSON_MalformedRejected(t *testing.T) {
	secret := "s3cr3t-token-value"
	_, err := ParseSetJSON("a={" + secret)
	assertSanitizedError(t, err, KindErrSyntax, secret)
}

func TestParseSet_EmptyValue(t *testing.T) {
	as, err := ParseSet("a=")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if as[0].Value.Kind != KindString || as[0].Value.Str != "" {
		t.Errorf("value = %+v, want empty string", as[0].Value)
	}
}

func TestParseSet_NullIsAValue(t *testing.T) {
	as, err := ParseSet("a=null")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if as[0].Value.Kind != KindNull {
		t.Errorf("value = %+v, want null", as[0].Value)
	}
}

func TestParseSet_SparseAndNegativeIndexesRejected(t *testing.T) {
	cases := []string{
		"a[-1]=1",
		"a[999999999999]=1",
	}
	for _, in := range cases {
		_, err := ParseSet(in)
		if err == nil {
			t.Errorf("ParseSet(%q) expected error", in)
		}
	}
}

func TestParseSet_RepeatedFlagLastWins(t *testing.T) {
	first, err := ParseSet("a=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := ParseSet("a=2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	set, err := Resolve(append(first, second...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(set.Ops) != 1 || set.Ops[0].Value.Int != 2 {
		t.Errorf("got %+v, want single op with value 2", set.Ops)
	}
}

func TestResolve_MixedModePrecedenceIsArgumentOrder(t *testing.T) {
	typed, err := ParseSet("a=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	str, err := ParseSetString("a=2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	jsn, err := ParseSetJSON(`a=3`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Order: typed, string, json -> json (last) wins.
	set, err := Resolve(append(append(append([]Assignment{}, typed...), str...), jsn...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(set.Ops) != 1 || set.Ops[0].Value.Kind != KindJSON {
		t.Errorf("got %+v, want single json op", set.Ops)
	}

	// Reversed order: json, string, typed -> typed (last) wins.
	set2, err := Resolve(append(append(append([]Assignment{}, jsn...), str...), typed...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(set2.Ops) != 1 || set2.Ops[0].Value.Kind != KindInt {
		t.Errorf("got %+v, want single typed int op", set2.Ops)
	}
}

func TestResolve_AncestorDescendantConflictRejected(t *testing.T) {
	cases := [][]string{
		{"a.b=1", "a=2"},
		{"a=2", "a.b=1"},
	}
	for _, pair := range cases {
		var all []Assignment
		for _, raw := range pair {
			as, err := ParseSet(raw)
			if err != nil {
				t.Fatalf("ParseSet(%q) error: %v", raw, err)
			}
			all = append(all, as...)
		}
		_, err := Resolve(all)
		assertSanitizedError(t, err, KindErrConflict)
	}
}

func TestResolve_NoConflictForUnrelatedPaths(t *testing.T) {
	as1, _ := ParseSet("a.b=1")
	as2, _ := ParseSet("a.c=2")
	set, err := Resolve(append(as1, as2...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(set.Ops) != 2 {
		t.Errorf("got %d ops, want 2", len(set.Ops))
	}
}

func TestParseSetFile_LiteralContentPreserved(t *testing.T) {
	content := "line1\nline2,with,commas\n{not json}"
	a, err := ParseSetFile("shortcuts.body=" + content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Mode != ModeFile || a.Value.Kind != KindString || a.Value.Str != content {
		t.Errorf("got %+v", a)
	}
}

func TestParseSetFile_TooLarge(t *testing.T) {
	big := strings.Repeat("x", MaxFileValueBytes+1)
	_, err := ParseSetFile("a=" + big)
	assertSanitizedError(t, err, KindErrLimit)
}

func TestParse_InputTooLarge(t *testing.T) {
	big := "a=" + strings.Repeat("x", MaxRawInputBytes+1)
	_, err := ParseSet(big)
	assertSanitizedError(t, err, KindErrLimit)
}

func TestParse_ErrorsDoNotLeakValues(t *testing.T) {
	secret := "sk-super-secret-do-not-leak"
	cases := []struct {
		fn  func(string) ([]Assignment, error)
		raw string
	}{
		{ParseSet, "a[bad]=" + secret},
		{ParseSetString, "a[-1]=" + secret},
		{ParseSetJSON, "a={" + secret},
	}
	for _, tc := range cases {
		_, err := tc.fn(tc.raw)
		if err == nil {
			t.Fatalf("expected error for %q", tc.raw)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked secret: %v", err)
		}
	}
}
