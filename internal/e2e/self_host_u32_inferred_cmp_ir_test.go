package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u32InferredCmpIRCases pin the fix for a self-host wasm-IR miscompile: an
// *inferred* unsigned local (`var a = <expr> as u32`, with no `: u32`
// annotation) was not marked u32 in irlower, so a later ordering compare
// (`a > b`) emitted a SIGNED IR kind. On wasm that is a true-32-bit `i32.gt_s`,
// which is wrong once the value's bit 31 is set (a u32 >= 2^31 reads as a
// negative i32). x86-64 / arm64 happened to be right because the value sits
// zero-extended in a 64-bit register, so their signed compare still ordered it
// correctly — hence the bug only surfaced on wasm.
//
// The fix marks an un-annotated binding u32 (u64) from its initializer's type
// (`expr_is_u32`/`expr_is_u64`), so the unsigned compare/div remap fires. These
// cases are value-pinned against the interpreter oracle (the bug was a wrong
// value, not a routing change), and routing-pinned to "ir" on x86-64. Every
// result <= 120.
var u32InferredCmpIRCases = []struct {
	name string
	main string
}{
	// The headline case: a u32 >= 2^31 must compare greater than a small u32.
	{"inferred-gt-big", `function main(): i32 { var a = 3000000000 as u32; var b = 5 as u32; if (a > b) { return 1; } return 0; }`},
	// ...and must NOT compare less-than (signed would say it does).
	{"inferred-lt-big", `function main(): i32 { var a = 3000000000 as u32; var b = 5 as u32; if (a < b) { return 1; } return 0; }`},
	// `>=` on equal big values.
	{"inferred-ge-eq", `function main(): i32 { var a = 3000000000 as u32; var b = 3000000000 as u32; if (a >= b) { return 1; } return 0; }`},
	// All four orderings between a big and a small u32: a>b, a>=b, b<a, a!=b -> 4.
	{"inferred-count", `function main(): i32 { var a = 3000000000 as u32; var b = 5 as u32; var n = 0; if (a > b) { n = n + 1; } if (a >= b) { n = n + 1; } if (b < a) { n = n + 1; } if (a != b) { n = n + 1; } return n; }`},
	// u32 division is unsigned too: 3000000000 / 3 == 1000000000 (unsigned),
	// which is NOT > 1000000000 -> 0. (Signed div would give a negative quotient.)
	{"inferred-div-u", `function main(): i32 { var a = 3000000000 as u32; var b = 3 as u32; var q = a / b; if (q > 1000000001 as u32) { return 1; } return 0; }`},
	// Regression: a small inferred u32 still orders correctly.
	{"small-u32", `function main(): i32 { var a = 100 as u32; var b = 200 as u32; if (a < b) { return 1; } return 0; }`},
	// Regression: an explicitly-typed u32 (already worked) stays correct.
	{"explicit-u32", `function main(): i32 { var a: u32 = 3000000000; var b: u32 = 5; if (a > b) { return 1; } return 0; }`},
	// Regression: a signed i32 negative value still uses a SIGNED compare.
	{"signed-i32-neg", `function main(): i32 { var a = (0 - 5); var b = 3; if (a < b) { return 1; } return 0; }`},
}

// TestSelfHostU32InferredCmpIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostU32InferredCmpIRX86_64(t *testing.T) {
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

	for _, tc := range u32InferredCmpIRCases {
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

// TestSelfHostU32InferredCmpIRWasm runs the same cases through the wasm IR
// backend — the backend the bug was specific to.
func TestSelfHostU32InferredCmpIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-inferred-cmp wasm IR e2e")
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

	for _, tc := range u32InferredCmpIRCases {
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
			watFile := filepath.Join(dir, "u32cmp_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("u32-inferred-cmp wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
