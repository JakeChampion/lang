package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmIRComponentAdapter(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR component-adapter e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR component-adapter e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR component-adapter e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")

	emitIR := func(t *testing.T, src string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("wasm_ir_run failed for %q: %v", src, err)
		}
		return wat
	}

	compose := func(t *testing.T, tag string, wat []byte) string {
		t.Helper()
		watPath := filepath.Join(dir, tag+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write %s: %v", watPath, err)
		}
		corePath := filepath.Join(dir, tag+".core.wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		compPath := filepath.Join(dir, tag+".component.wasm")
		cnew := exec.Command(wasmtools, "component", "new", corePath,
			"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath)
		if out, err := cnew.CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component new --adapt: %v\n%s", err, out)
		}
		if out, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate: %v\n%s", err, out)
		}
		pout, _ := exec.Command(wasmtools, "print", compPath).Output()
		if !strings.Contains(string(pout), "wasi:cli/run") {
			t.Errorf("%s: component missing wasi:cli/run export", tag)
		}
		return compPath
	}

	runComp := func(t *testing.T, compPath string) (string, int) {
		t.Helper()
		cmd := exec.Command(wasmtime, "run", compPath)
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	t.Run("print", func(t *testing.T) {
		comp := compose(t, "print", emitIR(t, `function main(): i32 { print("hello from a wasm-ir component"); return 0; }`))
		out, code := runComp(t, comp)
		if out != "hello from a wasm-ir component\n" {
			t.Errorf("stdout = %q, want the printed line", out)
		}
		if code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	})

	t.Run("exit-zero", func(t *testing.T) {
		comp := compose(t, "zero", emitIR(t, `function main(): i32 { return 0; }`))
		if _, code := runComp(t, comp); code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	})

	t.Run("exit-nonzero-collapses", func(t *testing.T) {
		comp := compose(t, "five", emitIR(t, `function main(): i32 { return 5; }`))
		if _, code := runComp(t, comp); code != 1 {
			t.Errorf("exit = %d, want 1 (cli/run collapses non-zero to err)", code)
		}
	})
}
