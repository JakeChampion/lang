package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tabFile writes `content` under `dir` as `name` and returns its path.
// expand and unexpand share it; their fixtures are the same shapes.
func tabFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tabListCases returns the `-t` grammar cases shared by expand and
// unexpand: the two read the list with the same code, so a divergence in
// one is a divergence in both, and neither corpus is the place to leave
// the other's half untested.
//
// The rules that are not obvious: a `/` or `+` prefix latches for the
// REST of the operand it appears in (so `-t /4,8` is a fault and
// `-t /4 -t 8` is not), an overflowing value reports and carries on
// while an invalid byte stops the parse, and the ascending / zero checks
// wait until the whole option scan is over — `-t 0 -x` names the option.
func tabListCases(stdin string) []invocation {
	lists := []string{
		"4", "8", "1", "100", "1,4,8", "4,8", "4,8,12", "2,4,6,8",
		"/4", "+4", "/1", "+1", "2,/4", "2,+4", "3,/2", "3,+2", "/0", "+0",
		"", ",,", ",4", "4,", "4,,8", "4 8", "4\t8", " 4", "4 ",
		"0", "4,0", "1,1", "4,2", "2,4,3",
		"/4,8", "+4,8", "/4,/8", "+4,+8", "/4,+8", "+4,/8", "4,/8,/9",
		"4/8", "4+8", "/4,,8", "4,/8,", "/ 4", "/,4", "9+/0", "2/-",
		"x", "1,x", "1,2x3", "-1", "+", "/", "++", "//",
	}
	// Numbers big enough to matter to the parse. They run on empty
	// input because what they pin is the parse and not the spacing: an
	// accepted stop a million columns out would otherwise be a million
	// spaces per tab. UINTMAX_MAX itself is deliberately absent — GNU
	// accepts it and then dies in malloc, which is its allocator's
	// answer rather than a defined one.
	huge := []string{
		"1000000", "18446744073709551616", "99999999999999999999",
		"99999999999999999999,5", "5,99999999999999999999",
		"99999999999999999999,88888888888888888888", "99999999999999999999x",
		"x99999999999999999999",
	}
	var out []invocation
	for _, l := range lists {
		out = append(out, invocation{name: "tab list " + quote([]byte(l)), args: []string{"-t", l}, stdin: stdin})
	}
	for _, l := range huge {
		out = append(out, invocation{name: "tab list " + quote([]byte(l)), args: []string{"-t", l}})
	}
	// Two operands: the latch is per operand, the stored size is not.
	pairs := [][2]string{
		{"/4", "8"}, {"4", "/8"}, {"/4", "/8"}, {"+4", "+8"}, {"/4", "+8"},
		{"4", "2"}, {"4", "8"}, {"0", "4"}, {"4", "0"}, {"1,2", "/3"},
		{"4,/8", "9"}, {"", "4"}, {"4", ""},
	}
	for _, p := range pairs {
		out = append(out, invocation{
			name:  "tab lists " + quote([]byte(p[0])) + " then " + quote([]byte(p[1])),
			args:  []string{"-t", p[0], "-t", p[1]},
			stdin: stdin,
		})
	}
	// The stops are checked only after the whole scan.
	out = append(out,
		invocation{name: "zero tab size then a bad option", args: []string{"-t", "0", "-Q"}, stdin: stdin},
		invocation{name: "bad option then a zero tab size", args: []string{"-Q", "-t", "0"}, stdin: stdin},
		invocation{name: "descending stops then a bad option", args: []string{"-t", "4,2", "-Q"}, stdin: stdin},
		invocation{name: "an invalid byte is reported during the scan", args: []string{"-t", "x", "-Q"}, stdin: stdin},
		invocation{name: "long tabs option", args: []string{"--tabs=4"}, stdin: stdin},
		invocation{name: "long tabs option with a space", args: []string{"--tabs", "4"}, stdin: stdin},
		invocation{name: "long tabs prefix", args: []string{"--t=4"}, stdin: stdin},
		invocation{name: "long tabs needs a value", args: []string{"--tabs"}, stdin: stdin},
		invocation{name: "short tabs needs a value", args: []string{"-t"}, stdin: stdin},
	)
	return out
}

func init() {
	registerCorpus("expand", expandCases)
}

// expandCases is expand(1)'s corpus.
//
// Beyond the tab list it is the column arithmetic: `\b` moves back a
// column AND rewinds the stop cursor, a tab past the last explicit stop
// becomes ONE space rather than a stop's worth, and `-i` stops
// converting at the first byte of the line that is not a blank. Files
// are one stream, so a FILE with no final newline runs into the next.
func expandCases(t *testing.T) []invocation {
	dir := t.TempDir()
	tabby := tabFile(t, dir, "tabby", "\t\ta\tb\n")
	lead := tabFile(t, dir, "lead", "  a\tb\tc\n")
	back := tabFile(t, dir, "back", "ab\b\tc\n")
	nonl := tabFile(t, dir, "nonl", "a\t")
	rest := tabFile(t, dir, "rest", "\tb\n")
	empty := tabFile(t, dir, "e0", "")
	blank := tabFile(t, dir, "blank", "\n\n")
	nul := tabFile(t, dir, "nul", "a\x00\tb\n")
	raw := tabFile(t, dir, "na\xffme", "a\tb\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	big := tabFile(t, dir, "big", strings.Repeat("col\tcol\tcol\n", 20000))
	plain := tabFile(t, dir, "plain", strings.Repeat("no tabs here\n", 20000))

	cases := []invocation{
		// Defaults and the column rules.
		{name: "default eight columns", args: []string{tabby}},
		{name: "explicit width", args: []string{"-t4", tabby}},
		{name: "stop list", args: []string{"-t", "1,4,8", tabby}},
		{name: "past the last stop is one space", args: []string{"-t", "4,8", tabby}},
		{name: "extend after the last stop", args: []string{"-t", "2,/4", tabby}},
		{name: "increment after the last stop", args: []string{"-t", "2,+4", tabby}},
		{name: "backspace rewinds a column", args: []string{"-t4", back}},
		{name: "backspace at column zero", args: []string{"-t4"}, stdin: "\b\b\tc\n"},
		{name: "backspace with a stop list", args: []string{"-t", "2,4,6"}, stdin: "\t\t\b\ta\n"},
		{name: "carriage return is one column", args: []string{"-t4"}, stdin: "a\r\tb\n"},
		{name: "NUL is one column", args: []string{"-t4", nul}},
		{name: "no tabs at all", args: []string{"-t4"}, stdin: "plain text\n"},
		{name: "empty lines", args: []string{"-t4", blank}},
		{name: "empty file", args: []string{"-t4", empty}},
		{name: "no final newline", args: []string{"-t4"}, stdin: "a\tb"},

		// -i.
		{name: "initial only", args: []string{"-i", "-t4", lead}},
		{name: "initial with a leading tab", args: []string{"-i", "-t4", tabby}},
		{name: "initial with no leading blank", args: []string{"-i", "-t4"}, stdin: "x\ty\n"},
		{name: "initial stops at a backspace", args: []string{"-i", "-t4"}, stdin: " \b\ta\n"},
		{name: "initial across blanks and tabs", args: []string{"-i", "-t4"}, stdin: "\t \tx\ty\n"},
		{name: "long initial option", args: []string{"--initial", "-t4", lead}},
		{name: "initial clustered with a digit", args: []string{"-i4", tabby}},

		// The obsolete -NUM form.
		{name: "obsolete width", args: []string{"-4", tabby}},
		{name: "obsolete list", args: []string{"-4,8", tabby}},
		{name: "obsolete extend", args: []string{"-2,/4", tabby}},
		{name: "obsolete after an operand", args: []string{tabby, "-4"}},
		{name: "obsolete after dashdash is a file", args: []string{"--", "-4"}},
		{name: "obsolete with a bad suffix", args: []string{"-4x", tabby}},
		{name: "obsolete zero", args: []string{"-0", tabby}},
		{name: "obsolete then explicit", args: []string{"-4", "-t8", tabby}},
		{name: "obsolete slash is not an option", args: []string{"-/4", tabby}},
		{name: "digit after a flag", args: []string{"-i", "-4", tabby}},

		// Operands. expand joins files into one stream, so an
		// unterminated line runs into the next FILE's first one.
		{name: "no operand reads stdin", stdin: "a\tb\n"},
		{name: "lone dash", args: []string{"-t4", "-"}, stdin: "a\tb\n"},
		{name: "two files join", args: []string{"-t4", nonl, rest}},
		{name: "a missing file between two that join", args: []string{"-t4", nonl, missing, rest}},
		{name: "dashdash", args: []string{"-t4", "--", tabby}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"-t4", raw}},
		{name: "missing file", args: []string{missing}},
		{name: "missing then good", args: []string{"-t4", missing, tabby}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{"-t4", d, tabby}},

		// getopt.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "flag rejects a glued value", args: []string{"--initial=x", tabby}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "posix operand then option", args: []string{tabby, "-t4"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-t4", tabby}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout closed", args: []string{"-t4", tabby}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"-t4", tabby}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-t4", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-t4", big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},

		// Bulk and block boundaries.
		{name: "large tabbed file", args: []string{"-t4", big}},
		{name: "large file with no tabs", args: []string{plain}},
		{name: "line longer than one read block", args: []string{"-t4"}, stdin: strings.Repeat("z", 70000) + "\ta\n"},
		{name: "tabs across read blocks", args: []string{"-t4"}, stdin: strings.Repeat("a\tb\n", 30000)},
		{name: "a wide tab stop", args: []string{"-t", "5000"}, stdin: "\ta\n"},
	}
	return append(cases, tabListCases("\ta\tb\n")...)
}

func TestExpandParity(t *testing.T) {
	requireParity(t, "expand", expandCases(t))
}

func TestExpandHelpVersion(t *testing.T) {
	requireHelp(t, "expand", []string{"--help"}, 0)
	requireHelp(t, "expand", []string{"--he"}, 0)
	requireVersion(t, "expand", []string{"--version"}, 0)
	requireVersion(t, "expand", []string{"--vers"}, 0)
	// The tab stops are checked only after the whole scan, so a list
	// that would be refused still gets out of the way of --help.
	requireHelp(t, "expand", []string{"--tabs=0", "--help"}, 0)
	requireVersion(t, "expand", []string{"--tabs=4,2", "--version"}, 0)
}
