package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestSelfHostSnapshotParamReclaimArm64 is the arm64 mirror: __fern_snapshot_dec
// is now a REAL rc-guarded shallow free on arm64 (previously a leak-safe
// pass-through). The heavy OOM-churn is x86-only (it needs ~240M allocations to
// exhaust the bump heap, far too slow under qemu); arm64 is verified by
// CORRECTNESS — a wrong free of the caller's snapshot or a shared field buffer
// corrupts the result — plus an asm-content check that the real freelist-push
// body is emitted (the pass-through had no `str x5, [x6, x4, lsl #3]`).
func TestSelfHostSnapshotParamReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantFree bool) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		// The real snapshot_dec body ends with the freelist push `str x5, [x6,
		// x4, lsl #3]`; the old leak-safe pass-through (`ldr x0,[sp,#32]; ret`)
		// had no such store. Proves the reclaim body — not the stub — is emitted.
		if wantFree && !strings.Contains(string(asm), "str x5, [x6, x4, lsl #3]") {
			t.Errorf("%s: real __fern_snapshot_dec free body not found in arm64 asm (still the pass-through?)", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// CALLER-ORIGINAL-INTACT: a wrong free of the snapshot corrupts seed.a. 40.
	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function thread(c: C): i32 { c = c.step(10); c = c.step(20); return c.a; }
function main(): i32 {
    var seed: C = C { a: 5, n: 0 };
    var t: i32 = thread(seed);
    return seed.a + t;
}`, "snap_param_caller_intact_arm64", 40, true)

	// SCALAR-STRUCT CHURN: threads a scalar struct param 50x via a method
	// receiver (`c = c.step(k)`), so __fern_snapshot_dec frees 50 intermediate
	// boxes; returns an i32 (not c). A double-free / wrong free across the 50
	// rebinds would crash or corrupt. sum(0..49) = 1225, so build == 1225 and
	// the program returns 7. (A scalar struct avoids the per-type field-reclaim
	// deep-drop helper, which is a separate unfinished arm64 slice — a RETURNED
	// heap-field builder there link-errors on `__field_reclaim_<T>`, unrelated to
	// snapshot_dec.)
	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function build(c: C): i32 {
    var k: i32 = 0;
    while (k < 50) { c = c.step(k); k = k + 1; }
    return c.a;
}
function main(): i32 {
    var seed: C = C { a: 0, n: 0 };
    return build(seed) - 1218;
}`, "snap_param_scalar_churn_arm64", 7, true)
}
