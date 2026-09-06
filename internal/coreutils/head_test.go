package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headFile writes `content` under `dir` as `name` and returns its path.
func headFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// headCases is head(1)'s corpus.
//
// The counts are the interesting half: `-n` / `-c` take glibc's
// strtoumax plus gnulib's multiplier suffixes, a leading minus turns the
// count into an elision, and the ceiling on an elided BYTE count is
// off_t rather than uintmax_t — a difference visible only at
// 2**63 - 1. The other half is the obsolete `-NUM[bkmclqvz]` form, which
// is argv[1] or nothing: every other digit in an option position is
// `invalid trailing option`.
func headCases(t *testing.T) []invocation {
	dir := t.TempDir()
	// Twenty short lines: long enough for every count in the corpus,
	// short enough to diff by eye when one fails.
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	s20 := headFile(t, dir, "s20", b.String())
	// 400 lines / 1492 bytes: bigger than the 512- and 1024-byte
	// obsolete multipliers, so `-1b` and `-1k` are distinguishable.
	b.Reset()
	for i := 1; i <= 400; i++ {
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	s400 := headFile(t, dir, "s400", b.String())
	nonl := headFile(t, dir, "nonl", "a\nb\nc")
	empty := headFile(t, dir, "e0", "")
	nul := headFile(t, dir, "nul", "a\x00b\x00c\x00")
	spaced := headFile(t, dir, "f name", "x\n")
	quoted := headFile(t, dir, "f'n", "x\n")
	// A name that is not valid UTF-8: it reaches the header and the
	// diagnostics as bytes, so nothing may re-encode it.
	raw := headFile(t, dir, "na\xffme", "x\n")
	missing := filepath.Join(dir, "nosuch")
	// A directory: open(2) succeeds and read(2) fails EISDIR, which is
	// the only way this corpus reaches a read error.
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Output larger than one stdout block, so a write failure is met
	// while streaming (`error writing 'standard output'`) rather than
	// at the final flush (`write error`).
	big := headFile(t, dir, "big", strings.Repeat("0123456789\n", 5000))

	return []invocation{
		// Defaults and the two counts.
		{name: "default ten lines", args: []string{s20}},
		{name: "n with a space", args: []string{"-n", "3", s20}},
		{name: "n glued", args: []string{"-n3", s20}},
		{name: "long lines", args: []string{"--lines", "3", s20}},
		{name: "long lines glued", args: []string{"--lines=3", s20}},
		{name: "c with a space", args: []string{"-c", "5", s20}},
		{name: "c glued", args: []string{"-c5", s20}},
		{name: "long bytes", args: []string{"--bytes=5", s20}},
		{name: "zero lines", args: []string{"-n", "0", s20}},
		{name: "zero bytes", args: []string{"-c", "0", s20}},
		{name: "plus is accepted", args: []string{"-n", "+3", s20}},
		{name: "leading blank is accepted", args: []string{"-n", " 3", s20}},
		{name: "leading zero is decimal", args: []string{"-n", "010", s20}},
		{name: "count larger than the file", args: []string{"-n", "500", s20}},
		{name: "bytes larger than the file", args: []string{"-c", "500", s20}},
		{name: "last count wins", args: []string{"-n", "2", "-n", "3", s20}},
		{name: "bytes then lines", args: []string{"-c", "3", "-n", "2", s20}},
		{name: "lines then bytes", args: []string{"-n", "2", "-c", "3", s20}},

		// Elision: all but the last N.
		{name: "elide lines", args: []string{"-n", "-3", s20}},
		{name: "elide zero lines", args: []string{"-n", "-0", s20}},
		{name: "elide bytes", args: []string{"-c", "-5", s20}},
		{name: "elide zero bytes", args: []string{"-c", "-0", s20}},
		{name: "elide more lines than there are", args: []string{"-n", "-100", s20}},
		{name: "elide more bytes than there are", args: []string{"-c", "-1000", s20}},
		{name: "elide from an empty file", args: []string{"-n", "-5", empty}},
		{name: "elide bytes from an empty file", args: []string{"-c", "-5", empty}},
		// A final line with no terminator is a line, so it can be the
		// one elided.
		{name: "elide the unterminated tail", args: []string{"-n", "-1", nonl}},
		{name: "elide two with an unterminated tail", args: []string{"-n", "-2", nonl}},
		{name: "elide all with an unterminated tail", args: []string{"-n", "-3", nonl}},
		{name: "elide bytes with an unterminated tail", args: []string{"-c", "-1", nonl}},
		{name: "elide across a large file", args: []string{"-n", "-4", s400}},
		{name: "elide bytes across a large file", args: []string{"-c", "-4", s400}},

		// Multiplier suffixes.
		{name: "b is 512", args: []string{"-c", "1b", s400}},
		{name: "k is 1024", args: []string{"-c", "1k", s400}},
		{name: "K is 1024", args: []string{"-c", "1K", s400}},
		{name: "kB is 1000", args: []string{"-c", "1kB", s400}},
		{name: "KiB is 1024", args: []string{"-c", "1KiB", s400}},
		{name: "MB", args: []string{"-c", "1MB", s400}},
		{name: "M", args: []string{"-c", "1M", s400}},
		{name: "lines take suffixes too", args: []string{"-n", "1b", s20}},
		{name: "B alone is not a suffix", args: []string{"-c", "1B", s20}},
		{name: "Ki without B is not a suffix", args: []string{"-n", "1Ki", s20}},
		{name: "Y overflows", args: []string{"-n", "1Y", s20}},

		// Malformed and out-of-range counts: one line, exit 1, and NO
		// `Try …` line.
		{name: "not a number", args: []string{"-n", "x", s20}},
		{name: "trailing garbage", args: []string{"-n", "1x", s20}},
		{name: "hex is not accepted", args: []string{"-n", "0x10", s20}},
		{name: "exponent is not accepted", args: []string{"-n", "1e3", s20}},
		{name: "trailing blank", args: []string{"-n", "3 ", s20}},
		{name: "blank before the sign", args: []string{"-n", " -3", s20}},
		{name: "blank after the sign", args: []string{"-n", "+ 3", s20}},
		{name: "double sign", args: []string{"-n", "++1", s20}},
		{name: "empty count", args: []string{"-n", "", s20}},
		{name: "empty byte count", args: []string{"-c", "", s20}},
		{name: "bare minus is not a number", args: []string{"-n", "-", s20}},
		{name: "elide garbage", args: []string{"-n", "-3x", s20}},
		{name: "elide with a suffix", args: []string{"-n", "-3b", s20}},
		{name: "far too many lines", args: []string{"-n", "99999999999999999999", s20}},
		{name: "uintmax lines", args: []string{"-n", "18446744073709551615", s20}},
		{name: "past uintmax lines", args: []string{"-n", "18446744073709551616", s20}},
		{name: "uintmax bytes", args: []string{"-c", "18446744073709551615", s20}},
		{name: "past uintmax bytes", args: []string{"-c", "18446744073709551616", s20}},
		{name: "elide uintmax lines", args: []string{"-n", "-18446744073709551615", s20}},
		{name: "elide past uintmax lines", args: []string{"-n", "-18446744073709551616", s20}},
		// The one place the two ceilings differ: an elided byte count is
		// an offset, so off_t bounds it.
		{name: "elide off_t max bytes", args: []string{"-c", "-9223372036854775807", s20}},
		{name: "elide past off_t max bytes", args: []string{"-c", "-9223372036854775808", s20}},
		{name: "elide uintmax bytes", args: []string{"-c", "-18446744073709551615", s20}},
		{name: "past off_t max bytes is fine unelided", args: []string{"-c", "9223372036854775808", s20}},
		{name: "elide off_t max lines", args: []string{"-n", "-9223372036854775808", s20}},

		// Headers.
		{name: "two files get headers", args: []string{"-n", "2", s20, s20}},
		{name: "three files", args: []string{"-n", "2", s20, s20, s20}},
		{name: "verbose forces a header", args: []string{"-v", "-n", "1", s20}},
		{name: "quiet suppresses headers", args: []string{"-q", "-n", "1", s20, s20}},
		{name: "silent suppresses headers", args: []string{"--silent", "-n", "1", s20, s20}},
		{name: "long quiet", args: []string{"--quiet", "-n", "1", s20, s20}},
		{name: "long verbose", args: []string{"--verbose", "-n", "1", s20}},
		{name: "quiet then verbose", args: []string{"-q", "-v", "-n", "1", s20, s20}},
		{name: "verbose then quiet", args: []string{"-v", "-q", "-n", "1", s20, s20}},
		{name: "headers around an empty file", args: []string{empty, empty}},
		{name: "verbose on an empty file", args: []string{"-v", empty}},
		{name: "header for stdin", args: []string{"-n", "2", "-", s20}, stdin: "p\nq\nr\n"},

		// Operands.
		{name: "no operand reads stdin", stdin: "a\nb\nc\n"},
		{name: "lone dash is stdin", args: []string{"-"}, stdin: "a\nb\n"},
		{name: "dashdash alone", args: []string{"--"}, stdin: "a\nb\n"},
		{name: "dashdash then an option-looking name", args: []string{"-n", "2", "--", "-3"}},
		{name: "empty operand", args: []string{""}},
		{name: "empty operand among others", args: []string{"-n", "1", "", s20}},
		{name: "operand that is not valid UTF-8", args: []string{raw}},
		{name: "verbose header of a name that is not valid UTF-8", args: []string{"-v", raw}},
		{name: "name with a space", args: []string{"-v", spaced}},
		{name: "name with a quote", args: []string{"-v", quoted}},
		{name: "missing file", args: []string{missing}},
		{name: "missing file then a good one", args: []string{"-n", "2", missing, s20}},
		{name: "good file then a missing one", args: []string{"-n", "2", s20, missing}},
		{name: "missing between two good ones", args: []string{"-n", "2", s20, missing, s20}},
		{name: "two missing files", args: []string{"-n", "2", missing, missing}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "missing name with a quote", args: []string{filepath.Join(dir, "no'such")}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch")}},

		// A directory: the open succeeds, the read fails EISDIR, and the
		// header is already out when the diagnostic lands.
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{"-n", "3", d, s20}},
		{name: "directory with a zero count", args: []string{"-n", "0", d}},
		{name: "directory with a zero byte count", args: []string{"-c", "0", d}},
		{name: "directory elided", args: []string{"-n", "-1", d}},

		// -z: NUL line terminator. The headers keep their newline.
		{name: "zero terminated", args: []string{"-z", "-n", "2", nul}},
		{name: "zero terminated long", args: []string{"--zero-terminated", "-n", "2", nul}},
		{name: "zero terminated prefix", args: []string{"--zero", "-n", "2", nul}},
		{name: "zero terminated bytes", args: []string{"-z", "-c", "3", nul}},
		{name: "zero terminated headers", args: []string{"-z", "-v", "-n", "1", nul, nul}},
		{name: "zero terminated elision", args: []string{"-z", "-n", "-1", nul}},
		{name: "newline is not a terminator under -z", args: []string{"-z", "-n", "1", s20}},

		// The obsolete -NUM form: argv[1] only, and its suffix letters
		// each re-derive the count from the digits.
		{name: "obsolete count", args: []string{"-3", s20}},
		{name: "obsolete zero", args: []string{"-0", s20}},
		{name: "obsolete two digits", args: []string{"-12", s20}},
		{name: "obsolete bytes", args: []string{"-5c", s20}},
		{name: "obsolete lines", args: []string{"-5l", s20}},
		{name: "obsolete blocks", args: []string{"-1b", s400}},
		{name: "obsolete kibibytes", args: []string{"-1k", s400}},
		{name: "obsolete mebibytes", args: []string{"-1m", s400}},
		{name: "obsolete two blocks", args: []string{"-2b", s400}},
		{name: "obsolete blocks then bytes", args: []string{"-1bc", s400}},
		{name: "obsolete bytes then blocks", args: []string{"-1cb", s400}},
		{name: "obsolete lines then blocks", args: []string{"-1lb", s400}},
		{name: "obsolete lines then bytes", args: []string{"-1lc", s400}},
		{name: "obsolete bytes then lines", args: []string{"-1cl", s400}},
		{name: "obsolete quiet", args: []string{"-5q", s20}},
		{name: "obsolete quiet then verbose", args: []string{"-5qv", s20}},
		{name: "obsolete verbose then quiet", args: []string{"-1vq", s400}},
		{name: "obsolete repeated flag", args: []string{"-1qq", s400}},
		{name: "obsolete bytes then quiet", args: []string{"-5cq", s20}},
		{name: "obsolete zero terminated", args: []string{"-1z", nul}},
		{name: "obsolete then a real option", args: []string{"-5", "-c", "3", s20}},
		{name: "obsolete with two files", args: []string{"-5", s20, s20}},
		{name: "obsolete overflow", args: []string{"-9999999999999999999999", s20}},
		{name: "obsolete past uintmax", args: []string{"-18446744073709551616", s20}},
		// Anywhere but argv[1] a digit is an option byte, and there are
		// no digit options.
		{name: "obsolete bad suffix", args: []string{"-5x", s20}},
		{name: "obsolete bad suffix after a good one", args: []string{"-5cx", s20}},
		{name: "obsolete bad suffix after a multiplier", args: []string{"-1kx", s400}},
		{name: "obsolete digit after a suffix", args: []string{"-1c2", s20}},
		{name: "obsolete letter that is not a suffix", args: []string{"-1n", s400}},
		{name: "two obsolete counts", args: []string{"-1", "-2", s20}},
		{name: "obsolete after an option", args: []string{"-c", "5", "-3", s20}},
		{name: "obsolete after an obsolete", args: []string{"-5c", "-3", s20}},
		{name: "obsolete after an operand", args: []string{s20, "-5"}},
		{name: "obsolete after a dash operand", args: []string{"-", "-5", s20}},
		{name: "digit inside a cluster", args: []string{"-q5", s20}},
		{name: "sign is not the obsolete form", args: []string{"-+5", s20}},

		// getopt: the messages, the prefix rule, the hidden option.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option in a cluster", args: []string{"-qx", s20}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "short option needs a value", args: []string{"-n"}},
		{name: "long option needs a value", args: []string{"--lines"}},
		{name: "long bytes needs a value", args: []string{"--bytes"}},
		{name: "flag rejects a glued value", args: []string{"--verbose=x", s20}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		// The empty long name is a prefix of every option, so glibc
		// calls it ambiguous and lists them all — including the hidden
		// `---presume-input-pipe`.
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "v is ambiguous between verbose and version", args: []string{"--v", s20}},
		{name: "unique prefix lines", args: []string{"--li", "3", s20}},
		{name: "one letter prefix lines", args: []string{"--l", "3", s20}},
		{name: "unique prefix silent", args: []string{"--s", s20}},
		{name: "hidden option in full", args: []string{"---presume-input-pipe", s20}},
		{name: "hidden option by prefix", args: []string{"---p", s20}, stdin: "a\n"},
		{name: "hidden option as a bare dash name", args: []string{"---", s20}},
		{name: "hidden option does not match without its dash", args: []string{"--p", s20}},
		{name: "hidden option prefix without its dash", args: []string{"--presume", s20}},
		{name: "hidden option changes nothing", args: []string{"---presume-input-pipe", "-n", "-3", s20}},
		// POSIXLY_CORRECT ends the option scan at the first operand.
		{name: "posix operand then option", args: []string{s20, "-n", "1"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-n", "1", s20}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures. Under one block the failure is only found at
		// the final flush, which is close_stdout's wording; over one
		// block it is met mid-stream, which is head's own.
		{name: "stdout closed", args: []string{"-n", "1", s20}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"-n", "1", s20}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-n", "5000", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-n", "5000", big}, stdout: stdoutFull},
		// Nothing is written, so a closed stdout is not an error at all.
		{name: "stdout closed with nothing to write", args: []string{"-n", "0", s20}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},

		// stdin.
		{name: "stdin lines", args: []string{"-n", "2"}, stdin: "1\n2\n3\n4\n"},
		{name: "stdin bytes", args: []string{"-c", "3"}, stdin: "12345"},
		{name: "stdin elide lines", args: []string{"-n", "-2"}, stdin: "1\n2\n3\n4\n"},
		{name: "stdin elide bytes", args: []string{"-c", "-2"}, stdin: "12345"},
		{name: "stdin elide zero bytes", args: []string{"-c", "-0"}, stdin: "12345"},
		{name: "stdin empty", stdin: ""},
		{name: "stdin with no trailing newline", args: []string{"-n", "5"}, stdin: "a\nb"},
		{name: "stdin larger than one read block", args: []string{"-n", "3"}, stdin: strings.Repeat("x\n", 40000)},
		{name: "stdin bytes across read blocks", args: []string{"-c", "70000"}, stdin: strings.Repeat("y", 100000)},
		{name: "stdin elide across read blocks", args: []string{"-n", "-2"}, stdin: strings.Repeat("z\n", 40000)},
	}
}

// itoa is strconv.Itoa without the import, for the two fixture builders.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestHeadParity(t *testing.T) {
	requireParity(t, "head", headCases(t))
}

func TestHeadHelpVersion(t *testing.T) {
	requireHelp(t, "head", []string{"--help"}, 0)
	requireHelp(t, "head", []string{"--he"}, 0)
	requireHelp(t, "head", []string{"--help", "ignored"}, 0)
	requireVersion(t, "head", []string{"--version"}, 0)
	requireVersion(t, "head", []string{"--vers"}, 0)
}
