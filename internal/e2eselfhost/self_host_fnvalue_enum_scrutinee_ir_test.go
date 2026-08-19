package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFnValueEnumScrutineeIRX86_64 pins a fn-value (closure) call used
// DIRECTLY as a match scrutinee where it returns a generic enum:
// `match (f(k)) { Has(v) => …, Non => … }` with `f: (i32) => Opt[i32]`. A
// fn-typed param / local coarsens to the bare spelling "fn" (its return type
// held in the ParamDecl's fn_ret, or the init lambda's ret_type), so the
// monomorphiser's env carried "fn" with no return type and me_scrutinee_type
// couldn't recover the scrutinee's generic-enum instantiation — the arms
// stayed un-mangled against the concrete enum and the match read the wrong
// variant (0 instead of 42). me_env_from_params and the StmtVar binding now
// encode a fn value as "fn => <ret>" (the me_bind_pat_vars fn-payload
// convention), so the scrutinee recovers its instantiation and the arms
// rewrite. The fix restores VALUE correctness on whichever backend path the
// module lowers on; the param cases reach the IR path (asserted), the
// closure-local case still bails (value only).
func TestSelfHostFnValueEnumScrutineeIRX86_64(t *testing.T) {
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
		name   string
		src    string
		want   int
		wantIR bool
	}{
		// Fn-value PARAM returning a generic enum, matched directly (the bug).
		{"fn-param-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function run(f: (i32) => Opt[i32]): i32 { match (f(7)) { Has(v) => { return v; }, Non => { return 0; } } } function main(): i32 { return run(function (k: i32): Opt[i32] { return Has(k * 6); }); }`,
			42, true},
		// Fn-value param, string-payload generic enum.
		{"fn-param-string-payload-scrutinee",
			`enum Opt[T] { Non, Has(T) } function run(f: (i32) => Opt[string]): i32 { match (f(1)) { Has(s) => { return s.len() + 40; }, Non => { return 0; } } } function main(): i32 { return run(function (k: i32): Opt[string] { return Has("xx"); }); }`,
			42, true},
		// Fn-value param, Non branch taken.
		{"fn-param-non-branch-scrutinee",
			`enum Opt[T] { Non, Has(T) } function run(f: (i32) => Opt[i32], k: i32): i32 { match (f(k)) { Has(v) => { return v; }, Non => { return 42; } } } function main(): i32 { return run(function (k: i32): Opt[i32] { if (k > 0) { return Has(k); } return Non; }, 0); }`,
			42, true},
		// Fn-value LOCAL (closure) returning a generic enum, matched directly.
		// The local closure still bails the module, but the fix
		// restores the correct value there.
		{"fn-local-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var f: (i32) => Opt[i32] = function (k: i32): Opt[i32] { if (k > 0) { return Has(k * 6); } return Non; }; match (f(7)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42, false},
		// Regression: var-bind the closure result then match.
		{"fn-value-result-var-bound",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var f: (i32) => Opt[i32] = function (k: i32): Opt[i32] { return Has(k * 6); }; var r: Opt[i32] = f(7); match (r) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if tc.wantIR && !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the fn-value enum scrutinee did not lower through the IR", tc.name)
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
