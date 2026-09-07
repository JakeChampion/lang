package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// rmdir(1) is rmdir(2) and nothing else, which is what makes its error paths
// the interesting half: a directory with anything in it is `Directory not
// empty` and a regular file is `Not a directory`, both of which `rm -r`
// hides. On top of that sit three options that interact — `-p` walks the
// operand's ancestors, `-v` narrates every attempt on STDOUT before it is
// made, and `--ignore-fail-on-non-empty` turns one particular failure into a
// silent success without touching the others.
//
// The `-p` walk is textual: trailing slashes come off once, then each round
// cuts at the last slash and steps back over a run of them. So `ds//sub`
// reaches `ds`, `./a/b` reaches `.` (and reports EINVAL there), and an
// absolute operand climbs to `/`. Each of those has a case.

// rmdirTree is the fixture: nested empty directories, a directory with a
// file in it, an ancestor chain whose top is not empty, a regular file, a
// path with a doubled separator, and names whose quoting differs.
func rmdirTree(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{
		"a/b/c", "empty", "full", "x/y/z", "ds/sub", "abs/one/two",
		"sp ace", "q'2", "nl\nx", "\xff\xfe", "-d",
	} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", d, err)
		}
	}
	for _, f := range []string{"full/f", "x/keep", "afile"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", f, err)
		}
	}
}

func rmdirCases(t *testing.T) []invocation {
	return []invocation{
		{name: "an empty directory", args: []string{"empty"}, tree: rmdirTree},
		{name: "a non-empty directory", args: []string{"full"}, tree: rmdirTree},
		{name: "a regular file", args: []string{"afile"}, tree: rmdirTree},
		{name: "a missing directory", args: []string{"nosuch"}, tree: rmdirTree},
		{name: "a missing parent", args: []string{"nosuch/x"}, tree: rmdirTree},
		{name: "a parent that is a file", args: []string{"afile/x"}, tree: rmdirTree},
		{name: "the empty operand", args: []string{""}, tree: rmdirTree},
		{name: "a nested directory without -p", args: []string{"a/b/c"}, tree: rmdirTree},
		{name: "a trailing slash", args: []string{"empty/"}, tree: rmdirTree},

		// Several operands: each is attempted, and one failure does not stop
		// the rest — only the exit status remembers.
		{name: "several operands, one missing", args: []string{"empty", "nosuch", "a/b/c"}, tree: rmdirTree},
		{name: "several operands, all fine", args: []string{"a/b/c", "a/b", "a"}, tree: rmdirTree},
		{name: "the same operand twice", args: []string{"empty", "empty"}, tree: rmdirTree},

		// -p: the ancestor walk, and where it stops.
		{name: "-p removes the whole chain", args: []string{"-p", "a/b/c"}, tree: rmdirTree},
		{name: "-p stops at a non-empty ancestor", args: []string{"-p", "x/y/z"}, tree: rmdirTree},
		{name: "-p on an operand with no slash", args: []string{"-p", "empty"}, tree: rmdirTree},
		{name: "-p over a doubled separator", args: []string{"-p", "ds//sub"}, tree: rmdirTree},
		{name: "-p with trailing slashes", args: []string{"-p", "ds/sub//"}, tree: rmdirTree},
		{name: "-p reaches dot and reports it", args: []string{"-p", "./a/b"}, tree: rmdirTree},
		{name: "-p over a dotdot component", args: []string{"-p", "a/b/../b/c"}, tree: rmdirTree},
		{name: "-p when the operand itself fails", args: []string{"-p", "full"}, tree: rmdirTree},
		{name: "-p when the operand is missing", args: []string{"-p", "nosuch/x"}, tree: rmdirTree},
		{name: "--parents long form", args: []string{"--parents", "a/b/c"}, tree: rmdirTree},

		// -v: one line per attempt, on stdout, BEFORE the attempt.
		{name: "-v on success", args: []string{"-v", "empty"}, tree: rmdirTree},
		{name: "-v on failure", args: []string{"-v", "full"}, tree: rmdirTree},
		{name: "-pv over a chain", args: []string{"-pv", "a/b/c"}, tree: rmdirTree},
		{name: "-pv stopping at a non-empty ancestor", args: []string{"-pv", "x/y/z"}, tree: rmdirTree},
		{name: "-vp is the same as -pv", args: []string{"-vp", "a/b/c"}, tree: rmdirTree},
		{name: "-v names a directory with a space", args: []string{"-v", "sp ace"}, tree: rmdirTree},
		{name: "-v names one with an apostrophe", args: []string{"-v", "q'2"}, tree: rmdirTree},
		{name: "-v names one with a newline", args: []string{"-v", "nl\nx"}, tree: rmdirTree},
		{name: "-v names one that is not valid UTF-8", args: []string{"-v", "\xff\xfe"}, tree: rmdirTree},
		{name: "--verbose long form", args: []string{"--verbose", "empty"}, tree: rmdirTree},

		// --ignore-fail-on-non-empty silences exactly one errno family.
		{name: "ignore-fail on a non-empty directory", args: []string{"--ignore-fail-on-non-empty", "full"}, tree: rmdirTree},
		{name: "ignore-fail does not silence ENOENT", args: []string{"--ignore-fail-on-non-empty", "nosuch"}, tree: rmdirTree},
		{name: "ignore-fail does not silence ENOTDIR", args: []string{"--ignore-fail-on-non-empty", "afile"}, tree: rmdirTree},
		{name: "ignore-fail with -p", args: []string{"-p", "--ignore-fail-on-non-empty", "x/y/z"}, tree: rmdirTree},
		{name: "ignore-fail with -pv", args: []string{"-pv", "--ignore-fail-on-non-empty", "x/y/z"}, tree: rmdirTree},
		{name: "ignore-fail by unique prefix", args: []string{"--i", "full"}, tree: rmdirTree},
		{name: "ignore-fail with a value", args: []string{"--ignore-fail-on-non-empty=x", "full"}, tree: rmdirTree},

		// Operands and the option terminator.
		{name: "no operands"},
		{name: "dashdash then an option-looking name", args: []string{"--", "-d"}, tree: rmdirTree},
		{name: "an option after an operand permutes", args: []string{"empty", "-v"}, tree: rmdirTree},
		{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"empty", "-v"}, env: []string{"POSIXLY_CORRECT=1"}, tree: rmdirTree},
		{name: "a lone dash is an operand", args: []string{"-"}, tree: rmdirTree},

		// getopt faults. The ambiguity list prints in declaration order, so
		// `--v` names '--verbose' before '--version'.
		{name: "invalid short option", args: []string{"-x", "empty"}},
		{name: "invalid byte in a cluster", args: []string{"-px", "empty"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "ambiguous long option", args: []string{"--v"}},
		// `--path` is GNU's undocumented synonym for `--parents`. It is
		// visible in the ambiguity list, and `--pa` resolves rather than
		// faulting because glibc calls two candidates carrying the same
		// value unambiguous.
		{name: "the undocumented --path", args: []string{"--path", "a/b/c"}, tree: rmdirTree},
		{name: "--pa is not ambiguous", args: []string{"--pa", "a/b/c"}, tree: rmdirTree},
		{name: "--pat resolves to --path", args: []string{"--pat", "a/b/c"}, tree: rmdirTree},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "parents with a value", args: []string{"--parents=x", "empty"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},

		// The write-failure paths: -v is the only thing on stdout.
		{name: "stdout closed with -v", args: []string{"-v", "empty"}, tree: rmdirTree, stdout: stdoutClosed},
		{name: "stdout full with -v", args: []string{"-v", "empty"}, tree: rmdirTree, stdout: stdoutFull},
		{name: "stdout closed without -v", args: []string{"empty"}, tree: rmdirTree, stdout: stdoutClosed},
		{name: "stdout closed on a fault", stdout: stdoutClosed},
	}
}

func TestRmdirParity(t *testing.T) {
	requireParity(t, "rmdir", rmdirCases(t))
}

func TestRmdirHelpVersion(t *testing.T) {
	requireHelp(t, "rmdir", []string{"--help"}, 0)
	requireHelp(t, "rmdir", []string{"--hel"}, 0)
	requireHelp(t, "rmdir", []string{"--help", "x"}, 0)
	requireHelp(t, "rmdir", []string{"x", "--help"}, 0)
	requireVersion(t, "rmdir", []string{"--version"}, 0)
	requireVersion(t, "rmdir", []string{"--versio"}, 0)
	requireVersion(t, "rmdir", []string{"x", "--version"}, 0)
}
