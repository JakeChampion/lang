package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// poly makes six fused multiply-adds. The conformance case f64_fma checks
// their values on each backend.
const fmaPolySrc = `import "std/f64";

@noinline
function poly(x: f64, c0: f64, c1: f64, c2: f64, c3: f64): f64 {
  let a = __fma_f64(c3, x, c2);
  a = __fma_f64(a, x, c1);
  a = __fma_f64(x, a, c0);
  let b = __fma_f64(a, a, x);
  return __fma_f64(b, x, a) + __fma_f64(c0, c1, b);
}

function main(): i32 {
  print(poly(1.0000001, 0.1, 0.2, 0.3, 0.4).to_string());
  return 0;
}
`

// fmaSection gives the lines after the line that starts with start, up to the
// first line after it that contains end.
func fmaSection(t *testing.T, asm, start, end string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), start) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.Contains(lines[j], end) {
				return strings.Join(lines[i+1:j], "\n")
			}
		}
	}
	t.Fatalf("no %q section in the emitted code", start)
	return ""
}

// Each native backend gives __fma_f64 one fused instruction, and wasm calls
// the software form of the runtime, because wasm has no fused multiply-add.
func TestSelfHostFmaF64Emission(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(fmaPolySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, start, end, fused, separate string
	}{
		{e2eharness.TargetX86_64Linux, "__fn_poly:", ".cfi_endproc", "vfmadd231sd", "mulsd"},
		{e2eharness.TargetArm64Linux, "__fn_poly:", ".cfi_endproc", "fmadd d", "fmul"},
		{e2eharness.TargetWasm32Wasi, "(func $poly ", "(func ", "call $__fern_fma_f64", "f64.mul"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			asm := e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), c.target, src)
			body := fmaSection(t, asm, c.start, c.end)
			if n := strings.Count(body, c.fused); n != 6 {
				t.Fatalf("poly has %d of %q, want 6:\n%s", n, c.fused, body)
			}
			if strings.Contains(body, c.separate) {
				t.Fatalf("poly rounds a product with %q:\n%s", c.separate, body)
			}
		})
	}
}
