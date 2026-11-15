package e2ecompiler

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitDiffCases are the forms of the diff of log: the patch, the stats,
// the summary, the context, the prefixes, the searches of xdiff, the
// graph, the pathspec, and `-s` with the other forms.
var gitDiffCases = []string{
	"log -p", "log -p --oneline", "log -p --format=%s", "log --stat", "log --stat --oneline", "log --numstat", "log --shortstat",
	"log --summary", "log --summary --oneline", "log --patch-with-stat", "log -p --stat --numstat --shortstat --summary",
	"log -U0", "log -U1 --oneline", "log -U10", "log --unified=2", "log -p --no-prefix", "log -p --src-prefix=x/ --dst-prefix=y/",
	"log -p --minimal", "log -p --patience", "log -p --histogram", "log -p --diff-algorithm=histogram", "log -p --no-indent-heuristic",
	"log -p --full-index", "log -p --first-parent", "log -p --graph", "log --stat --graph", "log --stat --graph --oneline",
	"log --numstat --graph --oneline", "log -p --graph --oneline --all", "log --stat --all --graph", "log -p -- a.txt",
	"log -p --full-diff -- a.txt", "log --stat -- dir", "log -p --no-renames", "log --stat --no-renames", "log --stat=60",
	"log --stat=100,20", "log --stat=200,10,1", "log --stat-width=50", "log --stat-name-width=10", "log --stat-graph-width=5",
	"log --stat-count=2", "log -p -s", "log -s --stat", "log --stat -s", "log --name-status -s", "log -s --name-status",
	"log --name-only --name-status", "log --name-status -p", "log -p --text", "log -p --inter-hunk-context=3",
	"log -p -U0 --inter-hunk-context=1", "log -p --reverse", "log -p --format=", "log --stat --format=", "log -p --pretty=format:%h",
	"log --stat --pretty=format:%h", "log -p --pretty=tformat:%h", "log -p --pretty=raw", "log -p --pretty=fuller",
	"log -p --pretty=reference", "log -p side", "log -p -U",
}

// gitDiffFixture makes a repository with each kind of change: text with
// and without a line end at the end, binary files, a change of mode, a
// rename with and without changes, links, a file that becomes a link,
// names that git quotes, a long path, a large change, and a merge.
func gitDiffFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
	t.Helper()
	when := 1700000000
	run := func(args ...string) {
		t.Helper()
		cmd := oracle.cmd(args...)
		cmd.Dir = dir
		stamp := fmt.Sprintf("%d +0100", when)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
			"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL=c@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		os.Remove(path)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(name, target string) {
		t.Helper()
		os.Remove(filepath.Join(dir, name))
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg ...string) {
		t.Helper()
		when += 3600
		run("add", "-A")
		args := []string{"commit", "-q"}
		for _, m := range msg {
			args = append(args, "-m", m)
		}
		run(args...)
	}
	seq := func(n int, f func(int) string) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			b.WriteString(f(i) + "\n")
		}
		return b.String()
	}
	run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	write("code.c", "int main(void)\n{\n\treturn 0;\n}\n")
	write("a.txt", "one\ntwo\nthree\n")
	write("nonl", "no newline")
	write("bin.dat", "bin\x00ary\x01\x02\x03")
	write("sp ace.txt", "x\n")
	write("t\tb", "tab\n")
	write("caf\xc3\xa9", "q\n")
	write("dir/sub/file", "deep\n")
	write("long.txt", seq(50, func(i int) string { return fmt.Sprint(i) }))
	write("empty", "")
	commit("first")
	write("code.c", "int main(void)\n{\n\tint x = 1;\n\treturn x;\n}\n")
	write("a.txt", "one\n2\nthree\nfour\n")
	write("nonl", "no newline at all")
	write("bin.dat", "bin\x00ary\x01\x02\x03\x04\x05")
	if err := os.Chmod(filepath.Join(dir, "sp ace.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("mv", "dir/sub/file", "dir/moved")
	write("long.txt", seq(50, func(i int) string {
		if i == 25 {
			return "twenty-five"
		}
		return fmt.Sprint(i)
	}))
	write("empty", "now\n")
	commit("second", "with a body")
	run("mv", "long.txt", "long2.txt")
	write("long2.txt", seq(50, func(i int) string {
		if i == 25 {
			return "twenty-five"
		}
		return fmt.Sprint(i)
	})+"extra\n")
	link("link", "target")
	link("a.txt", "a-target")
	os.Remove(filepath.Join(dir, "caf\xc3\xa9"))
	commit("third: rename and links")
	if err := os.Chmod(filepath.Join(dir, "sp ace.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("bin.dat", "bin\x00new")
	write("newfile", "line\n")
	write("empty2", "")
	commit("fourth")
	rnd := rand.New(rand.NewSource(5))
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("%d %s", i, strings.Repeat("x", rnd.Intn(10))))
	}
	write("big.txt", strings.Join(lines, "\n")+"\n")
	commit("fifth: big")
	var kept []string
	for _, l := range lines {
		if rnd.Float64() > 0.2 {
			kept = append(kept, l)
		}
	}
	write("big.txt", strings.Join(kept, "\n"))
	write("very/long/directory/name/that/goes/on/and/on/for/a/while/longer/than/forty/file-with-a-long-name.txt", "deep\n")
	commit("sixth")
	run("checkout", "-q", "-b", "side", "HEAD~2")
	write("side.txt", "side\n")
	commit("side commit")
	run("checkout", "-q", "main")
	when += 3600
	run("merge", "-q", "--no-ff", "-m", "merge side", "side")
}

// gitEdits makes commits that change texts of code at random: lines go
// in, go out, change and move, with blank lines, braces and indents that
// the heuristics of xdiff see, and with texts of thousands of lines.
func gitEdits(t *testing.T, oracle gitTool, dir string, seed int64, n int) {
	t.Helper()
	rnd := rand.New(rand.NewSource(seed))
	pool := []string{"int main(void)", "{", "\treturn 0;", "}", "", "", "static int x;", "\tif (x) {", "\t\tx++;", "\t}",
		"// comment", "    indented four", "        eight", "def f():", "    pass", "end", "begin", "  two", "_under", "$dollar"}
	texts := map[string][]string{}
	names := []string{"e/one.c", "e/two.py", "e/three"}
	for i := 0; i < n; i++ {
		name := names[rnd.Intn(len(names))]
		lines := texts[name]
		if lines == nil {
			for k := 0; k < []int{5, 50, 2000}[rnd.Intn(3)]; k++ {
				lines = append(lines, pool[rnd.Intn(len(pool))])
			}
		}
		for k := 0; k < []int{1, 2, 5, 30}[rnd.Intn(4)]; k++ {
			at := rnd.Intn(len(lines) + 1)
			switch rnd.Intn(4) {
			case 0:
				var add []string
				for j := 0; j < 1+rnd.Intn(6); j++ {
					add = append(add, pool[rnd.Intn(len(pool))])
				}
				lines = append(lines[:at], append(add, lines[at:]...)...)
			case 1:
				end := at + 1 + rnd.Intn(4)
				if end > len(lines) {
					end = len(lines)
				}
				lines = append(lines[:at], lines[end:]...)
			case 2:
				if at < len(lines) {
					lines[at] = fmt.Sprintf("changed %d %d", i, k)
				}
			default:
				lines = append(lines[:at], append([]string{fmt.Sprintf("new %d", i)}, lines[at:]...)...)
			}
		}
		texts[name] = lines
		var b bytes.Buffer
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", fmt.Sprintf("edit %d", i)}} {
			cmd := oracle.cmd(args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
				"GIT_AUTHOR_NAME=E", "GIT_AUTHOR_EMAIL=e@example.com", "GIT_COMMITTER_NAME=E", "GIT_COMMITTER_EMAIL=e@example.com",
				fmt.Sprintf("GIT_AUTHOR_DATE=%d +0000", 1700000000+i), fmt.Sprintf("GIT_COMMITTER_DATE=%d +0000", 1700000000+i))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
}

// TestSelfHostExampleGitDiff runs each form of the diff of log with git
// and with the port, at the top of the worktree and in a directory below
// it, and the patches of random edits with each search of xdiff.
func TestSelfHostExampleGitDiff(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			gitDiffFixture(t, oracle, dir, run)
			for _, at := range []string{dir, filepath.Join(dir, "dir")} {
				for _, c := range gitDiffCases {
					gitCompare(t, oracle, port, at, "", strings.Fields(c)...)
				}
			}
			for _, cols := range []string{"COLUMNS=60", "COLUMNS=200"} {
				gitCompareEnv(t, oracle, port, dir, []string{cols}, "log", "--stat", "--graph")
			}
			gitEdits(t, oracle, dir, 7, 40)
			for _, c := range []string{"log -p -n 40", "log -p -n 40 --patience", "log -p -n 40 --histogram", "log -p -n 40 --minimal",
				"log -p -n 40 --no-indent-heuristic", "log -U0 -n 40", "log -U1 -n 40 --inter-hunk-context=2", "log --stat -n 40", "log --numstat -n 40"} {
				gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
			}
		})
	}
}

// gitCompareEnv runs one command with git and with the port in dir, with
// more variables in the environment.
func gitCompareEnv(t *testing.T, oracle, port gitTool, dir string, env []string, args ...string) {
	t.Helper()
	with := func(g gitTool) gitTool {
		return gitTool{name: g.name, cmd: func(a ...string) *exec.Cmd {
			cmd := g.cmd(a...)
			cmd.Env = env
			return cmd
		}}
	}
	gitCompare(t, with(oracle), with(port), dir, "", args...)
}
