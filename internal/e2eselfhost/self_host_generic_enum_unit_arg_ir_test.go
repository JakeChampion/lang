package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostGenericEnumUnitArgIRX86_64 pins the generic-enum unit-variant
// call-argument family (#5247). A generic enum's unit (payload-less) variant
// has no payload to infer its type arg from, so it must pin its instantiation
// from an `expected` type; monomorphize_enums otherwise drops the generic
// variant struct and the dangling bare reference emits "unresolved ident" + a
// null arg → SIGSEGV at the callee's match.
//
// The BARE form `get(Non)` pins from the callee's parameter type flowed into
// the argument (me_expr's ExprCall arm + func_param_type_at). The QUALIFIED
// form `get(Opt.Non)` pins the same `expected` in me_expr's ExprFieldAccess
// arm — the delta this test's `qualified-unit-variant-arg` case guards. All
// cases lower via the IR path (.Lir_ witness) and compute the native value;
// non-generic enums, annotated-var binds, and payload-bearing variant args are
// kept as regressions.
func TestSelfHostGenericEnumUnitArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Qualified `Opt.Non` as a direct call argument — this PR's delta.
		{"qualified-unit-variant-arg",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32]): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return get(Opt.Non); }`,
			42},
		// Bare unit variant `Non` as a direct call argument.
		{"bare-unit-variant-arg",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32]): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return get(Non); }`,
			42},
		// String-payload generic enum — the bare unit variant still pins from
		// the string-typed parameter.
		{"string-payload-enum-bare-arg",
			`enum Opt[T] { Non, Has(T) } function first(o: Opt[string]): i32 { match (o) { Has(s) => { return s.len(); }, Non => { return 9; } } } function main(): i32 { return first(Non) + 33; }`,
			42},
		// Two generic-enum parameters, both bare `Non` args.
		{"two-unit-variant-args",
			`enum Opt[T] { Non, Has(T) } function pick(a: Opt[i32], b: Opt[i32]): i32 { match (a) { Has(v) => { return v; }, Non => { match (b) { Has(w) => { return w; }, Non => { return 42; } } } } } function main(): i32 { return pick(Non, Non); }`,
			42},
		// Mixed: one Full-payload call + one bare-Non call, summed.
		{"mixed-payload-and-unit-args",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32], d: i32): i32 { match (o) { Has(v) => { return v; }, Non => { return d; } } } function main(): i32 { return get(Has(30), 0) + get(Non, 12); }`,
			42},
		// Regression: non-generic enum, bare unit variant as arg.
		{"non-generic-unit-variant-arg",
			`enum Opt { Has(i32), Non } function get(o: Opt): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return get(Non); }`,
			42},
		// Regression: generic enum via annotated var bind.
		{"var-annotated-unit-variant",
			`enum Opt[T] { Non, Has(T) } function get(o: Opt[i32]): i32 { match (o) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { var e: Opt[i32] = Non; return get(e); }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the generic-enum unit-variant arg fell back to the AST path", tc.name)
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
