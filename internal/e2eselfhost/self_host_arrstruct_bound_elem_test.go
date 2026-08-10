package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrstructBoundElemCases pin the #6535 array-store element move: an element
// BOUND to a local before being pushed — `var v = Val { … }; vals =
// vals.append(v)` — must cost the same fresh memory as the identical value
// pushed inline. The append-built ARRSTRUCT credit (#6559) admitted only a
// literal element, so every bound push refused the credit and leaked the whole
// structure per round.
//
// The admission is the construction-move analysis, not the syntax: a bare-ident
// element qualifies exactly when the analysis names that source position, which
// means the local is an owned rc local at its LAST USE and was declared inside
// the loop body doing the pushing. The two refusal cases below are the ones that
// would double-free if it were widened to "any bare ident".
//
// Exit codes match the sibling reclaim suites: 90 = a live buffer was freed and
// reused underneath its owner, 91/93 = wrong value, 92 = fresh bytes grew where
// the shape must be flat, 99 = over-release/underflow.
var arrstructBoundElemCases = []struct {
	name string
	src  string
	want int
}{
	// The accumulator loop. Leaked 35368 B over 50 rounds, exactly doubling,
	// against 872 / 0 for the same value pushed inline.
	{"loop-bound-elem", `struct Val { kind: i32, kids: i32[] }
function bnd(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        var v: Val = Val { kind: i, kids: [i, i + 1] };
        vals = vals.append(v);
        total = total + vals.len();
    }
    return total;
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    for i in 0..n { acc = acc + bnd(8); }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1800) { return 91; }
    if (y != 3600) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The straight-line shape, which the top-level construction-move walk already
	// reached — kept as the regression guard for it, since the loop-body pass
	// rewrites the scan the straight-line case shares.
	{"straightline-bound-elem", `struct Val { kind: i32, kids: i32[] }
function line(k: i32): i32 {
    var vals: Val[] = [];
    var v: Val = Val { kind: k, kids: [k] };
    vals = vals.append(v);
    return vals.len();
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    for i in 0..n { acc = acc + line(i); }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 50) { return 91; }
    if (y != 100) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The inline element, which needs no move at all. It reclaimed before this
	// change and must keep reclaiming after it.
	{"inline-elem-still-flat", `struct Val { kind: i32, kids: i32[] }
function inl(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        vals = vals.append(Val { kind: i, kids: [i, i + 1] });
        total = total + vals.len();
    }
    return total;
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    for i in 0..n { acc = acc + inl(8); }
    return acc;
}
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds(50);
    var b1: i64 = __heap_bump_bytes();
    var y: i32 = rounds(100);
    var b2: i64 = __heap_bump_bytes();
    if (x != 1800) { return 91; }
    if (y != 3600) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The refusal that carries the whole safety argument: `v` is declared OUTSIDE
	// the loop, so the SAME box is pushed on every iteration. Admitting it would
	// let the buffer's deep walk release that one box four times over, and the
	// post-loop read of `v.kids` would then read recycled memory. The
	// declared-in-this-body gate is what refuses it; the reads below report 90 if
	// it ever stops.
	{"outside-loop-elem-refused", `struct Val { kind: i32, kids: i32[] }
function haz(n: i32): i32 {
    var v: Val = Val { kind: 7, kids: [7, 8] };
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        vals = vals.append(v);
        total = total + vals.len();
    }
    return total + v.kids[0] + v.kids[1];
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    for i in 0..n { acc = acc + haz(4); }
    return acc;
}
function main(): i32 {
    var x: i32 = rounds(50);
    var y: i32 = rounds(100);
    if (x != 1250) { return 90; }
    if (y != 2500) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	// The other refusal: `v` is READ after the push, so the push is not its last
	// use and the analysis does not name the site. The buffer's deep walk would
	// otherwise free `v.kids` before the read.
	{"read-after-push-refused", `struct Val { kind: i32, kids: i32[] }
function after(k: i32): i32 {
    var vals: Val[] = [];
    var v: Val = Val { kind: k, kids: [k] };
    vals = vals.append(v);
    return vals.len() + v.kids[0];
}
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    for i in 0..n { acc = acc + after(i); }
    return acc;
}
function main(): i32 {
    var x: i32 = rounds(50);
    var y: i32 = rounds(100);
    if (x != 1275) { return 90; }
    if (y != 5050) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostArrStructBoundElemX86_64 drives the cases through the self-hosted
// x86-64 compiler.
func TestSelfHostArrStructBoundElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range arrstructBoundElemCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (90 = a live buffer was freed and reused; "+
					"91/93 = wrong value; 92 = fresh bytes grew where the shape must be flat; "+
					"99 = over-release/underflow)", tc.name, code, tc.want)
			}
		})
	}
}
