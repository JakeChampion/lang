package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ssaReuseAllocProg = `enum Tree { Node(Tree, i32, Tree), Leaf }

@noinline function is_leaf(t: Tree): boolean {
  return match (t) { Leaf => true, _ => false };
}

// The token of t reaches two constructions behind tests of other values.
@noinline function grow(t: Tree, k: i32): Tree {
  match (t) {
    Node(l, x, r) => {
      if (k < x) {
        if (is_leaf(l)) {
          return Node(Node(Leaf, k, Leaf), x, r);
        }
        return Node(grow(l, k), x, r);
      }
      return Node(l, x, grow(r, k));
    },
    Leaf => { return Node(Leaf, k, Leaf); },
  }
}

@noinline function sum(t: Tree): i32 {
  return match (t) { Node(l, x, r) => sum(l) + x + sum(r), Leaf => 0 };
}

function main(): i32 {
  let t = Leaf;
  for i in 0..6 {
    t = grow(t, 6 - i);
  }
  let shared = t;
  let u = grow(shared, 9);
  return sum(u) + sum(t);
}
`

// A reuse allocation tests its token inline (ssa.inline_reuse_call), and calls
// the helper only for a token of another size. bump of ssaBoxedCountProg
// allocates on the null side of a test of its token, so it calls the fresh
// entry of the helper with no test (ssa.fresh_boxes).
func TestSelfHostSSAReuseAllocIsInline(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	inline := filepath.Join(dir, "inline.fern")
	if err := os.WriteFile(inline, []byte(ssaReuseAllocProg), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "fresh.fern")
	if err := os.WriteFile(fresh, []byte(ssaBoxedCountProg), 0o644); err != nil {
		t.Fatal(err)
	}
	size := map[string]string{"x86-64-linux": "cmpq %rsi, -16(%rax)", "arm64-linux": "ldur x30, [x0, #-16]"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, inline, target))
			if err != nil {
				t.Fatal(err)
			}
			grow := tailCallBody(t, string(asm), "grow")
			for _, want := range []string{size[target], "__fn___fern_alloc_reuse.r", "__fern_arr_box"} {
				if !strings.Contains(grow, want) {
					t.Fatalf("grow has no %q:\n%s", want, grow)
				}
			}
			if stderr, code := cli.exitOf(t, ssaReuseAllocProg, target); code != 51 {
				t.Fatalf("exit %d, want 51\n%s", code, stderr)
			}
			asm, err = os.ReadFile(cli.emit(t, fresh, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := tailCallBody(t, string(asm), "bump")
			if !strings.Contains(bump, "__fn___fern_alloc_reuse.f") || strings.Contains(bump, size[target]) {
				t.Fatalf("bump does not call the fresh entry only:\n%s", bump)
			}
		})
	}
}
