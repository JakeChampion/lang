package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestDiscardsMayRepeatInOneScope(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"two tuple destructures", `function t(): (i32, i32) { return (1, 2); }
function main(): i32 { let (a, _) = t(); let (b, _) = t(); return a + b; }`},
		{"every element discarded", `function t(): (i32, i32) { return (1, 2); }
function main(): i32 { let (_, _) = t(); return 0; }`},
		{"discard in either position", `function t(): (i32, i32) { return (1, 2); }
function main(): i32 { let (a, _) = t(); let (_, b) = t(); return a + b; }`},
		{"repeated plain binding", `function main(): i32 { let _ = 5; let _ = 6; return 0; }`},
		{"repeated parameter", `function f(_: i32, _: i32): i32 { return 7; }
function main(): i32 { return f(1, 2); }`},
		{"repeated lambda parameter", `function main(): i32 {
    let f: (i32, i32) => i32 = (_: i32, _: i32) => 7;
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

func TestDiscardIsRenamedNotBound(t *testing.T) {
	src := `function t(): (i32, i32) { return (1, 2); }
function f(_: i32): i32 { return 0; }
function main(): i32 { let (a, _) = t(); let _ = 9; return a; }`
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
	if discards != 3 {
		t.Fatalf("wanted 3 renamed discards, got %d (all: %v)", discards, names)
	}
}

func TestDiscardNamesAreDistinct(t *testing.T) {
	prog, err := Parse(`function t(): (i32, i32) { return (1, 2); }
function main(): i32 { let (_, _) = t(); return 0; }`)
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
