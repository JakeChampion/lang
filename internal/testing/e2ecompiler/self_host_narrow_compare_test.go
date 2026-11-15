package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// differ and below each compare an i32 with a sum or a difference that wraps
// to 32 bits. main gives them operands at the two ends of the range of i32.
const narrowCompareProg = `@noinline function note(n: i32): i32 {
  return n + 1;
}

@noinline function differ(a: i32, b: i32, c: i32): i32 {
  if (a != b + c) {
    return note(0);
  }
  return 0;
}

@noinline function below(a: i32, b: i32, c: i32): i32 {
  if (a < b - c) {
    return note(0);
  }
  return 0;
}

function main(): i32 {
  if (differ(5, 2, 3) != 0 || differ(5, 2, 4) != 1) {
    return 1;
  }
  // The sum wraps to the least i32.
  if (differ(-2147483648, 2147483647, 1) != 0 || differ(0, 2147483647, 1) != 1) {
    return 2;
  }
  if (below(1, 5, 3) != 1 || below(2, 5, 3) != 0) {
    return 3;
  }
  // The difference wraps to the largest i32.
  if (below(7, -2147483648, 1) != 1 || below(-1, 2147483647, -1) != 0) {
    return 4;
  }
  return 0;
}
`

// On x86-64 a branch on a compare of two values of 32 bits reads their low
// 32 bits with cmpl. The wrap of the sum to 32 bits then has no instruction.
func TestSelfHostBranchComparesTheLowHalf(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(narrowCompareProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	narrow := regexp.MustCompile(`cmpl %e\w+, %e\w+`)
	wrap := regexp.MustCompile(`movslq %e\w+, %r\w+`)
	for _, name := range []string{"differ", "below"} {
		body := tailCallBody(t, string(asm), name)
		if !narrow.MatchString(body) {
			t.Errorf("%s has no compare of 32 bits:\n%s", name, body)
		}
		if wrap.MatchString(body) {
			t.Errorf("%s still extends the sign before its compare:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, narrowCompareProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
