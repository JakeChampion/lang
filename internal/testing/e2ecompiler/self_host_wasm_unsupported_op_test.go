package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostWasmUnsupportedOpRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_runio_run.fern")
	wasmDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_runio_run.fern", "wasmdriver")

	const src = `function main(): i32 { let m: i64 = __heap_mark(); __heap_release_to(m); return 0; }`

	t.Run("wasm-refuses", func(t *testing.T) {
		cmd := exec.Command(wasmDriver)
		cmd.Stdin = bytes.NewReader([]byte(src))
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("wasm driver did not exit normally")
		}
		if code := cmd.ProcessState.ExitCode(); code != 3 {
			t.Errorf("exit = %d, want 3 (the unsupported-op refusal); stderr:\n%s", code, errb.String())
		}
		if !strings.Contains(errb.String(), "heap_mark") {
			t.Errorf("refusal does not name the op; stderr:\n%s", errb.String())
		}
		if !strings.Contains(errb.String(), "no instruction selection") {
			t.Errorf("refusal is not the instruction-selection diagnostic; stderr:\n%s", errb.String())
		}
		if strings.Contains(out.String(), ";; ir: unsupported") {
			t.Errorf("emitted the op as a WAT comment instead of refusing:\n%s", out.String())
		}
	})

	t.Run("x86-64-still-lowers-it", func(t *testing.T) {
		asmDir := t.TempDir()
		copySelfHostDriver(t, asmDir, "drivers/asm_ir_run.fern")
		asmDriver := buildSelfHostBin(t, gcc, asmDir, "drivers/asm_ir_run.fern", "asmdriver")
		cmd := exec.Command(asmDriver)
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("x86-64 driver failed on a program it is expected to lower: %v", err)
		}
		if len(out) == 0 {
			t.Fatal("x86-64 driver emitted 0 bytes")
		}
		if strings.Contains(string(out), "# ir: unsupported") {
			t.Error("x86-64 emitted the op as a comment — its own #6917 refusal has regressed")
		}
	})
}
