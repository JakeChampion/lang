package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tacFile writes `content` under `dir` as `name` and returns its path.
func tacFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tacCases is tac(1)'s corpus.
//
// Three things carry most of it. The separator is ATTACHED to the record
// it ends, so a file whose last record has none is printed with none and
// `-b` moves the attachment to the record that FOLLOWS. `-r` reads the
// separator in glibc's syntax 0 — Emacs, because tac never calls
// re_set_syntax — where `+` and `?` are bare operators, `\+` and `\?`
// and `\{m,n\}` are the plain characters, `[:alpha:]` is six ordinary
// bracket members, `.` skips a newline, and `^` and `$` anchor at one.
// And the input is read BACKWARDS in 8 KiB blocks that double whenever a
// record outgrows one, which the sizes below straddle: the block is not
// a free choice, since a directory answers lseek(SEEK_END) with
// LLONG_MAX and the read that follows fails EINVAL for the offset
// overflow at 8 KiB where a larger block would reach the descriptor and
// say EISDIR.
//
// The write failures are the fourth: tac never checks an fwrite, so
// whether stdout's failure is reported as `write error: <strerror>` or a
// bare `write error` is decided by which bytes glibc still had pending
// when fclose ran, and the sizes here are on both sides of that.
func tacCases(t *testing.T) []invocation {
	dir := t.TempDir()
	abc := tacFile(t, dir, "abc", "a\nb\nc\n")
	nonl := tacFile(t, dir, "nonl", "a\nb\nc")
	one := tacFile(t, dir, "one", "only\n")
	noeol := tacFile(t, dir, "noeol", "only")
	empty := tacFile(t, dir, "e0", "")
	blank := tacFile(t, dir, "blank", "\n")
	seps := tacFile(t, dir, "seps", "\n\n\n")
	nul := tacFile(t, dir, "nul", "a\x00b\x00c\x00")
	crlf := tacFile(t, dir, "crlf", "a\r\nb\r\nc\r\n")
	xs := tacFile(t, dir, "xs", "axxbxc")
	digits := tacFile(t, dir, "digits", "a1b22c333d")
	spaced := tacFile(t, dir, "f name", "x\ny\n")
	quoted := tacFile(t, dir, "f'n", "x\ny\n")
	// A name that is not valid UTF-8: it reaches the diagnostics as
	// bytes, so nothing may re-encode it.
	raw := tacFile(t, dir, "na\xffme", "x\ny\n")
	// Content that is not valid UTF-8 either side of a separator.
	rawc := tacFile(t, dir, "rawc", "\xff\xfe\n\xfd\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")

	// Sizes around the 8 KiB block: the first read is of `size % 8192`
	// bytes and every one after it walks a whole block backwards.
	lines := func(n int) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			b.WriteString(itoa(i))
			b.WriteByte('\n')
		}
		return b.String()
	}
	fill := func(name string, size int) string {
		s := lines(size)
		return tacFile(t, dir, name, s[:size])
	}
	b8191 := fill("b8191", 8191)
	b8192 := fill("b8192", 8192)
	b8193 := fill("b8193", 8193)
	b16385 := fill("b16385", 16385)
	b40000 := fill("b40000", 40000)
	// One record longer than the initial block, so the buffer doubles.
	long := tacFile(t, dir, "long", strings.Repeat("q", 100000)+"\nab\n")
	// No separator anywhere: the whole file is one record and every
	// block is read before a byte is written.
	nosep := tacFile(t, dir, "nosep", strings.Repeat("z", 20000))

	// Output sizes on both sides of where glibc's 4 KiB stdout buffer
	// leaves bytes pending at fclose. 4000 and 12000 end with a partial
	// buffer (`write error: <strerror>`); 4096 and 13192 do not (a bare
	// `write error`).
	w4000 := tacFile(t, dir, "w4000", strings.Repeat("0123456789\n", 400)[:4000])
	w4096 := tacFile(t, dir, "w4096", strings.Repeat("0123456789\n", 400)[:4096])
	w12000 := tacFile(t, dir, "w12000", strings.Repeat("0123456789\n", 1100)[:12000])
	w13192 := tacFile(t, dir, "w13192", strings.Repeat("0123456789\n", 1300)[:13192])

	return []invocation{
		// The default separator, attached after its record.
		{name: "three lines", args: []string{abc}},
		{name: "unterminated last line", args: []string{nonl}},
		{name: "one line", args: []string{one}},
		{name: "one unterminated line", args: []string{noeol}},
		{name: "empty file", args: []string{empty}},
		{name: "one blank line", args: []string{blank}},
		{name: "only separators", args: []string{seps}},
		{name: "carriage returns are not separators", args: []string{crlf}},
		{name: "content that is not valid UTF-8", args: []string{rawc}},

		// -b: the separator is attached before its record.
		{name: "before", args: []string{"-b", abc}},
		{name: "before unterminated", args: []string{"-b", nonl}},
		{name: "before empty", args: []string{"-b", empty}},
		{name: "before one blank line", args: []string{"-b", blank}},
		{name: "before only separators", args: []string{"-b", seps}},
		{name: "long before", args: []string{"--before", abc}},
		{name: "before by prefix", args: []string{"--be", abc}},
		{name: "before twice", args: []string{"-b", "-b", abc}},

		// -s: another separator.
		{name: "one-byte separator", args: []string{"-s", "x", xs}},
		{name: "two-byte separator", args: []string{"-s", "xx", xs}},
		{name: "separator that is not there", args: []string{"-s", "Q", xs}},
		{name: "separator glued", args: []string{"-sx", xs}},
		{name: "long separator", args: []string{"--separator", "x", xs}},
		{name: "long separator glued", args: []string{"--separator=x", xs}},
		{name: "separator by prefix", args: []string{"--sep=x", xs}},
		{name: "last separator wins", args: []string{"-s", "a", "-s", "x", xs}},
		{name: "newline separator named", args: []string{"-s", "\n", abc}},
		{name: "empty separator is NUL", args: []string{"-s", "", nul}},
		{name: "empty separator on text", args: []string{"-s", "", abc}},
		{name: "separator before with -s", args: []string{"-b", "-s", "x", xs}},
		{name: "separator that is the whole file", args: []string{"-s", "axxbxc", xs}},
		{name: "separator longer than the file", args: []string{"-s", "axxbxcd", xs}},
		{name: "separator that is not valid UTF-8", args: []string{"-s", "\xff", rawc}},
		{name: "separator that looks like dashdash", args: []string{"-s", "--", xs}},
		{name: "separator that looks like an option", args: []string{"-s", "-b", xs}},

		// -r: the separator is a regular expression, in glibc's syntax 0.
		{name: "regex literal", args: []string{"-r", "-s", "x", xs}},
		{name: "regex star", args: []string{"-r", "-s", "x*", xs}},
		{name: "regex plus is bare", args: []string{"-r", "-s", "x+", xs}},
		{name: "regex question is bare", args: []string{"-r", "-s", "x?", xs}},
		{name: "backslash plus is a literal", args: []string{"-r", "-s", "x\\+", xs}},
		{name: "backslash question is a literal", args: []string{"-r", "-s", "x\\?", xs}},
		{name: "intervals are literal braces", args: []string{"-r", "-s", "x\\{1,2\\}", xs}},
		{name: "alternation is backslashed", args: []string{"-r", "-s", "1\\|22", digits}},
		{name: "bare bar is a literal", args: []string{"-r", "-s", "1|22", digits}},
		{name: "regex group", args: []string{"-r", "-s", "\\(x\\)", xs}},
		{name: "regex backreference", args: []string{"-r", "-s", "\\(x\\)\\1", xs}},
		{name: "bracket expression", args: []string{"-r", "-s", "[123]", digits}},
		{name: "negated bracket", args: []string{"-r", "-s", "[^0-9]", digits}},
		{name: "character classes are not a thing", args: []string{"-r", "-s", "[[:digit:]]", digits}},
		{name: "dot does not match a newline", args: []string{"-r", "-s", "b.", abc}},
		{name: "dot matches a byte", args: []string{"-r", "-s", "x.", xs}},
		{name: "dot matches a NUL", args: []string{"-r", "-s", "a.", nul}},
		{name: "caret anchors at a newline", args: []string{"-r", "-s", "^b", abc}},
		{name: "dollar anchors at a newline", args: []string{"-r", "-s", "b$", abc}},
		{name: "caret alone", args: []string{"-r", "-s", "^", abc}},
		{name: "dollar alone", args: []string{"-r", "-s", "$", abc}},
		{name: "buffer start", args: []string{"-r", "-s", "\\`a", abc}},
		{name: "buffer end", args: []string{"-r", "-s", "c\\'", nonl}},
		{name: "word boundary", args: []string{"-r", "-s", "\\bb", abc}},
		{name: "word character", args: []string{"-r", "-s", "\\w", xs}},
		{name: "regex matching nothing", args: []string{"-r", "-s", "Q", xs}},
		{name: "regex that can match empty", args: []string{"-r", "-s", "Q*", xs}},
		{name: "regex before", args: []string{"-r", "-b", "-s", "x+", xs}},
		{name: "regex on an empty file", args: []string{"-r", "-s", "x", empty}},
		{name: "long regex", args: []string{"--regex", "-s", "x+", xs}},
		{name: "regex by prefix", args: []string{"--re", "-s", "x+", xs}},
		{name: "regex flag after its separator", args: []string{"-s", "x+", "-r", xs}},
		{name: "regex twice", args: []string{"-r", "-r", "-s", "x+", xs}},
		{name: "clustered regex and before", args: []string{"-rb", "-s", "x+", xs}},
		{name: "clustered before and regex", args: []string{"-br", "-s", "x+", xs}},
		{name: "clustered flags with a glued separator", args: []string{"-brs", "x", xs}},

		// The separator a regular expression may not be, and the ones
		// that are not regular expressions at all.
		{name: "empty regex separator", args: []string{"-r", "-s", "", abc}},
		{name: "unmatched group", args: []string{"-r", "-s", "a\\(", abc}},
		{name: "unmatched close", args: []string{"-r", "-s", "a\\)", abc}},
		{name: "unmatched bracket", args: []string{"-r", "-s", "[abc", abc}},
		{name: "trailing backslash", args: []string{"-r", "-s", "a\\", abc}},
		{name: "backreference to nothing", args: []string{"-r", "-s", "\\1", abc}},
		{name: "invalid range", args: []string{"-r", "-s", "[z-a]", abc}},

		// Blocks: the read walks backwards 8 KiB at a time and doubles
		// when a record does not fit.
		{name: "one byte under a block", args: []string{b8191}},
		{name: "exactly one block", args: []string{b8192}},
		{name: "one byte over a block", args: []string{b8193}},
		{name: "two blocks and a byte", args: []string{b16385}},
		{name: "five blocks", args: []string{b40000}},
		{name: "a record that outgrows the block", args: []string{long}},
		{name: "a record that outgrows the block, before", args: []string{"-b", long}},
		{name: "a file with no separator at all", args: []string{nosep}},
		{name: "regex across blocks", args: []string{"-r", "-s", "[0-9]", b16385}},
		{name: "regex anchored across blocks", args: []string{"-r", "-s", "^1", b16385}},
		{name: "regex anchored at the end across blocks", args: []string{"-r", "-s", "1$", b16385}},
		{name: "regex over a record that outgrows the block", args: []string{"-r", "-s", "q+", long}},
		{name: "two-byte separator across blocks", args: []string{"-s", "55", b16385}},
		{name: "separator across blocks, before", args: []string{"-b", "-s", "5", b40000}},

		// Operands.
		{name: "two files", args: []string{abc, abc}},
		{name: "three files", args: []string{abc, nonl, empty}},
		// The output buffer carries ACROSS operands, so a second file
		// starts mid-block whenever the first did not end on one.
		{name: "two files that straddle the output block", args: []string{w12000, b8191}},
		{name: "two files that fill the output block exactly", args: []string{w4096, b16385}},
		{name: "two files, separator before", args: []string{"-b", w4000, b8193}},
		{name: "no operand reads stdin", stdin: "a\nb\nc\n"},
		{name: "lone dash is stdin", args: []string{"-"}, stdin: "a\nb\n"},
		{name: "dashdash alone", args: []string{"--"}, stdin: "a\nb\n"},
		{name: "dash among files", args: []string{abc, "-", abc}, stdin: "p\nq\n"},
		{name: "dash twice", args: []string{"-", "-"}, stdin: "p\nq\n"},
		{name: "dashdash then a name that looks like an option", args: []string{"--", "-b"}},
		{name: "empty operand", args: []string{""}},
		{name: "empty operand among others", args: []string{"", abc}},
		{name: "operand that is not valid UTF-8", args: []string{raw}},
		{name: "name with a space", args: []string{spaced}},
		{name: "name with a quote", args: []string{quoted}},

		// Operands that cannot be read.
		{name: "missing file", args: []string{missing}},
		{name: "missing file then a good one", args: []string{missing, abc}},
		{name: "good file then a missing one", args: []string{abc, missing}},
		{name: "missing between two good ones", args: []string{abc, missing, abc}},
		{name: "two missing files", args: []string{missing, missing}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "missing name with a quote", args: []string{filepath.Join(dir, "no'such")}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch")}},
		// A directory: the open succeeds, lseek answers LLONG_MAX and
		// the read at LLONG_MAX - LLONG_MAX % 8192 fails EINVAL.
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{d, abc}},
		{name: "file then a directory", args: []string{abc, d}},
		{name: "directory with a regex separator", args: []string{"-r", "-s", "x", d}},

		// getopt: the messages, the prefix rule, the ambiguity list.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option in a cluster", args: []string{"-bx", abc}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "short option needs a value", args: []string{"-s"}},
		{name: "long option needs a value", args: []string{"--separator"}},
		{name: "flag rejects a glued value", args: []string{"--before=x", abc}},
		{name: "regex rejects a glued value", args: []string{"--regex=x", abc}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "bare dashes", args: []string{"---", abc}},
		{name: "uppercase is not an option", args: []string{"-B", abc}},
		// The option scan permutes, and POSIXLY_CORRECT stops it at the
		// first operand.
		{name: "option after the operand", args: []string{abc, "-b"}},
		{name: "posix operand then option", args: []string{abc, "-b"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-b", abc}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix separator after the operand", args: []string{xs, "-s", "x"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix dashdash then an option-looking name", args: []string{"--", "-b"}, env: []string{"POSIXLY_CORRECT=1"}},

		// stdin, which never seeks here: the harness always hands the
		// child a pipe, so every one of these is the held-stream path.
		{name: "stdin unterminated", stdin: "a\nb"},
		{name: "stdin empty", stdin: ""},
		{name: "stdin one newline", stdin: "\n"},
		{name: "stdin with -b", args: []string{"-b"}, stdin: "a\nb\nc\n"},
		{name: "stdin with a separator", args: []string{"-s", "x"}, stdin: "axbxc"},
		{name: "stdin with a regex", args: []string{"-r", "-s", "x+"}, stdin: "axxbxc"},
		{name: "stdin larger than one block", stdin: lines(3000)},
		{name: "stdin across blocks with a regex", args: []string{"-r", "-s", "^1"}, stdin: lines(3000)},
		{name: "stdin with a record that outgrows the block", stdin: strings.Repeat("q", 100000) + "\nab\n"},
		{name: "stdin that is not valid UTF-8", stdin: "\xff\xfe\n\xfd\n"},

		// Write failures. Which wording GNU gives depends on the size.
		{name: "stdout closed", args: []string{abc}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{abc}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout full with nothing to write", args: []string{empty}, stdout: stdoutFull},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},
		{name: "stdout full with a missing file", args: []string{missing}, stdout: stdoutFull},
		{name: "stdout full, pending at the close", args: []string{w4000}, stdout: stdoutFull},
		{name: "stdout full, nothing pending at the close", args: []string{w4096}, stdout: stdoutFull},
		{name: "stdout full, pending after a block", args: []string{w12000}, stdout: stdoutFull},
		{name: "stdout full, dropped after a block", args: []string{w13192}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{w13192}, stdout: stdoutClosed},
		{name: "stdout full from stdin", stdin: lines(3000), stdout: stdoutFull},
	}
}

func TestTacParity(t *testing.T) {
	requireParity(t, "tac", tacCases(t))
}

func TestTacHelpVersion(t *testing.T) {
	requireHelp(t, "tac", []string{"--help"}, 0)
	requireHelp(t, "tac", []string{"--he"}, 0)
	requireHelp(t, "tac", []string{"--help", "ignored"}, 0)
	requireVersion(t, "tac", []string{"--version"}, 0)
	requireVersion(t, "tac", []string{"--vers"}, 0)
	requireVersion(t, "tac", []string{"--version", "ignored"}, 0)
}
