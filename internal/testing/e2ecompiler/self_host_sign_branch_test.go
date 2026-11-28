package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// below and floor branch on the sign of an i64, narrow on the sign of an
// i32, and down counts to zero with `i >= 0` as its test. One arm of below
// and of floor makes a call, so the branch does not become a select.
// positive tests `x > 0`, which the sign bit alone does not answer.
const signBranchProg = `@noinline function negated(x: i64): i64 { return 0i64 - x; }
@noinline function below(x: i64): i64 { if (x < 0i64) { return negated(x); } return x + 1i64; }
@noinline function floor(x: i64): i64 { if (x >= 0i64) { return negated(0i64 - x * 3i64); } return 7i64; }
@noinline function narrow(x: i32): i32 { if (x < 0) { return 0 - x; } return x + 1; }
@noinline function positive(x: i64): i64 { if (x > 0i64) { return 1i64; } return 2i64; }
@noinline function down(xs: i32[]): i32 {
  let s = 0;
  let i = xs.len() - 1;
  while (i >= 0) { s = s * 3 + xs[i]; i -= 1; }
  return s;
}
function main(): i32 {
  if (below(0i64 - 5i64) != 5i64 || below(0i64) != 1i64 || below(9i64) != 10i64) { return 1; }
  if (floor(0i64 - 1i64) != 7i64 || floor(0i64) != 0i64 || floor(4i64) != 12i64) { return 2; }
  if (floor(0i64 - 9223372036854775807i64 - 1i64) != 7i64 || below(9223372036854775806i64) != 9223372036854775807i64) { return 3; }
  if (narrow(0 - 5) != 5 || narrow(0) != 1 || narrow(2147483646) != 2147483647 || narrow(0 - 2147483647) != 2147483647) { return 4; }
  if (positive(0i64) != 2i64 || positive(1i64) != 1i64 || positive(0i64 - 1i64) != 2i64) { return 5; }
  if (down([1, 2, 3]) != 34 || down([]) != 0) { return 6; }
  return 0;
}
`

var (
	signBitBranch = regexp.MustCompile(`(?m)^\s+tbn?z [xw]\d+, #(63|31), `)
	compareToZero = regexp.MustCompile(`(?m)^\s+cmp [xw]\d+, #0$`)
)

// On arm64 a branch on `a < 0` or `a >= 0` tests the sign bit with one tbnz
// or tbz. It does not compare with zero first.
func TestSelfHostSignTestIsOneBranch(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(signBranchProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"below", "floor", "narrow", "down"} {
		body := tailCallBody(t, string(arm), name)
		if !signBitBranch.MatchString(body) {
			t.Errorf("arm64: %s has no branch on the sign bit:\n%s", name, body)
		}
		if compareToZero.MatchString(body) {
			t.Errorf("arm64: %s compares with zero:\n%s", name, body)
		}
	}
	if body := tailCallBody(t, string(arm), "positive"); signBitBranch.MatchString(body) || !compareToZero.MatchString(body) {
		t.Errorf("arm64: positive wants a compare with zero and no branch on the sign bit:\n%s", body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, signBranchProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
