package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The modern-form rule finds an annotation that the initializer already
// gives, `x = x op e` and `0 - e`. It leaves an annotation that changes the
// type of a literal, a field target, and `0 - 1`, which does not fit an
// unsigned type as `-1`.
func TestModernFormRule(t *testing.T) {
	src := `function f(k: i32): i32 {
  let a: i32 = 5;
  let s: string = "x";
  let w: u64 = 0;
  let n = k;
  n = n + 1;
  n = 0 - n;
  let u: u32 = 0 - 1;
  return n + a + s.len() + (w as i32) + (u as i32);
}
`
	dir := t.TempDir()
	path := dir + "/m.fern"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-lint", "-lint-set", "modern-form=warn", "-lint-set", "cyclomatic-complexity=allow", path)
	cmd.Env = e2eharness.ChildEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fern -lint: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"m.fern:2:3: warning[modern-form]: the initializer of `a` is already i32",
		"m.fern:3:3: warning[modern-form]: the initializer of `s` is already string",
		"m.fern:6:3: warning[modern-form]: `n = n + ...` repeats `n`",
		"m.fern:7:7: warning[modern-form]: `0 - e` subtracts from zero",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "warning[modern-form]"); n != 4 {
		t.Errorf("want 4 findings, got %d:\n%s", n, got)
	}
}
