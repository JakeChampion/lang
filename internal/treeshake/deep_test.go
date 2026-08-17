package treeshake_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/treeshake"
)

// These tests deepen reachability coverage beyond the basics in
// treeshake_test.go. They reuse the runShake / hasName helpers
// defined there (same package).

// TestShakeTransitiveChain — a four-deep call chain
// (main→a→b→c) keeps every link alive, while an unreferenced
// leaf `d` parallel to the chain is dropped. The basic
// TestShakeKeepsCalledFunctions only goes two deep and has no
// distractor function to confirm pruning still happens.
func TestShakeTransitiveChain(t *testing.T) {
	src := `function d(): i32 { return 4; }
function c(): i32 { return 3; }
function b(): i32 { return c(); }
function a(): i32 { return b(); }
function main(): i32 { return a(); }`
	names := runShake(t, src)
	for _, want := range []string{"a", "b", "c", "main"} {
		if !hasName(names, want) {
			t.Errorf("%s should survive the transitive chain: %v", want, names)
		}
	}
	if hasName(names, "d") {
		t.Errorf("d is unreferenced and should be dropped: %v", names)
	}
}

// TestShakeDiamondGraph — a diamond call graph
// (main→{a,b}, both a and b call c) keeps c alive exactly once
// and does not corrupt the program by double-processing the
// shared callee. Confirms the BFS `reachable` guard against
// re-enqueueing a node already visited from another path.
func TestShakeDiamondGraph(t *testing.T) {
	src := `function c(): i32 { return 3; }
function a(): i32 { return c(); }
function b(): i32 { return c(); }
function main(): i32 { return a() + b(); }`
	names := runShake(t, src)
	for _, want := range []string{"a", "b", "c", "main"} {
		if !hasName(names, want) {
			t.Errorf("%s should survive the diamond: %v", want, names)
		}
	}
	// c kept exactly once — count occurrences directly.
	count := 0
	for _, n := range names {
		if n == "c" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("shared callee c should survive exactly once, got %d: %v", count, names)
	}
}

// TestShakeMutualRecursionReachable — two mutually-recursive
// functions (isEven↔isOdd) reached from main both survive.
// Confirms the visited-set prevents the BFS from looping
// forever on the cycle while still keeping both nodes.
func TestShakeMutualRecursionReachable(t *testing.T) {
	src := `function isEven(n: i32): boolean { if (n == 0) { return true; } return isOdd(n - 1); }
function isOdd(n: i32): boolean { if (n == 0) { return false; } return isEven(n - 1); }
function main(): i32 { if (isEven(4)) { return 1; } return 0; }`
	names := runShake(t, src)
	for _, want := range []string{"isEven", "isOdd", "main"} {
		if !hasName(names, want) {
			t.Errorf("%s should survive (mutually recursive, reachable from main): %v", want, names)
		}
	}
}

// TestShakeMutualRecursionIsland — a mutually-recursive pair
// (ping↔pong) that nothing reachable references is dropped
// entirely, even though they reference each other. A live
// main calling an unrelated `live` keeps the entry-point
// fallback from kicking in (so the island isn't kept by the
// "no entry point" rule). Confirms internal cycles do not make
// dead code look reachable.
func TestShakeMutualRecursionIsland(t *testing.T) {
	src := `function ping(n: i32): i32 { return pong(n); }
function pong(n: i32): i32 { return ping(n); }
function live(): i32 { return 1; }
function main(): i32 { return live(); }`
	names := runShake(t, src)
	if !hasName(names, "live") || !hasName(names, "main") {
		t.Errorf("live + main should survive: %v", names)
	}
	if hasName(names, "ping") || hasName(names, "pong") {
		t.Errorf("ping/pong are an unreferenced mutually-recursive island and should be dropped: %v", names)
	}
}

// TestShakeKeepsMethodReachableViaCall — a function reachable
// only through a method call survives. After type-checking, the
// method `(p: Point) getx()` is a top-level func mangled to
// `__method_Point_getx`, and `p.getx()` resolves to a call of
// that mangled name. An unrelated `dead` function is dropped.
// Confirms method dispatch participates in reachability.
func TestShakeKeepsMethodReachableViaCall(t *testing.T) {
	src := `struct Point { x: i32 }
function (p: Point) getx(): i32 { return p.x; }
function dead(): i32 { return 9; }
function main(): i32 { var p = Point { x: 5 }; return p.getx(); }`
	names := runShake(t, src)
	if !hasName(names, "__method_Point_getx") {
		t.Errorf("method getx (mangled) should survive via the method call: %v", names)
	}
	if hasName(names, "dead") {
		t.Errorf("dead is unreferenced and should be dropped: %v", names)
	}
}

// TestShakeKeepsFunctionReachableOnlyViaClosure — a top-level
// function referenced only from inside a closure body that also
// captures a local survives. Extends TestShakeWalksLambdaBody by
// adding a capture (`x`) so the lambda is a genuine closure, not
// just an anonymous function, confirming the Lambda walk fires
// regardless of captures.
func TestShakeKeepsFunctionReachableOnlyViaClosure(t *testing.T) {
	src := `function target(): i32 { return 7; }
function main(): i32 {
    var x = 3;
    var f = function (): i32 { return target() + x; };
    return f();
}`
	names := runShake(t, src)
	if !hasName(names, "target") {
		t.Errorf("target should survive (referenced from a capturing closure body): %v", names)
	}
}

// TestShakeHandleIsEntryPoint — `handle` is an entry point
// independent of `main`. A function reachable only from `handle`
// survives even though main never references it, while an
// unreferenced `dead` is dropped. Pairs handle with a trivial
// main so the program type-checks without the HTTP-server
// runtime path.
func TestShakeHandleIsEntryPoint(t *testing.T) {
	src := `function onlyFromHandle(): i32 { return 7; }
function dead(): i32 { return 9; }
function handle(): i32 { return onlyFromHandle(); }
function main(): i32 { return 0; }`
	names := runShake(t, src)
	for _, want := range []string{"handle", "main", "onlyFromHandle"} {
		if !hasName(names, want) {
			t.Errorf("%s should survive (handle is an entry point): %v", want, names)
		}
	}
	if hasName(names, "dead") {
		t.Errorf("dead is reachable from neither main nor handle and should be dropped: %v", names)
	}
}

// TestShakeExtraSeedsTransitiveClosure — a name passed in
// `extras` is kept alive AND its own callees are walked, so a
// function reachable only through an extra-seeded function also
// survives. Extends TestShakeHonoursExtras (which only checks the
// directly-named extra) by asserting the BFS continues from the
// seed.
func TestShakeExtraSeedsTransitiveClosure(t *testing.T) {
	src := `function dep(): i32 { return 1; }
function seeded(): i32 { return dep(); }
function main(): i32 { return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	treeshake.Run(prog, info, "seeded")
	names := make([]string, 0, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		names = append(names, fn.Name)
	}
	for _, want := range []string{"seeded", "dep", "main"} {
		if !hasName(names, want) {
			t.Errorf("%s should survive (extra seed + its transitive callee): %v", want, names)
		}
	}
}
