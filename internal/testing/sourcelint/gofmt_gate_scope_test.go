package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGofmtGateReadsGitsFileSetNotTheDirectory(t *testing.T) {
	gate, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "gofmt_gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gate); err != nil {
		t.Fatalf("gate script missing: %v", err)
	}

	const misformatted = "package p\n\nfunc  Bad( ) int {\nreturn 1\n}\n"

	for _, tc := range []struct {
		name           string
		rel            string
		track          bool
		removeFromDisk bool
		wantFail       bool
	}{
		{name: "an ignored path is not the repository's to format", rel: ".claude/worktrees/agent-x/bad.go"},
		{name: "a tracked path still is", rel: "pkg/bad.go", track: true, wantFail: true},
		{name: "so is a new file git does not track yet", rel: "pkg/untracked.go", wantFail: true},
		{name: "a tracked file deleted from the worktree is skipped, not an error", rel: "pkg/removed.go", track: true, removeFromDisk: true},
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
			run("git", "config", "gc.auto", "0")
			run("git", "config", "maintenance.auto", "false")

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
			run("git", "add", ".gitignore")
			if tc.track {
				run("git", "add", tc.rel)
			}
			run("git", "commit", "-q", "-m", "seed", "--no-gpg-sign")
			if tc.removeFromDisk {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}

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
				t.Fatalf("gate named a path it should have passed over:\n%s", out)
			}
		})
	}
}
