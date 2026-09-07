package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile puts `content` under `dir` as `name` and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// outFile reserves a path in a directory of its own for a case that
// writes one, and returns the path plus the `prepare` that puts the
// directory back to empty before each side runs. Each writing case gets
// its own directory so the self-host leg can still run them in parallel.
func outFile(t *testing.T, name string) (string, func(*testing.T)) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	return p, func(t *testing.T) {
		t.Helper()
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
}

// outCwd is outFile for an operand that has to be spelled relative: it
// returns the working directory to run in, the path to compare, and the
// `prepare` that empties the directory before each side runs.
func outCwd(t *testing.T, name string) (string, string, func(*testing.T)) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	return dir, p, func(t *testing.T) {
		t.Helper()
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
}

// uniqCases is uniq(1)'s corpus.
//
// Three things here are not what the synopsis suggests, and each has its
// own block below. The second operand is an OUTPUT file, opened
// truncating before the input is read. The comparison is of a FIELD —
// `-f` fields, then `-s` bytes, then at most `-w` of what is left — so
// `-w0` makes every line equal and `-f` past the end makes every field
// empty. And the option scan hands operands back interleaved: `+N` is
// the obsolete skip-chars count until a file operand has been seen, and
// only while `_POSIX2_VERSION` is below 200112.
func uniqCases(t *testing.T) []invocation {
	dir := t.TempDir()
	// a a b a c c c: four groups, two of them repeated, and the same
	// line ("a") in two non-adjacent groups — uniq collapses only what
	// is ADJACENT, so `-c` reports it twice.
	d1 := writeFile(t, dir, "d1", "a\na\nb\na\nc\nc\nc\n")
	// Leading blanks, a tab, and a field that differs only in the last
	// column: the fixture every -f / -s / -w case reads.
	const fieldsText = "  aa bb cc\n  aa bb dd\nxx bb cc\n\tzz bb cc\n"
	fields := writeFile(t, dir, "fields", fieldsText)
	// A copy of it for the cases where the operand BEFORE it might turn
	// out to be a file name, which would make this one the output and
	// truncate it. Restored before each side runs and compared after, so
	// a build that reads the obsolete-operand rule wrong is caught here
	// instead of quietly poisoning every later case that shares the
	// fixture.
	fieldsCopy := func() (string, func(*testing.T)) {
		d := t.TempDir()
		p := filepath.Join(d, "fields")
		return p, func(t *testing.T) {
			t.Helper()
			if err := os.WriteFile(p, []byte(fieldsText), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	fOverflow, prepOverflow := fieldsCopy()
	fBlank, prepBlank := fieldsCopy()
	fGarbage, prepGarbage := fieldsCopy()
	fDashdash, prepDashdash := fieldsCopy()
	fP200112, prepP200112 := fieldsCopy()
	fPHuge, prepPHuge := fieldsCopy()
	fP200808, prepP200808 := fieldsCopy()
	nonl := writeFile(t, dir, "nonl", "a\na")
	nonl2 := writeFile(t, dir, "nonl2", "a\nab")
	empty := writeFile(t, dir, "e0", "")
	nul := writeFile(t, dir, "nul", "a\x00a\x00b\x00")
	// A NUL-terminated line holding newlines: they are still field
	// separators under -z, which is the only place that shows.
	nulnl := writeFile(t, dir, "nulnl", "x\ny\x00x\nz\x00")
	folded := writeFile(t, dir, "folded", "A\na\nB\n")
	// Non-ASCII that differs only in the case bit of its second byte:
	// C-locale folding leaves both alone, so they stay different.
	high := writeFile(t, dir, "high", "\xc3\xa9\n\xc3\x89\n")
	spaced := writeFile(t, dir, "f name", "x\nx\n")
	quoted := writeFile(t, dir, "f'n", "x\nx\n")
	raw := writeFile(t, dir, "na\xffme", "x\nx\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// More than one output block, so a write failure is met while
	// streaming rather than only at the final flush, and so the line
	// loop crosses a read block too.
	var b strings.Builder
	for i := 0; i < 40000; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i / 3))
		b.WriteByte('\n')
	}
	big := writeFile(t, dir, "big", b.String())
	// One line longer than a read block, so a group spans chunks.
	long := writeFile(t, dir, "long", strings.Repeat("z", 200000)+"\n"+strings.Repeat("z", 200000)+"\n")

	out1, prep1 := outFile(t, "out")
	out2, prep2 := outFile(t, "out")
	out3, prep3 := outFile(t, "out")
	out4, prep4 := outFile(t, "out")
	// Two operands that can only be written as relative names, so the
	// child gets a working directory of its own for each.
	dashDir, dashOut, prepDash := outCwd(t, "-c")
	plusDir, plusOut, prepPlus := outCwd(t, "+2")
	// An output operand that is also the input: the truncation happens
	// first, so the read finds nothing.
	selfDir := t.TempDir()
	self := filepath.Join(selfDir, "self")
	prepSelf := func(t *testing.T) {
		t.Helper()
		if err := os.WriteFile(self, []byte("a\na\nb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return []invocation{
		// The four output modes and every combination of them.
		{name: "default", args: []string{d1}},
		{name: "count", args: []string{"-c", d1}},
		{name: "repeated", args: []string{"-d", d1}},
		{name: "all repeated", args: []string{"-D", d1}},
		{name: "unique", args: []string{"-u", d1}},
		{name: "count repeated", args: []string{"-cd", d1}},
		{name: "count unique", args: []string{"-cu", d1}},
		{name: "repeated unique", args: []string{"-du", d1}},
		{name: "all repeated unique", args: []string{"-Du", d1}},
		{name: "repeated then all repeated", args: []string{"-dD", d1}},
		{name: "all repeated then repeated", args: []string{"-Dd", d1}},
		{name: "count and all repeated is refused", args: []string{"-cD", d1}},
		{name: "all repeated and count is refused", args: []string{"-Dc", d1}},
		// The refusal is decided before the input is opened.
		{name: "count and all repeated beats a missing file", args: []string{"-cD", missing}},
		{name: "long count", args: []string{"--count", d1}},
		{name: "long repeated", args: []string{"--repeated", d1}},
		{name: "long unique", args: []string{"--unique", d1}},

		// --all-repeated's separator modes. The short -D always resets
		// the mode to none, whichever order the two are given in.
		{name: "all repeated none", args: []string{"--all-repeated=none", d1}},
		{name: "all repeated prepend", args: []string{"--all-repeated=prepend", d1}},
		{name: "all repeated separate", args: []string{"--all-repeated=separate", d1}},
		{name: "all repeated bare", args: []string{"--all-repeated", d1}},
		{name: "all repeated prefix", args: []string{"--all-repeated=sep", d1}},
		{name: "all repeated rejects both", args: []string{"--all-repeated=both", d1}},
		{name: "all repeated rejects append", args: []string{"--all-repeated=append", d1}},
		{name: "all repeated rejects a bad method", args: []string{"--all-repeated=bogus", d1}},
		{name: "all repeated empty method is ambiguous", args: []string{"--all-repeated=", d1}},
		{name: "short D after a method", args: []string{"--all-repeated=prepend", "-D", d1}},
		{name: "method after short D", args: []string{"-D", "--all-repeated=prepend", d1}},
		// A cluster: -D takes no value, so the rest is -s with a value.
		{name: "D takes no value in a cluster", args: []string{"-Dseparate", d1}},
		{name: "separate with one group", args: []string{"--all-repeated=separate"}, stdin: "a\na\n"},
		{name: "prepend with one group", args: []string{"--all-repeated=prepend"}, stdin: "a\na\n"},
		{name: "separate with no input", args: []string{"--all-repeated=separate"}},

		// --group prints every line, and is exclusive with the four
		// output modes.
		{name: "group", args: []string{"--group", d1}},
		{name: "group separate", args: []string{"--group=separate", d1}},
		{name: "group prepend", args: []string{"--group=prepend", d1}},
		{name: "group append", args: []string{"--group=append", d1}},
		{name: "group both", args: []string{"--group=both", d1}},
		{name: "group prefix", args: []string{"--group=s", d1}},
		{name: "group rejects none", args: []string{"--group=none", d1}},
		{name: "group rejects a bad method", args: []string{"--group=bogus", d1}},
		{name: "group empty method is ambiguous", args: []string{"--group=", d1}},
		{name: "group with count is refused", args: []string{"--group", "-c", d1}},
		{name: "group with repeated is refused", args: []string{"--group", "-d", d1}},
		{name: "group with all repeated is refused", args: []string{"--group", "-D", d1}},
		{name: "group with unique is refused", args: []string{"--group", "-u", d1}},
		{name: "count before group is refused", args: []string{"-c", "--group", d1}},
		{name: "group with ignore case is fine", args: []string{"--group", "-i", folded}},
		{name: "group of one line", args: []string{"--group=both"}, stdin: "a\n"},
		{name: "group append with no input", args: []string{"--group=append"}},
		{name: "group both with no input", args: []string{"--group=both"}},
		{name: "group prepend with no input", args: []string{"--group=prepend"}},
		{name: "group over fields", args: []string{"--group", "-f1", fields}},
		{name: "group zero terminated", args: []string{"--group", "-z", nul}},
		{name: "group of a directory", args: []string{"--group", d}},

		// Skipping: fields, then chars, then a compare width.
		{name: "skip one field", args: []string{"-c", "-f1", fields}},
		{name: "skip two fields", args: []string{"-c", "-f2", fields}},
		{name: "skip no fields", args: []string{"-c", "-f0", fields}},
		{name: "skip more fields than there are", args: []string{"-c", "-f10", fields}},
		{name: "skip two chars", args: []string{"-c", "-s2", fields}},
		{name: "skip three chars", args: []string{"-c", "-s3", fields}},
		{name: "compare two chars", args: []string{"-c", "-w2", fields}},
		{name: "compare zero chars", args: []string{"-c", "-w0", fields}},
		{name: "field then char", args: []string{"-c", "-f1", "-s1", fields}},
		{name: "field then width", args: []string{"-c", "-f1", "-w2", fields}},
		{name: "field then a char count past the end", args: []string{"-c", "-f1", "-s100", fields}},
		{name: "width past the end", args: []string{"-c", "-w100", fields}},
		{name: "long skip fields", args: []string{"-c", "--skip-fields=1", fields}},
		{name: "long skip chars", args: []string{"-c", "--skip-chars=2", fields}},
		{name: "long check chars", args: []string{"-c", "--check-chars=2", fields}},
		{name: "last skip fields wins", args: []string{"-c", "-f2", "-f1", fields}},
		{name: "skip fields with a space", args: []string{"-c", "-f", "1", fields}},
		{name: "skip fields saturates", args: []string{"-c", "-f18446744073709551616", fields}},
		{name: "skip fields at size max", args: []string{"-c", "-f18446744073709551615", fields}},
		{name: "check chars saturates", args: []string{"-c", "-w18446744073709551616", fields}},
		{name: "skip chars saturates", args: []string{"-c", "-s99999999999999999999", fields}},
		{name: "leading blank in a count", args: []string{"-c", "-f", " 1", fields}},
		{name: "plus in a count", args: []string{"-c", "-f+1", fields}},

		// Malformed counts: one line, exit 1, and NO `Try …` line.
		{name: "skip fields needs a value", args: []string{"-f"}},
		{name: "skip fields is not a number", args: []string{"-fx", d1}},
		{name: "skip fields is negative", args: []string{"-f-1", d1}},
		{name: "skip fields has trailing garbage", args: []string{"-f1x", d1}},
		{name: "skip fields is hex", args: []string{"-f0x10", d1}},
		{name: "skip fields takes no suffix", args: []string{"-f1k", d1}},
		{name: "skip fields is empty", args: []string{"-f", "", d1}},
		{name: "trailing blank in a count", args: []string{"-f", "1 ", d1}},
		{name: "skip chars is not a number", args: []string{"-sx", d1}},
		{name: "skip chars is negative", args: []string{"-s-1", d1}},
		{name: "check chars is not a number", args: []string{"-wx", d1}},
		{name: "check chars is negative", args: []string{"-w-1", d1}},
		// The count is validated in argv order, before the next option.
		{name: "a bad count beats a later bad option", args: []string{"-fx", "-Q", d1}},
		{name: "a bad count beats a third operand", args: []string{"-fx", d1, d1, d1}},

		// -i folds only the C locale's case pairs.
		{name: "ignore case", args: []string{"-ic", folded}},
		{name: "long ignore case", args: []string{"-c", "--ignore-case", folded}},
		{name: "ignore case within a width", args: []string{"-icw1"}, stdin: "Ax\nay\nB\n"},
		{name: "ignore case leaves high bytes alone", args: []string{"-ic", high}},
		{name: "ignore case with different lengths", args: []string{"-icw5"}, stdin: "ab\nA\n"},

		// -z: NUL terminates a line, newline still separates its fields.
		{name: "zero terminated", args: []string{"-z", nul}},
		{name: "zero terminated count", args: []string{"-zc", nul}},
		{name: "long zero terminated", args: []string{"--zero-terminated", nul}},
		{name: "zero terminated prefix", args: []string{"--zero", nul}},
		{name: "newline is not a terminator under -z", args: []string{"-zc", d1}},
		{name: "newline is a field separator under -z", args: []string{"-z", "-f1", "-c", nulnl}},
		{name: "zero terminated all repeated", args: []string{"-zD", nul}},

		// The final line gets its delimiter, so it can still match.
		{name: "unterminated last line", args: []string{"-c", nonl}},
		{name: "unterminated last line differs", args: []string{"-c", nonl2}},
		{name: "unterminated from stdin", args: []string{"-c"}, stdin: "a\na"},
		{name: "unterminated under -z", args: []string{"-zc"}, stdin: "a\x00a"},
		{name: "empty file", args: []string{"-c", empty}},
		{name: "empty stdin", args: []string{"-c"}},
		{name: "only newlines", args: []string{"-c"}, stdin: "\n\n\n"},
		{name: "one line", args: []string{"-c"}, stdin: "a\n"},

		// Blocks: more lines than one read or one write block holds, and
		// one line longer than either.
		{name: "many lines", args: []string{"-c", big}},
		{name: "many lines grouped", args: []string{"--group", big}},
		{name: "many lines all repeated", args: []string{"-D", big}},
		{name: "a line longer than a read block", args: []string{"-c", long}},

		// The obsolete `-N` (fields) and `+N` (chars) forms.
		{name: "obsolete one field", args: []string{"-c", "-1", fields}},
		{name: "obsolete two fields", args: []string{"-c", "-2", fields}},
		{name: "obsolete ten fields", args: []string{"-c", "-10", fields}},
		{name: "obsolete digits accumulate", args: []string{"-c", "-1", "-2", fields}},
		{name: "obsolete two chars", args: []string{"-c", "+2", fields}},
		{name: "obsolete three chars", args: []string{"-c", "+3", fields}},
		{name: "obsolete fields and chars", args: []string{"-c", "-1", "+1", fields}},
		{name: "obsolete digit restarts after -f", args: []string{"-c", "-f1", "-1", fields}},
		{name: "obsolete digit before -f", args: []string{"-c", "-1", "-f1", fields}},
		{name: "obsolete digit after -f after a digit", args: []string{"-c", "-1", "-f2", "-3", fields}},
		{name: "obsolete zero", args: []string{"-c", "-0", fields}},
		{name: "obsolete digits saturate", args: []string{"-c", "-99999999999999999999", fields}},
		{name: "obsolete chars at size max", args: []string{"-c", "+18446744073709551615", fields}},
		{name: "obsolete chars overflow is a file", args: []string{"-c", "+99999999999999999999999", fOverflow}, prepare: prepOverflow, artifacts: []string{fOverflow}},
		{name: "obsolete chars with a blank is a file", args: []string{"-c", "+ 1", fBlank}, prepare: prepBlank, artifacts: []string{fBlank}},
		{name: "obsolete chars with garbage is a file", args: []string{"-c", "+1x", fGarbage}, prepare: prepGarbage, artifacts: []string{fGarbage}},
		{name: "obsolete chars after the input", args: []string{"-c", fields, "+2"}},
		{name: "obsolete fields after the input", args: []string{"-c", fields, "-1"}},
		{name: "obsolete chars after dashdash is a file", args: []string{"-c", "--", "+2", fDashdash}, prepare: prepDashdash, artifacts: []string{fDashdash}},
		{name: "option after the input", args: []string{fields, "-c"}},

		// _POSIX2_VERSION retires the `+N` form at 200112.
		{name: "posix2 200112 makes +N a file", args: []string{"-c", "+2", fP200112}, env: []string{"_POSIX2_VERSION=200112"}, prepare: prepP200112, artifacts: []string{fP200112}},
		{name: "posix2 199209 keeps +N", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=199209"}},
		{name: "posix2 200809 keeps +N", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=200809"}},
		// The window that refuses `+N` is closed at BOTH ends: POSIX
		// withdrew the form in 1003.1-2001 and allowed it again in 2008.
		{name: "posix2 200808 still makes +N a file", args: []string{"-c", "+2", fP200808}, env: []string{"_POSIX2_VERSION=200808"}, prepare: prepP200808, artifacts: []string{fP200808}},
		{name: "posix2 200810 keeps +N", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=200810"}},
		{name: "posix2 200111 keeps +N", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=200111"}},
		{name: "posix2 that is not a number", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=abc"}},
		{name: "posix2 empty", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION="}},
		{name: "posix2 with a trailing blank", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=199209 "}},
		{name: "posix2 negative", args: []string{"-c", "+2", fields}, env: []string{"_POSIX2_VERSION=-1"}},
		{name: "posix2 far too large", args: []string{"-c", "+2", fPHuge}, env: []string{"_POSIX2_VERSION=99999999999999999999"}, prepare: prepPHuge, artifacts: []string{fPHuge}},

		// POSIXLY_CORRECT ends the scan at the first FILE operand — but
		// `+N` before one is still the obsolete count.
		{name: "posix option after the input", args: []string{fields, "-c"}, env: []string{"POSIXLY_CORRECT=1"}, dir: dashDir, prepare: prepDash, artifacts: []string{dashOut}},
		{name: "posix option before the input", args: []string{"-c", fields}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix obsolete chars before the input", args: []string{"-c", "+2", fields}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix obsolete chars leading", args: []string{"+2", "-c", fields}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix obsolete chars after the input", args: []string{"-c", fields, "+2"}, env: []string{"POSIXLY_CORRECT=1"}, dir: plusDir, prepare: prepPlus, artifacts: []string{plusOut}},

		// Operands: an input, an output, and no more.
		{name: "input and output", args: []string{d1, out1}, prepare: prep1, artifacts: []string{out1}},
		{name: "count into an output", args: []string{"-c", d1, out2}, prepare: prep2, artifacts: []string{out2}},
		{name: "stdin into an output", args: []string{"-", out3}, stdin: "x\nx\ny\n", prepare: prep3, artifacts: []string{out3}},
		{name: "output truncates", args: []string{d1, out4}, prepare: func(t *testing.T) {
			t.Helper()
			prep4(t)
			if err := os.WriteFile(out4, []byte(strings.Repeat("stale\n", 100)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, artifacts: []string{out4}},
		{name: "input is also the output", args: []string{self, self}, prepare: prepSelf, artifacts: []string{self}},
		{name: "output dash is stdout", args: []string{d1, "-"}},
		{name: "input dash is stdin", args: []string{"-"}, stdin: "x\nx\ny\n"},
		{name: "both operands dash", args: []string{"-", "-"}, stdin: "x\nx\ny\n"},
		{name: "no operand reads stdin", stdin: "x\nx\ny\n"},
		{name: "three operands", args: []string{d1, d1, d1}},
		{name: "four operands", args: []string{d1, d1, d1, d1}},
		{name: "dashdash alone", args: []string{"--"}, stdin: "x\nx\n"},
		{name: "empty input operand", args: []string{""}},
		{name: "empty output operand", args: []string{d1, ""}},
		{name: "missing input", args: []string{missing}},
		{name: "missing input with an output", args: []string{missing, out1}, prepare: prep1, artifacts: []string{out1}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "missing name with a quote", args: []string{filepath.Join(dir, "no'such")}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(dir, "no\xffsuch")}},
		{name: "name with a space", args: []string{"-c", spaced}},
		{name: "name with a quote", args: []string{"-c", quoted}},
		{name: "name that is not valid UTF-8", args: []string{"-c", raw}},
		{name: "input is a directory", args: []string{d}},
		{name: "input is a directory with -c", args: []string{"-c", d}},
		{name: "output is a directory", args: []string{d1, d}},
		{name: "output cannot be created", args: []string{d1, filepath.Join(dir, "nodir", "out")}},

		// getopt: the messages, the prefix rule, the declaration order.
		{name: "invalid short option", args: []string{"-x", d1}},
		{name: "invalid short option in a cluster", args: []string{"-cx", d1}},
		{name: "unrecognized long option", args: []string{"--foo", d1}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", d1}},
		{name: "flag rejects a glued value", args: []string{"--count=x", d1}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "c is ambiguous", args: []string{"--c", d1}},
		{name: "s is ambiguous", args: []string{"--s", d1}},
		{name: "a is all-repeated", args: []string{"--a", d1}},
		{name: "g is group", args: []string{"--g", d1}},
		{name: "i is ignore-case", args: []string{"--i", d1}},
		{name: "u is unique", args: []string{"--u", d1}},
		{name: "z is zero-terminated", args: []string{"--z", d1}},
		{name: "r is repeated", args: []string{"--r", d1}},
		{name: "f is not an option", args: []string{"--f", "1", d1}},
		{name: "w is not an option", args: []string{"--w", "1", d1}},
		{name: "long skip fields needs a value", args: []string{"--skip-fields"}},
		{name: "long check chars needs a value", args: []string{"--check-chars"}},

		// Write failures.
		{name: "stdout closed", args: []string{d1}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{d1}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{big}, stdout: stdoutFull},
		// Nothing is written, so a closed stdout is not an error.
		{name: "stdout closed with nothing to write", args: []string{"-du", d1}, stdout: stdoutClosed},
		{name: "stdout closed with an empty input", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing input", args: []string{missing}, stdout: stdoutClosed},
		{name: "stdout closed with an output file", args: []string{d1, out1}, stdout: stdoutClosed, prepare: prep1, artifacts: []string{out1}},

		// stdin.
		{name: "stdin all repeated", args: []string{"-D"}, stdin: "a\na\nb\nc\nc\n"},
		{name: "stdin larger than one read block", args: []string{"-c"}, stdin: strings.Repeat("x\n", 40000)},
		{name: "stdin groups across read blocks", args: []string{"-c"}, stdin: strings.Repeat("x\ny\n", 20000)},
		{name: "stdin all unique", args: []string{"-u"}, stdin: "a\nb\nc\n"},
		{name: "stdin all the same", args: []string{"-c"}, stdin: "a\na\na\n"},
	}
}

func TestUniqParity(t *testing.T) {
	requireParity(t, "uniq", uniqCases(t))
}

func TestUniqHelpVersion(t *testing.T) {
	requireHelp(t, "uniq", []string{"--help"}, 0)
	requireHelp(t, "uniq", []string{"--h"}, 0)
	requireHelp(t, "uniq", []string{"--help", "ignored"}, 0)
	requireVersion(t, "uniq", []string{"--version"}, 0)
	requireVersion(t, "uniq", []string{"--v"}, 0)
}
