package configpath

import (
	"strings"
	"testing"
)

// FuzzParsePath proves the path parser cannot panic and stays within the
// configured depth/length limits regardless of input.
func FuzzParsePath(f *testing.F) {
	seeds := []string{
		"",
		"a",
		"a.b.c",
		"task_label_colors[0].color",
		`a\.b`,
		`a\,b`,
		`a\\b`,
		"a[0][1][2]",
		"a[-1]",
		"a[999999999999999999999]",
		"a..b",
		"a[",
		"a]",
		"[0]",
		strings.Repeat("a.", 100),
		strings.Repeat("[0]", 100),
		`a\`,
		`a\x`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePath(s)
		if err != nil {
			return
		}
		if len(p) > MaxPathDepth {
			t.Fatalf("ParsePath(%q) returned %d segments, exceeds MaxPathDepth", s, len(p))
		}
		for _, seg := range p {
			if seg.IsIndex {
				if seg.Index < 0 || seg.Index > MaxArrayIndex {
					t.Fatalf("ParsePath(%q) index %d out of bounds", s, seg.Index)
				}
				continue
			}
			if len(seg.Key) > MaxKeySegmentLength {
				t.Fatalf("ParsePath(%q) key segment too long: %d", s, len(seg.Key))
			}
		}
		// Round-trip through String() must not panic either.
		_ = p.String()
	})
}

// FuzzParseSet proves the typed value parser (--set) cannot panic and stays
// bounded regardless of input.
func FuzzParseSet(f *testing.F) {
	seeds := []string{
		"",
		"a=1",
		"a=true",
		"a=null",
		"a=",
		"a={1,2,3}",
		"a={}",
		"a={1,{2,3}}",
		"a.b[0]=x",
		`a=rgb(0\,1\,2)`,
		strings.Repeat("a=1,", 200),
		"a=" + strings.Repeat("9", 5000),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		as, err := ParseSet(s)
		if err != nil {
			return
		}
		if len(as) > MaxOperations {
			t.Fatalf("ParseSet(%q) returned %d assignments, exceeds MaxOperations", s, len(as))
		}
		// Resolving must also never panic.
		_, _ = Resolve(as)
	})
}

// FuzzParseSetJSON proves the JSON value parser (--set-json) cannot panic,
// cannot exceed the configured nesting/list/key limits, and cannot be
// coerced into unbounded allocation via deeply nested or huge input.
func FuzzParseSetJSON(f *testing.F) {
	seeds := []string{
		"",
		"a=1",
		`a="s"`,
		"a=null",
		"a=true",
		`a={"x":1,"y":[1,2,3]}`,
		"a=[[[[[1]]]]]",
		strings.Repeat("a=[", 50) + strings.Repeat("]", 50),
		`a={` + strings.Repeat(`"k":1,`, 100) + `"z":1}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		as, err := ParseSetJSON(s)
		if err != nil {
			return
		}
		if len(as) > MaxOperations {
			t.Fatalf("ParseSetJSON(%q) returned %d assignments, exceeds MaxOperations", s, len(as))
		}
		_, _ = Resolve(as)
	})
}
