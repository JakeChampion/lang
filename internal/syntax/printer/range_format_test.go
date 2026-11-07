package printer

import "testing"

const rangeSrc = `import "core/iter" as it;

function f(n: i32): i32 {
  let r = 0..n;
  let s = 1 + 2..=n * 4;
  let t = it.range(0, 3);
  let total = 0;
  for x in (0..n) {
    total += x;
  }
  total += it.sum(0..=n);
  return total;
}
`

func TestFormatKeepsRangeExpressions(t *testing.T) {
	got := formatSrc(t, rangeSrc)
	if got != rangeSrc {
		t.Errorf("formatting changed the written ranges:\n%s", got)
	}
	if again := formatSrc(t, got); again != got {
		t.Errorf("formatting is not idempotent:\n%s", again)
	}
}
