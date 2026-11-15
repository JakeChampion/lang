package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// differ and below each compare an i32 with a sum or a difference that wraps
// to 32 bits. main gives them operands at the two ends of the range of i32.
// The test of steps adds before it compares, so a copy of the test at the end
// of the loop must add again.
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

@noinline function steps(start: i32, step: i32): i32 {
  let i = start;
  let n = 0;
  while (i + step <= 576) {
    i += step;
    n += 1;
  }
  return n;
}

function main(): i32 {
  if (steps(0, 4) != 144 || steps(570, 4) != 1 || steps(573, 4) != 0) {
    return 5;
  }
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

// A branch on a compare of two values of 32 bits reads their low 32 bits:
// cmpl on x86-64 and cmp of w registers on arm64. The wrap of the sum to 32
// bits then has no instruction.
func TestSelfHostBranchComparesTheLowHalf(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(narrowCompareProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, isa := range []struct{ target, narrow, wrap string }{
		{"x86-64-linux", `cmpl %e\w+, %e\w+`, `movslq %e\w+, %r\w+`},
		{"arm64-linux", `cmp w\d+, w\d+`, `sxtw x\d+, w\d+`},
	} {
		asm, err := os.ReadFile(cli.emit(t, src, isa.target))
		if err != nil {
			t.Fatal(err)
		}
		narrow := regexp.MustCompile(isa.narrow)
		wrap := regexp.MustCompile(isa.wrap)
		for _, name := range []string{"differ", "below"} {
			body := tailCallBody(t, string(asm), name)
			if !narrow.MatchString(body) {
				t.Errorf("%s: %s has no compare of 32 bits:\n%s", isa.target, name, body)
			}
			if wrap.MatchString(body) {
				t.Errorf("%s: %s still extends the sign before its compare:\n%s", isa.target, name, body)
			}
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, narrowCompareProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
