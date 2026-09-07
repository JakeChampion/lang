package coreutils

import (
	"strings"
	"testing"
)

// base32Cases is base32(1)'s corpus.
//
// The half worth having beside base64's is the padding arithmetic, which
// is where the two differ: a base32 group carries 2, 4, 5, 7 or 8 data
// characters and nothing else, so `AAAAAA==` is six of them and fails
// after writing three bytes. Its SHORT final group also writes nothing,
// where base64's writes the bytes it holds — `MZXW6` is silent and
// `aGVsbG8` is not.
func base32Cases(t *testing.T) []invocation {
	f := baseFixtureFor(t)

	cases := []invocation{
		// Encoding, one case per residue.
		{name: "empty", stdin: ""},
		{name: "one byte", stdin: "f"},
		{name: "two bytes", stdin: "fo"},
		{name: "three bytes", stdin: "foo"},
		{name: "four bytes", stdin: "foob"},
		{name: "five bytes", stdin: "fooba"},
		{name: "six bytes", stdin: "foobar"},
		{name: "high bytes", stdin: "\xff\xfe\xfd\x80\x7f\x00"},
		{name: "every byte value", args: []string{f.allByte}},
		{name: "unwrapped", args: []string{"-w0", f.allByte}},
		{name: "one byte short of a block", args: []string{f.short}},
		{name: "exactly a block", args: []string{f.exact}},
		{name: "one byte past a block", args: []string{f.over}},
		{name: "empty file", args: []string{f.empty}},

		// Decoding: the five data-character counts a group may carry,
		// and the three it may not.
		{name: "decode", args: []string{"-d"}, stdin: "MZXW6YTB"},
		{name: "decode empty", args: []string{"-d"}, stdin: ""},
		{name: "decode two data characters", args: []string{"-d"}, stdin: "MZ======"},
		{name: "decode four data characters", args: []string{"-d"}, stdin: "MZXQ===="},
		{name: "decode five data characters", args: []string{"-d"}, stdin: "MZXW6==="},
		{name: "decode seven data characters", args: []string{"-d"}, stdin: "MZXW6YQ="},
		{name: "decode one data character", args: []string{"-d"}, stdin: "A======="},
		{name: "decode three data characters", args: []string{"-d"}, stdin: "AAA====="},
		{name: "decode six data characters", args: []string{"-d"}, stdin: "AAAAAA=="},
		{name: "decode all pads", args: []string{"-d"}, stdin: "========"},
		{name: "decode seven pads", args: []string{"-d"}, stdin: "======="},
		{name: "decode nine characters", args: []string{"-d"}, stdin: "AA======="},
		{name: "decode a trailing data character", args: []string{"-d"}, stdin: "AA======A"},
		{name: "decode pad then data", args: []string{"-d"}, stdin: "AA=A===="},
		{name: "decode two groups", args: []string{"-d"}, stdin: "AA======AA======"},
		{name: "decode unpadded", args: []string{"-d"}, stdin: "MZXW6"},
		// A short final group writes nothing here, unlike base64's.
		{name: "decode one character", args: []string{"-d"}, stdin: "A"},
		{name: "decode two characters", args: []string{"-d"}, stdin: "AA"},
		{name: "decode four characters", args: []string{"-d"}, stdin: "AAAA"},
		{name: "decode seven characters", args: []string{"-d"}, stdin: "AAAAAAA"},
		{name: "decode eight characters", args: []string{"-d"}, stdin: "AAAAAAAA"},
		{name: "decode the whole alphabet", args: []string{"-d"}, stdin: "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"},
		{name: "decode is uppercase only", args: []string{"-d"}, stdin: "mzxw6ytb"},
		{name: "decode is uppercase only with ignore", args: []string{"-d", "-i"}, stdin: "mzxw6ytb"},

		// The filter.
		{name: "decode with a newline", args: []string{"-d"}, stdin: "MZXW\n6YTB"},
		{name: "decode with a trailing newline", args: []string{"-d"}, stdin: "MZXW6YTB\n"},
		{name: "decode with a space", args: []string{"-d"}, stdin: "MZXW 6YTB"},
		{name: "decode with a space ignored", args: []string{"-d", "-i"}, stdin: "MZXW 6YTB"},
		{name: "decode with an embedded pad", args: []string{"-d"}, stdin: "MZXW=6YTB"},
		{name: "decode with an embedded pad and ignore", args: []string{"-d", "-i"}, stdin: "MZXW=6YTB"},
		{name: "decode with a high byte ignored", args: []string{"-d", "-i"}, stdin: "MZXW\xff6YTB"},
		{name: "clustered short options", args: []string{"-di"}, stdin: "MZXW 6YTB"},
		{name: "wrap is ignored on decode", args: []string{"-d", "-w2"}, stdin: "MZXW6YTB"},

		// Across the read block.
		{name: "decode a block", args: []string{"-d", f.b32Exact}},
		{name: "decode a block with a bad tail", args: []string{"-d", f.b32Bad}},
		{name: "decode from a file", args: []string{"-d", f.b32Hello}},

		// Operands and option faults, the two that differ from base64's
		// only by the utility's name in the message.
		{name: "dash is stdin", args: []string{"-"}, stdin: "hi"},
		{name: "file operand", args: []string{f.hello}},
		{name: "two operands", args: []string{f.hello, "extra"}},
		{name: "missing file", args: []string{f.nosuch}},
		{name: "empty operand", args: []string{""}},
		{name: "directory operand", args: []string{f.subdir}},
		{name: "name that is not valid UTF-8", args: []string{f.nonUTF}},
		{name: "invalid short option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo=bar"}},
		{name: "wrap without its value", args: []string{"--wrap"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "posix stops at the operand", args: []string{f.hello, "-w0"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "exactly one line", stdin: strings.Repeat("x", 95)},
		{name: "one byte past a line", stdin: strings.Repeat("x", 96)},

		// Write failures.
		{name: "stdout closed", stdin: "hello", stdout: stdoutClosed},
		{name: "stdout full", stdin: "hello", stdout: stdoutFull},
		{name: "stdout closed with nothing to write", stdin: "", stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{f.short}, stdout: stdoutFull},
		{name: "stdout full with a decode fault", args: []string{"-d"}, stdin: "MZXW6", stdout: stdoutFull},
	}
	cases = append(cases, wrapCases("", nil)...)
	return cases
}

func TestBase32Parity(t *testing.T) {
	requireParity(t, "base32", base32Cases(t))
}

func TestBase32HelpVersion(t *testing.T) {
	requireHelp(t, "base32", []string{"--help"}, 0)
	requireHelp(t, "base32", []string{"--he"}, 0)
	requireVersion(t, "base32", []string{"--version"}, 0)
	requireVersion(t, "base32", []string{"--vers"}, 0)
}
