package coreutils

import "testing"

func TestDirnameParity(t *testing.T) {
	requireParity(t, "dirname", []invocation{
		// The name rule: drop the last component and the slashes before
		// it; nothing left is `.`, only slashes left is `/`.
		{name: "plain name", args: []string{"a"}},
		{name: "path", args: []string{"/a/b"}},
		{name: "deeper path", args: []string{"/a/b/c/d"}},
		{name: "deeper path with a trailing slash", args: []string{"/a/b/c/d/"}},
		{name: "relative path", args: []string{"x/y/z"}},
		{name: "trailing slash", args: []string{"a/b/"}},
		{name: "many trailing slashes", args: []string{"a/b//"}},
		{name: "doubled inner slash", args: []string{"a//b"}},
		{name: "many inner slashes", args: []string{"a///b///"}},
		{name: "root", args: []string{"/"}},
		{name: "double slash", args: []string{"//"}},
		{name: "triple slash", args: []string{"///"}},
		{name: "child of root", args: []string{"/a"}},
		{name: "child of root with a trailing slash", args: []string{"/a/"}},
		{name: "child of root with trailing slashes", args: []string{"/a//"}},
		{name: "child of a double slash", args: []string{"//a"}},
		{name: "child of a triple slash", args: []string{"///a"}},
		{name: "child of a triple slash with trailing slashes", args: []string{"///a///"}},
		{name: "leading slashes are kept", args: []string{"//a//b//"}},
		{name: "three leading slashes are kept", args: []string{"///a///b"}},
		{name: "path under a trailing slash", args: []string{"/a/b//"}},
		{name: "dot", args: []string{"."}},
		{name: "dot dot", args: []string{".."}},
		{name: "relative dot", args: []string{"./a"}},
		{name: "dot under slashes", args: []string{"//./"}},
		{name: "dot under three slashes", args: []string{"///."}},
		{name: "name with a trailing slash", args: []string{"a/"}},
		{name: "name with trailing slashes", args: []string{"a//"}},
		{name: "empty name", args: []string{""}},
		{name: "lone dash is a name", args: []string{"-"}},
		{name: "name that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "path that is not valid UTF-8", args: []string{"\xff\xfe/\xfd"}},
		{name: "several names", args: []string{"a", "/b/c", "d/e"}},

		// -z, and the scan permutes so it is found anywhere.
		{name: "-z", args: []string{"-z", "a", "b"}},
		{name: "-z after the name", args: []string{"a", "-z"}},
		{name: "-z between names", args: []string{"a", "--zero", "b"}},
		{name: "-z repeated in a cluster", args: []string{"-zz", "a"}},
		{name: "-z repeated", args: []string{"-z", "-z", "a"}},
		{name: "-z on an empty name", args: []string{"-z", ""}},
		{name: "-z on two empty names", args: []string{"-z", "", ""}},
		{name: "--zero", args: []string{"--zero", "a"}},
		{name: "--zero abbreviated", args: []string{"--z", "a"}},
		{name: "-z then dashdash", args: []string{"-z", "--", "-a"}},
		{name: "dashdash then an option-like name", args: []string{"--", "-a"}},
		{name: "posix stops at the first name", args: []string{"a", "-z"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the name", args: []string{"-z", "a"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Faults.
		{name: "no arguments"},
		{name: "-z alone", args: []string{"-z"}},
		{name: "invalid short option", args: []string{"-x", "a"}},
		{name: "invalid short option after the name", args: []string{"a", "-x"}},
		{name: "invalid short option after two names", args: []string{"a", "b", "-x"}},
		{name: "unrecognized long option", args: []string{"--foo", "a"}},
		{name: "unrecognized long option with a newline", args: []string{"--foo=a\nb", "x"}},
		{name: "empty long option is ambiguous", args: []string{"--=x", "a"}},
		{name: "--zero with a value", args: []string{"--zero=x", "a"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
	})
}

func TestDirnameHelpVersion(t *testing.T) {
	requireHelp(t, "dirname", []string{"--help"}, 0)
	requireHelp(t, "dirname", []string{"--hel"}, 0)
	requireHelp(t, "dirname", []string{"--help", "a"}, 0)
	requireHelp(t, "dirname", []string{"a", "--help"}, 0)
	requireHelp(t, "dirname", []string{"--help", "--foo"}, 0)
	requireVersion(t, "dirname", []string{"--version"}, 0)
	requireVersion(t, "dirname", []string{"--vers"}, 0)
	requireVersion(t, "dirname", []string{"--version", "a"}, 0)
}
