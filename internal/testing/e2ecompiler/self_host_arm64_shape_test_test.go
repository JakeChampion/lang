package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Each variant of Shape has its own tag. kind tests the tag of each, so the
// program fails when a tag test takes the wrong arm.
const shapeTestProg = `enum Shape { Dot, Line(i64), Box(i64, i64), Cube(i64, i64, i64) }

@noinline function kind(s: Shape): i64 {
  return match (s) { Dot => 1i64, Line(a) => 10i64 + a, Box(a, b) => 100i64 + a + b, Cube(a, b, c) => 1000i64 + a + b + c };
}

@noinline function is_box(s: Shape): boolean {
  return match (s) { Box(_, _) => true, _ => false };
}

function main(): i32 {
  let shapes = [Dot, Line(2i64), Box(3i64, 4i64), Cube(5i64, 6i64, 7i64)];
  let total = 0i64;
  let boxes = 0;
  for s in shapes {
    total += kind(s);
    if (is_box(s)) {
      boxes += 1;
    }
  }
  if (total != 1i64 + 12i64 + 107i64 + 1018i64 || boxes != 1) {
    return 1;
  }
  return 0;
}
`

// On arm64 a test of a shape tag subtracts the high 12 bits of the tag and
// compares with the low 12 bits. It does not build the tag in a register.
func TestSelfHostArm64ShapeTestTakesTheTag(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(shapeTestProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	split := regexp.MustCompile(`sub x5, x\d+, #\d+, lsl #12\n\s+cmp x5, #\d+\n`)
	built := regexp.MustCompile(`movk w\d+, #\d+, lsl #16\n\s+cmp x\d+, x\d+\n`)
	for _, name := range []string{"kind", "is_box"} {
		body := tailCallBody(t, string(asm), name)
		if !split.MatchString(body) {
			t.Errorf("%s does not test a tag with sub and cmp:\n%s", name, body)
		}
		if built.MatchString(body) {
			t.Errorf("%s builds a tag in a register to compare it:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, shapeTestProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
