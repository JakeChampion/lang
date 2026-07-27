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

// The normalization data is checked in rather than derived from the Go
// standard library, which ships neither decompositions nor combining
// classes. That makes its INVARIANTS the thing worth asserting: a
// corrupted or hand-edited normdata.txt has to fail loudly here rather
// than produce a subtly wrong normalizer.
func TestNormDataWellFormed(t *testing.T) {
	cccs, decomps, prims := parseNormData()
	if len(cccs) == 0 || len(decomps) == 0 || len(prims) == 0 {
		t.Fatalf("empty normalization tables: %d ccc, %d decomp, %d primary", len(cccs), len(decomps), len(prims))
	}
	for i, r := range cccs {
		if r.lo > r.hi {
			t.Errorf("ccc range %d inverted: U+%04X..U+%04X", i, r.lo, r.hi)
		}
		if r.ccc <= 0 || r.ccc > 254 {
			t.Errorf("ccc range %d has class %d, want 1..254", i, r.ccc)
		}
		if i > 0 && cccs[i-1].hi >= r.lo {
			t.Errorf("ccc ranges %d and %d overlap or are unsorted", i-1, i)
		}
	}
	for i, d := range decomps {
		if i > 0 && decomps[i-1].cp >= d.cp {
			t.Errorf("decomposition table not sorted at %d (U+%04X)", i, d.cp)
		}
		if d.to[0] == 0 {
			t.Errorf("U+%04X has an empty decomposition", d.cp)
		}
		// A 0 terminates the record, so no code point may follow one.
		seenZero := false
		for _, r := range d.to {
			if r == 0 {
				seenZero = true
			} else if seenZero {
				t.Errorf("U+%04X has a code point after a terminating 0: %v", d.cp, d.to)
				break
			}
		}
		if d.cp >= hangulSBase && d.cp < hangulSBase+hangulSCount {
			t.Errorf("Hangul U+%04X must not be in the table; it decomposes arithmetically", d.cp)
		}
	}
	for i, p := range prims {
		if i > 0 && (prims[i-1].a > p.a || (prims[i-1].a == p.a && prims[i-1].b >= p.b)) {
			t.Errorf("composition table not sorted by pair at %d", i)
		}
	}
}

// Composition is not the inverse of the stored decomposition: the table
// above holds the FULL expansion while composition recombines one step
// at a time. This pins the relationship that does hold — every primary
// composite decomposes to something starting with the pair's own first
// element — so a future change cannot quietly conflate the two.
func TestPrimaryCompositesDecompose(t *testing.T) {
	_, decomps, prims := parseNormData()
	byCP := make(map[rune]decomp, len(decomps))
	for _, d := range decomps {
		byCP[d.cp] = d
	}
	for _, p := range prims {
		d, ok := byCP[p.cp]
		if !ok {
			t.Errorf("primary composite U+%04X has no decomposition", p.cp)
			continue
		}
		// The full expansion begins with the expansion of the pair's
		// first element, which for a non-decomposing starter is itself.
		first := p.a
		if fd, ok := byCP[p.a]; ok {
			first = fd.to[0]
		}
		if d.to[0] != first {
			t.Errorf("U+%04X: expansion starts U+%04X, want U+%04X from pair head U+%04X",
				p.cp, d.to[0], first, p.a)
		}
	}
}

// Every emitted table has to decode back to what went in. generate()
// already calls verifyNorm and panics on a mismatch; running it here
// turns that into a named failure instead of a stack trace, and covers
// the decoders the generated Fern mirrors.
func TestNormTablesRoundTrip(t *testing.T) {
	cccs, decomps, prims := parseNormData()
	cccT, decompT, composeT := encodeCCC(cccs), encodeDecomp(decomps), encodeCompose(prims)
	verifyNorm(cccT, decompT, composeT, cccs, decomps, prims)

	// Absent keys must read as absent, not as a neighbouring entry.
	if got := decodeCCC(cccT, 'a'); got != 0 {
		t.Errorf("ccc('a') = %d, want 0", got)
	}
	if _, ok := decodeDecomp(decompT, 'a'); ok {
		t.Errorf("'a' must not decompose")
	}
	if got := decodeCompose(composeT, 'a', 'b'); got != 0 {
		t.Errorf("compose('a','b') = U+%04X, want none", got)
	}
	// e + COMBINING ACUTE composes to e-acute; the reverse pair does not.
	if got := decodeCompose(composeT, 'e', 0x0301); got != 0x00E9 {
		t.Errorf("compose(e, U+0301) = U+%04X, want U+00E9", got)
	}
	if got := decodeCompose(composeT, 0x0301, 'e'); got != 0 {
		t.Errorf("compose(U+0301, e) = U+%04X, want none", got)
	}
}

// Golden decompositions, including the shapes most likely to be got
// wrong: a singleton that maps to a different character, a multi-step
// expansion, and a four-code-point one.
func TestNormKnownAnswers(t *testing.T) {
	_, decomps, prims := parseNormData()
	byCP := make(map[rune]decomp, len(decomps))
	for _, d := range decomps {
		byCP[d.cp] = d
	}
	recomposes := make(map[rune]bool, len(prims))
	for _, p := range prims {
		recomposes[p.cp] = true
	}
	for _, tc := range []struct {
		cp   rune
		want []rune
	}{
		{0x00E9, []rune{'e', 0x0301}},                    // e-acute
		{0x212B, []rune{'A', 0x030A}},                    // ANGSTROM SIGN, a singleton
		{0x2126, []rune{0x03A9}},                         // OHM SIGN -> Greek omega
		{0x1E69, []rune{'s', 0x0323, 0x0307}},            // multi-step, three out
		{0x1F82, []rune{0x03B1, 0x0313, 0x0300, 0x0345}}, // the four-code-point case
		{0x0958, []rune{0x0915, 0x093C}},                 // DEVANAGARI KA WITH NUKTA
	} {
		d, ok := byCP[tc.cp]
		if !ok {
			t.Errorf("U+%04X has no decomposition", tc.cp)
			continue
		}
		var got []rune
		for _, r := range d.to {
			if r == 0 {
				break
			}
			got = append(got, r)
		}
		if len(got) != len(tc.want) {
			t.Errorf("U+%04X: decomposition %v, want %v", tc.cp, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("U+%04X: decomposition %v, want %v", tc.cp, got, tc.want)
				break
			}
		}
	}
	// Composition exclusions: these decompose but must never be rebuilt.
	// U+0958 is the script-exclusion case, the singletons the other kind.
	for _, cp := range []rune{0x0958, 0x212B, 0x2126, 0x0340} {
		if recomposes[cp] {
			t.Errorf("U+%04X is composition-excluded and must not be a primary composite", cp)
		}
	}
	// ... while an ordinary precomposed character must be.
	for _, cp := range []rune{0x00E9, 0x1E69} {
		if !recomposes[cp] {
			t.Errorf("U+%04X should recompose under NFC", cp)
		}
	}
}

// The Hangul constants drive arithmetic that replaces 11172 table
// entries, so an off-by-one in any of them silently corrupts a whole
// script. Check the formula against known syllables at both ends and
// across the with/without-trailing-jamo split.
func TestHangulConstants(t *testing.T) {
	decompose := func(s rune) []rune {
		si := int(s - hangulSBase)
		l := rune(hangulLBase + si/hangulNCount)
		v := rune(hangulVBase + (si%hangulNCount)/hangulTCount)
		out := []rune{l, v}
		if tj := si % hangulTCount; tj != 0 {
			out = append(out, rune(hangulTBase+tj))
		}
		return out
	}
	for _, tc := range []struct {
		s    rune
		want []rune
	}{
		{0xAC00, []rune{0x1100, 0x1161}},         // first syllable, no trailing jamo
		{0xAC01, []rune{0x1100, 0x1161, 0x11A8}}, // ... and with one
		{0xD7A3, []rune{0x1112, 0x1175, 0x11C2}}, // last syllable
	} {
		got := decompose(tc.s)
		if len(got) != len(tc.want) {
			t.Fatalf("U+%04X: %v, want %v", tc.s, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("U+%04X: %v, want %v", tc.s, got, tc.want)
			}
		}
	}
	if hangulSBase+hangulSCount != 0xD7A4 {
		t.Errorf("Hangul block ends at U+%04X, want U+D7A4", hangulSBase+hangulSCount)
	}
	if hangulNCount != 21*hangulTCount {
		t.Errorf("NCount %d must equal VCount*TCount", hangulNCount)
	}
}
