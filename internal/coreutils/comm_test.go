package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commCases is comm(1)'s corpus.
//
// Two behaviours carry most of the cases. The line ORDER is memcmp over
// the shorter line's content with the lengths as the tie-break, and the
// delimiter is outside it — `a\tx` sorts after `a`, which a comparison
// that included the delimiter would reverse. And the DEFAULT order
// check is not `--check-order`: it stays silent until an unpairable
// line has been seen, warns once per file, keeps going, and fails at
// the end — including a re-check of a file's last pair at end of input,
// because the unpairable line that turns checking on can arrive after
// that pair was read.
func commCases(t *testing.T) []invocation {
	dir := t.TempDir()
	// a d f / b d e: every column non-empty, and one line in each file
	// that the other lacks on both sides of the shared ones.
	c1 := writeFile(t, dir, "c1", "a\nb\nd\nf\n")
	c2 := writeFile(t, dir, "c2", "b\nc\nd\ne\n")
	sorted := writeFile(t, dir, "sorted", "a\nb\nc\n")
	// b a c: out of order at the FIRST pair.
	unsorted := writeFile(t, dir, "unsorted", "b\na\nc\n")
	// The end-of-input re-check: file 1's last two lines are out of
	// order, and the line that makes them worth checking is the last
	// one read.
	tailBad := writeFile(t, dir, "tailbad", "b\na\n")
	tailOne := writeFile(t, dir, "tailone", "b\n")
	tailTwo := writeFile(t, dir, "tailtwo", "b\nz\n")
	empty := writeFile(t, dir, "empty", "")
	// The tie-break: `a\tx` is longer than `a`, so it sorts after it
	// even though its next byte (TAB) is below the newline.
	tab := writeFile(t, dir, "tab", "a\tx\n")
	bare := writeFile(t, dir, "bare", "a\n")
	longer := writeFile(t, dir, "longer", "ab\n")
	blank := writeFile(t, dir, "blank", "\na\n")
	// No trailing delimiter: gnulib adds one, so the last line still
	// pairs with a terminated one.
	nonl := writeFile(t, dir, "nonl", "a\nb")
	nonlz := writeFile(t, dir, "nonlz", "a\x00b")
	z1 := writeFile(t, dir, "z1", "a\x00b\x00")
	z2 := writeFile(t, dir, "z2", "b\x00c\x00")
	// Newline-separated data read as NUL-terminated: one line each, so
	// the whole file is the key.
	spaced := writeFile(t, dir, "f name", "a\n")
	quoted := writeFile(t, dir, "f'n", "a\n")
	raw := writeFile(t, dir, "na\xffme", "a\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Both sides larger than one read block and one write block, with
	// every third line missing from the second so all three columns
	// cross a block boundary. The keys are fixed width because comm's
	// input has to be sorted in the C locale and a varying width would
	// not be.
	big1 := writeFile(t, dir, "big1", fixedKeys(0, 30000, 1))
	big2 := writeFile(t, dir, "big2", fixedKeys(0, 30000, 3))
	// One line longer than a read block on each side.
	long1 := writeFile(t, dir, "long1", strings.Repeat("y", 200000)+"\n")
	long2 := writeFile(t, dir, "long2", strings.Repeat("y", 200000)+"\n")

	return []invocation{
		// Columns.
		{name: "three columns", args: []string{c1, c2}},
		{name: "suppress 1", args: []string{"-1", c1, c2}},
		{name: "suppress 2", args: []string{"-2", c1, c2}},
		{name: "suppress 3", args: []string{"-3", c1, c2}},
		{name: "suppress 1 and 2", args: []string{"-12", c1, c2}},
		{name: "suppress 1 and 3", args: []string{"-13", c1, c2}},
		{name: "suppress 2 and 3", args: []string{"-23", c1, c2}},
		{name: "suppress all", args: []string{"-123", c1, c2}},
		{name: "repeated suppressions", args: []string{"-1", "-1", "-2", "-2", c1, c2}},
		{name: "separate suppressions", args: []string{"-1", "-2", c1, c2}},

		// --total: printed whatever the columns say, and with the same
		// delimiter.
		{name: "total", args: []string{"--total", c1, c2}},
		{name: "total with columns suppressed", args: []string{"--total", "-12", c1, c2}},
		{name: "total with everything suppressed", args: []string{"--total", "-123", c1, c2}},
		{name: "total of two empty files", args: []string{"--total", empty, empty}},
		{name: "total of one empty file", args: []string{"--total", empty, c2}},
		{name: "total prefix", args: []string{"--tot", c1, c2}},

		// --output-delimiter, including the empty one (a NUL) and the
		// refusal to take two different ones.
		{name: "output delimiter", args: []string{"--output-delimiter=:", c1, c2}},
		{name: "output delimiter of two bytes", args: []string{"--output-delimiter=XY", c1, c2}},
		{name: "empty output delimiter is NUL", args: []string{"--output-delimiter=", c1, c2}},
		{name: "output delimiter with total", args: []string{"--output-delimiter=:", "--total", c1, c2}},
		{name: "output delimiter twice the same", args: []string{"--output-delimiter=:", "--output-delimiter=:", c1, c2}},
		{name: "output delimiter twice differing", args: []string{"--output-delimiter=:", "--output-delimiter=;", c1, c2}},
		{name: "output delimiter then empty", args: []string{"--output-delimiter=:", "--output-delimiter=", c1, c2}},
		{name: "empty output delimiter then one", args: []string{"--output-delimiter=", "--output-delimiter=:", c1, c2}},
		{name: "empty output delimiter twice", args: []string{"--output-delimiter=", "--output-delimiter=", c1, c2}},
		{name: "output delimiter with a space", args: []string{"--output-delimiter= ", c1, c2}},
		{name: "output delimiter needs a value", args: []string{"--output-delimiter", c1}},
		{name: "output delimiter prefix", args: []string{"--out=:", c1, c2}},

		// Order checking. The default is silent while everything pairs.
		{name: "unsorted first file", args: []string{unsorted, sorted}},
		{name: "unsorted second file", args: []string{sorted, unsorted}},
		{name: "both files equally unsorted pair up", args: []string{unsorted, unsorted}},
		{name: "check order", args: []string{"--check-order", unsorted, sorted}},
		{name: "check order on sorted input", args: []string{"--check-order", sorted, sorted}},
		{name: "check order on a pairing but unsorted input", args: []string{"--check-order", unsorted, unsorted}},
		{name: "nocheck order", args: []string{"--nocheck-order", unsorted, sorted}},
		{name: "check then nocheck", args: []string{"--check-order", "--nocheck-order", unsorted, sorted}},
		{name: "nocheck then check", args: []string{"--nocheck-order", "--check-order", unsorted, sorted}},
		{name: "check order prefix", args: []string{"--check", unsorted, sorted}},
		{name: "nocheck order prefix", args: []string{"--nocheck", unsorted, sorted}},
		{name: "check order with columns suppressed", args: []string{"-12", unsorted, sorted}},
		// The end-of-input re-check.
		{name: "disorder in the last pair", args: []string{tailBad, tailOne}},
		{name: "disorder in the last pair with more to come", args: []string{tailBad, tailTwo}},
		{name: "disorder in the last pair reversed", args: []string{tailOne, tailBad}},
		{name: "disorder in the last pair against a sorted file", args: []string{tailBad, sorted}},
		{name: "a one-line file has no pair to re-check", args: []string{tailOne, sorted}},
		{name: "check order finds the last pair", args: []string{"--check-order", tailBad, tailOne}},

		// The line order itself.
		{name: "a tab sorts after the shorter line", args: []string{tab, bare}},
		{name: "the shorter line first", args: []string{bare, tab}},
		{name: "a prefix sorts first", args: []string{longer, bare}},
		{name: "an empty line sorts first", args: []string{blank, bare}},
		{name: "an unterminated last line still pairs", args: []string{nonl, c1}},
		{name: "an unterminated last line under -z", args: []string{"-z", nonlz, z1}},

		// -z.
		{name: "zero terminated", args: []string{"-z", z1, z2}},
		{name: "long zero terminated", args: []string{"--zero-terminated", z1, z2}},
		{name: "zero terminated prefix", args: []string{"--zero", z1, z2}},
		{name: "zero terminated with a total", args: []string{"-z", "--total", z1, z2}},
		{name: "zero terminated with a delimiter", args: []string{"-z", "--output-delimiter=:", z1, z2}},
		{name: "newline data read as zero terminated", args: []string{"-z", c1, c2}},

		// Operands.
		{name: "first is stdin", args: []string{"-", c2}, stdin: "a\nb\nd\nf\n"},
		{name: "second is stdin", args: []string{c1, "-"}, stdin: "b\nc\nd\ne\n"},
		// Both `-` is one stream in GNU, read alternately, and the
		// second close of it fails.
		{name: "both are stdin", args: []string{"-", "-"}, stdin: "a\nb\nd\nf\n"},
		{name: "both are stdin with an odd line count", args: []string{"-", "-"}, stdin: "a\nb\nc\n"},
		{name: "both are stdin with no input", args: []string{"-", "-"}},
		{name: "the same file twice", args: []string{c1, c1}},
		{name: "no operands", args: nil},
		{name: "one operand", args: []string{c1}},
		{name: "three operands", args: []string{c1, c2, c1}},
		{name: "four operands", args: []string{c1, c2, c1, c2}},
		{name: "empty first operand", args: []string{"", c2}},
		{name: "empty second operand", args: []string{c1, ""}},
		{name: "missing first file", args: []string{missing, c2}},
		{name: "missing second file", args: []string{c1, missing}},
		{name: "both files missing", args: []string{missing, missing}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such"), c2}},
		{name: "missing name with a quote", args: []string{filepath.Join(dir, "no'such"), c2}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch"), c2}},
		{name: "first is a directory", args: []string{d, c2}},
		{name: "second is a directory", args: []string{c1, d}},
		{name: "name with a space", args: []string{spaced, bare}},
		{name: "name with a quote", args: []string{quoted, bare}},
		{name: "name that is not valid UTF-8", args: []string{raw, bare}},
		{name: "dashdash then operands", args: []string{"--", c1, c2}},
		{name: "dashdash before an option-looking name", args: []string{"--", "-1", c2}},
		{name: "two empty files", args: []string{empty, empty}},
		{name: "empty against a file", args: []string{empty, c2}},
		{name: "file against empty", args: []string{c1, empty}},

		// Blocks.
		{name: "both larger than a read block", args: []string{big1, big2}},
		{name: "large with a total", args: []string{"--total", big1, big2}},
		{name: "large with columns suppressed", args: []string{"-12", big1, big2}},
		{name: "lines longer than a read block", args: []string{long1, long2}},
		{name: "stdin larger than a read block", args: []string{"-", big2}, stdin: fixedKeys(0, 30000, 1)},

		// getopt.
		{name: "invalid short option", args: []string{"-x", c1, c2}},
		{name: "digit that is not an option", args: []string{"-4", c1, c2}},
		{name: "zero is not an option", args: []string{"-0", c1, c2}},
		{name: "unrecognized long option", args: []string{"--foo", c1, c2}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", c1, c2}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "total rejects a glued value", args: []string{"--total=x", c1, c2}},
		{name: "check-order rejects a glued value", args: []string{"--check-order=x", c1, c2}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "c is check-order", args: []string{"--c", c1, c2}},
		{name: "n is nocheck-order", args: []string{"--n", c1, c2}},
		{name: "o is output-delimiter", args: []string{"--o", c1, c2}},
		{name: "t is total", args: []string{"--t", c1, c2}},
		{name: "z is zero-terminated", args: []string{"--z", c1, c2}},
		{name: "posix option after the operands", args: []string{c1, c2, "-1"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "option after the operands", args: []string{c1, c2, "-1"}},

		// Write failures. The output is closed before the disorder
		// status is reported, so a failing stdout replaces it.
		{name: "stdout closed", args: []string{c1, c2}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{c1, c2}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{big1, big2}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{big1, big2}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{"-123", c1, c2}, stdout: stdoutClosed},
		{name: "stdout closed with a total", args: []string{"--total", "-123", c1, c2}, stdout: stdoutClosed},
		{name: "stdout closed with a disorder warning", args: []string{unsorted, sorted}, stdout: stdoutClosed},
		{name: "stdout full with a disorder warning", args: []string{unsorted, sorted}, stdout: stdoutFull},
		{name: "stdout closed with a disorder warning and no output", args: []string{"-123", unsorted, sorted}, stdout: stdoutClosed},
		{name: "stdout full with a disorder warning and no output", args: []string{"-123", unsorted, sorted}, stdout: stdoutFull},
		{name: "stdout full under check-order", args: []string{"--check-order", unsorted, sorted}, stdout: stdoutFull},
		{name: "stdout closed with a missing file", args: []string{missing, c2}, stdout: stdoutClosed},
	}
}

// fixedKeys is `step`-spaced fixed-width keys in [lo, hi).
func fixedKeys(lo, hi, step int) string {
	var b strings.Builder
	for i := lo; i < hi; i += step {
		b.WriteString("key-")
		s := itoa(i)
		for n := len(s); n < 8; n++ {
			b.WriteByte('0')
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestCommParity(t *testing.T) {
	requireParity(t, "comm", commCases(t))
}

func TestCommHelpVersion(t *testing.T) {
	requireHelp(t, "comm", []string{"--help"}, 0)
	requireHelp(t, "comm", []string{"--h"}, 0)
	requireHelp(t, "comm", []string{"--help", "ignored"}, 0)
	requireVersion(t, "comm", []string{"--version"}, 0)
	requireVersion(t, "comm", []string{"--v"}, 0)
}
