package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #4402 opt 3: the runtime contract for the two widened reuse pairings.
//
//   - CROSS-BRANCH sharing: one dead donor feeds a construction in EVERY arm
//     of a branch. The adversarial case is alternation — each arm must find a
//     usable box on the pass that takes it and must not double-free the box
//     the other arm did not take.
//   - CROSS-KIND pairing at equal box class: a dead tuple, and a dead enum,
//     each hand their box to a struct construction. The donor's OLD pointer
//     payload is released through the DONOR's layout before the recipient
//     stores its own fields, so a wrong layout here shows up as a leak (bump
//     growth) or a double free (rc underflow), not a wrong value.
//
// Every leg returns __rc_underflow_count() so a mis-balanced release fails
// the run rather than passing quietly.
const crossBranchReuseSrc = `struct Holder { n: i32, items: i32[] }
enum Wrapper { Wrap(i32[]) }

// Both arms construct a Holder; the single dead donor a feeds whichever runs.
function branch_step(i: i32): i32 {
    var a: Holder = Holder { n: i, items: [i, i + 1] };
    var s: i32 = a.n + a.items[0] + a.items[1];      // a's last use: 3i+1
    var acc: i32 = 0;
    if (i % 2 == 0) {
        var b: Holder = Holder { n: s, items: [s, 1] };
        acc = b.n + b.items[0] + b.items[1];          // 2s+1 = 6i+3
    } else {
        var c: Holder = Holder { n: s + 1, items: [s, 2] };
        acc = c.n + c.items[0] + c.items[1];          // 2s+3 = 6i+5
    }
    return acc;
}

// A dead TUPLE donates its box to a struct of the same class.
function tuple_step(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var s: i32 = t.0 + t.1[0] + t.1[1];              // t's last use: 3i+1
    var b: Holder = Holder { n: s, items: [s, 2] };
    return b.n + b.items[0] + b.items[1];             // 2s+2 = 6i+4
}

// A dead ENUM donates its box (tag + payload) to a struct of the same class.
function enum_step(i: i32): i32 {
    var a: Wrapper = Wrap([i, i + 1]);
    var s: i32 = match (a) { Wrap(xs) => xs[0] };     // a's last use: i
    var b: Holder = Holder { n: s, items: [s, 3] };
    return b.n + b.items[0] + b.items[1];             // 2s+3 = 2i+3
}

function main(): i32 {
    if (branch_step(0) != 3) { return 1; }
    if (branch_step(1) != 11) { return 2; }
    if (tuple_step(0) != 4) { return 3; }
    if (enum_step(0) != 3) { return 4; }

    // Warm the freelists, then run each shape 400 times and require the bump
    // high-water to stay put: a leaked donor box grows it linearly.
    var warm: i32 = branch_step(1) + tuple_step(1) + enum_step(1);
    var before: i32 = (__heap_bump_bytes() as i32);
    var br: i32 = 0;
    var tu: i32 = 0;
    var en: i32 = 0;
    var i: i32 = 0;
    while (i < 400) {
        br = br + branch_step(i);
        tu = tu + tuple_step(i);
        en = en + enum_step(i);
        i = i + 1;
    }
    var grew: i32 = (__heap_bump_bytes() as i32) - before;
    if (br != 480400) { return 5; }
    if (tu != 480400) { return 6; }
    if (en != 160800) { return 7; }
    if (grew != 0) { return 8; }
    if (warm < 0) { return 9; }
    return __rc_underflow_count();
}`

func TestInterpCrossBranchReuseValues(t *testing.T) {
	if got := runInterpExit(t, crossBranchReuseSrc); got != 0 {
		t.Errorf("cross-branch / cross-kind reuse on interp: got %d, want 0", got)
	}
}

func TestX86_64CrossBranchReuse(t *testing.T) {
	if out, code := compileAndRunX86_64FreeOn(t, crossBranchReuseSrc); code != 0 {
		t.Errorf("cross-branch / cross-kind reuse on x86-64: exit %d, want 0 (out %q)", code, out)
	}
}

func TestArm64CrossBranchReuse(t *testing.T) {
	if out, code := compileAndRunArm64FreeOn(t, crossBranchReuseSrc); code != 0 {
		t.Errorf("cross-branch / cross-kind reuse on arm64: exit %d, want 0 (out %q)", code, out)
	}
}

func TestWASMCrossBranchReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, crossBranchReuseSrc); got != 0 {
		t.Errorf("cross-branch / cross-kind reuse on wasm: got %d, want 0", got)
	}
}
