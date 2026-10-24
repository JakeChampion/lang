package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const enumReassignChurn = `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    let b: Bag = Keep([0, 0, 0, 0]);
    let i: i32 = 0;
    while (i < n) {
        b = Keep([i, i, i, i]);
        b = Swap([i, i, i, i]);
        i = i + 1;
    }
    match (b) { Keep(_) => { return 1; }, Swap(_) => { return 2; }, }
    return 0;
}
function main(): i32 { return churn(%d); }
`

const enumReassignFlatHeap = `enum Big { A(i32[]), B(i32[]) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function churn(n: i32): i32 {
    let b: Big = A([0]);
    let i: i32 = 0;
    while (i < n) {
        b = A(id([0, 0, 0, 0, 0, 0, 0, 0, 0, 0]));
        b = B(id([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]));
        i = i + 1;
    }
    match (b) { A(_) => { return 1; }, B(_) => { return 7; }, }
    return 0;
}
function main(): i32 { return churn(3000000); }
`

const enumReassignCorruptionProbe = `enum Bag { Keep(i32[]), Swap(i32[]) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function churn(n: i32): i32 {
    let b: Bag = Keep([9, 9]);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < n) {
        b = Keep(id([1, 2]));
        b = Swap(id([3, 4]));
        let fresh: i32[] = id([7, 8]);
        acc = acc + fresh[0] + fresh[1];
        i = i + 1;
    }
    match (b) { Keep(_) => { return acc + 1; }, Swap(_) => { return acc; }, }
}
function main(): i32 { return churn(10); }
`

const enumReassignBoundFallback = `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    let b: Bag = Keep([0, 0, 0, 0, 0, 0, 0, 0]);
    let i: i32 = 0;
    while (i < n) {
        b = Keep([i, i, i, i, i, i, i, i]);
        b = Swap([i, i, i, i, i, i, i, i]);
        match (b) { Keep(a) => { }, Swap(a) => { }, }
        i = i + 1;
    }
    match (b) { Keep(a) => { return a[0]; }, Swap(a) => { return a[1]; }, }
    return 0;
}
function main(): i32 { return churn(3); }
`

func TestSelfHostEnumReassignReclaimX86_64(t *testing.T) {
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

	run := func(name, prog string, want int) {
		t.Run(name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d", name, code, want)
			}
		})
	}

	run("wildcard-churn", fmt.Sprintf(enumReassignChurn, 5), 2)
	run("flat-heap-3M", enumReassignFlatHeap, 7)
	run("corruption-probe", enumReassignCorruptionProbe, 150)
	run("payload-bound-fallback", enumReassignBoundFallback, 2)
}

func TestSelfHostEnumReassignReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum-reassign-reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	prog := fmt.Sprintf(enumReassignChurn, 5)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(prog))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "enumre_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	w := exec.Command("wasmtime", "run", watFile)
	_ = w.Run()
	if w.ProcessState == nil || !w.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := w.ProcessState.ExitCode(); code != 2 {
		t.Errorf("enum-reassign-reclaim wasm wildcard-churn = %d, want 2", code)
	}
}
