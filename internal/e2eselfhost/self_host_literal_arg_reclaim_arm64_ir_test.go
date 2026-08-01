package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostLiteralArgReclaimIRArm64 is the arm64 port of
// TestSelfHostLiteralArgReclaimIRX86_64 (#4355 slice 6): the literal
// string-arg box reclaim at borrowable call positions, on the arm64 IR
// backend (the reclaim is emitted at the IR layer, so both natives share
// it; arm64 additionally exercises __fern_str_free's register discipline
// mid-expression). Lighter churn under qemu.
func TestSelfHostLiteralArgReclaimIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = literal-arg box leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// Literal arg at a borrowable position — churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit("ab"); i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1000) { acc = acc + readit("ab"); j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 2400) { return 97; }
    return 0;
}`, "literal-arg-borrowable-flat-arm64", 0)

	// NON-borrowable position (callee returns its param): the bound value
	// stays readable at detector zero.
	run(t, `function keepit(nm: string): string { return nm; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var got: string = keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "literal-arg-retained-safe-arm64", 0)
}
