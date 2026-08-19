package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostSSALiftDifferential is the differential gate for the stack-IR ->
// SSA lift: it cross-checks the lift codegen path against an INDEPENDENT oracle
// — the native tree-walking interpreter. For each program it (a) runs an
// equivalent Fern source through `fern -interp` (reference semantics, computed
// by a completely separate implementation), and (b) lifts the hand-coded
// ir.Op[] form, emits x86-64 via ssa_lift_emit_run, assembles and runs it.
// The two exit codes must agree. Where TestSelfHostSSALiftEmit pins the lift
// path against hand-derived constants, this pins it against the interpreter —
// so a miscompile in lift_from_ir, ssa.optimize, or ssa_x86 surfaces as a
// divergence from reference semantics rather than having to be predicted up
// front. The Op[] and the source are equivalent by construction (same program,
// two front-ends); the interpreter is the source of truth for the value.
func TestSelfHostSSALiftDifferential(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_lift_emit_run.fern")
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_lift_emit_run.fern", "ssa_lift_emit_run")

	liftEmitRun := func(t *testing.T, prog string) int {
		t.Helper()
		asm, err := exec.Command(bin, prog).Output()
		if err != nil {
			t.Fatalf("emit driver failed for %q: %v", prog, err)
		}
		asmPath := filepath.Join(dir, "diff_"+prog+".s")
		binPath := filepath.Join(dir, "diff_"+prog)
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(x86gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed: %v\n%s\n--- asm ---\n%s", err, out, asm)
		}
		cmd := exec.Command(binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally")
		}
		return cmd.ProcessState.ExitCode()
	}

	// Each program's hand-coded ir.Op[] (named in the driver) paired with an
	// equivalent Fern source for the interpreter. Equivalent by construction.
	cases := []struct {
		prog string
		src  string
	}{
		{"arith", `function main(): i32 { return (3 + 4) * 2 - 1; }`},
		{"loopsum", `function main(): i32 { var i = 1; var acc = 0; while (i <= 5) { acc = acc + i; i = i + 1; } return acc; }`},
		{"branch", `function main(): i32 { var x = 0; if (7 > 3) { x = 42; } return x; }`},
		{"breakloop", `function main(): i32 { var i = 0; while (i < 100) { if (i == 42) { break; } i = i + 1; } return i; }`},
		{"callsum", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }`},
		{"factrec", `function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }`},
		{"arrsum", `function main(): i32 { var a = [5, 10, 15]; return a[0] + a[1] + a[2]; }`},
		{"structsum", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 10, y: 32 }; return p.x + p.y; }`},
		{"tuplesum", `function main(): i32 { var t = (10, 32); return t.0 + t.1; }`},
		{"f64cmp", `function main(): i32 { var x: f64 = 1.5; var y: f64 = 2.0; if (x * y + 0.5 > 3.0) { return 1; } return 0; }`},
		{"castrt", `function main(): i32 { var n: i32 = 10; var x: f64 = (n as f64) * 1.5; return x as i32; }`},
		{"strcat", `function main(): i32 { return ("foo" + "bar").len(); }`},
		{"strbuf", `function main(): i32 { strbuf_reset(); strbuf_append("ab"); strbuf_append("cde"); return strbuf_take().len(); }`},
		{"exitprog", `function main(): i32 { exit(42); return 0; }`},
		{"strindex", `function main(): i32 { return "ABC"[1] as i32; }`},
		{"optval", `function main(): i32 { match (Some(42)) { Some(v) => { return v; }, None => { return 0; } } }`},
		{"argslen", `function main(): i32 { return args().len(); }`},
		{"closure", `function main(): i32 { var x = 10; var f = (y: i32) => x + y; return f(5); }`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.prog, func(t *testing.T) {
			ref := runInterpExit(t, tc.src)
			got := liftEmitRun(t, tc.prog)
			if got != ref {
				t.Errorf("lift->emit->run %s = %d, interp reference = %d (divergence from reference semantics)", tc.prog, got, ref)
			}
		})
	}
}
