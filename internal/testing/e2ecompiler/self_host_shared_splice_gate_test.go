package e2ecompiler

import (
	"strings"
	"testing"
)

const grownSharedSrc = `enum T { E, N(T, i32, T) }

function depth(t: T): i32 {
  match (t) {
    N(l, _, r) => {
      let a: i32 = depth(l);
      let b: i32 = depth(r);
      if (a > b) {
        return a + 1;
      }
      return b + 1;
    },
    E => {
      return 0;
    }
  }
}

function rot_left(t: T): T {
  match (t) {
    N(a, k, N(b, k2, c)) => {
      return N(N(a, k, b), k2, c);
    },
    _ => {
      return t;
    }
  }
}

function rot_right(t: T): T {
  match (t) {
    N(N(a, k, b), k2, c) => {
      return N(a, k, N(b, k2, c));
    },
    _ => {
      return t;
    }
  }
}

function balance(l: T, k: i32, r: T): T {
  if (depth(l) > depth(r) + 1) {
    return rot_right(N(l, k, r));
  }
  if (depth(r) > depth(l) + 1) {
    return rot_left(N(l, k, r));
  }
  return N(l, k, r);
}

function ins(t: T, x: i32): T {
  match (t) {
    N(l, k, r) => {
      if (x < k) {
        return balance(ins(l, x), k, r);
      }
      return balance(l, k, ins(r, x));
    },
    E => {
      return N(E, x, E);
    }
  }
}

function sum(t: T): i32 {
  match (t) {
    N(l, k, r) => {
      return sum(l) + k + sum(r);
    },
    E => {
      return 0;
    }
  }
}

function main(): i32 {
  let t: T = E;
  let i: i32 = 0;
  while (i < 64) {
    t = ins(t, i);
    i = i + 1;
  }
  if (sum(t) != 2016 || depth(t) > 12) {
    return 1;
  }
  return 0;
}`

func TestSelfHostGrownSharedCulledX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, grownSharedSrc)
	if strings.Contains(asm, "\n__fn_balance:") {
		t.Errorf("balance is spliced into both of its callers but its body is still emitted")
	}
	if got := run(t, "grown_shared", asm); got != 0 {
		t.Errorf("exit = %d, want 0", got)
	}
}

const updateBaseSrc = `import "std/array";

struct P { a: i32[], n: i32 }

function mk(a: i32[], n: i32): P {
  if (n > 3) {
    return P { a: a, n: n };
  }
  return P { a: a.append(n), n: n + 1 };
}

@noinline
function step(p: P, c: boolean): P {
  let xs: i32[] = p.a;
  let q: P = mk(xs, p.n);
  if (c) {
    return q;
  }
  return P { ...q, a: [7], n: 0 };
}

@noinline
function grow(p: P): P {
  let xs: i32[] = p.a;
  return mk(xs, p.n);
}

function main(): i32 {
  let p: P = P { a: [1], n: 1 };
  let q: P = step(p, true);
  let r: P = step(q, false);
  let s: P = grow(q);
  if (q.a.len() != 2 || q.n != 2 || r.n != 0 || r.a[0] != 7 || s.a.len() != 3) {
    return 1;
  }
  return 0;
}`

func TestSelfHostSharedSpliceSeesUpdateBaseX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, updateBaseSrc)
	start := strings.Index(asm, "\n__fn_step:")
	if start < 0 {
		t.Fatalf("__fn_step not found in emitted asm")
	}
	body := asm[start+1:]
	for off := 0; ; {
		next := strings.Index(body[off+1:], "\n__fn_")
		if next < 0 {
			break
		}
		off += next + 1
		if !strings.HasPrefix(body[off+1:], "__fn_step.") {
			body = body[:off]
			break
		}
	}
	if !strings.Contains(body, "call __fn_mk") {
		t.Errorf("step: mk was spliced although its result is a record update's base")
	}
	if got := run(t, "update_base", asm); got != 0 {
		t.Errorf("exit = %d, want 0", got)
	}
}

const mutualSharedSrc = `struct Range { lo: i32, hi: i32, name: string }

function ping(lo: i32, n: i32, name: string): Range {
  if (n == 0) {
    return Range { lo: lo, hi: lo + 6, name: name };
  }
  let r: Range = pong(lo, n - 1, name);
  return Range { lo: r.lo, hi: r.hi + 1, name: r.name };
}

function pong(lo: i32, n: i32, name: string): Range {
  if (n == 0) {
    return Range { lo: lo, hi: lo + 4, name: name };
  }
  let r: Range = ping(lo, n - 1, name);
  return Range { lo: r.lo, hi: r.hi + 2, name: r.name };
}

@noinline function apart_ping(): i32 {
  let t: i32 = 0;
  let i: i32 = 0;
  while (i < 100) {
    let s: Range = ping(i, 3, "ab");
    t = t + s.hi - s.lo + s.name.len();
    i = i + 1;
  }
  return t;
}

function main(): i32 {
  if (apart_ping() != 1000) {
    return 1;
  }
  return 0;
}`

func TestSelfHostMutualRecursionNotSharedX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, mutualSharedSrc)
	body := asmFuncBody(t, asm, "__fn_apart_ping")
	if !strings.Contains(body, "call __fn_ping.") || strings.Count(body, "\n") > 200 {
		t.Errorf("apart_ping: ping was spliced although it recurses through pong:\n%s", body)
	}
	if got := run(t, "mutual_shared", asm); got != 0 {
		t.Errorf("exit = %d, want 0", got)
	}
}

const exhaustiveLeafSrc = `enum Color { Red, Black }
enum Tree { Node(Color, Tree, i32, Tree), Leaf }

function is_red(t: Tree): boolean {
  match (t) {
    Node(Red(), _, _, _) => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

@noinline
function probe(t: Tree): i32 {
  match (t) {
    Node(_, l, _, r) => {
      if (is_red(l)) {
        return 1;
      }
      if (is_red(r)) {
        return 2;
      }
      return 3;
    },
    Leaf => {
      return 0;
    }
  }
}

function main(): i32 {
  return probe(Node(Black, Leaf, 1, Node(Red, Leaf, 2, Leaf))) + probe(Leaf);
}`

func TestSelfHostExhaustiveLeafSplicedX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, exhaustiveLeafSrc)
	if strings.Contains(asm, "call __fn_is_red") {
		t.Errorf("is_red is still called: a leaf ending in an exhaustive match was not spliced")
	}
	if got := run(t, "exhaustive_leaf", asm); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
}
