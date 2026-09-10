package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("unexpand", unexpandCases)
}

// unexpandCases is unexpand(1)'s corpus.
//
// The rule for when a run of blanks becomes a tab is the interesting
// half: a SINGLE blank landing on a stop is held rather than converted,
// but only held — another blank behind it turns it into a tab after all,
// and reaching the last stop dumps whatever is held as the blanks it
// was. `-t` turns on `-a` where the obsolete `-NUM` does not, and
// `--first-only` overrides `-a` whichever order they are written in.
func unexpandCases(t *testing.T) []invocation {
	dir := t.TempDir()
	lead := tabFile(t, dir, "lead", "        a       b\n")
	mid := tabFile(t, dir, "mid", "a        b\n")
	short := tabFile(t, dir, "short", "   a\n")
	mixed := tabFile(t, dir, "mixed", "  \t  x\n")
	nonl := tabFile(t, dir, "nonl", "a       ")
	rest := tabFile(t, dir, "rest", " b\n")
	empty := tabFile(t, dir, "e0", "")
	blank := tabFile(t, dir, "blank", "\n\n")
	raw := tabFile(t, dir, "na\xffme", "        a\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	big := tabFile(t, dir, "big", strings.Repeat("        col     col\n", 20000))
	plain := tabFile(t, dir, "plain", strings.Repeat("noblanks\n", 20000))

	cases := []invocation{
		// Leading blanks only, by default.
		{name: "leading blanks", args: []string{lead}},
		{name: "blanks after a word are left alone", args: []string{mid}},
		{name: "too few blanks to reach a stop", args: []string{short}},
		{name: "a tab among the leading blanks", args: []string{mixed}},
		{name: "conversion stops at the first non-blank", args: []string{}, stdin: "  \ta  \tb\n"},
		{name: "a line that is only blanks", args: []string{}, stdin: "        \n"},
		{name: "blanks past a stop", args: []string{}, stdin: "         x\n"},
		{name: "a leading tab", args: []string{}, stdin: "\ta\n"},
		{name: "empty lines", args: []string{blank}},
		{name: "empty file", args: []string{empty}},
		{name: "no final newline", args: []string{nonl}},

		// -a.
		{name: "all blanks", args: []string{"-a", mid}},
		{name: "long all option", args: []string{"--all", mid}},
		{name: "a single blank on a stop is kept", args: []string{"-a"}, stdin: "1234567 x\n"},
		{name: "a held blank becomes a tab", args: []string{"-a"}, stdin: "1234567  x\n"},
		{name: "a held blank before a tab", args: []string{"-a"}, stdin: "1234567 \t x\n"},
		{name: "a blank opening a line is converted", args: []string{"-a", "-t1"}, stdin: " b \n"},
		{name: "a blank after a word is not", args: []string{"-a", "-t1"}, stdin: "b b\n"},
		{name: "the last stop ends conversion", args: []string{"-a", "-t", "4,8"}, stdin: "abcdefg  i\n"},
		{name: "the same input with endless stops", args: []string{"-a"}, stdin: "abcdefg  i\n"},
		{name: "two blanks reaching a stop", args: []string{"-a"}, stdin: "abcdef  i\n"},
		{name: "blanks nowhere near a stop", args: []string{"-a"}, stdin: "abcde  i\n"},
		{name: "a tab swallows the blanks before it", args: []string{"-a"}, stdin: "abcde  \ti\n"},
		{name: "trailing blanks", args: []string{"-a"}, stdin: "x         \n"},
		{name: "backspace goes back a column", args: []string{"-a"}, stdin: "\b        x\n"},
		{name: "carriage return is one column", args: []string{"-a"}, stdin: "\r        x\n"},
		{name: "tabs already in place", args: []string{"-a"}, stdin: "x\t\ty\n"},

		// --first-only overrides -a in either order.
		{name: "first only", args: []string{"--first-only", lead}},
		{name: "all then first only", args: []string{"-a", "--first-only", mid}},
		{name: "first only then all", args: []string{"--first-only", "-a", mid}},
		{name: "tabs then first only", args: []string{"-t8", "--first-only", mid}},
		{name: "first only then tabs", args: []string{"--first-only", "-t8", mid}},

		// -t turns on -a; the obsolete form does not.
		{name: "tabs turn on all", args: []string{"-t8", mid}},
		{name: "tabs with a space turn on all", args: []string{"-t", "8", mid}},
		{name: "long tabs turn on all", args: []string{"--tabs=8", mid}},
		{name: "obsolete width leaves all off", args: []string{"-8", mid}},
		{name: "obsolete digits concatenate", args: []string{"-1", "-2", lead}},
		{name: "obsolete comma is an option of its own", args: []string{"-1", "-,", "-2", lead}},
		{name: "obsolete leading comma", args: []string{"-,", "-4", lead}},
		{name: "obsolete comma alone", args: []string{"-,", lead}},
		{name: "obsolete zero after a digit is ten", args: []string{"-1", "-0", lead}},
		{name: "obsolete joins an explicit list", args: []string{"-t4", "-8", lead}},
		{name: "obsolete is read after the scan", args: []string{"-8", "-t4", lead}},
		{name: "obsolete before an explicit list", args: []string{"-4", "-t8", lead}},
		{name: "obsolete rejects a trailing dash", args: []string{"-4-", lead}},
		{name: "obsolete list leaves all off", args: []string{"-4,8", mid}},
		{name: "obsolete width with all", args: []string{"-8", "-a", mid}},
		{name: "obsolete width on leading blanks", args: []string{"-4", lead}},
		{name: "obsolete after an operand", args: []string{lead, "-4"}},
		{name: "obsolete after dashdash is a file", args: []string{"--", "-4"}},
		{name: "obsolete with a bad suffix", args: []string{"-4x", lead}},
		{name: "obsolete zero", args: []string{"-0", lead}},
		{name: "obsolete clashes with an explicit list", args: []string{"-t4", "-4", lead}},

		// Stop lists.
		{name: "width four", args: []string{"-t4", lead}},
		{name: "two stops", args: []string{"-t", "4,8", lead}},
		{name: "stops then nothing", args: []string{"-a", "-t", "2,4"}, stdin: "x      y\n"},
		{name: "width one", args: []string{"-t1"}, stdin: " a b\n"},
		{name: "extend after the last stop", args: []string{"-a", "-t", "2,/4"}, stdin: "x       y\n"},
		{name: "increment after the last stop", args: []string{"-a", "-t", "2,+4"}, stdin: "x       y\n"},

		// Operands. unexpand joins files into one stream.
		{name: "no operand reads stdin", stdin: "        a\n"},
		{name: "lone dash", args: []string{"-"}, stdin: "        a\n"},
		{name: "two files join", args: []string{"-a", nonl, rest}},
		{name: "dashdash", args: []string{"--", lead}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{raw}},
		{name: "missing file", args: []string{missing}},
		{name: "missing then good", args: []string{missing, lead}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{d, lead}},

		// getopt.
		{name: "invalid short option", args: []string{"-i"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "flag rejects a glued value", args: []string{"--all=x", lead}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unique prefix all", args: []string{"--al", mid}},
		{name: "unique prefix first-only", args: []string{"--f", "-a", mid}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "posix operand then option", args: []string{lead, "-a"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout closed", args: []string{lead}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{lead}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-a", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-a", big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},

		// Bulk and block boundaries.
		{name: "large file", args: []string{"-a", big}},
		{name: "large file with no blanks", args: []string{"-a", plain}},
		{name: "line longer than one read block", args: []string{"-a"}, stdin: strings.Repeat("z", 70000) + "        x\n"},
		{name: "blanks across read blocks", args: []string{"-a"}, stdin: strings.Repeat("        x\n", 30000)},
		{name: "a wide tab stop", args: []string{"-a", "-t", "5000"}, stdin: strings.Repeat(" ", 5001) + "x\n"},
	}
	return append(cases, tabListCases("        a       b\n")...)
}

func TestUnexpandParity(t *testing.T) {
	requireParity(t, "unexpand", unexpandCases(t))
}

func TestUnexpandHelpVersion(t *testing.T) {
	requireHelp(t, "unexpand", []string{"--help"}, 0)
	requireHelp(t, "unexpand", []string{"--he"}, 0)
	requireVersion(t, "unexpand", []string{"--version"}, 0)
	requireVersion(t, "unexpand", []string{"--vers"}, 0)
	requireHelp(t, "unexpand", []string{"--tabs=0", "--help"}, 0)
	requireVersion(t, "unexpand", []string{"--tabs=4,2", "--version"}, 0)
}
