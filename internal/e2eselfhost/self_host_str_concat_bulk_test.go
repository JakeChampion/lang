package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strConcatBulkProgram sweeps `a + b` over every length pair in 0..40 and folds a
// POSITION-WEIGHTED checksum of each result, so a copy that lands the right byte
// count in the wrong order — or repeats one operand — is caught as well as a
// short one. The two operands are cut from disjoint alphabets for the same
// reason: with a shared source literal, copying the left operand twice would
// still checksum correctly whenever the two lengths matched.
//
// The sweep is the point. `__fern_str_concat` bulk-copies each operand with
// `__memcpy`, whose backend lowerings dispatch on length, so the boundaries
// between those size classes (and the 0-length no-op at each end) are where a
// wrong answer lives. 0..40 straddles every one of them on both operands.
//
// Both operands are slices of a longer literal, so the copy source is a view
// into a larger buffer rather than a fresh box. That is both the shape the
// helper usually sees and the cheap one: cutting the lengths costs nothing, so
// the sweep is 1681 concatenations rather than the quadratic rebuild a `mk(n)`
// helper would cost — it has to run under qemu.
const strConcatBulkProgram = `function csum(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) {
        t = (t + (s[i] as i32) * (i + 1)) % 1000003;
        i = i + 1;
    }
    return t;
}

function main(): i32 {
    var a: string = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn";
    var b: string = "0123456789!@#$%^&*()nopqrstuvwxyzZYXWVUTS";
    var acc: i32 = 0;
    var la: i32 = 0;
    while (la <= 40) {
        var lb: i32 = 0;
        while (lb <= 40) {
            var c: string = slice_unchecked(a, 0, la) + slice_unchecked(b, 0, lb);
            if (c.len() != la + lb) { return 90; }
            acc = (acc + csum(c)) % 89;
            lb = lb + 1;
        }
        la = la + 1;
    }
    return acc;
}
`

// TestSelfHostStrConcatBulkX86_64 pins the x86-64 IR lowering of the bulk copy
// against the interpreter.
func TestSelfHostStrConcatBulkX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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

// TestSelfHostStrConcatBulkArm64 is the arm64 counterpart. Both register
// backends share `asmcore.rt_src_str_concat`, but each selects `__memcpy`
// independently, so the boundaries have to be walked on both.
func TestSelfHostStrConcatBulkArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	src := []byte(strConcatBulkProgram)
	want := interpExit(t, interpBin, string(src))

	asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux", "-ir")
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

// TestSelfHostStrConcatBulkWasm walks the same sweep on wasm. That backend
// lowers `str_concat` itself rather than through `rt_src_str_concat`, so this
// leg is a cross-backend agreement check: the three targets must fold the same
// checksum.
func TestSelfHostStrConcatBulkWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host str-concat wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := []byte(strConcatBulkProgram)
	want := interpExit(t, interpBin, string(src))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
