package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// sum and size each walk a tree and return a number. Each call of sum gives
// it a tree that the caller owns and does not use again. One call of size
// gives it a tree that the caller reads later.
const dyingTraversalProg = `enum Tree { Leaf, Node(Tree, i64, Tree) }

@noinline function build(n: i64): Tree {
  if (n <= 0i64) {
    return Leaf;
  }
  return Node(build(n - 1i64), n, build(n - 2i64));
}

@noinline function sum(t: Tree): i64 {
  match (t) {
    Node(l, v, r) => {
      return sum(l) + v + sum(r);
    },
    Leaf => {
      return 0i64;
    },
  }
}

@noinline function size(t: Tree): i64 {
  match (t) {
    Node(l, _, r) => {
      return size(l) + 1i64 + size(r);
    },
    Leaf => {
      return 0i64;
    },
  }
}

function main(): i32 {
  let kept = build(12i64);
  let first = size(kept);
  let second = size(kept);
  if (first != second || first != 376i64) {
    return 1;
  }
  if (sum(build(12i64)) != 972i64) {
    return 2;
  }
  if (sum(kept) != 972i64) {
    return 3;
  }
  return 0;
}
`

// A recursive walk that returns no reference consumes its parameter when
// each call gives it a value that dies at the call. The walk then releases
// each box while it reads it, and the caller has no second walk to release
// the tree. A walk with a call that keeps its argument borrows the parameter.
func TestSelfHostDyingTraversalConsumes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(dyingTraversalProg), 0o644); err != nil {
		t.Fatal(err)
	}
	release := regexp.MustCompile(`__sem_release_|__sem_drop_|__fern_box_dec|__fern_box_free|__fern_arr_dec`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(asm)
			if body := tailCallBody(t, text, "sum"); !release.MatchString(body) {
				t.Errorf("sum does not release the tree that it consumes:\n%s", body)
			}
			if body := tailCallBody(t, text, "size"); release.MatchString(body) {
				t.Errorf("size releases a tree that it borrows:\n%s", body)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, env := range []string{"", "FERN_LEAKCHECK=1"} {
			if stderr, code := cli.exitOf(t, dyingTraversalProg, target, env); code != 0 {
				t.Errorf("%s %s: exit %d, want 0\n%s", target, env, code, stderr)
			}
		}
	}
}
