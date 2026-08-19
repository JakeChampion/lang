package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostTupleWideCallElem pins a tuple element that comes from a call
// returning i64 or f64 to the IR path (#5902).
//
// tuple_elem_ctor_eligible's call arm admitted a free-fn call only when the
// callee was in NO wide-return registry, scoping itself to "a statically-known
// SCALAR (one-i32-slot) result". But i64/f64 are one-slot scalars too, and the
// width machinery was already there and already used by the other element
// forms: elem_type_tag classifies "i64" (infer_expr_width == 64) / "f64"
// (expr_is_f64), the construction loop routes an i64 element through lower_i64,
// and op_tuple_make_k stores each element at its own width. So a wide LOCAL or
// wide ARITHMETIC element lowered while only the CALL form bailed the whole
// enclosing module.
//
// Each case asserts BOTH halves of what the fix has to deliver:
//
//   - path: the module must lower on the IR path (the coverage half — goal 1).
//     A change that merely produced the right answer by some other route
//     would satisfy the value check while losing exactly what this is for.
//   - value: the tuple element must read back correctly, checked against the
//     same computation spelled without the tuple, so it asserts agreement
//     rather than a hard-coded constant.
//
// The i32 / string / local / arithmetic rows are controls: they lowered on the
// IR path before this change and must continue to.
func TestSelfHostTupleWideCallElem(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	asmRun := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probe := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range []struct {
		name string
		prog string
	}{
		{
			"i64-call-elem",
			`function fr(n: i32): i64 { return (n as i64) * 3000000000; }
			 function main(): i32 { var a: i32 = 2; var t: (i64, i32) = (fr(a), 1); if (t.0 == fr(a) && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			"f64-call-elem",
			`function fr(n: i32): f64 { return (n as f64) / 3.0; }
			 function main(): i32 { var a: i32 = 2; var t: (f64, i32) = (fr(a), 1); if (f64_bits(t.0) == f64_bits(fr(a)) && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			// Both elements wide, and of different widths, so the per-element
			// kind string has to carry each one independently.
			"mixed-i64-f64-call-elems",
			`function a1(n: i32): i64 { return (n as i64) * 3000000000; }
			 function a2(n: i32): f64 { return (n as f64) / 3.0; }
			 function main(): i32 { var t: (i64, f64) = (a1(2), a2(2)); if (t.0 == a1(2) && f64_bits(t.1) == f64_bits(a2(2))) { return 7; } return 9; }`,
		},
		{
			// The callee spelled as a METHOD rather than a free function. The
			// element itself is one i32 slot; what made it bail was the callee
			// form, so the wide sibling in the tuple is what keeps the case on
			// this suite's subject.
			"method-i32-call-elem",
			`struct P { x: i32, y: i32 }
			 function (p: P) sum(): i32 { return p.x + p.y; }
			 function main(): i32 { var p: P = P { x: 3, y: 4 }; var t: (i32, i64) = (p.sum(), 165i64); if (t.0 == 7 && t.1 == 165) { return 7; } return 9; }`,
		},
		{
			"method-i64-call-elem",
			`struct P { x: i32 }
			 function (p: P) wide(): i64 { return (p.x as i64) * 3000000000; }
			 function main(): i32 { var p: P = P { x: 2 }; var t: (i64, i32) = (p.wide(), 1); if (t.0 == p.wide() && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			"method-f64-call-elem",
			`struct P { x: i32 }
			 function (p: P) frac(): f64 { return (p.x as f64) / 3.0; }
			 function main(): i32 { var p: P = P { x: 2 }; var t: (f64, i32) = (p.frac(), 1); if (f64_bits(t.0) == f64_bits(p.frac()) && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			// A builtin receiver, which the struct/enum method registry cannot
			// answer for — the length is an i32 whatever it measures.
			"len-call-elem",
			`function main(): i32 { var s: string = "xyz"; var t: (i32, i64) = (s.len(), 165i64); if (t.0 == 3 && t.1 == 165) { return 7; } return 9; }`,
		},
		// Controls — already on the IR path before this change.
		{
			"i64-local-elem-control",
			`function main(): i32 { var w: i64 = 6000000000; var t: (i64, i32) = (w, 1); if (t.0 == w && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			"i64-arith-elem-control",
			`function main(): i32 { var w: i64 = 3000000000; var t: (i64, i32) = (w * 2, 1); if (t.0 == w * 2 && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			"i32-call-elem-control",
			`function fr(n: i32): i32 { return n * 3; }
			 function main(): i32 { var a: i32 = 2; var t: (i32, i32) = (fr(a), 1); if (t.0 == fr(a) && t.1 == 1) { return 7; } return 9; }`,
		},
		{
			"string-call-elem-control",
			`function fr(n: i32): string { return "xy"; }
			 function main(): i32 { var t: (string, i32) = (fr(1), 1); if (t.0.len() == 2 && t.1 == 1) { return 7; } return 9; }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.TrimSpace(string(runCapture(t, gcc, runner, probe, []byte(tc.prog)))); got != "ir" {
				t.Fatalf("path = %q, want \"ir\" — the module is not on the IR path, so this case covers nothing", got)
			}
			asm := runCapture(t, gcc, runner, asmRun, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "tupwide_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 7 {
				t.Errorf("exit = %d, want 7 (9 = the tuple element read back wrong)", code)
			}
		})
	}
}
