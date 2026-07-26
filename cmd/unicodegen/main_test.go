package main

import (
	"os"
	"strings"
	"testing"
	"unicode"
)

// The encoded tables ship as Fern STRING literals, so the alphabet has
// to survive the lexer with no escapes: every character printable ASCII,
// and never `"` (34, ends the literal) or `\` (92, starts an escape). A
// `\xNN` escape for a byte >= 0x80 would also make the literal invalid
// UTF-8, so the alphabet must stay below 0x80.
func TestAlphabetNeedsNoEscapes(t *testing.T) {
	seen := map[byte]bool{}
	for d := 0; d < 64; d++ {
		c := enc6(d)
		switch {
		case c < 33 || c > 126:
			t.Fatalf("enc6(%d) = %d, outside printable ASCII", d, c)
		case c == '"' || c == '\\':
			t.Fatalf("enc6(%d) = %q, needs an escape in a Fern string literal", d, c)
		case seen[c]:
			t.Fatalf("enc6(%d) = %q, already emitted for another value", d, c)
		}
		seen[c] = true
		if got := dec6(c); got != d {
			t.Fatalf("dec6(enc6(%d)) = %d, want round trip", d, got)
		}
	}
}

// Fields are the unit the Fern decoder reads; a rounding error here
// would corrupt every table silently.
func TestFieldRoundTrip(t *testing.T) {
	for _, v := range []int{0, 1, 63, 64, 4095, 4096, 0x10FFFF, bias, bias - 1, bias + 1, 1<<24 - 1} {
		var b strings.Builder
		encField(&b, v)
		if got := b.Len(); got != fieldChars {
			t.Fatalf("encField(%d) wrote %d chars, want %d", v, got, fieldChars)
		}
		if got := decField(b.String(), 0); got != v {
			t.Fatalf("decField(encField(%d)) = %d", v, got)
		}
	}
}

// The generator's own `verify` panics on a mismatch, but it only runs
// when someone regenerates. This runs the same check on every `go test`,
// so a bad edit to the run derivation or the decoder is caught by CI
// rather than by a wrong answer at runtime.
func TestTablesMatchGoUnicode(t *testing.T) {
	caseT, _, classes := tables() // panics on mismatch
	if len(caseT)%recChars != 0 {
		t.Fatalf("case table is %d chars, not a multiple of %d", len(caseT), recChars)
	}
	for name, c := range classes {
		if len(c.table)%rangeChars != 0 {
			t.Fatalf("%s table is %d chars, not a multiple of %d", name, len(c.table), rangeChars)
		}
	}
}

// Binary search only works on a sorted, disjoint table. The derivation
// walks code points in order so this should hold by construction —
// which is exactly the kind of assumption worth pinning.
func TestCaseRunsSortedAndDisjoint(t *testing.T) {
	runs := caseRuns()
	if len(runs) == 0 {
		t.Fatal("no case runs derived")
	}
	prev := rune(-1)
	for _, r := range runs {
		if r.lo > r.hi {
			t.Fatalf("run U+%04X..U+%04X is inverted", r.lo, r.hi)
		}
		if r.lo <= prev {
			t.Fatalf("run at U+%04X overlaps or follows U+%04X out of order", r.lo, prev)
		}
		if r.kind == 0 && r.du == 0 && r.dl == 0 {
			t.Fatalf("run U+%04X..U+%04X maps nothing and should have been omitted", r.lo, r.hi)
		}
		prev = r.hi
	}
}

// Deterministic output is what makes "re-running the generator produces
// no diff" a usable staleness check.
func TestGenerateIsDeterministic(t *testing.T) {
	a, sa := generate()
	b, sb := generate()
	if a != b {
		t.Fatal("two runs produced different output")
	}
	if sa != sb {
		t.Fatalf("stats differ: %+v vs %+v", sa, sb)
	}
}

// The whole point of #5627 is that a table is static data, not code
// that rebuilds an array on every call. A Fern array literal is
// executable code, so an `i32[]`-returning table would silently
// reintroduce the 176 KB / 22x regression — pin the representation
// rather than trusting a benchmark not to drift.
func TestTablesAreStringsNotArrays(t *testing.T) {
	src, _ := generate()
	if strings.Contains(src, "i32[]") {
		t.Error("generated module declares an i32[]; tables must be string literals")
	}
	for _, name := range []string{"_case_table", "_letter_ranges", "_digit_ranges",
		"_space_ranges", "_upper_ranges", "_lower_ranges"} {
		decl := "function " + name + "(): string {\n    return \""
		if !strings.Contains(src, decl) {
			t.Errorf("%s is not a string-literal-returning table", name)
		}
	}
}

// The committed internal/stdlib/std/unicode.fern must be what the
// generator currently produces — it is generated code, and a hand-edit
// or a stale regeneration would be invisible otherwise.
func TestCommittedFileIsUpToDate(t *testing.T) {
	const path = "../../internal/stdlib/std/unicode.fern"
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	got, _ := generate()
	if string(want) == got {
		return
	}
	// A Go toolchain upgrade can move the Unicode version under us; say
	// so explicitly, because "the file is stale" and "the data changed"
	// have the same fix but very different causes.
	t.Fatalf("%s is out of date (generator targets Unicode %s).\n"+
		"Re-run: go run ./cmd/unicodegen %s", path, unicode.Version, path)
}
