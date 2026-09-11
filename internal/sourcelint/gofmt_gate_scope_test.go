package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The gofmt gate reads its file set from git rather than walking the
// directory. `gofmt -l .` descends into everything .gitignore covers, and
// this repository keeps agent worktrees under .claude/ — separate checkouts
// carrying their own in-progress edits. That made `make gofmt-check` fail in
// the main checkout over files the pusher does not own, which blocks a push
// for someone else's half-finished work. A fresh clone has no such paths, so
// CI could never see it.
//
// The two halves have to hold together: ignoring ignored paths is only
// correct if everything else is still gated, so this checks a misformatted
// file in an ignored path passes AND that the same content fails when it is
// somewhere git tracks.
func TestGofmtGateReadsGitsFileSetNotTheDirectory(t *testing.T) {
	gate, err := filepath.Abs(filepath.Join("..", "..", "tools", "gofmt_gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gate); err != nil {
		t.Fatalf("gate script missing: %v", err)
	}

	const misformatted = "package p\n\nfunc  Bad( ) int {\nreturn 1\n}\n"

	for _, tc := range []struct {
		name     string
		rel      string
		wantFail bool
	}{
		{"ignored path is not the repository's to format", ".claude/worktrees/agent-x/bad.go", false},
		{"a tracked path still is", "pkg/bad.go", true},
		{"so is a new file git does not track yet", "pkg/untracked.go", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			run := func(name string, arg ...string) {
				t.Helper()
				cmd := exec.Command(name, arg...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s %v: %v\n%s", name, arg, err, out)
				}
			}
			run("git", "init", "-q")
			run("git", "config", "user.email", "gate@example.invalid")
			run("git", "config", "user.name", "gate")

			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/.claude/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(misformatted), 0o644); err != nil {
				t.Fatal(err)
			}
			// Committing only .gitignore leaves the "new file" case untracked
			// but not ignored, which is the third row above.
			run("git", "add", ".gitignore")
			if tc.rel == "pkg/bad.go" {
				run("git", "add", tc.rel)
			}
			run("git", "commit", "-q", "-m", "seed", "--no-gpg-sign")

			cmd := exec.Command("bash", gate)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			failed := err != nil

			if failed != tc.wantFail {
				t.Fatalf("gate failed=%v, want %v\n%s", failed, tc.wantFail, out)
			}
			if tc.wantFail && !strings.Contains(string(out), "not gofmt-clean") {
				t.Fatalf("a failure should say what is wrong, got:\n%s", out)
			}
			if !tc.wantFail && strings.Contains(string(out), tc.rel) {
				t.Fatalf("gate named an ignored path:\n%s", out)
			}
		})
	}
}
