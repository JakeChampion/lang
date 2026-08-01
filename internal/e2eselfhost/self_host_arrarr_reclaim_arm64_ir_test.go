package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostArrArrReclaimIRArm64 is the arm64 port of
// TestSelfHostArrArrReclaimIRX86_64 (#4355 slice 9): the arm64
// __fn___fern_arrarr_free / __fn___fern_strarrarr_free runtime bodies (the
// str_arr_free siblings — x19/x20/x21 + x30 register discipline across the
// per-element bl). Lighter churn under qemu.
func TestSelfHostArrArrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = leaked; 99 = over-release; 88 = live value freed; 97 = corrupted)", name, code, want)
		}
	}

	// string[][] churn — flat at detector zero.
	run(t, `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        acc = acc + g.len() + g[0][0].len();
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1000) {
        var g2: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        acc = acc + g2.len() + g2[1][1].len();
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-str-flat-arm64", 0)

	// i32[][] churn with expression inner elements — flat.
	run(t, `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: i32[][] = [[i, i + 1], [i + 2]];
        acc = acc + g.len() + g[0][0];
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1000) {
        var g2: i32[][] = [[j, j + 1], [j + 2]];
        acc = acc + g2.len() + g2[1][0];
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-scalar-flat-arm64", 0)

	// Row-alias exclusion pin.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        var row: string[] = g[1];
        if (row.len() != 2) { bad = 1; }
        if (row[0].len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-row-alias-safe-arm64", 0)
}
