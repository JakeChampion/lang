package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructFieldDropIRX86_64 verifies the Perceus deep-drop of a
// reclaimable struct's scalar-array (i32[]) field. use_bag builds a
// Bag{items: i32[], n} (b is reclaimable — fresh and only field-read) and returns
// b.items[0] + b.n = 1 + 3 = 4. At b's scope-exit reclamation the IR path frees
// BOTH the array it solely owns AND the struct box — two box releases (the leaf-
// safe rule makes items a fresh, sole-owned literal, so this can't double-free).
//
// main has no arrays, so every `call __fn___fern_arr_dec` is part of the Bag
// reclamation: 2 with the deep-drop (items field + box), 1 without — the count
// pins that the field is dropped (can't regress to leaking it). The exit code
// pins correctness (a double-free would corrupt the field read).
func TestSelfHostStructFieldDropIRX86_64(t *testing.T) {
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

	prog := `struct Bag { items: i32[], n: i32 }
function use_bag(): i32 {
    var b: Bag = Bag { items: [1, 2, 3], n: 3 };
    return b.items[0] + b.n;
}
function main(): i32 { return use_bag(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	if frees := bytes.Count(asm, []byte("call __fn___fern_arr_dec")); frees < 2 {
		t.Errorf("found %d __fern_arr_dec calls, want >= 2 (the i32[] field + the struct box) — the field was not deep-dropped", frees)
	}
	progBin := buildBin(t, gcc, dir, "field_drop", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 4 {
		t.Errorf("exit %d, want 4 (b.items[0] + b.n) — a double-free corrupted the field read", code)
	}
}
