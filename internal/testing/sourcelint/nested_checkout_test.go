package sourcelint

import (
	"os"
	"path/filepath"
	"testing"
)

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
