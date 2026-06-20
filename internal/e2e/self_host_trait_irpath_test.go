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
// Frontier (post to_string-builtin slice):
//   - Concrete struct-impl methods + monomorphised struct/primitive bounded
//     generics + parametric struct impls + primitive-receiver methods + ENUM
//     methods on an enum-typed LOCAL/param + struct-ARRAY element dispatch +
//     short-circuit `&&`/`||` with a call/index RHS + the i32/string
//     `.to_string()` builtin (so every @derive(Display) — struct, enum, and
//     generic — and the string-building parametric impl) all lower through the
//     IR path. The i32 helper is __fern_i32_to_string (a stack-ABI body on the
//     register backends; $__fern_i32_to_str on wasm).
//   - Enum methods called DIRECTLY on a variant construction (`Has(5).eq(…)`)
//     or a unit variant (`Nil.eq(…)`) now lower through the IR path too: the
//     parser records each variant's owning enum on its desugared StructDecl
//     (`enum_owner`), and irlower's `expr_enum_type` recovers it to dispatch
//     `<Enum>.<method>` with the fresh variant as the receiver.
//   - `@derive(Json)` on a struct + enum lowers through the IR path: the
//     synthesised `to_json` is structurally identical to the Display
//     `to_string` body (string concat + `match` + per-field/-payload
//     `.to_json()` dispatch), so it rides the same IR machinery already
//     proven for Display. Externally-tagged enums render unit variants as a
//     quoted name and single-payload variants as a one-key object.
//   - `@derive(Debug)` and `@derive(Hash)` on a struct + enum lower through
//     the IR path too. Debug is the structural sibling of Display (string
//     fields render quoted via the emitter-intrinsic render), so it rides the
//     same machinery. Hash is the seeded fold `h = h*31 + f.hash()` (struct)
//     / variant-tag-seeded fold (enum), the same match + arithmetic + method
//     dispatch shape already proven for the derived Eq/Ord.
//   - `dyn Trait` method dispatch now lowers through the IR path: a
//     `dyn Trait` / `dyn Trait[]` param/loop-var carries the coarse
//     "dyn <Trait>" type, and a `x.method()` call emits op_dyn_dispatch — the
//     backend reads the receiver's runtime shape pointer and dispatches to the
//     matching `<ConcreteType>.<method>` via a compare-branch chain over the
//     trait's impl types (mirroring the AST emitter).
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
	"trait-dyn-object-heterogeneous":             "ir",
	"trait-struct-array-loop-method":             "ir",
	"trait-derive-struct-eq":                     "ir",
	"trait-derive-struct-ord":                    "ir",
	"trait-derive-struct-display-nested":         "ir",
	"trait-enum-method":                          "ir",
	"trait-enum-method-unannot-local":            "ir",
	"trait-enum-method-unannot-payloadless":      "ir",
	"trait-derive-enum-display":                  "ir",
	"trait-derive-enum-eq":                       "ir",
	"trait-derive-enum-ord":                      "ir",
	"trait-derive-struct-json":                   "ir",
	"trait-derive-enum-json":                     "ir",
	"trait-derive-struct-debug":                  "ir",
	"trait-derive-enum-debug":                    "ir",
	"trait-derive-struct-hash":                   "ir",
	"trait-derive-enum-hash":                     "ir",
	"trait-generic-struct-derive-display-i32":    "ir",
	"trait-generic-struct-derive-display-string": "ir",
	"trait-generic-struct-derive-display-both":   "ir",
	"trait-generic-struct-derive-eq":             "ir",
	"trait-generic-struct-derive-ord":            "ir",
	"trait-generic-struct-parametric-impl":       "ir",
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
