package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// main keeps 4000 boxes of one size, which is more than four 64 KiB lines of
// the heap, and reads each of them back.
const bumpColourProg = `struct Item { n: i32, a: i32, b: i32, c: i32, d: i32 }

@noinline function fill(count: i32): Item[] {
  let cells: Item[] = [];
  for i in 0..count {
    cells = cells.append(Item { n: i, a: i + 1, b: i + 2, c: i + 3, d: i + 4 });
  }
  return cells;
}

function main(): i32 {
  let cells = fill(4000);
  let total = 0;
  for c in cells {
    total += c.n + c.a + c.b + c.c + c.d - 10;
  }
  if (total != 39990000) {
    return 1;
  }
  return 0;
}
`

// The bump allocator moves its pointer forward by a multiple of 64 bytes when
// a block crosses a 64 KiB line. Boxes of one size are then not a constant
// distance apart, and the boxes that a tree keeps on one path do not all use
// one cache set.
func TestSelfHostBumpAllocatorColoursEach64KLine(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(bumpColourProg), 0o644); err != nil {
		t.Fatal(err)
	}
	step := map[string][]string{
		"x86-64-linux": {"cmpw %ax, %di\n    jb .Lalloc_colour\n", "andq $15, %rax\n    shlq $6, %rax\n    addq %rdi, %rax"},
		"arm64-linux":  {"lsr x0, x0, #16\n    cbz x0, .Lalloc_plain\n", "and x0, x0, #15\n    lsl x0, x0, #6\n    add x3, x3, x0"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range step[target] {
				if !strings.Contains(string(asm), want) {
					t.Fatalf("the runtime has no %q", want)
				}
			}
			for _, env := range []string{"", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				if stderr, code := cli.exitOf(t, bumpColourProg, target, env); code != 0 {
					t.Fatalf("%s: exit %d, want 0\n%s", env, code, stderr)
				}
			}
		})
	}
}
