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
// shape. A box of two fields has two words, and calls the routine of a box of
// two words. A box of one field calls the routine of one word. Each of those
// routines takes the free list of a block of one word more, at
// __fern_freelist + 8 * (n + 1), writes the header with n in its high half,
// and goes to __fern_rec_box with n when the bump leaves its 64 KiB line.
// __fern_rec_box asks __fern_arr_box for two words less, and for no word for
// a box of one. So the block of two fields has 24 bytes. Each constructor
// stores a tag in the high half of the header, and the low 16 bits of the tag
// are the number of words.
// A 32-bit store to the high half of the header word of a box. On x86-64 the
// store takes the tag as an immediate.
var tagStore = map[string]*regexp.Regexp{
	"x86-64-linux": regexp.MustCompile(`movl \$\d+, -4\(%r\w+\)`),
	"arm64-linux":  regexp.MustCompile(`stu?r w\d+, \[x\d+, #-4\]`),
}

func TestSelfHostBoxHasAHeaderOfOneWord(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(boxWordsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := map[string][2]string{
		"x86-64-linux": {"call __fern_rec_box_2\n", "call __fern_rec_box_1\n"},
		"arm64-linux":  {"bl __fern_rec_box_2\n", "bl __fern_rec_box_1\n"},
	}
	sized := map[string][]string{
		"x86-64-linux": {
			"__fern_rec_box_2:\n    movq __fern_freelist+24(%rip), %rax\n",
			"    movl $1, (%rax)\n    movl $2, 4(%rax)\n    addq $8, %rax\n    ret\n",
			"    leaq 24(%rax), %rdi\n",
			".Lrecbox2_slow:\n    movl $2, %edi\n    jmp __fern_rec_box\n",
			"__fern_rec_box_1:\n    movq __fern_freelist+16(%rip), %rax\n",
		},
		"arm64-linux": {
			"__fern_rec_box_2:\n    adrp x2, __fern_freelist\n    add x2, x2, :lo12:__fern_freelist\n    ldr x1, [x2, #24]\n",
			"    mov x3, #1\n    movk x3, #2, lsl #32\n    str x3, [x1]\n    add x0, x1, #8\n    ret\n",
			"    add x3, x1, #24\n",
			".Lrecbox2_slow:\n    mov x0, #2\n    b __fern_rec_box\n",
			"__fern_rec_box_1:\n    adrp x2, __fern_freelist\n    add x2, x2, :lo12:__fern_freelist\n    ldr x1, [x2, #16]\n",
		},
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
			for _, part := range sized[target] {
				if !strings.Contains(text, part) {
					t.Fatalf("the routine of a size has no %q", part)
				}
			}
			if strings.Contains(text, "__fern_rec_box_3:") {
				t.Fatal("a routine of a size that no call names is in the output")
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
