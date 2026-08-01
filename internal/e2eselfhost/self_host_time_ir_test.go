package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTimeIR covers std/time's pure-i32 calendar helpers (is_leap_year,
// days_in_month) through the self-hosted x86-64 IR path (a "self-host pending"
// audit gap). These are pure integer arithmetic (no Date struct), so they lower
// directly. The Date/Instant methods (struct receivers, Option returns) are a
// separate widening effort and aren't covered here. Cross-checked against the
// reference interpreter.
func TestSelfHostTimeIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally")
		}
		return inner.ProcessState.ExitCode()
	}

	// std/time's pure-i32 helpers, verbatim.
	const helpers = `
function is_leap_year(y: i32): boolean {
    if (y % 4 != 0) { return false; }
    if (y % 100 != 0) { return true; }
    return y % 400 == 0;
}
function days_in_month(y: i32, m: i32): i32 {
    if (m == 1) { return 31; }
    if (m == 2) { if (is_leap_year(y)) { return 29; } return 28; }
    if (m == 3) { return 31; } if (m == 4) { return 30; }
    if (m == 5) { return 31; } if (m == 6) { return 30; }
    if (m == 7) { return 31; } if (m == 8) { return 31; }
    if (m == 9) { return 30; } if (m == 10) { return 31; }
    if (m == 11) { return 30; } if (m == 12) { return 31; }
    return 0;
}
`
	const src = helpers + `function main(): i32 {
    if (is_leap_year(2000) != true) { return 1; }   // /400
    if (is_leap_year(1900) != false) { return 2; }   // /100 not /400
    if (is_leap_year(2024) != true) { return 3; }    // /4
    if (is_leap_year(2023) != false) { return 4; }
    if (days_in_month(2024, 2) != 29) { return 5; }  // leap Feb
    if (days_in_month(2023, 2) != 28) { return 6; }
    if (days_in_month(2023, 4) != 30) { return 7; }
    if (days_in_month(2023, 12) != 31) { return 8; }
    return 42;
}`
	if got := emitAndRunIR(t, src); got != 42 {
		t.Errorf("self-host IR std/time pure helpers: check = %d, want 42", got)
	}
}
