package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostLoopReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host loop-reuse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"loop-struct-reuse", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`, 40},
		{"loop-tuple-reuse", `function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; let b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`, 34},
		{"loop-tuple-donor-live", `function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let b: (i32, i32) = (i, 3); sum = sum + a.0 + a.1 + b.0 + b.1; i = i + 1; } return sum; }`, 34},
		{"loop-funcupdate-reuse", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: P = P { x: i, y: 0 }; let c: P = P { ...d, y: 3 }; sum = sum + c.x + c.y; i = i + 1; } return sum; }`, 18},
		{"if-arm-reuse", `struct P { x: i32, y: i32 } function main(): i32 { let cond: i32 = 1; let r: i32 = 0; if (cond > 0) { let a: P = P { x: 10 * cond, y: 20 }; let s: i32 = a.x + a.y; let b: P = P { x: 3 * cond, y: 4 }; r = s + b.x + b.y; } return r; }`, 37},
		{"cross-block-reuse", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + s; i = i + 1; } return sum; }`, 31},
		{"cross-block-both-arms", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: 1 }; if (i % 2 == 0) { let b: P = P { x: i, y: 10 }; sum = sum + b.x + b.y; } else { let c: P = P { x: i, y: 20 }; sum = sum + c.x + c.y; } i = i + 1; } return sum; }`, 66},
		{"cross-block-tuple-reuse", `function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; if (i > 0) { let b: (i32, i32) = (i, 3); sum = sum + b.0 + b.1; } sum = sum + s; i = i + 1; } return sum; }`, 31},
		{"loop-struct-churn", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 2000) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 100; i = i + 1; } return sum; }`, 0},
		{"inarm-match-reuse", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9},
		{"inarm-match-reuse-array-move", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64},
		{"inarm-match-reuse-array-replace", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } return r; } function main(): i32 { return go(); }`, 18},
		{"enum-cross-reuse", `enum E { A(i32[]), B(i32[]) } function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; } function main(): i32 { return f(); }`, 12},
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
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
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
