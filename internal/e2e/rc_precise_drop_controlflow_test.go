package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Slice 1 of docs/OWNERSHIP-INFERENCE-PLAN.md: a string/array-free struct/tuple
// local whose last use sits inside an if/while/for/match now takes a precise
// deep-drop right after that statement (control-flow placement) instead of the
// function-exit sweep. Correctness is the bar in this sound-critical path — the
// fixture differential gate (free-on == free-off) is the corpus-wide net; these
// pin value + zero over-release on the exact shape across all backends.

const preciseCfSrc = `struct P { x: i32, y: i32 }
struct Pair { a: P, b: P }
function add(p: P): i32 { return p.x + p.y; }
function pairsum(q: Pair): i32 { return add(q.a) + add(q.b); }
function f(n: i32): i32 {
    var q: Pair = Pair { a: P { x: n, y: 1 }, b: P { x: 2, y: 3 } };
    var c: i32 = 0;
    if (n > 0) { c = pairsum(q); }   // q's last use is inside the if
    var t: (i32, i32) = (n, n + 1);
    var d: i32 = 0;
    if (n > 0) { d = t.0 + t.1; }    // tuple last use inside an if
    return c + d;
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { total = total + f(5); i = i + 1; }
    if (total != 100 * (5 + 1 + 2 + 3 + 5 + 6)) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64PreciseDropControlFlow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, preciseCfSrc); code != 0 {
		t.Errorf("precise control-flow drop: got %d, want 0", code)
	}
}

func TestArm64PreciseDropControlFlow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, preciseCfSrc); code != 0 {
		t.Errorf("precise control-flow drop: got %d, want 0", code)
	}
}

func TestWASMPreciseDropControlFlow(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, preciseCfSrc); got != 0 {
		t.Errorf("precise control-flow drop: got %d, want 0", got)
	}
}
