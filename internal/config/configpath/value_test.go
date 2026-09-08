package configpath

import (
	"reflect"
	"testing"
)

func TestParseTypedScalar(t *testing.T) {
	cases := []struct {
		in   string
		kind ValueKind
		want interface{}
	}{
		{"", KindString, ""},
		{"null", KindNull, nil},
		{"true", KindBool, true},
		{"false", KindBool, false},
		{"42", KindInt, int64(42)},
		{"-7", KindInt, int64(-7)},
		{"0", KindInt, int64(0)},
		{"007", KindString, "007"},
		{"-007", KindString, "-007"},
		{"3.14", KindFloat, 3.14},
		{"1e10", KindFloat, 1e10},
		{"hello", KindString, "hello"},
	}
	for _, tc := range cases {
		v, err := parseTypedScalar(tc.in)
		if err != nil {
			t.Fatalf("parseTypedScalar(%q) error: %v", tc.in, err)
		}
		if v.Kind != tc.kind {
			t.Errorf("parseTypedScalar(%q).Kind = %v, want %v", tc.in, v.Kind, tc.kind)
		}
		switch tc.kind {
		case KindBool:
			if v.Bool != tc.want {
				t.Errorf("parseTypedScalar(%q).Bool = %v, want %v", tc.in, v.Bool, tc.want)
			}
		case KindInt:
			if v.Int != tc.want {
				t.Errorf("parseTypedScalar(%q).Int = %v, want %v", tc.in, v.Int, tc.want)
			}
		case KindFloat:
			if v.Float != tc.want {
				t.Errorf("parseTypedScalar(%q).Float = %v, want %v", tc.in, v.Float, tc.want)
			}
		case KindString:
			if v.Str != tc.want {
				t.Errorf("parseTypedScalar(%q).Str = %v, want %v", tc.in, v.Str, tc.want)
			}
		}
	}
}

func TestParseTypedValue_BraceList(t *testing.T) {
	v, err := parseTypedValue("{a,1,true,null}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Kind != KindList || len(v.List) != 4 {
		t.Fatalf("got %+v", v)
	}
	if v.List[0].Kind != KindString || v.List[0].Str != "a" {
		t.Errorf("elem0 = %+v", v.List[0])
	}
	if v.List[1].Kind != KindInt || v.List[1].Int != 1 {
		t.Errorf("elem1 = %+v", v.List[1])
	}
	if v.List[2].Kind != KindBool || !v.List[2].Bool {
		t.Errorf("elem2 = %+v", v.List[2])
	}
	if v.List[3].Kind != KindNull {
		t.Errorf("elem3 = %+v", v.List[3])
	}
}

func TestParseTypedValue_EmptyBraceList(t *testing.T) {
	v, err := parseTypedValue("{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Kind != KindList || len(v.List) != 0 {
		t.Fatalf("got %+v, want empty list", v)
	}
}

func TestParseTypedValue_NestedListRejected(t *testing.T) {
	_, err := parseTypedValue("{a,{b,c}}")
	if err == nil {
		t.Fatalf("expected error for nested list")
	}
}

func TestParseStringValue_ForcesString(t *testing.T) {
	v, err := parseStringValue("42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Kind != KindString || v.Str != "42" {
		t.Errorf("got %+v, want string 42", v)
	}

	v, err = parseStringValue("true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Kind != KindString || v.Str != "true" {
		t.Errorf("got %+v, want string true", v)
	}
}

func TestParseStringValue_CommaContainingColor(t *testing.T) {
	// rgb-style value containing commas must be escaped by the caller.
	v, err := parseStringScalar(`rgb(0\,128\,255)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Kind != KindString || v.Str != "rgb(0,128,255)" {
		t.Errorf("got %+v, want rgb(0,128,255)", v)
	}
}

func TestParseJSONValue_Scalars(t *testing.T) {
	cases := []struct {
		in   string
		kind ValueKind
	}{
		{"42", KindJSON},
		{`"hello"`, KindJSON},
		{"true", KindJSON},
		{"null", KindJSON},
		{"3.14", KindJSON},
	}
	for _, tc := range cases {
		v, err := parseJSONValue(tc.in)
		if err != nil {
			t.Fatalf("parseJSONValue(%q) error: %v", tc.in, err)
		}
		if v.Kind != tc.kind {
			t.Errorf("parseJSONValue(%q).Kind = %v, want %v", tc.in, v.Kind, tc.kind)
		}
	}
}

func TestParseJSONValue_ObjectAndArray(t *testing.T) {
	v, err := parseJSONValue(`{"a":1,"b":[1,2,3]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := v.JSON.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", v.JSON)
	}
	if !reflect.DeepEqual(m["a"], int64(1)) {
		t.Errorf("a = %v, want int64(1)", m["a"])
	}
	arr, ok := m["b"].([]interface{})
	if !ok || len(arr) != 3 {
		t.Fatalf("b = %v", m["b"])
	}
}

func TestParseJSONValue_Malformed(t *testing.T) {
	cases := []string{
		"",
		"{",
		"[1,2,",
		`{"a":}`,
		"1 2",
		"tru",
	}
	for _, in := range cases {
		_, err := parseJSONValue(in)
		if err == nil {
			t.Errorf("parseJSONValue(%q) expected error", in)
		}
	}
}

func TestParseJSONValue_NestingLimit(t *testing.T) {
	deep := ""
	for i := 0; i < MaxJSONNestingDepth+5; i++ {
		deep += "["
	}
	for i := 0; i < MaxJSONNestingDepth+5; i++ {
		deep += "]"
	}
	_, err := parseJSONValue(deep)
	if err == nil {
		t.Fatalf("expected nesting-limit error")
	}
	pe, ok := err.(*Error)
	if !ok || pe.Kind != KindErrLimit {
		t.Fatalf("expected limit error, got %v", err)
	}
}

// --- Value.ToJSON (mitto-4rz.5) ---------------------------------------------

// TestValue_ToJSON_Scalars pins the pure Kind->interface{} conversion for
// every scalar Kind, the single source of truth shared by configsvc's
// write path and the CLI's live `config set` mode (see value.go's doc
// comment on ToJSON).
func TestValue_ToJSON_Scalars(t *testing.T) {
	cases := []struct {
		name string
		in   Value
		want interface{}
	}{
		{"null", Value{Kind: KindNull}, nil},
		{"bool true", Value{Kind: KindBool, Bool: true}, true},
		{"bool false", Value{Kind: KindBool, Bool: false}, false},
		{"int", Value{Kind: KindInt, Int: 42}, int64(42)},
		{"negative int", Value{Kind: KindInt, Int: -7}, int64(-7)},
		{"float", Value{Kind: KindFloat, Float: 3.14}, 3.14},
		{"string", Value{Kind: KindString, Str: "hello"}, "hello"},
		{"empty string", Value{Kind: KindString, Str: ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.in.ToJSON()
			if err != nil {
				t.Fatalf("ToJSON() error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ToJSON() = %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestValue_ToJSON_List pins recursive conversion of a KindList's elements
// (produced by a `--set` brace list, e.g. `{a,1,true,null}`), each of which
// converts independently per its own Kind.
func TestValue_ToJSON_List(t *testing.T) {
	v := Value{Kind: KindList, List: []Value{
		{Kind: KindString, Str: "a"},
		{Kind: KindInt, Int: 1},
		{Kind: KindBool, Bool: true},
		{Kind: KindNull},
	}}
	got, err := v.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error: %v", err)
	}
	want := []interface{}{"a", int64(1), true, nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ToJSON() = %#v, want %#v", got, want)
	}
}

// TestValue_ToJSON_List_Empty pins that an explicit `--set foo={}` (empty
// list, distinct from null/absent per docs/config/config-cli.md) converts to
// a non-nil empty []interface{}, not a nil slice that would round-trip as
// JSON null instead of [].
func TestValue_ToJSON_List_Empty(t *testing.T) {
	v := Value{Kind: KindList, List: []Value{}}
	got, err := v.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error: %v", err)
	}
	arr, ok := got.([]interface{})
	if !ok || arr == nil || len(arr) != 0 {
		t.Errorf("ToJSON() = %#v (%T), want a non-nil empty []interface{}", got, got)
	}
}

// TestValue_ToJSON_JSON pins that a KindJSON value (produced by
// `--set-json`) passes its already-decoded JSON payload through unchanged,
// object/array/scalar alike.
func TestValue_ToJSON_JSON(t *testing.T) {
	payload := map[string]interface{}{"a": int64(1), "b": []interface{}{int64(1), int64(2)}}
	v := Value{Kind: KindJSON, JSON: payload}
	got, err := v.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error: %v", err)
	}
	if !reflect.DeepEqual(got, payload) {
		t.Errorf("ToJSON() = %#v, want %#v", got, payload)
	}
}

// TestValue_ToJSON_UnknownKind_Errors pins that an invalid/zero-initialized
// Kind (never produced by the parser, but a defensive guard against a future
// added Kind forgetting to extend this switch) is a hard error, not a silent
// nil.
func TestValue_ToJSON_UnknownKind_Errors(t *testing.T) {
	v := Value{Kind: ValueKind(99)}
	if _, err := v.ToJSON(); err == nil {
		t.Fatalf("expected an error for an unknown Value.Kind")
	}
}
