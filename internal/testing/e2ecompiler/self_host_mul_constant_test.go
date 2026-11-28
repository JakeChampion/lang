package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shifted multiplies by powers of two and by one more than a power of two.
// plain multiplies by 10, which neither form covers. shared adds 8 to a
// product by 8. The i32 products of narrow wrap.
const mulConstantProg = `@noinline function shifted(x: i64, y: i64): i64 {
  return x * 5i64 + y * 8i64 + (x ^ y) * 9i64 + (x & y) * 3i64 + (x | y) * 4294967296i64 + 2i64 * x;
}
@noinline function plain(x: i64): i64 { return x * 10i64; }
@noinline function shared(x: i64): i64 { return x * 8i64 + 8i64; }
@noinline function narrow(x: i32, y: i32): i32 { return x * 65537 + y * 1024 + (x - y) * 3; }
function main(): i32 {
  if (shifted(3i64, 4i64) != 30064771188i64) { return 1; }
  if (shifted(0i64 - 7i64, 11i64) != 0i64 - 21474836540i64) { return 2; }
  if (plain(0i64 - 12i64) != 0i64 - 120i64) { return 3; }
  if (shared(5i64) != 48i64) { return 4; }
  if (narrow(2147483647, 2097153) != 2141127673) { return 5; }
  if (narrow(0 - 5, 7) != 0 - 320553) { return 6; }
  return 0;
}
`

// On arm64 a multiplication by 2^k is one lsl, and a multiplication by
// 2^k + 1 is one add of the value and the value shifted. The constant takes
// no register and no instruction.
func TestSelfHostMultiplyByConstantIsAShift(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(mulConstantProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	shifted := tailCallBody(t, string(arm), "shifted")
	if strings.Contains(shifted, "    mul ") {
		t.Errorf("arm64: shifted has a mul:\n%s", shifted)
	}
	for _, want := range []string{", lsl #2\n", ", #3\n", ", lsl #3\n", ", lsl #1\n", ", #32\n", ", #1\n"} {
		if !strings.Contains(shifted, want) {
			t.Errorf("arm64: shifted has no instruction that ends with %q:\n%s", want, shifted)
		}
	}
	if body := tailCallBody(t, string(arm), "plain"); !strings.Contains(body, "    mul ") {
		t.Errorf("arm64: plain has no mul, so the test does not show which constants keep it:\n%s", body)
	}
	for _, name := range []string{"shared", "narrow"} {
		if body := tailCallBody(t, string(arm), name); strings.Contains(body, "    mul ") {
			t.Errorf("arm64: %s has a mul:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, mulConstantProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
