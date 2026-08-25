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
// Any bare ident qualifies, because the append RETAINS an element whose source
// keeps a claim of its own and the buffer's element walk deep-drops the fields
// only under __fern_rc_is_unique. The two cases at the bottom are the ones that
// would double-free without that pairing — a box pushed on every iteration of a
// loop and read afterwards, and a source read after its push — so they pin the
// counted hand-off rather than a refusal.
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

	// The case that carries the whole safety argument: `v` is declared OUTSIDE the
	// loop, so the SAME box is pushed on every iteration and is read again after
	// it. Each push retains, so the buffer's walk decs the box once per element
	// and finds it shared every time; `v`'s own release is the one that reaches
	// rc 1 and walks the fields. Without the counted pairing the walk would
	// release that one box four times over and the post-loop read of `v.kids`
	// would see recycled memory — which is what the reads report as 90.
	{"outside-loop-elem-shared", `struct Val { kind: i32, kids: i32[] }
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

	// The push is not `v`'s last use, so nothing is moved and both owners are
	// live at the exit sweep. The retain is what makes that safe: whichever
	// release finds rc 1 walks `v.kids`, the other takes the box dec.
	{"read-after-push-shared", `struct Val { kind: i32, kids: i32[] }
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
