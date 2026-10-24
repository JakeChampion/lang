package e2ecompiler

import (
	"strings"
	"testing"
)

const soleReleaseSrc = `enum Tree { Leaf, Node(Tree, i32, Tree) }

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
function depth(own t: Tree): i32 {
  match (t) {
    Node(l, _, r) => {
      let a: i32 = depth(l);
      let b: i32 = depth(r);
      if (a > b) {
        return a + 1;
      }
      return b + 1;
    },
    Leaf => {
      return 0;
    }
  }
}

@noinline
function build(n: i32): Tree {
  let t: Tree = Leaf;
  let i: i32 = 0;
  while (i < n) {
    t = ins(t, (i * 37) % 101);
    i = i + 1;
  }
  return t;
}

function main(): i32 {
  let kept: Tree = build(100);
  let bad: i32 = 0;
  if (depth(ins(kept, 1000)) != 10) {
    bad = bad + 1;
  }
  if (depth(kept) != 10) {
    bad = bad + 2;
  }
  if (depth(ins(ins(Leaf, 2), 1)) != 2) {
    bad = bad + 4;
  }
  return bad;
}`

func soleReleaseShape(t *testing.T, body, call string) {
	t.Helper()
	if got := rcIsUniqueSites(body); got != 1 {
		t.Errorf("depth: %d uniqueness tests, want 1", got)
	}
	if got := strings.Count(body, call+" __fn___fern_arr_dec.u"); got != 1 {
		t.Errorf("depth: %d sole frees, want 1:\n%s", got, body)
	}
	if strings.Contains(body, "_rcdecd") {
		t.Errorf("depth: a release ladder reads the count the take already read:\n%s", body)
	}
}

func TestSelfHostSoleReleaseShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, soleReleaseSrc)
	soleReleaseShape(t, fnText(t, asm, "depth"), "call")
	if got := run(t, "sole", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: shared children, 2: snapshot at its last use, 4: fresh tree)", got)
	}
	cli := newStrictCLI(t)
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", soleReleaseSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	quarantine := buildBin(t, cli.gcc, t.TempDir(), "quarantine", cli.emit(t, "x86-64-linux", soleReleaseSrc, "FERN_RC_FREE_DEBUG=1"))
	if stderr, exit := runWithStdin(t, cli.runner, quarantine, nil); exit != 0 {
		t.Fatalf("quarantine run: exit %d, want 0\n%s", exit, stderr)
	}
}

func TestSelfHostSoleReleaseShapeArm64(t *testing.T) {
	emit, run := arm64ShapeHarness(t)
	asm := emit(t, soleReleaseSrc)
	soleReleaseShape(t, fnText(t, asm, "depth"), "bl")
	if got := run(t, "sole", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: shared children, 2: snapshot at its last use, 4: fresh tree)", got)
	}
}

func TestSelfHostSoleReleaseWasm(t *testing.T) {
	cli := newStrictCLI(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", soleReleaseSrc)); code != 0 {
		t.Fatalf("wasm: exit %d, stdout %q; want 0", code, out)
	}
}

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

func TestSelfHostClassifiedTakeShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, classifiedShapeSrc)
	for _, fn := range []string{"ins", "bump"} {
		if got := rcIsUniqueSites(fnText(t, asm, fn)); got != 1 {
			t.Errorf("%s: %d uniqueness tests, want 1: a take or the donor's death tested the donor again", fn, got)
		}
	}
	if got := run(t, "classified", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: insert, 2: bump of a shared tree, 4: bump of a unique one)", got)
	}
}
