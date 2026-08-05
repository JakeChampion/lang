package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLoopReuseWasmIR proves loop-body FBIP reuse (irlower
// `lower_loop_body`) lowers correctly on the self-hosted WASM IR backend,
// including the recipient prior-release (an `if (old != donor) rc_dec` guarding
// the loop-carried box). Value-through-wasmtime is the contract; exit codes stay
// < 126 for WASI's _start range.
func TestSelfHostLoopReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host loop-reuse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Struct loop reuse fires per iteration: sum over i in 0..3 of
		// (i+(i+1)) + (i*2+3) = 40.
		{"loop-struct-reuse", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`, 40},
		// Tuple loop reuse: sum over i in 0..3 of (i+(i+1)) + (i+3) = 34.
		{"loop-tuple-reuse", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`, 34},
		// Donor-live control (reuse suppressed): value stays correct across the loop.
		{"loop-tuple-donor-live", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var b: (i32, i32) = (i, 3); sum = sum + a.0 + a.1 + b.0 + b.1; i = i + 1; } return sum; }`, 34},
		// Functional-update (self-overwrite) reuse in a loop: `c = P { ...d, y: 3 }`
		// reuses d's box in place each iteration. sum over i in 0..3 of i + 3 = 18.
		{"loop-funcupdate-reuse", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: P = P { x: i, y: 0 }; var c: P = P { ...d, y: 3 }; sum = sum + c.x + c.y; i = i + 1; } return sum; }`, 18},
		// Same-block reuse fires in an if-arm body too (irlower lowers every nested
		// block with reuse). `b` reuses dead `a`'s box inside the `if`. (10+20)+(3+4)=37.
		// Each x is multiplied by a 1-valued variable so both literals stay OFF the
		// static-constant path (#6149) — written with bare literals the donor is
		// placed in data and there is no box left to reuse, so the case would still
		// return 37 while measuring nothing.
		{"if-arm-reuse", `struct P { x: i32, y: i32 } function main(): i32 { var cond: i32 = 1; var r: i32 = 0; if (cond > 0) { var a: P = P { x: 10 * cond, y: 20 }; var s: i32 = a.x + a.y; var b: P = P { x: 3 * cond, y: 4 }; r = s + b.x + b.y; } return r; }`, 37},
		// CROSS-BLOCK reuse: loop-body donor `a` reused by if-nested recipient `b`. 31.
		{"cross-block-reuse", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; if (i > 0) { var b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + s; i = i + 1; } return sum; }`, 31},
		// Cross-block with recipients in BOTH arms and a single donor — one arm reuses
		// it, the other allocates; value stays correct across the loop. 66.
		{"cross-block-both-arms", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: 1 }; if (i % 2 == 0) { var b: P = P { x: i, y: 10 }; sum = sum + b.x + b.y; } else { var c: P = P { x: i, y: 20 }; sum = sum + c.x + c.y; } i = i + 1; } return sum; }`, 66},
		// Cross-block TUPLE recipient: loop-body tuple donor reused by if-nested tuple. 31.
		{"cross-block-tuple-reuse", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; if (i > 0) { var b: (i32, i32) = (i, 3); sum = sum + b.0 + b.1; } sum = sum + s; i = i + 1; } return sum; }`, 31},
		// Higher iteration count: the prior-release must free each loop-carried box
		// (a double-free would trap). 2000 iters, value kept small (sum mod 100 = 0).
		{"loop-struct-churn", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 2000) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 100; i = i + 1; } return sum; }`, 0},
		// IN-ARM consuming-match reuse (#4350 slice 5 wasm coverage): the runtime-
		// guarded FBIP match — x's box reused (or a guarded fresh box) per arm.
		// Scalar payloads: V(3,4) -> W(4,5) -> 9.
		{"inarm-match-reuse", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9},
		// In-arm ARRAY MOVE: the same-slot binding moves the payload array into the
		// result box (cow-guard: old==new, no dec). 4+10+20+30 = 64.
		{"inarm-match-reuse-array-move", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64},
		// In-arm ARRAY REPLACE: a fresh literal replaces the old payload array
		// (cow-guard: old!=new, old dec'd on the reuse arm). 3+7+8 = 18.
		{"inarm-match-reuse-array-replace", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } return r; } function main(): i32 { return go(); }`, 18},
		// ENUM->ENUM cross-local reuse (#4350 slice 5 wasm coverage): dead donor
		// `a = A([1,2])`'s box reused for `c = B([3,4])` behind the guard; the
		// donor's old payload array released on the reuse arm. t=5, v=7 -> 12.
		{"enum-cross-reuse", `enum E { A(i32[]), B(i32[]) } function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; } function main(): i32 { return f(); }`, 12},
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
			if !strings.Contains(string(wat), "$__fern_str_box") {
				t.Errorf("%q did not reach the IR box path (no box in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("loop reuse wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
