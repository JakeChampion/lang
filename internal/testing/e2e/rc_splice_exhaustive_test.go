package e2e

import (
	"fmt"
	"testing"
)

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

func spliceCarriedSrc(n int) string {
	return fmt.Sprintf(`enum Color { Red, Black }

enum Tree { Leaf, Node(Color, Tree, i64, boolean, Tree) }

enum Trees { NoTrees, Cons(Tree, Trees) }

function fold(t: Tree, acc: i64, f: (i64, boolean, i64) => i64): i64 {
  match (t) {
    Leaf => {
      return acc;
    },
    Node(_, l, k, v, r) => {
      return fold(r, f(k, v, fold(l, acc, f)), f);
    }
  }
}

function balance1(kv: i64, vv: boolean, t: Tree, s: Tree): Tree {
  match (s) {
    Leaf => {
      return Leaf;
    },
    Node(_, Node(Red(), l, kx, vx, r1), ky, vy, r2) => {
      return Node(Red, Node(Black, l, kx, vx, r1), ky, vy, Node(Black, r2, kv, vv, t));
    },
    Node(_, l1, ky, vy, Node(Red(), l2, kx, vx, r)) => {
      return Node(Red, Node(Black, l1, ky, vy, l2), kx, vx, Node(Black, r, kv, vv, t));
    },
    Node(_, l, ky, vy, r) => {
      return Node(Black, Node(Red, l, ky, vy, r), kv, vv, t);
    }
  }
}

function balance2(t: Tree, kv: i64, vv: boolean, s: Tree): Tree {
  match (s) {
    Leaf => {
      return Leaf;
    },
    Node(_, Node(Red(), l, kx1, vx1, r1), ky, vy, r2) => {
      return Node(Red, Node(Black, t, kv, vv, l), kx1, vx1, Node(Black, r1, ky, vy, r2));
    },
    Node(_, l1, ky, vy, Node(Red(), l2, kx2, vx2, r2)) => {
      return Node(Red, Node(Black, t, kv, vv, l1), ky, vy, Node(Black, l2, kx2, vx2, r2));
    },
    Node(_, l, ky, vy, r) => {
      return Node(Black, t, kv, vv, Node(Red, l, ky, vy, r));
    }
  }
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

function ins(t: Tree, kx: i64, vx: boolean): Tree {
  match (t) {
    Leaf => {
      return Node(Red, Leaf, kx, vx, Leaf);
    },
    Node(Red(), l, ky, vy, r) => {
      if (kx < ky) {
        return Node(Red, ins(l, kx, vx), ky, vy, r);
      }
      if (kx == ky) {
        return Node(Red, l, kx, vx, r);
      }
      return Node(Red, l, ky, vy, ins(r, kx, vx));
    },
    Node(Black(), l, ky, vy, r) => {
      if (kx < ky) {
        if (is_red(l)) {
          return balance1(ky, vy, r, ins(l, kx, vx));
        }
        return Node(Black, ins(l, kx, vx), ky, vy, r);
      }
      if (kx == ky) {
        return Node(Black, l, kx, vx, r);
      }
      if (is_red(r)) {
        return balance2(l, ky, vy, ins(r, kx, vx));
      }
      return Node(Black, l, ky, vy, ins(r, kx, vx));
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

function insert(t: Tree, k: i64, v: boolean): Tree {
  if (is_red(t)) {
    return set_black(ins(t, k, v));
  }
  return ins(t, k, v);
}

function make_tree_aux(freq: i64, n: i64, t: Tree, acc: Trees): Trees {
  if (n <= 0i64) {
    return Cons(t, acc);
  }
  let next: Tree = insert(t, n, n %% 10i64 == 0i64);
  if (n %% freq == 0i64) {
    return make_tree_aux(freq, n - 1i64, next, Cons(next, acc));
  }
  return make_tree_aux(freq, n - 1i64, next, acc);
}

function make_tree(freq: i64, n: i64): Trees {
  return make_tree_aux(freq, n, Leaf, NoTrees);
}

function count_true(t: Tree): i64 {
  return fold(t, 0i64, (k: i64, v: boolean, r: i64): i64 => {
    if (v) {
      return r + 1i64;
    }
    return r;
  });
}

function count_head(trees: Trees): i64 {
  match (trees) {
    Cons(t, _) => {
      return count_true(t);
    },
    NoTrees => {
      return count_true(Leaf);
    }
  }
}

function main(): i32 {
    let trees: Trees = make_tree(%di64, %di64);
    if (count_head(trees) != %di64) { return 1; }
    return __rc_underflow_count();
}`, n+1, n, (n+9)/10)
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
	carried := func(n int) int64 {
		stdout, stderr, exit := run(t, spliceCarriedSrc(n))
		if exit != 0 || stdout != "" && stdout != "0\n" {
			t.Fatalf("%s carried n=%d: exit %d, stdout %q, want 0; stderr: %s", target, n, exit, stdout, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a != f || live != 0 {
			t.Fatalf("%s carried n=%d: allocs=%d frees=%d live_bytes=%d, want balanced", target, n, a, f, live)
		}
		t.Logf("%s carried n=%d: allocs=%d", target, n, a)
		return a
	}
	if lo, hi := carried(1000), carried(4000); hi-lo != 3000 {
		t.Errorf("%s carried: %d allocations for 1000 inserts and %d for 4000, want 3000 more: a rotation allocated beside a carried token", target, lo, hi)
	}
}

func TestInterpSpliceExhaustiveValues(t *testing.T) {
	if got := runInterpExit(t, spliceExhaustiveSrc(500)); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
	if got := runInterpExit(t, spliceCarriedSrc(500)); got != 0 {
		t.Errorf("carried: got %d, want 0", got)
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
