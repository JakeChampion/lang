package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ins rebuilds a node after a recursive call, so the size of its token is not
// known at the allocation.
const ssaReuseAllocProg = `enum Tree { Node(Tree, i32, Tree), Leaf }

@noinline function ins(t: Tree, x: i32): Tree {
  match (t) {
    Leaf => { return Node(Leaf, x, Leaf); },
    Node(l, k, r) => {
      if (x < k) { return Node(ins(l, x), k, r); }
      if (x > k) { return Node(l, k, ins(r, x)); }
      return Node(l, k, r);
    },
  }
}

function size(t: Tree): i32 {
  return match (t) { Node(l, _, r) => size(l) + 1 + size(r), Leaf => 0 };
}

function main(): i32 {
  let t = Leaf;
  for i in 0..10 { t = ins(t, (i * 7) % 11); }
  let u = t;
  let v = ins(u, 3);
  return size(t) * 10 + size(v);
}
`

// A reuse allocation tests its token inline (ssa.inline_reuse_call), and calls
// the helper for a token of another size only. A reuse allocation on the null
// side of a test of its token calls the allocator with no test
// (ssa.fresh_boxes): bump of ssaBoxedCountProg rebuilds a shared box there.
func TestSelfHostSSAReuseAllocIsInline(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	reuse := filepath.Join(dir, "reuse.fern")
	boxed := filepath.Join(dir, "boxed.fern")
	for path, prog := range map[string]string{reuse: ssaReuseAllocProg, boxed: ssaBoxedCountProg} {
		if err := os.WriteFile(path, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	size := map[string]string{"x86-64-linux": "cmpq %rsi, -16(%rax)", "arm64-linux": "ldur x30, [x0, #-16]"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, reuse, target))
			if err != nil {
				t.Fatal(err)
			}
			ins := tailCallBody(t, string(asm), "ins")
			for _, want := range []string{size[target], "__fn___fern_alloc_reuse.r"} {
				if !strings.Contains(ins, want) {
					t.Fatalf("ins has no %q:\n%s", want, ins)
				}
			}
			if stderr, code := cli.exitOf(t, ssaReuseAllocProg, target); code != 110 {
				t.Fatalf("exit %d, want 110\n%s", code, stderr)
			}
			asm, err = os.ReadFile(cli.emit(t, boxed, target))
			if err != nil {
				t.Fatal(err)
			}
			bump := tailCallBody(t, string(asm), "bump")
			if !strings.Contains(bump, "__fern_arr_box") || strings.Contains(bump, size[target]) {
				t.Fatalf("bump should call __fern_arr_box with no test of its token:\n%s", bump)
			}
			if stderr, code := cli.exitOf(t, ssaBoxedCountProg, target); code != 55 {
				t.Fatalf("exit %d, want 55\n%s", code, stderr)
			}
		})
	}
}
