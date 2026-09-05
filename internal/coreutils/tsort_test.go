package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// tsortFile writes `content` under `dir` as `name` and returns its path.
func tsortFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func tsortCases(t *testing.T) []invocation {
	dir := t.TempDir()
	pairs := tsortFile(t, dir, "pairs", "a b\nb c\n")
	loop := tsortFile(t, dir, "loop", "a b\nb a\n")
	odd := tsortFile(t, dir, "odd", "a b c\n")
	spaced := tsortFile(t, dir, "f name", "a b\nb a\n")
	quoted := tsortFile(t, dir, "f'n", "a b c\n")
	empty := tsortFile(t, dir, "empty", "")

	return []invocation{
		// The queue phase: byte-ordered seeds, FIFO, each written name
		// freeing its successors most recent first.
		{name: "one pair", stdin: "a b\n"},
		{name: "empty input", stdin: ""},
		{name: "newline only", stdin: "\n"},
		{name: "space only", stdin: " "},
		{name: "no trailing newline", stdin: "a b"},
		{name: "chain", stdin: "a b b c\n"},
		{name: "reversed chain", stdin: "c b b a\n"},
		{name: "self pair declares a name", stdin: "a a\n"},
		{name: "self pairs only", stdin: "a a b b\n"},
		{name: "two independent pairs", stdin: "z y x w\n"},
		{name: "three independent pairs", stdin: "a b c d e f\n"},
		{name: "fan out most recent first", stdin: "a b a c a d\n"},
		{name: "fan in", stdin: "b a c a d a\n"},
		{name: "diamond", stdin: "a b b c a c b d\n"},
		{name: "join", stdin: "a c b c\n"},
		{name: "repeated pair", stdin: "a b a b\n"},
		{name: "repeated pair reversed", stdin: "b a b a\n"},
		{name: "triangle", stdin: "x y y z x z\n"},
		{name: "seed order by bytes", stdin: "a b c d b c\n"},
		{name: "numbers sort as bytes", stdin: "1 2 2 3 3 1 10 1\n"},
		{name: "prefix sorts first", stdin: "aa a a aa\n"},
		{name: "case sorts as bytes", stdin: "A a a B\n"},
		{name: "upper case after lower", stdin: "b B B a\n"},
		{name: "digits within names", stdin: "a1 a10 a10 a2\n"},
		{name: "dash names", stdin: "a-b b-c\n"},
		{name: "option-like name", stdin: "-a b\n"},
		{name: "dashdash as a name", stdin: "-- b\n"},
		{name: "names that are not valid UTF-8", stdin: "a\xff b\xfe\n"},
		{name: "loop of names that are not valid UTF-8", stdin: "a\xff b\xfe b\xfe a\xff\n"},

		// Separators: space, tab and newline, nothing else; a NUL cuts
		// the name.
		{name: "tabs and blank lines", stdin: "a  b\tc\nd\n"},
		{name: "leading and trailing spaces", stdin: " a b "},
		{name: "runs of separators", stdin: "  a   b\n\n"},
		{name: "tab separated", stdin: "a\tb"},
		{name: "newline separated", stdin: "a\nb"},
		{name: "pairs across lines", stdin: "a b\nc d"},
		{name: "carriage return is not a separator", stdin: "a\rb c d\n"},
		{name: "vertical tab and form feed are not separators", stdin: "a\vb\fc d\n"},
		{name: "NBSP is not a separator", stdin: "a\xa0b c d\n"},
		{name: "NUL is not a separator", stdin: "a\x00b c d\n"},
		{name: "NUL cuts a name", stdin: "a\x00 b c d\n"},
		{name: "NUL cuts a name to nothing", stdin: "\x00 b c d\n"},

		// Odd token counts: an error before anything is written.
		{name: "one token", stdin: "a\n"},
		{name: "three tokens", stdin: "a b c\n"},
		{name: "five tokens", stdin: "a b c d e\n"},
		{name: "odd with a loop", stdin: "a b b a c\n"},

		// Loops: the predecessor chain from the smallest unresolved
		// name, printed head first, the closing edge dropped.
		{name: "two-cycle", stdin: "a b\nb a\n"},
		{name: "two-cycle reversed", stdin: "b a a b\n"},
		{name: "three-cycle", stdin: "a b b c c a\n"},
		{name: "three-cycle from b", stdin: "b c c a a b\n"},
		{name: "three-cycle from c", stdin: "c a a b b c\n"},
		{name: "three-cycle the other way", stdin: "a c c b b a\n"},
		{name: "four-cycle", stdin: "a b\nb c\nc d\nd a\n"},
		{name: "five-cycle", stdin: "a b b c c d d e e a\n"},
		{name: "five-cycle from e", stdin: "e a a b b c c d d e\n"},
		{name: "cycle with a seed", stdin: "a b b c c a d a\n"},
		{name: "cycle with a seed from d", stdin: "a b b c c a d b\n"},
		{name: "cycle with an upstream name", stdin: "p a a b b a\n"},
		{name: "cycle with an upstream name to each", stdin: "p a p b a b b a\n"},
		{name: "two cycles", stdin: "a b b c c a d a e f f e\n"},
		{name: "two cycles across lines", stdin: "a b\nb c\nc a\nx y\ny x\n"},
		{name: "two cycles reversed", stdin: "c d d c a b b a\n"},
		{name: "cycle after a seed", stdin: "a b b c c d d b\n"},
		{name: "cycle with an extra edge", stdin: "a b b c c a a d\n"},
		{name: "cycle then a dangling name", stdin: "a b b c c d d a d e\n"},
		{name: "duplicated edge in a cycle", stdin: "a b a b b a\n"},
		{name: "two identical loops", stdin: "a b b a b a\n"},
		{name: "self pair in a cycle", stdin: "a b b a a a\n"},
		{name: "cycle with a chord", stdin: "a b b c c d d a c a\n"},
		{name: "cycle with a chord forward", stdin: "a b b c c d d a a c\n"},
		{name: "cycle with a chord from c", stdin: "c a a b b c c d d a\n"},
		{name: "cycle with a double edge", stdin: "a b b c c a a c\n"},
		{name: "cycle with a double edge first", stdin: "a c a b b c c a\n"},
		{name: "two-cycle inside a three-cycle", stdin: "a b b c c a b a\n"},
		{name: "two-cycle before a three-cycle", stdin: "b a a b b c c a\n"},
		{name: "two-cycle hanging off a three-cycle", stdin: "a b b c c a b d d b\n"},
		{name: "two-cycle hanging off renamed", stdin: "b c c d d b c a a c\n"},
		{name: "two-cycle hanging off declared first", stdin: "b d d b a b b c c a\n"},
		{name: "two-cycle hanging off the middle name", stdin: "a b b d d a b c c b\n"},
		{name: "two-cycle hanging off from c", stdin: "b c c a a b c d d c\n"},
		{name: "two two-cycles on one name", stdin: "b c c b b d d b\n"},
		{name: "two two-cycles on one name reversed", stdin: "b d d b b c c b\n"},
		{name: "two two-cycles on the seed", stdin: "a c c a a b b a\n"},
		{name: "two two-cycles on the seed reversed", stdin: "a b b a a c c a\n"},
		{name: "two two-cycles interleaved", stdin: "a b a c b a c a\n"},
		{name: "two two-cycles interleaved again", stdin: "a c a b b a c a\n"},
		{name: "two-cycle with a dangling name after", stdin: "y z z y z a\n"},
		{name: "two-cycle with a dangling name from the first", stdin: "a b b a a c\n"},
		{name: "two-cycle with a dangling name from the second", stdin: "a b b a b c\n"},
		{name: "two-cycle with a dangling name declared first", stdin: "z a y z z y\n"},
		{name: "two-cycle with a dangling chain", stdin: "y z z y y a a b\n"},
		{name: "two-cycle with a dangling chain reversed", stdin: "y z z y y b b a\n"},
		{name: "two-cycle with a dangling chain sorted after", stdin: "b c c b b d d e\n"},
		{name: "two-cycle with a dangling chain from the second", stdin: "c b b c c d d e\n"},
		{name: "two-cycle with a dangling chain declared first", stdin: "y a a b y z z y\n"},
		{name: "two-cycle with a dangling chain back", stdin: "y z z y y a a b b y\n"},
		{name: "two-cycle with a dangling two-cycle", stdin: "y z z y z a a b b a\n"},
		{name: "two-cycle feeding a dangling chain", stdin: "y z z y z a a b b c\n"},
		{name: "two-cycle feeding a dangling three-cycle", stdin: "y z z y z a a b b c c a\n"},
		{name: "two-cycle feeding a dangling two-cycle", stdin: "y z z y z a a b b c c b\n"},
		{name: "two two-cycles feeding each other", stdin: "a b b a c d d c a c\n"},
		{name: "three-cycle around a two-cycle", stdin: "x a a b b a a q q x\n"},
		{name: "three-cycle on the seed and a two-cycle", stdin: "a b b a b c c d d c\n"},
		{name: "three cycles", stdin: "a b b a b c c d d c d a\n"},
		{name: "two three-cycles sharing the seed", stdin: "a b b d d a a c c e e a\n"},
		{name: "two-cycle behind a three-cycle", stdin: "k a a b b k b c c b\n"},
		{name: "loop with a chord to the seed", stdin: "a b b c c d d e e a c e\n"},
		{name: "loop with a chord back", stdin: "a b b c c d d e e a e c\n"},
		{name: "loop with an inner loop", stdin: "a b b c c d d e e a d b\n"},
		{name: "loop with a skip", stdin: "a b b c c d d e e a b d\n"},
		{name: "loop through a dangling two-cycle", stdin: "q r r q q a a b b c c q\n"},
		{name: "loop through a dangling two-cycle to r", stdin: "q r r q q a a b b c c r\n"},
		{name: "loop of a prefix and its name", stdin: "abc abd abd abc\n"},

		// Operands: at most one, `-` for stdin.
		{name: "dash", args: []string{"-"}, stdin: "a b\n"},
		{name: "dashdash", args: []string{"--"}, stdin: "a b\n"},
		{name: "dashdash then dash", args: []string{"--", "-"}, stdin: "a b\n"},
		{name: "file", args: []string{pairs}},
		{name: "file with a loop", args: []string{loop}},
		{name: "file with odd tokens", args: []string{odd}},
		{name: "file with a space in its name", args: []string{spaced}},
		{name: "file with a quote in its name", args: []string{quoted}},
		{name: "empty file", args: []string{empty}},
		{name: "dev null", args: []string{"/dev/null"}},
		{name: "two operands", args: []string{"a", "b"}},
		{name: "file then dash", args: []string{pairs, "-"}},
		{name: "dash then file", args: []string{"-", pairs}},
		{name: "two files", args: []string{pairs, loop}},
		{name: "file then a missing file", args: []string{pairs, "/nope"}},
		{name: "extra operand with a space", args: []string{"a", "b c"}},
		{name: "extra operand with a newline", args: []string{"a", "b\nc"}},
		{name: "extra operand with a high byte", args: []string{"a", "b\xffc"}},
		{name: "extra operand with a quote", args: []string{"a", "b'c"}},
		{name: "extra operand with a control", args: []string{"a", "b\x01c"}},
		{name: "extra operand with a backslash", args: []string{"a", "b\\c"}},
		{name: "extra operand that is empty", args: []string{"a", ""}},

		// Missing files, and the shell quoting of their names.
		{name: "missing file", args: []string{"/nope"}},
		{name: "missing file under a missing directory", args: []string{"/nosuch/x"}},
		{name: "a directory operand is a read error", args: []string{"/tmp"}},
		{name: "missing relative file", args: []string{"nosuch"}},
		{name: "missing file with a trailing slash", args: []string{"nosuch/"}},
		{name: "empty operand", args: []string{""}},
		{name: "missing file with a space", args: []string{"no such"}},
		{name: "missing file with a quote", args: []string{"no'such"}},
		{name: "missing file with a double quote", args: []string{"no\"such"}},
		{name: "missing file with a backslash", args: []string{"no\\such"}},
		{name: "missing file with a dollar", args: []string{"no$such"}},
		{name: "missing file with a backquote", args: []string{"no`such"}},
		{name: "missing file with shell globs", args: []string{"no*?[such"}},
		{name: "missing file with a bracket close", args: []string{"no]such"}},
		{name: "missing file with parentheses", args: []string{"no(such)"}},
		{name: "missing file with braces", args: []string{"a{b}c"}},
		{name: "missing file with a semicolon", args: []string{"no;such"}},
		{name: "missing file with an ampersand", args: []string{"no&such"}},
		{name: "missing file with a pipe", args: []string{"no|such"}},
		{name: "missing file with redirections", args: []string{"no<such>"}},
		{name: "missing file with a caret", args: []string{"no^such"}},
		{name: "missing file with a bang", args: []string{"no!such"}},
		{name: "missing file with an equals sign", args: []string{"no=such"}},
		{name: "missing file with a colon", args: []string{"no:such"}},
		{name: "missing file with a hash", args: []string{"no#such"}},
		{name: "missing file with a leading hash", args: []string{"#nosuch"}},
		{name: "missing file with a tilde", args: []string{"no~such"}},
		{name: "missing file with a leading tilde", args: []string{"~nosuch"}},
		{name: "missing file with the unquoted punctuation", args: []string{"a%b+c,d.e@f_g-h/i"}},
		{name: "missing file with a tab", args: []string{"no\tsuch"}},
		{name: "missing file with a newline", args: []string{"no\nsuch"}},
		{name: "missing file with the named controls", args: []string{"no\a\b\f\v\rsuch"}},
		{name: "missing file with an escape byte", args: []string{"no\x1bsuch"}},
		{name: "missing file with a low control", args: []string{"no\x01such"}},
		{name: "missing file with DEL", args: []string{"no\x7fsuch"}},
		{name: "missing file with a high byte", args: []string{"no\xffsuch"}},
		{name: "missing file with UTF-8", args: []string{"no\xc3\xa9such"}},
		{name: "missing file that is one high byte", args: []string{"\xff"}},
		{name: "missing file ending in a control", args: []string{"a\x7f"}},
		{name: "missing file starting with controls", args: []string{"\t\tx"}},
		{name: "missing file ending in controls", args: []string{"x\t\t"}},
		{name: "missing file that is a control", args: []string{"\t"}},
		{name: "missing file with a quote and a dollar", args: []string{"a'b$"}},
		{name: "missing file with a quote and a double quote", args: []string{"a'b\""}},
		{name: "missing file with a quote and a backslash", args: []string{"a'b\\"}},
		{name: "missing file with a quote and a backquote", args: []string{"a'b`"}},
		{name: "missing file with a quote and a space", args: []string{"a'b c"}},
		{name: "missing file with two quotes", args: []string{"a'b'c"}},
		{name: "missing file with three quotes", args: []string{"a'''"}},
		{name: "missing file with adjacent quotes", args: []string{"a''b"}},
		{name: "missing file with a quote and a bang", args: []string{"a'b!c"}},
		{name: "missing file with a quote and a tilde", args: []string{"a'b~"}},
		{name: "missing file with a leading tilde and a quote", args: []string{"~a'b"}},
		{name: "missing file with a quote and a hash", args: []string{"a'b#"}},
		{name: "missing file with a leading hash and a quote", args: []string{"#a'b"}},
		{name: "missing file with a quote and a colon", args: []string{"a'b:c"}},
		{name: "missing file with a quote and a bracket close", args: []string{"a'b]c"}},
		{name: "missing file with a quote and a brace", args: []string{"a'b{c"}},
		{name: "missing file with a quote and a glob", args: []string{"a'b*c"}},
		{name: "missing file with a quote and an equals sign", args: []string{"a'b=c"}},
		{name: "missing file with a quote and the safe punctuation", args: []string{"a'b%,.@_+/-c"}},
		{name: "missing file that is a quote", args: []string{"'"}},
		{name: "missing file starting with a quote", args: []string{"'a"}},
		{name: "missing file ending with a quote", args: []string{"a'"}},
		{name: "missing file with a quote and a tab", args: []string{"a'b\tc"}},
		{name: "missing file with a tab then a quote", args: []string{"a\tb'c"}},
		{name: "missing file with a tab right before a quote", args: []string{"a\t'c"}},
		{name: "missing file that is a tab and a quote", args: []string{"\t'"}},
		{name: "missing file with a quote and a high byte", args: []string{"a'b\xffc"}},

		// Getopt faults.
		{name: "invalid short option", args: []string{"-x"}, stdin: "a b\n"},
		{name: "unrecognized long option", args: []string{"--foo"}, stdin: "a b\n"},
		{name: "unrecognized long option after dash", args: []string{"-", "--zz"}, stdin: "a b\n"},
		{name: "empty long option is ambiguous", args: []string{"--=x"}, stdin: "a b\n"},
		{name: "help with a value", args: []string{"--help=x"}, stdin: "a b\n"},
		{name: "version with a value", args: []string{"--version=x"}, stdin: "a b\n"},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "posix operand ends the options", args: []string{"-", "--foo"}, stdin: "a b\n", env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"--foo", "-"}, env: []string{"POSIXLY_CORRECT=1"}},
	}
}

func TestTsortParity(t *testing.T) {
	requireParity(t, "tsort", tsortCases(t))
}

func TestTsortHelpVersion(t *testing.T) {
	requireHelp(t, "tsort", []string{"--help"}, 0)
	requireHelp(t, "tsort", []string{"--hel"}, 0)
	requireHelp(t, "tsort", []string{"--help", "a"}, 0)
	requireHelp(t, "tsort", []string{"-", "--help"}, 0)
	requireHelp(t, "tsort", []string{"--help", "--foo"}, 0)
	requireVersion(t, "tsort", []string{"--version"}, 0)
	requireVersion(t, "tsort", []string{"--vers"}, 0)
	requireVersion(t, "tsort", []string{"--version", "a"}, 0)
}
