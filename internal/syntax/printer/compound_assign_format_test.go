package printer

import "testing"

const compoundAssignSrc = `function f(xs: i32[]): i32 {
  let x = 1;
  x += 1;
  x -= 2 + 3;
  x *= 4;
  x /= 2;
  x %= 7;
  x &= 6;
  x |= 1;
  x ^= 3;
  x <<= 2;
  x >>= 1;
  x = x + 1;
  let a = xs;
  a[0] += 2;
  a[1] = a[1] + 2;
  return x;
}
`

func TestFormatKeepsCompoundAssignment(t *testing.T) {
	got := formatSrc(t, compoundAssignSrc)
	if got != compoundAssignSrc {
		t.Errorf("formatting changed the written assignments:\n%s", got)
	}
	again := formatSrc(t, got)
	if again != got {
		t.Errorf("formatting is not a fixpoint:\n%s\n---\n%s", got, again)
	}
}
