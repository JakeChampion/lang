// Float-semantics end-to-end test. Pins the portable subset of
// Fern's f32 / f64 surface (docs/FLOAT-SEMANTICS.md) by running
// the same program through every available backend and asserting
// they agree on the result, plus the #5363 defaults: a bare float
// literal is f64 and `float` is the f64 alias.
//
// The portable subset documented today: ordinary arithmetic,
// ordinary comparisons, in-range float-to-int truncation.
// Specifically NOT tested: NaN bit-patterns, sign-of-zero
// discrimination through arithmetic, denormal handling,
// out-of-range float-to-int — those are platform-dependent
// by spec.

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

// TestFloatDefaultWidthF64 pins the #5363 decision at runtime: an
// unannotated float expression evaluates and dispatches at f64
// precision (1.0/3.0 renders with std/float's shortest-round-trip f64
// formatter — 0.3333333333333333, #5536), a `float`-annotated value
// behaves identically (the alias IS f64), and an explicit f32 stays at
// the 7-digit f32 rendering — on the interpreter, native x86-64, and wasm.
func TestFloatDefaultWidthF64(t *testing.T) {
	src := `import "std/float";
function main(): i32 {
	if ((1.0 / 3.0).to_string() != "0.3333333333333333") { return 1; }
	var x: float = 1.0;
	if ((x / 3.0).to_string() != "0.3333333333333333") { return 2; }
	if ((1.0 as f32 / 3.0 as f32).to_string() != "0.3333333") { return 3; }
	return 0;
}`
	t.Run("interp", func(t *testing.T) {
		if got := runInterpExit(t, src); got != 0 {
			t.Errorf("interp exit=%d, want 0", got)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64(t, src)
		if code != 0 {
			t.Errorf("x86_64 exit=%d, want 0", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("wasm exit=%d, want 0", got)
		}
	})
}
