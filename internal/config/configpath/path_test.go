package configpath

import "testing"

func TestParsePath_Valid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // round-tripped via Path.String()
	}{
		{"simple key", "a", "a"},
		{"dotted keys", "a.b.c", "a.b.c"},
		{"array index", "task_label_colors[0].color", "task_label_colors[0].color"},
		{"multiple indices", "a[0][1]", "a[0][1]"},
		{"escaped dot in key", `nodeSelector.kubernetes\.io/role`, `nodeSelector.kubernetes\.io/role`},
		{"escaped comma in key", `a\,b.c`, `a\,b.c`},
		{"escaped backslash in key", `a\\b`, `a\\b`},
		{"leading index only", "[0]", "[0]"},
		{"web port", "web.port", "web.port"},
		{"shortcuts key", "shortcuts", "shortcuts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParsePath(tc.in)
			if err != nil {
				t.Fatalf("ParsePath(%q) error: %v", tc.in, err)
			}
			if got := p.String(); got != tc.want {
				t.Errorf("ParsePath(%q).String() = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParsePath_Invalid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		kind ErrKind
	}{
		{"empty path", "", KindErrSyntax},
		{"empty segment", "a..b", KindErrSyntax},
		{"negative index", "a[-1]", KindErrSyntax},
		{"leading zero index", "a[01]", KindErrSyntax},
		{"non numeric index", "a[x]", KindErrSyntax},
		{"unterminated bracket", "a[0", KindErrSyntax},
		{"unbalanced bracket", "a]0[", KindErrSyntax},
		{"huge index", "a[99999999999999999999]", KindErrLimit},
		{"index over limit", "a[10001]", KindErrLimit},
		{"trailing backslash", `a\`, KindErrSyntax},
		{"invalid escape", `a\xb`, KindErrSyntax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePath(tc.in)
			if err == nil {
				t.Fatalf("ParsePath(%q) expected error, got nil", tc.in)
			}
			pe, ok := err.(*Error)
			if !ok {
				t.Fatalf("ParsePath(%q) error is not *Error: %T", tc.in, err)
			}
			if pe.Kind != tc.kind {
				t.Errorf("ParsePath(%q) error kind = %v, want %v", tc.in, pe.Kind, tc.kind)
			}
		})
	}
}

func TestParsePath_MaxDepth(t *testing.T) {
	// Build a path with more than MaxPathDepth segments.
	s := "a"
	for i := 0; i < MaxPathDepth+5; i++ {
		s += ".a"
	}
	_, err := ParsePath(s)
	if err == nil {
		t.Fatalf("expected max-depth error")
	}
	pe, ok := err.(*Error)
	if !ok || pe.Kind != KindErrLimit {
		t.Fatalf("expected limit error, got %v", err)
	}
}
