package sourcelint

import (
	"os"
	"path/filepath"
	"testing"
)

// The agent tooling creates git worktrees under `.claude/worktrees/`, each a
// whole second checkout of this repository. A repo-hygiene walk that descends
// into one reports that checkout's files as this one's — so a guard fails
// naming a path the working tree does not contain, and a scratch file another
// process deletes mid-walk fails a check nobody can act on. The offending
// directory is also not fixable from here: it belongs to a different branch.
//
// A nested checkout is recognised by its own `.git`, which is a FILE in a
// worktree and a directory in a clone, so this tests for either. That is the
// property itself rather than a path spelling, so a worktree created somewhere
// other than `.claude/` is skipped too.
func isNestedCheckout(root, path string) bool {
	if path == root {
		return false
	}
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

func TestIsNestedCheckoutFindsAWorktree(t *testing.T) {
	root := t.TempDir()

	if isNestedCheckout(root, root) {
		t.Error("the root of the walk is not a nested checkout, even though it has a .git of its own")
	}

	plain := filepath.Join(root, "internal")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if isNestedCheckout(root, plain) {
		t.Errorf("%s has no .git and is part of this checkout", plain)
	}

	// A worktree's `.git` is a file holding a gitdir: line; a clone's is a
	// directory. Both are separate checkouts and both must be skipped.
	worktree := filepath.Join(root, ".claude", "worktrees", "agent-1")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isNestedCheckout(root, worktree) {
		t.Errorf("%s is a git worktree and must not be walked into", worktree)
	}

	clone := filepath.Join(root, "vendorish")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isNestedCheckout(root, clone) {
		t.Errorf("%s is a nested clone and must not be walked into", clone)
	}
}
