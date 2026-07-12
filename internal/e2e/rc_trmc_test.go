package e2e

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Tail-recursion-modulo-cons (ast.TrmcEnabled). A `map`-shaped function —
// `match (xs) { Cons(h,t) => Cons(g(h), self(t)), Nil => Nil }` — is not
// tail-recursive (the constructor wraps the recursive call), so ordinary
// lowering grows the stack O(n). TRMC rewrites it into a hole-passing loop:
// O(1) stack, single pass. These pin (1) value-correctness + no rc
// over-release on all three backends, (2) TRMC-on == TRMC-off byte-identical
// (the gate that makes the optimisation invisible), and (3) the actual O(1)
// stack win — a deep list that overflows without TRMC succeeds with it.

const trmcMapSrc = `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List {
    var acc: List = Nil;
    var i: i32 = 0;
    while (i < n) { acc = Cons(i, acc); i = i + 1; }   // [n-1, .., 1, 0]
    return acc;
}
function sum(l: List): i32 {
    var acc: i32 = 0;
    var cur: List = l;
    var go: boolean = true;
    while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } }
    return acc;
}
function main(): i32 {
    var ys: List = inc_all(build(50));   // sum(0..49) = 1225, +50 (one per elem) = 1275
    if (sum(ys) != 1275) { return 1; }
    return __rc_underflow_count();
}`

func TestX86_64Trmc(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, trmcMapSrc); code != 0 {
		t.Errorf("trmc map: got %d, want 0", code)
	}
}

func TestArm64Trmc(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, trmcMapSrc); code != 0 {
		t.Errorf("trmc map: got %d, want 0", code)
	}
}

func TestWASMTrmc(t *testing.T) {
	withTrmc(true, func() {
		if got := runWasm(t, trmcMapSrc); got != 0 {
			t.Errorf("trmc map: got %d, want 0", got)
		}
	})
}

// withTrmc runs fn with ast.TrmcEnabled forced to v, restoring it after. (The
// native compileAndRun*FreeOn helpers don't toggle it; these wrap codegen.)
func withTrmc(v bool, fn func()) {
	prev := ast.TrmcEnabled
	ast.TrmcEnabled = v
	defer func() { ast.TrmcEnabled = prev }()
	fn()
}

// --- TRMC-on == TRMC-off (the optimisation must be invisible) -------------

func TestX86_64TrmcMatchesNoTrmc(t *testing.T) {
	var on, off int
	withTrmc(true, func() { _, on = compileAndRunX86_64FreeOn(t, trmcMapSrc) })
	withTrmc(false, func() { _, off = compileAndRunX86_64FreeOn(t, trmcMapSrc) })
	if on != off || on != 0 {
		t.Errorf("TRMC on=%d off=%d, want both 0", on, off)
	}
}

func TestArm64TrmcMatchesNoTrmc(t *testing.T) {
	var on, off int
	withTrmc(true, func() { _, on = compileAndRunArm64FreeOn(t, trmcMapSrc) })
	withTrmc(false, func() { _, off = compileAndRunArm64FreeOn(t, trmcMapSrc) })
	if on != off || on != 0 {
		t.Errorf("TRMC on=%d off=%d, want both 0", on, off)
	}
}

func TestWASMTrmcMatchesNoTrmc(t *testing.T) {
	var on, off int
	withTrmc(true, func() { on = runWasm(t, trmcMapSrc) })
	withTrmc(false, func() { off = runWasm(t, trmcMapSrc) })
	if on != off || on != 0 {
		t.Errorf("TRMC on=%d off=%d, want both 0", on, off)
	}
}

// --- O(1) stack: deep list overflows without TRMC, succeeds with it -------

const trmcDeepSrc = `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) { Cons(h, t) => { return Cons(h + 1, inc_all(t)); }, Nil => { return Nil; } }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    if (sum(inc_all(build(300000))) != 600000) { return 1; }   // 300k elems, 1 -> 2 each
    return 0;
}`

// runWithStackLimit executes bin under an explicit RLIMIT_STACK soft limit
// (in KiB) and returns its exit code. The deep-stack contract is only
// meaningful inside a stack-size window: the TRMC-on leg still drops its
// 300k-deep result through the recursive __drop_enum_List glue (~10 MB of
// frames — drop specialisation hasn't loop-ified drop glue yet), while the
// TRMC-off leg's inc_all recursion needs ~24 MB. A host soft limit of 8 MB
// fails the "on" leg, unlimited passes the "off" leg — so pin 16 MB instead
// of inheriting whatever the host happens to use.
func runWithStackLimit(t *testing.T, kib int, bin string) int {
	t.Helper()
	cmd := exec.Command("bash", "-c", fmt.Sprintf("ulimit -S -s %d && exec \"$1\"", kib), "--", bin)
	if out, err := cmd.CombinedOutput(); err != nil && cmd.ProcessState == nil {
		t.Fatalf("run %s: %v\n%s", bin, err, out)
	}
	return cmd.ProcessState.ExitCode()
}

func TestX86_64TrmcDeepStack(t *testing.T) {
	var on, off int
	withTrmc(true, func() {
		bin, _ := compileX86_64FreeOn(t, trmcDeepSrc)
		on = runWithStackLimit(t, 16*1024, bin)
	})
	withTrmc(false, func() {
		bin, _ := compileX86_64FreeOn(t, trmcDeepSrc)
		off = runWithStackLimit(t, 16*1024, bin)
	})
	if on != 0 {
		t.Errorf("TRMC on: deep map should succeed, got %d", on)
	}
	if off == 0 {
		t.Errorf("TRMC off: deep map should overflow the stack, but got 0 (TRMC may not be the reason on succeeds)")
	}
}
