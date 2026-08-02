package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Direct-index of an array LITERAL — `[10.5, 20.5, 30.5][i]` — must read at the
// element's real stride. The ExprArray construction builds an f64 literal at an
// 8-byte stride (arr_make ewidth=64) and an `[x as i64, …]` literal via
// arr_make_i64 (8-byte), but the READ side (arr_index_is_f64 / arr_index_is_i64)
// had no ExprArray arm, so the index defaulted to the 4-byte i32 path. On the
// register backends every element sits in an 8-byte slot regardless, so they
// were unaffected; on wasm the 4-byte i32.load read only the low half of the
// 8-byte value — a silent wrong result (#4366's predicate-gap class, the
// ExprArray sibling of the #2908 ExprSlice stride gap). This pins both the f64
// and i64 literal-index reads on the x86-64 and wasm IR paths.
var arrLitIndexCases = []struct {
	name string
	src  string
	want int
}{
	// [10.5,20.5,30.5][1] == 20.5 → 42.
	{"f64-literal-index", "function main(): i32 {\n" +
		"    var i: i32 = 1;\n" +
		"    var x: f64 = [10.5, 20.5, 30.5][i];\n" +
		"    if (x > 20.0 && x < 21.0) { return 42; }\n" +
		"    return 1;\n" +
		"}\n", 42},
	// [100,200,300 as i64][2] == 300 → 43.
	{"i64-literal-index", "function main(): i32 {\n" +
		"    var i: i32 = 2;\n" +
		"    var x: i64 = [100 as i64, 200 as i64, 300 as i64][i];\n" +
		"    if (x == (300 as i64)) { return 43; }\n" +
		"    return 1;\n" +
		"}\n", 43},
}

// TestSelfHostArrayLiteralIndexIR pins the x86-64 IR path (register backend —
// always used 8-byte slots, so a regression here would be a broader stride
// break, not the wasm-specific one).
func TestSelfHostArrayLiteralIndexIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("single-program IR driver test runs only natively")
	}
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrLitIndexCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			run := exec.Command(progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s (x86-64 IR) exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostArrayLiteralIndexIRWasm is the wasm leg — the path the mis-stride
// actually broke (a 4-byte i32.load of an 8-byte f64/i64 element). Pre-fix these
// returned the wrong value; post-fix the read is f64.load / i64.load.
func TestSelfHostArrayLiteralIndexIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-literal-index wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrLitIndexCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			run.Dir = dir
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s (wasm IR) exited %d, want %d — mis-stride regression\n--- WAT ---\n%s", tc.name, code, tc.want, wat)
			}
		})
	}
}
