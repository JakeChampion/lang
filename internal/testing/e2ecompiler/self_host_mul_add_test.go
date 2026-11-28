package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The product of sum, of diff, of narrow and of hash feeds one addition or
// one subtraction. reversed subtracts from the product, and twice reads the
// product two times.
const mulAddProg = `@noinline function sum(a: i64, b: i64, c: i64): i64 { return a * b + c; }
@noinline function diff(a: i64, b: i64, c: i64): i64 { return c - a * b; }
@noinline function reversed(a: i64, b: i64, c: i64): i64 { return a * b - c; }
@noinline function twice(a: i64, b: i64, c: i64): i64 { let p = a * b; return p + c + p; }
@noinline function narrow(a: i32, b: i32, c: i32): i32 { return a * b + c; }
@noinline function hash(xs: i32[]): i32 {
  let h = 7;
  for x in xs { h = h * 31 + x; }
  return h;
}
function main(): i32 {
  if (sum(3i64, 4i64, 5i64) != 17i64) { return 1; }
  if (sum(0i64 - 3i64, 4i64, 5i64) != 0i64 - 7i64) { return 2; }
  if (diff(3i64, 4i64, 50i64) != 38i64) { return 3; }
  if (reversed(3i64, 4i64, 5i64) != 7i64) { return 4; }
  if (twice(3i64, 4i64, 5i64) != 29i64) { return 5; }
  if (narrow(65536, 65536, 7) != 7) { return 6; }
  if (narrow(0 - 46341, 46341, 1) != 2147479016) { return 7; }
  if (hash([1, 2, 3]) != 209563) { return 8; }
  return 0;
}
`

// On arm64 a multiplication that feeds only the addition or the subtraction
// after it is one madd or one msub. A product that is the left operand of a
// subtraction, or that has two readers, keeps its mul.
func TestSelfHostMultiplyThenAddIsOneInstruction(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(mulAddProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{
		{"sum", "    madd "}, {"diff", "    msub "}, {"narrow", "    madd "}, {"hash", "    madd "},
	} {
		body := tailCallBody(t, string(arm), tc.name)
		if !strings.Contains(body, tc.want) || strings.Contains(body, "    mul ") {
			t.Errorf("arm64: %s wants%sand no mul:\n%s", tc.name, tc.want, body)
		}
	}
	for _, name := range []string{"reversed", "twice"} {
		body := tailCallBody(t, string(arm), name)
		if !strings.Contains(body, "    mul ") || strings.Contains(body, "    madd ") || strings.Contains(body, "    msub ") {
			t.Errorf("arm64: %s wants a mul and no madd or msub:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, mulAddProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
