package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const ssaTokenKeepProg = `enum Tree { Node(Tree, i32, Tree), Leaf }

@noinline function is_leaf(t: Tree): boolean {
  return match (t) { Leaf => true, _ => false };
}

// The match tests the shape of t, so the token of t has a tested shape.
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

// The match tests only that t is not a leaf.
@noinline function sink(t: Tree, k: i32): Tree {
  match (t) {
    Leaf => { return Node(Leaf, k, Leaf); },
    Node(l, x, r) => {
      if (k < x) {
        if (is_leaf(l)) {
          return Node(Node(Leaf, k, Leaf), x, r);
        }
        return Node(sink(l, k), x, r);
      }
      return Node(l, x, sink(r, k));
    },
  }
}

@noinline function sum(t: Tree): i32 {
  return match (t) { Node(l, x, r) => sum(l) + x + sum(r), Leaf => 0 };
}

function main(): i32 {
  let t = Leaf;
  let u = Leaf;
  for i in 0..6 {
    t = grow(t, 6 - i);
    u = sink(u, 6 - i);
  }
  let shared = t;
  let t2 = grow(shared, 9);
  let kept = u;
  let u2 = sink(kept, 9);
  return sum(t2) + sum(t) + sum(u2) + sum(u) - 51;
}
`

var ssaFormBlock = regexp.MustCompile(`\n  b\d+:\n`)

var ssaWordStore = regexp.MustCompile(`= st_at v\d+ (v\d+)\n`)

var ssaShapeValue = regexp.MustCompile(`(v\d+) = shape_addr `)

// The count of stores of a shape address to the first word of a box. A fill
// through the address of a hole also stores at offset 0.
func ssaShapeStores(form, block string) int {
	shapes := map[string]bool{}
	for _, m := range ssaShapeValue.FindAllStringSubmatch(form, -1) {
		shapes[m[1]] = true
	}
	n := 0
	for _, m := range ssaWordStore.FindAllStringSubmatch(block, -1) {
		if shapes[m[1]] {
			n++
		}
	}
	return n
}

// A reuse allocation of a token that is its own box writes neither the shape
// nor a word that the box already has (ssatokenkeep.keeps). grow and sink
// keep the key of a node at offset 16 in two of their three constructions.
// Only a block that allocates stores a shape, and at most one other block
// stores a key. grow has a tested shape, so its allocations call the fresh
// entry. sink tests the shape of its token, so its allocations stay inline.
func TestSelfHostSSATokenKeep(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaTokenKeepProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"grow", "sink"} {
		t.Run(fn, func(t *testing.T) {
			cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, cli.stdlib)
			cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC="+fn)
			msg, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, msg)
			}
			form := string(msg)
			if end := strings.Index(form, "\n}\n"); end >= 0 {
				form = form[:end+1]
			}
			shapes, keys := 0, 0
			for _, block := range ssaFormBlock.Split(form, -1) {
				if strings.Contains(block, "__fern_alloc_reuse") || strings.Contains(block, "arr_box") {
					continue
				}
				shapes += ssaShapeStores(form, block)
				keys += strings.Count(block, "= st_at 16 ")
			}
			if shapes != 0 || keys > 1 {
				t.Fatalf("%s stores %d shapes and %d keys in blocks that do not allocate, want 0 and at most 1:\n%s", fn, shapes, keys, form)
			}
		})
	}
	size := map[string]string{"x86-64-linux": "cmpw %si, -4(%rax)", "arm64-linux": "ldurh w30, [x0, #-4]"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			grow := tailCallBody(t, string(asm), "grow")
			if !strings.Contains(grow, "__fn___fern_alloc_reuse.f") || strings.Contains(grow, size[target]) {
				t.Fatalf("grow does not call the fresh entry only:\n%s", grow)
			}
			sink := tailCallBody(t, string(asm), "sink")
			if !strings.Contains(sink, size[target]) {
				t.Fatalf("sink has no inline reuse allocation:\n%s", sink)
			}
			if stderr, code := cli.exitOf(t, ssaTokenKeepProg, target); code != 51 {
				t.Fatalf("exit %d, want 51\n%s", code, stderr)
			}
		})
	}
}
