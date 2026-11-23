package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A method call resolves the static type of its receiver in an environment
// that binds no type parameter, in one that binds a type parameter or a const
// parameter, and in a closure that a generic function made.
const interpTypeNamesProg = `struct P { n: i32 }
function (p: P) get(): i32 { return p.n; }

struct Box[T] { v: T }
function (b: Box[T]) first(): T { return b.v; }
function (b: Box[T]) pair(): T[] { let out: T[] = [b.v, b.v]; return out; }

struct Buf[const N: i32] { bytes: u8[] }
function (b: Buf[N]) cap(): i32 { return N; }

trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
impl Shape for Rect { function area(self: Rect): i32 { return self.w * self.h; } }

function total(shapes: dyn Shape[]): i32 {
  let sum = 0;
  for s in shapes {
    sum = sum + s.area();
  }
  return sum;
}

function twice[T](x: T): T[] {
  let xs: T[] = [x, x];
  return xs;
}

function lens[T](rows: T[][]): i32 {
  let n = 0;
  for r in rows {
    let row: T[] = r;
    n = n + row.len();
  }
  return n;
}

function later[T](x: T): () => i32 {
  return (): i32 => {
    let xs: T[] = [x, x, x];
    return xs.len();
  };
}

function ring[const N: i32, T](x: T): i32 {
  let b: Buf[N] = Buf { bytes: [] };
  let xs: T[] = [x];
  return b.cap() + xs.len();
}

function main(): i32 {
  let p: P = P { n: 3 };
  let xs: i32[] = [1, 2, 3];
  let rows: i32[][] = [[1], [2, 3]];
  print(p.get() + xs.len() + rows.len());
  let b: Box[i32] = Box { v: 7 };
  print(b.first() + b.pair().len());
  let buf: Buf[4] = Buf { bytes: [] };
  print(buf.cap());
  let shapes: dyn Shape[] = [Sq { s: 2 }, Rect { w: 2, h: 5 }];
  print(total(shapes));
  print(twice("a").len() + lens(rows));
  let f = later(1 as u8);
  print(f());
  print(ring[6, string]("z"));
  return 0;
}
`

const interpTypeNamesWant = "8\n9\n4\n14\n5\n3\n7\n"

func TestSelfHostInterpTypeNames(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpTypeNamesProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != interpTypeNamesWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, interpTypeNamesWant)
	}
	native, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatalf("x86-64: %v", err)
	}
	if string(native) != interpTypeNamesWant {
		t.Fatalf("x86-64 output:\n%s\nwant:\n%s", native, interpTypeNamesWant)
	}
}
