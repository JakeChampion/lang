package treeshake_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/treeshake"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

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

func TestShakeKeepsMethodReachableViaCall(t *testing.T) {
	src := `struct Point { x: i32 }
function (p: Point) getx(): i32 { return p.x; }
function dead(): i32 { return 9; }
function main(): i32 { let p = Point { x: 5 }; return p.getx(); }`
	names := runShake(t, src)
	if !hasName(names, "__method_Point_getx") {
		t.Errorf("method getx (mangled) should survive via the method call: %v", names)
	}
	if hasName(names, "dead") {
		t.Errorf("dead is unreferenced and should be dropped: %v", names)
	}
}

func TestShakeKeepsFunctionReachableOnlyViaClosure(t *testing.T) {
	src := `function target(): i32 { return 7; }
function main(): i32 {
    let x = 3;
    let f = (): i32 => { return target() + x; };
    return f();
}`
	names := runShake(t, src)
	if !hasName(names, "target") {
		t.Errorf("target should survive (referenced from a capturing closure body): %v", names)
	}
}

func TestShakeWasiHandleIsEntryPoint(t *testing.T) {
	src := `function onlyFromEntry(): i32 { return 7; }
function dead(): i32 { return 9; }
function ` + checker.WasiHandleName + `(): i32 { return onlyFromEntry(); }
function main(): i32 { return 0; }`
	names := runShake(t, src)
	for _, want := range []string{checker.WasiHandleName, "main", "onlyFromEntry"} {
		if !hasName(names, want) {
			t.Errorf("%s should survive (the wasi-http entry is an entry point): %v", want, names)
		}
	}
	if hasName(names, "dead") {
		t.Errorf("dead is reachable from neither main nor the entry and should be dropped: %v", names)
	}
}

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
