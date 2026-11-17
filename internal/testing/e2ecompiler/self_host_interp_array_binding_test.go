package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A binding of an array keeps the array when it has its element type, and
// gives the declared element type to an array that has none.
const interpArrayBindingProg = `function sum(xs: i32[]): i32 {
  let s = 0;
  for x in xs {
    s = s + x;
  }
  return s;
}

function doubled_last(own xs: u8[]): i32 {
  let ys = xs.append(250);
  let n = ys.len() - 1;
  return (ys[n] + ys[n]) as i32;
}

function view_sum(xs: [i32]): i32 {
  let s = 0;
  for x in xs {
    s = s + x;
  }
  return s;
}

function tail_sum(xs: [i32]): i32 {
  return view_sum(xs[1:xs.len()]);
}

function second_row(m: i32[][]): i32 {
  return sum(m[1]);
}

function main(): i32 {
  let typed: i32[] = [1, 2, 3];
  print("typed " + sum(typed).to_string());
  print("literal " + sum([4, 5]).to_string());
  let none: u8[] = [];
  print("empty " + doubled_last(none).to_string());
  print("bare " + doubled_last([]).to_string());
  let bytes: u8[] = [200, 100];
  print("bytes " + ((bytes[0] + bytes[1]) as i32).to_string());
  print("view " + view_sum(typed[0:2]).to_string());
  print("view of view " + tail_sum(typed[0:3]).to_string());
  print("nested " + second_row([[1], [7, 8]]).to_string());
  let again = typed;
  print("rebound " + sum(again).to_string());
  return 0;
}
`

const interpArrayBindingWant = `typed 6
literal 9
empty 244
bare 244
bytes 44
view 3
view of view 5
nested 15
rebound 6
`

func TestSelfHostInterpArrayBinding(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpArrayBindingProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != interpArrayBindingWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, interpArrayBindingWant)
	}
	native, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatalf("x86-64: %v", err)
	}
	if string(native) != interpArrayBindingWant {
		t.Fatalf("x86-64 output:\n%s\nwant:\n%s", native, interpArrayBindingWant)
	}
}
