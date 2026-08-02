package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmComponentAdapter exercises the *adapter* route to I/O
// components — the way the native compiler's `-wasi-adapter` option works.
// The self-host emits a normal WASI-preview1 *command* core (which it
// already does perfectly, including fd_write for output); composing that
// core with the preview1→preview2 adapter via `wasm-tools component new
// --adapt` yields a wasi:cli/run component that runs under wasmtime with
// real I/O — no preview2 codegen needed.
//
// Pipeline: source → wasm_run (preview1 WAT) → emit_binary (preview1 core)
// → wasm-tools component new --adapt → wasi:cli/run component.
//
// (component_full / emit_module_run cover the adapter-free no-I/O path;
// this covers the adapter path, which works for printing / file I/O too.)
func TestSelfHostWasmComponentAdapter(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-adapter e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-adapter e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping component-adapter e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// The binary core assembler (read target.wat, emit_binary, print bytes)
	// — shared with TestSelfHostWasmBinary (asmReadFileDriver).
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(asmReadFileDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("binary assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "asm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write asm wat: %v", err)
	}

	for _, tc := range []struct {
		name   string
		source string
		stdout string
	}{
		{"print", "function main(): i32 { write(\"hello from a component\\n\"); return 0; }", "hello from a component\n"},
		{"fstring", "function main(): i32 { var n: i32 = 21; write(f\"answer={n * 2}\"); return 0; }", "answer=42"},
		{"loop-print", "function main(): i32 { var i: i32 = 0; while (i < 3) { print_int(i); i = i + 1; } return 0; }", "012"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// source → preview1 core WAT → preview1 core binary
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("preview1 WAT empty")
			}
			if err := os.WriteFile(filepath.Join(dir, "target.wat"), wat, 0o644); err != nil {
				t.Fatalf("write target.wat: %v", err)
			}
			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				t.Fatalf("run core assembler: %v", err)
			}
			var core []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				core = append(core, byte(n))
			}
			corePath := filepath.Join(dir, tc.name+".core.wasm")
			if err := os.WriteFile(corePath, core, 0o644); err != nil {
				t.Fatalf("write core: %v", err)
			}

			// Compose with the preview1 adapter → a wasi:cli/run component.
			compPath := filepath.Join(dir, tc.name+".component.wasm")
			cnew := exec.Command(wasmtools, "component", "new", corePath,
				"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath)
			if out, err := cnew.CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools component new --adapt: %v\n%s", err, out)
			}
			if vout, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
			}
			pout, _ := exec.Command(wasmtools, "print", compPath).Output()
			if !strings.Contains(string(pout), "wasi:cli/run") {
				t.Errorf("component missing wasi:cli/run export:\n%s", pout)
			}

			// Run the component; assert its stdout.
			got, err := exec.Command(wasmtime, "run", compPath).Output()
			if err != nil {
				t.Fatalf("wasmtime run component: %v", err)
			}
			if string(got) != tc.stdout {
				t.Errorf("component stdout = %q, want %q", string(got), tc.stdout)
			}
		})
	}
}
