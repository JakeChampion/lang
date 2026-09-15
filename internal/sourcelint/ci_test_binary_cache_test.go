package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCITestBinaryCache(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-test-binary-cache"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, root, cache string)
		env    []string
		miss   bool
	}{
		{name: "matching inputs"},
		{name: "new commit", miss: true, mutate: func(t *testing.T, root, _ string) {
			cacheTestGit(t, root, "commit", "--allow-empty", "-qm", "new revision")
		}},
		{name: "tracked edit", miss: true, mutate: func(t *testing.T, root, _ string) {
			cacheTestWrite(t, filepath.Join(root, "source.go"), "changed source")
		}},
		{name: "untracked source", miss: true, mutate: func(t *testing.T, root, _ string) {
			cacheTestWrite(t, filepath.Join(root, "extra.go"), "extra source")
		}},
		{name: "missing manifest", miss: true, mutate: func(t *testing.T, _, cache string) {
			if err := os.Remove(filepath.Join(cache, "input-key")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "truncated second binary", miss: true, mutate: func(t *testing.T, _, cache string) {
			cacheTestWrite(t, filepath.Join(cache, "e2e.test"), "truncated")
		}},
		{name: "malformed checksum", miss: true, mutate: func(t *testing.T, _, cache string) {
			cacheTestWrite(t, filepath.Join(cache, "e2e.test.sha256"), "not a checksum")
		}},
		{name: "changed build flags", miss: true, env: []string{"GOFLAGS=-tags=cache_changed"}},
		{name: "changed target", miss: true, env: []string{"GOOS=linux", "GOARCH=386"}},
		{name: "changed runner image", miss: true, env: []string{"ImageVersion=changed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, cache := t.TempDir(), t.TempDir()
			cacheTestWrite(t, filepath.Join(root, "source.go"), "package fixture\n")
			cacheTestWrite(t, filepath.Join(root, ".gitignore"), "*.test\n")
			cacheTestGit(t, root, "init", "-q")
			cacheTestGit(t, root, "add", ".")
			cacheTestGit(t, root, "commit", "-qm", "initial")
			files := []string{"e2eselfhost.test", "e2e.test"}
			for _, file := range files {
				cacheTestWrite(t, filepath.Join(root, file), "compiled "+file)
			}
			run := func(mode string, extra []string) ([]byte, error) {
				cmd := exec.Command("bash", script, mode, cache)
				cmd.Dir = root
				cmd.Env = append(ciEnv("GOTOOLCHAIN=local", "ImageVersion=original", "GOFLAGS="), extra...)
				return cmd.CombinedOutput()
			}
			if out, err := run("save", nil); err != nil {
				t.Fatalf("save: %v: %s", err, out)
			}
			if tc.mutate != nil {
				tc.mutate(t, root, cache)
			}
			for _, file := range files {
				cacheTestWrite(t, filepath.Join(root, file), "existing "+file)
			}
			out, err := run("restore", tc.env)
			if (err != nil) != tc.miss {
				t.Fatalf("restore miss=%v, want %v: %v: %s", err != nil, tc.miss, err, out)
			}
			for _, file := range files {
				want := "compiled " + file
				if tc.miss {
					want = "existing " + file
				}
				data, err := os.ReadFile(filepath.Join(root, file))
				if err != nil || string(data) != want {
					t.Errorf("%s after restore: %q, %v, want %q", file, data, err, want)
				}
			}
		})
	}
}

func cacheTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-c", "user.name=Cache test", "-c", "user.email=cache@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func cacheTestWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
