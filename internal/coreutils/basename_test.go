package coreutils

import "testing"

// basename(1) is the first utility here with options of its own, so
// its corpus is also the corpus for the getopt_long emulation in
// lib/gnu.fern: clusters, a value glued or separate, the refusals and
// their exact wording. What it cannot reach is permutation — its
// optstring is `+as:z`, so the scan stops at the first operand, and
// `x -a y` is three operands. dirname(1) covers the permuting side.
func TestBasenameParity(t *testing.T) {
	requireParity(t, "basename", []invocation{
		{name: "a path", args: []string{"/usr/bin/sort"}},
		{name: "no directory at all", args: []string{"sort"}},
		{name: "empty operand", args: []string{""}},
		{name: "dot", args: []string{"."}},
		{name: "dotdot", args: []string{".."}},
		{name: "lone dash is an operand", args: []string{"-"}},

		// Slashes: leading ones are not part of a component, trailing
		// ones are dropped, and a name that is all slashes is `/`.
		{name: "root", args: []string{"/"}},
		{name: "double slash", args: []string{"//"}},
		{name: "three slashes", args: []string{"///"}},
		{name: "leading slashes", args: []string{"////a"}},
		{name: "trailing slash", args: []string{"a/b/"}},
		{name: "trailing slashes", args: []string{"a/b//"}},
		{name: "interior slashes", args: []string{"a//b"}},

		// The two-operand form: the second operand is the suffix.
		{name: "suffix removed", args: []string{"a.txt", ".txt"}},
		{name: "suffix is the whole name", args: []string{"a.txt", "a.txt"}},
		{name: "suffix is the whole dotfile", args: []string{".txt", ".txt"}},
		{name: "suffix is one letter", args: []string{"aa", "a"}},
		{name: "suffix need not start with a dot", args: []string{"abc", "c"}},
		{name: "suffix under a directory", args: []string{"/foo/bar/baz.c", ".c"}},
		{name: "suffix longer than the name", args: []string{"a", "aaa"}},
		{name: "suffix not present", args: []string{"a.c", ".h"}},
		{name: "empty suffix", args: []string{"a.c", ""}},
		{name: "empty name and suffix", args: []string{"", ""}},
		// A name that came out starting with a slash keeps its suffix.
		{name: "suffix against a root name", args: []string{"/", "/"}},
		{name: "suffix against a double-slash name", args: []string{"//", "/"}},
		{name: "the slash was stripped before the suffix", args: []string{"a/", "/"}},

		// -a / -s / -z.
		{name: "multiple", args: []string{"-a", "/a/b", "/c/d"}},
		{name: "multiple with one operand", args: []string{"-a", "x"}},
		{name: "suffix implies multiple", args: []string{"-s", ".c", "/a/b.c", "/c/d.c"}},
		{name: "suffix glued to its letter", args: []string{"-s.c", "a.c"}},
		{name: "suffix as a long option value", args: []string{"--suffix=.c", "a.c"}},
		{name: "suffix as a separate long value", args: []string{"--suffix", ".c", "a.c"}},
		{name: "empty suffix option", args: []string{"-s", "", "a.c"}},
		{name: "empty long suffix value", args: []string{"--suffix=", "a.c"}},
		{name: "zero", args: []string{"-z", "x"}},
		{name: "zero and multiple", args: []string{"-z", "-a", "x", "y"}},
		{name: "long forms", args: []string{"--zero", "--multiple", "x", "y"}},
		{name: "unique prefixes", args: []string{"--m", "x", "y"}},
		{name: "unique prefix with a value", args: []string{"--s", ".c", "x.c"}},
		{name: "unique prefix with a glued value", args: []string{"--su=1", "a1"}},
		{name: "empty names under -a", args: []string{"-a", "", ""}},
		{name: "empty names under -z", args: []string{"-z", "-a", "", ""}},
		{name: "slash names under -a", args: []string{"-a", "///", "//", "/", "a/"}},

		// Clusters, repeats and values that look like options.
		{name: "cluster ending in a valued option", args: []string{"-as", ".c", "x.c"}},
		{name: "cluster of flags", args: []string{"-za", "x", "y"}},
		{name: "value glued after a cluster", args: []string{"-as.c", "x.c"}},
		{name: "repeated flag", args: []string{"-zz", "x"}},
		{name: "repeated flag in two tokens", args: []string{"-aa", "x", "y"}},
		{name: "the last suffix wins", args: []string{"-s", ".c", "-s", ".h", "a.h"}},
		{name: "the last suffix wins the other way", args: []string{"-s", ".h", "-s", ".c", "a.h"}},
		{name: "a value that looks like an option", args: []string{"-s", "-a", "x"}},
		{name: "a value that is dashdash", args: []string{"-s", "--", "x"}},

		// `--` ends the options; the scan stops at the first operand.
		{name: "dashdash alone", args: []string{"--"}},
		{name: "dashdash then a name", args: []string{"--", "x"}},
		{name: "dashdash protects an option", args: []string{"--", "-a", "x"}},
		{name: "dashdash protects a dash", args: []string{"--", "-"}},
		{name: "options after dashdash under -a", args: []string{"-a", "--", "x", "y"}},
		{name: "an operand stops the scan", args: []string{"x", "-a", "y"}},
		// Once the scan has stopped, `--` is an operand like any other.
		{name: "dashdash after an operand", args: []string{"x", "--", "y"}},
		{name: "dashdash after an operand under -a", args: []string{"-a", "x", "--", "y"}},
		{name: "a valued option after an operand", args: []string{"-a", "x", "-s", ".c"}},
		{name: "a long option after an operand", args: []string{"--multiple", "x", "--zero"}},
		{name: "an operand stops the scan before help", args: []string{"x", "--help"}},
		{name: "an operand stops the scan before version", args: []string{"x", "--version"}},
		{name: "an operand stops the scan before a bad option", args: []string{"-a", "x", "-z"}},
		// POSIXLY_CORRECT changes nothing: the scan already stops.
		{name: "posix operand then option", args: []string{"x", "-a", "y"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix multiple", args: []string{"-a", "x", "y"}, env: []string{"POSIXLY_CORRECT="}},

		// Operands that are not valid UTF-8, in the name and in the
		// suffix: both are matched as bytes.
		{name: "name that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "component that is not valid UTF-8", args: []string{"-a", "a\xffb/c"}},
		{name: "suffix that is not valid UTF-8", args: []string{"-s", "\xff", "x\xff"}},

		// Usage errors: the message, the `Try …` line, exit 1.
		{name: "no operands", args: nil},
		{name: "no operands after an option", args: []string{"-a"}},
		{name: "no operands after a suffix", args: []string{"-s", ".c"}},
		{name: "no operands after a glued suffix", args: []string{"-sa.c"}},
		{name: "no operands after dashdash", args: []string{"--"}},
		{name: "extra operand", args: []string{"x", "y", "z"}},
		{name: "short option missing its value", args: []string{"-s"}},
		{name: "short option missing its value in a cluster", args: []string{"-as"}},
		{name: "short option missing its value at the end", args: []string{"-a", "-s"}},
		{name: "long option missing its value", args: []string{"--suffix"}},
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option after a valid one", args: []string{"-ax"}},
		{name: "invalid short option before a valid one", args: []string{"-xa"}},
		{name: "short forms of the long options are not options", args: []string{"-h"}},
		{name: "no short version option", args: []string{"-V"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "unrecognized long option that is a dash", args: []string{"---"}},
		{name: "an abbreviation that matches nothing", args: []string{"--supp", "a"}},
		{name: "an abbreviation past the whole name", args: []string{"--zeroo", "x"}},
		{name: "empty long option name", args: []string{"--=x"}},
		{name: "flag given a value", args: []string{"--zero=1"}},
		{name: "flag given a value by abbreviation", args: []string{"--z=1"}},
		{name: "multiple given a value", args: []string{"--multiple=1"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=x"}},
	})
}

// The unique-prefix rule on the two outputs whose text is ours, and
// the scan reaching an option that stands after another one.
func TestBasenameHelpVersion(t *testing.T) {
	requireHelp(t, "basename", []string{"--help"}, 0)
	requireHelp(t, "basename", []string{"--h"}, 0)
	requireHelp(t, "basename", []string{"--help", "x"}, 0)
	requireHelp(t, "basename", []string{"-a", "--help", "x"}, 0)
	requireVersion(t, "basename", []string{"--version"}, 0)
	requireVersion(t, "basename", []string{"--vers"}, 0)
	requireVersion(t, "basename", []string{"--version", "x"}, 0)
}
