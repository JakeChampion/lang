package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// traitIRPath records, for every case in traitsCases, WHICH backend path the
// self-hosted x86-64 compiler routes it through: "ir" (the stack-IR path,
// asm_ir.emit_module_ir) or "ast" (the legacy AST emitter). The trait cases
// themselves only assert exit codes, so without this gate a regression that
// silently kicked a trait program off the IR path — or, conversely, a change
// that made one newly IR-eligible — would go unnoticed. This is the
// observability the IR-trait-support work needs: it pins the current frontier
// so each widening of the IR subset (primitive receivers, enum receivers, the
// derive helpers, …) shows up as an intentional ast->ir flip here.
//
// The path decision is probed via the asm_pathprobe_run driver, which runs the
// EXACT production pipeline (parser.module_with_builtins → lift_lambdas →
// asm_ir.all_eligible — what emit_module checks) and prints "ir"/"ast" without
// emitting any assembly, so the gate is fast and assembler-free.
//
// Frontier (post short-circuit-&&/|| slice):
//   - Concrete struct-impl methods + monomorphised struct/primitive bounded
//     generics + parametric struct impls + primitive-receiver methods + ENUM
//     methods on an enum-typed LOCAL/param + struct-ARRAY element dispatch +
//     short-circuit `&&`/`||` with a call/index RHS (the field-wise derived Eq
//     shape) all lower through the IR path.
//   - `dyn Trait`, enum methods called directly on a variant construction
//     (`Has(5).eq(…)`, which needs the variant->enum map), and the
//     string-building @derive Display (the `.to_string()` builtin has no IR
//     runtime body) still fall back to the AST emitter — the next slices.
var traitIRPath = map[string]string{
	"trait-impl-method":                          "ir",
	"trait-impl-arg":                             "ir",
	"trait-two-impls":                            "ir",
	"trait-bounded-generic-monotype":             "ir",
	"trait-bounded-generic-multitype":            "ir",
	"trait-bounded-generic-primitive":            "ir",
	"trait-bounded-generic-mixed":                "ir",
	"trait-bounded-generic-array-elem":           "ir",
	"trait-bounded-generic-two-params":           "ir",
	"trait-parametric-impl-struct-elem":          "ir",
	"trait-dyn-object-heterogeneous":             "ast",
	"trait-struct-array-loop-method":             "ir",
	"trait-derive-struct-eq":                     "ir",
	"trait-derive-struct-ord":                    "ir",
	"trait-derive-struct-display-nested":         "ast",
	"trait-enum-method":                          "ir",
	"trait-derive-enum-display":                  "ast",
	"trait-derive-enum-eq":                       "ast",
	"trait-derive-enum-ord":                      "ast",
	"trait-generic-struct-derive-display-i32":    "ast",
	"trait-generic-struct-derive-display-string": "ast",
	"trait-generic-struct-derive-display-both":   "ast",
	"trait-generic-struct-derive-eq":             "ir",
	"trait-generic-struct-derive-ord":            "ir",
	"trait-generic-struct-parametric-impl":       "ast",
}

// TestSelfHostTraitIRPathX86_64 asserts the IR-vs-AST routing for every trait
// case matches traitIRPath. Every case in traitsCases must have an entry (a new
// trait case with no declared path fails the test), so the frontier can't drift
// silently. Pairs with TestSelfHostTraitsX86_64, which proves the chosen path
// produces the correct exit code.
func TestSelfHostTraitIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := traitIRPath[tc.name]
			if !ok {
				t.Fatalf("trait case %q has no traitIRPath entry — declare its IR/AST path", tc.name)
			}
			out := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if out != want {
				t.Errorf("%s routed through %q path, want %q", tc.name, out, want)
			}
		})
	}
}
