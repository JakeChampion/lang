// Command unicodegen generates internal/stdlib/std/unicode.fern — the
// Unicode simple (1:1) case-mapping table, the character-class range
// tables, and the API built on them. The data comes from the Go standard
// library's `unicode` package (unicode.ToUpper / unicode.ToLower and the
// category predicates), so the tables' Unicode version tracks the Go
// toolchain used to run this generator.
//
// Usage:
//
//	go run ./cmd/unicodegen internal/stdlib/std/unicode.fern
//
// Re-run after a Go toolchain upgrade to refresh the tables; the output
// is deterministic (sorted by code point), so an unchanged Unicode
// version reproduces the file byte-for-byte.
//
// # Why the tables are strings
//
// The tables were once emitted as `function _upper_keys(): i32[] { return
// [97, 98, …] }` — 2900 array-literal stores of *code*, materialised on
// every single call. That cost 176 KB of binary and ran 22x slower than
// the ASCII byte fold on ASCII input (#5627). A string literal is static
// rodata: it costs its own length, once, and indexing it allocates
// nothing. So every table here is a string in a fixed-width, 6-bit
// character encoding, binary-searched in place.
//
// The alphabet is 64 printable ASCII characters in two contiguous spans,
// 48..91 ('0'..'[') and 93..112 (']'..'p'), chosen to skip `\` (92) so no
// emitted literal ever needs an escape — `"` (34) is below the range, and
// a `\xNN` escape for a byte >= 0x80 would make the literal invalid
// UTF-8. Decoding is two comparisons (see `_dig` in the generated file).
//
// A field is 4 characters = 24 bits. Deltas are stored biased by 2^23 so
// they can be negative.
//
// # Record layouts
//
// Case table — 5 fields (20 chars) per entry, sorted by lo, disjoint:
//
//	lo | hi | kind | dUpper | dLower
//
//	kind 0: constant deltas. ToUpper(cp) = cp + dUpper, likewise lower.
//	kind 1: alternating pair run — even offset from lo is UPPERCASE, odd
//	        is its lowercase (the Latin-Extended-A shape: Ā ā Ă ă …).
//
// Class tables — 2 fields (8 chars) per entry: lo | hi, inclusive.
//
// Correctness does not rest on the run-derivation being clever: `verify`
// below decodes the emitted tables for every code point in 0..MaxRune and
// compares against the `unicode` package, failing the build on any
// mismatch.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// bias is added to a stored delta so negative deltas fit an unsigned
// 24-bit field.
const bias = 1 << 23

// fieldChars is the width of one encoded field, and recChars / rangeChars
// the width of one case-table / class-table record. The generated Fern
// decoder hard-codes the same numbers.
const (
	fieldChars = 4
	recChars   = 5 * fieldChars
	rangeChars = 2 * fieldChars
)

// enc6 maps a 6-bit value to its alphabet character: 0..43 to '0'..'[',
// 44..63 to ']'..'p'. The gap skips '\'.
func enc6(d int) byte {
	if d < 44 {
		return byte(48 + d)
	}
	return byte(93 + d - 44)
}

// dec6 is enc6's inverse, mirroring the generated `_dig`.
func dec6(c byte) int {
	if c <= 91 {
		return int(c) - 48
	}
	return int(c) - 49
}

// encField appends v as one 4-character big-endian base-64 field.
func encField(b *strings.Builder, v int) {
	if v < 0 || v >= 1<<24 {
		panic(fmt.Sprintf("unicodegen: field %d out of 24-bit range", v))
	}
	b.WriteByte(enc6((v >> 18) & 63))
	b.WriteByte(enc6((v >> 12) & 63))
	b.WriteByte(enc6((v >> 6) & 63))
	b.WriteByte(enc6(v & 63))
}

// decField reads the field at character offset i, mirroring `_fld`.
func decField(t string, i int) int {
	return dec6(t[i])<<18 | dec6(t[i+1])<<12 | dec6(t[i+2])<<6 | dec6(t[i+3])
}

// run is one case-table entry: an inclusive code-point range whose
// upper/lower mappings share a single rule.
type run struct {
	lo, hi rune
	kind   int
	du, dl int // kind 0 only
}

func deltaUpper(r rune) int { return int(unicode.ToUpper(r)) - int(r) }
func deltaLower(r rune) int { return int(unicode.ToLower(r)) - int(r) }

// startsAlt reports whether an alternating run could start at r: r is an
// uppercase letter whose lowercase is the very next code point (the
// Latin-Extended-A shape, Ā ā Ă ă …).
//
// A block starting on the *lowercase* half would need a second pattern.
// No such block exists in Unicode 15.0, so encoding one is not
// implemented — those code points fall back to constant runs, which are
// always correct, just less compact. If a future Unicode version
// introduces one the tables stay correct automatically; only the size
// regresses.
func startsAlt(r rune) bool {
	return deltaUpper(r) == 0 && deltaLower(r) == 1
}

// altHolds reports whether cp continues the alternating run anchored at
// lo: even offsets are the uppercase half, odd offsets the lowercase.
func altHolds(lo, cp rune) bool {
	if (cp-lo)&1 == 0 {
		return deltaUpper(cp) == 0 && deltaLower(cp) == 1
	}
	return deltaUpper(cp) == -1 && deltaLower(cp) == 0
}

// minAltRun is the shortest alternating run worth encoding as one. Below
// this, constant runs encode the same code points in fewer bytes.
const minAltRun = 4

// caseRuns derives the minimal set of case-mapping runs by walking every
// code point and diffing ToUpper/ToLower — never by copying
// unicode.CaseRanges, so the output is correct regardless of that table's
// stride conventions. Code points with no mapping are omitted entirely;
// the decoder returns them unchanged.
func caseRuns() []run {
	var runs []run
	for r := rune(0); r <= unicode.MaxRune; {
		du, dl := deltaUpper(r), deltaLower(r)
		if du == 0 && dl == 0 {
			r++
			continue
		}
		if startsAlt(r) {
			hi := r
			for hi < unicode.MaxRune && altHolds(r, hi+1) {
				hi++
			}
			if hi-r+1 >= minAltRun {
				runs = append(runs, run{lo: r, hi: hi, kind: 1})
				r = hi + 1
				continue
			}
		}
		hi := r
		for hi < unicode.MaxRune && deltaUpper(hi+1) == du && deltaLower(hi+1) == dl {
			hi++
		}
		runs = append(runs, run{lo: r, hi: hi, du: du, dl: dl})
		r = hi + 1
	}
	return runs
}

// encodeCase renders the case runs as the table string.
func encodeCase(runs []run) string {
	var b strings.Builder
	for _, rn := range runs {
		encField(&b, int(rn.lo))
		encField(&b, int(rn.hi))
		encField(&b, rn.kind)
		encField(&b, rn.du+bias)
		encField(&b, rn.dl+bias)
	}
	return b.String()
}

// applyCase decodes `t` for cp exactly as the generated Fern
// `_case_apply` does — the reference the verifier checks against.
func applyCase(t string, cp int, wantUpper bool) int {
	lo, hi := 0, len(t)/recChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * recChars
		rlo := decField(t, base)
		if cp < rlo {
			hi = mid - 1
			continue
		}
		if cp > decField(t, base+fieldChars) {
			lo = mid + 1
			continue
		}
		if decField(t, base+2*fieldChars) == 0 {
			if wantUpper {
				return cp + decField(t, base+3*fieldChars) - bias
			}
			return cp + decField(t, base+4*fieldChars) - bias
		}
		odd := (cp - rlo) & 1
		if wantUpper {
			return cp - odd
		}
		return cp + 1 - odd
	}
	return cp
}

// coalesce enumerates every code point satisfying pred and merges
// contiguous runs into inclusive [lo, hi] ranges. Enumerating and
// coalescing (rather than copying the stdlib RangeTable) keeps the output
// correct regardless of stride and yields the minimal set of ranges for a
// binary search.
func coalesce(pred func(rune) bool) [][2]rune {
	var out [][2]rune
	inRun := false
	var lo, prev rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if pred(r) {
			if !inRun {
				inRun, lo = true, r
			}
			prev = r
		} else if inRun {
			out = append(out, [2]rune{lo, prev})
			inRun = false
		}
	}
	if inRun {
		out = append(out, [2]rune{lo, prev})
	}
	return out
}

func encodeRanges(rs [][2]rune) string {
	var b strings.Builder
	for _, r := range rs {
		encField(&b, int(r[0]))
		encField(&b, int(r[1]))
	}
	return b.String()
}

// inRanges mirrors the generated `_in_ranges`.
func inRanges(t string, cp int) bool {
	lo, hi := 0, len(t)/rangeChars-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * rangeChars
		switch {
		case cp < decField(t, base):
			hi = mid - 1
		case cp > decField(t, base+fieldChars):
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// verify decodes every emitted table for every code point and compares
// against the `unicode` package. Any mismatch is a generator bug and
// fails the run rather than shipping a wrong table.
func verify(caseT string, classes map[string]classTable) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got, want := applyCase(caseT, int(r), true), int(unicode.ToUpper(r)); got != want {
			panic(fmt.Sprintf("unicodegen: to_upper(U+%04X) = U+%04X, want U+%04X", r, got, want))
		}
		if got, want := applyCase(caseT, int(r), false), int(unicode.ToLower(r)); got != want {
			panic(fmt.Sprintf("unicodegen: to_lower(U+%04X) = U+%04X, want U+%04X", r, got, want))
		}
	}
	names := make([]string, 0, len(classes))
	for name := range classes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := classes[name]
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if got, want := inRanges(c.table, int(r)), c.pred(r); got != want {
				panic(fmt.Sprintf("unicodegen: %s(U+%04X) = %v, want %v", name, r, got, want))
			}
		}
	}
}

type classTable struct {
	table string
	pred  func(rune) bool
	count int
}

// emitTable writes a non-public `name(): string` returning the encoded
// table, with a comment recording its entry count and byte size.
func emitTable(b *strings.Builder, name, comment, table string, entries, width int) {
	fmt.Fprintf(b, "// %s\n// %d entries, %d bytes (%d chars per entry).\nfunction %s(): string {\n    return \"%s\";\n}\n\n",
		comment, entries, len(table), width, name, table)
}

// tables builds the case table and every class table, verified against
// the `unicode` package before it is returned.
func tables() (caseT string, runs []run, classes map[string]classTable) {
	runs = caseRuns()
	caseT = encodeCase(runs)

	classes = map[string]classTable{}
	addClass := func(name string, pred func(rune) bool) {
		rs := coalesce(pred)
		classes[name] = classTable{table: encodeRanges(rs), pred: pred, count: len(rs)}
	}
	addClass("letter", unicode.IsLetter)
	addClass("digit", func(r rune) bool { return unicode.Is(unicode.Nd, r) })
	addClass("space", unicode.IsSpace)
	addClass("upper", unicode.IsUpper)
	addClass("lower", unicode.IsLower)

	verify(caseT, classes)
	return caseT, runs, classes
}

// genStats is what main reports after a run; the tests ignore it.
type genStats struct{ runs, tableBytes int }

// generate renders the complete std/unicode.fern source. Split out of
// main so the tests can regenerate in memory and compare against the
// committed file.
func generate() (string, genStats) {
	caseT, runs, classes := tables()
	stats := genStats{runs: len(runs), tableBytes: len(caseT)}
	for _, c := range classes {
		stats.tableBytes += len(c.table)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `// std/unicode — Unicode simple case mapping + character classes.
//
// GENERATED by cmd/unicodegen from the Go standard library's unicode
// package (Unicode %s). Do NOT edit by hand — re-run
// `+"`go run ./cmd/unicodegen internal/stdlib/std/unicode.fern`"+` to
// refresh after a Go toolchain upgrade.
//
// The Unicode-aware complement to std/string's byte-wise, ASCII-only
// helpers. Two families:
//   - Case mapping: to_upper / to_lower / to_upper_char / to_lower_char
//     / eq_ignore_case — full-code-point SIMPLE (1:1) mapping (Latin,
//     Greek, Cyrillic, Armenian, fullwidth, …), decoding UTF-8 via
//     std/utf8 and re-encoding.
//   - Character classes: is_letter / is_digit / is_alnum / is_whitespace
//     / is_upper / is_lower over a code point, via range binary search.
//
// Scope + caveats:
//   - SIMPLE case mappings only (1:1). Multi-code-point expansions
//     (e.g. `+"`ß`"+` → `+"`SS`"+`) are left unchanged — matching Go's
//     unicode.ToUpper / ToLower. Full mapping is #5630.
//   - is_digit is the decimal-digit class (Unicode Nd); is_letter is the
//     full Letter category; is_whitespace matches unicode.IsSpace.
//   - Not locale-aware (no Turkish dotless-i tailoring).
//
// Representation: every table below is a STRING literal, not an array —
// static rodata, so a lookup allocates nothing and no table is ever
// "built". Fields are 4 characters of a 6-bit alphabet (24 bits each),
// over the two printable spans 48..91 and 93..112 so no character ever
// needs an escape. Deltas are biased by 2^23. See cmd/unicodegen for the
// record layouts and the generator-side round-trip verification.
//
// When const/static arrays land as a language feature (COMPTIME-BRIEF),
// this encoding should be DELETED in favour of them — it exists only
// because a Fern array literal is executable code.
//
// Every entry point ASCII-fast-paths before touching a table: the
// tables cover ASCII too, but the common case shouldn't pay a binary
// search for it.

import "std/utf8" as utf8;

`, unicode.Version)

	emitTable(&b, "_case_table",
		"Case-mapping runs: lo | hi | kind | dUpper+2^23 | dLower+2^23.\n// kind 0 = constant deltas; kind 1 = alternating pairs, even offset\n// from lo is the uppercase half.",
		caseT, len(runs), recChars)
	for _, name := range []string{"letter", "digit", "space", "upper", "lower"} {
		c := classes[name]
		emitTable(&b, "_"+name+"_ranges",
			fmt.Sprintf("Inclusive code-point ranges for the %s class: lo | hi.", name),
			c.table, c.count, rangeChars)
	}

	b.WriteString(`// _dig decodes one table character to its 6-bit value. The alphabet
// skips ` + "`\\`" + ` (92), so the two spans are 48..91 and 93..112.
function _dig(c: i32): i32 {
    if (c <= 91) { return c - 48; }
    return c - 49;
}

// _fld reads the 24-bit field at character offset ` + "`i`" + `.
function _fld(t: string, i: i32): i32 {
    return (_dig(t[i]) << 18) | (_dig(t[i + 1]) << 12) | (_dig(t[i + 2]) << 6) | _dig(t[i + 3]);
}

// _case_apply maps ` + "`cp`" + ` through the case table — uppercase when
// ` + "`want_upper`" + `, lowercase otherwise — returning ` + "`cp`" + ` unchanged when it
// has no mapping. Binary search over 20-character records; no allocation.
function _case_apply(cp: i32, want_upper: boolean): i32 {
    var t: string = _case_table();
    var lo: i32 = 0;
    var hi: i32 = t.len() / 20 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 20;
        var rlo: i32 = _fld(t, base);
        if (cp < rlo) {
            hi = mid - 1;
        } else if (cp > _fld(t, base + 4)) {
            lo = mid + 1;
        } else {
            if (_fld(t, base + 8) == 0) {
                if (want_upper) { return cp + _fld(t, base + 12) - 8388608; }
                return cp + _fld(t, base + 16) - 8388608;
            }
            var odd: i32 = (cp - rlo) & 1;
            if (want_upper) { return cp - odd; }
            return cp + 1 - odd;
        }
    }
    return cp;
}

// ` + "`to_upper_char(cp)`" + ` — the uppercase of a single code point (its
// simple mapping), or ` + "`cp`" + ` unchanged when it has none.
pub function to_upper_char(cp: i32): i32 {
    if (cp < 128) {
        if (cp >= 97 && cp <= 122) { return cp - 32; }
        return cp;
    }
    return _case_apply(cp, true);
}

// ` + "`to_lower_char(cp)`" + ` — the lowercase of a single code point.
pub function to_lower_char(cp: i32): i32 {
    if (cp < 128) {
        if (cp >= 65 && cp <= 90) { return cp + 32; }
        return cp;
    }
    return _case_apply(cp, false);
}

// _is_ascii reports whether every byte of ` + "`s`" + ` is below 0x80, so the
// whole string can take the byte-fold fast path.
function _is_ascii(s: string): boolean {
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        if (s[i] >= 128) { return false; }
        i = i + 1;
    }
    return true;
}

// _ascii_fold remaps the 26 letters starting at ` + "`from`" + ` to those starting
// at ` + "`to`" + `, leaving every other byte alone. The fast path for
// to_upper / to_lower on ASCII input.
function _ascii_fold(s: string, from: i32, to: i32): string {
    var n: i32 = s.len();
    var buf: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i];
        if (b >= from && b < from + 26) { b = b + (to - from); }
        buf = buf.with(i, b as u8);
        i = i + 1;
    }
    return string_from_bytes(buf);
}

// _map_case decodes ` + "`s`" + `, maps every code point, and re-encodes.
// Malformed bytes each become U+FFFD, matching utf8.codepoints.
function _map_case(s: string, want_upper: boolean): string {
    var out: string = "";
    var n: i32 = s.len();
    var i: i32 = 0;
    while (i < n) {
        match (utf8.utf8_decode_at(s, i)) {
            Some(pair) => {
                out = out + utf8.utf8_encode(_case_apply(pair.0, want_upper));
                i = i + pair.1;
            },
            None => { out = out + utf8.utf8_encode(65533); i = i + 1; }
        }
    }
    return out;
}

// ` + "`to_upper(s)`" + ` — ` + "`s`" + ` with every code point mapped to its simple
// uppercase. Pure-ASCII input takes a byte fold and never touches the
// table.
pub function to_upper(s: string): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 97, 65); }
    return _map_case(s, true);
}

// ` + "`to_lower(s)`" + ` — ` + "`s`" + ` with every code point mapped to its simple
// lowercase.
pub function to_lower(s: string): string {
    if (_is_ascii(s)) { return _ascii_fold(s, 65, 97); }
    return _map_case(s, false);
}

// ` + "`eq_ignore_case(a, b)`" + ` — case-insensitive equality under simple
// lowercase folding. Streams both operands, so it allocates nothing and
// stops at the first difference. (Simple folding, so ` + "`ß`" + ` and ` + "`SS`" + ` are
// NOT equal — case FOLDING proper is #5631.)
pub function eq_ignore_case(a: string, b: string): boolean {
    var na: i32 = a.len();
    var nb: i32 = b.len();
    var i: i32 = 0;
    var j: i32 = 0;
    while (i < na && j < nb) {
        var ca: i32 = 65533;
        var wa: i32 = 1;
        match (utf8.utf8_decode_at(a, i)) {
            Some(pa) => { ca = pa.0; wa = pa.1; },
            None => { }
        }
        var cb: i32 = 65533;
        var wb: i32 = 1;
        match (utf8.utf8_decode_at(b, j)) {
            Some(pb) => { cb = pb.0; wb = pb.1; },
            None => { }
        }
        if (to_lower_char(ca) != to_lower_char(cb)) { return false; }
        i = i + wa;
        j = j + wb;
    }
    return i >= na && j >= nb;
}

// _in_ranges binary-searches an inclusive-range table (8-character
// lo | hi records, sorted and non-overlapping) for ` + "`cp`" + `.
function _in_ranges(t: string, cp: i32): boolean {
    var lo: i32 = 0;
    var hi: i32 = t.len() / 8 - 1;
    while (lo <= hi) {
        var mid: i32 = lo + (hi - lo) / 2;
        var base: i32 = mid * 8;
        if (cp < _fld(t, base)) { hi = mid - 1; }
        else if (cp > _fld(t, base + 4)) { lo = mid + 1; }
        else { return true; }
    }
    return false;
}

// ` + "`is_letter(cp)`" + ` — is ` + "`cp`" + ` in the Unicode Letter category (L*)?
pub function is_letter(cp: i32): boolean {
    if (cp < 128) { return (cp >= 65 && cp <= 90) || (cp >= 97 && cp <= 122); }
    return _in_ranges(_letter_ranges(), cp);
}

// ` + "`is_digit(cp)`" + ` — is ` + "`cp`" + ` a Unicode decimal digit (category Nd)?
pub function is_digit(cp: i32): boolean {
    if (cp < 128) { return cp >= 48 && cp <= 57; }
    return _in_ranges(_digit_ranges(), cp);
}

// ` + "`is_alnum(cp)`" + ` — a letter or a decimal digit.
pub function is_alnum(cp: i32): boolean {
    return is_letter(cp) || is_digit(cp);
}

// ` + "`is_whitespace(cp)`" + ` — matches Go's unicode.IsSpace (space, tab,
// newline, NBSP, the Unicode space separators, …).
pub function is_whitespace(cp: i32): boolean {
    if (cp < 128) { return cp == 32 || (cp >= 9 && cp <= 13); }
    return _in_ranges(_space_ranges(), cp);
}

// ` + "`is_upper(cp)`" + ` / ` + "`is_lower(cp)`" + ` — Unicode upper/lowercase letters.
pub function is_upper(cp: i32): boolean {
    if (cp < 128) { return cp >= 65 && cp <= 90; }
    return _in_ranges(_upper_ranges(), cp);
}

pub function is_lower(cp: i32): boolean {
    if (cp < 128) { return cp >= 97 && cp <= 122; }
    return _in_ranges(_lower_ranges(), cp);
}
`)

	return b.String(), stats
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: unicodegen OUTPUT.fern")
		os.Exit(2)
	}
	src, stats := generate()
	if err := os.WriteFile(os.Args[1], []byte(src), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d case runs, %d table bytes, Unicode %s)\n",
		os.Args[1], stats.runs, stats.tableBytes, unicode.Version)
}
