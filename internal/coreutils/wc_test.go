package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wcFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// wcCases is wc(1)'s corpus.
//
// The counts themselves are the easy half. The two that need the whole
// corpus are the COLUMN WIDTH — computed from the sizes of the operands
// that stat as regular files, before a byte is read, and skipped
// entirely for one input printing one count — and the C-locale ISPRINT
// rule that decides what a word is and what a line's display width is.
func wcCases(t *testing.T) []invocation {
	dir := t.TempDir()
	f1 := wcFile(t, dir, "f1", "a\nb\n")
	one := wcFile(t, dir, "one", "x")
	// 12345 bytes: five digits, so the width is visibly not 1.
	big := wcFile(t, dir, "big", strings.Repeat("\x00", 12345))
	empty := wcFile(t, dir, "e0", "")
	words := wcFile(t, dir, "words", "  spaced  words  \nand\tmore\n")
	spaced := wcFile(t, dir, "f name", "x\n")
	quoted := wcFile(t, dir, "f'n", "x\n")
	raw := wcFile(t, dir, "na\xffme", "x\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")

	// --files0-from lists, including the malformed ones.
	names2 := wcFile(t, dir, "n2", f1+"\x00"+one+"\x00")
	namesBig := wcFile(t, dir, "nbig", big+"\x00")
	namesOne := wcFile(t, dir, "n1", f1+"\x00")
	namesNoTerm := wcFile(t, dir, "nnt", f1)
	namesEmpty := wcFile(t, dir, "nempty", "")
	namesZero := wcFile(t, dir, "nzero", "\x00")
	namesZeroMid := wcFile(t, dir, "nzmid", f1+"\x00\x00")
	namesZeroLast := wcFile(t, dir, "nzlast", f1+"\x00"+one+"\x00\x00")
	namesDash := wcFile(t, dir, "ndash", "-\x00")
	namesMissing := wcFile(t, dir, "nmiss", missing+"\x00"+f1+"\x00")
	namesDir := wcFile(t, dir, "ndir", d+"\x00")

	return []invocation{
		// Counts and their fixed print order.
		{name: "default", args: []string{f1}},
		{name: "bytes", args: []string{"-c", f1}},
		{name: "lines", args: []string{"-l", f1}},
		{name: "words", args: []string{"-w", f1}},
		{name: "chars", args: []string{"-m", f1}},
		{name: "max line length", args: []string{"-L", f1}},
		{name: "order is fixed not given", args: []string{"-Lc", f1}},
		{name: "all five", args: []string{"-clmwL", f1}},
		{name: "chars and bytes", args: []string{"-mc", f1}},
		{name: "long bytes", args: []string{"--bytes", f1}},
		{name: "long chars", args: []string{"--chars", f1}},
		{name: "long lines", args: []string{"--lines", f1}},
		{name: "long words", args: []string{"--words", f1}},
		{name: "long max line length", args: []string{"--max-line-length", f1}},
		{name: "repeated option", args: []string{"-l", "-l", f1}},
		// --debug is not in the help text but is a real option.
		{name: "debug", args: []string{"--debug", f1}},
		{name: "debug with bytes", args: []string{"--debug", "-c", f1}},

		// The width: digits of the summed size of the regular operands,
		// 7 when any operand is not one, and 1 when nothing is statted.
		{name: "width from one file", args: []string{big}},
		{name: "width from two files", args: []string{one, big}},
		{name: "one input one count is never padded", args: []string{"-c", big}},
		{name: "one count two files", args: []string{"-c", big, f1}},
		{name: "one count two files words", args: []string{"-w", big, f1}},
		{name: "two counts one file", args: []string{"-cl", big}},
		{name: "a directory operand widens to seven", args: []string{f1, d}},
		{name: "a device operand widens to seven", args: []string{f1, "/dev/null"}},
		{name: "a missing operand contributes nothing", args: []string{f1, missing}},
		{name: "missing first", args: []string{missing, big}},
		{name: "all missing", args: []string{missing, missing}},
		{name: "stdin widens to seven", args: []string{f1, "-"}, stdin: "a\n"},
		{name: "stdin alone", stdin: "a b c\n"},
		{name: "stdin as an operand", args: []string{"-"}, stdin: "a b c\n"},
		{name: "two stdin operands", args: []string{"-", "-"}, stdin: "a\n"},
		{name: "one count from stdin", args: []string{"-w"}, stdin: "a b c\n"},
		{name: "two counts from stdin", args: []string{"-Lw"}, stdin: "a\tb\n"},

		// --total=WHEN, including argmatch's abbreviations.
		{name: "total only", args: []string{"--total=only", f1, f1}},
		{name: "total only one file", args: []string{"--total=only", f1}},
		{name: "total only big", args: []string{"--total=only", big}},
		{name: "total only stdin", args: []string{"--total=only"}, stdin: strings.Repeat("z", 100000)},
		{name: "total never", args: []string{"--total=never", f1, f1}},
		{name: "total always", args: []string{"--total=always", f1}},
		{name: "total auto", args: []string{"--total=auto", f1}},
		{name: "total abbreviated only", args: []string{"--total=o", f1, f1}},
		{name: "total abbreviated always", args: []string{"--total=al", f1, f1}},
		{name: "total abbreviated auto", args: []string{"--total=au", f1, f1}},
		{name: "total abbreviated never", args: []string{"--total=n", f1, f1}},
		{name: "total ambiguous", args: []string{"--total=a", f1}},
		{name: "total empty is ambiguous", args: []string{"--total=", f1}},
		{name: "total invalid", args: []string{"--total=x", f1}},
		{name: "total invalid with a prefix", args: []string{"--total=auto2", f1}},
		{name: "total takes the next argument", args: []string{"--total", f1}},
		{name: "total needs an argument", args: []string{"--total"}},

		// Words and the longest line: C-locale ISPRINT, tabs to the
		// next multiple of eight, CR and FF restart the line.
		{name: "words with runs of blanks", args: []string{"-w", words}},
		{name: "max line length with a tab", args: []string{"-L", words}},
		{name: "empty input", stdin: ""},
		{name: "no trailing newline", stdin: "no newline"},
		{name: "tab separated words", args: []string{"-w"}, stdin: "a\tb\nc\n"},
		{name: "NUL neither starts nor ends a word", args: []string{"-w"}, stdin: "a\x00b c\n"},
		{name: "a high byte is not a word", args: []string{"-w"}, stdin: "\xff\n"},
		{name: "a high byte inside a word", args: []string{"-w"}, stdin: "ab\xffcd\n"},
		{name: "chars count bytes in the C locale", args: []string{"-m"}, stdin: "\xff\xfe\n"},
		{name: "max line length of an unterminated line", args: []string{"-L"}, stdin: "abc"},
		{name: "max line length is the longest", args: []string{"-L"}, stdin: "abc\nde\n"},
		{name: "tab advances to eight", args: []string{"-L"}, stdin: "a\tb\n"},
		{name: "tab from zero", args: []string{"-L"}, stdin: "\t\n"},
		{name: "tab at seven", args: []string{"-L"}, stdin: "aaaaaaa\tb\n"},
		{name: "tab at eight", args: []string{"-L"}, stdin: "aaaaaaaa\tb\n"},
		{name: "backspace has no width", args: []string{"-L"}, stdin: "a\bb\n"},
		{name: "a control byte has no width", args: []string{"-L"}, stdin: "a\x01b\n"},
		{name: "carriage return restarts the line", args: []string{"-L"}, stdin: "a\rb\n"},
		{name: "carriage return is not a line", args: []string{"-l"}, stdin: "a\rb\n"},
		{name: "form feed restarts the line", args: []string{"-L"}, stdin: "abc\fd\n"},
		{name: "vertical tab ends a word", args: []string{"-w"}, stdin: "a\vb\n"},
		{name: "vertical tab has no width", args: []string{"-L"}, stdin: "a\vb\n"},
		{name: "high bytes have no width", args: []string{"-Lm"}, stdin: "\xc3\xa9\n"},
		{name: "DEL has no width", args: []string{"-L"}, stdin: "a\x7fb\n"},
		{name: "a line that spans read blocks", args: []string{"-L"}, stdin: strings.Repeat("x", 40000) + "\n"},
		{name: "words across read blocks", args: []string{"-w"}, stdin: strings.Repeat("ab ", 20000)},
		{name: "lines across read blocks", args: []string{"-l"}, stdin: strings.Repeat("x\n", 40000)},
		{name: "a word split across read blocks", args: []string{"-w"}, stdin: strings.Repeat("x", 20000) + " " + strings.Repeat("y", 20000)},

		// Operands and their diagnostics.
		{name: "empty operand", args: []string{""}},
		{name: "empty operand before a file", args: []string{"", f1}},
		{name: "empty operand after a file", args: []string{f1, ""}},
		{name: "missing file", args: []string{missing}},
		{name: "missing then present", args: []string{missing, f1}},
		{name: "present then missing", args: []string{f1, missing}},
		{name: "directory", args: []string{d}},
		{name: "directory with one count", args: []string{"-c", d}},
		{name: "directory with a file", args: []string{"-c", d, f1}},
		{name: "two directories", args: []string{d, d}},
		{name: "directory max line length", args: []string{"-L", d}},
		{name: "name with a space", args: []string{spaced}},
		{name: "name with a quote", args: []string{quoted}},
		{name: "name that is not valid UTF-8", args: []string{raw}},
		{name: "missing name with a space", args: []string{filepath.Join(dir, "no such")}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch")}},
		{name: "empty file", args: []string{empty}},
		{name: "dashdash", args: []string{"--"}, stdin: "a\n"},
		{name: "dashdash then a name", args: []string{"--", f1}},

		// --files0-from.
		{name: "files0 two names", args: []string{"--files0-from=" + names2}},
		{name: "files0 one name", args: []string{"--files0-from=" + namesOne}},
		{name: "files0 one name one count", args: []string{"-c", "--files0-from=" + namesBig}},
		{name: "files0 one name three counts", args: []string{"--files0-from=" + namesBig}},
		{name: "files0 total always", args: []string{"--total=always", "--files0-from=" + namesOne}},
		{name: "files0 without a trailing NUL", args: []string{"--files0-from=" + namesNoTerm}},
		{name: "files0 empty list", args: []string{"--files0-from=" + namesEmpty}},
		{name: "files0 zero-length name", args: []string{"--files0-from=" + namesZero}},
		{name: "files0 zero-length name in the middle", args: []string{"--files0-from=" + namesZeroMid}},
		{name: "files0 zero-length name last", args: []string{"--files0-from=" + namesZeroLast}},
		{name: "files0 missing name", args: []string{"--files0-from=" + namesMissing}},
		{name: "files0 directory name", args: []string{"--files0-from=" + namesDir}},
		{name: "files0 a dash name from a file", args: []string{"--files0-from=" + namesDash}, stdin: "a\n"},
		{name: "files0 from stdin", args: []string{"--files0-from=-"}, stdin: f1 + "\x00"},
		{name: "files0 a dash name from stdin", args: []string{"--files0-from=-"}, stdin: "-\x00"},
		{name: "files0 with an operand", args: []string{"--files0-from=" + names2, f1}},
		{name: "files0 missing list", args: []string{"--files0-from=" + missing}},
		{name: "files0 empty list name", args: []string{"--files0-from="}},
		{name: "files0 list is a directory", args: []string{"--files0-from=" + d}},
		{name: "files0 needs an argument", args: []string{"--files0-from"}},
		{name: "files0 as a separate argument", args: []string{"--files0-from", names2}},

		// getopt.
		{name: "invalid short option", args: []string{"-x", f1}},
		{name: "invalid short option in a cluster", args: []string{"-lx", f1}},
		{name: "unrecognized long option", args: []string{"--foo", f1}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", f1}},
		{name: "empty long name is ambiguous", args: []string{"--=x", f1}},
		{name: "unique prefix lines", args: []string{"--l", f1}},
		{name: "unique prefix li", args: []string{"--li", f1}},
		{name: "unique prefix line", args: []string{"--line", f1}},
		{name: "unique prefix bytes", args: []string{"--b", f1}},
		{name: "unique prefix by", args: []string{"--by", f1}},
		{name: "unique prefix chars", args: []string{"--c", f1}},
		{name: "unique prefix ch", args: []string{"--ch", f1}},
		{name: "unique prefix max line length", args: []string{"--m", f1}},
		{name: "unique prefix words", args: []string{"--w", f1}},
		{name: "unique prefix total", args: []string{"--t", f1}},
		{name: "unique prefix to", args: []string{"--to", f1}},
		{name: "flag rejects a glued value", args: []string{"--lines=1", f1}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "posix operand then option", args: []string{f1, "-l"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-l", f1}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures. wc flushes its counts itself, so a full
		// stdout has already taken the error by the time fclose runs and
		// there is no errno left to name.
		{name: "stdout closed", args: []string{f1}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{f1}, stdout: stdoutFull},
		{name: "stdout closed with no input", stdin: "", stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},
		{name: "stdout full with many files", args: []string{f1, f1, f1}, stdout: stdoutFull},
	}
}

func TestWcParity(t *testing.T) {
	requireParity(t, "wc", wcCases(t))
}

func TestWcHelpVersion(t *testing.T) {
	requireHelp(t, "wc", []string{"--help"}, 0)
	requireHelp(t, "wc", []string{"--he"}, 0)
	requireVersion(t, "wc", []string{"--version"}, 0)
	requireVersion(t, "wc", []string{"--vers"}, 0)
}
