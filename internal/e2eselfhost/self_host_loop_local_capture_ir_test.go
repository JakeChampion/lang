package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLoopLocalCaptureIRX86_64 pins the loop-local capturing-closure
// lift (closure_lift_one's nested-body recursion): a `let f = <capturing
// lambda>` that is only CALLED (not escaping) is param-lifted to a hoisted
// `__lam_N` + direct call sites. Before, closure_lift_one scanned only the
// TOP-LEVEL function body, so a capturing lambda bound inside a `while` / `for`
// / `if` / `match` body was never lifted and the whole module fell to the AST
// path — correct, but off the IR path. closure_lift_one now recurses into
// nested loop / conditional / match bodies (captures still resolve against the
// whole fd, so a capture declared outside the loop types fine), so these common
// shapes lower via the IR path (asserted via the .Lssa_main / .Lssa_g witness)
// and compute the native values.
func TestSelfHostLoopLocalCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	cases := []struct {
		name      string
		src       string
		want      int
		irWitness string
	}{
		// while-loop body, capturing a loop-external plain local.
		{"while-plain-local-capture",
			`function main(): i32 { let base: i32 = 100; let acc: i32 = 0; let i: i32 = 0; while (i < 3) { let f: (i32) => i32 = (n: i32): i32 => { return n + base; }; acc = acc + f(i); i = i + 1; } return acc - 297; }`,
			6, ".Lssa_main"},
		// while-loop body, capturing a struct FIELD (c.base).
		{"while-struct-field-capture",
			`struct Ctx { base: i32 } function main(): i32 { let c: Ctx = Ctx { base: 100 }; let acc: i32 = 0; let i: i32 = 0; while (i < 3) { let f: (i32) => i32 = (n: i32): i32 => { return n + c.base; }; acc = acc + f(i); i = i + 1; } return acc - 297; }`,
			6, ".Lssa_main"},
		// for-in loop body, capturing a loop-external local.
		{"for-loop-local-capture",
			`function main(): i32 { let base: i32 = 100; let xs: i32[] = [1, 2, 3]; let acc: i32 = 0; for x in xs { let f: (i32) => i32 = (n: i32): i32 => { return n + base; }; acc = acc + f(x); } return acc - 300; }`,
			6, ".Lssa_main"},
		// if-branch body, capturing a local.
		{"if-branch-capture",
			`function main(): i32 { let base: i32 = 40; let b: boolean = true; if (b) { let f: (i32) => i32 = (n: i32): i32 => { return n + base; }; return f(2); } return 0; }`,
			42, ".Lssa_main"},
		// match-arm body, capturing an outer local AND the arm payload binding.
		{"match-arm-capture",
			`function main(): i32 { let base: i32 = 40; let o: Option[i32] = Some(2); match (o) { Some(k) => { let f: (i32) => i32 = (n: i32): i32 => { return n + base + k; }; return f(0); }, None => { return 0; } } }`,
			42, ".Lssa_main"},
		// doubly-nested while, capturing across both loop levels.
		{"nested-while-capture",
			`function main(): i32 { let base: i32 = 10; let acc: i32 = 0; let i: i32 = 0; while (i < 2) { let j: i32 = 0; while (j < 3) { let f: (i32) => i32 = (n: i32): i32 => { return n + base; }; acc = acc + f(1); j = j + 1; } i = i + 1; } return acc - 60; }`,
			6, ".Lssa_main"},
		// loop-local capturing closure in a non-main function (g) — witnesses
		// the lift is per-function, not main-special.
		{"for-loop-capture-in-fn",
			`@noinline function g(base: i32): i32 { let xs: i32[] = [1, 2, 3]; let acc: i32 = 0; for x in xs { let f: (i32) => i32 = (n: i32): i32 => { return n + base; }; acc = acc + f(x); } return acc; } function main(): i32 { return g(100) - 300; }`,
			6, ".Lssa_g"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), tc.irWitness) {
				t.Fatalf("%s: emitted asm missing %q — the loop-local capture did not lower through the IR", tc.name, tc.irWitness)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
