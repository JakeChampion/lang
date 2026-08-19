package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrReclaimWasmIR proves fresh-string reclaim (#2649) lowers
// correctly on the self-hosted WASM IR backend and — crucially — that it never
// DOUBLE-FREES. Unlike the header-less asm string boxes, a wasm heap string is a
// single rc-headered inline block ($__fern_str_box), so __fern_str_free maps to
// $__fern_arr_dec, whose over-release detector ($__fern_rc_underflow) ticks on any
// dec below rc 0. Each case runs the string churn in a helper (lowered through the
// IR reclaim path) and checks __rc_underflow_count() == 0 in main: a nonzero
// count (a double-free) surfaces as exit 99. Exit codes stay < 126 (WASI _start).
func TestSelfHostStrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string-reclaim wasm IR e2e")
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
		// Fresh concat reclaimed each iteration, 2000 iters. churn returns sum%100
		// (kept 0); main returns 99 if any string was double-freed (underflow > 0).
		// "ab"+"cd" = len 4; sum stays a multiple of 4 → % 100 hits 0 at 2000 iters.
		{"loop-concat", `function churn(n: i32): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < n) { var s: string = "ab" + "cd"; sum = (sum + s.len()) % 100; i = i + 1; } return sum; } function main(): i32 { var r: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 0},
		// Fresh .to_ascii_upper() reclaimed each iteration. base len 3; sum kept mod 100.
		{"loop-to-upper", `function churn(n: i32): i32 { var base: string = "xyz"; var sum: i32 = 0; var i: i32 = 0; while (i < n) { var s: string = base.to_ascii_upper(); sum = (sum + s.len()) % 100; i = i + 1; } return sum; } function main(): i32 { var r: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 0},
		// Scope-exit reclaim of a single fresh string (no loop): freed once at return.
		// A double free would tick underflow → 99. len("hi"+"!") = 3.
		{"scope-exit", `function churn(): i32 { var s: string = "hi" + "!"; return s.len(); } function main(): i32 { var r: i32 = churn(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 3},
		// Aliased fresh string must NOT be reclaimed (would double-free the shared
		// block) — the analysis excludes it; underflow stays 0, value correct. 3+3=6.
		{"aliased-safe", `function churn(): i32 { var s: string = "ab" + "c"; var t: string = s; return s.len() + t.len(); } function main(): i32 { var r: i32 = churn(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 6},
		// i32_to_string reclaimed each iteration. This one is VALUE-only (no
		// __fern_rc_underflow_count in the module): the underflow builtin routes
		// i32_to_string in every function to the legacy AST wasm path — which lacks
		// the $i32_to_string helper (a legacy AST gap, not the IR reclaim) — so it
		// must stay out of a module exercising i32_to_string on the IR path. Pure IR
		// here: the loop reclaims s each iteration (proven double-free-safe by the
		// concat/to_upper underflow cases above — the identical $__fern_arr_dec
		// mechanism on an rc-headered str_box block). i in 0..49: 10*1 + 40*2 = 90.
		{"loop-i32-to-string", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 50) { var s: string = i32_to_string(i); sum = sum + s.len(); i = i + 1; } return sum; }`, 90},
		// Un-annotated chr(..) reclaimed each iteration (the rc-header fix makes the
		// wasm chr block reclaimable). Value-only. 20 iters * len 1 = 20.
		{"unannotated-chr", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 20) { var s = chr(65 + i); sum = sum + s.len(); i = i + 1; } return sum; }`, 20},
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
				t.Errorf("string reclaim wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
