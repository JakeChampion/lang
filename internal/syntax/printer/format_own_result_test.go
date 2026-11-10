package printer

import (
	"strings"
	"testing"
)

func TestFormatOwnedResults(t *testing.T) {
	const source = `function fold[T](own x: T, visit: (own T) => own T): own T { return visit(x); }
function main(): i32 {
  function make(): own i32[] { return [7]; }
  let factory: () => own i32[] = make;
  let f = (own x: string): own string => x;
  return factory()[0];
}`
	got := formatSrc(t, source)
	for _, part := range []string{"visit: (own T) => own T", "): own T", "): own string => x", "function make(): own i32[]"} {
		if !strings.Contains(got, part) {
			t.Fatalf("lost %q in:\n%s", part, got)
		}
	}
	mustCheck(t, got, "formatted output")
	if again := formatSrc(t, got); again != got {
		t.Fatalf("format changed on second pass:\n%s", again)
	}
}
