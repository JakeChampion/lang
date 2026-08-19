package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostJoinWasmIR pins `xs.join(sep)` on a string[] receiver lowering
// through the self-host WASM IR path (#5328 wasm slice; closes the
// module_calls_arr_str_join deferral in wasm_ir_deferrals_ok). irlower emits a
// call of the __fern_arr_str_join runtime helper; on wasm that call routes via
// wasm_ir.wasm_helper_symbol to the hand-written $__fern_str_join WAT
// (wasm.str_join_helper, gated on @uses_arr_str_join). Before this, a join module
// fell back to the legacy AST wasm emitter. Each case pipes a single program to
// the `wasm_ir_run -ir` driver (which resolves no stdlib, so `.join` is a builtin
// irlower intercepts), asserts the emitted WAT reached the join helper, then runs
// it under wasmtime and checks the joined string's length as the exit code.
func TestSelfHostJoinWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host join wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// 3 elements + "-": len("a-bb-ccc") = 8.
		{"multi", `function main(): i32 { var xs: string[] = []; xs = xs.append("a"); xs = xs.append("bb"); xs = xs.append("ccc"); return xs.join("-").len(); }`, 8},
		// Empty array joins to "" (len 0) — the n==0 accumulator path.
		{"empty", `function main(): i32 { var xs: string[] = []; return xs.join(",").len(); }`, 0},
		// A single element has no separator applied: len("solo") = 4.
		{"single", `function main(): i32 { var xs: string[] = []; xs = xs.append("solo"); return xs.join(",").len(); }`, 4},
		// Empty separator concatenates directly: len("abc") = 3.
		{"empty-sep", `function main(): i32 { var xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); xs = xs.append("c"); return xs.join("").len(); }`, 3},
		// A multi-char separator: len("x - y - z") = 9.
		{"multi-char-sep", `function main(): i32 { var xs: string[] = []; xs = xs.append("x"); xs = xs.append("y"); xs = xs.append("z"); return xs.join(" - ").len(); }`, 9},
		// The join result feeds `+` concat: len("[a,b]") = 5.
		{"concat-result", `function main(): i32 { var xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); var s: string = "[" + xs.join(",") + "]"; return s.len(); }`, 5},
	}

	for _, tc := range cases {
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
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !strings.Contains(string(wat), "$__fern_str_join") {
				t.Errorf("%q did not reach the IR join helper (no $__fern_str_join in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "join_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("join wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
