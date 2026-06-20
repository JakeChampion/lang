package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// heapBumpBytesIRCases pin the `__heap_bump_bytes()` introspection builtin — the
// bump allocator's high-water mark (cursor − region base; 0 before the first
// allocation) — on the self-host IR path (#3534). Before this it had no IR
// lowering and bailed the whole module to the legacy AST emitter; it now lowers
// on all three IR backends (x86-64 / arm64 inline cursor−base; wasm `$heap −
// heap_base`).
//
// The interpreter has no bump-allocator model (it always returns 0), so it
// cannot be the oracle here — these assert the relational contract directly with
// exact exit codes (cross-checked against the native backend, which lowers the
// builtin via __fern_heap_bump_bytes), mirroring the native rc_heap_bump_* style.
// Every result stays ≤ 120 (wasmtime exit-code clamp #2908).
var heapBumpBytesIRCases = []struct {
	name string
	main string
	want int
}{
	// Before any allocation the high-water mark is 0.
	{"zero-before-alloc", `function main(): i32 { if (__heap_bump_bytes() == 0) { return 7; } return 1; }`, 7},
	// A fresh allocation advances the cursor above the zero baseline.
	{"grows-on-alloc", `function main(): i32 { var before: i32 = __heap_bump_bytes(); var a: i32[] = [1, 2, 3, 4, 5]; var after: i32 = __heap_bump_bytes(); if (before == 0) { if (after > before) { return 7; } } return 1; }`, 7},
	// Read across a call boundary + an explicit "after > 0" check.
	{"after-positive", `function main(): i32 { var a: i32[] = [1, 2, 3]; if (__heap_bump_bytes() > 0) { return 11; } return 1; }`, 11},
}

// TestSelfHostHeapBumpBytesIRX86_64 routes each case through the self-hosted
// x86-64 IR driver (asm_run), pins the routing to "ir" (asm_pathprobe_run), and
// checks the exit code against the native backend's exit code (the oracle here,
// since the interpreter has no bump-allocator model).
func TestSelfHostHeapBumpBytesIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			// Native cross-check: the Go x86-64 backend must give the same code.
			if _, code := compileAndRunX86_64(t, tc.main+"\n"); code != tc.want {
				t.Fatalf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostHeapBumpBytesIRWasm runs the same cases through the wasm IR
// backend (the `$heap − heap_base` lowering).
func TestSelfHostHeapBumpBytesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host heap-bump-bytes wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "heap_bump_bytes_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("heap-bump-bytes wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
