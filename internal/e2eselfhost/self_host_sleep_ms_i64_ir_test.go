package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSleepMsI64IR pins `sleep_ms(<i64>)` lowering on the self-host
// x86-64 IR path. The clock builtins (monotonic_ns / now_unix_ms / now_ns) and
// sleep_ms already had IR ops, but sleep_ms lowered its argument through the i32
// path (lower_expr), which BAILed on an i64 argument (`sleep_ms(5 as i64)`) —
// dragging the whole timing module to the AST emitter. sleep_ms now width-
// dispatches its count: an i64 argument rides lower_i64, a plain i32 keeps the
// 32-bit path; either way the count is read into a 64-bit register (rdi / x0) by
// the same __fern_sleep_ms runtime the AST path calls. The program reads a
// monotonic clock, sleeps an i64 millisecond count, reads it again, and checks
// the clock did not go backwards -> exit 0; exercises monotonic_ns + sleep_ms
// (i64 arg) on the IR path.
func TestSelfHostSleepMsI64IR(t *testing.T) {
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

	const src = `function main(): i32 {
    var a: i64 = monotonic_ns();
    sleep_ms(1 as i64);
    var b: i64 = monotonic_ns();
    if (b < a) { return 1; }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_sleep_ms") {
		t.Fatal("sleep_ms did not reach the IR runtime path (no __fern_sleep_ms in asm)")
	}
	progBin := buildBin(t, gcc, dir, "sleepms_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("sleep_ms(i64) IR program exited %d, want 0 (monotonic clock + i64 sleep)", code)
	}
}
