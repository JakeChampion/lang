package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMethodEnumScrutineeIRX86_64 pins a method call that returns a
// generic enum used DIRECTLY as a match scrutinee: `match (s.find(k)) { … }`.
// The monomorphiser recovers a match scrutinee's generic-enum instantiation
// (to rewrite the arm patterns to the concrete `Variant__<key>` names) from
// the scrutinee expression's type — but me_scrutinee_type's ExprCall arm only
// handled a FREE-function callee, so a METHOD-call scrutinee resolved "" and
// the arms stayed un-mangled against the concrete enum: the match read the
// wrong variant and returned the wrong arm (0 instead of 42). me_scrutinee_
// type now resolves a method call's return type from the receiver's type +
// the method's declared return, so these shapes lower on the IR path (.Lir_
// witness) with the native value. Free-function scrutinees and var-bound
// method results already worked; kept as regressions.
func TestSelfHostMethodEnumScrutineeIRX86_64(t *testing.T) {
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
		// Method returning a generic enum, matched directly (the bug).
		{"method-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[i32] { if (k == s.n) { return Has(s.n * 2); } return Non; } function main(): i32 { var s: S = S { n: 21 }; match (s.find(21)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
		// Same shape but both branches carry a payload (no bare unit variant) —
		// isolates the scrutinee-type recovery from the unit-variant path.
		{"method-scrutinee-both-payload",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[i32] { if (k == s.n) { return Has(s.n * 2); } return Has(0); } function main(): i32 { var s: S = S { n: 21 }; match (s.find(21)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
		// String-payload generic enum from a method, struct-literal receiver.
		{"method-string-payload-scrutinee",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[string] { if (k == s.n) { return Has("hi"); } return Non; } function main(): i32 { match (S { n: 5 }.find(5)) { Has(v) => { return v.len() + 40; }, Non => { return 0; } } }`,
			42},
		// Regression: free-function-returning-generic-enum scrutinee (worked).
		{"free-fn-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function find(k: i32, n: i32): Opt[i32] { if (k == n) { return Has(n * 2); } return Non; } function main(): i32 { match (find(21, 21)) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
		// Regression: method result var-bound then matched (worked).
		{"method-result-var-bound",
			`enum Opt[T] { Non, Has(T) } struct S { n: i32 } function (s: S) find(k: i32): Opt[i32] { if (k == s.n) { return Has(s.n * 2); } return Non; } function main(): i32 { var s: S = S { n: 21 }; var r: Opt[i32] = s.find(21); match (r) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the method-enum scrutinee fell back to the AST path", tc.name)
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
