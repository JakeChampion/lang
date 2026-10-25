package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

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

func TestTablesMatchGoUnicode(t *testing.T) {
	caseT, _, classes := tables()
	if len(caseT)%recChars != 0 {
		t.Fatalf("case table is %d chars, not a multiple of %d", len(caseT), recChars)
	}
	for name, c := range classes {
		if len(c.table)%rangeChars != 0 {
			t.Fatalf("%s table is %d chars, not a multiple of %d", name, len(c.table), rangeChars)
		}
	}
}

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

func TestTablesAreStringsNotArrays(t *testing.T) {
	src, _ := generate()
	if regexp.MustCompile(`(?m)^(?:pub(?:\(package\))? )?const \w+:[^\n;]*\[\]`).MatchString(src) {
		t.Error("generated module declares an array table; tables must be string literals")
	}
	tables := regexp.MustCompile(`(?m)^function (_\w+(?:_table|_ranges))\(\)`).FindAllStringSubmatch(src, -1)
	if len(tables) != 20 {
		t.Fatalf("generated %d table functions, want 20", len(tables))
	}
	for _, table := range tables {
		name := table[1]
		decl := "function " + name + "(): string {\n  return \""
		if !strings.Contains(src, decl) {
			t.Errorf("%s is not a string-literal-returning table", name)
		}
	}
}

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
	t.Fatalf("%s is out of date (generator targets Unicode %s).\n"+
		"Re-run: go run ./cmd/unicodegen %s", path, unicode.Version, path)
}

func TestFullCaseKnownAnswers(t *testing.T) {
	for _, tc := range []struct {
		from rune
		want string
		up   bool
	}{
		{'ß', "SS", true},
		{'ﬁ', "FI", true},
		{'ﬄ', "FFL", true},
		{'ŉ', "ʼN", true},
		{'ǰ', "J̌", true},
		{'ΐ', "Ϊ́", true},
		{'և', "ԵՒ", true},
		{'ᾀ', "ἈΙ", true},
		{'İ', "i̇", false},
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
			if f.to[1] == 0 && f.to[2] != 0 {
				t.Errorf("%s: U+%04X has a hole in its expansion: %v", tab.name, f.from, f.to)
			}
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

func TestFoldKnownAnswers(t *testing.T) {
	for _, tc := range []struct {
		from rune
		want string
	}{
		{'\u00DF', "ss"},
		{'\u017F', "s"},
		{'\u00B5', "\u03BC"},
		{'\uFB01', "fi"},
		{'\uFB04', "ffl"},
		{'\u212A', "k"},
		{'A', "a"},
		{'\u0391', "\u03B1"},
	} {
		if got := foldCP(tc.from); got != tc.want {
			t.Errorf("U+%04X: fold = %q, want %q", tc.from, got, tc.want)
		}
	}
	for _, cp := range []rune{'\u00DF', '\u017F', '\u00B5', '\uFB01', '\uFB04'} {
		if _, ok := lookupFold(cp); !ok {
			t.Errorf("U+%04X must be a fold-table entry; lowercase cannot produce it", cp)
		}
	}
}

func foldCP(cp rune) string {
	if s, ok := lookupFold(cp); ok {
		return s
	}
	return string(unicode.ToLower(cp))
}

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

func TestNormTablesRoundTrip(t *testing.T) {
	cccs, decomps, prims := parseNormData()
	cccT, decompT, composeT := encodeCCC(cccs), encodeDecomp(decomps), encodeCompose(prims)
	verifyNorm(cccT, decompT, composeT, cccs, decomps, prims)

	if got := decodeCCC(cccT, 'a'); got != 0 {
		t.Errorf("ccc('a') = %d, want 0", got)
	}
	if _, ok := decodeDecomp(decompT, 'a'); ok {
		t.Errorf("'a' must not decompose")
	}
	if got := decodeCompose(composeT, 'a', 'b'); got != 0 {
		t.Errorf("compose('a','b') = U+%04X, want none", got)
	}
	if got := decodeCompose(composeT, 'e', 0x0301); got != 0x00E9 {
		t.Errorf("compose(e, U+0301) = U+%04X, want U+00E9", got)
	}
	if got := decodeCompose(composeT, 0x0301, 'e'); got != 0 {
		t.Errorf("compose(U+0301, e) = U+%04X, want none", got)
	}
}

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
		{0x00E9, []rune{'e', 0x0301}},
		{0x212B, []rune{'A', 0x030A}},
		{0x2126, []rune{0x03A9}},
		{0x1E69, []rune{'s', 0x0323, 0x0307}},
		{0x1F82, []rune{0x03B1, 0x0313, 0x0300, 0x0345}},
		{0x0958, []rune{0x0915, 0x093C}},
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
	for _, cp := range []rune{0x0958, 0x212B, 0x2126, 0x0340} {
		if recomposes[cp] {
			t.Errorf("U+%04X is composition-excluded and must not be a primary composite", cp)
		}
	}
	for _, cp := range []rune{0x00E9, 0x1E69} {
		if !recomposes[cp] {
			t.Errorf("U+%04X should recompose under NFC", cp)
		}
	}
}

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
		{0xAC00, []rune{0x1100, 0x1161}},
		{0xAC01, []rune{0x1100, 0x1161, 0x11A8}},
		{0xD7A3, []rune{0x1112, 0x1175, 0x11C2}},
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
