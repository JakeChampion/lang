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
		{
			name: "nested FuncDecl body",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    function bump(x: i32): i32 { return id(x) + 1; }
    return bump(41);
}`,
		},
		{
			name: "Lambda body",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var f: (i32) => i32 = function (x: i32): i32 { return id(x) + 1; };
    return f(41);
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

// TestRunHandlesPartiallyInferredGenericCalls — variant
// constructors like `Ok(x)` and `Err(e)` only fix one of
// Result[T, E]'s two type parameters via their payload. The
// other has to come from the surrounding context (var-init
// annotation, function return slot).
//
// Before the destination-refinement work, the checker stamped
// the call's TypeArgs as `[Result{no args}]`, monomorph
// mangled to `pick__Result`, and the cloned param types were
// bare Result. The re-check rejected with "Result has 2 type
// parameter(s), 0 supplied".
//
// This test locks in the fix: each program type-checks and
// monomorphs cleanly. Langsmith's `skipGeneric` workaround in
// internal/langsmith/langsmith.go can come back to a simple
// flip once this is solid.
func TestRunHandlesPartiallyInferredGenericCalls(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "pick with Ok+Err args, Result destination annotation",
			src: `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    var r: Result[i32, i32] = pick(true, Ok(1), Err(2));
    return 0;
}`,
		},
		{
			name: "id with Ok arg, Result destination annotation",
			src: `function id[T](x: T): T { return x; }
function main(): i32 {
    var r: Result[i32, i32] = id(Ok(7));
    return 0;
}`,
		},
		{
			name: "pick with None+None args, Option destination annotation",
			src: `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    var o: Option[i32] = pick(true, None, None);
    return 0;
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
		})
	}
}
