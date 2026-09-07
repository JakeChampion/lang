package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// catCases is cat(1)'s corpus.
//
// The copy is the easy half. What the corpus has to pin is the line
// rewriter: `%6d\t` numbering that widens past a million and skips
// blank lines under -b, squeezing that remembers a blank line across a
// file boundary, an unterminated file continuing its line into the
// next, and the -v spelling of every byte class. Then the two fstat
// checks — a closed stdout is refused before anything is read, and a
// regular file that is its own output is refused with bytes still to
// copy — which are reachable only through the harness's file-backed
// stdin and stdout.
func catCases(t *testing.T) []invocation {
	dir := t.TempDir()
	ab := catFile(t, dir, "ab", "a\nb\n")
	blanks := catFile(t, dir, "blanks", "a\n\n\n\nb\n\n")
	leading := catFile(t, dir, "leading", "\n\n\na\n")
	nonl := catFile(t, dir, "nonl", "x\ny")
	endsBlank := catFile(t, dir, "endsblank", "a\n\n")
	startsBlank := catFile(t, dir, "startsblank", "\n\nb\n")
	oneBlank := catFile(t, dir, "oneblank", "\n")
	empty := catFile(t, dir, "e0", "")
	// Every byte class -v distinguishes: printable, tab, a control,
	// DEL, then the same four with the high bit set, plus CR and ESC.
	ctrl := catFile(t, dir, "ctrl", "x\ty\x01\x7f\x80\x89\x8a\x9f\xa0\xfe\xff\r\x1b\n\x00\n")
	spaced := catFile(t, dir, "f name", "x\n")
	quoted := catFile(t, dir, "f'n", "x\n")
	raw := catFile(t, dir, "na\xffme", "x\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Past one read block, so the line state crosses a chunk boundary
	// in every mode, and past a million lines for the number width.
	big := catFile(t, dir, "big", strings.Repeat("line\n\n", 30000))
	million := catFile(t, dir, "million", strings.Repeat("\n", 1000002))
	// Its own output: the harness appends the run to it.
	self := catFile(t, dir, "self", "s\n")
	selfEmpty := catFile(t, dir, "selfempty", "")

	return []invocation{
		// The copy.
		{name: "one file", args: []string{ab}},
		{name: "two files", args: []string{ab, nonl}},
		{name: "no operand reads stdin", stdin: "p\nq\n"},
		{name: "lone dash is stdin", args: []string{"-"}, stdin: "p\n"},
		{name: "stdin between files", args: []string{ab, "-", ab}, stdin: "p\n"},
		{name: "stdin twice", args: []string{"-", "-"}, stdin: "p\n"},
		{name: "empty file", args: []string{empty}},
		{name: "empty stdin", stdin: ""},
		{name: "large file", args: []string{big}},
		{name: "large stdin", stdin: strings.Repeat("0123456789\n", 40000)},
		{name: "u is ignored", args: []string{"-u", ab}},

		// -n / -b.
		{name: "number", args: []string{"-n", ab}},
		{name: "number long", args: []string{"--number", ab}},
		{name: "number blanks", args: []string{"-n", blanks}},
		{name: "number nonblank", args: []string{"-b", blanks}},
		{name: "number nonblank long", args: []string{"--number-nonblank", blanks}},
		{name: "b overrides n", args: []string{"-bn", blanks}},
		{name: "n then b", args: []string{"-n", "-b", blanks}},
		{name: "numbers continue across files", args: []string{"-n", ab, ab}},
		{name: "unterminated line continues into the next file", args: []string{"-n", nonl, ab}},
		{name: "nonblank across an unterminated file", args: []string{"-b", nonl, endsBlank}},
		{name: "number an unterminated last line", args: []string{"-n", nonl}},
		{name: "number leading blanks", args: []string{"-n", leading}},
		{name: "nonblank leading blanks", args: []string{"-b", leading}},
		{name: "number past a million", args: []string{"-n", million}},
		{name: "number a large file", args: []string{"-n", big}},
		{name: "nonblank a large file", args: []string{"-b", big}},
		{name: "number stdin", args: []string{"-n"}, stdin: "p\n\nq"},

		// -s.
		{name: "squeeze", args: []string{"-s", blanks}},
		{name: "squeeze long", args: []string{"--squeeze-blank", blanks}},
		{name: "squeeze leading blanks", args: []string{"-s", leading}},
		{name: "squeeze a blank across files", args: []string{"-s", endsBlank, startsBlank}},
		{name: "squeeze blank files", args: []string{"-s", oneBlank, oneBlank, oneBlank}},
		{name: "squeeze then a blank file", args: []string{"-s", endsBlank, oneBlank, ab}},
		{name: "squeeze with numbers", args: []string{"-sn", blanks}},
		{name: "squeeze with nonblank numbers", args: []string{"-sb", blanks}},
		{name: "squeeze ends", args: []string{"-sE", blanks}},
		{name: "squeeze a large file", args: []string{"-s", big}},
		{name: "squeeze only blanks", args: []string{"-s"}, stdin: "\n\n\n"},
		{name: "squeeze with an unterminated blank", args: []string{"-s", endsBlank, nonl}},

		// -E / -T / -v and the combinations.
		{name: "show ends", args: []string{"-E", ctrl}},
		{name: "show ends long", args: []string{"--show-ends", nonl}},
		{name: "show tabs", args: []string{"-T", ctrl}},
		{name: "show tabs long", args: []string{"--show-tabs", ctrl}},
		{name: "show nonprinting", args: []string{"-v", ctrl}},
		{name: "show nonprinting long", args: []string{"--show-nonprinting", ctrl}},
		{name: "show all", args: []string{"-A", ctrl}},
		{name: "show all long", args: []string{"--show-all", ctrl}},
		{name: "e is vE", args: []string{"-e", ctrl}},
		{name: "t is vT", args: []string{"-t", ctrl}},
		{name: "vT", args: []string{"-vT", ctrl}},
		{name: "vE", args: []string{"-vE", ctrl}},
		{name: "TE", args: []string{"-TE", ctrl}},
		{name: "everything", args: []string{"-AbsTEvnet", blanks, ctrl}},
		{name: "nonprinting with numbers", args: []string{"-nv", ctrl}},
		{name: "show all a large file", args: []string{"-A", big}},
		{name: "show ends an unterminated line", args: []string{"-E", nonl}},
		{name: "high bytes", args: []string{"-v"}, stdin: "\x80\x81\x9f\xa0\xa1\xfe\xff"},
		{name: "a NUL", args: []string{"-v"}, stdin: "\x00\n"},
		{name: "a tab under v alone", args: []string{"-v"}, stdin: "\t\n"},
		{name: "a high tab under v", args: []string{"-v"}, stdin: "\x89\n"},
		{name: "a high newline under v", args: []string{"-v"}, stdin: "\x8a\n"},

		// Operands and their diagnostics.
		{name: "empty operand", args: []string{""}},
		{name: "missing file", args: []string{missing}},
		{name: "missing then present", args: []string{missing, ab}},
		{name: "present then missing", args: []string{ab, missing}},
		{name: "directory", args: []string{d}},
		{name: "directory between files", args: []string{ab, d, nonl}},
		{name: "directory with numbers", args: []string{"-n", ab, d, ab}},
		{name: "directory on stdin", stdinPath: d},
		{name: "directory on stdin with numbers", args: []string{"-n"}, stdinPath: d},
		{name: "name with a space", args: []string{spaced}},
		{name: "name with a quote", args: []string{quoted}},
		{name: "name that is not valid UTF-8", args: []string{raw}},
		{name: "missing name with a space", args: []string{filepath.Join(dir, "no such")}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch")}},
		{name: "dashdash", args: []string{"--"}, stdin: "p\n"},
		{name: "dashdash then an option-looking name", args: []string{"--", "-n"}},
		{name: "regular file on stdin", stdinPath: ab},
		{name: "regular file on stdin with numbers", args: []string{"-n"}, stdinPath: nonl},

		// Its own output. With bytes still to read the copy is refused;
		// an empty input has nothing to loop on and is not.
		{name: "input file is output file", args: []string{self}, stdoutPath: self},
		{name: "input file is output file among others", args: []string{ab, self, ab}, stdoutPath: self},
		{name: "stdin is the output file", args: []string{"-"}, stdinPath: self, stdoutPath: self},
		{name: "empty input file is output file", args: []string{selfEmpty}, stdoutPath: selfEmpty},
		{name: "another file appended to one", args: []string{ab}, stdoutPath: self},
		{name: "input is output with numbers", args: []string{"-n", self}, stdoutPath: self},

		// getopt.
		{name: "invalid short option", args: []string{"-x", ab}},
		{name: "invalid short option in a cluster", args: []string{"-nx", ab}},
		{name: "unrecognized long option", args: []string{"--foo", ab}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", ab}},
		{name: "flag rejects a glued value", args: []string{"--show-nonprinting=x", ab}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "n is ambiguous", args: []string{"--n", ab}},
		{name: "nu is ambiguous", args: []string{"--nu", ab}},
		{name: "number- is unique", args: []string{"--number-", blanks}},
		{name: "s is ambiguous", args: []string{"--s", ab}},
		{name: "sq is unique", args: []string{"--sq", blanks}},
		{name: "show is ambiguous", args: []string{"--show", ab}},
		{name: "show-n is unique", args: []string{"--show-n", ctrl}},
		{name: "show-e is unique", args: []string{"--show-e", ctrl}},
		{name: "show-t is unique", args: []string{"--show-t", ctrl}},
		{name: "show-a is unique", args: []string{"--show-a", ctrl}},
		{name: "posix operand then option", args: []string{ab, "-n"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-n", ab}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures. A closed stdout is found by the fstat before
		// any input is opened, so even a missing file or an empty input
		// reports it; a full one is met by the first write.
		{name: "stdout closed", args: []string{ab}, stdout: stdoutClosed},
		{name: "stdout closed with numbers", args: []string{"-n", ab}, stdout: stdoutClosed},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},
		{name: "stdout closed with a directory", args: []string{d}, stdout: stdoutClosed},
		{name: "stdout closed with a large file", args: []string{"-A", big}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{ab}, stdout: stdoutFull},
		{name: "stdout full with numbers", args: []string{"-n", ab}, stdout: stdoutFull},
		{name: "stdout full with nothing to write", args: []string{empty}, stdout: stdoutFull},
		{name: "stdout full with a missing file", args: []string{missing}, stdout: stdoutFull},
		{name: "stdout full with a directory then a file", args: []string{d, ab}, stdout: stdoutFull},
		{name: "stdout full with a file then a missing one", args: []string{ab, missing}, stdout: stdoutFull},
		{name: "stdout full with a large file", args: []string{big}, stdout: stdoutFull},
		{name: "stdout full with a large numbered file", args: []string{"-n", big}, stdout: stdoutFull},
	}
}

func TestCatParity(t *testing.T) {
	requireParity(t, "cat", catCases(t))
}

func TestCatHelpVersion(t *testing.T) {
	requireHelp(t, "cat", []string{"--help"}, 0)
	requireHelp(t, "cat", []string{"--he"}, 0)
	requireHelp(t, "cat", []string{"--help", "ignored"}, 0)
	requireVersion(t, "cat", []string{"--version"}, 0)
	requireVersion(t, "cat", []string{"--vers"}, 0)
}
