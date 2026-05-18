package monomorph_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// TestRunRewritesGenericCallSitesInsideEveryExprShape locks in
// the walker's coverage of expression shapes that can host a
// generic Call. Earlier versions missed MapLit / FString /
// Assign — a generic call buried inside one of these would
// survive un-mangled through Run, and the trailing re-check
// would fail with "undefined identifier <generic-fn>".
func TestRunRewritesGenericCallSitesInsideEveryExprShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "MapLit value position",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { 1: id(42) };
    return 0;
}`,
		},
		{
			name: "MapLit key position",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var m: Map[i32, i32] = Map { id(1): 42 };
    return 0;
}`,
		},
		{
			name: "FString interpolant",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var s: string = f"hello {id(42)} world";
    return 0;
}`,
		},
		{
			name: "Assign rhs",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var n: i32 = 0;
    n = id(7);
    return n;
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, err := parser.Parse(c.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
			// Confirm the generic decl is gone and a mangled
			// clone took its place — that's the sign the
			// rewrite-and-clone cycle ran end-to-end.
			var sawClone, sawGeneric bool
			for _, fn := range prog.Funcs {
				if fn.Name == "id" {
					sawGeneric = true
				}
				if strings.HasPrefix(fn.Name, "id__") {
					sawClone = true
				}
			}
			if sawGeneric {
				t.Errorf("generic `id` survived in prog.Funcs after monomorph")
			}
			if !sawClone {
				t.Errorf("no `id__*` clone found after monomorph")
			}
		})
	}
}
