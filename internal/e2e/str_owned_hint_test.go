package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

// A borrowed `str` never silently promotes to an owned `string` — that rule is
// deliberate. What was missing is the way out: the diagnostics restated the two
// type names and stopped, leaving the most ordinary string expression in the
// language (`var t: string = s.trim();`) with nowhere to go. `.to_owned()` is
// the materialiser, named in the checker's own comments and used throughout the
// stdlib, and now named in the message.
//
// The second half matters more than the first: a hint you cannot follow is
// worse than no hint. Every site that offers `.to_owned()` is re-checked here
// with the advice taken, and must compile.

const strHintPrelude = `import "std/string";
struct Box { name: string }
function consume(own s: string): i32 { return s.len(); }
`

func checkStrHintSource(t *testing.T, body string) error {
	t.Helper()
	prog, _, err := modload.LoadSource(strHintPrelude + body)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, cerr := checker.Check(prog)
	return cerr
}

func TestStrToOwnedHintAtEveryOwningSink(t *testing.T) {
	cases := []struct {
		name string
		bad  string // rejected: a `str` reaching an owning sink
		good string // the same program with the hint taken
	}{
		{
			"var init",
			`function main(): i32 { var s: string = "  x  "; var t: string = s.trim(); return t.len(); }`,
			`function main(): i32 { var s: string = "  x  "; var t: string = s.trim().to_owned(); return t.len(); }`,
		},
		{
			"assignment",
			`function main(): i32 { var s: string = "  x  "; var t: string = "y"; t = s.trim(); return t.len(); }`,
			`function main(): i32 { var s: string = "  x  "; var t: string = "y"; t = s.trim().to_owned(); return t.len(); }`,
		},
		{
			"return",
			`function f(s: string): string { return s.trim(); }
function main(): i32 { return f("  x  ").len(); }`,
			`function f(s: string): string { return s.trim().to_owned(); }
function main(): i32 { return f("  x  ").len(); }`,
		},
		{
			"struct field",
			`function main(): i32 { var s: string = "  x  "; var b: Box = Box { name: s.trim() }; return b.name.len(); }`,
			`function main(): i32 { var s: string = "  x  "; var b: Box = Box { name: s.trim().to_owned() }; return b.name.len(); }`,
		},
		{
			"own parameter",
			`function main(): i32 { var s: string = "  x  "; return consume(s.trim()); }`,
			`function main(): i32 { var s: string = "  x  "; return consume(s.trim().to_owned()); }`,
		},
	}

	const want = "`.to_owned()`"
	for _, c := range cases {
		err := checkStrHintSource(t, c.bad)
		if err == nil {
			t.Errorf("%s: expected a rejection of the `str`, got none", c.name)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: diagnostic does not name the remedy:\n%s", c.name, err)
		}
		// Taking the advice must actually compile. An `own` parameter is the
		// case this used to fail: clearing the type error left an E051 behind,
		// so the reader followed the hint into a second refusal.
		if err := checkStrHintSource(t, c.good); err != nil {
			t.Errorf("%s: taking the hint still fails:\n%s", c.name, err)
		}
	}
}

// The hint is scoped to the pair it describes. A `string` flowing INTO a `str`
// is legal, and an unrelated mismatch must not pick up string-view advice.
func TestStrToOwnedHintDoesNotLeakIntoOtherMismatches(t *testing.T) {
	if err := checkStrHintSource(t, `function main(): i32 { var s: string = "abc"; var v: str = s; return v.len(); }`); err != nil {
		t.Errorf("borrowing a string into a str should be legal: %s", err)
	}
	err := checkStrHintSource(t, `function main(): i32 { var n: i32 = "abc"; return n; }`)
	if err == nil {
		t.Fatal("expected a type error")
	}
	if strings.Contains(err.Error(), "to_owned") {
		t.Errorf("an unrelated mismatch picked up the str hint:\n%s", err)
	}
}
