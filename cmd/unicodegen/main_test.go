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

// The full-case tables are the one dataset here with no build-time oracle
// (Go's unicode package has simple mappings only), so they get the
// scrutiny the others get for free from `verify`.
//
// These are the cases the feature exists for. `ß` -> `SS` is the example
// #5552 was filed about; the rest cover each expansion length and each
// script family in the table.
func TestFullCaseKnownAnswers(t *testing.T) {
	for _, tc := range []struct {
		from rune
		want string
		up   bool
	}{
		{'ß', "SS", true},  // 1 -> 2, the headline case
		{'ﬁ', "FI", true},  // Latin ligature
		{'ﬄ', "FFL", true}, // 1 -> 3
		{'ŉ', "ʼN", true},  // modifier letter + N
		{'ǰ', "J̌", true},  // letter + combining caron
		{'ΐ', "Ϊ́", true}, // Greek, 1 -> 3
		{'և', "ԵՒ", true},  // Armenian ligature
		{'ᾀ', "ἈΙ", true},  // Greek iota subscript
		{'İ', "i̇", false}, // the only full LOWERcase entry
	} {
		got, ok := lookupFull(tc.from, tc.up)
		if !ok {
			t.Errorf("U+%04X: no full mapping, want %q", tc.from, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("U+%04X: got %q, want %q", tc.from, got, tc.want)
		}
	}
}

// Structural invariants the binary search in the generated Fern relies
// on, plus the ones that would make an entry pointless or unencodable.
func TestFullCaseTablesWellFormed(t *testing.T) {
	for _, tab := range []struct {
		name string
		fs   []fullCase
		up   bool
	}{{"fullUpper", fullUpper, true}, {"fullLower", fullLower, false}} {
		var prev rune = -1
		for _, f := range tab.fs {
			if f.from <= prev {
				t.Errorf("%s: U+%04X out of order (previous U+%04X) — the "+
					"generated lookup binary-searches, so order is load-bearing",
					tab.name, f.from, prev)
			}
			prev = f.from
			if f.to[0] == 0 {
				t.Errorf("%s: U+%04X has an empty expansion", tab.name, f.from)
			}
			// A zero is the "absent" marker, so it may only trail.
			if f.to[1] == 0 && f.to[2] != 0 {
				t.Errorf("%s: U+%04X has a hole in its expansion: %v", tab.name, f.from, f.to)
			}
			// An entry that expands to exactly the simple mapping would be
			// dead weight — the simple table already says it.
			simple := unicode.ToLower(f.from)
			if tab.up {
				simple = unicode.ToUpper(f.from)
			}
			if f.to[1] == 0 && f.to[0] == simple {
				t.Errorf("%s: U+%04X duplicates the simple mapping", tab.name, f.from)
			}
		}
	}
}

// lookupFull mirrors the generated `_full_case`, decoding the emitted
// table rather than reading the Go slice — so a bug in encodeFull or in
// the record layout shows up here rather than only at runtime.
func lookupFull(cp rune, up bool) (string, bool) {
	t := encodeFull(fullLower)
	if up {
		t = encodeFull(fullUpper)
	}
	lo, hi := 0, len(t)/fullChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * fullChars
		from := decField(t, base)
		switch {
		case int(cp) < from:
			hi = mid - 1
		case int(cp) > from:
			lo = mid + 1
		default:
			out := string(rune(decField(t, base+fieldChars)))
			for _, off := range []int{2 * fieldChars, 3 * fieldChars} {
				if c := decField(t, base+off); c != 0 {
					out += string(rune(c))
				}
			}
			return out, true
		}
	}
	return "", false
}

// Case folding is a third operation, distinct from both upper and lower.
// The table stores only the code points where the fold differs from simple
// lowercase; everything else falls through. So these check the fold
// RESULT (table plus fallback), which is what callers see, and then
// separately check that the entries which cannot come from lowercase are
// actually stored.
func TestFoldKnownAnswers(t *testing.T) {
	for _, tc := range []struct {
		from rune
		want string
	}{
		// Written as escapes, not literals: several are visually identical
		// to an ASCII letter.
		{'\u00DF', "ss"},     // sharp s - lower leaves it alone, fold expands
		{'\u017F', "s"},      // LATIN SMALL LETTER LONG S
		{'\u00B5', "\u03BC"}, // MICRO SIGN folds to Greek mu - changes script
		{'\uFB01', "fi"},     // fi ligature, 1 -> 2
		{'\uFB04', "ffl"},    // ffl ligature, 1 -> 3
		// These reach the same answer through the LOWERCASE fallback rather
		// than the table, which is the half a membership test would miss.
		{'\u212A', "k"}, // KELVIN SIGN, NOT ASCII K
		{'A', "a"},
		{'\u0391', "\u03B1"}, // Greek capital alpha
	} {
		if got := foldCP(tc.from); got != tc.want {
			t.Errorf("U+%04X: fold = %q, want %q", tc.from, got, tc.want)
		}
	}
	// The expansions genuinely require a table entry — simple lowercase
	// cannot produce a multi-code-point result.
	for _, cp := range []rune{'\u00DF', '\u017F', '\u00B5', '\uFB01', '\uFB04'} {
		if _, ok := lookupFold(cp); !ok {
			t.Errorf("U+%04X must be a fold-table entry; lowercase cannot produce it", cp)
		}
	}
}

// foldCP mirrors the generated `_fold_cp`: table first, simple lowercase
// as the fallback.
func foldCP(cp rune) string {
	if s, ok := lookupFold(cp); ok {
		return s
	}
	return string(unicode.ToLower(cp))
}

// Every stored entry must actually differ from the simple lowercase it
// would otherwise fall through to; an entry that agrees is dead weight,
// and a table full of them would hide a generation bug.
func TestFoldExceptionsAllDiffer(t *testing.T) {
	var prev rune = -1
	for _, f := range foldExceptions {
		if f.from <= prev {
			t.Errorf("fold table: U+%04X out of order after U+%04X", f.from, prev)
		}
		prev = f.from
		if f.to[1] == 0 && f.to[0] == unicode.ToLower(f.from) {
			t.Errorf("fold table: U+%04X folds to its simple lowercase and "+
				"should not be stored", f.from)
		}
	}
}

// lookupFold mirrors the generated `_fold_cp` lookup over the emitted
// table, minus the lowercase fallback.
func lookupFold(cp rune) (string, bool) {
	t := encodeFull(foldExceptions)
	lo, hi := 0, len(t)/fullChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * fullChars
		from := decField(t, base)
		switch {
		case int(cp) < from:
			hi = mid - 1
		case int(cp) > from:
			lo = mid + 1
		default:
			out := string(rune(decField(t, base+fieldChars)))
			for _, off := range []int{2 * fieldChars, 3 * fieldChars} {
				if c := decField(t, base+off); c != 0 {
					out += string(rune(c))
				}
			}
			return out, true
		}
	}
	return "", false
}
