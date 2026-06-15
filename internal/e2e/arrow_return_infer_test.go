package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Arrow lambdas written WITHOUT a return-type annotation — `(x) => expr` —
// infer their return type from the body expression (#2663 ergonomics). Before
// this, an unannotated arrow defaulted to a void return, so passing it where a
// `(T) => U` callback was expected failed ("returns void but expression is U")
// and combinators forced the verbose `(x): U => expr` form. This makes
// `xs.map((x) => x * 2).filter((x) => x > 2)` read naturally.
//
// `apply_i` takes an `(i32) => i32` and `keep` an `(i32) => boolean`; passing
// unannotated arrows to each exercises i32- and boolean-return inference.
// (Non-generic higher-order fns so the x86-64 e2e harness — which skips
// monomorph — can compile it; the generic-stdlib `xs.map((x) => …)` form this
// unblocks is covered on interp/arm64/wasm via the combinator suites.)
// apply_i((x) => x*2, 16) = 32; keep((x) => x > 5, 9) = 10. 32 + 10 = 42.
const arrowInferSrc = `function apply_i(f: (i32) => i32, x: i32): i32 { return f(x); }
function keep(f: (i32) => boolean, x: i32): i32 { if (f(x)) { return 10; } return 0; }
function main(): i32 {
    var a: i32 = apply_i((x: i32) => x * 2, 16);
    var b: i32 = keep((x: i32) => x > 5, 9);
    return a + b;
}
`

func TestInterpArrowReturnInfer(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(arrowInferSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ArrowReturnInfer(t *testing.T) {
	if out, code := compileAndRunX86_64(t, arrowInferSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64ArrowReturnInfer(t *testing.T) {
	if out, code := compileAndRunArm64(t, arrowInferSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMArrowReturnInfer(t *testing.T) {
	if code := runWasm(t, arrowInferSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
