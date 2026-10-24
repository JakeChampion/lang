package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const genericKeySplitModule = `pub struct Far { max: i32 }
pub struct FarBox[T] { inner: T }
pub enum FarPick { Left, Right(i32) }
pub function make(n: i32): Far { return Far { max: n }; }
pub function boxed[T](v: T): FarBox[T] { return FarBox { inner: v }; }
`

const genericKeySplitMain = `import "./mod/far";

struct Call[A, B, T] { id: i32, f: (A, B) => T, a: A, b: B }
struct Pair[K, V] { k: K, v: V }
struct Box[T] { inner: T }
enum Either[L, R] { Lft(L), Rgt(R) }

function handle(n: i32, x: far.Far): i32 { return n + x.max; }
function pick(n: i32, p: far.FarPick): i32 {
  match (p) {
    far.Left => { return n; },
    far.Right(k) => { return n + k; }
  }
  return 0;
}

// Bound from a clone value: the parameters come back out of the clone's
// mangled name.
function second[A, B, T](c: Call[A, B, T]): B { return c.b; }
function first[K, V](p: Pair[K, V]): K { return p.k; }

function main(): i32 {
  let x: far.Far = far.make(5);
  let total: i32 = 0;
  // A module-qualified struct in the middle of a three-parameter key.
  let c: Call[i32, far.Far, i32] = Call { id: 1, f: handle, a: 3, b: x };
  total = total + c.f(c.a, c.b) + c.id + second(c).max;
  // A module-qualified enum as an argument.
  let e: Call[i32, far.FarPick, i32] = Call { id: 2, f: pick, a: 10, b: far.Right(7) };
  total = total + e.f(e.a, e.b) + e.id;
  // A nested clone first, a module-qualified generic clone second.
  let p: Pair[Box[i32], far.FarBox[i32]] = Pair { k: Box { inner: 100 }, v: far.boxed(1000) };
  total = total + first(p).inner + p.v.inner;
  // A module-qualified struct in a generic enum's two-parameter key.
  let l: Either[far.Far, i32] = Lft(x);
  match (l) {
    Lft(f) => { total = total + f.max * 10000; },
    Rgt(_) => {}
  }
  if (total != 51133) {
    return 1;
  }
  return 0;
}
`

func TestSelfHostGenericKeySplit(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"main.fern": genericKeySplitMain, filepath.Join("mod", "far.fern"): genericKeySplitModule} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), target, nil, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0 (1: a wrong total)\n%s", code, stderr)
			}
		})
	}
}
