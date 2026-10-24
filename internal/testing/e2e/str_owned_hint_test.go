package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

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
		bad  string
		good string
	}{
		{
			"let init",
			`function main(): i32 { let s: string = "  x  "; let t: string = slice_unchecked(s, 0, 2); return t.len(); }`,
			`function main(): i32 { let s: string = "  x  "; let t: string = slice_unchecked(s, 0, 2).to_owned(); return t.len(); }`,
		},
		{
			"assignment",
			`function main(): i32 { let s: string = "  x  "; let t: string = "y"; t = slice_unchecked(s, 0, 2); return t.len(); }`,
			`function main(): i32 { let s: string = "  x  "; let t: string = "y"; t = slice_unchecked(s, 0, 2).to_owned(); return t.len(); }`,
		},
		{
			"return",
			`function f(s: string): string { return slice_unchecked(s, 0, 2); }
function main(): i32 { return f("  x  ").len(); }`,
			`function f(s: string): string { return slice_unchecked(s, 0, 2).to_owned(); }
function main(): i32 { return f("  x  ").len(); }`,
		},
		{
			"struct field",
			`function main(): i32 { let s: string = "  x  "; let b: Box = Box { name: slice_unchecked(s, 0, 2) }; return b.name.len(); }`,
			`function main(): i32 { let s: string = "  x  "; let b: Box = Box { name: slice_unchecked(s, 0, 2).to_owned() }; return b.name.len(); }`,
		},
		{
			"own parameter",
			`function main(): i32 { let s: string = "  x  "; return consume(slice_unchecked(s, 0, 2)); }`,
			`function main(): i32 { let s: string = "  x  "; return consume(slice_unchecked(s, 0, 2).to_owned()); }`,
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
		if err := checkStrHintSource(t, c.good); err != nil {
			t.Errorf("%s: taking the hint still fails:\n%s", c.name, err)
		}
	}
}

func TestStrToOwnedHintDoesNotLeakIntoOtherMismatches(t *testing.T) {
	if err := checkStrHintSource(t, `function main(): i32 { let s: string = "abc"; let v: str = s; return v.len(); }`); err != nil {
		t.Errorf("borrowing a string into a str should be legal: %s", err)
	}
	err := checkStrHintSource(t, `function main(): i32 { let n: i32 = "abc"; return n; }`)
	if err == nil {
		t.Fatal("expected a type error")
	}
	if strings.Contains(err.Error(), "to_owned") {
		t.Errorf("an unrelated mismatch picked up the str hint:\n%s", err)
	}
}
