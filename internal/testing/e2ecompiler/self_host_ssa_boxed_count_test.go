package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ssaBoxedCountProg = `enum Tree { Node(Tree, i32, Tree), Leaf }

// The match reads t before the uniqueness test of the rebuild.
@noinline function bump(t: Tree): Tree {
  match (t) {
    Node(l, k, r) => { return Node(l, k + 1, r); },
    Leaf => { return Leaf; },
  }
}

// Nothing reads t before the count.
@noinline function twice(t: Tree): (Tree, Tree) {
  return (t, t);
}

@noinline function key(t: Tree): i32 {
  return match (t) { Node(_, k, _) => k, Leaf => 0 };
}

function main(): i32 {
  let t = Node(Leaf, 4, Leaf);
  let (a, b) = twice(t);
  return key(bump(a)) * 10 + key(bump(b));
}
`

// A count of a value that a load already read needs no test that the value is
// a box (ssadeps.boxed_values). bump reads t in its match, so only the counts
// of the two children keep the test. twice reads nothing, so its count keeps
// the test. A sanitizer build keeps each test.
func TestSelfHostSSABoxedCountHasNoGuard(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaBoxedCountProg), 0o644); err != nil {
		t.Fatal(err)
	}
	floor := map[string]string{"x86-64-linux": "cmpq $0x10000,", "arm64-linux": "#16, lsl #12"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := tailCallBody(t, string(asm), "bump")
			if got := strings.Count(bump, floor[target]); got != 2 {
				t.Fatalf("bump tests %d values against the heap floor, want the 2 children:\n%s", got, bump)
			}
			twice := tailCallBody(t, string(asm), "twice")
			if got := strings.Count(twice, floor[target]); got != 1 {
				t.Fatalf("twice tests %d values against the heap floor, want 1:\n%s", got, twice)
			}
			if stderr, code := cli.exitOf(t, ssaBoxedCountProg, target); code != 55 {
				t.Fatalf("exit %d, want 55\n%s", code, stderr)
			}
		})
	}
	t.Run("sanitizer keeps the test", func(t *testing.T) {
		asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux", "FERN_SANITIZE=1"))
		if err != nil {
			t.Fatal(err)
		}
		bump := tailCallBody(t, string(asm), "bump")
		if got := strings.Count(bump, floor["x86-64-linux"]); got != 3 {
			t.Fatalf("a sanitizer build of bump tests %d values against the heap floor, want 3:\n%s", got, bump)
		}
	})
}
