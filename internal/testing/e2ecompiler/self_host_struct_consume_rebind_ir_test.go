package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructConsumeRebindReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	churn := `struct S { xs: i32[], n: i32 }
function bump(s: S): S { return S { xs: [s.n], n: s.n + 1 }; }
function main(): i32 {
    let s: S = S { xs: [0], n: 0 };
    let i: i32 = 0;
    while (i < 4000000) { s = bump(s); i = i + 1; }
    return s.n - s.n;
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(churn))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the churn program")
	}
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

	val := `struct B { xs: i32[], n: i32 }
function push(b: B, v: i32): B { return B { xs: b.xs.append(v), n: b.n + 1 }; }
function main(): i32 {
    let b: B = B { xs: [], n: 0 };
    let i: i32 = 0;
    while (i < 20) { b = push(b, i * 3); i = i + 1; }
    let sum: i32 = 0;
    let j: i32 = 0;
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
	if code := vcmd.ProcessState.ExitCode(); code != 58 {
		t.Errorf("consume-rebind builder value = %d, want 58 (570 & 255) — reclaim corrupted the shared field buffer", code)
	}
}
