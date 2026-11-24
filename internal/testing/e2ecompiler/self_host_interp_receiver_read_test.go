package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const interpReceiverReadProg = `struct P { x: i32 }

function (p: P) get(): i32 {
  return p.x;
}

function (p: P) plus(n: i32): P {
  return P { x: p.x + n };
}

enum Shape { Dot, Box(i32) }

function (s: Shape) area(): i32 {
  return match (s) { Shape.Dot => 0, Shape.Box(n) => n * n };
}

const ORIGIN: P = P { x: 40 };

function main(): i32 {
  let p = P { x: 3 };
  print(p.get().to_string());
  print(p.plus(4).get().to_string());
  print(ORIGIN.get().to_string());
  let b = Shape.Box(5);
  print(b.area().to_string());
  let total = 0;
  let add = (n: i32) => { total = total + p.get() + n; };
  add(1);
  add(2);
  print(total.to_string());
  let q = p;
  for i in 0..3 {
    q = q.plus(i);
    if (i == 1) {
      let q2 = q.plus(100);
      q = q2;
    }
  }
  print(q.get().to_string());
  let xs: i32[] = [];
  xs = xs.append(q.get());
  print(xs.len().to_string());
  return 0;
}
`

const interpReceiverReadWant = "3\n7\n40\n25\n9\n106\n1\n"

// A method call reads a receiver that is a name one time: a local, a constant,
// and a binding that a closure captured. An assignment finds its target in the
// scope of the statement, in an outer scope, and in a captured cell.
func TestSelfHostInterpReceiverRead(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpReceiverReadProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != interpReceiverReadWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, interpReceiverReadWant)
	}
	native, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatalf("x86-64: %v", err)
	}
	if string(native) != interpReceiverReadWant {
		t.Fatalf("x86-64 output:\n%s\nwant:\n%s", native, interpReceiverReadWant)
	}
}
