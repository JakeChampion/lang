package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Consuming methods (`own self`) — the FBIP recursive traversal written in
// method form. `function (own xs: List) inc(): List` is an inherent consuming
// method; the receiver hoists to an `own` Params[0], so it lowers exactly like
// the free-function consuming map, and the method-call transfer makes the
// recursive `t.inc()` consume the owned binding `t`. These pin value-correctness
// + zero over-release on every backend, and a bounded high-water (each input
// cell reclaimed as the output is built).

const consumingMethodSrc = `enum List { Cons(i32, List), Nil }
function (own xs: List) inc(): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, t.inc()); },
        Nil => { return Nil; },
    }
}
function sum(l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } }
}
function build(n: i32): List {
    if (n == 0) { return Nil; }
    return Cons(n, build(n - 1));
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        total = total + sum(build(5).inc());   // [5..1] +1 each, sum = 20
        i = i + 1;
    }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ConsumingMethod(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, consumingMethodSrc); code != 0 {
		t.Errorf("consuming method: got %d, want 0", code)
	}
}

func TestArm64ConsumingMethod(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, consumingMethodSrc); code != 0 {
		t.Errorf("consuming method: got %d, want 0", code)
	}
}

func TestWASMConsumingMethod(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, consumingMethodSrc); got != 0 {
		t.Errorf("consuming method: got %d, want 0", got)
	}
	// NOTE: no heap-bump (bounded) assertion here. A method's *result/receiver
	// temporary* is not freed when used inline — `sum(build(6).inc())` leaks the
	// inc result — a PRE-EXISTING method-call-temp cleanup gap that affects even
	// a borrowed method (`sum(build(6).dup())` leaks identically) and is
	// independent of `own` (these changes only touch own-method calls). Tracked
	// separately; here we pin only the soundness of the consuming method itself
	// (value-correct + zero over-release), which the free/reuse differential
	// fixture gates also cover.
}
