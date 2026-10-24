package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func usizeCase(t *testing.T, name string) (src, want string) {
	t.Helper()
	dir := filepath.Join(e2eharness.ConformanceCases(), name)
	src = filepath.Join(dir, "main.fern")
	b, err := os.ReadFile(filepath.Join(dir, "expected.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	return src, string(b)
}

const interpUsizeProgram = `import "core/int";
import "std/u64";

function wide(a: i64): usize {
    return a as usize;
}

function main(): i32 {
    let z: usize = 0;
    let max: usize = 18446744073709551615;
    print((z - 1).to_string());
    print((wide(4294967295) + wide(1)).to_string());
    print((max + 1).to_string());
    print((max >> 1).to_string());
    print((max / 3).to_string());
    print((wide(1) << 64).to_string());
    print((-wide(1)).to_string());
    print((10000000000.0 as usize).to_string());
    if (z - 1 > z) {
        print("a borrow reads unsigned");
    }
    print(f"{max}");
    return 0;
}
`

func TestSelfHostInterpUsizeIsTheHostWidth(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(interpUsizeProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	want := "18446744073709551615\n4294967296\n0\n9223372036854775807\n6148914691236517205\n1\n" +
		"18446744073709551615\n10000000000\na borrow reads unsigned\n18446744073709551615\n"
	if string(out) != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestSelfHostWasmRefusesAUsizeLiteralPast32Bits(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := `function main(): i32 {
  let x: usize = 4294967296;
  if (x > 0) {
    return 1;
  }
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cli.emit(t, src, "x86-64-linux")
	out := filepath.Join(t.TempDir(), "out.wat")
	msg, err := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "asm", "-o", out, src, cli.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("wasm32 accepted a usize literal past u32::MAX:\n%s", msg)
	}
	if want := "literal 4294967296 does not fit in usize on wasm32"; !strings.Contains(string(msg), want) {
		t.Fatalf("diagnostic %q does not say %q", msg, want)
	}
}
