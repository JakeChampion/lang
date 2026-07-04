package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFieldReclaimIRX86_64 covers field-level move tracking (#3457): the
// per-type __field_reclaim_<T> helper that frees a superseded builder box's
// REPLACED array-field buffers before freeing the box. This converges the
// dominant clone-form leak — `LowerState { ops: s.ops.append(op), … }` clones
// `s.ops` each emit, so the dead SOURCE buffer leaks O(K^2)/function without it.
//
// A struct PARAM threaded through a consume-rebind snapshots its entry box; each
// rebind frees the old box (snapshot-guarded) AND, now, each replaced array
// field that differs from BOTH the new value (cow) and the caller's snapshot.
// The local consume-rebind (`c = bump(c)`) variant uses just the != new guard.
func TestSelfHostFieldReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog string, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// CHURN (local consume-rebind, clone-form array field): a fresh struct LOCAL
	// `b` with an i32[] field is threaded via the immutable-update method
	// `b = b.emit(f)` (each builds `B { ops: b.ops.append(f), n: b.n+1 }`, a fresh
	// growing clone of the ops buffer), bounded by `b = b.clear()` once it reaches
	// 100 elements, for 200M iterations. Without field-level reclaim EVERY rebind's
	// dead ops clone (and every cleared builder's buffer) leaks — O(iterations) of
	// growing buffers exhaust the heap (exit 137; verified on origin/main). With
	// __field_reclaim_B freeing each replaced ops buffer the freed blocks recycle
	// through the size-class freelist → bounded → exit 0. The per-module-
	// convergence repro shape (#3457): a builder's replaced array fields reclaim.
	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function (b: B) clear(): B { return B { ops: [], n: 0 }; }
function churn(): i32 {
    var b: B = B { ops: [], n: 0 };
    var f: i32 = 0;
    while (f < 200000000) {
        b = b.emit(f);
        if (b.n >= 100) { b = b.clear(); }
        f = f + 1;
    }
    return b.n - b.n;
}
function main(): i32 { return churn(); }`, "field_reclaim_churn", 0)

	// CHURN (snapshot-param): the same bounded clone-form threading, but on a
	// struct PARAM (snapshot-reclaim path) — intermediate dead clones free against
	// the != new AND != caller-snapshot guards. Without it the per-call dead clones
	// leak across 3M calls → 137; with it → bounded → 0. The final box is cleared
	// to [] before return so the (un-swept) snapshot-param final box leaks only a
	// tiny empty buffer, isolating the intermediate-clone reclaim under test.
	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function (b: B) clear(): B { return B { ops: [], n: 0 }; }
function thread(b: B): i32 {
    var f: i32 = 0;
    while (f < 100) { b = b.emit(f); if (b.n >= 30) { b = b.clear(); } f = f + 1; }
    var r: i32 = b.n;
    b = b.clear();
    return r - r;
}
function main(): i32 {
    var g: i32 = 0;
    while (g < 3000000) {
        var seed: B = B { ops: [], n: 0 };
        g = g + 1 + thread(seed);
    }
    return 0;
}`, "field_reclaim_snap_churn", 0)

	// VALUE-CORRECTNESS: the builder is read back after threading — a wrong free of
	// a LIVE (shared / cow) field buffer would corrupt the array. The param's final
	// value is moved out (`return b`) and summed. ops = [10,20,30] (sum 60) + n 3 = 63.
	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function build(b: B): B { b = b.emit(10); b = b.emit(20); b = b.emit(30); return b; }
function main(): i32 {
    var b: B = B { ops: [], n: 0 };
    b = build(b);
    var sum: i32 = 0; var j: i32 = 0;
    while (j < b.ops.len()) { sum = sum + b.ops[j]; j = j + 1; }
    return sum + b.n;
}`, "field_reclaim_value", 63)

	// CALLER-ORIGINAL-INTACT: thread() rebinds its param `b` (replacing the ops
	// field) and returns an i32; main reads `seed.ops` AFTER the call. If the
	// field reclaim wrongly freed the caller's original ops buffer (the snapshot),
	// seed.ops would be corrupted / reused. seed.ops = [7] (sum 7) + thread's 2 = 9.
	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function thread(b: B): i32 { b = b.emit(1); b = b.emit(2); return b.n; }
function main(): i32 {
    var seed: B = B { ops: [7], n: 0 };
    var t: i32 = thread(seed);
    var sum: i32 = 0; var j: i32 = 0;
    while (j < seed.ops.len()) { sum = sum + seed.ops[j]; j = j + 1; }
    return sum + t;
}`, "field_reclaim_caller_intact", 9)

	// LOCAL consume-rebind (no snapshot — the != new guard only): a fresh,
	// non-escaping struct LOCAL with an array field is threaded via `c = step(c)`
	// in a churn loop. Each rebind's old box + its replaced ops buffer reclaim.
	run(t, `struct B { ops: i32[], n: i32 }
function step(c: B): B { return B { ops: c.ops.append(c.n), n: c.n + 1 }; }
function main(): i32 {
    var f: i32 = 0;
    while (f < 2000000) {
        var c: B = B { ops: [], n: 0 };
        var k: i32 = 0;
        while (k < 100) { c = step(c); k = k + 1; }
        f = f + 1 + (c.n - c.n);
    }
    return 0;
}`, "field_reclaim_local_churn", 0)
}
