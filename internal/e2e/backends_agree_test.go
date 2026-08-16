// One runner for "compile this on every backend and check what it prints".
//
// There used to be three near-identical copies of the loop below —
// dynAllBackends, dynCompiledBackends and dropAllCompiled — differing only in
// where the expectation came from. Three names for one operation meant picking
// between them was a judgement call at every new test, and the third was added
// by copying the second.
package e2e

import (
	"strings"
	"testing"
)

// backendsAgree runs src on wasm, x86-64 and arm64 and asserts each one's
// stdout equals want.
//
// Where `want` comes from is the whole design, and it belongs at the CALL SITE
// rather than buried in a helper name:
//
//	backendsAgree(t, src, interpOracle(t, src, "v=105"))  // differential
//	backendsAgree(t, src, "65/7")                         // compiled-only
//
// The first form is the default and much the stronger test: the interpreter is
// the reference semantics, so the backends are checked against a second
// implementation rather than against a string someone typed. Reach for the
// second form ONLY when the interpreter cannot model the program's observable
// behaviour, and say why in the test's own comment. Two such classes exist
// today, both documented rather than discovered:
//
//   - **Reclamation.** The interpreter has no refcounts (interp.go — "the
//     interpreter has no refcounts to underflow"), so no value reaches
//     rc-zero and a `core/mem.Drop` finalizer never runs there (#2705).
//   - **Integer width behind `dyn`.** Its width-less Number tags every integer
//     "i32", so `dyn` over i64 cannot dispatch (interp.valueTypeName,
//     docs/DYN-TRAITS.md §4.1).
//
// A literal expectation is a weaker test, so the rule is narrow on purpose: a
// feature the interpreter CAN model has no business skipping it.
func backendsAgree(t *testing.T, src, want string) {
	t.Helper()
	t.Run("wasm32-wasi", func(t *testing.T) {
		if got := runWasmCapturingStdout(t, src); got != want {
			t.Errorf("wasm = %q, want %q", got, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("x86-64 = %q, want %q", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("arm64 = %q, want %q", got, want)
		}
	})
}

// interpOracle runs src on the interpreter and returns its stdout, for feeding
// to backendsAgree as the expectation.
//
// `want` pins the oracle itself: without it a test passes whenever the
// interpreter and all three backends agree, including when they have drifted
// together away from what the test was written to prove. Failing here rather
// than in a backend subtest also localises the break — a changed baseline is a
// different bug from a backend that disagrees with it.
func interpOracle(t *testing.T, src, want string) string {
	t.Helper()
	got := dynInterpStdout(t, src)
	if got != want {
		t.Fatalf("interp baseline = %q, want %q", got, want)
	}
	return got
}
