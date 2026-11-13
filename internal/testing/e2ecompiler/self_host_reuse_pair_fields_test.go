package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reusePairFieldsProg = `enum Tree { Node(i32, Tree, i32, Tree), Leaf }

@noinline function same(t: Tree): Tree {
  return t;
}

// The inner node keeps three fields of the result of same. The outer node
// keeps two fields of t.
@noinline function mark(t: Tree): Tree {
  match (t) {
    Node(_, l, x, r) => {
      match (same(l)) {
        Node(_, a, y, b) => { return Node(0, Node(1, a, y, b), x, r); },
        Leaf => { return Leaf; },
      }
    },
    Leaf => { return Leaf; },
  }
}

@noinline function sum(t: Tree): i32 {
  return match (t) { Node(c, l, x, r) => c + sum(l) + x + sum(r), Leaf => 0 };
}

function main(): i32 {
  let t = Node(5, Node(5, Leaf, 2, Leaf), 7, Node(5, Leaf, 3, Leaf));
  let kept = t;
  let u = mark(mark(t));
  if (sum(u) * 2 + sum(mark(kept)) + sum(kept) != 81) {
    return 1;
  }
  return 0;
}
`

// A carried token goes to the construction that keeps the most fields of its
// donor in their slots (ssaunits.best_claim_of). mark has two tokens and two
// constructions. The token of t builds the outer node, which keeps x and r.
// The token of the inner match then builds the inner node, which keeps a, y
// and b. The first construction in the block took the carried token before,
// and mark then stored 15 words.
func TestSelfHostReusePairsByFields(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(reusePairFieldsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC=mark")
	msg, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	form := string(msg)
	if end := strings.Index(form, "\n}\n"); end >= 0 {
		form = form[:end+1]
	}
	if stores := strings.Count(form, "= st_at "); stores == 0 || stores > 10 {
		t.Fatalf("mark has %d stores, want 1 to 10:\n%s", stores, form)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, reusePairFieldsProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
