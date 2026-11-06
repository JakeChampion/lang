package printer

import "testing"

// Where a bitwise operator and a comparison meet, the formatter writes the
// parentheses, in either order.
func TestFormatParenthesizesBitwiseInComparison(t *testing.T) {
	const src = `function f(a: i32, b: i32, c: boolean, d: boolean): boolean {
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
	got := formatSrc(t, src)
	if got != want {
		t.Errorf("format:\n%s\nwant:\n%s", got, want)
	}
	if again := formatSrc(t, got); again != got {
		t.Errorf("formatting is not a fixpoint:\n%s", again)
	}
}
