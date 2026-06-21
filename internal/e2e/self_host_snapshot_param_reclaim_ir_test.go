package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostSnapshotParamReclaimIRX86_64 covers the snapshot-param reclaim
// (#3456 slice 2): the dominant builder pattern
// `function f(s: S): R { s = s.step(x); ...; return ... }`, where a struct PARAM
// (or method receiver) is threaded through a consume-rebind. Such a param is not
// reclaimable the normal way (params are caller-owned, and the final value may
// share the caller's fields), so lower_func snapshots the param's ENTRY box into
// a hidden `$snap$<name>` local and each reassign frees the old box only when it
// differs from BOTH the new value (cow) AND the snapshot — via the helper
// __fern_snapshot_dec(new, old, snap) -> new. A function thus reclaims its OWN
// intermediate builder boxes but never the caller's original.
//
// Two contracts:
//   - churn-no-OOM: a function threads a struct param hundreds of times; without
//     the per-rebind reclaim the dead boxes exhaust the bump heap (exit 137).
//   - caller-original-intact: the caller's struct, passed into a threading
//     function, is read AFTER the call — a wrong free of the snapshot corrupts it.
func TestSelfHostSnapshotParamReclaimIRX86_64(t *testing.T) {
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

	// CHURN: heavy() threads its struct param `c` 200x via a method receiver
	// (`c = c.step(k)`) and returns an i32 (not c). Without the per-rebind
	// snapshot reclaim the 200 intermediate boxes per call leak; 3M calls then
	// OOM (exit 137). With it they reclaim → exit 0. (Scalar struct, so the box is
	// the whole allocation — no array field to confound the measurement.)
	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function heavy(c: C): i32 {
    var k: i32 = 0;
    while (k < 200) { c = c.step(k); k = k + 1; }
    return c.n;
}
function main(): i32 {
    var f: i32 = 0;
    while (f < 3000000) {
        var seed: C = C { a: 0, n: 0 };
        var r: i32 = heavy(seed);
        f = f + 1 + (r - r);
    }
    return 0;
}`, "snap_param_churn", 0)

	// CALLER-ORIGINAL-INTACT: thread() reassigns its param `c` twice and returns
	// an i32; main reads `seed` AFTER the call. If the snapshot reclaim wrongly
	// freed the caller's box, seed.a would be corrupted. 5 + (5+10+20) = 40.
	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function thread(c: C): i32 { c = c.step(10); c = c.step(20); return c.a; }
function main(): i32 {
    var seed: C = C { a: 5, n: 0 };
    var t: i32 = thread(seed);
    return seed.a + t;
}`, "snap_param_caller_intact", 40)

	// HEAP-FIELD param builder threaded + RETURNED (`return e`): the param's final
	// value is moved out to the caller and read back; a wrong free of a shared
	// field buffer would corrupt the array. buf = [1,2,3] (sum 6) + n 3 = 9.
	run(t, `struct E { buf: i32[], n: i32 }
function (e: E) emit(v: i32): E { return E { buf: e.buf.append(v), n: e.n + 1 }; }
function build(e: E): E { e = e.emit(1); e = e.emit(2); e = e.emit(3); return e; }
function main(): i32 {
    var e: E = E { buf: [], n: 0 };
    e = build(e);
    var sum: i32 = 0; var j: i32 = 0;
    while (j < e.buf.len()) { sum = sum + e.buf[j]; j = j + 1; }
    return sum + e.n;
}`, "snap_param_heapfield", 9)
}
