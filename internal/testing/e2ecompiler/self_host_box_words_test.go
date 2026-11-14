package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// add makes a box of two fields and val makes a box of one. main builds a
// list of 1000 sums, and bump updates each in place, so the reuse allocation
// takes the same size.
const boxWordsProg = `enum Expr { Add(Expr, Expr), Val(i64) }

@noinline function val(v: i64): Expr {
  return Val(v);
}

@noinline function add(a: Expr, b: Expr): Expr {
  return Add(a, b);
}

@noinline function bump(e: Expr): Expr {
  return match (e) { Add(a, b) => Add(b, a), Val(v) => Val(v + 1i64) };
}

function sum(e: Expr): i64 {
  return match (e) { Add(a, b) => sum(a) + sum(b), Val(v) => v };
}

function main(): i32 {
  let sums: Expr[] = [];
  for i in 0..1000 {
    sums = sums.append(bump(add(bump(val(i as i64)), val(7i64))));
  }
  let total = 0i64;
  for e in sums {
    total += sum(e);
  }
  if (total != 507500i64) {
    return 1;
  }
  return 0;
}
`

// A box uses the word of an array length as its first word. A box of two
// fields asks __fern_arr_box for two words, not three, and a box of one field
// asks for one. The block of two fields has 40 bytes.
func TestSelfHostBoxTakesNoSpareWord(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(boxWordsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := map[string][2]string{
		"x86-64-linux": {"movq $2, %rdi\n    call __fern_arr_box", "movq $1, %rdi\n    call __fern_arr_box"},
		"arm64-linux":  {"mov x0, #2\n    bl __fern_arr_box", "mov x0, #1\n    bl __fern_arr_box"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(asm)
			if body := tailCallBody(t, text, "add"); !strings.Contains(body, asked[target][0]) {
				t.Fatalf("add has no %q:\n%s", asked[target][0], body)
			}
			if body := tailCallBody(t, text, "val"); !strings.Contains(body, asked[target][1]) {
				t.Fatalf("val has no %q:\n%s", asked[target][1], body)
			}
			for _, env := range []string{"", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				if stderr, code := cli.exitOf(t, boxWordsProg, target, env); code != 0 {
					t.Fatalf("%s: exit %d, want 0\n%s", env, code, stderr)
				}
			}
		})
	}
}
