package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Phase 5h — loop-body local drops. A `var` re-declared inside a loop
// reuses ONE slot across iterations; before this slice the prior
// iteration's value was overwritten with no dec, so N-1 allocations
// leaked and the rc undercount kept the freelist from reclaiming them
// (unbounded growth in a hot build-and-discard loop). The dec-on-reinit
// (emitVarReinitDropOld) releases the slot's previous value, so each
// iteration's buffer is freed and the next same-size alloc reuses it.
//
// These sources are the soundness gate: each iteration's contribution
// sums to a known constant, so a reclaimed-then-reused buffer handed out
// while still live would drift the read-back and change the exit code.
// `__rc_underflow_count()` is folded in — it stays 0 only if no dec
// over-releases. 300 iterations so reclaim+reuse fires many times. The
// free-on/free-off differential gate (loop_var_reclaim fixture) pins the
// byte-identical-result property separately.

// Array loop-body var: row[0]+row[1]+row[2] - 6i == 0 each iteration.
const loopVarArrayReuseSrc = `function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var row: i32[] = [i, i * 2, i * 3];
        total = total + (row[0] + row[1] + row[2]) - (i * 6);
        i = i + 1;
    }
    return total + __rc_underflow_count();
}`

// Struct loop-body var: (p.y - p.x) - 1 == 0 each iteration.
const loopVarStructReuseSrc = `struct Pt { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var p: Pt = Pt { x: i, y: i + 1 };
        acc = acc + (p.y - p.x) - 1;
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

// Enum loop-body var with an array payload: head(b) - i == 0 each
// iteration. Exercises the enum dec path (flat rc_dec of the old box;
// the array payload leaks exactly as the baseline overwrite-dec does —
// rc-neutral, zero over-release surface, per slice 5e's analysis).
const loopVarEnumReuseSrc = `enum Box { Val(i32[]), Empty }
function head(b: Box): i32 {
    match (b) {
        Val(xs) => { return xs[0]; },
        Empty => { return 0; }
    }
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var b: Box = Val([i, i + 7]);
        acc = acc + head(b) - i;
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

// String loop-body var: a runtime `"v-" + suffix(i)` concat allocates a
// fresh heap string each iteration (the call defeats const-folding into a
// static literal). s.len() - 2 - suffix(i).len() == 0 each iteration. The
// prior buffer is reclaimed each iteration on ALL THREE backends (the
// str_dec / rc_dec path in emitOwnedSlotDrop — arm64 two-word heap strings
// now reclaim too, slice 5g done; the bounded-growth win is pinned by
// TestArm64LongStringReinitBounded), with the read-back + underflow staying
// correct.
const loopVarStringReuseSrc = `function suffix(n: i32): string {
    if (n % 2 == 0) { return "even"; }
    return "odd";
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var s: string = "v-" + suffix(i);
        acc = acc + s.len() - 2 - suffix(i).len();
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

var loopVarReuseCases = []struct {
	name string
	src  string
}{
	{"array", loopVarArrayReuseSrc},
	{"struct", loopVarStructReuseSrc},
	{"enum", loopVarEnumReuseSrc},
	{"string", loopVarStringReuseSrc},
}

func TestX86_64LoopVarReclaim(t *testing.T) {
	for _, c := range loopVarReuseCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s loop-body var free+reuse: got %d, want 0 (drift / over-release on the per-iteration dec)", c.name, code)
			}
		})
	}
}

func TestArm64LoopVarReclaim(t *testing.T) {
	for _, c := range loopVarReuseCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s loop-body var free+reuse: got %d, want 0 (drift / over-release)", c.name, code)
			}
		})
	}
}

func TestWASMLoopVarReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range loopVarReuseCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s loop-body var free+reuse: got %d, want 0 (drift / over-release)", c.name, got)
			}
		})
	}
}
