package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Consuming trait method (`own self` declared on the trait + impl) returning the
// Self enum — the FBIP map behind a trait bound. Needs `own`-aware
// conformance and no spurious enum-kind coherence mismatch (E021) to compile
// and run soundly on every backend.
const consumingTraitMethodSrc = `enum List { Cons(i32, List), Nil }
trait Mapper { function inc(own self: Self): List; }
impl Mapper for List {
    function inc(own self: Self): List {
        match (self) {
            Cons(h, t) => { return Cons(h + 1, t.inc()); },
            Nil => { return Nil; },
        }
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
    while (i < 200) { total = total + sum(build(5).inc()); i = i + 1; }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ConsumingTraitMethod(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, consumingTraitMethodSrc); code != 0 {
		t.Errorf("consuming trait method: got %d, want 0", code)
	}
}

func TestArm64ConsumingTraitMethod(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, consumingTraitMethodSrc); code != 0 {
		t.Errorf("consuming trait method: got %d, want 0", code)
	}
}

func TestWASMConsumingTraitMethod(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, consumingTraitMethodSrc); got != 0 {
		t.Errorf("consuming trait method: got %d, want 0", got)
	}
}
