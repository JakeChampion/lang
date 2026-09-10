package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cutFile writes `content` under `dir` as `name` and returns its path.
func cutFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("cut", cutCases)
}

// cutCases is cut(1)'s corpus.
//
// The list grammar is the half with the most corners: overlapping ranges
// merge but adjacent ones do not, `N-0` is a decreasing range where `N-`
// is an open one, blanks separate as commas do but a newline does not,
// and UINTMAX_MAX is refused even though it fits. The other half is the
// field machinery, where a line with no delimiter is printed whole, a
// delimiter equal to the LINE delimiter makes the whole file one record,
// and the trailing line delimiter of an unterminated final record turns
// on whether gnulib had to buffer the first field to judge it.
func cutCases(t *testing.T) []invocation {
	dir := t.TempDir()
	f := cutFile(t, dir, "f", "a:b:c:d\nnodelim\n:x::y:\n")
	tabs := cutFile(t, dir, "tabs", "a\tb\tc\n")
	nonl := cutFile(t, dir, "nonl", "a:b:c")
	nul := cutFile(t, dir, "nul", "a:b\x00c:d\x00")
	empty := cutFile(t, dir, "e0", "")
	blank := cutFile(t, dir, "blank", "\n\n")
	raw := cutFile(t, dir, "na\xffme", "x:y\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Bigger than one output block, so a write failure is met while
	// streaming rather than only at the final flush.
	big := cutFile(t, dir, "big", strings.Repeat("alpha:beta:gamma\n", 20000))
	// A file whose last line has no terminator, for the record-end rules.
	part := cutFile(t, dir, "part", "one:two\nthree:")

	return []invocation{
		// Bytes and characters: the same thing in the C locale.
		{name: "byte one", args: []string{"-b1", f}},
		{name: "char one", args: []string{"-c1", f}},
		{name: "byte range", args: []string{"-b", "2-4", f}},
		{name: "byte open range", args: []string{"-b", "3-", f}},
		{name: "byte leading range", args: []string{"-b", "-3", f}},
		{name: "byte list", args: []string{"-b", "1,3,5", f}},
		{name: "byte overlapping ranges merge", args: []string{"-b", "1-3,2-5", f}},
		{name: "byte out of order ranges", args: []string{"-b", "5-,1-2", f}},
		{name: "byte repeated position", args: []string{"-b", "1,1,1", f}},
		{name: "byte blank separator", args: []string{"-b", "1 3", f}},
		{name: "byte tab separator", args: []string{"-b", "1\t3", f}},
		{name: "byte past the line", args: []string{"-b", "40", f}},
		{name: "byte range past the line", args: []string{"-b", "3-40", f}},
		{name: "byte whole line", args: []string{"-b", "1-", f}},
		{name: "byte four billion", args: []string{"-b", "4294967295", f}},
		{name: "byte on a file with no final newline", args: []string{"-b", "1", nonl}},
		{name: "byte on empty lines", args: []string{"-b", "1", blank}},
		{name: "byte on an empty file", args: []string{"-b", "1", empty}},
		{name: "byte -n is ignored", args: []string{"-n", "-b", "2-4", f}},
		{name: "byte -n glued", args: []string{"-nb", "1", f}},
		{name: "characters long option", args: []string{"--characters=2-4", f}},
		{name: "bytes long option", args: []string{"--bytes", "2-4", f}},

		// --complement.
		{name: "complement bytes", args: []string{"--complement", "-b", "2-4", f}},
		{name: "complement everything", args: []string{"--complement", "-b", "1-", f}},
		{name: "complement nothing selected", args: []string{"--complement", "-b", "100", f}},
		{name: "complement fields", args: []string{"--complement", "-d:", "-f2", f}},
		{name: "complement all fields", args: []string{"--complement", "-d:", "-f1-", f}},
		{name: "complement with only-delimited", args: []string{"-s", "--complement", "-d:", "-f1", f}},

		// --output-delimiter separates RANGES under -b and FIELDS under -f.
		{name: "output delimiter between byte ranges", args: []string{"-b", "1-2,4-5", "--output-delimiter=X", f}},
		{name: "output delimiter between single bytes", args: []string{"-b", "1,3", "--output-delimiter=X", f}},
		{name: "adjacent ranges are two ranges", args: []string{"-b", "1-2,3-4", "--output-delimiter=X", f}},
		{name: "output delimiter after complement", args: []string{"-b", "1-2", "--complement", "--output-delimiter=X", f}},
		{name: "empty output delimiter is NUL", args: []string{"-b", "1,3", "--output-delimiter=", f}},
		{name: "multi-byte output delimiter", args: []string{"-d:", "-f1,3", "--output-delimiter=XY", f}},
		{name: "output delimiter between fields", args: []string{"-d:", "-f1-2,3-4", "--output-delimiter=X", f}},
		{name: "output delimiter on a non-delimited line", args: []string{"-d:", "-f1,2", "--output-delimiter=@", f}},

		// Fields.
		{name: "field one", args: []string{"-d:", "-f1", f}},
		{name: "field list", args: []string{"-d:", "-f2,4", f}},
		{name: "field open range", args: []string{"-d:", "-f2-", f}},
		{name: "field past the line", args: []string{"-d:", "-f9", f}},
		{name: "field range past the line", args: []string{"-d:", "-f2-9", f}},
		{name: "default delimiter is tab", args: []string{"-f2", tabs}},
		{name: "long fields option", args: []string{"--fields=2", "--delimiter=:", f}},
		{name: "only delimited", args: []string{"-s", "-d:", "-f2", f}},
		{name: "long only delimited", args: []string{"--only-delimited", "-d:", "-f2", f}},
		{name: "only delimited on empty lines", args: []string{"-s", "-d:", "-f1", blank}},
		{name: "only delimited picks nothing", args: []string{"-s", "-d:", "-f9", f}},
		{name: "fields with no final newline", args: []string{"-d:", "-f2", nonl}},
		{name: "fields from an empty file", args: []string{"-d:", "-f1", empty}},
		{name: "unterminated final record", args: []string{"-d:", "-f1", part}},
		{name: "unterminated final record second field", args: []string{"-d:", "-f2", part}},
		{name: "unterminated final record beyond", args: []string{"-d:", "-f5", part}},
		{name: "unterminated final record suppressed", args: []string{"-s", "-d:", "-f1", part}},

		// The delimiter itself.
		{name: "empty delimiter is NUL", args: []string{"-d", "", "-f1", nul}},
		{name: "empty delimiter second field", args: []string{"-d", "", "-f2", nul}},
		{name: "delimiter is a letter", args: []string{"-da", "-f2", f}},
		{name: "delimiter equal to the line delimiter", args: []string{"-d", "\n", "-f1", f}},
		{name: "delimiter equal to the line delimiter open range", args: []string{"-d", "\n", "-f1-", f}},
		{name: "delimiter equal to the line delimiter beyond", args: []string{"-d", "\n", "-f9", f}},
		{name: "delimiter equal to the line delimiter suppressed", args: []string{"-s", "-d", "\n", "-f1", f}},
		{name: "delimiter equal to the line delimiter no delimiter at all", args: []string{"-d", "\n", "-f2"}, stdin: "abc"},
		{name: "delimiter equal to the line delimiter suppressed no delimiter", args: []string{"-s", "-d", "\n", "-f1"}, stdin: "abc"},
		{name: "last delimiter wins", args: []string{"-d,", "-d:", "-f2", f}},

		// -z.
		{name: "zero terminated bytes", args: []string{"-z", "-b1", nul}},
		{name: "zero terminated fields", args: []string{"-z", "-d:", "-f2", nul}},
		{name: "zero terminated long", args: []string{"--zero-terminated", "-d:", "-f1", nul}},
		{name: "zero terminated with a NUL delimiter", args: []string{"-z", "-d", "", "-f1", nul}},
		{name: "zero terminated output delimiter", args: []string{"-z", "-d:", "-f1,2", "--output-delimiter=X", nul}},
		{name: "zero terminated unterminated buffered", args: []string{"-z", "-d:", "-f2"}, stdin: "ab:"},
		{name: "zero terminated unterminated unbuffered", args: []string{"-z", "-d:", "-f1"}, stdin: "ab:"},
		{name: "zero terminated unterminated suppressed", args: []string{"-z", "-s", "-d:", "-f1"}, stdin: "ab:"},
		{name: "zero terminated unterminated with content", args: []string{"-z", "-d:", "-f5"}, stdin: "ab:cd"},
		{name: "newline terminated unterminated buffered", args: []string{"-d:", "-f2"}, stdin: "ab:"},

		// List faults. Every one prints a line and the `Try …` line.
		{name: "no list", args: []string{f}},
		{name: "two lists", args: []string{"-b1", "-f1", f}},
		{name: "two lists same kind", args: []string{"-f1", "-f2", f}},
		{name: "byte zero", args: []string{"-b", "0", f}},
		{name: "field zero", args: []string{"-f", "0", f}},
		{name: "byte zero in a range", args: []string{"-b", "0-3", f}},
		{name: "decreasing range", args: []string{"-b", "3-1", f}},
		{name: "field decreasing range", args: []string{"-f", "3-1", "-d:", f}},
		{name: "range to zero", args: []string{"-b", "1-0", f}},
		{name: "leading range to zero", args: []string{"-b", "-0", f}},
		{name: "lone dash", args: []string{"-b", "-", f}},
		{name: "two dashes", args: []string{"-b", "1-2-3", f}},
		{name: "two dashes in a field list", args: []string{"-f", "1-2-3", "-d:", f}},
		{name: "empty list", args: []string{"-b", "", f}},
		{name: "trailing comma", args: []string{"-b", "1,", f}},
		{name: "empty element", args: []string{"-b", "1,,3", f}},
		{name: "lone comma", args: []string{"-b", ",", f}},
		{name: "leading blank", args: []string{"-b", " 3", f}},
		{name: "trailing blank", args: []string{"-b", "3 ", f}},
		{name: "newline is not a separator", args: []string{"-b", "1\n3", f}},
		{name: "not a number", args: []string{"-b", "x", f}},
		{name: "trailing garbage", args: []string{"-b", "1x", f}},
		{name: "garbage after a dash", args: []string{"-b", "1-x", f}},
		{name: "field not a number", args: []string{"-f", "x", "-d:", f}},
		{name: "plus is not accepted", args: []string{"-b", "+3", f}},
		{name: "too large", args: []string{"-b", "99999999999999999999", f}},
		{name: "too large in a range", args: []string{"-b", "1-99999999999999999999", f}},
		{name: "uintmax is too large", args: []string{"-b", "18446744073709551615", f}},
		{name: "past uintmax", args: []string{"-b", "18446744073709551616", f}},
		{name: "two too-large numbers name the first", args: []string{"-b", "99999999999999999999,88888888888888888888", f}},
		{name: "field too large", args: []string{"-f", "99999999999999999999", "-d:", f}},

		// Mode faults, in GNU's order: the list first, then -d, then -s.
		{name: "delimiter without fields", args: []string{"-b1", "-d:", f}},
		{name: "delimiter before the list", args: []string{"-d:", "-b1", f}},
		{name: "tab delimiter still counts as specified", args: []string{"-d", "\t", "-b1", f}},
		{name: "suppress without fields", args: []string{"-b1", "-s", f}},
		{name: "delimiter fault beats the suppress fault", args: []string{"-b1", "-d:", "-s", f}},
		{name: "list fault waits for the mode faults", args: []string{"-b0", "-d:", f}},
		{name: "list fault after a good mode", args: []string{"-z", "-b0", f}},
		{name: "no list beats everything", args: []string{"-s", "-d:", f}},
		{name: "multi-character delimiter", args: []string{"-d::", "-f1", f}},
		{name: "multi-character delimiter is found during the scan", args: []string{"-d::", "-f1", "-x", f}},
		{name: "delimiter fault beats a later list fault", args: []string{"-f0", "-d::", f}},

		// Operands.
		{name: "no operand reads stdin", args: []string{"-b1"}, stdin: "abc\ndef\n"},
		{name: "lone dash is stdin", args: []string{"-b1", "-"}, stdin: "abc\n"},
		{name: "dashdash", args: []string{"-b1", "--", f}},
		{name: "two files", args: []string{"-b1", f, f}},
		{name: "two files do not join", args: []string{"-b1", nonl, f}},
		{name: "fields do not join across files", args: []string{"-d:", "-f2", part, f}},
		{name: "empty operand", args: []string{"-b1", ""}},
		{name: "missing file", args: []string{"-b1", missing}},
		{name: "missing then good", args: []string{"-b1", missing, f}},
		{name: "good then missing", args: []string{"-b1", f, missing}},
		{name: "operand that is not valid UTF-8", args: []string{"-d:", "-f2", raw}},
		{name: "missing name that needs quoting", args: []string{"-b1", filepath.Join(dir, "no such")}},
		{name: "directory", args: []string{"-b1", d}},
		{name: "directory then a file", args: []string{"-b1", d, f}},

		// getopt.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "short option needs a value", args: []string{"-d"}},
		{name: "long delimiter needs a value", args: []string{"--delimiter"}},
		{name: "output delimiter needs a value", args: []string{"--output-delimiter"}},
		{name: "flag rejects a glued value", args: []string{"--complement=x", "-b1", f}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "c is ambiguous", args: []string{"--c", "1", f}},
		{name: "o is ambiguous", args: []string{"--o"}},
		{name: "unique prefix bytes", args: []string{"--by", "1", f}},
		{name: "unique prefix zero", args: []string{"--z", "-b1", nul}},
		{name: "n has no long name", args: []string{"--n", "-b1", f}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "posix operand then option", args: []string{f, "-b1"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-b1", f}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout closed", args: []string{"-b1", f}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"-b1", f}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-d:", "-f2", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-d:", "-f2", big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{"-b1", empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{"-b1", missing}, stdout: stdoutClosed},

		// stdin across read blocks.
		{name: "stdin larger than one read block", args: []string{"-b", "1-3"}, stdin: strings.Repeat("abcdef\n", 20000)},
		{name: "fields across read blocks", args: []string{"-d:", "-f2"}, stdin: strings.Repeat("alpha:beta:gamma\n", 8000)},
		{name: "a line longer than one read block", args: []string{"-d:", "-f2"}, stdin: strings.Repeat("x", 70000) + ":tail\n"},
		{name: "a field longer than one read block", args: []string{"-d:", "-f1"}, stdin: strings.Repeat("y", 70000) + ":tail\n"},
	}
}

func TestCutParity(t *testing.T) {
	requireParity(t, "cut", cutCases(t))
}

func TestCutHelpVersion(t *testing.T) {
	requireHelp(t, "cut", []string{"--help"}, 0)
	requireHelp(t, "cut", []string{"--he"}, 0)
	requireVersion(t, "cut", []string{"--version"}, 0)
	requireVersion(t, "cut", []string{"--vers"}, 0)
}
