package coreutils

import (
	"encoding/base32"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The corpus for the three encoding utilities — `basenc` here,
// `base64` and `base32` in their own files, all three over the one
// codec in `coreutils/lib/base.fern`.
//
// What the cases are chosen to pin, because none of it is guessable
// from the manual:
//
//   - Decoding ignores newlines and nothing else. `-i` widens that to
//     every byte outside the alphabet — except `=`, which survives the
//     filter for EVERY encoding and then fails as an invalid character
//     in the ones that do not pad.
//   - A group is validated left to right and the bytes it completed
//     before the fault are written anyway: `AA=A` writes one byte and
//     exits 1.
//   - A short FINAL group is an error everywhere, but base64 alone
//     writes the bytes it holds first. `base64 -d` of `aGVsbG8` writes
//     `hello` and fails; `base32 -d` of `MZXW6` writes nothing.
//   - `-w` is a signed decimal with glibc's blanks and sign. A negative
//     is `invalid wrap size` (with no `Try …` line), but a magnitude
//     past INTMAX_MAX is accepted and turns wrapping OFF, so
//     `-w 9223372036854775807` and `-w 9223372036854775808` differ by
//     the trailing newline.
//   - Encoding reads 30 KiB blocks, and z85 makes that observable:
//     `basenc --z85` of 30721 bytes writes the first 30720 bytes'
//     worth before `length must be multiple of 4`.

// baseFile writes `content` under `dir` as `name` and returns its path.
func baseFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// baseFixture is the tree the three corpora share: a few short inputs,
// one with every byte value, one either side of the 30 KiB read block,
// and the names that are awkward to report.
type baseFixture struct {
	dir     string
	empty   string
	hello   string
	allByte string // 0x00..0xff
	// block-1 / block / block+1 bytes, where block is the 30 KiB read.
	short  string
	exact  string
	over   string
	nonUTF string
	subdir string
	nosuch string
	// Encoded streams longer than one read block, so a decode carries a
	// group across it, plus the same stream with one stray character on
	// the end so the fault lands on the tail.
	b64Hello string
	b64Exact string
	b64Bad   string
	b32Hello string
	b32Exact string
	b32Bad   string
}

// wrapped is `s` broken into 76-column lines with a trailing newline,
// which is what every one of the three writes by default.
func wrapped(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteByte('\n')
		s = s[76:]
	}
	b.WriteString(s)
	b.WriteByte('\n')
	return b.String()
}

func baseFixtureFor(t *testing.T) baseFixture {
	t.Helper()
	dir := t.TempDir()
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	// The read block coreutils/lib/base.fern uses, which is GNU's.
	const block = 30720
	fill := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*7 + i/251)
		}
		return b
	}
	f := baseFixture{
		dir:     dir,
		empty:   baseFile(t, dir, "empty", nil),
		hello:   baseFile(t, dir, "hello", []byte("hello")),
		allByte: baseFile(t, dir, "allbytes", all),
		short:   baseFile(t, dir, "short", fill(block-1)),
		exact:   baseFile(t, dir, "exact", fill(block)),
		over:    baseFile(t, dir, "over", fill(block+1)),
		nonUTF:  baseFile(t, dir, "na\xffme", []byte("x")),
		subdir:  filepath.Join(dir, "d"),
		nosuch:  filepath.Join(dir, "nosuch"),
	}
	blockBytes := fill(block)
	b64 := wrapped(base64.StdEncoding.EncodeToString(blockBytes))
	b32 := wrapped(base32.StdEncoding.EncodeToString(blockBytes))
	f.b64Hello = baseFile(t, dir, "hello.b64", []byte("aGVsbG8=\n"))
	f.b64Exact = baseFile(t, dir, "block.b64", []byte(b64))
	f.b64Bad = baseFile(t, dir, "block.bad.b64", []byte(b64+"A"))
	f.b32Hello = baseFile(t, dir, "hello.b32", []byte("NBSWY3DP\n"))
	f.b32Exact = baseFile(t, dir, "block.b32", []byte(b32))
	f.b32Bad = baseFile(t, dir, "block.bad.b32", []byte(b32+"A"))
	if err := os.Mkdir(f.subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// wrapCases are the `-w` values, which every one of the three parses
// the same way. `name` prefixes each case so the three corpora do not
// collide in the test log.
func wrapCases(prefix string, lead []string) []invocation {
	args := func(rest ...string) []string {
		return append(append([]string{}, lead...), rest...)
	}
	return []invocation{
		{name: prefix + "wrap zero", args: args("-w0"), stdin: "hello world"},
		{name: prefix + "wrap one", args: args("-w", "1"), stdin: "hello world"},
		{name: prefix + "wrap two", args: args("-w2"), stdin: "hello world"},
		{name: prefix + "wrap four", args: args("-w4"), stdin: "hello world"},
		{name: prefix + "wrap five", args: args("-w5"), stdin: "hello world"},
		{name: prefix + "wrap seven", args: args("-w7"), stdin: "hello world"},
		{name: prefix + "wrap long", args: args("--wrap=13"), stdin: "hello world, a longer input"},
		{name: prefix + "wrap long spaced", args: args("--wrap", "13"), stdin: "hello world, a longer input"},
		{name: prefix + "wrap larger than the output", args: args("-w1000"), stdin: "hello"},
		{name: prefix + "last wrap wins", args: args("-w2", "-w8"), stdin: "hello world"},
		// The column the output ends exactly on: no second newline.
		{name: prefix + "wrap lands on the last column", args: args("-w8"), stdin: "hello"},
		{name: prefix + "wrap one past the last column", args: args("-w9"), stdin: "hello"},
		{name: prefix + "wrap one before", args: args("-w7"), stdin: "hello"},
		// strtoimax's own rules: leading blanks, a sign, decimal only.
		{name: prefix + "wrap leading blank", args: args("-w", " 5"), stdin: "hello"},
		{name: prefix + "wrap leading tab", args: args("-w", "\t5"), stdin: "hello"},
		{name: prefix + "wrap plus", args: args("-w", "+5"), stdin: "hello"},
		{name: prefix + "wrap leading zeros", args: args("-w", "010"), stdin: "hello"},
		{name: prefix + "wrap minus zero", args: args("-w", "-0"), stdin: "hello"},
		{name: prefix + "wrap minus zeros", args: args("-w", "-0000"), stdin: "hello"},
		{name: prefix + "wrap plus zero", args: args("-w", "+0"), stdin: "hello"},
		{name: prefix + "wrap intmax", args: args("-w", "9223372036854775807"), stdin: "hello"},
		// Past INTMAX_MAX: accepted, and wrapping is off — the newline
		// the case above ends with is gone.
		{name: prefix + "wrap intmax plus one", args: args("-w", "9223372036854775808"), stdin: "hello"},
		{name: prefix + "wrap uintmax", args: args("-w", "18446744073709551615"), stdin: "hello"},
		{name: prefix + "wrap twenty nines", args: args("-w", "99999999999999999999"), stdin: "hello"},
		{name: prefix + "wrap plus overflow", args: args("-w", "+99999999999999999999"), stdin: "hello"},
		{name: prefix + "wrap negative overflow", args: args("-w", "-99999999999999999999")},
		{name: prefix + "wrap intmin", args: args("-w", "-9223372036854775808")},
		{name: prefix + "wrap negative", args: args("-w", "-1")},
		{name: prefix + "wrap empty", args: args("-w", "")},
		{name: prefix + "wrap not a number", args: args("-w", "abc")},
		{name: prefix + "wrap trailing letter", args: args("-w", "5x")},
		{name: prefix + "wrap hex", args: args("-w", "0x10")},
		{name: prefix + "wrap exponent", args: args("-w", "1e3")},
		{name: prefix + "wrap trailing blank", args: args("-w", "5 ")},
		{name: prefix + "wrap blanks both sides", args: args("-w", "  12  ")},
		{name: prefix + "wrap newline in the value", args: args("-w", "1\n2")},
	}
}

// basencCases is basenc(1)'s corpus.
func basencCases(t *testing.T) []invocation {
	f := baseFixtureFor(t)
	encodings := []string{"--base64", "--base64url", "--base32", "--base32hex", "--base16", "--base2msbf", "--base2lsbf"}

	cases := []invocation{
		// The encoding is mandatory, and its absence is reported before
		// anything about the operands.
		{name: "no encoding", args: nil},
		{name: "no encoding with an operand", args: []string{f.hello}},
		{name: "no encoding with two operands", args: []string{"a", "b"}},
		{name: "no encoding with a missing file", args: []string{f.nosuch}},
		{name: "no encoding after a terminator", args: []string{"--"}},
		{name: "no encoding with decode", args: []string{"-d"}},
		{name: "no encoding with ignore", args: []string{"-i"}},
		// A bad wrap size is met during the scan, so it beats the
		// missing encoding.
		{name: "bad wrap beats the missing encoding", args: []string{"-w", "abc"}},
		{name: "bad wrap with an encoding", args: []string{"--base64", "-w", "abc"}},

		// The ambiguity list, in declaration order.
		{name: "base prefix is ambiguous", args: []string{"--base"}},
		{name: "b prefix is ambiguous", args: []string{"--b"}},
		{name: "base3 is ambiguous", args: []string{"--base3"}},
		{name: "base2 is ambiguous", args: []string{"--base2"}},
		{name: "base32 is exact despite base32hex", args: []string{"--base32"}, stdin: "hi"},
		{name: "base64 is exact despite base64url", args: []string{"--base64"}, stdin: "hi"},
		{name: "base1 is unique", args: []string{"--base1"}, stdin: "hi"},
		{name: "base64u is unique", args: []string{"--base64u"}, stdin: "hi"},
		{name: "base32h is unique", args: []string{"--base32h"}, stdin: "hi"},
		{name: "base2m is unique", args: []string{"--base2m"}, stdin: "hi"},
		{name: "base2l is unique", args: []string{"--base2l"}, stdin: "hi"},
		{name: "z is unique", args: []string{"--z"}, stdin: "hell"},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unrecognized long option", args: []string{"--nope"}},
		{name: "unrecognized long option with a value", args: []string{"--nope=1"}},
		{name: "invalid short option", args: []string{"-x"}},
		{name: "encoding does not take a value", args: []string{"--base64=1"}},
		{name: "decode does not take a value", args: []string{"--decode=1"}},
		{name: "wrap without its value", args: []string{"--base64", "--wrap"}},
		{name: "wrap short without its value", args: []string{"--base64", "-w"}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},

		// The last encoding on the line decides.
		{name: "last encoding wins", args: []string{"--base64", "--base32"}, stdin: "hello"},
		{name: "last encoding wins the other way", args: []string{"--base32", "--base64"}, stdin: "hello"},
		{name: "encoding repeated", args: []string{"--base64", "--base64"}, stdin: "hello"},
		{name: "z85 then base64", args: []string{"--z85", "--base64"}, stdin: "hello"},
		{name: "base64 then z85", args: []string{"--base64", "--z85"}, stdin: "hell"},

		// Operands.
		{name: "dash is stdin", args: []string{"--base64", "-"}, stdin: "hi"},
		{name: "file operand", args: []string{"--base64", f.hello}},
		{name: "two operands", args: []string{"--base64", "a", "b"}},
		{name: "missing file", args: []string{"--base64", f.nosuch}},
		{name: "empty operand", args: []string{"--base64", ""}},
		{name: "directory operand", args: []string{"--base64", f.subdir}},
		{name: "name that is not valid UTF-8", args: []string{"--base64", f.nonUTF}},
		{name: "missing name that is not valid UTF-8", args: []string{"--base64", f.nosuch + "\xff"}},
		{name: "terminator before the operand", args: []string{"--base64", "--", f.hello}},
		{name: "posix stops at the operand", args: []string{"--base64", f.hello, "-w0"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "option after the operand permutes", args: []string{"--base64", f.hello, "-w0"}},

		// z85: the block requirement on both sides, and the one
		// diagnostic that is not `invalid input`.
		{name: "z85 four bytes", args: []string{"--z85"}, stdin: "hell"},
		{name: "z85 eight bytes", args: []string{"--z85"}, stdin: "hellhell"},
		{name: "z85 empty", args: []string{"--z85"}, stdin: ""},
		{name: "z85 one byte", args: []string{"--z85"}, stdin: "a"},
		{name: "z85 three bytes", args: []string{"--z85"}, stdin: "abc"},
		{name: "z85 five bytes", args: []string{"--z85"}, stdin: "abcde"},
		{name: "z85 high bytes", args: []string{"--z85"}, stdin: "\xff\xff\xff\xff"},
		{name: "z85 zero bytes", args: []string{"--z85"}, stdin: "\x00\x00\x00\x00"},
		{name: "z85 all byte values", args: []string{"--z85", f.allByte}},
		{name: "z85 decode", args: []string{"--z85", "-d"}, stdin: "xK#0@"},
		{name: "z85 decode two groups", args: []string{"--z85", "-d"}, stdin: "xK#0@xK#0@"},
		{name: "z85 decode empty", args: []string{"--z85", "-d"}, stdin: ""},
		{name: "z85 decode four characters", args: []string{"--z85", "-d"}, stdin: "xK#0"},
		{name: "z85 decode six characters", args: []string{"--z85", "-d"}, stdin: "xK#0@x"},
		{name: "z85 decode the maximum", args: []string{"--z85", "-d"}, stdin: "%nSc0"},
		// One past 2**32-1: every character is in the alphabet and the
		// group is still invalid.
		{name: "z85 decode overflows", args: []string{"--z85", "-d"}, stdin: "%nSc1"},
		{name: "z85 decode overflows after a group", args: []string{"--z85", "-d"}, stdin: "%nSc0#####"},
		{name: "z85 decode with a newline", args: []string{"--z85", "-d"}, stdin: "xK#0@\nxK#0@"},
		{name: "z85 decode equals is a digit", args: []string{"--z85", "-d"}, stdin: "====="},
		{name: "z85 decode garbage", args: []string{"--z85", "-d"}, stdin: "xK#0@ xK#0@"},
		{name: "z85 decode garbage ignored", args: []string{"--z85", "-d", "-i"}, stdin: "xK#0@ xK#0@"},
		// The read block is 60 KiB, so the whole blocks before the short
		// one are already on stdout when the length error fires.
		{name: "z85 one byte past a block", args: []string{"--z85", "-w0", f.over}},
		{name: "z85 one byte short of a block", args: []string{"--z85", "-w0", f.short}},
		{name: "z85 exactly a block", args: []string{"--z85", "-w0", f.exact}},

		// base16 and base2: no padding, so `=` is simply invalid.
		{name: "base16 empty", args: []string{"--base16"}, stdin: ""},
		{name: "base16 one byte", args: []string{"--base16"}, stdin: "\xab"},
		{name: "base16 all byte values", args: []string{"--base16", "-w0", f.allByte}},
		{name: "base16 decode", args: []string{"--base16", "-d"}, stdin: "48454C4C4F"},
		{name: "base16 decode is uppercase only", args: []string{"--base16", "-d"}, stdin: "48454c4c4f"},
		{name: "base16 decode odd length", args: []string{"--base16", "-d"}, stdin: "4A4"},
		{name: "base16 decode one character", args: []string{"--base16", "-d"}, stdin: "4"},
		{name: "base16 decode equals", args: []string{"--base16", "-d"}, stdin: "4A="},
		{name: "base16 decode equals is not ignored", args: []string{"--base16", "-d", "-i"}, stdin: "4A=4B"},
		{name: "base16 decode garbage ignored", args: []string{"--base16", "-d", "-i"}, stdin: "4A 4B"},
		{name: "base16 decode newline", args: []string{"--base16", "-d"}, stdin: "4A\n4B"},
		{name: "base2msbf one byte", args: []string{"--base2msbf"}, stdin: "A"},
		{name: "base2lsbf one byte", args: []string{"--base2lsbf"}, stdin: "A"},
		{name: "base2msbf two bytes", args: []string{"--base2msbf"}, stdin: "AB"},
		{name: "base2lsbf two bytes", args: []string{"--base2lsbf"}, stdin: "AB"},
		{name: "base2msbf all byte values", args: []string{"--base2msbf", "-w0", f.allByte}},
		{name: "base2lsbf all byte values", args: []string{"--base2lsbf", "-w0", f.allByte}},
		{name: "base2msbf decode", args: []string{"--base2msbf", "-d"}, stdin: "0100000101000010"},
		{name: "base2lsbf decode", args: []string{"--base2lsbf", "-d"}, stdin: "0100000101000010"},
		{name: "base2msbf decode short", args: []string{"--base2msbf", "-d"}, stdin: "0100000"},
		{name: "base2lsbf decode short", args: []string{"--base2lsbf", "-d"}, stdin: "0100000"},
		{name: "base2msbf decode nine bits", args: []string{"--base2msbf", "-d"}, stdin: "010000011"},
		{name: "base2lsbf decode nine bits", args: []string{"--base2lsbf", "-d"}, stdin: "010000011"},
		{name: "base2msbf decode a two", args: []string{"--base2msbf", "-d"}, stdin: "2"},
		{name: "base2msbf decode equals", args: []string{"--base2msbf", "-d"}, stdin: "01000001="},
		{name: "base2msbf decode garbage ignored", args: []string{"--base2msbf", "-d", "-i"}, stdin: "0100 0001"},

		// base32hex: base32's arithmetic on a different alphabet, so
		// the padding rules are the same and the characters are not.
		{name: "base32hex encode", args: []string{"--base32hex"}, stdin: "hello"},
		{name: "base32hex encode one byte", args: []string{"--base32hex"}, stdin: "f"},
		{name: "base32hex all byte values", args: []string{"--base32hex", "-w0", f.allByte}},
		{name: "base32hex decode", args: []string{"--base32hex", "-d"}, stdin: "CPNMUOJ1"},
		{name: "base32hex decode padded", args: []string{"--base32hex", "-d"}, stdin: "CO======"},
		{name: "base32hex decode is uppercase only", args: []string{"--base32hex", "-d"}, stdin: "cpnmuoj1"},
		{name: "base32hex decode a bad pad length", args: []string{"--base32hex", "-d"}, stdin: "AAAAAA=="},
		{name: "base32hex decode split by a newline", args: []string{"--base32hex", "-d"}, stdin: "CO==\n===="},

		// base64url: the two characters that differ, and the padding
		// base64url keeps (unlike most URL-token encoders).
		{name: "base64url encode", args: []string{"--base64url"}, stdin: "hello"},
		{name: "base64url all byte values", args: []string{"--base64url", "-w0", f.allByte}},
		{name: "base64url rejects plus", args: []string{"--base64url", "-d"}, stdin: "a+VsbG8="},
		{name: "base64url decode", args: []string{"--base64url", "-d"}, stdin: "aGVsbG8="},
		{name: "base64 rejects the url alphabet", args: []string{"--base64", "-d"}, stdin: "a-VsbG8="},

		// Write failures, on and over the stdio block.
		{name: "stdout closed", args: []string{"--base64"}, stdin: "hello", stdout: stdoutClosed},
		{name: "stdout full", args: []string{"--base64"}, stdin: "hello", stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{"--base64"}, stdin: "", stdout: stdoutClosed},
		{name: "stdout full with nothing to write", args: []string{"--base64"}, stdin: "", stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"--base64", f.short}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"--base64", f.short}, stdout: stdoutFull},
		{name: "stdout closed with a missing file", args: []string{"--base64", f.nosuch}, stdout: stdoutClosed},
		{name: "stdout closed with a directory", args: []string{"--base64", f.subdir}, stdout: stdoutClosed},
		{name: "stdout closed with a bad wrap", args: []string{"--base64", "-w", "x"}, stdout: stdoutClosed},
		// A short output that then fails to decode: the diagnostic
		// flushes stdout, so the failed flush is reported second.
		{name: "stdout full with a decode fault", args: []string{"--base64", "-d"}, stdin: "aGk", stdout: stdoutFull},
		{name: "stdout closed with a decode fault", args: []string{"--base64", "-d"}, stdin: "aGk", stdout: stdoutClosed},
		{name: "stdout full with a decode fault and no output", args: []string{"--base64", "-d"}, stdin: "A", stdout: stdoutFull},
		{name: "stdout closed with a decode fault and no output", args: []string{"--base64", "-d"}, stdin: "A", stdout: stdoutClosed},
		{name: "stdout full with a z85 length fault", args: []string{"--z85"}, stdin: "abc", stdout: stdoutFull},
	}

	// Every encoding over the same inputs, so a block boundary, an
	// empty input and a partial final block are covered for each.
	for _, e := range encodings {
		short := strings.TrimPrefix(e, "--")
		cases = append(cases,
			invocation{name: short + " empty", args: []string{e}, stdin: ""},
			invocation{name: short + " one byte", args: []string{e}, stdin: "a"},
			invocation{name: short + " two bytes", args: []string{e}, stdin: "ab"},
			invocation{name: short + " three bytes", args: []string{e}, stdin: "abc"},
			invocation{name: short + " four bytes", args: []string{e}, stdin: "abcd"},
			invocation{name: short + " five bytes", args: []string{e}, stdin: "abcde"},
			invocation{name: short + " six bytes", args: []string{e}, stdin: "abcdef"},
			invocation{name: short + " seven bytes", args: []string{e}, stdin: "abcdefg"},
			invocation{name: short + " NUL bytes", args: []string{e}, stdin: "\x00\x00\x00"},
			invocation{name: short + " every byte value", args: []string{e, "-w0", f.allByte}},
			invocation{name: short + " one byte past a block", args: []string{e, "-w0", f.over}},
			invocation{name: short + " exactly a block", args: []string{e, "-w0", f.exact}},
			invocation{name: short + " a bare newline decodes to nothing", args: []string{e, "-d"}, stdin: "\n"},
			invocation{name: short + " an empty input decodes to nothing", args: []string{e, "-d"}, stdin: ""},
		)
	}
	cases = append(cases, wrapCases("basenc ", []string{"--base64"})...)
	cases = append(cases, wrapCases("basenc base32 ", []string{"--base32"})...)
	return cases
}

func TestBasencParity(t *testing.T) {
	requireParity(t, "basenc", basencCases(t))
}

func TestBasencHelpVersion(t *testing.T) {
	requireHelp(t, "basenc", []string{"--help"}, 0)
	requireHelp(t, "basenc", []string{"--hel"}, 0)
	requireHelp(t, "basenc", []string{"--help", "ignored"}, 0)
	requireVersion(t, "basenc", []string{"--version"}, 0)
	requireVersion(t, "basenc", []string{"--vers"}, 0)
	requireVersion(t, "basenc", []string{"--v"}, 0)
}
