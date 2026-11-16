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
	"log -g --graph", "log -g HEAD~1..HEAD", "log -g ^HEAD~1 HEAD", "log -g HEAD~1", "log --oneline -g main@{2}",
	"log --oneline -g HEAD@{1}", "log -g nosuch", "log --oneline -g main side", "log --oneline -g --all",
	"log -g --topo-order", "log --oneline -g -- f", "log -g --reverse", "log --oneline -g --no-merges",
	"log --oneline -g --since=1700010000", "log --oneline -g -p -1", "log --oneline -g --stat -2",
	"log -g --format=%gd%x09%gD%x09%gs -3 refs/heads/main", "log --oneline -g @{1}", "log -g --date=relative -2",
	"log -g -1 --pretty=raw", "log -g --decorate --oneline -2", "log -g side..main", "log -g --left-right --oneline -2",
	"log -g -3", "log -g --pretty=fuller -2", "log -g --format=%h|%gn|%ge|%gN|%gE|%gs", "log -g --oneline --author=New",
	"log -g --oneline --grep=side", "log -g --oneline -n 3 --skip=2", "log --oneline --walk-reflogs side",
	"log -g --date=iso --format=%gd -3", "log --oneline -S2", "log --oneline -G2", "log --oneline -S2 -- f", "log -p -S3",
	"log --oneline -S2 -n1", "log --oneline -S 4", "log --oneline -G [0-9]", "log --oneline -S x --pickaxe-regex",
	"log --oneline -S [34] --pickaxe-regex", "log --stat -S s", "log --stat -S s --pickaxe-all", "log --oneline -G S -i",
	"log --oneline -S S -i", "log --oneline --graph -S2", "log --oneline --follow -- f", "log --oneline --follow -- s",
	"log --oneline --follow f s", "log --name-status --follow -- s",
	// A vertical tab and a form feed are not white space for git.
	"log --format=%s|%b|%aN| -1 vt", "log --oneline --cherry-mark main...vt",
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
	// A branch with the change of "four" and a vertical tab, which is not
	// white space for the patch id.
	run("checkout", "-q", "-b", "vt", "topic")
	who = []string{"Tab", "tab@example.com", "Tab", "tab@example.com"}
	write("f", "4\v\n")
	commit("four\f\v\n\nbody\f\v")
	run("checkout", "-q", "main")
	write(".mailmap", "# comment line\nNew Name <new@example.com> Old Name <old@example.com>\nCommitter Proper <c@example.com>\n"+
		"Joe Real <joe@home.com>\n<other-new@example.com> <other@example.com>\nSide Person <side@new.com> <side@example.com>\n"+
		"\vTab Name\f <tab@example.com>\n")
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
			// With GIT_DIR, the current directory is the worktree, unless
			// the repository is bare.
			gitDir := []string{"GIT_DIR=.git"}
			for _, c := range []string{"log --format=%aN|%aE|%cN", "log --oneline -1 -- ./f", "log --oneline -1 -- ../f", "rev-parse HEAD:./f"} {
				gitCompareEnv(t, oracle, port, dir, gitDir, strings.Fields(c)...)
			}
			must("config", "core.bare", "true")
			gitCompareEnv(t, oracle, port, dir, gitDir, "log", "--format=%aN|%aE|%cN")
			gitCompareEnv(t, oracle, port, dir, gitDir, "rev-parse", "HEAD:./f")
			must("config", "core.bare", "false")
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

// gitColorCases are the forms of log with colors, for the repository of
// gitOptionsFixture and the commits that TestSelfHostExampleGitColor adds.
var gitColorCases = []string{
	"log --color", "log --color --oneline", "log --color --oneline --decorate=full", "log --color -p --stat",
	"log --color --graph --oneline --all", "log --color --graph --all --stat", "log --color --graph -p --all",
	"log --color --graph --oneline --boundary main~3..main", "log --color --left-right --oneline main...topic",
	"log --color --left-right main...topic", "log --color --pretty=reference -3", "log --color -p -U0 -2",
	"log --color --stat=50 --numstat --shortstat -3", "log --color --name-status -2", "log --color=always -1 -p",
	"log --color=never -p -1", "log --no-color -p -1", "log --color --no-color -p -1", "log --color=bad -1",
	"log --color --format=%Cred%h%Creset%x20%C(bold#102030)%s%C(reset)%C(auto)%d%x20%C(auto)%h%x20%H%x20%D",
	"log --color --format=%C(ul#102030brightblue)a%C(nobold200normal)b%C(normal)c%C(reset)%C(-1)d%C(9)e%C(default)",
	"log --format=%C(always,red)x%C(red)y%Cred%x20z%C(auto,blue)w -1", "log --color --format=%C(auto,blue)w%Cgreen -1",
	"log --color --format=%C(foo)x -1", "log --format=%C(foo)x -1", "log --color --graph --format=%C(auto)%h%d",
}

// TestSelfHostExampleGitColor runs log with colors with git and with the
// port: the header, the decorations, the columns of the graph, the patch
// with errors of white space, the stat, and the colors of a format.
func TestSelfHostExampleGitColor(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			gitOptionsFixture(t, oracle, dir, run)
			must := func(args ...string) {
				t.Helper()
				args = append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)
				if _, stderr, code := oracle.run(t, dir, "", args...); code != 0 {
					t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
				}
			}
			write := func(name, text string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// Errors of white space: a space before a tab, blanks at the
			// end of a line and at the end of the file, and a carriage
			// return.
			write("w", "int f(void)\n{\n\treturn 1;\n}\nx\n")
			must("add", "w")
			must("commit", "-q", "-m", "w one")
			write("w", "int f(void)\n{\n\treturn 2;  \n \tx\n\t \t y\r\n}\n\n  \n")
			must("add", "w")
			must("commit", "-q", "-m", "w two")
			// An octopus merge, for the dashes of the graph.
			for _, b := range []string{"o1", "o2", "o3"} {
				must("checkout", "-q", "-b", b, "main")
				write(b, b+"\n")
				must("add", b)
				must("commit", "-q", "-m", b)
			}
			must("checkout", "-q", "main")
			must("merge", "-q", "-m", "octopus", "o1", "o2", "o3")
			// git 2.43 cannot stash in a repository with SHA-256 names.
			must("update-ref", "refs/stash", "HEAD~1")
			for _, c := range gitColorCases {
				gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
			}
			gitCompare(t, oracle, port, dir, "", "log", "--color", "--oneline", "--decorate", "--all")
			gitCompare(t, oracle, port, dir, "", "log", "--color", "--format=%C(bold blue)%h%C(reset) %s", "-2")
			for _, v := range []string{"always", "true", "never", "false", "auto"} {
				must("config", "color.ui", v)
				gitCompare(t, oracle, port, dir, "", "log", "-p", "-1")
				must("config", "color.diff", "always")
				gitCompare(t, oracle, port, dir, "", "log", "--graph", "--oneline", "-3")
				must("config", "--unset", "color.diff")
			}
		})
	}
}

// TestSelfHostExampleGitPush reads `@{push}` with git and with the port:
// each `push.default`, a push refspec, `remote.pushDefault`,
// `branch.NAME.pushRemote`, and the branches that have no destination.
func TestSelfHostExampleGitPush(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			top := t.TempDir()
			up, dir := filepath.Join(top, "up"), filepath.Join(top, "repo")
			must := func(at string, args ...string) {
				t.Helper()
				if _, stderr, code := oracle.run(t, at, "", args...); code != 0 {
					t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
				}
			}
			for _, d := range []string{up, dir} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
				must(d, "init", "-q", "-b", "main", "--object-format="+run.format, ".")
				if err := os.WriteFile(filepath.Join(d, "f"), []byte(d+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				must(d, "add", "f")
				must(d, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "one")
			}
			must(up, "branch", "other")
			must(up, "branch", "topic")
			for _, args := range [][]string{{"remote", "add", "origin", up}, {"remote", "add", "fork", up}, {"fetch", "-q", "origin"}, {"fetch", "-q", "fork"},
				{"branch", "-q", "--set-upstream-to=origin/main", "main"}, {"branch", "-q", "topic"}, {"branch", "-q", "--set-upstream-to=origin/topic", "topic"},
				{"branch", "-q", "lone"}, {"branch", "-q", "diff"}, {"branch", "-q", "--set-upstream-to=origin/other", "diff"}, {"branch", "-q", "local"},
				{"config", "branch.local.remote", "."}, {"config", "branch.local.merge", "refs/heads/main"}, {"branch", "-q", "pr"},
				{"config", "branch.pr.pushRemote", "fork"}, {"branch", "-q", "--set-upstream-to=origin/main", "pr"}} {
				must(dir, args...)
			}
			branches := []string{"", "main", "diff", "topic", "lone", "local", "pr", "nosuch"}
			check := func() {
				t.Helper()
				for _, b := range branches {
					gitCompare(t, oracle, port, dir, "", "rev-parse", b+"@{push}")
				}
				gitCompare(t, oracle, port, dir, "", "log", "--oneline", "-1", "main@{PUSH}~0")
			}
			check()
			for _, mode := range []string{"current", "matching", "nothing", "upstream", "tracking", "simple"} {
				must(dir, "config", "push.default", mode)
				check()
			}
			must(dir, "config", "--unset", "push.default")
			must(dir, "config", "remote.origin.push", "refs/heads/main:refs/heads/other")
			check()
			must(dir, "config", "--unset", "remote.origin.push")
			must(dir, "config", "remote.pushDefault", "fork")
			check()
		})
	}
}
