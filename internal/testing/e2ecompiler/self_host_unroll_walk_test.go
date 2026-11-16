package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sum consumes the tree, and depth borrows it. Each calls itself twice and
// returns an integer, so each body is spliced into its own two calls once.
// mirror builds a tree, so it keeps its calls. A tree with item i and a depth
// of 1 or more sums to i + 1 after the mirror.
const unrollWalkProg = `enum Tree { Leaf, Node(i64, Tree, Tree) }

@noinline function make(item: i64, depth: i32): Tree {
  if (depth == 0) {
    return Node(item, Leaf, Leaf);
  }
  return Node(item, make(2i64 * item - 1i64, depth - 1), make(2i64 * item, depth - 1));
}

function sum(t: Tree): i64 {
  return match (t) { Node(item, l, r) => item + sum(l) - sum(r), Leaf => 0i64 };
}

function depth(t: Tree): i64 {
  return match (t) {
    Node(_, l, r) => {
      let a = depth(l);
      let b = depth(r);
      if (a > b) { 1i64 + a } else { 1i64 + b }
    },
    Leaf => 0i64
  };
}

function mirror(t: Tree): Tree {
  return match (t) { Node(item, l, r) => Node(item, mirror(r), mirror(l)), Leaf => Leaf };
}

function main(): i32 {
  let total = 0i64;
  for d in 0..9 {
    let t = make(3i64, d);
    if (depth(t) != (d + 1) as i64) {
      return 1;
    }
    total += sum(mirror(t));
  }
  if (total != 3i64 + 8i64 * 4i64) {
    return total as i32;
  }
  return 0;
}
`

func TestSelfHostRecursiveWalkIsUnrolledOnce(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(unrollWalkProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		calls int
	}{{"sum", 4}, {"depth", 4}, {"mirror", 2}} {
		body := tailCallBody(t, string(asm), c.name)
		if got := strings.Count(body, "call __fn_"+c.name+".r"); got != c.calls {
			t.Errorf("%s calls itself %d times, want %d:\n%s", c.name, got, c.calls, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, unrollWalkProg, target, "FERN_LEAKCHECK=1")
		if code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
			continue
		}
		assertBalancedCensus(t, stderr)
	}
}
