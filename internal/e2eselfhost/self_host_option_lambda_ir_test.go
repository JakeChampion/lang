package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostOptionLambdaIR pins the lambda_captures fix: a no-capture lambda
// whose body constructs an Option/Result (`function(x){ return Some(x+1); }`)
// passed as a fn-value argument. lambda_captures excluded `None`/`true`/`false`
// but NOT the call-style variant constructors `Some`/`Ok`/`Err`, so it miscounted
// `Some` as a captured free variable, misrouted the lambda to the capturing-
// closure (`$clo`) path, and never hoisted it -> BAIL const_func, dragging the
// module to AST (#3457: std/option / std/result `.and_then` / `.or_else`). Each
// case forces the IR path via the -ir driver, asserts the oracle exit code, and
// pins that a `$wrap` trampoline (the no-capture lift) was emitted.
func TestSelfHostOptionLambdaIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Option-returning lambda to a free function; unwrap -> 6.
		{"option-free", `function applyf(v: i32, f: (i32) => Option[i32]): Option[i32] { return f(v); }
function main(): i32 { match (applyf(5, function (x: i32): Option[i32] { return Some(x + 1); })) { Some(y) => { return y; }, None => { return 0; }, } }`, 6},
		// Result-returning lambda (Ok); unwrap -> 8.
		{"result-ok", `function applyr(v: i32, f: (i32) => Result[i32, i32]): Result[i32, i32] { return f(v); }
function main(): i32 { match (applyr(7, function (x: i32): Result[i32, i32] { return Ok(x + 1); })) { Ok(y) => { return y; }, Err(_) => { return 0; }, } }`, 8},
		// Option-returning lambda to a method on a struct receiver -> 6.
		{"option-method", `struct W { v: i32 }
function (w: W) applyo(f: (i32) => Option[i32]): Option[i32] { return f(w.v); }
function main(): i32 { var w: W = W { v: 5 }; match (w.applyo(function (x: i32): Option[i32] { return Some(x + 1); })) { Some(y) => { return y; }, None => { return 0; }, } }`, 6},
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
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), "$wrap") {
				t.Errorf("%s: lambda did not reach the no-capture $wrap lift path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "optlam_"+tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(progBin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
