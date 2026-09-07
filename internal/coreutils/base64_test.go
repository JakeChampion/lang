package coreutils

import (
	"strings"
	"testing"
)

// base64Cases is base64(1)'s corpus.
//
// The decoder is where the surprises are, and most of them are shapes a
// round trip never produces: `AA==AA==` is two groups and decodes to two
// bytes, `AB==` is accepted with its ignored low bits, and a group that
// goes wrong still writes the bytes it had already completed. base64 is
// also the one encoding whose SHORT final group writes anything at all
// before failing — base32's writes nothing, which base32_test.go pins.
func base64Cases(t *testing.T) []invocation {
	f := baseFixtureFor(t)

	cases := []invocation{
		// Encoding, one case per residue and around the block.
		{name: "empty", stdin: ""},
		{name: "one byte", stdin: "a"},
		{name: "two bytes", stdin: "ab"},
		{name: "three bytes", stdin: "abc"},
		{name: "four bytes", stdin: "abcd"},
		{name: "five bytes", stdin: "abcde"},
		{name: "high bytes", stdin: "\xff\xfe\xfd\x80\x7f"},
		{name: "NUL bytes", stdin: "\x00\x00\x00\x00"},
		{name: "every byte value", args: []string{f.allByte}},
		{name: "exactly one line", stdin: strings.Repeat("x", 57)},
		{name: "one byte past a line", stdin: strings.Repeat("x", 58)},
		{name: "one byte short of a line", stdin: strings.Repeat("x", 56)},
		{name: "two lines", stdin: strings.Repeat("x", 114)},
		{name: "one byte short of a block", args: []string{f.short}},
		{name: "exactly a block", args: []string{f.exact}},
		{name: "one byte past a block", args: []string{f.over}},
		{name: "a block unwrapped", args: []string{"-w0", f.over}},
		{name: "empty file", args: []string{f.empty}},

		// Decoding: padding, and the counts that are not encodable.
		{name: "decode", args: []string{"-d"}, stdin: "aGVsbG8="},
		{name: "decode empty", args: []string{"-d"}, stdin: ""},
		{name: "decode unpadded", args: []string{"-d"}, stdin: "aGVsbG8"},
		{name: "decode one character", args: []string{"-d"}, stdin: "A"},
		{name: "decode two characters", args: []string{"-d"}, stdin: "AA"},
		{name: "decode three characters", args: []string{"-d"}, stdin: "AAA"},
		{name: "decode four characters", args: []string{"-d"}, stdin: "AAAA"},
		{name: "decode five characters", args: []string{"-d"}, stdin: "AAAAA"},
		{name: "decode one pad", args: []string{"-d"}, stdin: "AAA="},
		{name: "decode two pads", args: []string{"-d"}, stdin: "AA=="},
		{name: "decode three pads", args: []string{"-d"}, stdin: "A==="},
		{name: "decode four pads", args: []string{"-d"}, stdin: "===="},
		{name: "decode one pad alone", args: []string{"-d"}, stdin: "="},
		{name: "decode two pads alone", args: []string{"-d"}, stdin: "=="},
		{name: "decode three pads alone", args: []string{"-d"}, stdin: "==="},
		{name: "decode short pad", args: []string{"-d"}, stdin: "AA="},
		{name: "decode pad then data", args: []string{"-d"}, stdin: "AA=A"},
		{name: "decode pad in the second position", args: []string{"-d"}, stdin: "A=AA"},
		{name: "decode pad then a group", args: []string{"-d"}, stdin: "AA==AA=="},
		{name: "decode a fifth pad", args: []string{"-d"}, stdin: "aGVsbG8=="},
		{name: "decode data after a full group", args: []string{"-d"}, stdin: "AAAA="},
		// The bits a padded group cannot represent are ignored rather
		// than rejected, so these are all valid.
		{name: "decode non-canonical two-pad bits", args: []string{"-d"}, stdin: "AB=="},
		{name: "decode non-canonical two-pad bits again", args: []string{"-d"}, stdin: "Ab=="},
		{name: "decode non-canonical one-pad bits", args: []string{"-d"}, stdin: "AAB="},
		{name: "decode the whole alphabet", args: []string{"-d"}, stdin: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"},

		// What a decoder ignores: newlines always, everything else only
		// under -i, and `=` under neither.
		{name: "decode with a newline", args: []string{"-d"}, stdin: "aGVs\nbG8="},
		{name: "decode with a leading newline", args: []string{"-d"}, stdin: "\naGVsbG8="},
		{name: "decode with a trailing newline", args: []string{"-d"}, stdin: "aGVsbG8=\n"},
		{name: "decode with only newlines", args: []string{"-d"}, stdin: "\n\n\n"},
		{name: "decode with a carriage return", args: []string{"-d"}, stdin: "aGVs\rbG8="},
		{name: "decode with a carriage return ignored", args: []string{"-d", "-i"}, stdin: "aGVs\rbG8="},
		{name: "decode with a tab", args: []string{"-d"}, stdin: "aGVs\tbG8="},
		{name: "decode with a tab ignored", args: []string{"-d", "-i"}, stdin: "aGVs\tbG8="},
		{name: "decode with a space", args: []string{"-d"}, stdin: "aGVs bG8="},
		{name: "decode with a space ignored", args: []string{"-d", "-i"}, stdin: "aGVs bG8="},
		{name: "decode with a NUL", args: []string{"-d"}, stdin: "aGVs\x00bG8="},
		{name: "decode with a NUL ignored", args: []string{"-d", "-i"}, stdin: "aGVs\x00bG8="},
		{name: "decode with a high byte ignored", args: []string{"-d", "-i"}, stdin: "aGVs\xffbG8="},
		{name: "decode with a dash ignored", args: []string{"-d", "-i"}, stdin: "aGVs-bG8="},
		{name: "decode with an embedded pad", args: []string{"-d"}, stdin: "aGVs=bG8="},
		{name: "decode with an embedded pad and ignore", args: []string{"-d", "-i"}, stdin: "aGVs=bG8="},
		{name: "ignore without decode", args: []string{"-i"}, stdin: "hello"},
		{name: "decode after ignore", args: []string{"-i", "-d"}, stdin: "aGVs bG8="},
		{name: "clustered short options", args: []string{"-di"}, stdin: "aGVs bG8="},
		{name: "clustered with a wrap", args: []string{"-diw0"}, stdin: "aGVs bG8="},
		{name: "wrap is ignored on decode", args: []string{"-d", "-w2"}, stdin: "aGVsbG8="},

		// Decoding across the read block: the group carries over, so the
		// fault lands on the tail rather than on a block boundary.
		{name: "decode a block", args: []string{"-d", f.b64Exact}},
		{name: "decode a block with a bad tail", args: []string{"-d", f.b64Bad}},

		// Round trips through the file operand.
		{name: "decode from a file", args: []string{"-d", f.b64Hello}},
		{name: "decode from a directory", args: []string{"-d", f.subdir}},
		{name: "decode a missing file", args: []string{"-d", f.nosuch}},

		// Operands.
		{name: "dash is stdin", args: []string{"-"}, stdin: "hi"},
		{name: "dash twice is an extra operand", args: []string{"-", "-"}},
		{name: "file operand", args: []string{f.hello}},
		{name: "two operands", args: []string{f.hello, "extra"}},
		{name: "missing file", args: []string{f.nosuch}},
		{name: "empty operand", args: []string{""}},
		{name: "directory operand", args: []string{f.subdir}},
		{name: "name that is not valid UTF-8", args: []string{f.nonUTF}},
		{name: "terminator", args: []string{"--", f.hello}},
		{name: "terminator makes a dash an operand", args: []string{"--", "-"}, stdin: "hi"},
		{name: "operand then option permutes", args: []string{f.hello, "-w0"}},
		{name: "posix stops at the operand", args: []string{f.hello, "-w0"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-w0", f.hello}, env: []string{"POSIXLY_CORRECT=1"}},

		// Option faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid option in a cluster", args: []string{"-dx"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "decode does not take a value", args: []string{"--decode=1"}},
		{name: "ignore does not take a value", args: []string{"--ignore-garbage=1"}},
		{name: "wrap without its value", args: []string{"--wrap"}},
		{name: "wrap prefix without its value", args: []string{"--w"}},
		{name: "short wrap without its value", args: []string{"-w"}},
		{name: "wrap takes the next token whatever it is", args: []string{"-w", "--", "x"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "decode by prefix", args: []string{"--d"}, stdin: "aGk="},
		{name: "ignore by prefix", args: []string{"--i", "--d"}, stdin: "aG k="},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},

		// Write failures.
		{name: "stdout closed", stdin: "hello", stdout: stdoutClosed},
		{name: "stdout full", stdin: "hello", stdout: stdoutFull},
		{name: "stdout closed with nothing to write", stdin: "", stdout: stdoutClosed},
		{name: "stdout closed with a large output", args: []string{f.short}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{f.short}, stdout: stdoutFull},
		{name: "stdout closed on decode", args: []string{"-d"}, stdin: "aGk=", stdout: stdoutClosed},
		{name: "stdout full on decode", args: []string{"-d"}, stdin: "aGk=", stdout: stdoutFull},
		{name: "stdout closed with a decode fault", args: []string{"-d"}, stdin: "aGk", stdout: stdoutClosed},
		{name: "stdout full with a decode fault", args: []string{"-d"}, stdin: "aGk", stdout: stdoutFull},
		{name: "stdout closed with a missing file", args: []string{f.nosuch}, stdout: stdoutClosed},
	}
	cases = append(cases, wrapCases("", nil)...)
	return cases
}

func TestBase64Parity(t *testing.T) {
	requireParity(t, "base64", base64Cases(t))
}

func TestBase64HelpVersion(t *testing.T) {
	requireHelp(t, "base64", []string{"--help"}, 0)
	requireHelp(t, "base64", []string{"--he"}, 0)
	requireHelp(t, "base64", []string{"--help", "ignored"}, 0)
	requireVersion(t, "base64", []string{"--version"}, 0)
	requireVersion(t, "base64", []string{"--vers"}, 0)
	requireVersion(t, "base64", []string{"--v"}, 0)
}
