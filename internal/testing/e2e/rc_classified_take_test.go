package e2e

import (
	"strings"
	"testing"
)

const classifiedTakeSrc = `import "std/i32";
import "std/string";

enum Tree { Leaf, Node(Tree, i32, Tree) }
enum Expr { Val(i32), Add(Expr, Expr), Neg(Expr) }
struct Pair { left: i32[], right: i32[], n: i32 }
struct Wide { a: i32[], b: i32[], c: i32[], d: i32[], n: i32 }

@noinline
function ins(t: Tree, x: i32): Tree {
  match (t) {
    Node(l, k, r) => {
      if (x < k) {
        return Node(ins(l, x), k, r);
      }
      if (x > k) {
        return Node(l, k, ins(r, x));
      }
      return Node(l, x, r);
    },
    Leaf => {
      return Node(Leaf, x, Leaf);
    }
  }
}

function sum(t: Tree): i32 {
  match (t) {
    Node(l, k, r) => {
      return sum(l) + k + sum(r);
    },
    Leaf => {
      return 0;
    }
  }
}

@noinline
function combine(a: Expr, b: Expr): Expr {
  match (a) {
    Val(x) => {
      match (b) {
        Val(y) => {
          return Val(x + y);
        },
        _ => {}
      }
    },
    _ => {}
  }
  return Add(a, b);
}

@noinline
function fold(e: Expr): Expr {
  match (e) {
    Add(a, b) => {
      return combine(fold(a), fold(b));
    },
    Neg(a) => {
      return Neg(fold(a));
    },
    Val(_) => {
      return e;
    }
  }
}

function build(n: i32): Expr {
  if (n == 0) {
    return Val(1);
  }
  return Add(build(n - 1), Neg(build(n - 1)));
}

function value(e: Expr): i32 {
  match (e) {
    Val(x) => {
      return x;
    },
    Add(a, b) => {
      return value(a) + value(b);
    },
    Neg(a) => {
      return 0 - value(a);
    }
  }
}

@noinline
function cat(own a: i32[], own b: i32[]): i32[] {
  let out: i32[] = a;
  for x in b {
    out = out.append(x);
  }
  return out;
}

@noinline
function joined(own p: Pair): i32[] {
  let l: i32[] = p.left;
  let r: i32[] = p.right;
  return cat(l, r).append(p.n);
}

@noinline
function first(own w: Wide): i32[] {
  let a: i32[] = w.a;
  return a.append(w.n);
}

function total(xs: i32[]): i32 {
  let s: i32 = 0;
  for x in xs {
    s = s + x;
  }
  return s;
}

function main(): i32 {
  let t: Tree = Leaf;
  let kept: Tree[] = [];
  let i: i32 = 0;
  while (i < 200) {
    t = ins(t, (i * 37) % 211);
    if (i % 5 == 0) {
      kept = kept.append(t);
    }
    i = i + 1;
  }
  let bad: i32 = 0;
  if (sum(t) != 21010 || sum(kept[1]) != 555 || kept.len() != 40) {
    bad = bad + 1;
  }
  let e: Expr = build(6);
  let shared: Expr = e;
  if (value(fold(e)) != value(shared) || value(fold(build(5))) != 0) {
    bad = bad + 2;
  }
  let p: Pair = Pair { left: [1, 2], right: [3], n: 4 };
  let q: Pair = p;
  if (total(joined(p)) != 10 || total(joined(Pair { left: [5], right: [6, 7], n: 8 })) != 26 || total(q.left) != 3) {
    bad = bad + 4;
  }
  let w: Wide = Wide { a: [1], b: [2], c: [3], d: [4], n: 5 };
  let w2: Wide = w;
  if (total(first(w)) != 6 || total(first(Wide { a: [10], b: [20], c: [30], d: [40], n: 50 })) != 60 || total(w2.d) != 4) {
    bad = bad + 8;
  }
  return bad;
}
`

func TestInterpClassifiedTakeValues(t *testing.T) {
	if got := runInterpExit(t, classifiedTakeSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func checkClassifiedTake(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, classifiedTakeSrc)
	if exit != 0 || stdout != "" && stdout != "0\n" {
		t.Fatalf("exit %d, stdout %q, want 0 (bits 1..8 name the shape that came back wrong); stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64ClassifiedTakeSanitized(t *testing.T) {
	checkClassifiedTake(t, runSanitizeX86_64)
}

func TestArm64ClassifiedTakeSanitized(t *testing.T) {
	checkClassifiedTake(t, runSanitizeArm64)
}

func TestWASMClassifiedTakeSanitized(t *testing.T) {
	checkClassifiedTake(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
