package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// plus and minus read an i32 value as the right operand of a 64-bit
// addition or subtraction. reversed subtracts from the i32 value, and twice
// reads the extended value two times. chain adds three i32 values.
const extendOperandProg = `@noinline function plus(x: i64, y: i32): i64 { return x + (y * 3) as i64; }
@noinline function minus(x: i64, y: i32): i64 { return x - (y + 1) as i64; }
@noinline function reversed(x: i64, y: i32): i64 { return (y + 1) as i64 - x; }
@noinline function twice(x: i64, y: i32): i64 { let w = (y + 1) as i64; return x + w + w; }
@noinline function chain(a: i32, b: i32, c: i32): i32 { return a + b + c; }
function main(): i32 {
  if (plus(10i64, 4) != 22i64 || plus(10i64, 0 - 4) != 0i64 - 2i64) { return 1; }
  if (plus(10i64, 1431655766) != 12i64) { return 2; }
  if (minus(10i64, 4) != 5i64 || minus(10i64, 0 - 9) != 18i64) { return 3; }
  if (minus(0i64, 2147483647) != 2147483648i64) { return 4; }
  if (reversed(10i64, 4) != 0i64 - 5i64 || reversed(0i64, 2147483647) != 0i64 - 2147483648i64) { return 5; }
  if (twice(1i64, 4) != 11i64 || twice(1i64, 0 - 3) != 0i64 - 3i64) { return 6; }
  if (chain(2147483647, 1, 5) != 0 - 2147483643 || chain(0 - 7, 3, 2) != 0 - 2) { return 7; }
  return 0;
}
`

var extendedOperand = regexp.MustCompile(`(?m)^\s+(add|sub) x\d+, x\d+, w\d+, sxtw$`)

// On arm64 a sign extension of 32 bits that feeds only the addition or the
// subtraction after it is an operand of that instruction: add or sub with
// `sxtw`. An extended value that is the left operand of a subtraction, or
// that has two readers, keeps its sxtw.
func TestSelfHostSignExtensionIsAnOperand(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(extendOperandProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, op string }{{"plus", "add"}, {"minus", "sub"}} {
		body := tailCallBody(t, string(arm), tc.name)
		m := extendedOperand.FindStringSubmatch(body)
		if m == nil || m[1] != tc.op || strings.Contains(body, "    sxtw ") {
			t.Errorf("arm64: %s wants one %s with sxtw and no sxtw instruction:\n%s", tc.name, tc.op, body)
		}
	}
	for _, name := range []string{"reversed", "twice"} {
		if body := tailCallBody(t, string(arm), name); !strings.Contains(body, "    sxtw ") {
			t.Errorf("arm64: %s wants an sxtw instruction:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, extendOperandProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
