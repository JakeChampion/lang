package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// rmdir(1) is one rmdir(2) per operand, so most of its corpus is about which
// errno the kernel gives and how GNU words it. The three that are easy to get
// wrong, and each has cases below:
//
//   - a PARENT removed by -p says `failed to remove directory 'x'`, where the
//     operand itself says `failed to remove 'x'`;
//   - `rmdir symlink/` is ENOTDIR and GNU replaces the strerror text with
//     `Symbolic link not followed`, while `rmdir regularfile/` — also ENOTDIR —
//     keeps it;
//   - `--ignore-fail-on-non-empty` forgives ENOTEMPTY outright and forgives
//     EBUSY only after finding the directory really is non-empty, which is
//     what `rmdir --ignore-fail-on-non-empty /` exercises.
//
// The -p walk is string surgery on the operand rather than a resolved path, so
// `./d` climbs to `.` (EINVAL) and `a//b` climbs to `a`; both are here.

// rmdirTree is the fixture: empty directories at several depths, a non-empty
// one, a regular file, and the two symlinks the ENOTDIR wording turns on.
func rmdirTree(t *testing.T, dir string) {
	t.Helper()
	mk := func(rel string) {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mk("empty")
	mk("empty2")
	mk("empty3")
	mk("deep/a/b")
	mk("slashes/a/b")
	mk("dot/d")
	mk("full")
	mk("target")
	mk("outer/inner")
	mk("a b")
	mk("a'b")
	if err := os.WriteFile(filepath.Join(dir, "full", "occupant"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write occupant: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outer", "occupant"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write occupant: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	if err := os.Symlink("target", filepath.Join(dir, "sym")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink("plain", filepath.Join(dir, "symfile")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func init() {
	registerCorpus("rmdir", rmdirCases)
}

func rmdirCases(t *testing.T) []invocation {
	return []invocation{
		// The plain removals.
		{name: "one empty directory", args: []string{"empty"}, seedTree: rmdirTree},
		{name: "several empty directories", args: []string{"empty", "empty2", "empty3"}, seedTree: rmdirTree},
		{name: "a trailing slash", args: []string{"empty/"}, seedTree: rmdirTree},
		{name: "several trailing slashes", args: []string{"empty///"}, seedTree: rmdirTree},
		{name: "a nested empty directory", args: []string{"deep/a/b"}, seedTree: rmdirTree},
		{name: "a name with a space", args: []string{"a b"}, seedTree: rmdirTree},
		{name: "a name with an apostrophe", args: []string{"a'b"}, seedTree: rmdirTree},

		// The errnos, one per kernel answer.
		{name: "a non-empty directory", args: []string{"full"}, seedTree: rmdirTree},
		{name: "a missing directory", args: []string{"nosuch"}, seedTree: rmdirTree},
		{name: "a missing parent", args: []string{"nosuch/deep"}, seedTree: rmdirTree},
		{name: "a regular file", args: []string{"plain"}, seedTree: rmdirTree},
		{name: "a parent that is a file", args: []string{"plain/x"}, seedTree: rmdirTree},
		{name: "dot is EINVAL", args: []string{"."}, seedTree: rmdirTree},
		{name: "dotdot is not empty", args: []string{".."}, seedTree: rmdirTree},
		{name: "the root is busy", args: []string{"/"}, seedTree: rmdirTree},
		{name: "the root spelled with two slashes", args: []string{"//"}, seedTree: rmdirTree},

		// ENOTDIR, and the one spelling GNU rewords.
		{name: "a symlink to a directory", args: []string{"sym"}, seedTree: rmdirTree},
		{name: "a symlink to a directory with a slash", args: []string{"sym/"}, seedTree: rmdirTree},
		{name: "a symlink to a directory with two slashes", args: []string{"sym//"}, seedTree: rmdirTree},
		{name: "a dangling symlink", args: []string{"dangling"}, seedTree: rmdirTree},
		{name: "a dangling symlink with a slash", args: []string{"dangling/"}, seedTree: rmdirTree},
		{name: "a symlink to a file with a slash keeps the strerror", args: []string{"symfile/"}, seedTree: rmdirTree},
		{name: "a regular file with a slash keeps the strerror", args: []string{"plain/"}, seedTree: rmdirTree},
		{name: "a slash after a directory entry of a symlink", args: []string{"sym/."}, seedTree: rmdirTree},

		// --ignore-fail-on-non-empty: ENOTEMPTY outright, EBUSY after a read.
		{name: "ignore forgives a non-empty directory", args: []string{"--ignore-fail-on-non-empty", "full"}, seedTree: rmdirTree},
		{name: "ignore forgives dotdot", args: []string{"--ignore-fail-on-non-empty", ".."}, seedTree: rmdirTree},
		{name: "ignore forgives the busy root", args: []string{"--ignore-fail-on-non-empty", "/"}, seedTree: rmdirTree},
		{name: "ignore does not forgive EINVAL", args: []string{"--ignore-fail-on-non-empty", "."}, seedTree: rmdirTree},
		{name: "ignore does not forgive ENOENT", args: []string{"--ignore-fail-on-non-empty", "nosuch"}, seedTree: rmdirTree},
		{name: "ignore does not forgive ENOTDIR", args: []string{"--ignore-fail-on-non-empty", "plain"}, seedTree: rmdirTree},
		{name: "ignore still removes an empty one", args: []string{"--ignore-fail-on-non-empty", "empty"}, seedTree: rmdirTree},
		{name: "ignore as a unique prefix", args: []string{"--ig", "full"}, seedTree: rmdirTree},

		// -p, and what the walk does to the spelling it was handed.
		{name: "parents removes each component", args: []string{"-p", "deep/a/b"}, seedTree: rmdirTree},
		{name: "parents with a trailing slash", args: []string{"-p", "deep/a/b/"}, seedTree: rmdirTree},
		{name: "parents over redundant slashes", args: []string{"-p", "slashes//a//b"}, seedTree: rmdirTree},
		{name: "parents on a single component stops", args: []string{"-p", "empty"}, seedTree: rmdirTree},
		{name: "parents on a single component with a slash", args: []string{"-p", "empty/"}, seedTree: rmdirTree},
		{name: "parents climbs to dot", args: []string{"-p", "dot/d"}, seedTree: rmdirTree},
		{name: "parents climbs to dot spelled explicitly", args: []string{"-p", "./empty"}, seedTree: rmdirTree},
		{name: "parents stops at a non-empty parent", args: []string{"-p", "outer/inner"}, seedTree: rmdirTree},
		{name: "parents with ignore stops quietly", args: []string{"-p", "--ignore-fail-on-non-empty", "outer/inner"}, seedTree: rmdirTree},
		{name: "parents on a missing directory", args: []string{"-p", "nosuch/deep"}, seedTree: rmdirTree},
		{name: "parents long spelling", args: []string{"--parents", "deep/a/b"}, seedTree: rmdirTree},

		// -v prints before it tries, so a failure is named twice.
		{name: "verbose on a success", args: []string{"-v", "empty"}, seedTree: rmdirTree},
		{name: "verbose on a failure", args: []string{"-v", "nosuch"}, seedTree: rmdirTree},
		{name: "verbose over several operands", args: []string{"-v", "empty", "nosuch", "empty2"}, seedTree: rmdirTree},
		{name: "verbose with parents", args: []string{"-pv", "deep/a/b"}, seedTree: rmdirTree},
		{name: "verbose with parents and a failure", args: []string{"-pv", "outer/inner"}, seedTree: rmdirTree},
		{name: "verbose with parents and ignore", args: []string{"-pv", "--ignore-fail-on-non-empty", "outer/inner"}, seedTree: rmdirTree},
		{name: "verbose quotes a name with a space", args: []string{"-v", "a b"}, seedTree: rmdirTree},
		{name: "verbose quotes a name with an apostrophe", args: []string{"-v", "a'b"}, seedTree: rmdirTree},
		{name: "verbose long spelling", args: []string{"--verbose", "empty"}, seedTree: rmdirTree},
		{name: "repeated options are idempotent", args: []string{"-vv", "-pp", "deep/a/b"}, seedTree: rmdirTree},

		// The operand shapes.
		{name: "no operands"},
		{name: "dashdash alone"},
		{name: "dashdash then an operand", args: []string{"--", "empty"}, seedTree: rmdirTree},
		{name: "dashdash before an option-looking name", args: []string{"--", "-v"}, seedTree: rmdirTree},
		{name: "the empty operand", args: []string{""}, seedTree: rmdirTree},
		{name: "a lone dash is an operand", args: []string{"-"}, seedTree: rmdirTree},
		{name: "an operand that is not valid UTF-8", args: []string{"\xff\xfe"}, seedTree: rmdirTree},
		{name: "an operand with a newline", args: []string{"a\nb"}, seedTree: rmdirTree},
		{name: "an operand with a tab", args: []string{"a\tb"}, seedTree: rmdirTree},
		{name: "an operand with a double quote", args: []string{"a\"b"}, seedTree: rmdirTree},
		{name: "a failure before a success still exits 1", args: []string{"nosuch", "empty"}, seedTree: rmdirTree},
		{name: "a success before a failure still exits 1", args: []string{"empty", "nosuch"}, seedTree: rmdirTree},
		{name: "the same operand twice", args: []string{"empty", "empty"}, seedTree: rmdirTree},

		// getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option after a good one", args: []string{"-vx"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "an ambiguous prefix lists in declaration order", args: []string{"--v"}},
		{name: "a unique prefix of parents", args: []string{"--p", "empty"}, seedTree: rmdirTree},
		// --path is a deprecated synonym for --parents that the help does not
		// mention; it stands ahead of --parents in the ambiguity list above and
		// does not make --p or --pa ambiguous, because the two do the same thing.
		{name: "the hidden path synonym", args: []string{"--path", "deep/a/b"}, seedTree: rmdirTree},
		{name: "a prefix shared by path and parents is not ambiguous", args: []string{"--pa", "deep/a/b"}, seedTree: rmdirTree},
		{name: "the path synonym refuses a value", args: []string{"--path=x"}},
		{name: "a unique prefix of verbose", args: []string{"--verb", "empty"}, seedTree: rmdirTree},
		{name: "a flag refuses a value", args: []string{"--parents=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "an option after an operand is permuted out", args: []string{"empty", "-v"}, seedTree: rmdirTree},
		{name: "a bad option after an operand", args: []string{"empty", "--foo"}, seedTree: rmdirTree},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{"empty", "-v"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: rmdirTree},
		{name: "POSIXLY_CORRECT leaves a bad option as an operand",
			args: []string{"empty", "--foo"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: rmdirTree},

		// The write-failure paths. Only -v writes anything, and whether the
		// report carries strerror text or is the bare `write error` turns on
		// what was still pending when the diagnostic flushed stdout.
		{name: "stdout closed with nothing to say", args: []string{"empty"}, seedTree: rmdirTree, stdout: stdoutClosed},
		{name: "stdout closed on a fault", args: []string{"nosuch"}, seedTree: rmdirTree, stdout: stdoutClosed},
		{name: "stdout closed under verbose", args: []string{"-v", "empty"}, seedTree: rmdirTree, stdout: stdoutClosed},
		{name: "stdout closed under verbose on a fault", args: []string{"-v", "nosuch"}, seedTree: rmdirTree, stdout: stdoutClosed},
		{name: "stdout full under verbose", args: []string{"-v", "empty"}, seedTree: rmdirTree, stdout: stdoutFull},
		{name: "stdout full under verbose on a fault", args: []string{"-v", "nosuch"}, seedTree: rmdirTree, stdout: stdoutFull},
		{name: "stdout full with nothing to say", args: []string{"empty"}, seedTree: rmdirTree, stdout: stdoutFull},
		{name: "stdout closed on a usage error", stdout: stdoutClosed},
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
	requireVersion(t, "rmdir", []string{"--vers"}, 0)
	requireVersion(t, "rmdir", []string{"--version", "x"}, 0)
}
