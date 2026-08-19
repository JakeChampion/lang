package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostNestedArrFirstElemIR pins the type-driven arr-of-arr
// classification at a `var m = [<first>, …]` binding (#5326): the old
// detection recognised only a LITERAL first element (`[[…], …]`), so a
// call-shaped (`[mk(), …]`) or ident-shaped (`[inner, …]`) first element —
// array-typed by the return/slot registries — left `m` un-arr-arr-marked and
// nested reads m[i][j] took the 4-byte default stride. For f64/string inner
// elements that was a silent wrong-value miscompile on the self-host IR path
// (found differentially vs the native interp oracle; native backends were
// always type-driven via ast.ElemSizeBytesFor). The binding now classifies the
// first element through expr_is_arr_src (literal, array-marked local,
// registered T[]-returning call, slice), so these shapes stride correctly.
func TestSelfHostNestedArrFirstElemIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		// f64[][] whose first element is a registered f64[]-returning call:
		// 1.5 + 2.5 + 0.25 = 4.25 -> 42. Pre-fix: inner reads truncated to
		// 4-byte stride -> 7.
		{"f64-call-first",
			`function mk(): f64[] { return [1.5, 2.5]; }
function main(): i32 { var m = [mk(), [0.25]]; var s = m[0][0] + m[0][1] + m[1][0]; if (s > 4.24 && s < 4.26) { return 42; } return 7; }`, 42},
		// f64[][] whose first element is an f64[]-marked local.
		{"f64-ident-first",
			`function mk(): f64[] { return [1.5, 2.5]; }
function main(): i32 { var inner = mk(); var m = [inner, [0.25]]; var s = m[0][0] + m[0][1] + m[1][0]; if (s > 4.24 && s < 4.26) { return 42; } return 7; }`, 42},
		// string[][] whose first element is a string[]-returning call: nested
		// element .len() must dispatch str_len (2+3+1 = 6 -> 42). Pre-fix the
		// inner element read the array header as a string box -> 7.
		{"string-call-first",
			`function mk(): string[] { return ["ab", "cde"]; }
function main(): i32 { var m = [mk(), ["f"]]; var s = m[0][0].len() + m[0][1].len() + m[1][0].len(); if (s == 6) { return 42; } return 7; }`, 42},
		// i64[][] whose first element is an i64[]-returning call: 8-byte inner
		// loads (values past 2^32).
		{"i64-call-first",
			`function mk(): i64[] { return [5000000000, 2]; }
function main(): i32 { var m = [mk(), [1 as i64]]; if (m[0][0] + m[0][1] + m[1][0] == 5000000003) { return 42; } return 7; }`, 42},
		// The literal-first shape that always worked — pinned against
		// regression by the same classifier now serving all four shapes.
		{"f64-literal-first",
			`function main(): i32 { var m = [[1.5, 2.5], [0.25]]; var s = m[0][0] + m[0][1] + m[1][0]; if (s > 4.24 && s < 4.26) { return 42; } return 7; }`, 42},
		// UNANNOTATED array-returning function (#5326, second cluster): the
		// ret-type inferencer had no ExprArray arm, so `mk2` never entered
		// f64arr_ret_fns and `var a = mk2()` element-reads took the 4-byte
		// default stride (silent wrong value pre-fix).
		{"f64-unannotated-ret",
			`function mk2() { return [1.5, 2.5]; }
function main(): i32 { var a = mk2(); var s = a[0] + a[1]; if (s > 3.99 && s < 4.01) { return 42; } return 7; }`, 42},
		{"string-unannotated-ret",
			`function mk3() { return ["ab", "cde"]; }
function main(): i32 { var a = mk3(); if (a[0].len() + a[1].len() == 5) { return 42; } return 7; }`, 42},
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
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: did not lower through the IR (no .Lir_ labels)", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
