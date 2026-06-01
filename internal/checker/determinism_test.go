package checker

// Determinism guard for diagnostic reporting.
//
// The checker accumulates diagnostics into a flat slice (c.errors)
// during its walk and returns them as a diag.Errors aggregate with no
// sorting step — the reported order IS the walk order. Diagnostic
// ordering is the single most regression-prone surface for error UX:
// users (and the test suite, and the LSP) read the *first* error
// first, golden-output tests pin the sequence, and a reorder is an
// invisible-until-it-bites change. Yet nothing pinned that a given
// invalid program reports the same diagnostics in the same order on
// every run.
//
// The risk is concrete: checking walks Go maps along the way (the
// builtin-injection dedup tables, struct/enum/func symbol tables,
// method-set maps), and Go map iteration order is randomized. If any
// diagnostic is emitted from inside a map range — or if the order of
// two errors depends on map-driven resolution order — the reported
// sequence would shuffle run-to-run. This test locks the current
// (verified-stable) behaviour so a future change that introduces
// map-order dependence fails loudly here instead of as flaky golden
// diffs elsewhere.

import (
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// errorString runs parse + check on src and returns the aggregated
// diagnostic text. It requires src to be invalid (Check must return a
// non-nil error); a program that suddenly type-checks would mask a
// reordering, so that's a test failure, not a skip.
func errorString(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, cerr := Check(prog)
	if cerr == nil {
		t.Fatalf("expected a type error, program checked clean:\n%s", src)
	}
	return cerr.Error()
}

// errorMatrix is a set of programs that each produce multiple
// diagnostics, biased toward the map-walking resolution paths where
// ordering could drift: several offending top-level functions,
// multiple bad struct fields, and a mix of error kinds (return
// mismatch, undefined identifier, bad assignment, non-boolean
// condition) whose relative order is what we're pinning.
var errorMatrix = map[string]string{
	"two_errors_one_func": `
function f(): i32 {
	return true;
	var x: i32 = unknownThing;
}`,

	"errors_across_funcs": `
function a(): i32 { return true; }
function b(): boolean { return 1; }
function c(): i32 { return missing; }`,

	"bad_struct_fields": `
struct S { a: i32, b: i32, c: i32 }
function main(): i32 {
	var s: S = S { a: true, b: missing, c: false };
	return 0;
}`,

	"mixed_error_kinds": `
function f(n: i32): i32 {
	if (n) { return 0; }
	var x: i32 = true;
	return undefinedThing;
}`,

	"multiple_undefineds": `
function main(): i32 {
	var a: i32 = one;
	var b: i32 = two;
	var c: i32 = three;
	return a + b + c;
}`,
}

// TestDiagnosticOrderDeterministic checks each invalid program several
// times and asserts the aggregated diagnostic text is byte-identical
// to the first run. A failure means diagnostic ordering has become
// nondeterministic (most likely a diagnostic emitted from inside a Go
// map range, or error order made dependent on map-driven resolution) —
// which would surface elsewhere as flaky golden-output tests and an
// inconsistent first-error experience for users and the LSP.
func TestDiagnosticOrderDeterministic(t *testing.T) {
	for name, src := range errorMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			want := errorString(t, src)
			for i := 0; i < 12; i++ {
				got := errorString(t, src)
				if got != want {
					t.Fatalf("diagnostic output not deterministic on run %d:\n--- first run ---\n%s\n--- run %d ---\n%s",
						i+2, want, i+2, got)
				}
			}
		})
	}
}
