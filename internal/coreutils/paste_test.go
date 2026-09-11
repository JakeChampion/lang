package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pasteFile writes `content` under `dir` as `name` and returns its path.
func pasteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("paste", pasteCases)
}

// pasteCases is paste(1)'s corpus.
//
// Unequal file lengths are where the behaviour lives: the delimiter of a
// file that has run out is HELD until a later column on the same line
// produces something, so a short first file makes the following lines
// start with delimiters. The other half is the delimiter list, where a
// NUL entry writes nothing at all and an empty list is exactly one of
// those, so `paste -d` with nothing after it concatenates.
func pasteCases(t *testing.T) []invocation {
	dir := t.TempDir()
	three := pasteFile(t, dir, "three", "1\n2\n3\n")
	two := pasteFile(t, dir, "two", "a\nb\n")
	one := pasteFile(t, dir, "one", "X\n")
	empty := pasteFile(t, dir, "e0", "")
	nonl := pasteFile(t, dir, "nonl", "1\n2")
	blanks := pasteFile(t, dir, "blanks", "\n\n\n")
	nul := pasteFile(t, dir, "nul", "1\x002\x00")
	nul2 := pasteFile(t, dir, "nul2", "a\x00")
	raw := pasteFile(t, dir, "na\xffme", "z\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Several output blocks, which is what the write-failure cases need.
	// Not more than that: the self-host leg runs this same corpus, and a
	// self-host build accumulates output quadratically (#8793), so a
	// fixture sized for the utility would be sized wrong for the gate.
	bigA := pasteFile(t, dir, "bigA", strings.Repeat("left\n", 12000))
	bigB := pasteFile(t, dir, "bigB", strings.Repeat("right\n", 12000))
	// A line longer than one read block, so a column spans reads.
	longline := pasteFile(t, dir, "long", strings.Repeat("x", 70000)+"\ntail\n")

	return []invocation{
		// Parallel.
		{name: "one file", args: []string{three}},
		{name: "two files", args: []string{three, two}},
		{name: "three files", args: []string{three, two, one}},
		{name: "shorter file first", args: []string{one, three}},
		{name: "empty file second", args: []string{three, empty}},
		{name: "empty file first", args: []string{empty, three}},
		{name: "two empty files", args: []string{empty, empty}},
		{name: "empty file alone", args: []string{empty}},
		{name: "empty between two", args: []string{three, empty, two}},
		{name: "two empties then a file", args: []string{empty, empty, three}},
		{name: "no final newline", args: []string{nonl, two}},
		{name: "no final newline alone", args: []string{nonl}},
		{name: "no final newline last", args: []string{two, nonl}},
		{name: "blank lines", args: []string{blanks, three}},
		{name: "same file twice", args: []string{three, three}},
		{name: "long line", args: []string{longline, two}},

		// -d.
		{name: "one delimiter", args: []string{"-d,", three, two}},
		{name: "delimiter list cycles", args: []string{"-d", ",;", three, two, one}},
		{name: "more delimiters than columns", args: []string{"-d", ",;.:", three, two}},
		{name: "empty delimiter", args: []string{"-d", "", three, two}},
		{name: "empty delimiter three files", args: []string{"-d", "", three, two, one}},
		{name: "NUL escape writes nothing", args: []string{"-d", `\0`, three, two}},
		{name: "NUL inside a list", args: []string{"-d", `a\0b`, three, two, one}},
		{name: "newline escape", args: []string{"-d", `\n`, three, two}},
		{name: "tab escape", args: []string{"-d", `\t`, three, two}},
		{name: "backslash escape", args: []string{"-d", `\\`, three, two}},
		{name: "every named escape", args: []string{"-d", `\b\f\n\r\t\v`, three, two, one}},
		{name: "unknown escape is the letter", args: []string{"-d", `\q`, three, two}},
		{name: "a escape is the letter", args: []string{"-d", `\a`, three, two}},
		{name: "long delimiters option", args: []string{"--delimiters=,", three, two}},
		{name: "last delimiter option wins", args: []string{"-d,", "-d;", three, two}},
		{name: "trailing backslash", args: []string{"-d", `a\`, three}},
		{name: "lone backslash", args: []string{"-d", `\`, three}},

		// -s.
		{name: "serial", args: []string{"-s", three}},
		{name: "serial two files", args: []string{"-s", three, two}},
		{name: "serial delimiter", args: []string{"-s", "-d,", three}},
		{name: "serial delimiter cycles", args: []string{"-s", "-d", ",;", three}},
		{name: "serial NUL delimiter", args: []string{"-s", "-d", `\0`, three}},
		{name: "serial empty file", args: []string{"-s", empty}},
		{name: "serial two empty files", args: []string{"-s", empty, empty}},
		{name: "serial no final newline", args: []string{"-s", nonl}},
		{name: "serial blank lines", args: []string{"-s", blanks}},
		{name: "long serial option", args: []string{"--serial", three, two}},
		{name: "serial long line", args: []string{"-s", longline}},

		// -z.
		{name: "zero terminated", args: []string{"-z", nul, nul2}},
		{name: "zero terminated serial", args: []string{"-z", "-s", nul}},
		{name: "zero terminated on newline input", args: []string{"-z", "-s", three}},
		{name: "long zero terminated option", args: []string{"--zero-terminated", nul, nul2}},

		// Operands.
		{name: "no operand reads stdin", stdin: "q\nr\n"},
		{name: "lone dash", args: []string{"-"}, stdin: "q\nr\n"},
		{name: "two dashes share stdin", args: []string{"-", "-"}, stdin: "q\nr\ns\nt\n"},
		{name: "three dashes share stdin", args: []string{"-", "-", "-"}, stdin: "q\nr\ns\nt\n"},
		{name: "dash and a file", args: []string{"-", three}, stdin: "q\nr\n"},
		{name: "file and a dash", args: []string{three, "-"}, stdin: "q\nr\n"},
		{name: "serial two dashes", args: []string{"-s", "-", "-"}, stdin: "q\nr\n"},
		{name: "dashdash", args: []string{"--", three}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{raw, three}},
		{name: "missing file", args: []string{three, missing}},
		{name: "missing file first", args: []string{missing, three}},
		{name: "two missing files name the first", args: []string{missing, missing + "2"}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "serial missing file", args: []string{"-s", missing, three}},
		{name: "directory", args: []string{three, d}},
		{name: "directory first", args: []string{d, three}},
		{name: "serial directory", args: []string{"-s", d, three}},

		// getopt.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "delimiter needs a value", args: []string{"-d"}},
		{name: "long delimiter needs a value", args: []string{"--delimiters"}},
		{name: "flag rejects a glued value", args: []string{"--serial=x", three}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unique prefix serial", args: []string{"--se", three}},
		{name: "unique prefix delimiters", args: []string{"--d=,", three, two}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "posix operand then option", args: []string{three, "-s"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout closed", args: []string{three}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{three}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{bigA, bigB}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{bigA, bigB}, stdout: stdoutFull},
		{name: "stdout closed serial", args: []string{"-s", bigA}, stdout: stdoutClosed},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},

		// Bulk.
		{name: "two large files", args: []string{bigA, bigB}},
		{name: "large serial", args: []string{"-s", bigA}},
		{name: "stdin across read blocks", args: []string{"-", three}, stdin: strings.Repeat("row\n", 20000)},
	}
}

func TestPasteParity(t *testing.T) {
	requireParity(t, "paste", pasteCases(t))
}

func TestPasteHelpVersion(t *testing.T) {
	requireHelp(t, "paste", []string{"--help"}, 0)
	requireHelp(t, "paste", []string{"--he"}, 0)
	requireVersion(t, "paste", []string{"--version"}, 0)
	requireVersion(t, "paste", []string{"--vers"}, 0)
}
