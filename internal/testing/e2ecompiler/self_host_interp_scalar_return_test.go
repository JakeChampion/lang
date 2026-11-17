package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Each function gives its result the width of its declared type. The
// results of a tuple, a struct and an array keep their own shapes.
const interpScalarReturnProg = `struct Pt { x: i32, y: i32 }

function byte(): u8 { return 200; }
function half(): u16 { return 65535; }
function short(): i16 { return 32767; }
function word(): u32 { return 1; }
function int(x: i32): i32 { return x + 1; }
function wide(): i64 { return 1; }
function uwide(): u64 { return 1; }
function size(): usize { return 5; }
function third(): f32 { return 1.0 / 3.0; }
function whole(): f64 { return 2.0; }
function pair(): (u8, i64) { return (200, 1); }
function point(): Pt { return Pt { x: 3, y: 4 }; }
function list(): u8[] { return [250, 251]; }

function main(): i32 {
  print("u8 " + ((byte() + byte()) as i32).to_string());
  print("u16 " + ((half() + half()) as i32).to_string());
  print("i16 " + ((short() + short()) as i32).to_string());
  print("u32 " + (if (word() - 2 > 100) { "unsigned" } else { "signed" }));
  print("i32 " + int(2147483647).to_string());
  print("i64 " + (if (wide() * 4294967296 > 0) { "wide" } else { "narrow" }));
  print("u64 " + (if (uwide() - 2 > 100) { "unsigned" } else { "signed" }));
  print("usize " + (size() as i32).to_string());
  print("f32 " + (if ((third() as f64) == 1.0 / 3.0) { "f64" } else { "f32" }));
  print("f64 " + (whole() as i32).to_string());
  let p = pair();
  print("tuple " + ((p.0 + p.0) as i32).to_string() + " " + (if (p.1 * 4294967296 > 0) { "wide" } else { "narrow" }));
  print("struct " + (point().x + point().y).to_string());
  let xs = list();
  print("array " + ((xs[0] + xs[1]) as i32).to_string());
  return 0;
}
`

const interpScalarReturnWant = `u8 144
u16 65534
i16 -2
u32 unsigned
i32 -2147483648
i64 wide
u64 unsigned
usize 5
f32 f32
f64 2
tuple 144 wide
struct 7
array 245
`

func TestSelfHostInterpScalarReturnWidths(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpScalarReturnProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != interpScalarReturnWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, interpScalarReturnWant)
	}
	native, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatalf("x86-64: %v", err)
	}
	if string(native) != interpScalarReturnWant {
		t.Fatalf("x86-64 output:\n%s\nwant:\n%s", native, interpScalarReturnWant)
	}
}
