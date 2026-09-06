package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// perfHistoryScript locates scripts/perf-history from this package dir.
func perfHistoryScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "perf-history"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("perf-history script missing: %v", err)
	}
	return p
}

// gitEnv is ciEnv with every inherited GIT_* variable dropped.
//
// GIT_DIR is the one that bites: with it set, the fixture repositories below
// are never touched and every command runs against the caller's own checkout
// instead — including the `add` and `commit` that build the fixture.
func gitEnv(extra ...string) []string {
	var env []string
	for _, kv := range ciEnv() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// gitIn runs git in dir with a fixed identity and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// runPerfHistory invokes the script in dir and returns exit code + output.
func runPerfHistory(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{perfHistoryScript(t)}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); !ok {
			t.Fatalf("run perf-history %v: %v\n%s", args, err, out)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

// perfRepo builds a bare origin with one commit on main and returns it with a
// clone whose default branch tracks it.
func perfRepo(t *testing.T) (origin, clone string) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	clone = filepath.Join(root, "clone")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, root, "clone", "-q", origin, clone)
	if err := os.WriteFile(filepath.Join(clone, "f"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, "add", "f")
	gitIn(t, clone, "commit", "-q", "-m", "one")
	gitIn(t, clone, "push", "-q", "origin", "main")
	return origin, clone
}

func writeReport(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Two lanes record against the same commit; the note carries both blocks with
// the comment and directive lines dropped, `show` reads a metric back, and a
// re-run of a lane is a no-op rather than a duplicate block.
func TestPerfHistoryRecordsLanesOnceAndShowsThem(t *testing.T) {
	origin, clone := perfRepo(t)
	r1 := writeReport(t, t.TempDir(), "x86.txt", "# header\n# no-valgrind: absent\nx86_64/a.text\t100\nx86_64/a.ir\t2000\n")
	r2 := writeReport(t, t.TempDir(), "arm.txt", "aarch64/a.text\t120\n")

	if code, out := runPerfHistory(t, clone, "record", "perf-x86_64", r1); code != 0 {
		t.Fatalf("record x86: exit %d\n%s", code, out)
	}
	if code, out := runPerfHistory(t, clone, "record", "perf-aarch64", r2); code != 0 {
		t.Fatalf("record aarch64: exit %d\n%s", code, out)
	}
	code, out := runPerfHistory(t, clone, "record", "perf-x86_64", r1)
	if code != 0 || !strings.Contains(out, "already recorded") {
		t.Fatalf("second record of the same lane: exit %d, want a no-op\n%s", code, out)
	}

	// Read the note back from ORIGIN, not the clone: the push is the point.
	head := strings.TrimSpace(gitIn(t, clone, "rev-parse", "HEAD"))
	gitIn(t, origin, "update-ref", "refs/notes/perf-check", "refs/notes/perf")
	note := gitIn(t, origin, "notes", "--ref=refs/notes/perf", "show", head)
	for _, want := range []string{"# lane: perf-x86_64\n", "x86_64/a.text\t100\n", "x86_64/a.ir\t2000\n", "# lane: perf-aarch64\n", "aarch64/a.text\t120\n"} {
		if !strings.Contains(note, want) {
			t.Errorf("origin note missing %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "no-valgrind") || strings.Contains(note, "# header") {
		t.Errorf("comment/directive lines must not be recorded:\n%s", note)
	}
	if n := strings.Count(note, "# lane: perf-x86_64"); n != 1 {
		t.Errorf("lane block recorded %d times, want 1:\n%s", n, note)
	}

	code, out = runPerfHistory(t, clone, "show", "x86_64/a.ir", "10", "origin/main")
	if code != 0 {
		t.Fatalf("show: exit %d\n%s", code, out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), head[:12]+"  2000") || strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("show: want one `date  sha  value` line ending in %q, got:\n%s", head[:12]+"  2000", out)
	}

	code, out = runPerfHistory(t, clone, "metrics", "origin/main")
	if code != 0 || !strings.Contains(out, "x86_64/a.ir\tperf-x86_64") || !strings.Contains(out, "aarch64/a.text\tperf-aarch64") {
		t.Errorf("metrics: exit %d, want each metric tagged with its lane:\n%s", code, out)
	}
}

// The race the script exists to survive: between one job's fetch and push,
// another job lands its own block. A pre-push hook in the first clone plays the
// other job, so the first push is rejected as non-fast-forward exactly once.
func TestPerfHistorySurvivesAConcurrentLane(t *testing.T) {
	origin, clone := perfRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	rOther := writeReport(t, t.TempDir(), "other.txt", "x86_64/b.text\t7\n")

	hooks := filepath.Join(clone, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fires on the first push only: the marker file is created by the hook.
	marker := filepath.Join(t.TempDir(), "raced")
	hook := "#!/usr/bin/env bash\n" +
		"[ -e '" + marker + "' ] && exit 0\n" +
		"touch '" + marker + "'\n" +
		"cd '" + other + "' && bash '" + perfHistoryScript(t) + "' record perf-other '" + rOther + "' >/dev/null 2>&1\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}

	r := writeReport(t, t.TempDir(), "mine.txt", "x86_64/a.text\t100\n")
	code, out := runPerfHistory(t, clone, "record", "perf-x86_64", r)
	if code != 0 {
		t.Fatalf("record under contention: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "rejected (attempt 1)") {
		t.Fatalf("the hook should have forced one rejected push; output:\n%s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the racing lane never ran; the test proves nothing")
	}

	head := strings.TrimSpace(gitIn(t, clone, "rev-parse", "HEAD"))
	note := gitIn(t, origin, "notes", "--ref=refs/notes/perf", "show", head)
	for _, want := range []string{"# lane: perf-other\nx86_64/b.text\t7\n", "# lane: perf-x86_64\nx86_64/a.text\t100\n"} {
		if !strings.Contains(note, want) {
			t.Errorf("origin note lost a lane; missing %q:\n%s", want, note)
		}
	}
}

// A GIT_DIR in the ambient environment must not reach the fixtures.
//
// Every git command below names its repository by working directory, and
// GIT_DIR outranks that: with one set, `perfRepo` builds nothing, the `add`
// and `commit` land in whatever repository the variable names, and the script
// records its note there too. The decoy here is a path that does not exist, so
// a leak is a failure rather than a write into someone else's checkout.
func TestPerfHistoryIgnoresAnInheritedGitDir(t *testing.T) {
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "decoy.git"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())

	origin, clone := perfRepo(t)
	r := writeReport(t, t.TempDir(), "x86.txt", "x86_64/a.text\t100\n")
	if code, out := runPerfHistory(t, clone, "record", "perf-x86_64", r); code != 0 {
		t.Fatalf("record: exit %d\n%s", code, out)
	}
	head := strings.TrimSpace(gitIn(t, clone, "rev-parse", "HEAD"))
	note := gitIn(t, origin, "notes", "--ref=refs/notes/perf", "show", head)
	if !strings.Contains(note, "x86_64/a.text\t100\n") {
		t.Errorf("the note did not reach the fixture origin:\n%s", note)
	}
}
