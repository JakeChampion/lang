// Float-semantics end-to-end test. Pins the portable subset of
// Lang's f32 / f64 surface (docs/FLOAT-SEMANTICS.md) by running
// the same program through every available backend and asserting
// they agree on the result.
//
// The portable subset documented today: ordinary arithmetic,
// ordinary comparisons, in-range float-to-int truncation.
// Specifically NOT tested: NaN bit-patterns, sign-of-zero
// discrimination through arithmetic, denormal handling,
// out-of-range float-to-int — those are platform-dependent
// by spec.
//
// The doc additionally recommends `x != x` as the IEEE NaN-test
// idiom and `x > 1.0e38f32` as the Inf range check. Neither is
// exercised cross-backend here yet because the interpreter
// doesn't support FloatLit (see `*ast.FloatLit` in the interp's
// expression switch) and the parser doesn't accept scientific-
// notation float literals (`1.0e38`). Both gaps are real but
// orthogonal to this doc; the e2e coverage will extend once
// they're closed.

package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// TestFloatSemantics_PortableSubset runs a small program through
// arm64 / x86_64 / wasm and asserts every available backend
// returns the documented exit code. Exercises ordinary arithmetic
// + comparison, which the doc guarantees as bit-identical across
// every backend.
//
// Each backend skips individually when its toolchain is missing
// (mirrors the diff-oracle's skip behaviour).
func TestFloatSemantics_PortableSubset(t *testing.T) {
	src := `function main(): i32 {
		var a: f32 = 3.5;
		var b: f32 = 1.5;
		if ((a + b) * 2.0 > 9.0) {
			return 7;
		}
		return 0;
	}`
	const want = 7

	t.Run("arm64", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		if code != want {
			t.Errorf("arm64 exit=%d, want %d\nsrc:\n%s", code, want, src)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64(t, src)
		if code != want {
			t.Errorf("x86_64 exit=%d, want %d\nsrc:\n%s", code, want, src)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		componentPath := buildComponent(t, src)
		stdout, stderr, ec := runComponent(t, componentPath, runOpts{})
		if ec != 0 {
			t.Fatalf("wasmtime exit=%d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
		}
		got, err := strconv.Atoi(strings.TrimSpace(stdout))
		if err != nil {
			t.Fatalf("parse wasm stdout %q: %v", strings.TrimSpace(stdout), err)
		}
		if got != want {
			t.Errorf("wasm result=%d, want %d\nsrc:\n%s", got, want, src)
		}
	})
}
