package checker

import (
	"strings"
	"testing"
)

// A `str[]` destination types its array literal: each element is read at
// `str`, so a string literal widens beside a view (#10889). With no
// destination the elements must still agree.
func TestStrArrayDestinationTypesTheLiteral(t *testing.T) {
	const pre = `function main(): i32 { let owned: string = "ab" + "cd"; let s: str = slice_unchecked(owned, 1, 3); `
	for _, lit := range []string{`["x", s]`, `[s, "x"]`, `[s, s]`, `["x", "y"]`} {
		if err := checkSource(t, pre+"let xs: str[] = "+lit+"; return xs.len(); }"); err != nil {
			t.Errorf("%s into str[]: %v", lit, err)
		}
	}
	for _, src := range []string{
		pre + `let xs: str[] = ["x", 5]; return xs.len(); }`,
		pre + `let xs = ["x", s]; return xs.len(); }`,
	} {
		if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "array element:") {
			t.Errorf("want E034 for %s, got %v", src, err)
		}
	}
}
