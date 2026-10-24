package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const strConcatBulkProgram = `function csum(s: string): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < s.len()) {
        t = (t + (s[i] as i32) * (i + 1)) % 1000003;
        i = i + 1;
    }
    return t;
}

function main(): i32 {
    let a: string = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn";
    let b: string = "0123456789!@#$%^&*()nopqrstuvwxyzZYXWVUTS";
    let acc: i32 = 0;
    let la: i32 = 0;
    while (la <= 40) {
        let lb: i32 = 0;
        while (lb <= 40) {
            let c: string = slice_unchecked(a, 0, la) + slice_unchecked(b, 0, lb);
            if (c.len() != la + lb) { return 90; }
            acc = (acc + csum(c)) % 89;
            lb = lb + 1;
        }
        la = la + 1;
    }
    return acc;
}
`

func TestSelfHostStrConcatBulkX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	src := []byte(strConcatBulkProgram)
	want := interpExit(t, interpBin, string(src))

	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "str_concat_bulk", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("str-concat bulk sweep exited %d, want %d (interp oracle)", code, want)
	}
}

func TestSelfHostStrConcatBulkArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := []byte(strConcatBulkProgram)
	want := interpExit(t, interpBin, string(src))

	asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	progBin := buildBin(t, arm64gcc, dir, "str_concat_bulk", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("str-concat bulk sweep exited %d, want %d (interp oracle)", code, want)
	}
}

func TestSelfHostStrConcatBulkWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host str-concat wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := []byte(strConcatBulkProgram)
	want := interpExit(t, interpBin, string(src))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader(src)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "str_concat_bulk.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != want {
		t.Errorf("str-concat bulk sweep (wasm) = %d, want %d (interp oracle)", code, want)
	}
}
