package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const interpQualifiedCallProg = `import "std/base64";

struct Box { n: i32 }

function (b: Box) twice(): i32 {
  return b.n * 2;
}

function pick(base64: Box): i32 {
  return base64.twice();
}

function main(): i32 {
  let box = Box { n: 4 };
  print(box.twice().to_string());
  print(base64.encode("fern".bytes()));
  print(pick(Box { n: 5 }).to_string());
  return 0;
}
`

const interpQualifiedCallWant = "8\nZmVybg==\n10\n"

// A call on an identifier is a call of a module function only when the module
// has that function. A local with the name of a module keeps its methods.
func TestSelfHostInterpQualifiedCall(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpQualifiedCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != interpQualifiedCallWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, interpQualifiedCallWant)
	}
	native, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatalf("x86-64: %v", err)
	}
	if string(native) != interpQualifiedCallWant {
		t.Fatalf("x86-64 output:\n%s\nwant:\n%s", native, interpQualifiedCallWant)
	}
}
