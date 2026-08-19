package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructConsumeRebindReclaimIRX86_64 covers the escaping-struct
// reclaim slice (#3456): a LOCAL struct that is threaded through a
// consume-rebind — `var s = S{...}; ... s = bump(s) ...` — is now reclaimable
// even though it appears as a call ARGUMENT / method RECEIVER, because
// reclaimable_names_of switched from the crude walk_stmts_escapes to the
// borrow-AWARE body_unsafe_for (a borrowable free-call arg and a method
// receiver count as borrows, not escapes — the same predicate the array
// reclaim already uses). The StmtAssign struct reassign now wires
// slot_is_reclaimable_struct into emit_arr_store's do_dec, so each rebind
// frees the previous box with a cow-guarded, rc-guarded SHALLOW arr_dec
// (box-only — a builder's shared field pointers keep rc==1 across the chain
// and are freed once at the final box's death).
//
// `s` is NOT returned (only a field is read out at the end), so it stays
// reclaimable; a returned builder would (correctly) flag unsafe and leak.
func TestSelfHostStructConsumeRebindReclaimIRX86_64(t *testing.T) {
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

	// CHURN: 4,000,000 consume-rebinds of a struct holding a heap (array) field.
	// Each `s = bump(s)` allocates a fresh box (+ a fresh xs array); without the
	// per-rebind reclaim the dead boxes accumulate and exhaust the bump heap
	// (exit 137). With the reclaim the box is freed each iteration, so it runs to
	// exit 0. `s` is read (s.n) but never returned, so it is reclaim-eligible.
	churn := `struct S { xs: i32[], n: i32 }
function bump(s: S): S { return S { xs: [s.n], n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [0], n: 0 };
    var i: i32 = 0;
    while (i < 4000000) { s = bump(s); i = i + 1; }
    return s.n - s.n;
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(churn))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the churn program")
	}
	// The per-rebind reclaim must be present (a bail, or no reclaim, emits
	// no box dec in main's loop). bump borrows its param + main's `s` is a
	// borrow-safe consume-rebind, so the reassign frees the old box.
	if frees := bytes.Count(asm, []byte("call __fn___fern_arr_dec")); frees < 1 {
		t.Errorf("found %d __fern_arr_dec calls, want >= 1 — consume-rebind struct box not reclaimed (module bailed or reclaim gate too strict)", frees)
	}
	churnBin := buildBin(t, gcc, dir, "struct_cr_churn", string(asm))
	var ccmd *exec.Cmd
	if len(runner) == 0 {
		ccmd = exec.Command(churnBin)
	} else {
		ccmd = exec.Command(runner[0], append(runner[1:], churnBin)...)
	}
	_ = ccmd.Run()
	if code := ccmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("consume-rebind churn exited %d, want 0 (137 = box leak: the per-rebind struct reclaim did not fire)", code)
	}

	// CORRECTNESS: the reclaim must not corrupt the live builder. Build a struct
	// up through consume-rebinds and read its array back — a use-after-free of a
	// wrongly-freed (shared) field buffer would corrupt the values.
	val := `struct B { xs: i32[], n: i32 }
function push(b: B, v: i32): B { return B { xs: b.xs.append(v), n: b.n + 1 }; }
function main(): i32 {
    var b: B = B { xs: [], n: 0 };
    var i: i32 = 0;
    while (i < 20) { b = push(b, i * 3); i = i + 1; }
    var sum: i32 = 0;
    var j: i32 = 0;
    while (j < b.xs.len()) { sum = sum + b.xs[j]; j = j + 1; }
    return sum;
}`
	vasm := runCapture(t, gcc, runner, driverBin, []byte(val))
	if len(vasm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the value program")
	}
	valBin := buildBin(t, gcc, dir, "struct_cr_val", string(vasm))
	var vcmd *exec.Cmd
	if len(runner) == 0 {
		vcmd = exec.Command(valBin)
	} else {
		vcmd = exec.Command(runner[0], append(runner[1:], valBin)...)
	}
	_ = vcmd.Run()
	// 3*(0+1+...+19) = 3*190 = 570; exit code is the low byte: 570 & 255 = 58.
	if code := vcmd.ProcessState.ExitCode(); code != 58 {
		t.Errorf("consume-rebind builder value = %d, want 58 (570 & 255) — reclaim corrupted the shared field buffer", code)
	}
}
