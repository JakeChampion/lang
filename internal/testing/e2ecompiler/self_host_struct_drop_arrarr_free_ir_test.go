package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var structDropArrArrFreeProg = `struct P { x: i32, y: i32 }
struct Bag { es: P[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
        acc = (acc + b.n + b.es.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 220) { return 98; }
    return 0;
}`

var structDropArrArrSharedProg = `struct P { x: i32, y: i32 }
struct Bag { es: P[], n: i32 }
function take(b: Bag): i32 { return b.es[0].x + b.es.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
        let c: Bag = b;
        acc = (acc + take(b) + take(c)) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 249) { return 97; }
    return 0;
}`

func TestSelfHostStructDropArrArrFreeIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := string(runCapture(t, gcc, runner, driverBin, []byte(prog)))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, asm)
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

	run(t, structDropArrArrFreeProg, "struct_drop_arrarr_free", 0)
	run(t, structDropArrArrSharedProg, "struct_drop_arrarr_free_shared", 0)
}

func TestSelfHostStructDropArrArrFreeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, prog string }{
		{"struct_drop_arrarr_free_arm64", structDropArrArrFreeProg},
		{"struct_drop_arrarr_free_shared_arm64", structDropArrArrSharedProg},
	} {
		if code := arm64CensusRun(t, x86runner, driverBin, arm64gcc, qemu, tc.name, tc.prog); code != 0 {
			t.Errorf("%s exited %d, want 0", tc.name, code)
		}
	}
}
