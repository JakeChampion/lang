package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// link(1) is one link(2) call: exactly two operands, no options of its own,
// and no cleverness about what went wrong. What there is to get wrong is the
// diagnostics — the operand order in `cannot create link 'b' to 'a'` is the
// NEW name first, and each side of it is gnulib's quoteaf, which quotes even
// when nothing needs it — and the three arities, which are three different
// messages.
//
// Every case that touches the filesystem builds its own tree, so the
// comparison is of the two resulting DIRECTORIES as well as of the output: a
// hard link says nothing on stdout, and whether it reached the same inode is
// the whole question.

// linkTree is the fixture: a plain file, a directory, a symlink to the file,
// and a dangling symlink. link(2) does not follow the final symlink of
// either operand, so `link sym hard` must leave a second link to the SYMLINK.
func linkTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "src"), []byte("payload\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taken"), []byte("other\n"), 0o644); err != nil {
		t.Fatalf("write taken: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("src", filepath.Join(dir, "sym")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func linkCases(t *testing.T) []invocation {
	return []invocation{
		// The happy path, and the proof it is a HARD link: the two names
		// share an inode, which the tree comparison's link groups carry.
		{name: "creates a hard link", args: []string{"src", "dst"}, seedTree: linkTree},
		{name: "a second link to the same file", args: []string{"src", "another"}, seedTree: linkTree},
		// link(2) does not follow the final symlink of either operand, so
		// the new name is a second link to the LINK, not to its target.
		{name: "a symlink is linked to itself", args: []string{"sym", "dst"}, seedTree: linkTree},
		{name: "a dangling symlink links fine", args: []string{"dangling", "dst"}, seedTree: linkTree},

		// The error paths, each with its own errno text.
		{name: "the new name is taken", args: []string{"src", "taken"}, seedTree: linkTree},
		{name: "the target does not exist", args: []string{"nope", "dst"}, seedTree: linkTree},
		{name: "the target is a directory", args: []string{"adir", "dst"}, seedTree: linkTree},
		{name: "the new name is a directory", args: []string{"src", "adir"}, seedTree: linkTree},
		{name: "the new name has no parent", args: []string{"src", "nodir/dst"}, seedTree: linkTree},
		{name: "the new name is under a file", args: []string{"src", "src/dst"}, seedTree: linkTree},
		{name: "linking a file to itself", args: []string{"src", "src"}, seedTree: linkTree},
		{name: "empty operands", args: []string{"", ""}, seedTree: linkTree},
		{name: "empty target", args: []string{"", "dst"}, seedTree: linkTree},
		{name: "empty new name", args: []string{"src", ""}, seedTree: linkTree},

		// The three arities are three different messages.
		{name: "no operands"},
		{name: "one operand", args: []string{"a"}},
		{name: "three operands", args: []string{"a", "b", "c"}},
		{name: "four operands", args: []string{"a", "b", "c", "d"}},
		{name: "one operand needing quotes", args: []string{"a b"}},
		{name: "one operand with a newline", args: []string{"a\nb"}},
		{name: "extra operand with a newline", args: []string{"a", "b", "c\nd"}},

		// Names whose quoting differs between quote and quotef: a space, an
		// apostrophe (which makes gnulib reach for double quotes), a control
		// byte, and one that is not valid UTF-8.
		{name: "names with a space", args: []string{"no such src", "no such dst"}, seedTree: linkTree},
		{name: "names with an apostrophe", args: []string{"a'b", "c'd"}, seedTree: linkTree},
		{name: "names with a newline", args: []string{"a\nb", "c\nd"}, seedTree: linkTree},
		{name: "names that are not valid UTF-8", args: []string{"\xff\xfe", "\xfd\xfc"}, seedTree: linkTree},

		// `-` is an ordinary operand here; `--` ends the options.
		{name: "lone dash target", args: []string{"-", "dst"}, seedTree: linkTree},
		{name: "dashdash then operands", args: []string{"--", "src", "dst"}, seedTree: linkTree},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "dashdash with one operand", args: []string{"--", "src"}, seedTree: linkTree},

		// getopt faults. Only --help and --version are declared, so `--v` is
		// ambiguous between them and an option is found wherever it stands.
		{name: "invalid short option", args: []string{"-x", "a", "b"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", "a", "b"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option after operands", args: []string{"a", "b", "--foo"}},
		{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"a", "--help"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths: link writes nothing on success, so a
		// closed stdout is only observable on the diagnostic side.
		{name: "stdout closed on success", args: []string{"src", "dst"}, seedTree: linkTree, stdout: stdoutClosed},
		{name: "stdout closed on a fault", stdout: stdoutClosed},
	}
}

func TestLinkParity(t *testing.T) {
	requireParity(t, "link", linkCases(t))
}

func TestLinkHelpVersion(t *testing.T) {
	requireHelp(t, "link", []string{"--help"}, 0)
	requireHelp(t, "link", []string{"--hel"}, 0)
	requireHelp(t, "link", []string{"--help", "a", "b"}, 0)
	requireHelp(t, "link", []string{"a", "--help"}, 0)
	requireVersion(t, "link", []string{"--version"}, 0)
	requireVersion(t, "link", []string{"--vers"}, 0)
	requireVersion(t, "link", []string{"a", "b", "--version"}, 0)
}
