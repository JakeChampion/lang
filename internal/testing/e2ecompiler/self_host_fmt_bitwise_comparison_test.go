package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// The self-host formatter writes the parentheses where a bitwise operator and
// a comparison meet, as the Go printer does, and keeps the ones it finds.
func TestSelfHostFmtParenthesizesBitwiseInComparison(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `function f(a: i32, b: i32, c: boolean, d: boolean): boolean {
  let x = a & b != 0;
  let y = (a | 1) == b;
  let z = c & (a < b);
  let w = a ^ b << 1;
  return x && y && z && w == 0 && d;
}
`
	const want = `function f(a: i32, b: i32, c: boolean, d: boolean): boolean {
  let x = (a & b) != 0;
  let y = (a | 1) == b;
  let z = c & (a < b);
  let w = a ^ b << 1;
  return x && y && z && w == 0 && d;
}
`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
			t.Fatalf("format pass %d: %v\n%s", pass, err, out)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("format pass %d:\n%s\nwant:\n%s", pass, got, want)
		}
	}
}
