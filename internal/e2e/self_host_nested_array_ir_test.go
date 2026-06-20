package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedArrayIRCases pin array-of-arrays element reads (`T[][]`) to the self-host
// IR path on x86-64 + wasm. A nested array element is a leak-only heap-array
// pointer (one slot, like a struct/string/tuple element), so `m[i][j]` lowers
// through the IR path and the module stays IR-eligible. This shape was previously
// only exercised indirectly (string-array byte indexing in the csv pin, `f[2][0]`,
// which is a string-array + byte load — not a true T[][] element load), so the
// genuine nested-array load had no dedicated routing pin.
//
// Scope is i32[][] and string[][] only: the i64[][] variant currently routes
// "ast" (a separate width-decode widening, parallel to the i64-tuple sibling), so
// it is deliberately excluded to keep the path == "ir" assertion honest. Each
// case is oracle-checked against the interpreter and returns <= 126 (wasmtime
// exit-code truncation, cf. #2908). Mirrors self_host_nested_tuple_ir_test.go.
var nestedArrayIRCases = []struct {
	name string
	main string
}{
	// Read across two rows of a 2x2 i32[][]: m[0][1] + m[1][0] = 2 + 3 = 5.
	{"read-2x2", `function main(): i32 { var m: i32[][] = [[1, 2], [3, 4]]; return m[0][1] + m[1][0]; }`},
	// Outer + inner length: m.len() (2) + m[0].len() (3) = 5.
	{"row-len", `function main(): i32 { var m: i32[][] = [[1, 2, 3], [4, 5]]; return m.len() + m[0].len(); }`},
	// Loop-sum every element via per-row binding: 1+2+3+4+5+6 = 21.
	{"loop-sum", `function main(): i32 { var m: i32[][] = [[1, 2], [3, 4], [5, 6]]; var s = 0; var i = 0; while (i < m.len()) { var r = m[i]; var j = 0; while (j < r.len()) { s = s + r[j]; j = j + 1; } i = i + 1; } return s; }`},
	// string[][]: nested string elements round-trip; sum their byte lengths.
	// "ab"(2) + "c"(1) + "de"(2) = 5.
	{"string-nested", `function main(): i32 { var g: string[][] = [["ab", "c"], ["de"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len(); }`},
}

// TestSelfHostNestedArrayIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedArrayIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range nestedArrayIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostNestedArrayIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostNestedArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-array wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range nestedArrayIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			watFile := filepath.Join(dir, "nested_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested-array wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
