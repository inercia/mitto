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
