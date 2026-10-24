package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostLambdaReachesIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping lambda-reaches-IR guard")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := `@noinline function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply((x: i32): i32 => { return x + 1; }, 41); }`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	out := string(wat)
	if !strings.Contains(out, "$main$wrap0") {
		t.Errorf("lambda argument did not reach the IR env-box path: emitted WAT has no $main$wrap0 trampoline\n%s", out)
	}
	if !strings.Contains(out, "call_indirect") {
		t.Errorf("lambda argument did not reach the IR env-box path: emitted WAT has no call_indirect dispatch\n%s", out)
	}
	if strings.Contains(out, "release_JNull") {
		t.Errorf("lambda program did not lower through the IR (AST tagged-value runtime present)")
	}

	src2 := `function main(): i32 { let f = (x: i32): i32 => { return x * 2; }; return f(21); }`
	var cmd2 *exec.Cmd
	if len(runner) == 0 {
		cmd2 = exec.Command(driverBin)
	} else {
		cmd2 = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd2.Stdin = bytes.NewReader([]byte(src2))
	wat2, err := cmd2.Output()
	if err != nil || len(wat2) == 0 {
		t.Fatalf("driver failed (local-bound lambda): %v", err)
	}
	out2 := string(wat2)
	if !strings.Contains(out2, "__lam_0") {
		t.Errorf("local-bound lambda did not reach the IR path: emitted WAT has no __lam_0 (lifted fn)\n%s", out2)
	}
	if strings.Contains(out2, "release_JNull") {
		t.Errorf("local-bound lambda did not lower through the IR (AST tagged-value runtime present)")
	}
}
