package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// unlink(1) is one unlink(2) call and exactly one operand. The distinction it
// exists for is the one `rm` hides: a DIRECTORY is EISDIR here rather than
// something to recurse into, and a symlink is removed itself rather than
// followed. Both are in the corpus, each with the strerror text GNU prints.

// unlinkTree is the fixture: a file, a directory, a symlink to the file, a
// dangling symlink, and a hard-linked pair so removing one name leaves the
// other.
func unlinkTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "victim"), []byte("payload\n"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kept"), []byte("kept\n"), 0o644); err != nil {
		t.Fatalf("write kept: %v", err)
	}
	if err := os.Link(filepath.Join(dir, "kept"), filepath.Join(dir, "keptlink")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("victim", filepath.Join(dir, "sym")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func unlinkCases(t *testing.T) []invocation {
	return []invocation{
		{name: "removes a file", args: []string{"victim"}, seedTree: unlinkTree},
		// A symlink is removed ITSELF: what it points at survives.
		{name: "removes a symlink, not its target", args: []string{"sym"}, seedTree: unlinkTree},
		{name: "removes a dangling symlink", args: []string{"dangling"}, seedTree: unlinkTree},
		// One name of a hard-linked pair goes; the other keeps the content.
		{name: "removes one name of a hard-linked pair", args: []string{"keptlink"}, seedTree: unlinkTree},

		// The error paths.
		{name: "a directory is not unlinkable", args: []string{"adir"}, seedTree: unlinkTree},
		{name: "a missing file", args: []string{"nope"}, seedTree: unlinkTree},
		{name: "a missing parent", args: []string{"nodir/x"}, seedTree: unlinkTree},
		{name: "a parent that is a file", args: []string{"victim/x"}, seedTree: unlinkTree},
		{name: "the empty operand", args: []string{""}, seedTree: unlinkTree},
		{name: "a lone dash names a file called -", args: []string{"-"}, seedTree: unlinkTree},
		{name: "a name with a space", args: []string{"no such name"}, seedTree: unlinkTree},
		{name: "a name with an apostrophe", args: []string{"a'b"}, seedTree: unlinkTree},
		{name: "a name with a newline", args: []string{"a\nb"}, seedTree: unlinkTree},
		{name: "a name that is not valid UTF-8", args: []string{"\xff\xfe"}, seedTree: unlinkTree},

		// The arities: none is `missing operand`, two is `extra operand`.
		{name: "no operands"},
		{name: "two operands", args: []string{"a", "b"}},
		{name: "three operands", args: []string{"a", "b", "c"}},
		{name: "extra operand needing quotes", args: []string{"a", "b c"}},
		{name: "extra operand with a newline", args: []string{"a", "b\nc"}},

		{name: "dashdash then an operand", args: []string{"--", "victim"}, seedTree: unlinkTree},
		{name: "dashdash alone"},
		{name: "dashdash before an option-looking name", args: []string{"--", "--help"}, seedTree: unlinkTree},

		// getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option after an operand", args: []string{"a", "--foo"}},
		{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"a", "--help"}, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "stdout closed on success", args: []string{"victim"}, seedTree: unlinkTree, stdout: stdoutClosed},
		{name: "stdout closed on a fault", stdout: stdoutClosed},
	}
}

func TestUnlinkParity(t *testing.T) {
	requireParity(t, "unlink", unlinkCases(t))
}

func TestUnlinkHelpVersion(t *testing.T) {
	requireHelp(t, "unlink", []string{"--help"}, 0)
	requireHelp(t, "unlink", []string{"--hel"}, 0)
	requireHelp(t, "unlink", []string{"--help", "x"}, 0)
	requireHelp(t, "unlink", []string{"x", "--help"}, 0)
	requireVersion(t, "unlink", []string{"--version"}, 0)
	requireVersion(t, "unlink", []string{"--vers"}, 0)
	requireVersion(t, "unlink", []string{"x", "--version"}, 0)
}
