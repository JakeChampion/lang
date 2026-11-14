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

// scriptAliases are the other names of each script, as regex-syntax 0.8.2
// knows them, with the scripts of Unicode 16.0 and 17.0.
var scriptAliases = map[string][]string{
	"Adlam": {"adlm"}, "Anatolian_Hieroglyphs": {"hluw"}, "Arabic": {"arab"}, "Armenian": {"armn"},
	"Avestan": {"avst"}, "Balinese": {"bali"}, "Bamum": {"bamu"}, "Bassa_Vah": {"bass"}, "Batak": {"batk"},
	"Bengali": {"beng"}, "Beria_Erfe": {"berf"}, "Bhaiksuki": {"bhks"}, "Bopomofo": {"bopo"},
	"Brahmi": {"brah"}, "Braille": {"brai"}, "Buginese": {"bugi"}, "Buhid": {"buhd"},
	"Canadian_Aboriginal": {"cans"}, "Carian": {"cari"}, "Caucasian_Albanian": {"aghb"}, "Chakma": {"cakm"},
	"Cherokee": {"cher"}, "Chorasmian": {"chrs"}, "Common": {"zyyy"}, "Coptic": {"copt", "qaac"},
	"Cuneiform": {"xsux"}, "Cypriot": {"cprt"}, "Cypro_Minoan": {"cpmn"}, "Cyrillic": {"cyrl"},
	"Deseret": {"dsrt"}, "Devanagari": {"deva"}, "Dives_Akuru": {"diak"}, "Dogra": {"dogr"},
	"Duployan": {"dupl"}, "Egyptian_Hieroglyphs": {"egyp"}, "Elbasan": {"elba"}, "Elymaic": {"elym"},
	"Ethiopic": {"ethi"}, "Garay": {"gara"}, "Georgian": {"geor"}, "Glagolitic": {"glag"}, "Gothic": {"goth"},
	"Grantha": {"gran"}, "Greek": {"grek"}, "Gujarati": {"gujr"}, "Gunjala_Gondi": {"gong"},
	"Gurmukhi": {"guru"}, "Gurung_Khema": {"gukh"}, "Han": {"hani"}, "Hangul": {"hang"},
	"Hanifi_Rohingya": {"rohg"}, "Hanunoo": {"hano"}, "Hatran": {"hatr"}, "Hebrew": {"hebr"},
	"Hiragana": {"hira"}, "Imperial_Aramaic": {"armi"}, "Inherited": {"qaai", "zinh"},
	"Inscriptional_Pahlavi": {"phli"}, "Inscriptional_Parthian": {"prti"}, "Javanese": {"java"},
	"Kaithi": {"kthi"}, "Kannada": {"knda"}, "Katakana": {"kana"}, "Katakana_Or_Hiragana": {"hrkt"},
	"Kayah_Li": {"kali"}, "Kharoshthi": {"khar"}, "Khitan_Small_Script": {"kits"}, "Khmer": {"khmr"},
	"Khojki": {"khoj"}, "Khudawadi": {"sind"}, "Kirat_Rai": {"krai"}, "Lao": {"laoo"}, "Latin": {"latn"},
	"Lepcha": {"lepc"}, "Limbu": {"limb"}, "Linear_A": {"lina"}, "Linear_B": {"linb"}, "Lycian": {"lyci"},
	"Lydian": {"lydi"}, "Mahajani": {"mahj"}, "Makasar": {"maka"}, "Malayalam": {"mlym"}, "Mandaic": {"mand"},
	"Manichaean": {"mani"}, "Marchen": {"marc"}, "Masaram_Gondi": {"gonm"}, "Medefaidrin": {"medf"},
	"Meetei_Mayek": {"mtei"}, "Mende_Kikakui": {"mend"}, "Meroitic_Cursive": {"merc"},
	"Meroitic_Hieroglyphs": {"mero"}, "Miao": {"plrd"}, "Mongolian": {"mong"}, "Mro": {"mroo"},
	"Multani": {"mult"}, "Myanmar": {"mymr"}, "Nabataean": {"nbat"}, "Nag_Mundari": {"nagm"},
	"Nandinagari": {"nand"}, "New_Tai_Lue": {"talu"}, "Nko": {"nkoo"}, "Nushu": {"nshu"},
	"Nyiakeng_Puachue_Hmong": {"hmnp"}, "Ogham": {"ogam"}, "Ol_Chiki": {"olck"}, "Ol_Onal": {"onao"},
	"Old_Hungarian": {"hung"}, "Old_Italic": {"ital"}, "Old_North_Arabian": {"narb"}, "Old_Permic": {"perm"},
	"Old_Persian": {"xpeo"}, "Old_Sogdian": {"sogo"}, "Old_South_Arabian": {"sarb"}, "Old_Turkic": {"orkh"},
	"Old_Uyghur": {"ougr"}, "Oriya": {"orya"}, "Osage": {"osge"}, "Osmanya": {"osma"}, "Pahawh_Hmong": {"hmng"},
	"Palmyrene": {"palm"}, "Pau_Cin_Hau": {"pauc"}, "Phags_Pa": {"phag"}, "Phoenician": {"phnx"},
	"Psalter_Pahlavi": {"phlp"}, "Rejang": {"rjng"}, "Runic": {"runr"}, "Samaritan": {"samr"},
	"Saurashtra": {"saur"}, "Sharada": {"shrd"}, "Shavian": {"shaw"}, "Siddham": {"sidd"}, "Sidetic": {"sidt"},
	"SignWriting": {"sgnw"}, "Sinhala": {"sinh"}, "Sogdian": {"sogd"}, "Sora_Sompeng": {"sora"},
	"Soyombo": {"soyo"}, "Sundanese": {"sund"}, "Sunuwar": {"sunu"}, "Syloti_Nagri": {"sylo"},
	"Syriac": {"syrc"}, "Tagalog": {"tglg"}, "Tagbanwa": {"tagb"}, "Tai_Le": {"tale"}, "Tai_Tham": {"lana"},
	"Tai_Viet": {"tavt"}, "Tai_Yo": {"tayo"}, "Takri": {"takr"}, "Tamil": {"taml"}, "Tangsa": {"tnsa"},
	"Tangut": {"tang"}, "Telugu": {"telu"}, "Thaana": {"thaa"}, "Tibetan": {"tibt"}, "Tifinagh": {"tfng"},
	"Tirhuta": {"tirh"}, "Todhri": {"todr"}, "Tolong_Siki": {"tols"}, "Tulu_Tigalari": {"tutg"},
	"Ugaritic": {"ugar"}, "Unknown": {"zzzz"}, "Vai": {"vaii"}, "Vithkuqi": {"vith"}, "Wancho": {"wcho"},
	"Warang_Citi": {"wara"}, "Yezidi": {"yezi"}, "Yi": {"yiii"}, "Zanabazar_Square": {"zanb"},
}

// binaryAliases are the short names of the binary properties.
var binaryAliases = map[string][]string{
	"ASCII_Hex_Digit": {"ahex"}, "Alphabetic": {"alpha"}, "Bidi_Control": {"bidic"}, "Bidi_Mirrored": {"bidim"},
	"Case_Ignorable": {"ci"}, "Changes_When_Casefolded": {"cwcf"}, "Changes_When_Casemapped": {"cwcm"},
	"Changes_When_Lowercased": {"cwl"}, "Changes_When_Titlecased": {"cwt"}, "Changes_When_Uppercased": {"cwu"},
	"Default_Ignorable_Code_Point": {"di"}, "Deprecated": {"dep"}, "Diacritic": {"dia"},
	"Emoji_Component": {"ecomp"}, "Emoji_Modifier": {"emod"}, "Emoji_Modifier_Base": {"ebase"},
	"Emoji_Presentation": {"epres"}, "Extended_Pictographic": {"extpict"}, "Extender": {"ext"},
	"Grapheme_Base": {"grbase"}, "Grapheme_Extend": {"grext"}, "Grapheme_Link": {"grlink"},
	"Hex_Digit": {"hex"}, "IDS_Binary_Operator": {"idsb"}, "IDS_Trinary_Operator": {"idst"},
	"ID_Continue": {"idc"}, "ID_Start": {"ids"}, "Ideographic": {"ideo"}, "Join_Control": {"joinc"},
	"Logical_Order_Exception": {"loe"}, "Lowercase": {"lower"}, "Noncharacter_Code_Point": {"nchar"},
	"Other_Alphabetic": {"oalpha"}, "Other_Default_Ignorable_Code_Point": {"odi"},
	"Other_Grapheme_Extend": {"ogrext"}, "Other_ID_Continue": {"oidc"}, "Other_ID_Start": {"oids"},
	"Other_Lowercase": {"olower"}, "Other_Math": {"omath"}, "Other_Uppercase": {"oupper"},
	"Pattern_Syntax": {"patsyn"}, "Pattern_White_Space": {"patws"}, "Prepended_Concatenation_Mark": {"pcm"},
	"Quotation_Mark": {"qmark"}, "Regional_Indicator": {"ri"}, "Sentence_Terminal": {"sterm"},
	"Soft_Dotted": {"sd"}, "Terminal_Punctuation": {"term"}, "Unified_Ideograph": {"uideo"},
	"Uppercase": {"upper"}, "Variation_Selector": {"vs"}, "White_Space": {"space", "wspace"},
	"XID_Continue": {"xidc"}, "XID_Start": {"xids"},
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
		if name == "STerm" {
			continue
		}
		t := t
		props[name] = func(r rune) bool { return unicode.Is(t, r) }
	}
	props["Cased"] = isCased
	props["Case_Ignorable"] = isCaseIgnorable
	for name, rs := range emojiProps() {
		rs := rs
		props[name] = func(r rune) bool { return inRuneRanges(rs, r) }
	}
	return props
}

// emojiProps gives the ranges of each property of emoji-data.txt that
// gcbdata.txt holds.
func emojiProps() map[string][][2]rune {
	out := map[string][][2]rune{}
	_, out["Extended_Pictographic"], _ = parseGCBData()
	for _, line := range strings.Split(gcbData, "\n") {
		f := strings.Split(line, ";")
		if f[0] == "M" {
			out[f[3]] = append(out[f[3]], [2]rune{rune(mustHex(f[1])), rune(mustHex(f[2]))})
		}
	}
	return out
}

func inRuneRanges(rs [][2]rune, r rune) bool {
	i := sort.Search(len(rs), func(i int) bool { return rs[i][1] >= r })
	return i < len(rs) && rs[i][0] <= r
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
		add("sc", name, scriptAliases[name], func(r rune) bool { return unicode.Is(t, r) })
	}
	props := binaryProps()
	var names []string
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		add("bp", name, binaryAliases[name], props[name])
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

const regexFunctions = `// _prop_kept says whether a byte of a name counts: not a space, an
// underscore, a hyphen or a byte outside ASCII.
function _prop_kept(c: i32): boolean {
    return c != 32 && c != 95 && c != 45 && c < 128;
}

// _prop_key gives the loose form of a name, as regex-syntax makes it: no
// "is" at the start, no case, and only the bytes that _prop_kept keeps.
// "isc" stays, as an alias of the category Other.
function _prop_key(name: string): string {
    let from = _prop_is(name);
    let n = 0;
    for i in from..name.len() {
        if (_prop_kept(name[i] as i32)) { n += 1; }
    }
    let out = __alloc_u8(n);
    n = 0;
    for i in from..name.len() {
        let c = name[i] as i32;
        if (_prop_kept(c)) {
            if (c >= 65 && c <= 90) { c += 32; }
            out = out.with(n, c as u8);
            n += 1;
        }
    }
    let key = unsafe { string_from_bytes_unchecked(out) };
    return if (from == 2 && key == "c") { "isc" } else { key };
}

// _prop_is gives 2 for a name that starts with "is" in any case, else 0.
function _prop_is(name: string): i32 {
    return if (name.len() >= 2 && (name[0] | 32u8) == 105u8 && (name[1] | 32u8) == 115u8) { 2 } else { 0 };
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
// range in order. The name is that of ` + "`\\p{...}`" + ` in a regular expression, as
// regex-syntax reads it: case, spaces, underscores, hyphens and an "is" at
// the start do not count. ` + "`gc=`" + `, ` + "`sc=`" + `, ` + "`General_Category=`" + ` and ` + "`Script=`" + `, or the
// same names with ` + "`:`" + ` or ` + "`!=`" + `, select the kind of property. Without a kind, a
// binary property comes first, then a general category, then a script.
// An unknown name gives None.
pub function property_ranges(name: string): Option[i32[]] {
    let (op, width) = _prop_op(name);
    if (op >= 0) {
        let kind = _prop_key(slice_unchecked(name, 0, op) + "");
        return _prop_decode(_prop_valued(kind, _prop_key(slice_unchecked(name, op + width, name.len()) + "")));
    }
    let key = _prop_key(name);
    let span = if (key != "cf" && key != "sc" && key != "lc") { _prop_index("bp:" + key) } else { (-1, 0) };
    if (span.0 < 0) { span = _prop_index("gc:" + key); }
    if (span.0 < 0) { span = _prop_index("sc:" + key); }
    return _prop_decode(span);
}

// _prop_valued gives the span of the ranges of a value of the property
// kind: a general category, Any, Assigned and ASCII for gc, or a script.
function _prop_valued(kind: string, value: string): (i32, i32) {
    if (kind == "sc" || kind == "script") { return _prop_index("sc:" + value); }
    if (kind != "gc" && kind != "generalcategory") { return (-1, 0); }
    let span = _prop_index("gc:" + value);
    if (span.0 < 0 && (value == "any" || value == "assigned" || value == "ascii")) { return _prop_index("bp:" + value); }
    return span;
}

// _prop_op gives the offset and the width of the operator of a name with a
// kind: the first "!=", else the first ":", else the first "=", or -1.
function _prop_op(name: string): (i32, i32) {
    for i in 0..name.len() {
        if (name[i] == 33u8 && i + 1 < name.len() && name[i + 1] == 61u8) { return (i, 2); }
    }
    let colon = __memchr(name, 58, 0);
    if (colon >= 0) { return (colon, 1); }
    return (__memchr(name, 61, 0), 1);
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
