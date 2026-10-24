package e2e

import (
	"strconv"
	"strings"
	"testing"
)

func TestFloatSemantics_PortableSubset(t *testing.T) {
	src := `function main(): i32 {
		let a: f32 = 3.5;
		let b: f32 = 1.5;
		if ((a + b) * 2.0 > 9.0) {
			return 7;
		}
		return 0;
	}`
	const want = 7

	t.Run("arm64-linux", func(t *testing.T) {
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
	t.Run("wasm32-wasi", func(t *testing.T) {
		core := buildWasmCore(t, src)
		stdout, stderr, ec := runWasmArtifact(t, core, runOpts{})
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

func TestFloatDefaultWidthF64(t *testing.T) {
	src := `import "std/f64";
function main(): i32 {
	if ((1.0 / 3.0).to_string() != "0.3333333333333333") { return 1; }
	let x: f64 = 1.0;
	if ((x / 3.0).to_string() != "0.3333333333333333") { return 2; }
	if ((1.0 as f32 / 3.0 as f32).to_string() != "0.33333334") { return 3; }
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
	t.Run("wasm32-wasi", func(t *testing.T) {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("wasm exit=%d, want 0", got)
		}
	})
}
