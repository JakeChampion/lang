package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTupleEnumScrutineeIRX86_64 pins a tuple-element access used
// DIRECTLY as a match scrutinee where the element is a generic enum:
// `match (t.0) { Has(v) => …, Non => … }` with `t: (Opt[i32], i32)`. The
// monomorphiser recovers a scrutinee's generic-enum instantiation from its
// type (to rewrite the arm patterns to the concrete `Variant__<key>` names),
// but me_scrutinee_type's ExprFieldAccess arm resolved only NAMED struct
// fields — a tuple index `t.N` fell through to `me_field_type_of` (struct-only)
// and resolved "", so the arms stayed un-mangled and the match read the wrong
// variant (0 instead of 42). me_scrutinee_type now returns a tuple element's
// type spelling (me_tuple_elem_type, depth-aware split), so these shapes lower
// on the IR path (.Lir_ witness) with the native value. Var-extracted tuple
// elements and named-field scrutinees already worked; kept as regressions.
func TestSelfHostTupleEnumScrutineeIRX86_64(t *testing.T) {
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
		// Generic enum in tuple position 0, matched directly (the bug).
		{"tuple-elem0-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var t: (Opt[i32], i32) = (Has(40), 2); match (t.0) { Has(v) => { return v + t.1; }, Non => { return 0; } } }`,
			42},
		// Generic enum in tuple position 1.
		{"tuple-elem1-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var t: (i32, Opt[i32]) = (2, Has(40)); match (t.1) { Has(v) => { return v + t.0; }, Non => { return 0; } } }`,
			42},
		// Middle element of a three-element tuple.
		{"tuple-elem-mid-of-three",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var t: (i32, Opt[i32], i32) = (1, Has(39), 2); match (t.1) { Has(v) => { return v + t.0 + t.2; }, Non => { return 0; } } }`,
			42},
		// String-payload generic enum as a tuple element.
		{"tuple-elem-string-payload",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var t: (Opt[string], i32) = (Has("abcd"), 3); match (t.0) { Has(s) => { return s.len() + t.1 + 35; }, Non => { return 0; } } }`,
			42},
		// Regression: var-extract the tuple element then match.
		{"tuple-elem-var-extracted",
			`enum Opt[T] { Non, Has(T) } function main(): i32 { var t: (Opt[i32], i32) = (Has(40), 2); var e: Opt[i32] = t.0; match (e) { Has(v) => { return v + t.1; }, Non => { return 0; } } }`,
			42},
		// Regression: named struct-field generic-enum scrutinee.
		{"struct-field-generic-enum-scrutinee",
			`enum Opt[T] { Non, Has(T) } struct Box { o: Opt[i32] } function main(): i32 { var b: Box = Box { o: Has(42) }; match (b.o) { Has(v) => { return v; }, Non => { return 0; } } }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the tuple-enum scrutinee fell back to the AST path", tc.name)
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
