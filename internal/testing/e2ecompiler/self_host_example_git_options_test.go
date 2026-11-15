package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitOptionCases are the options of log for the repository of
// gitOptionsFixture: the mailmap and the decorations.
var gitOptionCases = []string{
	"log", "log --pretty=short", "log --pretty=full", "log --pretty=fuller", "log --pretty=raw",
	"log --format=%an|%aN|%ae|%aE|%al|%aL|%cn|%cN|%ce|%cE|%cl|%cL", "log --no-mailmap --format=%aN|%aE", "log --no-use-mailmap",
	"log --use-mailmap --pretty=full", "log --oneline --author=New", "log --oneline --author=Old", "log --oneline --author=joe",
	"log --oneline --author=Joe", "log --oneline --committer=Proper", "log --oneline --committer=Itter",
	"log --oneline --no-mailmap --author=Old", "log --oneline --author=side@new",
	"log --oneline --decorate", "log --oneline --decorate=short", "log --oneline --decorate=full", "log --oneline --decorate=no",
	"log --oneline --decorate=auto", "log --oneline --no-decorate", "log --decorate=full -2", "log --format=%d%n%D --decorate=full",
	"log --oneline --decorate --decorate-refs=refs/tags", "log --oneline --decorate --decorate-refs=foo",
	"log --oneline --decorate --decorate-refs-exclude=refs/tags", "log --oneline --decorate --decorate-refs-exclude=refs/heads/*",
	"log --oneline --decorate --clear-decorations",
	"log --oneline --left-right main...topic", "log --oneline --left-right --graph main...topic", "log --left-right main...topic",
	"log --format=%m%h --left-right main...topic", "log --format=%m%h main...side", "log --oneline --left-only main...topic",
	"log --oneline --right-only main...topic", "log --oneline --left-only main...side", "log --oneline --right-only side...main",
	"log --oneline --left-only main", "log --oneline --left-only --topo-order main",
	"log --oneline --cherry-pick main...topic", "log --oneline --cherry-mark main...topic", "log --oneline --cherry main...topic",
	"log --oneline --cherry-mark --left-right main...side", "log --oneline --cherry-pick --left-right main...pick",
	"log --oneline --cherry-mark pick...main", "log --oneline --cherry main...pick",
	"log --oneline --boundary main~2..main", "log --boundary main~2..main", "log --oneline --boundary --graph main~2..main",
	"log --format=%m%h --boundary topic..main", "log --oneline --boundary --graph topic..main", "log --boundary -p topic..side",
	"log --boundary --oneline -n1 topic..main", "log --boundary --oneline --skip=1 -n1 topic..main",
	"log --boundary --oneline --reverse topic..main", "log --left-right --boundary --format=%m%h main...side",
	"log --oneline --ancestry-path topic..main", "log --oneline --ancestry-path=side topic..main", "log --oneline --ancestry-path=topic main",
	"log --oneline --ancestry-path main", "log --oneline --ancestry-path --graph topic..main", "log --oneline --simplify-by-decoration",
	"log --oneline --simplify-by-decoration --decorate", "log --oneline --simplify-by-decoration -- s",
	"log --oneline --simplify-by-decoration --graph", "log --oneline --simplify-by-decoration --graph --all",
	"log --oneline --simplify-merges -- s", "log --oneline --graph --simplify-merges --all -- f",
}

// gitOptionsFixture makes a repository with a mailmap, branches, tags, a
// merge, and refs that git decorates only with --clear-decorations.
func gitOptionsFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
	t.Helper()
	when := 1700000000
	who := []string{"Old Name", "old@example.com", "Comm Itter", "C@Example.com"}
	run := func(args ...string) {
		t.Helper()
		cmd := oracle.cmd(args...)
		cmd.Dir = dir
		stamp := fmt.Sprintf("%d +0100", when)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
			"GIT_AUTHOR_NAME="+who[0], "GIT_AUTHOR_EMAIL="+who[1], "GIT_COMMITTER_NAME="+who[2], "GIT_COMMITTER_EMAIL="+who[3],
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		t.Helper()
		when += 3600
		run("add", "-A")
		run("commit", "-q", "-m", msg)
	}
	run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	write("f", "1\n")
	commit("one")
	who = []string{"joe", "joe@home.com", "joe", "joe@home.com"}
	write("f", "2\n")
	commit("two: fix")
	who = []string{"Other", "other@example.com", "Other", "other@example.com"}
	write("f", "3\n")
	commit("three")
	run("branch", "side", "HEAD~1")
	run("checkout", "-q", "side")
	who = []string{"Side Guy", "Side@Example.COM", "x", "x@x"}
	write("s", "s\n")
	commit("side one")
	write("s", "t\n")
	commit("side two")
	run("checkout", "-q", "main")
	write("f", "4\n")
	commit("four")
	when += 3600
	run("merge", "-q", "--no-ff", "-m", "merge side", "side")
	run("tag", "v1", "HEAD~1")
	run("tag", "-a", "-m", "annotated", "v2", "HEAD")
	run("branch", "topic", "HEAD~2")
	run("update-ref", "refs/foo/bar", "HEAD~1")
	run("update-ref", "refs/notes/x", "HEAD~2")
	run("update-ref", "refs/remotes/o/HEAD", "HEAD~3")
	// A branch with a copy of the change of "four", for --cherry-pick.
	run("checkout", "-q", "-b", "pick", "topic")
	write("f", "4\n")
	commit("four again")
	run("checkout", "-q", "main")
	write(".mailmap", "# comment line\nNew Name <new@example.com> Old Name <old@example.com>\nCommitter Proper <c@example.com>\n"+
		"Joe Real <joe@home.com>\n<other-new@example.com> <other@example.com>\nSide Person <side@new.com> <side@example.com>\n")
}

// TestSelfHostExampleGitOptions runs the options of log with git and
// with the port: the mailmap of `.mailmap`, of `mailmap.file` and of
// `mailmap.blob`, and the decorations.
func TestSelfHostExampleGitOptions(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			gitOptionsFixture(t, oracle, dir, run)
			for _, c := range gitOptionCases {
				gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
			}
			must := func(args ...string) {
				t.Helper()
				if _, stderr, code := oracle.run(t, dir, "", args...); code != 0 {
					t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
				}
			}
			// A mailmap of mailmap.file overrides .mailmap, and one of
			// mailmap.blob comes between them.
			if err := os.WriteFile(filepath.Join(dir, "other.map"), []byte("File Name <file@example.com> <old@example.com>\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			must("config", "mailmap.file", "other.map")
			gitCompare(t, oracle, port, dir, "", "log", "--format=%aN <%aE>")
			must("config", "mailmap.blob", "HEAD:f")
			gitCompare(t, oracle, port, dir, "", "log", "--format=%aN <%aE>")
			must("config", "log.mailmap", "false")
			gitCompare(t, oracle, port, dir, "", "log", "-2")
			must("config", "log.excludeDecoration", "refs/tags")
			gitCompare(t, oracle, port, dir, "", "log", "--oneline", "--decorate")
		})
	}
}
