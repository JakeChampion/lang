package printer

import (
	"strings"
	"testing"
)

// A named argument keeps its `name = ` prefix through formatting. Args holds
// them in SOURCE order with the names alongside in ArgNames, so dropping the
// names rewrote `mk(b = 1, a = 9)` as `mk(1, 9)` — binding each value to the
// other parameter. `-fmt` silently changed what the program computed: the
// original returned 8, the formatted one -8.
func TestFormatKeepsNamedArguments(t *testing.T) {
	got := formatSrc(t, `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return mk(b = 1, a = 9); }
`)
	if !strings.Contains(got, "mk(b = 1, a = 9)") {
		t.Errorf("named arguments lost their names or their order:\n%s", got)
	}
}

// The same on a pipe call, whose trailing arguments run through their own
// emission path.
func TestFormatKeepsNamedArgumentsThroughPipe(t *testing.T) {
	got := formatSrc(t, `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 9 |> mk(b = 1); }
`)
	if !strings.Contains(got, "mk(b = 1)") {
		t.Errorf("named argument lost through a pipe call:\n%s", got)
	}
}
