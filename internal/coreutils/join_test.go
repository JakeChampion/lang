package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func joinFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// joinCases is join(1)'s corpus.
//
// Two halves repay the most cases. The first is the command line: `-o`
// takes field specs as separate arguments and `-j1` / `-j2` take their
// field from the next one, so a bare word after either is ambiguous
// between an option argument and a file — and it is settled by how many
// arguments follow, not by the word. The second is the order check,
// which by default reports a disorder only once a line has already
// proved unpairable, and even then not for the line that immediately
// follows the first unpairable one.
func joinCases(t *testing.T) []invocation {
	dir := t.TempDir()
	f1 := joinFile(t, dir, "f1", "a 1\nb 2\nc 3\n")
	f2 := joinFile(t, dir, "f2", "a x\nb y\nd z\n")
	// Comma-separated, with an empty field and a leading separator.
	c1 := joinFile(t, dir, "c1", "a,1\nb,2\n")
	c2 := joinFile(t, dir, "c2", "a,x\nb,y\n")
	cempty := joinFile(t, dir, "cempty", "a,,1\nb,2,\n")
	clead := joinFile(t, dir, "clead", ",a,1\n,b,2\n")
	// Three fields, so `-o auto` has a width to take.
	w1 := joinFile(t, dir, "w1", "a 1 2\nb 3 4\n")
	// Case differences for -i.
	u1 := joinFile(t, dir, "u1", "A 1\nb 2\n")
	u2 := joinFile(t, dir, "u2", "a x\nB y\n")
	// Duplicate keys on both sides: the cross product.
	d1 := joinFile(t, dir, "d1", "a 1\na 2\nb 3\n")
	d2 := joinFile(t, dir, "d2", "a x\na y\n")
	// Out of order in file 1, with everything still pairable.
	o1 := joinFile(t, dir, "o1", "b 1\na 2\nc 3\n")
	o2 := joinFile(t, dir, "o2", "a x\nb y\nc z\n")
	// Out of order and unpairable, which is what the default check
	// waits for.
	p1 := joinFile(t, dir, "p1", "a 1\nz 2\nc 3\nb 4\n")
	p2 := joinFile(t, dir, "p2", "a x\nq y\n")
	// Out of order only past the point where the other file ended.
	q1 := joinFile(t, dir, "q1", "a 1\nc 2\nb 3\n")
	q2 := joinFile(t, dir, "q2", "a x\n")
	// Out of order with the first line unpairable from file 1, which
	// exempts the line that follows it.
	e1 := joinFile(t, dir, "e1", "b 1\na 2\n")
	e2 := joinFile(t, dir, "e2", "z x\n")
	empty := joinFile(t, dir, "empty", "")
	blank := joinFile(t, dir, "blank", "\n\na 1\n")
	// Trailing separators: a line ending in one has an empty field
	// after it, which -e fills.
	tr1 := joinFile(t, dir, "tr1", "a 1 \n")
	// No terminator on the last line.
	n1 := joinFile(t, dir, "n1", "a 1")
	n2 := joinFile(t, dir, "n2", "a x")
	// NUL-terminated records for -z.
	z1 := joinFile(t, dir, "z1", "a 1\x00b 2\x00")
	z2 := joinFile(t, dir, "z2", "a x\x00b y\x00")
	// Headers.
	h1 := joinFile(t, dir, "h1", "H1 A\na 1\n")
	h2 := joinFile(t, dir, "h2", "H2 B\na x\n")
	h3 := joinFile(t, dir, "h3", "H1 A\n")
	// A name that is not valid UTF-8, and one that needs quoting.
	raw := joinFile(t, dir, "na\xffme", "a 1\n")
	missing := filepath.Join(dir, "nosuch")
	// A directory: open(2) succeeds and read(2) fails EISDIR.
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Enough lines that the output crosses a write block.
	var big strings.Builder
	for i := 0; i < 20000; i++ {
		big.WriteString("k" + itoa(i) + " v" + itoa(i) + "\n")
	}
	b1 := joinFile(t, dir, "b1", big.String())

	return []invocation{
		// The default join.
		{name: "default", args: []string{f1, f2}},
		{name: "duplicate keys", args: []string{d1, d2}},
		{name: "empty file 1", args: []string{empty, f2}},
		{name: "empty file 2", args: []string{f1, empty}},
		{name: "both empty", args: []string{empty, empty}},
		{name: "blank lines", args: []string{blank, f2}},
		{name: "no trailing newline", args: []string{n1, n2}},
		{name: "large output", args: []string{b1, b1}},

		// -a and -v.
		{name: "a1", args: []string{"-a1", f1, f2}},
		{name: "a2", args: []string{"-a2", f1, f2}},
		{name: "a1 and a2", args: []string{"-a1", "-a2", f1, f2}},
		{name: "a with a space", args: []string{"-a", "1", f1, f2}},
		{name: "v1", args: []string{"-v1", f1, f2}},
		{name: "v2", args: []string{"-v2", f1, f2}},
		{name: "v1 and v2", args: []string{"-v1", "-v2", f1, f2}},
		{name: "v1 and a1", args: []string{"-v1", "-a1", f1, f2}},
		{name: "a1 and v1", args: []string{"-a1", "-v1", f1, f2}},
		{name: "v1 and a2", args: []string{"-v1", "-a2", f1, f2}},
		{name: "a with a leading zero", args: []string{"-a", "01", f1, f2}},
		{name: "a with a plus", args: []string{"-a", "+1", f1, f2}},
		{name: "a with a blank", args: []string{"-a", " 1", f1, f2}},
		{name: "a with two digits", args: []string{"-a", "12", f1, f2}},
		{name: "a with a bad number", args: []string{"-a", "1x", f1, f2}},
		{name: "a3", args: []string{"-a3", f1, f2}},
		{name: "a0", args: []string{"-a0", f1, f2}},
		{name: "a not a number", args: []string{"-a", "x", f1, f2}},
		{name: "a negative", args: []string{"-a", "-1", f1, f2}},
		{name: "v3", args: []string{"-v3", f1, f2}},
		{name: "v0", args: []string{"-v0", f1, f2}},

		// Join fields.
		{name: "field 2 both", args: []string{"-j", "2", f1, f2}},
		{name: "field 2 of file 1", args: []string{"-1", "2", f1, f2}},
		{name: "field 2 of file 2", args: []string{"-2", "2", f1, f2}},
		{name: "both fields named", args: []string{"-1", "2", "-2", "2", f1, f2}},
		{name: "field zero", args: []string{"-1", "0", f1, f2}},
		{name: "field not a number", args: []string{"-1", "x", f1, f2}},
		{name: "field empty", args: []string{"-1", "", f1, f2}},
		{name: "field negative", args: []string{"-1", "-1", f1, f2}},
		{name: "field with a plus", args: []string{"-1", "+1", f1, f2}},
		{name: "field with a leading zero", args: []string{"-1", "01", f1, f2}},
		{name: "field with a leading blank", args: []string{"-1", " 1", f1, f2}},
		{name: "field with a trailing blank", args: []string{"-1", "1 ", f1, f2}},
		{name: "field past uintmax", args: []string{"-1", "99999999999999999999", f1, f2}},
		{name: "field past uintmax again", args: []string{"-1", "18446744073709551616", f1, f2}},
		{name: "field past a line's width", args: []string{"-1", "4294967296", f1, f2}},
		{name: "field beyond the line", args: []string{"-1", "5", f1, f2}},
		{name: "incompatible j then 1", args: []string{"-j", "2", "-1", "1", f1, f2}},
		{name: "incompatible 1 then j", args: []string{"-1", "1", "-j", "2", f1, f2}},
		{name: "incompatible j then 2", args: []string{"-j", "2", "-2", "1", f1, f2}},
		{name: "compatible j then 1", args: []string{"-j", "2", "-1", "2", f1, f2}},
		{name: "incompatible j twice", args: []string{"-j", "2", "-j", "3", f1, f2}},
		{name: "incompatible 1 twice", args: []string{"-1", "1", "-1", "2", f1, f2}},
		{name: "compatible j twice", args: []string{"-j", "1", "-j", "1", f1, f2}},

		// The obsolete -j1 / -j2 forms, and the counting that decides
		// whether the next word is their field or a file.
		{name: "obsolete j1", args: []string{"-j1", "2", f1, f2}},
		{name: "obsolete j2", args: []string{"-j2", "2", f1, f2}},
		{name: "obsolete j1 with nothing to take", args: []string{"-j1", f1, f2}},
		{name: "obsolete j1 taking a file name", args: []string{"-j1", f1, f2, f2}},
		{name: "obsolete j1 with one file", args: []string{"-j1", "2", f1}},
		{name: "obsolete j1 with a bad field", args: []string{"-j1", "x", f1, f2}},
		{name: "obsolete j1 then two spare words", args: []string{"-j1", "2", "3", f1, f2}},
		{name: "obsolete j1 then three spare words", args: []string{"-j1", "2", "3", "4", f1, f2}},
		{name: "obsolete j1 and j2", args: []string{"-j1", "-j2", "2", "3", f1, f2}},
		{name: "obsolete j1 interrupted", args: []string{"-j1", "-i", f1, f2}},
		{name: "obsolete j1 then an option", args: []string{"-j1", "2", "-i", f1}},
		{name: "obsolete j1 after an operand", args: []string{f1, "-j1", "2", f2}},
		{name: "j with a space is not obsolete", args: []string{"-j", "1", "2", f1, f2}},
		{name: "j glued with two digits", args: []string{"-j12", f1, f2}},

		// -o.
		{name: "o one spec", args: []string{"-o", "1.1", f1, f2}},
		{name: "o comma separated", args: []string{"-o", "0,1.2,2.2", f1, f2}},
		{name: "o blank separated", args: []string{"-o", "1.1 2.2", f1, f2}},
		{name: "o tab separated", args: []string{"-o", "1.1\t2.2", f1, f2}},
		{name: "o as separate arguments", args: []string{"-o", "1.1", "2.2", f1, f2}},
		{name: "o three separate arguments", args: []string{"-o", "1.1", "2.2", "1.2", f1, f2}},
		{name: "o given twice", args: []string{"-o", "1.1", "-o", "2.2", f1, f2}},
		{name: "o with one file", args: []string{"-o", "1.1", f1}},
		{name: "o spare word with one file", args: []string{"-o", "1.1", "2.2", f1}},
		{name: "o and a third file name", args: []string{"-o", "1.1", f1, f2, f2}},
		{name: "o interrupted by an option", args: []string{"-o", "1.1", "-i", "2.2", f1, f2}},
		{name: "o followed by an option", args: []string{"-o", "1.1", "2.2", "-i", f1, f2}},
		{name: "o then dashdash", args: []string{"-o", "1.1", "--", "2.2", f1, f2}},
		{name: "o bad file number", args: []string{"-o", "3.1", f1, f2}},
		{name: "o file number zero", args: []string{"-o", "0.1", f1, f2}},
		{name: "o field zero", args: []string{"-o", "1.0", f1, f2}},
		{name: "o without a dot", args: []string{"-o", "11", f1, f2}},
		{name: "o with a leading zero", args: []string{"-o", "00", f1, f2}},
		{name: "o with file zero and a field", args: []string{"-o", "0.0", f1, f2}},
		{name: "o with two digits before the dot", args: []string{"-o", "12.1", f1, f2}},
		{name: "o with a letter for the dot", args: []string{"-o", "1x1", f1, f2}},
		{name: "o with no field after the dot", args: []string{"-o", "1.", f1, f2}},
		{name: "o with two dots", args: []string{"-o", "1..1", f1, f2}},
		{name: "o with no file before the dot", args: []string{"-o", ".1", f1, f2}},
		{name: "o with a signed file number", args: []string{"-o", "-1.1", f1, f2}},
		{name: "o with a leading blank", args: []string{"-o", " 1.1", f1, f2}},
		{name: "o with a bare file number", args: []string{"-o", "1", f1, f2}},
		{name: "o not a spec", args: []string{"-o", "x", f1, f2}},
		{name: "o empty item", args: []string{"-o", "1.1,,2.1", f1, f2}},
		{name: "o empty", args: []string{"-o", "", f1, f2}},
		{name: "o past the fields", args: []string{"-o", "1.5", f1, f2}},
		{name: "o auto", args: []string{"-o", "auto", w1, f2}},
		{name: "o auto with unpairables", args: []string{"-o", "auto", "-a1", "-a2", "-e", "Q", w1, f2}},
		{name: "o auto with an empty file", args: []string{"-o", "auto", "-a2", "-e", "Q", empty, w1}},
		{name: "o auto with a separator", args: []string{"-t,", "-o", "auto", "-a1", "-e", "Q", cempty, c2}},
		{name: "o auto twice", args: []string{"-o", "auto", "-o", "auto", f1, f2}},
		{name: "o auto with a suffix", args: []string{"-o", "auto,1.1", f1, f2}},
		{name: "o zero on an unpairable from file 2", args: []string{"-v2", "-o", "0", f1, f2}},
		{name: "o field of the other file", args: []string{"-v2", "-o", "1.1", f1, f2}},
		{name: "o zero on an unpairable from file 1", args: []string{"-v1", "-o", "0", f1, f2}},

		// -e.
		{name: "e without o", args: []string{"-a1", "-e", "E", f1, f2}},
		{name: "e with o", args: []string{"-a1", "-o", "0,1.2,2.2", "-e", "E", f1, f2}},
		{name: "e empty", args: []string{"-a1", "-o", "0,1.2,2.2", "-e", "", f1, f2}},
		{name: "e fills an empty field", args: []string{"-t,", "-o", "1.2", "-e", "E", cempty, c2}},
		{name: "e fills a trailing empty field", args: []string{"-o", "1.3", "-e", "E", tr1, tr1}},
		{name: "e needs an argument", args: []string{"-e"}},
		{name: "e then a spare word", args: []string{"-e", "X", "1.1", f1, f2}},

		// -t.
		{name: "t comma", args: []string{"-t,", c1, c2}},
		{name: "t with a space", args: []string{"-t", ",", c1, c2}},
		{name: "t empty means the whole line", args: []string{"-t", "", f1, f1}},
		{name: "t backslash zero", args: []string{"-t", "\\0", z1, z2}},
		{name: "t multi-character", args: []string{"-t", "ab", f1, f2}},
		{name: "t twice the same", args: []string{"-t,", "-t,", c1, c2}},
		{name: "t twice different", args: []string{"-t,", "-t.", c1, c2}},
		{name: "t with an empty field", args: []string{"-t,", cempty, c2}},
		{name: "t with a leading separator", args: []string{"-t,", clead, clead}},
		{name: "t with unpairables", args: []string{"-t,", "-a1", "-a2", cempty, c2}},
		{name: "t is also the output separator", args: []string{"-t,", "-o", "1.1,2.2", c1, c2}},
		{name: "t tab", args: []string{"-t", "\t", f1, f2}},

		// -i.
		{name: "ignore case", args: []string{"-i", u1, u2}},
		{name: "ignore case long", args: []string{"--ignore-case", u1, u2}},
		{name: "case matters by default", args: []string{u1, u2}},
		{name: "ignore case after the operands", args: []string{u1, u2, "-i"}},
		{name: "ignore case with POSIXLY_CORRECT", args: []string{u1, u2, "-i"}, env: []string{"POSIXLY_CORRECT=1"}},

		// -z.
		{name: "zero terminated", args: []string{"-z", z1, z2}},
		{name: "zero terminated long", args: []string{"--zero-terminated", z1, z2}},
		{name: "zero terminated over newline records", args: []string{"-z", f1, f2}},
		{name: "zero terminated with unpairables", args: []string{"-z", "-a1", "-a2", z1, z2}},

		// The order check.
		{name: "unsorted but all pairable", args: []string{o1, o2}},
		{name: "unsorted and checked", args: []string{"--check-order", o1, o2}},
		{name: "unsorted with nocheck", args: []string{"--nocheck-order", o1, o2}},
		{name: "check then nocheck", args: []string{"--check-order", "--nocheck-order", o1, o2}},
		{name: "nocheck then check", args: []string{"--nocheck-order", "--check-order", o1, o2}},
		{name: "the line after the first unpairable is exempt", args: []string{e1, e2}},
		{name: "the exempt line under check-order", args: []string{"--check-order", e1, e2}},
		{name: "unpairable then disorder", args: []string{p1, p2}},
		{name: "unpairable then disorder with a1", args: []string{"-a1", p1, p2}},
		{name: "unpairable then disorder with v1", args: []string{"-v1", p1, p2}},
		{name: "disorder past the end of the other file", args: []string{q1, q2}},
		{name: "disorder past the end with a1", args: []string{"-a1", q1, q2}},
		{name: "equal keys are in order", args: []string{"--check-order", d1, d1}},
		{name: "disorder in file 2", args: []string{"--check-order", o2, o1}},
		{name: "disorder found under -i", args: []string{"--check-order", "-i", u1, u2}},

		// --header.
		{name: "header", args: []string{"--header", h1, h2}},
		{name: "header with o", args: []string{"--header", "-o", "0,1.2,2.2", h1, h2}},
		{name: "header with v1", args: []string{"--header", "-v1", h1, h2}},
		{name: "header with a1", args: []string{"--header", "-a1", h1, h2}},
		{name: "header with a one-line file", args: []string{"--header", h3, h2}},
		{name: "header with an empty file", args: []string{"--header", empty, h2}},
		{name: "header with both empty", args: []string{"--header", empty, empty}},
		{name: "header does not order-check across itself", args: []string{"--header", "--check-order", h1, h2}},
		{name: "header with a missing file", args: []string{"--header", h1, missing}},

		// Operands.
		{name: "no operands"},
		{name: "one operand", args: []string{f1}},
		{name: "three operands", args: []string{f1, f2, f1}},
		{name: "four operands", args: []string{f1, f2, f1, f2}},
		{name: "empty operand", args: []string{"", f2}},
		{name: "operand that is not valid UTF-8", args: []string{raw, f2}},
		{name: "missing file 1", args: []string{missing, f2}},
		{name: "missing file 2", args: []string{f1, missing}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such"), f2}},
		{name: "missing name with a quote", args: []string{filepath.Join(dir, "no'such"), f2}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch"), f2}},
		{name: "directory as file 1", args: []string{d, f2}},
		{name: "directory as file 2", args: []string{f1, d}},
		{name: "stdin as file 1", args: []string{"-", f2}, stdin: "a 1\nb 2\n"},
		{name: "stdin as file 2", args: []string{f1, "-"}, stdin: "a x\nb y\n"},
		{name: "both stdin", args: []string{"-", "-"}, stdin: "a 1\n"},
		{name: "stdin names itself in the order warning", args: []string{"--check-order", "-", f2}, stdin: "b 1\na 2\n"},
		{name: "dashdash", args: []string{"--", f1, f2}},
		{name: "dashdash with three operands", args: []string{"--", f1, f2, f1}},

		// getopt.
		{name: "invalid short option", args: []string{"-x", f1, f2}},
		{name: "unrecognized long option", args: []string{"--foo", f1, f2}},
		{name: "long flag rejects a value", args: []string{"--header=x", f1, f2}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unique long prefix header", args: []string{"--hea", h1, h2}},
		{name: "unique long prefix check", args: []string{"--c", o1, o2}},
		{name: "unique long prefix nocheck", args: []string{"--n", o1, o2}},
		{name: "unique long prefix ignore", args: []string{"--ig", u1, u2}},
		{name: "h is ambiguous between header and help", args: []string{"--h", f1, f2}},
		{name: "he is ambiguous too", args: []string{"--he", f1, f2}},
		{name: "one needs an argument", args: []string{"-1"}},
		{name: "o needs an argument", args: []string{"-o"}},
		{name: "t needs an argument", args: []string{"-t"}},
		{name: "clustered flags", args: []string{"-iz", z1, z2}},
		{name: "help rejects a value", args: []string{"--help=x"}},
		{name: "version rejects a value", args: []string{"--version=x"}},

		// Write failures.
		{name: "stdout closed", args: []string{f1, f2}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{f1, f2}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{b1, b1}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{b1, b1}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty, empty}, stdout: stdoutClosed},
		{name: "stdout closed with a usage error", args: []string{f1}, stdout: stdoutClosed},
		{name: "stdout closed with an order warning", args: []string{p1, p2}, stdout: stdoutClosed},
	}
}

func TestJoinParity(t *testing.T) {
	requireParity(t, "join", joinCases(t))
}

func TestJoinHelpVersion(t *testing.T) {
	requireHelp(t, "join", []string{"--help"}, 0)
	requireHelp(t, "join", []string{"--hel"}, 0)
	requireVersion(t, "join", []string{"--version"}, 0)
	requireVersion(t, "join", []string{"--vers"}, 0)
}
