package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// detectTrmc admits more than the canonical `match (xs) { Cons(h,t) =>
// Cons(g(h), self(t)), Nil => Nil }` (#5344): setup statements before the
// match, statements before an arm's tail, `when` guards, a `_` arm, a tail
// `if`/`else`, a bare tail self-call, and a hole in a non-last payload. What it
// must NOT admit is anything whose lowering would register an rc obligation —
// the loop returns through its own `return`, which bypasses the exit sweep —
// or an arm chain the guards can exhaust, which re-enters the loop with the
// scrutinee unchanged (a hang).
//
// These pin the DETECTOR. End-to-end value-correctness and TRMC-on ==
// TRMC-off for the same shapes is internal/e2e/rc_trmc_widen_test.go.

const trmcListDecl = `enum List { Cons(i32, List), Nil }
`

// trmcVerdicts runs the real pre-lowering pipeline and returns the TRMC and
// consume-safe function sets.
func trmcVerdicts(t *testing.T, src string) (trmc, consumeSafe map[string]bool) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	const ptrW = 8
	pairForm := findPairFormFuncs(prog, info, ptrW, addressTakenFuncs(prog, info))
	return findTrmcFuncs(prog, info, ptrW, pairForm)
}

func trmcFires(t *testing.T, src, fn string) bool {
	t.Helper()
	trmc, _ := trmcVerdicts(t, src)
	return trmc[fn]
}

func TestDetectTrmcWidenedShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"setup statements before the match", `
function f(xs: List, n: i32): List {
    var lim: i32 = n * 2;
    if (lim <= 0) { return Nil; }
    match (xs) {
        Cons(h, t) => { return Cons(h + lim, f(t, n - 1)); },
        Nil => { return Nil; },
    }
}`},
		{"statements before an arm tail", `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { var d: i32 = h * 2; return Cons(d + 1, f(t)); },
        Nil => { return Nil; },
    }
}`},
		{"guarded arm with an unguarded sibling", `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) when h < 0 => { return Cons(0 - h, f(t)); },
        Cons(h, t) => { return Cons(h, f(t)); },
        Nil => { return Nil; },
    }
}`},
		{"wildcard base arm", `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, f(t)); },
        _ => { return Nil; },
    }
}`},
		{"tail if/else with a bare self-call leaf", `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return f(t); } else { return Cons(h, f(t)); } },
        Nil => { return Nil; },
    }
}`},
		{"guard clause inside an arm body", `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h == 0) { return Nil; } return Cons(h, f(t)); },
        Nil => { return Nil; },
    }
}`},
		{"hole in a non-last payload", `
enum Rev { Node(Rev, i32), End }
function f(xs: List): Rev {
    match (xs) {
        Cons(h, t) => { return Node(f(t), h + 1); },
        Nil => { return End; },
    }
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !trmcFires(t, trmcListDecl+tc.body+"\nfunction main(): i32 { return 0; }", "f") {
				t.Errorf("detectTrmc declined %s", tc.name)
			}
		})
	}
}

func TestDetectTrmcDeclines(t *testing.T) {
	cases := []struct {
		name string
		why  string
		body string
	}{
		{"rc-bearing local in an arm body",
			"the loop's own return bypasses the exit sweep, so the local would leak",
			`
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { var s: string = "x"; return Cons(h + s.len(), f(t)); },
        Nil => { return Nil; },
    }
}`},
		{"rc-bearing local before the match",
			"same: no exit sweep to discharge it",
			`
function f(xs: List): List {
    var s: string = "x";
    match (xs) {
        Cons(h, t) => { return Cons(h + s.len(), f(t)); },
        Nil => { return Nil; },
    }
}`},
		{"statements after the match",
			"the loop's `return` is the function's only exit, so nothing may follow it",
			`
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, f(t)); },
        Nil => { return Nil; },
    }
    return Nil;
}`},
		{"unclassifiable branch of a tail if/else",
			"one leaf binds an rc local, which the loop's exit would never drop",
			`
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => {
            if (h > 0) { return Cons(h, f(t)); }
            else { var s: string = "xy"; return Cons(s.len(), f(t)); }
        },
        Nil => { return Nil; },
    }
}`},
		{"loop before the match",
			"only scalar straight-line setup is rc-neutral by construction",
			`
function f(xs: List, n: i32): List {
    var i: i32 = 0;
    while (i < n) { i = i + 1; }
    match (xs) {
        Cons(h, t) => { return Cons(h + i, f(t, n)); },
        Nil => { return Nil; },
    }
}`},
		{"self-call buried in a payload expression",
			"the hole must BE a payload, not a subexpression of one",
			`
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h, Cons(h, f(t))); },
        Nil => { return Nil; },
    }
}`},
		{"two self-calls",
			"tree-shaped: two holes",
			`
enum Tree { Fork(Tree, Tree), Leaf }
function g(xs: Tree): Tree {
    match (xs) {
        Fork(l, r) => { return Fork(g(l), g(r)); },
        Leaf => { return Leaf; },
    }
}`},
		{"only tail self-calls",
			"plainly tail-recursive, not modulo-cons",
			`
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { return f(t); },
        Nil => { return Nil; },
    }
}`},
		{"@ binding",
			"the arm binds payloads by hand with nowhere to put the whole-value name",
			`
function f(xs: List): List {
    match (xs) {
        whole @ Cons(h, t) => { return Cons(h, f(whole)); },
        Nil => { return Nil; },
    }
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := trmcListDecl + tc.body + "\nfunction main(): i32 { return 0; }"
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
			for _, fn := range []string{"f", "g"} {
				trmc, _ := trmcVerdicts(t, src)
				if trmc[fn] {
					t.Errorf("detectTrmc admitted %s — %s", tc.name, tc.why)
				}
			}
		})
	}
}

// The TRMC-consuming cell recycling stays scoped to the narrow shape: it
// releases a cell only on the advance, so any path that leaves the loop early
// walks away from cells the caller already retained for us.
func TestTrmcConsumeStaysNarrow(t *testing.T) {
	pobd := ast.OwnedByDefault
	defer func() { ast.OwnedByDefault = pobd }()
	ast.OwnedByDefault = true

	const narrow = trmcListDecl + `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, f(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`
	if _, safe := trmcVerdicts(t, narrow); !safe["f"] {
		t.Error("the canonical list map must stay consume-safe")
	}

	widened := map[string]string{
		"guarded arm": `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) when h < 0 => { return Cons(0 - h, f(t)); },
        Cons(h, t) => { return Cons(h, f(t)); },
        Nil => { return Nil; },
    }
}`,
		"early return before the loop": `
function f(xs: List, n: i32): List {
    if (n <= 0) { return Nil; }
    match (xs) {
        Cons(h, t) => { return Cons(h, f(t, n - 1)); },
        Nil => { return Nil; },
    }
}`,
		"branch tail with a base leaf": `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return Nil; } else { return Cons(h, f(t)); } },
        Nil => { return Nil; },
    }
}`,
		"wildcard arm": `
function f(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, f(t)); },
        _ => { return Nil; },
    }
}`,
	}
	for name, body := range widened {
		t.Run(name, func(t *testing.T) {
			src := trmcListDecl + body + "\nfunction main(): i32 { return 0; }"
			trmc, safe := trmcVerdicts(t, src)
			if !trmc["f"] {
				t.Fatalf("%s should still lower via TRMC", name)
			}
			if safe["f"] {
				t.Errorf("%s must not be consume-safe — the loop frees only on the advance", name)
			}
		})
	}

	// Scalar setup that cannot leave the loop early keeps the consume verdict.
	const scalarSetup = trmcListDecl + `
function f(xs: List, n: i32): List {
    var bump: i32 = n + 1;
    match (xs) {
        Cons(h, t) => { return Cons(h + bump, f(t, n)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`
	if _, safe := trmcVerdicts(t, scalarSetup); !safe["f"] {
		t.Error("scalar setup before the match must keep the consume verdict")
	}
}

// The arm chain has to stay total WITHOUT its guarded arms: TRMC's loop has no
// fall-through-to-join, so falling off the last arm re-enters the loop with the
// scrutinee unchanged — a hang, not a wrong answer. The checker's exhaustiveness
// rule (a guarded arm covers nothing) means no source program reaches the gate
// today, which is exactly why it is pinned here rather than left to that rule.
func TestTrmcArmsTotalNeedsUnguardedCoverage(t *testing.T) {
	prog, err := parser.Parse(trmcListDecl + "function main(): i32 { return 0; }")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	b := &builder{info: info, fn: prog.Funcs[0]}
	list := ast.EnumType{Name: "List"}

	guardedCons := trmcArm{scrutVarIdx: 0, guard: &ast.BoolLit{Value: true}}
	plainCons := trmcArm{scrutVarIdx: 0}
	plainNil := trmcArm{scrutVarIdx: 1}
	wildcard := trmcArm{isWildcard: true}

	if b.trmcArmsTotal(&trmcShape{arms: []trmcArm{guardedCons, plainNil}}, list) {
		t.Error("a variant reachable only through a guarded arm must decline TRMC")
	}
	if !b.trmcArmsTotal(&trmcShape{arms: []trmcArm{guardedCons, plainCons, plainNil}}, list) {
		t.Error("an unguarded sibling covering the same variant makes the chain total")
	}
	if !b.trmcArmsTotal(&trmcShape{arms: []trmcArm{guardedCons, wildcard}}, list) {
		t.Error("an unguarded wildcard covers every variant")
	}
}
