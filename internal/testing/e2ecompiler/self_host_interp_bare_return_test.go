package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A bare `return;` leaves a void function early under the self-host
// interpreter, as it does compiled: the parser's `punct:;` placeholder in the
// value slot is not an expression to evaluate.
func TestSelfHostInterpBareReturn(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := `function f(x: i32): void {
  if (x > 1) {
    print("big");
    return;
  }
  print("small");
}

function main(): i32 {
  f(3);
  f(0);
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != "big\nsmall\n" {
		t.Fatalf("output %q, want %q", out, "big\nsmall\n")
	}
}
