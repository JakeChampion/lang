package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `_` is a discard at every binding site, so each occurrence gets its own
// internal name (`discardName`) rather than binding `_`. Two consequences,
// both asserted here: several discards may share a scope, and `_` itself is
// never in scope.
//
// It was already a wildcard in match patterns, in `for (k, _) in m`, and in
// a single parameter position — only `var` bindings, destructure elements
// and repeated parameters treated it as a real name. `var (a, _) = t(); var
// (b, _) = t();` failed with `variable "_" already declared in this scope`,
// which describes the implementation rather than the mistake: nobody
// declared a variable, they wrote a discard twice.
func TestDiscardsMayRepeatInOneScope(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"two tuple destructures", `function t(): (i32, i32) { return (1, 2); }
function main(): i32 { var (a, _) = t(); var (b, _) = t(); return a + b; }`},
		{"every element discarded", `function t(): (i32, i32) { return (1, 2); }
function main(): i32 { var (_, _) = t(); return 0; }`},
		{"discard in either position", `function t(): (i32, i32) { return (1, 2); }
function main(): i32 { var (a, _) = t(); var (_, b) = t(); return a + b; }`},
		{"repeated plain binding", `function main(): i32 { var _ = 5; var _ = 6; return 0; }`},
		{"repeated parameter", `function f(_: i32, _: i32): i32 { return 7; }
function main(): i32 { return f(1, 2); }`},
		{"repeated lambda parameter", `function main(): i32 {
    var f: (i32, i32) => i32 = (_: i32, _: i32) => 7;
    return f(1, 2);
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Fatalf("repeated discard rejected in %s: %v", tc.name, err)
			}
		})
	}
}

// A discard introduces no name spelled `_`. The rename is what makes both
// halves of the contract hold, so assert it on the AST directly rather than
// only through its effects: nothing reaches the checker bound to `_`, which
// is why reading it back is an undefined identifier.
// `conformance/cases/underscore_not_readable` pins that end to end.
func TestDiscardIsRenamedNotBound(t *testing.T) {
	src := `function t(): (i32, i32) { return (1, 2); }
function f(_: i32): i32 { return 0; }
function main(): i32 { var (a, _) = t(); var _ = 9; return a; }`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var names []string
	for _, fn := range prog.Funcs {
		for _, p := range fn.Params {
			names = append(names, p.Name)
		}
		for _, st := range fn.Body.Stmts {
			switch n := st.(type) {
			case *ast.Var:
				names = append(names, n.Name)
			case *ast.Destructure:
				names = append(names, n.Names...)
			}
		}
	}

	discards := 0
	for _, n := range names {
		if n == "_" {
			t.Fatalf("a binding named %q survived parsing; discards must be renamed (all: %v)", n, names)
		}
		if strings.HasPrefix(n, "__discard_") {
			discards++
		}
	}
	// One discarded parameter, one discarded destructure element, one
	// discarded plain binding.
	if discards != 3 {
		t.Fatalf("wanted 3 renamed discards, got %d (all: %v)", discards, names)
	}
}

// Each discard must get a DISTINCT name — renaming them all to one shared
// name would reintroduce the collision this fixes, and the parser test above
// would not notice because it only checks the prefix.
func TestDiscardNamesAreDistinct(t *testing.T) {
	prog, err := Parse(`function t(): (i32, i32) { return (1, 2); }
function main(): i32 { var (_, _) = t(); return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		for _, st := range fn.Body.Stmts {
			d, ok := st.(*ast.Destructure)
			if !ok {
				continue
			}
			for _, n := range d.Names {
				if seen[n] {
					t.Fatalf("two discards share the name %q", n)
				}
				seen[n] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("wanted 2 distinct discard names, got %d: %v", len(seen), seen)
	}
}
