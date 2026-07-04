package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Simulated request-loop guard for the immutable-data RC model. Each "request"
// rebuilds a small state struct with a FRESH heap string payload and rebinds the
// state (`st = State { ...st, ... }`). That is the sanctioned replacement for
// post-construction field mutation, and it must stay reclaim-bounded: a future
// regression in replaced-field release (or a cycle-like leak reopening) would
// make the bump high-water grow with the iteration count instead of plateauing.

func requestLoopBumpSrc(n string) string {
	return `struct State { count: i32, last: string }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var st: State = State { count: 0, last: "seed-seed-seed" };
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var prefix: string = "A";
        if (i % 2 == 1) { prefix = "B"; }
        var body: string = prefix + "0123456789abcdef";
        st = State { ...st, count: st.count + 1, last: body };
        acc = acc + st.last.len();
        i = i + 1;
    }
    if (st.count != ` + n + `) { return 901; }
    if (acc == 0) { return 902; }
    return __heap_bump_bytes() - before;
}`
}

const requestLoopUnderflowSrc = `struct State { count: i32, last: string }
function main(): i32 {
    var st: State = State { count: 0, last: "seed-seed-seed" };
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var prefix: string = "A";
        if (i % 2 == 1) { prefix = "B"; }
        var body: string = prefix + "0123456789abcdef";
        st = State { ...st, count: st.count + 1, last: body };
        acc = acc + st.last.len();
        i = i + 1;
    }
    if (st.count != 200) { return 903; }
    if (st.last.len() != 17) { return 904; }
    if (acc != 3400) { return 905; }
    return __rc_underflow_count();
}`

func TestX86_64RequestLoopBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, requestLoopBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, requestLoopBumpSrc("5000"))
	if large > small {
		t.Errorf("request-loop bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, requestLoopUnderflowSrc); code != 0 {
		t.Errorf("request-loop reclaim: code=%d (903/904/905=value mismatch, >0=over-release)", code)
	}
}

func TestArm64RequestLoopBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, requestLoopBumpSrc("50"))
	large := mustRunArm64FreeOn(t, requestLoopBumpSrc("5000"))
	if large > small {
		t.Errorf("request-loop bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, requestLoopUnderflowSrc); code != 0 {
		t.Errorf("request-loop reclaim: code=%d", code)
	}
}

func TestWASMRequestLoopBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, requestLoopBumpSrc("50"))
	large := runWasm(t, requestLoopBumpSrc("5000"))
	if large > small {
		t.Errorf("request-loop bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, requestLoopUnderflowSrc); got != 0 {
		t.Errorf("request-loop reclaim: got %d", got)
	}
}
