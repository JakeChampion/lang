package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIRComponentAdapter is the IR-path twin of
// TestSelfHostWasmComponentAdapter (#3457 phase 1 / #4315): it proves the
// self-host wasm *IR* path (wasm_ir_run -ir) produces a preview1 command core
// that composes — via `wasm-tools component new --adapt` — into a wasi:cli/run
// component that runs under wasmtime with real I/O.
//
// This matters because the async/socket ops being ported to the wasm-IR path
// (#4316 poll, #4318 tcp) lower through this IR path and will emit their
// preview2 interface imports into the same core module; establishing that the
// IR-emitted core is component-able is the foundation those phases attach to.
//
// Validated contract (see #4315):
//   - `wasi:cli/run` collapses exit codes to result<_,_>: return 0 → component
//     exit 0, any non-zero → exit 1 (the exact code is NOT preserved — which is
//     why the exit-code-exact wasm-IR e2e tests keep the direct core-module run,
//     and the component path is reserved for the preview2/server-shaped ops);
//   - stdout (print / write) survives the adapter.
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
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	// emitIR pipes src to the driver with -ir and returns the emitted WAT.
	emitIR := func(t *testing.T, src string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("wasm_ir_run -ir failed for %q: %v", src, err)
		}
		return wat
	}

	// compose turns IR-emitted WAT into a wasi:cli/run component via the
	// preview1 adapter, returning the component path.
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

	// runComp runs the component and returns (stdout, exitCode).
	runComp := func(t *testing.T, compPath string) (string, int) {
		t.Helper()
		cmd := exec.Command(wasmtime, "run", compPath)
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	// stdout survives the adapter; return 0 → exit 0.
	t.Run("print", func(t *testing.T) {
		comp := compose(t, "print", emitIR(t, `function main(): i32 { print("hello from a wasm-ir component"); return 0; }`))
		out, code := runComp(t, comp)
		if out != "hello from a wasm-ir component\n" { // print appends a newline
			t.Errorf("stdout = %q, want the printed line", out)
		}
		if code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	})

	// return 0 → component exit 0.
	t.Run("exit-zero", func(t *testing.T) {
		comp := compose(t, "zero", emitIR(t, `function main(): i32 { return 0; }`))
		if _, code := runComp(t, comp); code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	})

	// return non-zero → collapsed to component exit 1 (cli/run result<_,_>).
	t.Run("exit-nonzero-collapses", func(t *testing.T) {
		comp := compose(t, "five", emitIR(t, `function main(): i32 { return 5; }`))
		if _, code := runComp(t, comp); code != 1 {
			t.Errorf("exit = %d, want 1 (cli/run collapses non-zero to err)", code)
		}
	})
}
