package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The bound 20000 fits one arm64 mov and no immediate of a compare. The loop
// of fill makes a call, so the bound does not move in front of the loop. The
// loop of sum reads the bound in its body too.
const rotateConstantProg = `@noinline function fill(n: i32): i32[] {
  let xs: i32[] = [];
  for i in 0..20000 { xs = xs.append(i * 7 + n); }
  return xs;
}
@noinline function sum(xs: i32[]): i64 {
  let s = 0i64;
  for i in 0..20000 { s += xs[i] as i64 + 20000i64; }
  return s;
}
function main(): i32 {
  let xs = fill(1);
  if (xs.len() != 20000) { return 1; }
  if (sum(xs) != 1799950000i64) { return 2; }
  return 0;
}
`

var rotatedConstantTest = regexp.MustCompile(`mov (x\d+), #20000\n\s+cmp x\d+, (x\d+)\n\s+b\.lt `)

// On arm64 a loop whose head loads a constant for its test has the test at
// its latch too: the latch loads the constant again, compares, and branches
// to the body. Without this the latch jumps to the head, one branch more for
// each trip.
func TestSelfHostLoopWithConstantBoundRotates(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(rotateConstantProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(arm), "fill")
	m := rotatedConstantTest.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("arm64: the latch of fill does not load 20000, compare and branch to the body:\n%s", body)
	}
	if m[1] != m[2] {
		t.Errorf("arm64: the latch of fill loads 20000 into %s and compares with %s:\n%s", m[1], m[2], body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, rotateConstantProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
