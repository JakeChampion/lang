package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// util.hex_value gives the value of a hex digit in either case and -1 for
// each other byte. util.hex_digits needs one or more bytes from its offset.
func TestSelfHostUtilHexValueReadsBothCases(t *testing.T) {
	src := `import "./util";

function shown(b: boolean): string {
  if (b) {
    return "1";
  }
  return "0";
}

function main(): i32 {
  let digits = "0123456789abcdef";
  let upper = "0123456789ABCDEF";
  let sum = 0;
  for i in 0..16 {
    if (util.hex_value(digits[i]) != i || util.hex_value(upper[i]) != i) {
      return 1;
    }
    sum += util.hex_value(util.hex_digit(i));
  }
  let refused = 0;
  for c in 0..256 {
    if (util.hex_value(c as u8) < 0) {
      refused += 1;
    }
  }
  let out = sum.to_string() + " " + refused.to_string() + " ";
  out += shown(util.hex_digits("0x1aF", 2));
  out += shown(util.hex_digits("ff", 0));
  out += " ";
  out += shown(util.hex_digits("0x", 2));
  out += shown(util.hex_digits("0x1g", 2));
  out += shown(util.hex_digits("0x1 ", 2));
  out += shown(util.hex_digits("", 0));
  print(out);
  return 0;
}
`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "util.fern")
	if err := os.WriteFile(filepath.Join(dir, "hexvalue.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "hexvalue.fern", "hexvalue")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("hexvalue: %v\n%s", err, out)
	}
	if want := "120 234 11 0000\n"; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
