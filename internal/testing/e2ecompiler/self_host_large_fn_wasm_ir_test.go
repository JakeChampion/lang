package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func largeSingleFnProgram(n int) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n    let acc: i32 = 0;\n")
	for i := 0; i < n; i++ {
		s := strconv.Itoa(i)
		b.WriteString("    acc = acc + " + s + " * 3 - " + s + " + (acc / 2) + (" + s + " % 7);\n")
	}
	b.WriteString("    if (acc == acc) { return 42; }\n    return 1;\n}\n")
	return b.String()
}

func TestSelfHostLargeFnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host large-fn wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := []byte(largeSingleFnProgram(240))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader(src)
	wat, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver failed to compile large single-function module (a re-introduced "+
			"per-pass re-lowering would OOM here): %v", err)
	}
	if len(wat) == 0 {
		t.Fatal("self-host wasm IR driver emitted 0 bytes for the large single-function module")
	}
	watFile := filepath.Join(dir, "large_fn_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat[:min(len(wat), 400)])
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("large-fn wasm IR exit = %d, want 42", code)
	}
}
