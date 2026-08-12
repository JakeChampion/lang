package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostErasedWideTupleWasm pins the TUPLE half of the erased-wide close
// (#5464). A bare-tuple-return erased fn — `pair[K,V](k:K,v:V):(K,V)` — passing a
// 64-bit / f64 value through its erased params now LOWERS on the wasm IR path
// instead of deferring to the (miscompiling) AST emitter. The wasm tuple box is a
// uniform 8-byte-per-element layout, so the widened i64 element is stored 8-byte
// and read back at the caller's concrete per-element width with no layout change:
// t.0 (i64) reads i64.load, t.1 (i32) reads i32.load of the low word — both at the
// same 8-byte-strided offset. Cases assert the module reached the IR path (no
// `$__lit0` AST-fallback locals) and computes the right value under wasmtime.
// Values cross-checked against the native interpreter.
func TestSelfHostErasedWideTupleWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide tuple wasm IR e2e")
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
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const pair = `function pair[K, V](k: K, v: V): (K, V) { return (k, v); }`
	cases := []struct {
		name string
		src  string
		want int
	}{
		// i64 first element: t.0 rides the full-width i64 slot; t.1 (i32) reads the
		// low word at the same 8-byte stride. 5e9/1e9 + 3 = 5 + 3 = 8.
		{"i64-elem", pair + ` function main(): i32 { var t = pair(5000000000 as i64, 3); return (t.0 / 1000000000) as i32 + t.1; }`, 8},
		// f64 first element: reinterpreted into the i64 slot at the call, read back
		// as f64. 2.5*2.0 + 4 = 5 + 4 = 9.
		{"f64-elem", pair + ` function main(): i32 { var t = pair(2.5, 4); return (t.0 * 2.0) as i32 + t.1; }`, 9},
		// A string (pointer) element still round-trips through the widened i64 slot
		// (wasm32 pointer in the low word). "xy".len() + 3 = 2 + 3 = 5. Guards that
		// widening the shared fn does not regress a non-wide tuple caller.
		{"string-elem", pair + ` function main(): i32 { var t = pair("xy", 3); return t.0.len() + t.1; }`, 5},
		// Destructure `var (a, b) = pair(...)` recovers both widened elements. 6 + 7 = 13.
		{"destructure", pair + ` function main(): i32 { var (a, b) = pair(6000000000 as i64, 7); return (a / 1000000000) as i32 + b; }`, 13},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			if strings.Contains(string(wat), "$__lit0") {
				t.Errorf("%s deferred to the AST path (found $__lit0); expected IR lowering", tc.name)
			}
			watFile := filepath.Join(dir, "tup_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s (an invalid module fails to load)", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("erased-wide tuple wasm IR %s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
