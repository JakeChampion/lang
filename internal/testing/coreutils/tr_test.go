package coreutils

import (
	"strings"
	"testing"
)

func init() {
	registerCorpus("tr", trCases)
}

// trCases is tr(1)'s corpus.
//
// tr has no file operands: everything it does is decided by two SET
// strings, so the corpus is mostly a grammar test. The parts that repay
// the most cases are the ones where a construct is recognised only when
// its whole shape is present (`[:up` and `[x*1` are ordinary bytes,
// `[x*z]` is a fault), and the pairing rules between SET1 and SET2,
// where the same two operands can be a case fold, a positional map, a
// misalignment or an un-extendable tail depending on where the classes
// sit.
func trCases(t *testing.T) []invocation {
	// Mixed case, digits, blanks and a terminator: enough for every
	// class in one line, short enough to read in a failure.
	mixed := "aBcXyZ123 \t\n"
	// Every byte, four times over: the only input that proves a table
	// entry for each of the 256 and that a run crossing a read block is
	// still squeezed as one.
	var all strings.Builder
	for i := 0; i < 256; i++ {
		all.WriteByte(byte(i))
	}
	allBytes := strings.Repeat(all.String(), 4)
	// Longer than one 64 KiB read, so a squeeze run and a translation
	// both cross a block boundary.
	big := strings.Repeat("aabbccdd\n", 20000)
	// A SET that is not valid UTF-8: it reaches the tables and the
	// diagnostics as bytes, so nothing may re-encode it.
	raw := "a\xffb"
	// A directory as stdin: open(2) succeeds and read(2) fails EISDIR,
	// which is the only way this corpus reaches a read error.
	dir := t.TempDir()

	return []invocation{
		// Translation.
		{name: "translate", args: []string{"abc", "xyz"}, stdin: mixed},
		{name: "set2 shorter", args: []string{"abcde", "xy"}, stdin: mixed},
		{name: "set2 longer", args: []string{"a", "XYZ"}, stdin: mixed},
		{name: "set1 empty", args: []string{"", "X"}, stdin: mixed},
		{name: "both empty", args: []string{"", ""}, stdin: mixed},
		{name: "set2 empty", args: []string{"a", ""}, stdin: mixed},
		{name: "duplicate in set1", args: []string{"aa", "XY"}, stdin: mixed},
		{name: "identity", args: []string{"abc", "abc"}, stdin: mixed},
		{name: "every byte", args: []string{"\\000-\\377", "[X*]"}, stdin: allBytes},
		{name: "translate across read blocks", args: []string{"ab", "xy"}, stdin: big},

		// Ranges.
		{name: "range", args: []string{"a-c", "x-z"}, stdin: mixed},
		{name: "range in set1 only", args: []string{"a-z", "X"}, stdin: mixed},
		{name: "one-byte range", args: []string{"a-a", "X"}, stdin: mixed},
		{name: "range then chars", args: []string{"a-c12", "XYZ"}, stdin: mixed},
		{name: "dash at the end is a byte", args: []string{"ab-", "xyz"}, stdin: mixed},
		{name: "escaped dash is a byte", args: []string{"a\\-c", "XYZ"}, stdin: mixed},
		{name: "range over escapes", args: []string{"\\001-\\003", "XYZ"}, stdin: allBytes},
		{name: "reverse range", args: []string{"z-a", "x"}, stdin: mixed},
		{name: "reverse range in set2", args: []string{"a-c", "z-x"}, stdin: mixed},
		{name: "reverse range of control bytes", args: []string{"\\002-\\001", "x"}, stdin: mixed},
		{name: "reverse range of high bytes", args: []string{"\\377-\\376", "x"}, stdin: mixed},
		{name: "range to a bracket is reversed", args: []string{"a-[:alpha:]", "X"}, stdin: mixed},

		// Escapes.
		{name: "named escapes", args: []string{"\\a\\b\\f\\n\\r\\t\\v", "ABCDEFG"}, stdin: "a\a\b\f\n\r\t\vz"},
		{name: "escaped backslash", args: []string{"a\\\\b", "XYZ"}, stdin: "a\\b\n"},
		{name: "unknown escape is the byte", args: []string{"a\\zb", "XYZ"}, stdin: mixed},
		{name: "escaped digit is not octal", args: []string{"a\\9", "XY"}, stdin: mixed},
		{name: "escaped eight is not octal", args: []string{"\\8", "X"}, stdin: mixed},
		{name: "one octal digit", args: []string{"\\1", "X"}, stdin: "a\x01b\n"},
		{name: "two octal digits", args: []string{"\\40", "X"}, stdin: mixed},
		{name: "three octal digits", args: []string{"\\101", "Z"}, stdin: mixed},
		{name: "octal NUL", args: []string{"\\0", "X"}, stdin: "a\x00b\n"},
		{name: "octal 377", args: []string{"\\377", "X"}, stdin: "a\xffb\n"},
		{name: "ambiguous octal 400", args: []string{"\\400", "Z"}, stdin: mixed},
		{name: "ambiguous octal 777", args: []string{"\\777", "Z"}, stdin: mixed},
		{name: "ambiguous octal 501", args: []string{"\\501", "Z"}, stdin: mixed},
		{name: "two ambiguous octals", args: []string{"\\400\\500", "Z"}, stdin: mixed},
		{name: "octal then a digit", args: []string{"\\0400", "Z"}, stdin: mixed},
		{name: "trailing backslash", args: []string{"ab\\", "XYZ"}, stdin: mixed},
		{name: "trailing backslash in set2", args: []string{"ab", "X\\"}, stdin: mixed},
		{name: "both warnings", args: []string{"ab\\", "\\400"}, stdin: mixed},
		{name: "set that is not valid UTF-8", args: []string{raw, "XYZ"}, stdin: "a\xffb\n"},
		{name: "set2 that is not valid UTF-8", args: []string{"abc", raw}, stdin: mixed},

		// Character classes.
		{name: "class alnum", args: []string{"-d", "[:alnum:]"}, stdin: mixed},
		{name: "class alpha", args: []string{"-d", "[:alpha:]"}, stdin: mixed},
		{name: "class blank", args: []string{"-d", "[:blank:]"}, stdin: mixed},
		{name: "class cntrl", args: []string{"-d", "[:cntrl:]"}, stdin: allBytes},
		{name: "class digit", args: []string{"-d", "[:digit:]"}, stdin: mixed},
		{name: "class graph", args: []string{"-d", "[:graph:]"}, stdin: allBytes},
		{name: "class lower", args: []string{"-d", "[:lower:]"}, stdin: mixed},
		{name: "class print", args: []string{"-d", "[:print:]"}, stdin: allBytes},
		{name: "class punct", args: []string{"-d", "[:punct:]"}, stdin: allBytes},
		{name: "class space", args: []string{"-d", "[:space:]"}, stdin: mixed},
		{name: "class upper", args: []string{"-d", "[:upper:]"}, stdin: mixed},
		{name: "class xdigit", args: []string{"-d", "[:xdigit:]"}, stdin: mixed},
		{name: "invalid class", args: []string{"[:foo:]", "x"}, stdin: mixed},
		{name: "class name is case sensitive", args: []string{"[:UPPER:]", "x"}, stdin: mixed},
		{name: "empty class name", args: []string{"[::]", "x"}, stdin: mixed},
		{name: "class name with a control byte", args: []string{"[:\t:]", "x"}, stdin: mixed},
		{name: "unterminated class is bytes", args: []string{"[:up", "XYZW"}, stdin: mixed},
		{name: "class without the colon is bytes", args: []string{"[:upper]", "X"}, stdin: mixed},
		{name: "bare bracket is a byte", args: []string{"[", "X"}, stdin: "a[b\n"},
		{name: "bracket pair is bytes", args: []string{"a[b]c", "XYZW"}, stdin: "a[b]c\n"},

		// The case-fold pairing.
		{name: "lower to upper", args: []string{"[:lower:]", "[:upper:]"}, stdin: mixed},
		{name: "upper to lower", args: []string{"[:upper:]", "[:lower:]"}, stdin: mixed},
		{name: "upper to upper is the identity", args: []string{"[:upper:]", "[:upper:]"}, stdin: mixed},
		{name: "lower to lower is the identity", args: []string{"[:lower:]", "[:lower:]"}, stdin: mixed},
		{name: "both classes swapped", args: []string{"[:upper:][:lower:]", "[:lower:][:upper:]"}, stdin: mixed},
		{name: "class in set1 with a plain set2", args: []string{"[:upper:]", "x"}, stdin: mixed},
		{name: "aligned after one byte", args: []string{"0[:lower:]", "1[:upper:]"}, stdin: mixed},
		{name: "aligned after a class", args: []string{"[:digit:][:lower:]", "0123456789[:upper:]"}, stdin: mixed},
		{name: "class after the pair", args: []string{"[:lower:]", "[:upper:]x"}, stdin: mixed},
		{name: "misaligned plain to class", args: []string{"x", "[:upper:]"}, stdin: mixed},
		{name: "misaligned range to class", args: []string{"a-z", "[:upper:]"}, stdin: mixed},
		{name: "misaligned other class", args: []string{"[:alpha:]", "[:upper:]"}, stdin: mixed},
		{name: "misaligned offset", args: []string{"x[:lower:]", "[:upper:]"}, stdin: mixed},
		{name: "misaligned second class", args: []string{"[:lower:]", "[:upper:][:upper:]"}, stdin: mixed},
		{name: "misaligned before the tail", args: []string{"ab", "x[:upper:]"}, stdin: mixed},
		{name: "restricted class in set2", args: []string{"[:lower:]", "[:digit:]"}, stdin: mixed},
		{name: "restricted class after a pair", args: []string{"[:lower:][:digit:]", "[:upper:][:digit:]"}, stdin: mixed},
		{name: "set2 ends with a class", args: []string{"[:lower:]0", "[:upper:]"}, stdin: mixed},
		{name: "set2 ends with a class under -t", args: []string{"-t", "[:lower:]0", "[:upper:]"}, stdin: mixed},
		{name: "set2 ends with a range", args: []string{"abcd", "x-y"}, stdin: mixed},

		// Equivalence classes.
		{name: "equiv in set1", args: []string{"[=a=]", "X"}, stdin: mixed},
		{name: "equiv deleted", args: []string{"-d", "[=a=]"}, stdin: mixed},
		{name: "equiv of an escape", args: []string{"[=\\n=]", "x"}, stdin: mixed},
		{name: "equiv in set2 while translating", args: []string{"abc", "[=x=]"}, stdin: mixed},
		{name: "equiv in set2 while squeezing", args: []string{"-ds", "a", "[=b=]"}, stdin: "aabbcc\n"},
		{name: "equiv with two bytes", args: []string{"[=ab=]", "X"}, stdin: mixed},
		{name: "equiv with two control bytes", args: []string{"[=\t\t=]", "X"}, stdin: mixed},
		{name: "empty equiv", args: []string{"[==]", "X"}, stdin: mixed},
		{name: "unterminated equiv is bytes", args: []string{"[=a", "XY"}, stdin: mixed},
		{name: "equiv is not a range endpoint", args: []string{"[=a=]-z", "X"}, stdin: mixed},

		// Repeats.
		{name: "repeat until set1 ends", args: []string{"abc", "[x*]"}, stdin: mixed},
		{name: "repeat with a count", args: []string{"abcde", "[x*3]"}, stdin: mixed},
		{name: "repeat before other bytes", args: []string{"abcd", "[q*]xy"}, stdin: mixed},
		{name: "repeat after other bytes", args: []string{"abcd", "xy[q*]"}, stdin: mixed},
		{name: "repeat when set2 is already longer", args: []string{"ab", "xyz[q*]"}, stdin: mixed},
		{name: "repeat count zero", args: []string{"abcd", "[x*0]"}, stdin: mixed},
		{name: "repeat count in octal", args: []string{"abcdefghijk", "[x*012]"}, stdin: mixed},
		{name: "repeat of an escape", args: []string{"abcd", "[\\n*3]"}, stdin: mixed},
		{name: "repeat of a bracket", args: []string{"abcd", "[[*3]"}, stdin: mixed},
		{name: "repeat of a star", args: []string{"abcd", "[**3]"}, stdin: mixed},
		{name: "repeat in set1 with a count", args: []string{"[a*3]", "x"}, stdin: mixed},
		{name: "repeat in set1 without one", args: []string{"[a*]", "x"}, stdin: mixed},
		{name: "two repeats in set2", args: []string{"abcd", "[x*][y*]"}, stdin: mixed},
		{name: "repeat count not a number", args: []string{"abc", "[x*z]"}, stdin: mixed},
		{name: "repeat count with trailing garbage", args: []string{"abc", "[x*1z]"}, stdin: mixed},
		{name: "repeat count is a bad octal", args: []string{"abcd", "[x*08]"}, stdin: mixed},
		{name: "repeat count is hex", args: []string{"abc", "[x*0x10]"}, stdin: mixed},
		{name: "repeat count is negative", args: []string{"abcd", "[x*-1]"}, stdin: mixed},
		{name: "repeat count is signed", args: []string{"abc", "[x*+1]"}, stdin: mixed},
		{name: "repeat count is blank-led", args: []string{"abc", "[x* 1]"}, stdin: mixed},
		{name: "repeat count at the maximum", args: []string{"abc", "[x*18446744073709551614]"}, stdin: mixed},
		{name: "repeat count past the maximum", args: []string{"abc", "[x*18446744073709551615]"}, stdin: mixed},
		{name: "repeat count overflows", args: []string{"abc", "[x*99999999999999999999]"}, stdin: mixed},
		{name: "repeat count in set1", args: []string{"[x*z]", "Q"}, stdin: mixed},
		{name: "unterminated repeat is bytes", args: []string{"abcdef", "[x*1"}, stdin: mixed},
		{name: "unterminated bare repeat is bytes", args: []string{"abcdef", "[x*"}, stdin: mixed},
		{name: "escaped bracket is not a repeat", args: []string{"\\[x*3]", "QRSTUV"}, stdin: "a[x*3]z\n"},
		{name: "class then a star", args: []string{"abc", "[[:upper:]*3]"}, stdin: mixed},

		// -d.
		{name: "delete", args: []string{"-d", "abc"}, stdin: mixed},
		{name: "delete long", args: []string{"--delete", "abc"}, stdin: mixed},
		{name: "delete nothing", args: []string{"-d", ""}, stdin: mixed},
		{name: "delete a range", args: []string{"-d", "a-z"}, stdin: mixed},
		{name: "delete every byte", args: []string{"-d", "\\000-\\377"}, stdin: allBytes},
		{name: "delete across read blocks", args: []string{"-d", "a"}, stdin: big},

		// -s.
		{name: "squeeze one set", args: []string{"-s", "a"}, stdin: "aaabbb\n"},
		{name: "squeeze long", args: []string{"--squeeze-repeats", "a"}, stdin: "aaabbb\n"},
		{name: "squeeze a class", args: []string{"-s", "[:space:]"}, stdin: "a   b\t\t\tc\n\n\n"},
		{name: "squeeze uses set2 when translating", args: []string{"-s", "ab", "xy"}, stdin: "aabbcc\n"},
		{name: "squeeze after translating to one byte", args: []string{"-s", "abc", "xxx"}, stdin: "abcabc\n"},
		{name: "squeeze nothing", args: []string{"-s", ""}, stdin: mixed},
		{name: "squeeze every byte", args: []string{"-s", "\\000-\\377"}, stdin: allBytes},
		{name: "squeeze across read blocks", args: []string{"-s", "a"}, stdin: strings.Repeat("a", 200000)},
		{name: "squeeze a run that straddles a block", args: []string{"-s", "ab"}, stdin: big},

		// -d -s.
		{name: "delete and squeeze", args: []string{"-ds", "a", "b"}, stdin: "aabbcc\n"},
		{name: "delete and squeeze long", args: []string{"--delete", "--squeeze-repeats", "a", "b"}, stdin: "aabbcc\n"},
		{name: "delete and squeeze with an empty set2", args: []string{"-ds", "a", ""}, stdin: mixed},
		{name: "delete and squeeze classes", args: []string{"-ds", "[:digit:]", "[:space:]"}, stdin: "a1  2\t\tb\n"},
		{name: "delete brings two runs together", args: []string{"-ds", "b", "a"}, stdin: "abaaba\n"},

		// -c / -C.
		{name: "complement translate", args: []string{"-c", "a", "X"}, stdin: mixed},
		{name: "complement with a short set2", args: []string{"-c", "a", "XY"}, stdin: mixed},
		{name: "complement upper case", args: []string{"-C", "a", "X"}, stdin: mixed},
		{name: "complement long", args: []string{"--complement", "a", "X"}, stdin: mixed},
		{name: "complement delete", args: []string{"-cd", "[:alnum:]"}, stdin: mixed},
		{name: "complement squeeze one set", args: []string{"-cs", "ab"}, stdin: "aabbcc\n"},
		{name: "complement squeeze two sets", args: []string{"-cs", "ab", "xy"}, stdin: "aabbcc\n"},
		{name: "complement delete and squeeze", args: []string{"-cds", "ab", "xy"}, stdin: "aabbcc\n"},
		{name: "complement truncated", args: []string{"-ct", "a", "XY"}, stdin: "abcdef\n"},
		{name: "complement with a class tail", args: []string{"-c", "a", "[:upper:]"}, stdin: mixed},
		{name: "complement with a class tail truncated", args: []string{"-ct", "a", "[:upper:]"}, stdin: mixed},
		{name: "complement with a class then a byte", args: []string{"-c", "a", "[:upper:]x"}, stdin: mixed},
		{name: "complement of a repeat", args: []string{"-c", "[a*230]", "ab[:upper:]"}, stdin: mixed},
		{name: "complement of a class", args: []string{"-c", "[:lower:]", "[:upper:]"}, stdin: mixed},
		{name: "complement with an empty set2", args: []string{"-c", "a", ""}, stdin: mixed},
		{name: "complement of everything", args: []string{"-c", "\\000-\\377", "X"}, stdin: mixed},

		// -t.
		{name: "truncate", args: []string{"-t", "abc", "X"}, stdin: mixed},
		{name: "truncate long", args: []string{"--truncate-set1", "abc", "X"}, stdin: mixed},
		{name: "truncate with a longer set2", args: []string{"-t", "a", "XYZ"}, stdin: mixed},
		{name: "truncate with an empty set2", args: []string{"-t", "a", ""}, stdin: mixed},
		{name: "truncate a case fold", args: []string{"-t", "[:lower:]", "[:upper:]abc"}, stdin: mixed},
		{name: "truncate is ignored when deleting", args: []string{"-td", "abc"}, stdin: mixed},

		// Operand counts: each combination words its own fault.
		{name: "no operands", stdin: mixed},
		{name: "one operand translating", args: []string{"abc"}, stdin: mixed},
		{name: "three operands translating", args: []string{"a", "b", "c"}, stdin: mixed},
		{name: "no operands deleting", args: []string{"-d"}, stdin: mixed},
		{name: "two operands deleting", args: []string{"-d", "a", "b"}, stdin: mixed},
		{name: "three operands deleting", args: []string{"-d", "a", "b", "c"}, stdin: mixed},
		{name: "no operands squeezing", args: []string{"-s"}, stdin: mixed},
		{name: "three operands squeezing", args: []string{"-s", "a", "b", "c"}, stdin: mixed},
		{name: "one operand deleting and squeezing", args: []string{"-ds", "a"}, stdin: mixed},
		{name: "three operands deleting and squeezing", args: []string{"-ds", "a", "b", "c"}, stdin: mixed},
		{name: "one operand complemented", args: []string{"-c", "a"}, stdin: mixed},
		{name: "operand needing quotes", args: []string{"a'b", "c d", "e"}, stdin: mixed},
		{name: "operand that is not valid UTF-8 in a fault", args: []string{"a", "b", raw}, stdin: mixed},

		// Option scanning. tr's optstring starts with `+`, so the first
		// operand ends it whether or not POSIXLY_CORRECT is set.
		{name: "option after an operand", args: []string{"a", "b", "-d"}, stdin: mixed},
		{name: "option after one operand", args: []string{"a", "-d"}, stdin: mixed},
		{name: "posix option after an operand", args: []string{"a", "b", "-d"}, stdin: mixed, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix options first", args: []string{"-d", "a"}, stdin: mixed, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "dashdash", args: []string{"--", "a", "X"}, stdin: mixed},
		{name: "dashdash before a dash set", args: []string{"--", "-a", "X"}, stdin: "a-b\n"},
		{name: "lone dash is a set", args: []string{"-", "X"}, stdin: "a-b\n"},
		{name: "clustered flags", args: []string{"-cds", "a", "b"}, stdin: "aabbcc\n"},
		{name: "repeated flag", args: []string{"-dd", "a"}, stdin: mixed},
		{name: "invalid short option", args: []string{"-x", "a", "b"}, stdin: mixed},
		{name: "invalid option in a cluster", args: []string{"-dx", "a"}, stdin: mixed},
		{name: "unrecognized long option", args: []string{"--foo", "a", "b"}, stdin: mixed},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}, stdin: mixed},
		{name: "long flag rejects a value", args: []string{"--delete=x", "a"}, stdin: mixed},
		{name: "empty long name is ambiguous", args: []string{"--=x"}, stdin: mixed},
		{name: "unique long prefix c", args: []string{"--c", "a", "X"}, stdin: mixed},
		{name: "unique long prefix d", args: []string{"--d", "a"}, stdin: mixed},
		{name: "unique long prefix s", args: []string{"--s", "a"}, stdin: "aab\n"},
		{name: "unique long prefix t", args: []string{"--t", "a", "X"}, stdin: mixed},
		{name: "help rejects a value", args: []string{"--help=x"}, stdin: mixed},
		{name: "version rejects a value", args: []string{"--version=x"}, stdin: mixed},

		// Input edges.
		{name: "empty stdin", args: []string{"a", "b"}},
		{name: "stdin with no trailing newline", args: []string{"a", "X"}, stdin: "abc"},
		{name: "stdin of NUL bytes", args: []string{"\\0", "X"}, stdin: "a\x00b\x00c"},
		{name: "stdin from a directory", args: []string{"a", "b"}, stdinPath: dir},

		// Write failures.
		{name: "stdout closed", args: []string{"a", "b"}, stdin: mixed, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"a", "b"}, stdin: mixed, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"a", "b"}, stdin: big, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"a", "b"}, stdin: big, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{"a", "b"}, stdout: stdoutClosed},
		{name: "stdout closed with a usage error", args: []string{"a"}, stdout: stdoutClosed},
	}
}

func TestTrParity(t *testing.T) {
	requireParity(t, "tr", trCases(t))
}

func TestTrHelpVersion(t *testing.T) {
	requireHelp(t, "tr", []string{"--help"}, 0)
	requireHelp(t, "tr", []string{"--he"}, 0)
	requireVersion(t, "tr", []string{"--version"}, 0)
	requireVersion(t, "tr", []string{"--vers"}, 0)
}
