package e2e

import (
	"strings"
	"testing"
)

// A donor whose payloads a block takes and which dies in the same block reads
// its uniqueness once, at its first take, and its death follows that answer:
// a unique donor's taken slots are the frame's, and only its other fields are
// released. Each shape below runs on a unique donor and on one a snapshot
// still holds, so both arms of the one test are exercised: `ins` holds its
// donor's token for a construction in a later block, `fold` drops its donor,
// `joined` releases the rest of a record inline and `first` through the
// record's drop helper. `cins` drops the colour it tested and rebuilds its
// node in place, and `pick` drops a child it took before its donor dies, so
// the donor's helper walk releases it: what the takes owe is settled in the
// death's or the construction's own test of the token.
const classifiedTakeSrc = `import "std/i32";
import "std/string";

enum Tree { Leaf, Node(Tree, i32, Tree) }
enum Expr { Val(i32), Add(Expr, Expr), Neg(Expr) }
struct Pair { left: i32[], right: i32[], n: i32 }
struct Wide { a: i32[], b: i32[], c: i32[], d: i32[], n: i32 }
enum Color { Red, Black }
enum CTree { CLeaf, CNode(Color, CTree, i32, CTree) }
struct Quad { a: i32[], b: i32[], c: i32[], d: i32[] }

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

@noinline
function cins(t: CTree, x: i32): CTree {
  match (t) {
    CNode(Red(), l, k, r) => {
      if (x < k) {
        return CNode(Red, cins(l, x), k, r);
      }
      return CNode(Black, l, k, cins(r, x));
    },
    CNode(Black(), l, k, r) => {
      if (x < k) {
        return CNode(Black, cins(l, x), k, r);
      }
      return CNode(Red, l, k, cins(r, x));
    },
    CLeaf => {
      return CNode(Red, CLeaf, x, CLeaf);
    }
  }
}

function reds(t: CTree): i32 {
  match (t) {
    CNode(Red(), l, k, r) => {
      return 1000 + k + reds(l) + reds(r);
    },
    CNode(Black(), l, k, r) => {
      return k + reds(l) + reds(r);
    },
    CLeaf => {
      return 0;
    }
  }
}

@noinline
function pick(own q: Quad): i32 {
  let a: i32[] = q.a;
  let n: i32 = a.len();
  let b: i32[] = q.b;
  return n + total(b);
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
  let c: CTree = CLeaf;
  let snaps: CTree[] = [];
  i = 0;
  while (i < 60) {
    c = cins(c, (i * 13) % 61);
    if (i % 7 == 0) {
      snaps = snaps.append(c);
    }
    i = i + 1;
  }
  if (reds(c) != 57782 || reds(snaps[2]) != 8450 || snaps.len() != 9) {
    bad = bad + 16;
  }
  let qd: Quad = Quad { a: [1, 2, 3], b: [4, 5], c: [6], d: [7] };
  let qd2: Quad = qd;
  if (pick(qd) != 12 || pick(Quad { a: [1], b: [10, 20], c: [30], d: [40] }) != 31 || total(qd2.c) + total(qd2.a) != 12) {
    bad = bad + 32;
  }
  return bad;
}
`

func TestInterpClassifiedTakeValues(t *testing.T) {
	if got := runInterpExit(t, classifiedTakeSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// checkClassifiedTake runs the program on one target: main's result is the exit
// code natively and stdout's one line on wasm, and the census is balanced.
func checkClassifiedTake(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, classifiedTakeSrc)
	if exit != 0 || stdout != "" && stdout != "0\n" {
		t.Fatalf("exit %d, stdout %q, want 0 (bits 1..32 name the shape that came back wrong); stderr: %s", exit, stdout, stderr)
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
