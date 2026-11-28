package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bound of count does not fit an arm64 immediate, and scaled multiplies
// by a constant. The loop of calling makes a call that returns.
const constantHoistProg = `@noinline function count(n: i64): i64 {
  let s = 0i64;
  for i in 0i64..3000000i64 { s += i ^ n; }
  return s;
}
@noinline function scaled(xs: i32[]): i32 {
  let s = 0;
  for x in xs { s += x * 3329; }
  return s;
}
@noinline function tick(t: i64): i64 { return t + 1i64; }
@noinline function calling(n: i64): i64 {
  let s = 0i64;
  for i in 0i64..100000i64 { s = tick(s) + n; }
  return s;
}
function main(): i32 {
  if (count(0i64) != 4499998500000i64) { return 1; }
  if (scaled([1, 2, 3]) != 19974) { return 2; }
  if (calling(2i64) != 300000i64) { return 3; }
  return 0;
}
`

// On arm64 a constant that costs an instruction in each trip moves in front
// of a loop that makes no call that returns: a value that is not an
// immediate, or an operand of a multiplication. A constant stays in a loop
// with a call, because no register that a call does not keep can hold it
// there. x86-64 does not run the pass, and the bound stays an immediate of
// the compare.
func TestSelfHostCostlyConstantLeavesTheLoop(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(constantHoistProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, constant string
		inLoop         bool
	}{{"count", "=3000000", false}, {"scaled", "#3329", false}, {"calling", "=100000", true}} {
		body := tailCallBody(t, string(arm), tc.name)
		if !strings.Contains(body, tc.constant) {
			t.Fatalf("arm64: %s does not make its constant as %s, so the test does not see it:\n%s", tc.name, tc.constant, body)
		}
		loops := loopBodies(body)
		if len(loops) == 0 {
			t.Fatalf("arm64: no loop in %s:\n%s", tc.name, body)
		}
		for _, l := range loops {
			if strings.Contains(l, tc.constant) != tc.inLoop {
				t.Errorf("arm64: the loop of %s makes its constant: %t, want %t:\n%s", tc.name, !tc.inLoop, tc.inLoop, body)
			}
		}
	}
	x86, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(x86), "count")
	immediate := false
	for _, l := range loopBodies(body) {
		immediate = immediate || strings.Contains(l, "$3000000")
	}
	if !immediate {
		t.Errorf("x86-64: the loop of count does not compare with the immediate $3000000:\n%s", body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, constantHoistProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
