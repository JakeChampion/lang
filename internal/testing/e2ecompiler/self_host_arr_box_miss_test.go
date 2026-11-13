package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grow makes 40 boxes with no box to take from a free list, frees them, and
// makes 40 more from the list.
const arrBoxMissProg = `struct Item { n: i32, next: i32[] }

@noinline function grow(k: i32): i32 {
  let cells: Item[] = [];
  for i in 0..40 {
    cells = cells.append(Item { n: i + k, next: [i] });
  }
  let total = 0;
  for c in cells {
    total += c.n + c.next[0];
  }
  return total;
}

function main(): i32 {
  let a = grow(0);
  let b = grow(1);
  if (a != 1560 || b != 1600) {
    return 1;
  }
  return 0;
}
`

// __fern_arr_box reads the free list of the size class. When the list is
// empty it goes to the bump allocator directly, so the allocator does not
// round the size and read the same list again. A sanitizer build has another
// allocator and keeps the full call.
func TestSelfHostArrBoxMissSkipsTheSecondLookup(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(arrBoxMissProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bump := map[string]string{"x86-64-linux": "call .Lalloc_fast_miss", "arm64-linux": "bl __fern_alloc_bump"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(asm), bump[target]) {
				t.Fatalf("the runtime has no %q", bump[target])
			}
			if stderr, code := cli.exitOf(t, arrBoxMissProg, target); code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			if stderr, code := cli.exitOf(t, arrBoxMissProg, target, "FERN_SANITIZE=1"); code != 0 {
				t.Fatalf("sanitizer build: exit %d, want 0\n%s", code, stderr)
			}
		})
	}
}
