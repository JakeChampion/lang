package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// foldFile writes `content` under `dir` as `name` and returns its path.
func foldFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// foldCases is fold(1)'s corpus.
//
// Column counting is where -b differs from the default: a tab jumps to
// the next multiple of eight, a backspace goes back one and a carriage
// return returns to zero, and the character that overflows a line is
// re-measured against the fresh one — so a tab can appear alone on a
// line of its own. `-s` looks back for the last blank and falls through
// to the hard break when there is none.
func foldCases(t *testing.T) []invocation {
	dir := t.TempDir()
	alpha := foldFile(t, dir, "alpha", "abcdefghij\n")
	words := foldFile(t, dir, "words", "aaa bbb ccc ddd\n")
	tabs := foldFile(t, dir, "tabs", "\t\tabc\n")
	mixed := foldFile(t, dir, "mixed", "a\tb\bc\rd\n")
	nonl := foldFile(t, dir, "nonl", "abcdef")
	empty := foldFile(t, dir, "e0", "")
	blank := foldFile(t, dir, "blank", "\n\n")
	nul := foldFile(t, dir, "nul", "a\x00b\n")
	raw := foldFile(t, dir, "na\xffme", "abcdef\n")
	// 100 columns, so the default width of 80 wraps it once.
	wide := foldFile(t, dir, "wide", strings.Repeat("x", 100)+"\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	big := foldFile(t, dir, "big", strings.Repeat("0123456789abcdef\n", 20000))

	return []invocation{
		// Widths.
		{name: "default width", args: []string{wide}},
		{name: "width five", args: []string{"-w5", alpha}},
		{name: "width one", args: []string{"-w1", alpha}},
		{name: "width with a space", args: []string{"-w", "3", alpha}},
		{name: "long width", args: []string{"--width=4", alpha}},
		{name: "width wider than the line", args: []string{"-w", "100", alpha}},
		{name: "leading blank in the width", args: []string{"-w", " 5", alpha}},
		{name: "plus in the width", args: []string{"-w", "+5", alpha}},
		{name: "leading zero in the width", args: []string{"-w", "05", alpha}},
		{name: "last width wins", args: []string{"-w9", "-w3", alpha}},

		// The obsolete -NUM form.
		{name: "obsolete width", args: []string{"-5", alpha}},
		{name: "obsolete width two digits", args: []string{"-12", wide}},
		{name: "obsolete after an operand", args: []string{alpha, "-5"}},
		{name: "obsolete then explicit", args: []string{"-5", "-w3", alpha}},
		{name: "explicit then obsolete", args: []string{"-w3", "-5", alpha}},
		{name: "obsolete after dashdash is a file", args: []string{"--", "-5"}},
		{name: "obsolete with a suffix letter", args: []string{"-5x", alpha}},
		{name: "obsolete zero", args: []string{"-0", alpha}},
		{name: "digit inside a cluster", args: []string{"-b5", alpha}},
		{name: "digit before a flag", args: []string{"-5b", alpha}},

		// Width faults: one line, no `Try …` line.
		{name: "zero width", args: []string{"-w0", alpha}},
		{name: "negative width", args: []string{"-w", "-1", alpha}},
		{name: "not a number", args: []string{"-wx", alpha}},
		{name: "trailing garbage", args: []string{"-w", "5x", alpha}},
		{name: "suffixes are not accepted", args: []string{"-w", "5b", alpha}},
		{name: "empty width", args: []string{"-w", "", alpha}},
		{name: "hex is not accepted", args: []string{"-w", "0x10", alpha}},
		{name: "trailing blank", args: []string{"-w", "5 ", alpha}},
		{name: "the largest width", args: []string{"-w", "18446744073709551606", alpha}},
		{name: "one past the largest width", args: []string{"-w", "18446744073709551607", alpha}},
		{name: "far too large", args: []string{"-w", "99999999999999999999", alpha}},

		// Columns versus bytes.
		{name: "tabs count to the next stop", args: []string{"-w9", tabs}},
		{name: "tabs that do not fit", args: []string{"-w5", mixed}},
		{name: "bytes count tabs as one", args: []string{"-b", "-w3", tabs}},
		{name: "long bytes option", args: []string{"--bytes", "-w3", tabs}},
		{name: "backspace goes back a column", args: []string{"-w2"}, stdin: "ab\bc\n"},
		{name: "backspace at column zero", args: []string{"-w2"}, stdin: "\b\babc\n"},
		{name: "carriage return resets the column", args: []string{"-w3"}, stdin: "ab\rcd\n"},
		{name: "bytes ignore the backspace", args: []string{"-b", "-w2"}, stdin: "ab\bc\n"},
		{name: "bytes ignore the carriage return", args: []string{"-b", "-w3"}, stdin: "ab\rcd\n"},
		{name: "a tab alone on a line", args: []string{"-w5"}, stdin: "a\tb\n"},
		{name: "NUL is one column", args: []string{"-w1", nul}},

		// -s.
		{name: "break at spaces", args: []string{"-s", "-w7", words}},
		{name: "break at spaces width one", args: []string{"-s", "-w1", words}},
		{name: "break with no space to break at", args: []string{"-s", "-w3", alpha}},
		{name: "break at a leading space", args: []string{"-s", "-w3"}, stdin: " aaaa\n"},
		{name: "break at a tab", args: []string{"-s", "-w4"}, stdin: "aa\tbb\n"},
		{name: "break at spaces with bytes", args: []string{"-s", "-b", "-w3"}, stdin: "aa bb\n"},
		{name: "long spaces option", args: []string{"--spaces", "-w7", words}},
		{name: "trailing spaces", args: []string{"-s", "-w2"}, stdin: "  ab\n"},
		{name: "clustered flags", args: []string{"-sbw5", alpha}},

		// Line shapes.
		{name: "no final newline", args: []string{"-w2", nonl}},
		{name: "empty file", args: []string{"-w2", empty}},
		{name: "blank lines", args: []string{"-w2", blank}},
		{name: "line exactly the width", args: []string{"-w10", alpha}},
		{name: "line one past the width", args: []string{"-w9", alpha}},

		// Operands. Files are NOT joined: a tail with no newline keeps
		// its own line and the next file starts at column zero.
		{name: "no operand reads stdin", args: []string{"-w3"}, stdin: "abcdefgh\n"},
		{name: "lone dash", args: []string{"-w3", "-"}, stdin: "abcdefgh\n"},
		{name: "two files", args: []string{"-w2", nonl, alpha}},
		{name: "two files both unterminated", args: []string{"-w2", nonl, nonl}},
		{name: "dashdash", args: []string{"-w3", "--", alpha}},
		{name: "empty operand", args: []string{"-w3", ""}},
		{name: "operand that is not valid UTF-8", args: []string{"-w3", raw}},
		{name: "missing file", args: []string{missing}},
		{name: "missing then good", args: []string{"-w3", missing, alpha}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{"-w3", d, alpha}},

		// getopt.
		{name: "invalid short option", args: []string{"-c", alpha}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "width needs a value", args: []string{"-w"}},
		{name: "long width needs a value", args: []string{"--width"}},
		{name: "flag rejects a glued value", args: []string{"--bytes=x", alpha}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unique prefix width", args: []string{"--w=3", alpha}},
		{name: "unique prefix bytes", args: []string{"--b", "-w3", tabs}},
		{name: "unique prefix spaces", args: []string{"--sp", "-w7", words}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "posix operand then option", args: []string{alpha, "-w3"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-w3", alpha}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout closed", args: []string{"-w3", alpha}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"-w3", alpha}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-w3", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-w3", big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},

		// Bulk and block boundaries.
		{name: "large file", args: []string{"-w7", big}},
		{name: "line longer than one read block", args: []string{"-w13"}, stdin: strings.Repeat("z", 70000) + "\n"},
		{name: "line longer than one read block with -s", args: []string{"-s", "-w13"}, stdin: strings.Repeat("zz ", 30000) + "\n"},
		{name: "many short lines", args: []string{"-w80"}, stdin: strings.Repeat("short\n", 20000)},
	}
}

func TestFoldParity(t *testing.T) {
	requireParity(t, "fold", foldCases(t))
}

func TestFoldHelpVersion(t *testing.T) {
	requireHelp(t, "fold", []string{"--help"}, 0)
	requireHelp(t, "fold", []string{"--he"}, 0)
	requireVersion(t, "fold", []string{"--version"}, 0)
	requireVersion(t, "fold", []string{"--vers"}, 0)
}
