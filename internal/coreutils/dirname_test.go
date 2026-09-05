package coreutils

import "testing"

// dirname(1) is the permuting half of the getopt pair: its optstring
// is a plain `z`, so an option is found wherever it stands, and
// POSIXLY_CORRECT is what turns that off. basename(1) next door has
// the `+` and covers the stop-at-the-first-operand side.
func TestDirnameParity(t *testing.T) {
	requireParity(t, "dirname", []invocation{
		{name: "a path", args: []string{"/usr/bin/sort"}},
		{name: "one directory", args: []string{"dir/file"}},
		{name: "no slash at all", args: []string{"stdio.h"}},
		{name: "empty operand", args: []string{""}},
		{name: "dot", args: []string{"."}},
		{name: "dotdot", args: []string{".."}},
		{name: "dot slash", args: []string{"./a"}},
		{name: "a space in the directory", args: []string{"a b/c"}},
		{name: "lone dash is an operand", args: []string{"-"}},

		// The slash edges: a leading slash is a root and survives, an
		// interior run of slashes stays, trailing ones go.
		{name: "root", args: []string{"/"}},
		{name: "double slash", args: []string{"//"}},
		{name: "three slashes", args: []string{"///"}},
		{name: "under the root", args: []string{"/a"}},
		{name: "under the root with a trailing slash", args: []string{"/a/"}},
		{name: "under a double slash", args: []string{"//a"}},
		{name: "under three slashes", args: []string{"///a"}},
		{name: "interior double slash", args: []string{"//a//b"}},
		{name: "interior slashes kept", args: []string{"a//b//c//"}},
		{name: "every run of slashes", args: []string{"///a///b///"}},
		{name: "trailing slash", args: []string{"a/"}},
		{name: "trailing slashes", args: []string{"a//"}},
		{name: "no directory", args: []string{"a"}},
		{name: "dotdot component", args: []string{"a/../b"}},

		// Several operands, and -z.
		{name: "two operands", args: []string{"x", "y"}},
		{name: "three operands", args: []string{"a/b", "c/d", "e"}},
		{name: "zero", args: []string{"-z", "x", "y"}},
		{name: "zero long", args: []string{"--zero", "x"}},
		{name: "zero abbreviated", args: []string{"--ze", "a/b"}},
		{name: "zero on an empty operand", args: []string{"-z", ""}},
		{name: "repeated flag", args: []string{"-zz", "a/b"}},
		{name: "repeated flag in two tokens", args: []string{"-z", "-z", "a/b"}},

		// Permutation: an option after an operand is still an option,
		// and only `--` protects what follows it.
		{name: "operand then option", args: []string{"x", "-z"}},
		{name: "option after dashdash is an operand", args: []string{"-z", "--", "-z"}},
		{name: "dashdash then an operand", args: []string{"--", "x"}},
		{name: "dashdash protects an option", args: []string{"--", "-z"}},
		{name: "dashdash protects a dash", args: []string{"--", "-"}},
		// POSIXLY_CORRECT makes the scan stop at the first operand, so
		// the `-z` here is an operand rather than the option it is
		// without it.
		{name: "posix operand then option", args: []string{"x", "-z"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix empty value still counts as set", args: []string{"x", "-z"}, env: []string{"POSIXLY_CORRECT="}},
		{name: "posix option before the operand still an option", args: []string{"-z", "x"}, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "operand that is not valid UTF-8", args: []string{"a\xff/b"}},
		{name: "every component invalid UTF-8", args: []string{"\xff\xfe/\xff"}},

		// Usage errors: the message, the `Try …` line, exit 1.
		{name: "no operands", args: nil},
		{name: "no operands after dashdash", args: []string{"--"}},
		{name: "no operands after an option", args: []string{"-z"}},
		{name: "no operands after a long option", args: []string{"--zero"}},
		{name: "no operands after an abbreviation", args: []string{"--z"}},
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option after a valid one", args: []string{"-zy"}},
		{name: "no short help option", args: []string{"-h"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=1"}},
		{name: "empty long option name", args: []string{"--=x"}},
		{name: "flag given a value", args: []string{"--zero=1"}},
		{name: "flag given a value by abbreviation", args: []string{"--zer=1"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=x"}},
	})
}

// The permuting scan reaches `--help` and `--version` after an
// operand, where basename(1) would not.
func TestDirnameHelpVersion(t *testing.T) {
	requireHelp(t, "dirname", []string{"--help"}, 0)
	requireHelp(t, "dirname", []string{"--h"}, 0)
	requireHelp(t, "dirname", []string{"--help", "x"}, 0)
	requireHelp(t, "dirname", []string{"x", "--help"}, 0)
	requireVersion(t, "dirname", []string{"--version"}, 0)
	requireVersion(t, "dirname", []string{"--vers"}, 0)
	requireVersion(t, "dirname", []string{"x", "--version"}, 0)
}
