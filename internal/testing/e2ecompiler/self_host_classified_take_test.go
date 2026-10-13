package e2ecompiler

import (
	"regexp"
	"strings"
	"testing"
)

// classifiedShapeSrc's `ins` takes both children of a node it rebuilds in a
// later block, and `bump` rebuilds the node's own variant in the block that
// takes them. Each reads its donor's uniqueness once: before, every take
// tested the donor again and its death a third time. `ins` also tests the
// token that answer leaves once: its takes' retains wait for its death's
// test rather than each testing it. main runs both on a unique tree and on
// one a snapshot holds.
const classifiedShapeSrc = `enum Tree { Leaf, Node(Tree, i32, Tree) }

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

@noinline
function bump(t: Tree): Tree {
  match (t) {
    Node(l, k, r) => {
      return Node(bump(l), k + 1, r);
    },
    Leaf => {
      return Leaf;
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

function spine(t: Tree): i32 {
  match (t) {
    Node(l, _, _) => {
      return 1 + spine(l);
    },
    Leaf => {
      return 0;
    }
  }
}

function main(): i32 {
  let t: Tree = Leaf;
  let kept: Tree = Leaf;
  let i: i32 = 0;
  while (i < 100) {
    t = ins(t, (i * 37) % 101);
    if (i == 50) {
      kept = t;
    }
    i = i + 1;
  }
  let bad: i32 = 0;
  if (sum(t) != 4986 || sum(kept) != 2533) {
    bad = bad + 1;
  }
  let b: Tree = bump(t);
  if (sum(b) - sum(t) != spine(t)) {
    bad = bad + 2;
  }
  if (sum(bump(ins(ins(Leaf, 2), 1))) != 5) {
    bad = bad + 4;
  }
  return bad;
}`

// fnText is the emitted text of function `name`, both entries, up to the
// next function's label.
func fnText(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n__fn_"+name+":")
	if start < 0 {
		t.Fatalf("__fn_%s not found in emitted asm", name)
	}
	body := asm[start+1:]
	for off := 0; ; {
		next := strings.Index(body[off+1:], "\n__fn_")
		if next < 0 {
			return body
		}
		off += next + 1
		if !strings.HasPrefix(body[off+1:], "__fn_"+name+".") {
			return body[:off]
		}
	}
}

// tokenTestRe matches a test of a register against zero, which in `ins` is
// only ever its token.
var tokenTestRe = regexp.MustCompile(`(?m)^\s+cmpq \$0, %r\w+$`)

func TestSelfHostClassifiedTakeShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, classifiedShapeSrc)
	for _, fn := range []string{"ins", "bump"} {
		if got := rcIsUniqueSites(fnText(t, asm, fn)); got != 1 {
			t.Errorf("%s: %d uniqueness tests, want 1: a take or the donor's death tested the donor again", fn, got)
		}
	}
	if got := len(tokenTestRe.FindAllString(fnText(t, asm, "ins"), -1)); got != 1 {
		t.Errorf("ins: %d tests of its token, want 1: a take's retain or the donor's death tested it again", got)
	}
	if got := run(t, "classified", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: insert, 2: bump of a shared tree, 4: bump of a unique one)", got)
	}
}
