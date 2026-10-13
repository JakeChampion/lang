package e2ecompiler

import (
	"regexp"
	"testing"
)

// A record's or an enum's value is a box: never null, never a tagged inline
// string. Its retains and uniqueness tests read the count with no guard, while
// a string's keep the tag bit and the heap floor.
const provenBoxRCSrc = `enum Tree { Leaf, Node(Tree, i32, Tree) }

struct Pair { a: Tree, b: Tree }

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
function twice(t: Tree): Pair {
  return Pair { a: t, b: t };
}

@noinline
function both(p: Pair): Pair {
  return Pair { a: p.b, b: p.a };
}

@noinline
function echo(s: string): string[] {
  return [s, s];
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

function main(): i32 {
  let t: Tree = Leaf;
  let i: i32 = 0;
  while (i < 50) {
    t = ins(t, (i * 37) % 101);
    i = i + 1;
  }
  let bad: i32 = 0;
  let p: Pair = both(twice(t));
  if (sum(p.a) + sum(p.b) != 2 * sum(t)) {
    bad = bad + 1;
  }
  let s: string = "a string too long" + " to be stored inline";
  let e: string[] = echo(s);
  if (e[0].len() != 37 || e[1] != e[0]) {
    bad = bad + 2;
  }
  return bad;
}`

var (
	tagGuardX86Re   = regexp.MustCompile(`(?m)^\s+testb \$1, %\w+$`)
	floorGuardX86Re = regexp.MustCompile(`(?m)^\s+cmpq \$0x10000, %\w+$`)
)

func TestSelfHostProvenBoxRCX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, provenBoxRCSrc)
	for _, fn := range []string{"ins", "twice", "both"} {
		body := fnText(t, asm, fn)
		if rcIncSites(body)+rcIsUniqueSites(body) == 0 {
			t.Fatalf("%s: no retain or uniqueness test, so the check reads nothing", fn)
		}
		if tagGuardX86Re.MatchString(body) {
			t.Errorf("%s: a record's or an enum's count is guarded by a tag test:\n%s", fn, body)
		}
	}
	if got := len(floorGuardX86Re.FindAllString(fnText(t, asm, "twice"), -1)); got != 0 {
		t.Errorf("twice: %d heap-floor guards, want 0", got)
	}
	echo := fnText(t, asm, "echo")
	if !tagGuardX86Re.MatchString(echo) || !floorGuardX86Re.MatchString(echo) {
		t.Errorf("echo: a string's retain lost its guards:\n%s", echo)
	}
	if got := run(t, "proven-box", asm); got != 0 {
		t.Errorf("exit %d, want 0", got)
	}
}

var (
	tagGuardArm64Re   = regexp.MustCompile(`(?m)^\s+(?:tbnz x\d+, #0,|tst x\d+, #1$)`)
	floorGuardArm64Re = regexp.MustCompile(`(?m)^\s+cmp x\d+, #16, lsl #12$`)
)

func TestSelfHostProvenBoxRCArm64(t *testing.T) {
	emit, run := arm64ShapeHarness(t)
	asm := emit(t, provenBoxRCSrc)
	for _, fn := range []string{"ins", "twice", "both"} {
		body := fnText(t, asm, fn)
		if rcIncSites(body)+rcIsUniqueSites(body) == 0 {
			t.Fatalf("%s: no retain or uniqueness test, so the check reads nothing", fn)
		}
		if tagGuardArm64Re.MatchString(body) {
			t.Errorf("%s: a record's or an enum's count is guarded by a tag test:\n%s", fn, body)
		}
	}
	if got := len(floorGuardArm64Re.FindAllString(fnText(t, asm, "twice"), -1)); got != 0 {
		t.Errorf("twice: %d heap-floor guards, want 0", got)
	}
	echo := fnText(t, asm, "echo")
	if !tagGuardArm64Re.MatchString(echo) || !floorGuardArm64Re.MatchString(echo) {
		t.Errorf("echo: a string's retain lost its guards:\n%s", echo)
	}
	if got := run(t, "proven-box", asm); got != 0 {
		t.Errorf("exit %d, want 0", got)
	}
}
