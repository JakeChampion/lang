package coreutils

import (
	"testing"
)

// realpath(1) runs the same walk readlink's `-f` / `-e` / `-m` do
// (`coreutils/lib/canon.fern`), so this corpus does not re-prove the
// walk: readlink's covers the modes, the trailing slash, `..`, the
// cycle and the dangling component. What is here is what realpath
// adds.
//
// Two of them are easy to conflate and are not the same axis. `-e` /
// `-m` say how much has to EXIST. `-P` / `-L` / `-s` say what happens
// to the LINKS, and the three are one setting where the last one
// wins: `-P` follows a link where it stands, `-s` reads no link at
// all, and `-L` does both in order — a lexical pass that settles `..`
// against the name as written, then a physical pass over its answer.
// `sublink/..` separates all three, and `-P -s` / `-s -P` prove the
// last-wins.
//
// The rest is `--relative-to` and `--relative-base`, which are string
// work over two resolved paths. The rule that is easy to get wrong:
// with a base, the output is relative only when the path AND the
// directory measured from are both under it, so a base that the
// `--relative-to` directory is not under makes every answer absolute.
// A directory that will not resolve at all ends the program before any
// operand is printed, and `-q` does not cover that.

func init() {
	registerCorpus("realpath", realpathCases)
}

func realpathCases(t *testing.T) []invocation {
	tree := readlinkTree(t)
	at := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: tree}
	}
	return []invocation{
		// The default mode: all but the last component must exist.
		at("a link", "link"),
		at("a plain file", "file"),
		at("a directory", "dir"),
		at("a missing last component", "missing"),
		at("a missing middle component", "missing/x"),
		at("a dangling link", "dangling"),
		at("a cycle", "loop1"),
		at("dot", "."),
		at("the root", "/"),
		at("a lone dash is an operand", "-"),
		at("several operands", "link", "file", "missing"),
		at("an operand that fails between two that do not", "link", "missing/x", "file"),

		// -q covers an operand's failure and nothing else.
		at("-q on a missing last component", "-q", "missing"),
		at("-q on a missing middle component", "-q", "missing/x"),
		at("-q over several operands", "-q", "link", "missing/x", "file"),

		// -e and -m, and the last of the two winning.
		at("-e a missing name", "-e", "missing"),
		at("-e a dangling link", "-e", "dangling"),
		at("-e a link", "-e", "link"),
		at("-e dot", "-e", "."),
		at("-m a missing middle component", "-m", "missing/x"),
		at("-m a cycle", "-m", "loop1"),
		at("-e then -m", "-e", "-m", "missing"),
		at("-m then -e", "-m", "-e", "missing"),
		at("-em in one cluster", "-em", "missing"),
		at("-me in one cluster", "-me", "missing"),

		// The link axis. `sublink` is a link two directories deep, so
		// popping it physically and popping it lexically land in
		// different places.
		at("-P past a link two deep", "-P", "sublink/.."),
		at("-L past a link two deep", "-L", "sublink/.."),
		at("-s past a link two deep", "-s", "sublink/.."),
		at("the default is -P", "sublink/.."),
		at("-P past a link to a directory", "-P", "dirlink/../file"),
		at("-L past a link to a directory", "-L", "dirlink/../file"),
		at("-s past a link to a directory", "-s", "dirlink/../file"),
		at("-P a link", "-P", "link"),
		at("-L a link", "-L", "link"),
		at("-s a link", "-s", "link"),
		at("-L then -s", "-L", "-s", "link"),
		at("-s then -L", "-s", "-L", "link"),
		at("-P then -s", "-P", "-s", "link"),
		at("-s then -P", "-s", "-P", "link"),
		at("-sP in one cluster", "-sP", "link"),
		at("-Ps in one cluster", "-Ps", "link"),
		at("-Ls in one cluster", "-Ls", "link"),
		at("-sL in one cluster", "-sL", "link"),
		at("-s a trailing slash on a link to a file", "-s", "link/"),
		at("-L a trailing slash on a link to a file", "-L", "link/"),
		at("-L a file two components from the end", "-L", "dir/f/x"),
		at("-s a file two components from the end", "-s", "dir/f/x"),

		// -s reads no link, so every question about a component is
		// asked of the path as written.
		at("-s a missing last component", "-s", "missing"),
		at("-s a missing middle component", "-s", "missing/x"),
		at("-s a dangling link", "-s", "dangling"),
		at("-s a cycle", "-s", "loop1"),
		at("-s under a cycle", "-s", "loop1/x"),
		at("-s a trailing slash on a directory", "-s", "dir/"),
		at("-s a trailing slash on a file", "-s", "file/"),
		at("-s a trailing slash on a missing name", "-s", "missing/"),
		at("-s dot", "-s", "."),
		at("-s dotdot", "-s", ".."),
		at("-s the root", "-s", "/"),
		at("-s a dot component inside the name", "-s", "dir/./f"),
		at("-s an empty operand", "-s", ""),
		at("-e -s a link", "-e", "-s", "link"),
		at("-e -s a dangling link", "-e", "-s", "dangling"),
		at("-m -s a cycle", "-m", "-s", "loop1"),

		// -L is the two-pass one, so a fault can come out of either
		// pass and the message still names the operand as typed.
		at("-L a name with several dotdots", "-L", "dir/../sublink/../file"),
		at("-L past a link then down", "-L", "sublink/../f"),
		at("-P past a link then down", "-P", "sublink/../f"),
		at("-L a missing component before a dotdot", "-L", "missing/../file"),
		at("-P a missing component before a dotdot", "-P", "missing/../file"),
		at("-L a plain file before a dotdot", "-L", "file/../dir"),
		at("-L -m a missing component before a dotdot", "-L", "-m", "missing/../x"),
		at("-L -e past a link two deep", "-L", "-e", "sublink/.."),
		at("-L -e a dangling link", "-L", "-e", "dangling"),
		at("-L -e a link", "-L", "-e", "link"),
		at("-L a dangling link", "-L", "dangling"),
		at("-L under a plain file", "-L", "link/x"),
		at("-L a dotdot at the root", "-L", "/../etc"),
		at("-L an absolute target that is nowhere", "-L", "abslink"),
		at("-L a cycle", "-L", "loop1"),
		at("-L -m a cycle", "-L", "-m", "loop1"),
		at("-L -m a dotdot past a plain file", "-L", "-m", "file/../.."),
		at("-s a dotdot pair on names that do not exist", "-s", "a/b/../../c"),
		at("-s dotdot past the root", "-s", "../../../../"),

		// -z is the NUL delimiter.
		at("-z one operand", "-z", "link"),
		at("-z two operands", "-z", "link", "file"),
		at("-z with a failure", "-z", "link", "missing/x"),

		// --relative-to on its own: always relative.
		at("--relative-to here", "--relative-to=.", "dir/f"),
		at("--relative-to a subdirectory", "--relative-to=dir", "file"),
		at("--relative-to two deep", "--relative-to=dir/sub", "file"),
		at("--relative-to the root", "--relative-to=/", "dir/f"),
		at("--relative-to somewhere else entirely", "--relative-to=/usr", "/etc/passwd"),
		at("--relative-to a plain file", "--relative-to=file", "dir/f"),
		at("--relative-to with a trailing slash", "--relative-to=dir/", "file"),
		at("--relative-to the same path", "--relative-to=.", "."),
		at("--relative-to a missing directory", "--relative-to=missing", "file"),
		at("--relative-to given twice", "--relative-to=.", "--relative-to=dir", "file"),
		at("--relative-to over several operands", "--relative-to=dir", "file", "dir/f"),
		at("--relative-to with a failing operand", "--relative-to=dir", "missing/x", "file"),
		at("--relative-to quietly with a failing operand", "-q", "--relative-to=dir", "missing/x", "file"),
		at("--relative-to under -z", "-z", "--relative-to=.", "dir/f", "file"),
		at("--relative-to under -s", "-s", "--relative-to=.", "link"),
		at("--relative-to under -L", "-L", "--relative-to=sublink/..", "dir/f"),
		at("--relative-to an empty directory name", "--relative-to=", "file"),
		// The DIR is resolved the same way an operand is, so a DIR that
		// is itself a link lands where the link points and the three
		// link settings answer differently.
		at("--relative-to a directory that is a link", "--relative-to=sublink", "dir/f"),
		at("-L --relative-to a directory that is a link", "-L", "--relative-to=dirlink", "dir/f"),
		at("-s --relative-to a directory that is a link", "-s", "--relative-to=dirlink", "dir/f"),
		at("-P --relative-to a directory that is a link", "-P", "--relative-to=dirlink", "dir/f"),

		// -m puts the whole thing on names that need not exist, which
		// is the only way to pin the ../.. arithmetic exactly.
		at("--relative-to a sibling", "-m", "--relative-to=/a/b", "/a/c"),
		at("--relative-to the path itself", "-m", "--relative-to=/a", "/a"),
		at("--relative-to an ancestor", "-m", "--relative-to=/a/b", "/a/b/c/d"),
		at("--relative-to a descendant", "-m", "--relative-to=/a/b/c", "/a/b"),
		at("--relative-to a name sharing a prefix", "-m", "--relative-to=/a/b", "/a/bc"),
		at("--relative-to a shorter name sharing a prefix", "-m", "--relative-to=/a", "/ab"),
		at("--relative-to a different branch", "-m", "--relative-to=/a/b/c", "/x/y"),
		at("--relative-to across a dotdot", "-m", "--relative-to=/a", "/a/b/../c"),
		at("--relative-to the root itself", "-m", "--relative-to=/", "/"),

		// --relative-base alone is also the directory measured from.
		at("--relative-base holding the path", "--relative-base=/etc", "/etc/passwd"),
		at("--relative-base not holding the path", "--relative-base=/usr", "/etc/passwd"),
		at("--relative-base here", "--relative-base=.", "dir/f"),
		at("--relative-base above here", "--relative-base=..", "dir/f"),
		at("--relative-base the root", "--relative-base=/", "dir/f"),
		at("--relative-base over several operands", "--relative-base=dir", "dir/f", "file"),
		at("--relative-base equal to the path", "--relative-base=/etc/passwd", "/etc/passwd"),
		at("--relative-base sharing a prefix only", "-m", "--relative-base=/a/b", "/a/bc/d"),
		at("--relative-base the root itself", "-m", "--relative-base=/", "/"),
		at("--relative-base given twice", "--relative-base=.", "--relative-base=dir", "dir/f"),
		at("--relative-base an empty directory name", "--relative-base=", "file"),

		// Both together: relative only when the path and the
		// directory measured from are BOTH under the base.
		at("both, everything under the base", "--relative-to=dir", "--relative-base=/", "file"),
		at("both, the path outside the base", "--relative-to=/etc", "--relative-base=/usr", "/etc/passwd"),
		at("both, the directory outside the base", "--relative-to=/usr", "--relative-base=/etc", "/etc/passwd"),
		at("both, the root measured from", "--relative-to=/", "--relative-base=/etc", "/etc/passwd"),
		at("both, base above directory", "-m", "--relative-base=/a", "--relative-to=/a/b", "/a/c"),
		at("both, base below directory", "-m", "--relative-base=/a/b/c", "--relative-to=/a/b", "/a/b/c/d"),
		at("both, base beside directory", "-m", "--relative-base=/a/b", "--relative-to=/a/b/c", "/a/b/d"),
		at("both, the path under the base", "--relative-to=dir/sub", "--relative-base=dir", "dir/f"),
		at("both, the path outside a base the directory is under", "--relative-to=dir/sub", "--relative-base=dir", "file"),
		at("--relative-base with a trailing slash", "-m", "--relative-base=/a/b", "/a/b/"),

		// A directory that will not resolve ends the program, before
		// any operand and whatever -q says.
		at("--relative-to a missing directory under -e", "-e", "--relative-to=missing", "file"),
		at("--relative-to a missing directory quietly", "-q", "-e", "--relative-to=missing", "file"),
		at("--relative-to under a plain file", "-e", "--relative-to=file/x", "file"),
		at("--relative-to reported before --relative-base", "-e", "--relative-to=m1", "--relative-base=m2", "file"),
		at("--relative-to reported whichever came first", "-e", "--relative-base=m2", "--relative-to=m1", "file"),
		at("--relative-base a missing directory under -e", "-e", "--relative-base=missing", "file"),
		at("an operand still fails after the directories resolve", "-e", "--relative-to=dir", "missing"),

		// The long spellings and the prefixes that are unique.
		at("--canonicalize-existing", "--canonicalize-existing", "missing"),
		at("--canonicalize-missing", "--canonicalize-missing", "missing"),
		at("--logical", "--logical", "link"),
		at("--physical", "--physical", "link"),
		at("--quiet", "--quiet", "missing/x"),
		at("--strip", "--strip", "link"),
		at("--no-symlinks", "--no-symlinks", "link"),
		at("--zero", "--zero", "link"),
		at("--canonicalize-e is a unique prefix", "--canonicalize-e", "link"),
		at("--l is a unique prefix", "--l", "link"),
		at("--p is a unique prefix", "--p", "link"),
		at("--q is a unique prefix", "--q", "missing/x"),
		at("--s is a unique prefix", "--s", "link"),
		at("--st is a unique prefix", "--st", "link"),
		at("--n is a unique prefix", "--n", "link"),
		at("--no is a unique prefix", "--no", "link"),
		at("--z is a unique prefix", "--z", "link"),
		at("--relative-t is a unique prefix", "--relative-t", "dir", "file"),
		at("--relative-b is a unique prefix", "--relative-b", "dir", "dir/f"),
		at("--relative-to takes the next token", "--relative-to", "dir", "file"),

		// getopt faults, and the ambiguity lists in declaration order.
		{name: "--c is ambiguous", args: []string{"--c", "link"}},
		{name: "--ca is ambiguous", args: []string{"--ca", "link"}},
		{name: "--r is ambiguous", args: []string{"--r", "link"}},
		{name: "--rel is ambiguous", args: []string{"--rel", "link"}},
		{name: "--relative- is ambiguous", args: []string{"--relative-", "link"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "invalid short option", args: []string{"-x", "link"}},
		{name: "invalid short option in a cluster", args: []string{"-ex", "link"}},
		{name: "unrecognized long option", args: []string{"--foo", "link"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", "link"}},
		{name: "zero with a value", args: []string{"--zero=1", "link"}},
		{name: "strip with a value", args: []string{"--strip=1", "link"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "relative-to with no value at the end", args: []string{"link", "--relative-to"}},

		// Operands, the awkward ones. The missing-operand check comes
		// before the directories are resolved, so a bad one is not
		// what a run with no operands reports.
		{name: "no operands"},
		{name: "no operands with an option", args: []string{"-z"}},
		{name: "no operands with a relative-to", args: []string{"--relative-to=dir"}},
		{name: "no operands with a bad relative-to", args: []string{"-e", "--relative-to=m1/x"}},
		{name: "dashdash alone", args: []string{"--"}},
		at("dashdash then an operand", "--", "link"),
		at("dashdash then an option-shaped operand", "--", "-e"),
		at("an empty operand", ""),
		at("an empty operand quietly", "-q", ""),
		at("an empty operand under -m", "-m", ""),
		at("an empty operand measured from somewhere", "-m", "--relative-to=/a/b", ""),
		at("a link target that is not valid UTF-8", "badtarget"),
		at("-e a link target that is not valid UTF-8", "-e", "badtarget"),
		at("-s a link target that is not valid UTF-8", "-s", "badtarget"),
		at("an operand that is not valid UTF-8", "\xff\xfe"),
		at("an operand that is not valid UTF-8 under -e", "-e", "\xff\xfe"),
		at("an operand with a newline", "-e", "a\nb"),
		at("an operand with a space", "-e", "a b"),
		at("an operand with an apostrophe", "-e", "a'b"),
		{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"link", "-z"}, dir: tree, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths.
		{name: "stdout closed with output", args: []string{"link"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed with no output", args: []string{"missing/x"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed quietly", args: []string{"-q", "missing/x"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed on a usage error", stdout: stdoutClosed},
		{name: "stdout closed on a relative-to fault", args: []string{"-e", "--relative-to=missing", "file"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"link"}, dir: tree, stdout: stdoutFull},
		{name: "stdout full with a diagnostic", args: []string{"link", "missing/x"}, dir: tree, stdout: stdoutFull},
		{name: "stdout full relative", args: []string{"--relative-to=dir", "file"}, dir: tree, stdout: stdoutFull},
	}
}

func TestRealpathParity(t *testing.T) {
	requireParity(t, "realpath", realpathCases(t))
}

func TestRealpathHelpVersion(t *testing.T) {
	requireHelp(t, "realpath", []string{"--help"}, 0)
	requireHelp(t, "realpath", []string{"--hel"}, 0)
	requireHelp(t, "realpath", []string{"--help", "link"}, 0)
	requireHelp(t, "realpath", []string{"link", "--help"}, 0)
	requireVersion(t, "realpath", []string{"--version"}, 0)
	requireVersion(t, "realpath", []string{"--vers"}, 0)
	// realpath declares no `--verbose`, so a lone `--v` is the version
	// rather than the ambiguity readlink's `--v` is.
	requireVersion(t, "realpath", []string{"--v"}, 0)
	requireVersion(t, "realpath", []string{"-e", "--version"}, 0)
}
