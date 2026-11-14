package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// The tables of std/regex: the code point ranges of each general category,
// script and binary property for \p{...}, the word characters of \w and
// Unicode \b, and the runs of simple case folding for (?i).

// gcNames are the long names and the other aliases of each general category,
// from PropertyValueAliases.txt.
var gcNames = map[string][]string{
	"C": {"Other"}, "Cc": {"Control", "cntrl"}, "Cf": {"Format"}, "Cn": {"Unassigned"},
	"Co": {"Private_Use"}, "Cs": {"Surrogate"}, "L": {"Letter"}, "LC": {"Cased_Letter"},
	"Ll": {"Lowercase_Letter"}, "Lm": {"Modifier_Letter"}, "Lo": {"Other_Letter"},
	"Lt": {"Titlecase_Letter"}, "Lu": {"Uppercase_Letter"}, "M": {"Mark", "Combining_Mark"},
	"Mc": {"Spacing_Mark"}, "Me": {"Enclosing_Mark"}, "Mn": {"Nonspacing_Mark"}, "N": {"Number"},
	"Nd": {"Decimal_Number", "digit"}, "Nl": {"Letter_Number"}, "No": {"Other_Number"},
	"P": {"Punctuation", "punct"}, "Pc": {"Connector_Punctuation"}, "Pd": {"Dash_Punctuation"},
	"Pe": {"Close_Punctuation"}, "Pf": {"Final_Punctuation"}, "Pi": {"Initial_Punctuation"},
	"Po": {"Other_Punctuation"}, "Ps": {"Open_Punctuation"}, "S": {"Symbol"},
	"Sc": {"Currency_Symbol"}, "Sk": {"Modifier_Symbol"}, "Sm": {"Math_Symbol"},
	"So": {"Other_Symbol"}, "Z": {"Separator"}, "Zl": {"Line_Separator"},
	"Zp": {"Paragraph_Separator"}, "Zs": {"Space_Separator"},
}

// gcGroups are the categories of one letter and LC, as unions of the
// categories of two letters.
var gcGroups = map[string]string{
	"C": "Cc Cf Cn Co Cs", "L": "Lu Ll Lt Lm Lo", "LC": "Lu Ll Lt", "M": "Mn Mc Me",
	"N": "Nd Nl No", "P": "Pc Pd Ps Pe Pi Pf Po", "S": "Sm Sc Sk So", "Z": "Zs Zl Zp",
}

// propKey is the loose form of a property name in UTS #18: no case, and no
// spaces, underscores or hyphens.
func propKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r != ' ' && r != '_' && r != '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var assigned = func() []bool {
	out := make([]bool, unicode.MaxRune+1)
	for name, t := range unicode.Categories {
		if len(name) != 2 || name == "Cn" || name == "LC" {
			continue
		}
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if !out[r] && unicode.Is(t, r) {
				out[r] = true
			}
		}
	}
	return out
}()

func isAssigned(r rune) bool { return assigned[r] }

func isAlphabetic(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl, unicode.Other_Alphabetic)
}

// isPerlWord is \w of UTS #18 Annex C: Alphabetic, Mark, Decimal_Number,
// Connector_Punctuation and Join_Control.
func isPerlWord(r rune) bool {
	return isAlphabetic(r) || unicode.In(r, unicode.M, unicode.Nd, unicode.Pc, unicode.Join_Control)
}

func gcPred(name string) func(rune) bool {
	if name == "Cn" {
		return func(r rune) bool { return !isAssigned(r) }
	}
	if group, ok := gcGroups[name]; ok {
		parts := strings.Fields(group)
		return func(r rune) bool {
			for _, p := range parts {
				if gcPred(p)(r) {
					return true
				}
			}
			return false
		}
	}
	t := unicode.Categories[name]
	return func(r rune) bool { return unicode.Is(t, r) }
}

// binaryProps are the binary properties of \p{...}: those of Go's unicode
// package that UTS #18 names, and four that UCD derives from them.
func binaryProps() map[string]func(rune) bool {
	props := map[string]func(rune) bool{
		"Alphabetic": isAlphabetic,
		"Lowercase":  func(r rune) bool { return unicode.In(r, unicode.Ll, unicode.Other_Lowercase) },
		"Uppercase":  func(r rune) bool { return unicode.In(r, unicode.Lu, unicode.Other_Uppercase) },
		"Math":       func(r rune) bool { return unicode.In(r, unicode.Sm, unicode.Other_Math) },
		"Any":        func(r rune) bool { return true },
		"ASCII":      func(r rune) bool { return r < 128 },
		"Assigned":   isAssigned,
	}
	for name, t := range unicode.Properties {
		if strings.HasPrefix(name, "Other_") || name == "STerm" {
			continue
		}
		t := t
		props[name] = func(r rune) bool { return unicode.Is(t, r) }
	}
	return props
}

type propTable struct {
	keys []string
	rs   [][2]rune
}

// propTables gives each table of ranges and the keys that name it. A key is
// "gc:", "sc:" or "bp:" and the loose name, or "pw:word" for \w.
func propTables() []propTable {
	var out []propTable
	seen := map[string]bool{}
	add := func(kind, name string, aliases []string, pred func(rune) bool) {
		var keys []string
		for _, n := range append([]string{name}, aliases...) {
			k := kind + ":" + propKey(n)
			if seen[k] {
				panic("unicodegen: property key " + k + " is not unique")
			}
			seen[k] = true
			keys = append(keys, k)
		}
		out = append(out, propTable{keys: keys, rs: coalesce(pred)})
	}
	var gcs []string
	for name := range gcNames {
		gcs = append(gcs, name)
	}
	sort.Strings(gcs)
	for _, name := range gcs {
		add("gc", name, gcNames[name], gcPred(name))
	}
	var scripts []string
	for name := range unicode.Scripts {
		scripts = append(scripts, name)
	}
	sort.Strings(scripts)
	for _, name := range scripts {
		t := unicode.Scripts[name]
		add("sc", name, nil, func(r rune) bool { return unicode.Is(t, r) })
	}
	props := binaryProps()
	var names []string
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		add("bp", name, nil, props[name])
	}
	add("pw", "word", nil, isPerlWord)
	return out
}

// encodeProps packs the tables into one string of ranges, and gives the
// offset and the count of ranges of each key.
func encodeProps(ts []propTable) (string, map[string][2]int) {
	var b strings.Builder
	index := map[string][2]int{}
	at := 0
	for _, t := range ts {
		b.WriteString(encodeRanges(t.rs))
		for _, k := range t.keys {
			index[k] = [2]int{at, len(t.rs)}
		}
		at += len(t.rs)
	}
	return b.String(), index
}

type foldRun struct {
	lo, hi rune
	even   int
	odd    int
}

func foldDelta(r rune) int { return int(unicode.SimpleFold(r)) - int(r) }

// foldRuns gives the runs of simple case folding. In a run, the next code
// point of the case orbit of each code point is that code point plus the
// delta of its parity from the start of the run.
func foldRuns() []foldRun {
	var runs []foldRun
	for r := rune(0); r <= unicode.MaxRune; {
		d := foldDelta(r)
		if d == 0 {
			r++
			continue
		}
		run := foldRun{lo: r, hi: r, even: d, odd: d}
		if r < unicode.MaxRune && foldDelta(r+1) != 0 {
			run.odd = foldDelta(r + 1)
		}
		for run.hi < unicode.MaxRune {
			next := run.hi + 1
			want := run.even
			if (next-run.lo)&1 == 1 {
				want = run.odd
			}
			if foldDelta(next) != want {
				break
			}
			run.hi = next
		}
		runs = append(runs, run)
		r = run.hi + 1
	}
	return runs
}

func encodeFoldRuns(runs []foldRun) string {
	var b strings.Builder
	for _, rn := range runs {
		encField(&b, int(rn.lo))
		encField(&b, int(rn.hi))
		encField(&b, rn.even+bias)
		encField(&b, rn.odd+bias)
	}
	return b.String()
}

func decodeFold(t string, cp int) int {
	lo, hi := 0, len(t)/16-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		base := mid * 16
		rlo := decField(t, base)
		switch {
		case cp < rlo:
			hi = mid - 1
		case cp > decField(t, base+4):
			lo = mid + 1
		case (cp-rlo)&1 == 0:
			return cp + decField(t, base+8) - bias
		default:
			return cp + decField(t, base+12) - bias
		}
	}
	return cp
}

func verifyRegexData(propT string, index map[string][2]int, ts []propTable, foldT string) {
	for _, t := range ts {
		span := index[t.keys[0]]
		if got := propT[span[0]*rangeChars : (span[0]+span[1])*rangeChars]; got != encodeRanges(t.rs) {
			panic("unicodegen: property table " + t.keys[0] + " does not decode to its ranges")
		}
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if got, want := decodeFold(foldT, int(r)), int(unicode.SimpleFold(r)); got != want {
			panic(fmt.Sprintf("unicodegen: simple_fold(U+%04X) = U+%04X, want U+%04X", r, got, want))
		}
	}
}

// encodePropNames packs the keys in order, each as the key, "=", and the
// offset and the count of its ranges, then ";".
func encodePropNames(index map[string][2]int) string {
	keys := make([]string, 0, len(index))
	for k := range index {
		if strings.ContainsAny(k, "=;") {
			panic("unicodegen: property key " + k + " holds a separator")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=")
		encField(&b, index[k][0])
		encField(&b, index[k][1])
		b.WriteString(";")
	}
	return b.String()
}

// regexData writes the tables and the functions of std/unicode that
// std/regex uses.
func regexData(b *strings.Builder) int {
	ts := propTables()
	propT, index := encodeProps(ts)
	foldT := encodeFoldRuns(foldRuns())
	verifyRegexData(propT, index, ts, foldT)
	emitTable(b, "_prop_table", propT)
	emitTable(b, "_fold_runs_table", foldT)
	namesT := encodePropNames(index)
	emitTable(b, "_prop_names_table", namesT)
	b.WriteString(regexFunctions)
	return len(propT) + len(foldT) + len(namesT)
}

const regexFunctions = `function _prop_kept(c: i32): boolean {
    return c != 32 && c != 95 && c != 45;
}

function _prop_key(name: string): string {
    let n = 0;
    for i in 0..name.len() {
        if (_prop_kept(name[i] as i32)) { n += 1; }
    }
    let out = __alloc_u8(n);
    n = 0;
    for i in 0..name.len() {
        let c = name[i] as i32;
        if (_prop_kept(c)) {
            if (c >= 65 && c <= 90) { c += 32; }
            out = out.with(n, c as u8);
            n += 1;
        }
    }
    unsafe {
        return string_from_bytes_unchecked(out);
    }
}

// _prop_index gives the offset and the count of the ranges of a key, or -1
// and 0.
function _prop_index(key: string): (i32, i32) {
    let t = _prop_names_table();
    let i = 0;
    while (i < t.len()) {
        let eq = __memchr(t, 61, i);
        if (eq - i == key.len() && _same_at(t, i, key)) {
            return (_fld(t, eq + 1), _fld(t, eq + 5));
        }
        i = eq + 10;
    }
    return (-1, 0);
}

function _same_at(t: string, at: i32, key: string): boolean {
    for k in 0..key.len() {
        if (t[at + k] != key[k]) { return false; }
    }
    return true;
}

function _prop_decode(span: (i32, i32)): Option[i32[]] {
    if (span.0 < 0) { return None; }
    let t = _prop_table();
    let out = __alloc_i32(2 * span.1);
    for k in 0..2 * span.1 {
        out = out.with(k, _fld(t, 8 * span.0 + 4 * k));
    }
    return Some(out);
}

// property_ranges gives the code points of a general category, a script or a
// binary property, as pairs of the first and the last code point of each
// range in order. The name is that of ` + "`\\p{...}`" + ` in a regular expression: case,
// spaces, underscores and hyphens do not count. ` + "`gc=`" + `, ` + "`sc=`" + `,
// ` + "`General_Category=`" + ` and ` + "`Script=`" + ` select the kind of property. Without a
// kind, a general category comes first, then a script, then a binary
// property. An unknown name gives None.
pub function property_ranges(name: string): Option[i32[]] {
    let key = _prop_key(name);
    let eq = __memchr(key, 61, 0);
    if (eq < 0) { eq = __memchr(key, 58, 0); }
    if (eq >= 0) {
        let kind = slice_unchecked(key, 0, eq) + "";
        let value = slice_unchecked(key, eq + 1, key.len()) + "";
        if (kind == "gc" || kind == "generalcategory") { return _prop_decode(_prop_index("gc:" + value)); }
        if (kind == "sc" || kind == "script") { return _prop_decode(_prop_index("sc:" + value)); }
        return None;
    }
    for kind in ["gc:", "sc:", "bp:"] {
        let span = _prop_index(kind + key);
        if (span.0 >= 0) { return _prop_decode(span); }
    }
    return None;
}

// word_ranges gives the word characters of UTS #18, which ` + "`\\w`" + ` matches:
// Alphabetic, Mark, Decimal_Number, Connector_Punctuation and Join_Control.
pub function word_ranges(): i32[] {
    let Some(rs) = _prop_decode(_prop_index("pw:word")) else { return []; };
    return rs;
}

function _fold_next(cp: i32): i32 {
    let t = _fold_runs_table();
    let lo = 0;
    let hi = t.len() / 16 - 1;
    while (lo <= hi) {
        let mid = lo + (hi - lo) / 2;
        let base = mid * 16;
        let rlo = _fld(t, base);
        if (cp < rlo) {
            hi = mid - 1;
        } else if (cp > _fld(t, base + 4)) {
            lo = mid + 1;
        } else if ((cp - rlo & 1) == 0) {
            return cp + _fld(t, base + 8) - 8388608;
        } else {
            return cp + _fld(t, base + 12) - 8388608;
        }
    }
    return cp;
}

// simple_fold gives the next character of the orbit of ` + "`c`" + ` under simple case
// folding, or ` + "`c`" + ` when the orbit has no other character. The orbit goes up
// and then comes back to its lowest character: k, then KELVIN SIGN, then K.
pub function simple_fold(c: char): char {
    return _fold_next(c as i32) as char;
}

// fold_equivalents gives the characters that simple case folding makes
// equal to a character from ` + "`lo`" + ` to ` + "`hi`" + `. They can repeat, and they can be in
// the range.
pub function fold_equivalents(lo: char, hi: char): char[] {
    let out: char[] = [];
    let t = _fold_runs_table();
    let first = lo as i32;
    let last = hi as i32;
    let k = 0;
    let runs = t.len() / 16;
    while (k < runs && _fld(t, 16 * k + 4) < first) {
        k += 1;
    }
    while (k < runs && _fld(t, 16 * k) <= last) {
        let from = _fld(t, 16 * k);
        let to = _fld(t, 16 * k + 4);
        if (from < first) { from = first; }
        if (to > last) { to = last; }
        for cp in from..to + 1 {
            let x = _fold_next(cp);
            while (x != cp) {
                out = out.append(x as char);
                x = _fold_next(x);
            }
        }
        k += 1;
    }
    return out;
}
`
