package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const interpBinaryProg = `struct P { x: i32 }

function (p: P) add(k: i32): i32 {
  return p.x + k;
}

function widen(x: i32): i64 {
  return x;
}

function main(): i32 {
  let f: f64 = 1.5;
  let g: f32 = 2.5;
  print((f + 1).to_string());
  print((2 * f).to_string());
  print((g - 1).to_string());
  print((3 + g).to_string());
  let big: i32 = 2147483647;
  print((big + 1).to_string());
  print((7 % 0).to_string());
  print((7 / 2 * 3 - 1).to_string());
  let b: u8 = 250;
  print((b + 10).to_string());
  let w: i64 = 4000000000;
  print((w + 1).to_string());
  print(widen(-3).to_string());
  print(P { x: 40 }.add(2).to_string());
  print((1 < 2 && 3 >= 3).to_string());
  return 0;
}
`

const interpBinaryWant = "2.5\n3\n1.5\n5.5\n-2147483648\n7\n8\n4\n4000000001\n-3\n42\ntrue\n"

// An integer literal beside a float takes the width of the float, and a
// result keeps the width of its type. The interpreter and an x86-64 build
// print the same output.
func TestSelfHostInterpBinary(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpBinaryProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != interpBinaryWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, interpBinaryWant)
	}
	native, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatalf("x86-64: %v", err)
	}
	if string(native) != interpBinaryWant {
		t.Fatalf("x86-64 output:\n%s\nwant:\n%s", native, interpBinaryWant)
	}
}
