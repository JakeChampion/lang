package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostClosureWasmIR is the wasm gate for closures slice 3a (the x86
// sibling is TestSelfHostClosureX86IR): first-class (escaping) capturing
// closures — `return function(x){ … cap … }` bound to a local and called. The
// env box is an i32[] [funcval, caps…]; const_func + arr_make build it,
// call_indirect dispatches env-first. Asserts the oracle exit code AND that the
// IR closure path was taken (`$clo` in the WAT). Exit codes <= 125.
func TestSelfHostClosureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure wasm IR e2e")
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
		{"adder", `function make_adder(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; } function main(): i32 { var add5 = make_adder(5); return add5(3); }`, 8},
		{"multi-capture", `function make(a: i32, b: i32): (i32) => i32 { return function(x: i32): i32 { return x * a + b; }; } function main(): i32 { var f = make(3, 7); return f(5); }`, 22},
		{"called-twice", `function make(a: i32, b: i32): (i32) => i32 { return function(x: i32): i32 { return x * a + b; }; } function main(): i32 { var f = make(2, 1); return f(3) + f(4); }`, 16},
		// A CAPTURING closure stored as a MAP VALUE, retrieved and called (slice
		// #3445 map-values): the lambda is wrapped into a `$cloN` env box stored in
		// the map (the lift method-callee arm fires for a lambda in a generic
		// builtin map-value slot), `m.get` returns it, and the `Some(f) => f()`
		// match-binding dispatches it env-first — on wasm too.
		{"map-value-closure-captured", `import "core/map"; function main(): i32 { var n = 10; var m: Map[i32, () => i32] = map_new(4); m = m.set(1, function(): i32 { return n + 7; }); match (m.get(1)) { Some(f) => { return f(); }, None => { return 0; } } }`, 17},
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
			if !strings.Contains(string(wat), "$clo") {
				t.Errorf("%q did not reach the IR closure path (no $clo in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("closure wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
