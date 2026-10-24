package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestMatchArmCarriesItsResolution(t *testing.T) {
	const src = `enum A { Red, Green, Blue }
enum B { Blue, Red }

function a(x: A): i32 {
  match (x) {
    Red => { return 1; },
    Blue => { return 3; },
    _ => { return 0; },
  }
}

function b(y: B): i32 {
  let v: i32 = match (y) {
    Blue => 10,
    Red => 20,
  };
  return v;
}

function main(): i32 { return a(A.Blue) + b(B.Red); }
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}

	stmtWant := map[string]struct {
		enum string
		idx  int
	}{
		"Red":  {"A", 0},
		"Blue": {"A", 2},
	}
	exprWant := map[string]struct {
		enum string
		idx  int
	}{
		"Blue": {"B", 0},
		"Red":  {"B", 1},
	}

	var stmtSeen, exprSeen int
	for _, fn := range prog.Funcs {
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Match:
				for _, arm := range x.Arms {
					if arm.IsWildcard {
						if arm.EnumName != "" {
							t.Errorf("wildcard arm stamped %q", arm.EnumName)
						}
						continue
					}
					w, ok := stmtWant[arm.VariantName]
					if !ok {
						t.Errorf("unexpected stmt arm %q", arm.VariantName)
						continue
					}
					if arm.EnumName != w.enum || arm.VariantIndex != w.idx {
						t.Errorf("stmt arm %q: got %s#%d, want %s#%d",
							arm.VariantName, arm.EnumName, arm.VariantIndex, w.enum, w.idx)
					}
					stmtSeen++
				}
			case *ast.MatchExpr:
				for _, arm := range x.Arms {
					w, ok := exprWant[arm.VariantName]
					if !ok {
						t.Errorf("unexpected expr arm %q", arm.VariantName)
						continue
					}
					if arm.EnumName != w.enum || arm.VariantIndex != w.idx {
						t.Errorf("expr arm %q: got %s#%d, want %s#%d",
							arm.VariantName, arm.EnumName, arm.VariantIndex, w.enum, w.idx)
					}
					exprSeen++
				}
			}
			return true
		})
	}
	if stmtSeen != len(stmtWant) || exprSeen != len(exprWant) {
		t.Fatalf("visited %d stmt / %d expr arms, want %d / %d",
			stmtSeen, exprSeen, len(stmtWant), len(exprWant))
	}
}
