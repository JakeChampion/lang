package e2e

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// updateDiagGolden regenerates the golden files under
// testdata/diag_golden/ instead of asserting against them. Run
// `go test ./internal/e2e -run TestDiagnosticGolden -update-diag-golden`
// after a deliberate phrasing change, then review the diff.
var updateDiagGolden = flag.Bool("update-diag-golden", false, "regenerate diagnostic golden files")

// diagnosticGoldenCases pin the END-TO-END rendered form of a representative
// spread of diagnostics — the "product copy" a user actually sees from
// `fern -check` (which renders through diag.Format, the same call this test
// makes). Unlike internal/diag/diag_test.go, which unit-tests the renderer
// on synthetic Diagnostic structs, this exercises the real
// parser/modload → checker → diag.Format pipeline, so an accidental change
// to a message string, a code, a caret column, or a suggestion is caught as
// a golden diff rather than sailing through (#4413 Rec §10). Each program is
// single-file (no imports) so the rendered filename is fully controlled by
// the stable name passed to diag.Format below.
var diagnosticGoldenCases = []struct {
	name string
	src  string
}{
	// E001 undefined identifier, with the near-miss machine-applicable fix
	// (`help: replace …`) and the multi-char squiggle under the name.
	{"E001_undefined_suggestion", "function main(): i32 { var count = 1; return kount; }\n"},
	// E002 return-type mismatch (declared i32, returns string).
	{"E002_return_type", "function f(): i32 { return \"x\"; }\nfunction main(): i32 { return 0; }\n"},
	// E004 free-function call arity.
	{"E004_arg_count", "function g(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return g(1); }\n"},
	// E005 struct literal missing a declared field.
	{"E005_missing_field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p.x; }\n"},
	// E006 function redeclared.
	{"E006_redeclared", "function dup(): i32 { return 1; }\nfunction dup(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n"},
	// E008 non-boolean if condition.
	{"E008_nonbool_if", "function main(): i32 { if (3) { return 1; } return 0; }\n"},
	// E021 generic-bound conformance at a call site (#4842): a struct with no
	// `impl Ord` passed to `pick[T: Ord]`.
	{"E021_bound_conformance", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Foo { x: i32 }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { var p: Foo = Foo { x: 1 }; var r: Foo = pick(p); return r.x; }\n"},
	// E052 missing return (non-void body falls off the end).
	{"E052_missing_return", "function f(): i32 { var x = 1; }\nfunction main(): i32 { return 0; }\n"},
	// P001 parser error (a stray operator) — pins that parse-time diagnostics
	// render through the same path as checker ones.
	{"P001_parse_error", "function main(): i32 { return 1 +; }\n"},
	// A broad spread across the emitted surface so a phrasing regression in
	// any common shape surfaces as a golden diff (#4413 Rec §10).
	{"E003_assign_mismatch", "function main(): i32 { var x: i32 = \"s\"; return x; }\n"},
	{"E011_break_outside_loop", "function main(): i32 { break; return 0; }\n"},
	{"E013_dup_var", "function main(): i32 { var x = 1; var x = 2; return x; }\n"},
	{"E019_generic_arity", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
	{"E020_empty_array_annot", "function main(): i32 { var a = []; return 0; }\n"},
	{"E040_typearg_arity", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id[i32, i32](1); }\n"},
	{"E043_unknown_field", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p.y; }\n"},
	{"E048_immutable_field", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; p.x = 2; return p.x; }\n"},
	{"E063_slice_escape", "function f(): [i32] { var xs: i32[] = [1, 2, 3]; return xs[0:2]; }\nfunction main(): i32 { return 0; }\n"},
	{"E064_unknown_type", "function f(a: Wibble): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
	// Rarer but distinctly-phrased shapes.
	{"E024_tuple_destructure", "function main(): i32 { var (a, b) = 5; return a; }\n"},
	{"E026_wildcard_not_last", "function main(): i32 { var x = 1; match (x) { _ => { return 0; }, 1 => { return 1; } } }\n"},
	{"E033_invalid_cast", "function main(): i32 { var b: boolean = true; return b as i32; }\n"},
	{"E037_slice_bound", "function main(): i32 { var a = [1, 2, 3]; var s = a[\"x\":2]; return 0; }\n"},
	{"E041_eq_mismatch", "function main(): i32 { if (\"a\" == 1) { return 1; } return 0; }\n"},
	{"E046_tuple_index_oor", "function main(): i32 { var t = (1, 2); return t.5; }\n"},
	{"E047_int_overflow", "function main(): i32 { return 9999999999; }\n"},
	{"E055_discarded_result", "function main(): i32 { var a: i32[] = [1]; a.append(2); return 0; }\n"},
	{"E058_labeled_break", "function main(): i32 { var c = 0; while (c < 3) { c = c + 1; if (c == 2) { break nope; } } return c; }\n"},
	{"E061_value_block_no_tail", "function main(): i32 { var x = if (1 < 2) { print(\"hi\"); } else { 2 }; return 0; }\n"},
}

func TestDiagnosticGolden(t *testing.T) {
	goldenDir := filepath.Join("testdata", "diag_golden")
	if *updateDiagGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
	}
	for _, tc := range diagnosticGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderDiagnostic(t, tc.name, tc.src)
			if got == "" {
				t.Fatalf("%s produced no diagnostic — the case no longer exercises its error shape", tc.name)
			}
			goldenPath := filepath.Join(goldenDir, tc.name+".golden")
			if *updateDiagGolden {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update-diag-golden to create)", goldenPath, err)
			}
			if got != string(want) {
				t.Errorf("%s diagnostic drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, string(want))
			}
		})
	}
}

// renderDiagnostic loads + checks src and renders whatever diagnostic it
// produces through diag.Format, using a STABLE filename (name.fern) so the
// golden output doesn't embed the test's temp path. A parse/load failure is
// rendered directly (it carries the position); otherwise the checker's error
// is rendered. Returns "" when the program is clean.
func renderDiagnostic(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	display := name + ".fern"
	prog, _, loadErr := modload.Load(path)
	if loadErr != nil {
		return diag.Format(display, src, loadErr)
	}
	if _, chkErr := checker.Check(prog); chkErr != nil {
		return diag.Format(display, src, chkErr)
	}
	return ""
}
