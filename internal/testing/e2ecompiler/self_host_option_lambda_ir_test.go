package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostOptionLambdaIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"option-free", `function applyf(v: i32, f: (i32) => Option[i32]): Option[i32] { return f(v); }
function main(): i32 { match (applyf(5, (x: i32): Option[i32] => { return Some(x + 1); })) { Some(y) => { return y; }, None => { return 0; }, } }`, 6},
		{"result-ok", `function applyr(v: i32, f: (i32) => Result[i32, i32]): Result[i32, i32] { return f(v); }
function main(): i32 { match (applyr(7, (x: i32): Result[i32, i32] => { return Ok(x + 1); })) { Ok(y) => { return y; }, Err(_) => { return 0; }, } }`, 8},
		{"option-method", `struct W { v: i32 }
function (w: W) applyo(f: (i32) => Option[i32]): Option[i32] { return f(w.v); }
function main(): i32 { let w: W = W { v: 5 }; match (w.applyo((x: i32): Option[i32] => { return Some(x + 1); })) { Some(y) => { return y; }, None => { return 0; }, } }`, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
