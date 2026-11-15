package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A read of a[i] proves the write a.with(i, v) after it, so the write has no
// check of its own (#15975). The branch puts the write in another block.
const withAfterRead = `@noinline function bump(own a: i32[], i: i32): i32[] { let m = a[i]; return a.with(i, if (m >= 8) { m - 8 } else { m + 1 }); }
@noinline function put(own a: i32[], i: i32): i32[] { return a.with(i, 7); }
@noinline function maybe(own a: i32[], i: i32, flag: boolean): i32[] { let x = 0; if (flag) { x = a[i]; } return a.with(i, x + 1); }
`

func TestSelfHostWithAfterReadUnchecked(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "with.fern")
	program := withAfterRead + `function main(): i32 {
  let a = bump(bump([1, 2, 3], 1), 1);
  let b = maybe(put([1, 4, 3], 0), 2, true);
  return a[1] + b[0] + b[2] - 15;
}`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, program, target); code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := strings.Count(asmFunction(string(asm), "__fn_bump:"), "oob_abort")
			put := strings.Count(asmFunction(string(asm), "__fn_put:"), "oob_abort")
			if bump != put || put == 0 {
				t.Errorf("bump has %d checks and put has %d: the write after the read kept its check", bump, put)
			}
			out := withAfterRead + `function main(): i32 { let b = maybe([1, 2], 5, false); return b.len(); }`
			if stderr, code := cli.exitOf(t, out, target); code != 134 || !strings.Contains(stderr, "fern: array index out of range") {
				t.Errorf("a write that no read dominates exited %d, want the array abort 134\n%s", code, stderr)
			}
		})
	}
}
