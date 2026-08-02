package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Enum self-reassign payload deep-drop (Perceus): a loop-carried array-payload enum
// `var b: E = V0([..]); while (..) { b = V1([..]); b = V2([..]); }` whose payload is
// NEVER bound (all-`_` matches) has each superseded box DEEP-DROPPED (payload array +
// box) at the reassign, closing the register-backend per-reassign leak (box-only
// shallow free left it). enum_only_wildcard_used_rec gates soundness (no payload
// aliasing); a payload-binding match disqualifies b (safe fallback to the shallow
// free). On wasm the pattern is already reclaimed, so the extra dec is a rc-guarded
// no-op.

const enumReassignChurn = `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    var b: Bag = Keep([0, 0, 0, 0]);
    var i: i32 = 0;
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

// A 4000-byte payload (1000 i32) per reassign: on the register backends, if the
// superseded array leaks, ~600k iterations exhaust the 2.5 GiB heap and the program
// traps (exit 137). With the deep-drop it stays flat and completes.
const enumReassignFlatHeap = `enum Big { A(i32[]), B(i32[]) }
function churn(n: i32): i32 {
    var b: Big = A([0]);
    var i: i32 = 0;
    while (i < n) {
        b = A([0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
        b = B([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
        i = i + 1;
    }
    match (b) { A(_) => { return 1; }, B(_) => { return 7; }, }
    return 0;
}
function main(): i32 { return churn(3000000); }
`

// Corruption probe (x86): a fresh array allocated in the same scope as the reassigns
// must read back correctly (a mis-freed superseded payload would poison the recycled
// buffer). acc = 10 * (7+8) = 150.
const enumReassignCorruptionProbe = `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    var b: Bag = Keep([9, 9]);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < n) {
        b = Keep([1, 2]);
        b = Swap([3, 4]);
        var fresh: i32[] = [7, 8];
        acc = acc + fresh[0] + fresh[1];
        i = i + 1;
    }
    match (b) { Keep(_) => { return acc + 1; }, Swap(_) => { return acc; }, }
}
function main(): i32 { return churn(10); }
`

// GATED OUT: the payload IS bound (`Keep(a) => ...`), so b is disqualified and keeps
// the safe box-only shallow free. Must still compute correctly. churn(3): last b is
// Swap([2,2,2,2,2,2,2,2]) -> a[1] = 2.
const enumReassignBoundFallback = `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    var b: Bag = Keep([0, 0, 0, 0, 0, 0, 0, 0]);
    var i: i32 = 0;
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
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
	run("flat-heap-3M", enumReassignFlatHeap, 7) // exit 137 (OOM) if the payload leaks
	run("corruption-probe", enumReassignCorruptionProbe, 150)
	run("payload-bound-fallback", enumReassignBoundFallback, 2)
}

// Wasm: the pattern is already reclaimed on wasm, so the extra deep-drop is a
// rc-guarded no-op — the wildcard churn must still compute correctly and stay flat.
func TestSelfHostEnumReassignReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum-reassign-reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	prog := fmt.Sprintf(enumReassignChurn, 5)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
