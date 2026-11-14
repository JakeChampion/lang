package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
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

// A box has a header of one word, which holds its count and the tag of its
// shape. A box of two fields has two words, and asks __fern_rec_box for two.
// A box of one field asks for one. __fern_rec_box asks __fern_arr_box for
// two words less, and for no word for a box of one. So the block of two
// fields has 24 bytes. Each constructor stores a tag in the high half of
// the header, and the low 16 bits of the tag are the number of words.
// A 32-bit store to the high half of the header word of a box.
var tagStore = map[string]*regexp.Regexp{
	"x86-64-linux": regexp.MustCompile(`movl %e\w+, -4\(%r\w+\)`),
	"arm64-linux":  regexp.MustCompile(`stu?r w\d+, \[x\d+, #-4\]`),
}

func TestSelfHostBoxHasAHeaderOfOneWord(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(boxWordsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := map[string][2]string{
		"x86-64-linux": {"movq $2, %rdi\n    call __fern_rec_box", "movq $1, %rdi\n    call __fern_rec_box"},
		"arm64-linux":  {"mov x0, #2\n    bl __fern_rec_box", "mov x0, #1\n    bl __fern_rec_box"},
	}
	routine := map[string]string{
		"x86-64-linux": "    pushq %rdi\n    subq $2, %rdi\n    jae .Lrecbox_sized\n    xorl %edi, %edi\n.Lrecbox_sized:\n    call __fern_arr_box\n",
		"arm64-linux":  "    subs x0, x0, #2\n    csel x0, x0, xzr, hs\n    bl __fern_arr_box\n",
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(asm)
			if !strings.Contains(text, routine[target]) {
				t.Fatalf("__fern_rec_box does not ask for two words less: no %q", routine[target])
			}
			// A store of 32 bits leaves the last bytes of a block from a
			// free list in the half that marks a box.
			if target == "arm64-linux" && (strings.Contains(text, "str w3, [x2, #8]") || !strings.Contains(text, "mov x3, #1\n    str x3, [x2, #8]")) {
				t.Fatal("__fern_arr_box does not write all 64 bits of the count word")
			}
			if body := tailCallBody(t, text, "add"); !strings.Contains(body, asked[target][0]) {
				t.Fatalf("add has no %q:\n%s", asked[target][0], body)
			}
			if body := tailCallBody(t, text, "val"); !strings.Contains(body, asked[target][1]) {
				t.Fatalf("val has no %q:\n%s", asked[target][1], body)
			}
			if body := tailCallBody(t, text, "add"); !tagStore[target].MatchString(body) {
				t.Fatalf("add stores no tag in the header of its box:\n%s", body)
			}
			for _, env := range []string{"", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				if stderr, code := cli.exitOf(t, boxWordsProg, target, env); code != 0 {
					t.Fatalf("%s: exit %d, want 0\n%s", env, code, stderr)
				}
			}
		})
	}
}
