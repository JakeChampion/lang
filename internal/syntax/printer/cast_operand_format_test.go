package printer

import "testing"

const castOperandSrc = `function f(x: i32, r: Result[i32, string]): i64 {
  let a = (-x) as i64;
  let b = -(x as i64);
  let c = (-1.5) as i32;
  let d = (-(x as i64)) as u64;
  let e = (-r?) as i64;
  let g = x as i64 + -(x as i64);
  return a + b + c as i64 + d as i64 + e + g;
}
`

func TestFormatKeepsParensAroundNegatedCastOperand(t *testing.T) {
	got := formatSrc(t, castOperandSrc)
	if got != castOperandSrc {
		t.Errorf("formatting changed what the casts apply to:\n%s", got)
	}
	again := formatSrc(t, got)
	if again != got {
		t.Errorf("formatting is not a fixpoint:\n%s\n---\n%s", got, again)
	}
}
