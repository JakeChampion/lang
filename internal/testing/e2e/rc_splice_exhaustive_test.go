package e2e

import (
	"fmt"
	"testing"
)

// A red-black tree's balance functions end in an exhaustive match, whose
// fall-through is an unreachable block. The inliner would not splice a body
// with one, so balance_left and balance_right stayed calls: the node `ins`
// took apart died before the call and could not serve the rotation the
// callee built, and every insert that rebalanced allocated. Spliced into
// `ins`, the rotations build in the boxes that die there, and an insert
// allocates only the new leaf.
func spliceExhaustiveSrc(n int) string {
	return fmt.Sprintf(`enum Color { Red, Black }

enum Tree {
  Node(Color, Tree, i32, boolean, Tree),
  Leaf,
}

function is_red(t: Tree): boolean {
  match (t) {
    Node(Red(), _, _, _, _) => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

function balance_left(l: Tree, k: i32, v: boolean, r: Tree): Tree {
  match (l) {
    Node(_, Node(Red(), lx, kx, vx, rx), ky, vy, ry) => {
      return Node(Red, Node(Black, lx, kx, vx, rx), ky, vy, Node(Black, ry, k, v, r));
    },
    Node(_, ly, ky, vy, Node(Red(), lx, kx, vx, rx)) => {
      return Node(Red, Node(Black, ly, ky, vy, lx), kx, vx, Node(Black, rx, k, v, r));
    },
    Node(_, lx, kx, vx, rx) => {
      return Node(Black, Node(Red, lx, kx, vx, rx), k, v, r);
    },
    Leaf => {
      return Leaf;
    }
  }
}

function balance_right(l: Tree, k: i32, v: boolean, r: Tree): Tree {
  match (r) {
    Node(_, Node(Red(), lx, kx, vx, rx), ky, vy, ry) => {
      return Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, rx, ky, vy, ry));
    },
    Node(_, lx, kx, vx, Node(Red(), ly, ky, vy, ry)) => {
      return Node(Red, Node(Black, l, k, v, lx), kx, vx, Node(Black, ly, ky, vy, ry));
    },
    Node(_, lx, kx, vx, rx) => {
      return Node(Black, l, k, v, Node(Red, lx, kx, vx, rx));
    },
    Leaf => {
      return Leaf;
    }
  }
}

function ins(t: Tree, k: i32, v: boolean): Tree {
  match (t) {
    Node(Red(), l, kx, vx, r) => {
      if (k < kx) {
        return Node(Red, ins(l, k, v), kx, vx, r);
      }
      if (k > kx) {
        return Node(Red, l, kx, vx, ins(r, k, v));
      }
      return Node(Red, l, k, v, r);
    },
    Node(Black(), l, kx, vx, r) => {
      if (k < kx) {
        if (is_red(l)) {
          return balance_left(ins(l, k, v), kx, vx, r);
        }
        return Node(Black, ins(l, k, v), kx, vx, r);
      }
      if (k > kx) {
        if (is_red(r)) {
          return balance_right(l, kx, vx, ins(r, k, v));
        }
        return Node(Black, l, kx, vx, ins(r, k, v));
      }
      return Node(Black, l, k, v, r);
    },
    Leaf => {
      return Node(Red, Leaf, k, v, Leaf);
    }
  }
}

function set_black(t: Tree): Tree {
  match (t) {
    Node(_, l, k, v, r) => {
      return Node(Black, l, k, v, r);
    },
    _ => {
      return t;
    }
  }
}

function insert(t: Tree, k: i32, v: boolean): Tree {
  return set_black(ins(t, k, v));
}

function fold(t: Tree, b: i32, f: (i32, boolean, i32) => i32): i32 {
  match (t) {
    Node(_, l, k, v, r) => {
      return fold(r, f(k, v, fold(l, b, f)), f);
    },
    Leaf => {
      return b;
    }
  }
}

function make_tree_aux(n: i32, t: Tree): Tree {
  if (n <= 0) {
    return t;
  }
  let n1: i32 = n - 1;
  return make_tree_aux(n1, insert(t, n1, n1 %% 10 == 0));
}

function make_tree(n: i32): Tree {
  return make_tree_aux(n, Leaf);
}

function count_true(t: Tree): i32 {
  return fold(t, 0, (k: i32, v: boolean, r: i32): i32 => {
    if (v) {
      return r + 1;
    }
    return r;
  });
}

function sum_keys(t: Tree): i32 {
    match (t) {
        Node(_, l, k, _, r) => { return sum_keys(l) + k + sum_keys(r); },
        Leaf => { return 0; }
    }
}

function main(): i32 {
    let t: Tree = make_tree(%d);
    if (count_true(t) != %d || sum_keys(t) != %d) { return 1; }
    return __rc_underflow_count();
}`, n, (n+9)/10, n*(n-1)/2)
}

func checkSpliceExhaustive(t *testing.T, target string, run censusRun) {
	t.Helper()
	census := func(n int) int64 {
		stdout, stderr, exit := run(t, spliceExhaustiveSrc(n))
		if exit != 0 || stdout != "" && stdout != "0\n" {
			t.Fatalf("%s n=%d: exit %d, stdout %q, want 0; stderr: %s", target, n, exit, stdout, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a != f || live != 0 {
			t.Fatalf("%s n=%d: allocs=%d frees=%d live_bytes=%d, want balanced", target, n, a, f, live)
		}
		t.Logf("%s n=%d: allocs=%d", target, n, a)
		return a
	}
	if lo, hi := census(1000), census(4000); hi-lo != 3000 {
		t.Errorf("%s: %d allocations for 1000 inserts and %d for 4000, want 3000 more: an insert allocated more than its leaf", target, lo, hi)
	}
}

func TestInterpSpliceExhaustiveValues(t *testing.T) {
	if got := runInterpExit(t, spliceExhaustiveSrc(500)); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestX86_64SpliceExhaustive(t *testing.T) {
	checkSpliceExhaustive(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64SpliceExhaustive(t *testing.T) {
	checkSpliceExhaustive(t, "arm64", runLeakCheckArm64)
}

func TestWASMSpliceExhaustive(t *testing.T) {
	checkSpliceExhaustive(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
