package e2e

import (
	"strings"
	"testing"
)

const decidedBranchSrc = `enum Color { Red, Black }
enum Shade { Light, Mid, Dark }

enum Tree {
  Node(Color, Tree, i32, Tree),
  Leaf,
}

enum Swatch {
  Patch(Shade, i32),
  Blank,
}

enum Chain {
  Link(Color, i32, Chain),
  End,
}

enum Pick {
  Plain,
  Flagged(boolean, i32),
}

struct Rec { n: i64, res: Result[i32, i32], ok: boolean }

function same[T](x: T): T {
  return x;
}

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
function kind(t: Tree): i32 {
  match (t) {
    Node(Red(), l, k, r) => {
      return 100 + k;
    },
    Node(Black(), l, k, r) => {
      return 200 + k;
    },
    Leaf => {
      return 300;
    }
  }
}

@noinline
function weight(s: Swatch): i32 {
  match (s) {
    Patch(Light(), n) => {
      return n;
    },
    Patch(Mid(), n) => {
      return 10 * n;
    },
    Patch(Dark(), n) => {
      return 100 * n;
    },
    Blank => {
      return 0 - 1;
    }
  }
}

@noinline
function reddish(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) => {
      return k;
    },
    _ => {
      return 0 - k_of(t);
    }
  }
}

function k_of(t: Tree): i32 {
  match (t) {
    Node(_, _, k, _) => {
      return k;
    },
    Leaf => {
      return 0;
    }
  }
}

@noinline
function not_red(t: Tree): i32 {
  if (!is_red(t)) {
    match (t) {
      Node(Black(), _, k, _) => {
        return k;
      },
      _ => {
        return 7;
      }
    }
  }
  return 0 - 1;
}

@noinline
function valued(t: Tree): i32 {
  let v: i32 = match (t) {
    Node(Red(), _, k, _) => k,
    Node(Black(), _, k, _) => 2 * k,
    Leaf => 5,
  };
  return v + 1;
}

@noinline
function guarded(t: Tree): i32 {
  match (t) {
    Node(Red(), _, k, _) when k > 5 => {
      return 1;
    },
    Node(Red(), _, k, _) => {
      return 2;
    },
    Node(Black(), Node(Red(), _, _, _), _, _) => {
      return 3;
    },
    Node(Black(), _, _, _) => {
      return 4;
    },
    Leaf => {
      return 5;
    }
  }
}

@noinline
function reds(c: Chain, acc: i32): i32 {
  match (c) {
    Link(Red(), n, rest) => {
      return reds(rest, acc + n);
    },
    Link(Black(), n, rest) => {
      return reds(rest, acc - n);
    },
    End => {
      return acc;
    }
  }
}

@noinline
function rejoined(v: Pick): i32 {
  let r: Rec = if (match (v) { Plain => if (false) { false } else { match (v) { Plain => false, Flagged(_, _) => true } }, Flagged(_, _) => true }) {
    Rec {
      n: match (v) { Plain => if (true) { 885i64 } else { 967i64 }, Flagged(_, _) => same(940i64) },
      res: if (if (true) { true } else { false }) { Err(680) } else { match (v) { Plain => Ok(361), Flagged(_, k) => Err(k) } },
      ok: match (v) { Plain => false, Flagged(flag, _) => flag && flag },
    }
  } else {
    match (v) { Plain => Rec { n: 1i64, res: Ok(815), ok: true }, Flagged(_, k) => Rec { n: 2i64, res: Err(k), ok: false } }
  };
  if (r.ok) {
    return 1;
  }
  return 2;
}

function main(): i32 {
  let red: Tree = Node(Red, Leaf, 3, Leaf);
  let black: Tree = Node(Black, red, 9, Leaf);
  let leaf: Tree = Leaf;
  let bad: i32 = 0;
  if (kind(red) != 103 || kind(black) != 209 || kind(leaf) != 300) {
    bad = bad + 1;
  }
  if (weight(Patch(Light, 2)) != 2 || weight(Patch(Mid, 2)) != 20 || weight(Patch(Dark, 2)) != 200 || weight(Blank) != 0 - 1) {
    bad = bad + 2;
  }
  if (reddish(red) != 3 || reddish(black) != 0 - 9 || reddish(leaf) != 0) {
    bad = bad + 4;
  }
  if (not_red(red) != 0 - 1 || not_red(black) != 9 || not_red(leaf) != 7) {
    bad = bad + 8;
  }
  if (valued(red) != 4 || valued(black) != 19 || valued(leaf) != 6) {
    bad = bad + 16;
  }
  let big: Tree = Node(Red, Leaf, 8, Leaf);
  let plain: Tree = Node(Black, Leaf, 1, Leaf);
  if (guarded(big) != 1 || guarded(red) != 2 || guarded(black) != 3 || guarded(plain) != 4 || guarded(leaf) != 5) {
    bad = bad + 32;
  }
  let c: Chain = End;
  let i: i32 = 0;
  while (i < 10) {
    if (i % 3 == 0) {
      c = Link(Black, i, c);
    } else {
      c = Link(Red, i, c);
    }
    i = i + 1;
  }
  if (reds(c, 0) != 27 - 18) {
    bad = bad + 64;
  }
  if (rejoined(Flagged(true, 5)) != 1 || rejoined(Plain) != 1 || rejoined(Flagged(false, 6)) != 2) {
    bad = bad + 128;
  }
  return bad;
}
`

func TestInterpDecidedBranchValues(t *testing.T) {
	if got := runInterpExit(t, decidedBranchSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func checkDecidedBranch(t *testing.T, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, decidedBranchSrc)
	if exit != 0 || stdout != "" && stdout != "0\n" {
		t.Fatalf("exit %d, stdout %q, want 0 (bits 1..128 name the shape that came back wrong); stderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64DecidedBranchSanitized(t *testing.T) {
	checkDecidedBranch(t, runSanitizeX86_64)
}

func TestArm64DecidedBranchSanitized(t *testing.T) {
	checkDecidedBranch(t, runSanitizeArm64)
}

func TestWASMDecidedBranchSanitized(t *testing.T) {
	checkDecidedBranch(t, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
