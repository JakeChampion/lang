package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

// The self-host formatter keeps the 16-bit types and literal suffixes, and it
// writes what the Go printer writes for them.
func TestSelfHostFmtSixteenBitIntegers(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = "function f(a:u16,b:i16):u16[]{let xs:i16[]=[b,-2i16,0x7fff];return [a,65535u16,xs.len() as u16];}\n"
	const want = "function f(a: u16, b: i16): u16[] {\n  let xs: i16[] = [b, -2i16, 0x7fff];\n  return [a, 65535u16, xs.len() as u16];\n}\n"
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", path).Output()
	if err != nil {
		t.Fatalf("fern -fmt: %v", err)
	}
	if string(out) != want {
		t.Errorf("self-host -fmt =\n%s\nwant\n%s", out, want)
	}
	prog, err := goparser.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := goprinter.Format(prog); got != want {
		t.Errorf("Go printer =\n%s\nwant\n%s", got, want)
	}
}
