package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostBigI64LiteralWasmIR pins the fix for #2928: a u64/i64 LITERAL
// whose value exceeds the i32 range (e.g. 9000000000000000007) must lower to an
// `i64.const` on the wasm IR backend, not `i32.const <big>` — which is invalid
// WAT that wasmtime rejects. The bug was in irlower's `as_i64`/`as_u64` arm,
// which lowered the literal operand as an i32 const before the widen; a literal
// too big for i32 is already a 64-bit value, so the cast is identity and it is
// emitted directly as const_i64.
//
// The program is oracle-checked against the reference interpreter (not a
// hardcoded exit code) so a wrong-but-stable result can't slip through (cf. the
// hardcoded-expectation gap noted in the numeric-methods IR test). Goal-1
// self-host IR coverage; surfaced by the std numeric self-host IR work (#2917).
func TestSelfHostBigI64LiteralWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host big-i64-literal wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// 9000000000000000007 is 19 digits — far beyond i32 range but inside i64/u64.
	// 9000000000000000000 is divisible by 256, so the modulus is 7 → exit 7.
	src := `function main(): i32 {
    var big: u64 = 9000000000000000007 as u64;
    return (big % 256 as u64) as i32;
}`
	want := interpExit(t, interpBin, src)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "ir_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally (likely invalid WAT — the bug):\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("big-i64-literal wasm IR exited %d, want %d (interp oracle)", got, want)
	}
}
