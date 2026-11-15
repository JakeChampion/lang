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
	"log --oneline --follow -- long2.txt", "log --name-status --follow -- long2.txt", "log -p --follow -- long2.txt",
	"log --stat --follow -- dir/moved", "log --oneline --follow -- dir/moved", "log --oneline --follow -- a.txt",
	"log --oneline --follow -- big.txt", "log --oneline -S twenty", "log -p -S twenty", "log --oneline -G x{9}",
	"log --oneline -S bin", "log --oneline -G ary", "log --oneline --follow --all -- side.txt",
	"log --oneline --first-parent -S side", "log --oneline --follow -- nosuch",
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

// gitRandomMerges makes merges of two and three branches whose files of
// lines change at random on each branch. A merge with conflicts takes
// lines of each side and lines of its own, and a clean merge sometimes
// changes a line of its own, so that the combined diffs have each kind of
// hunk.
func gitRandomMerges(t *testing.T, oracle gitTool, dir string, seed int64, n int, hash gitRun) {
	t.Helper()
	rnd := rand.New(rand.NewSource(seed))
	when := 1700000000
	run := func(args ...string) int {
		t.Helper()
		cmd := oracle.cmd(args...)
		cmd.Dir = dir
		stamp := fmt.Sprintf("%d +0000", when)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
			"GIT_AUTHOR_NAME=M", "GIT_AUTHOR_EMAIL=m@example.com", "GIT_COMMITTER_NAME=M", "GIT_COMMITTER_EMAIL=m@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		} else if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return 0
	}
	must := func(args ...string) {
		t.Helper()
		if code := run(args...); code != 0 {
			t.Fatalf("git %v: exit %d", args, code)
		}
	}
	output := func(args ...string) string {
		t.Helper()
		cmd := oracle.cmd(args...)
		cmd.Dir = dir
		stamp := fmt.Sprintf("%d +0000", when)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
			"GIT_AUTHOR_NAME=M", "GIT_AUTHOR_EMAIL=m@example.com", "GIT_COMMITTER_NAME=M", "GIT_COMMITTER_EMAIL=m@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	pool := []string{"alpha", "beta", "gamma", "delta", "", "int f(void)", "{", "}", "\treturn 0;", "_x", "$y"}
	names := []string{"one.txt", "two.c", "three"}
	edit := func() {
		for _, name := range names {
			if rnd.Intn(2) == 0 {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(dir, name))
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			for k := 0; k < 1+rnd.Intn(4); k++ {
				at := rnd.Intn(len(lines) + 1)
				switch rnd.Intn(3) {
				case 0:
					lines = append(lines[:at], append([]string{pool[rnd.Intn(len(pool))]}, lines[at:]...)...)
				case 1:
					if at < len(lines) {
						lines = append(lines[:at], lines[at+1:]...)
					}
				default:
					if at < len(lines) {
						lines[at] = fmt.Sprintf("%s %d", pool[rnd.Intn(len(pool))], when)
					}
				}
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		when += 60
		must("add", "-A")
		must("commit", "-q", "--allow-empty", "-m", fmt.Sprintf("edit %d", when))
	}
	must("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	for _, name := range names {
		var b strings.Builder
		for k := 0; k < 12; k++ {
			b.WriteString(pool[rnd.Intn(len(pool))] + "\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("add", "-A")
	must("commit", "-q", "-m", "base")
	for i := 0; i < n; i++ {
		sides := []string{"main"}
		for k := 0; k < 1+rnd.Intn(2); k++ {
			b := fmt.Sprintf("s%d-%d", i, k)
			must("checkout", "-q", "-b", b, "main")
			edit()
			sides = append(sides, b)
		}
		must("checkout", "-q", "main")
		edit()
		when += 60
		if run(append([]string{"merge", "-q", "--no-ff", "--no-commit"}, sides[1:]...)...) != 0 || rnd.Intn(3) == 0 {
			for _, name := range names {
				var b strings.Builder
				for _, side := range sides {
					cmd := oracle.cmd("show", side+":"+name)
					cmd.Dir = dir
					out, _ := cmd.Output()
					for _, l := range strings.SplitAfter(string(out), "\n") {
						if l != "" && rnd.Intn(3) != 0 {
							b.WriteString(l)
						}
					}
				}
				if rnd.Intn(2) == 0 {
					b.WriteString(fmt.Sprintf("own %d\n", i))
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		// An octopus merge with conflicts stops, so commit-tree makes each
		// merge.
		must("add", "-A")
		args := []string{"commit-tree", output("write-tree"), "-m", fmt.Sprintf("merge %d", i)}
		for _, side := range sides {
			args = append(args, "-p", output("rev-parse", side))
		}
		must("update-ref", "refs/heads/main", output(args...))
		must("reset", "-q", "--hard")
	}
}

// gitMovedCases are the forms of --color-moved for the repository of
// gitMovedFixture. Each case is split at spaces, and `\s` is a space.
var gitMovedCases = []string{
	"log -p --color --color-moved", "log -p --color --color-moved=plain", "log -p --color --color-moved=blocks",
	"log -p --color --color-moved=zebra", "log -p --color --color-moved=default", "log -p --color --color-moved=dimmed-zebra",
	"log -p --color --color-moved=dimmed_zebra", "log -p --color --color-moved=no", "log -p --color --color-moved=true",
	"log -p --color --color-moved=bad", "log -p --color-moved", "log -p --color --no-color-moved",
	"log -p --color --color-moved --color-moved-ws=ignore-space-change", "log -p --color --color-moved --color-moved-ws=ignore-space-at-eol",
	"log -p --color --color-moved --color-moved-ws=ignore-all-space",
	"log -p --color --color-moved --color-moved-ws=allow-indentation-change",
	"log -p --color --color-moved=dimmed-zebra --color-moved-ws=allow-indentation-change", "log -p --color --color-moved --color-moved-ws=no",
	"log -p --color --color-moved --color-moved-ws=bogus",
	"log -p --color --color-moved --color-moved-ws=allow-indentation-change,ignore-all-space",
	"log -p --color --color-moved --color-moved-ws=\\signore-space-change\\s,ignore-space-at-eol", "log -p --color --color-moved --graph",
	"log -p --color --color-moved -U0", "log -p --color --color-moved --stat", "log -p --color", "log -p --color --color-moved",
	"log -p --color --color-moved=blocks", "log -p --color", "log -p --color",
}

// gitMovedFixture makes moved blocks for gitMovedCases: in a file, across
// files, two blocks next to each other, a short block, and blocks with a
// new indent and new spaces.
func gitMovedFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
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
	commit := func(msg string, files map[string]string) {
		t.Helper()
		for name, text := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		when += 3600
		run("add", "-A")
		run("commit", "-q", "-m", msg)
	}
	fn := func(indent, name, args string) string {
		return indent + "int " + name + "(void)\n" + indent + "{\n" + indent + "\treturn compute_" + name + "_value(" + args + ");\n" + indent + "}\n"
	}
	helpers := []string{"static int helper_one = 1;\n", "static int helper_two = 2;\n", "static int helper_three = 3;\n"}
	run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	commit("one", map[string]string{
		"a.c": fn("", "alpha", "1, 2, 3") + "\n" + fn("", "beta", "4, 5, 6") + "\n" + fn("", "gamma", "7, 8, 9") + "x\ny\n",
		"b.c": helpers[0] + helpers[1] + helpers[2],
	})
	commit("two", map[string]string{
		"a.c": fn("", "gamma", "7, 8, 9") + "\n" + fn("", "beta", "4, 5, 6") + "y\nx\n" + helpers[0] + helpers[1],
		"b.c": fn("", "alpha", "1, 2, 3") + helpers[2],
	})
	commit("three", map[string]string{
		"a.c": "    int gamma(void)\n    {\n    \treturn compute_gamma_value(7, 8, 9);\n    }\n\nint beta(void)\n{\n  return  compute_beta_value(4, 5, 6);  \n}\ny\nx\n",
		"b.c": helpers[0] + helpers[1] + fn("\t", "alpha", "1, 2, 3") + helpers[2],
	})
}

// gitMergeCases are the diffs of merges for the repositories of
// gitFormsFixture and gitMergeFixture.
var gitMergeCases = []string{
	"log -m", "log -m -p", "log -m --raw", "log -m --stat --oneline", "log -m -p --format=%h", "log -m --name-status", "log -m -p --first-parent",
	"log --first-parent -m -p --oneline", "log -m -p --oneline -- words.txt", "log -m -p --graph --oneline", "log --cc", "log -c", "log --cc --oneline",
	"log --cc --stat", "log --cc --raw", "log -c --raw", "log --cc --name-status", "log -c --name-only", "log --cc --stat -p",
	"log --cc --numstat --shortstat", "log --cc --summary", "log --cc --raw -p", "log --cc --raw --stat", "log --cc --raw --stat -p --summary",
	"log -c --format=%h", "log --cc --format=", "log --cc -s", "log --cc --graph --oneline", "log --cc --graph", "log --cc --graph --stat",
	"log --cc -m -p --oneline", "log -m --cc -p --oneline", "log --cc -U0", "log -c -U1", "log --cc -U10", "log --cc --color", "log -c --color --graph",
	"log --cc --full-index", "log --cc --no-prefix", "log --cc --abbrev=12 --raw", "log --cc -- words.txt", "log --cc --first-parent",
	"log --diff-merges=c", "log --diff-merges=separate", "log --diff-merges=bogus", "log --diff-merges=on", "log --diff-merges=off -p", "log --dd",
	"log --no-diff-merges -p", "log --diff-merges=1", "log --diff-merges=c --oneline -2", "log --diff-merges=cc --stat", "log --cc --name-only --stat",
	"log --cc -p --minimal", "log --cc --patience", "log -c --histogram",
}

// gitMergeFixture makes merges for gitMergeCases: a clean merge, an
// octopus merge that adds, deletes and changes files and a binary file,
// and a merge that changes the mode of a file.
func gitMergeFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
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
		if out, err := cmd.CombinedOutput(); err != nil && args[0] != "merge" {
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
	seq := func(from, to int) string {
		var b strings.Builder
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "%d\n", i)
		}
		return b.String()
	}
	code := func(one, two string) string {
		return "int f(void)\n{\n\treturn " + one + ";\n}\n\nint g(void)\n{\n\treturn " + two + ";\n}\n"
	}
	chmod := func(name string, mode os.FileMode) {
		t.Helper()
		if err := os.Chmod(filepath.Join(dir, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	write("code.c", code("1", "2"))
	write("n.txt", seq(1, 20))
	write("keep.txt", "keep\n")
	write("gone.txt", "gone\n")
	write("b.bin", "bin\x00x\n")
	commit("base")
	run("checkout", "-q", "-b", "a")
	write("code.c", code("10", "2"))
	write("n.txt", seq(1, 22))
	write("a.txt", "a\n")
	commit("on a")
	run("checkout", "-q", "-b", "b", "main")
	write("code.c", code("1", "20"))
	write("n.txt", seq(0, 20))
	write("b.bin", "bin\x00y\n")
	commit("on b")
	run("checkout", "-q", "-b", "c", "main")
	write("c.txt", "c\n")
	chmod("keep.txt", 0o755)
	commit("on c")
	run("checkout", "-q", "main")
	write("m.txt", "main\n")
	commit("on main")
	when += 3600
	run("merge", "-q", "--no-ff", "-m", "clean merge of c", "c")
	when += 3600
	run("merge", "-q", "--no-ff", "--no-commit", "a", "b")
	write("code.c", code("10", "20")+"int h;\n")
	write("n.txt", seq(0, 22))
	os.Remove(filepath.Join(dir, "gone.txt"))
	write("b.bin", "bin\x00z\n")
	write("new.txt", "new\n")
	commit("octopus with changes")
	run("checkout", "-q", "-b", "d", "HEAD~1")
	write("keep.txt", "d\n")
	commit("on d")
	run("checkout", "-q", "main")
	when += 3600
	run("merge", "-q", "--no-ff", "--no-commit", "d")
	write("keep.txt", "merged\n")
	chmod("keep.txt", 0o644)
	commit("merge d with a change of mode")
}

// gitFormCases are the forms of log for the repository of gitFormsFixture.
// Each case is split at spaces, and `\s` in a case is a space.
var gitFormCases = []string{
	// The placeholders that change the columns of the next placeholder.
	"log --format=%<(10)%h|%s", "log --format=%<(5)%s|", "log --format=%>(30)%s|", "log --format=%><(30)%s|",
	"log --format=%<(20,trunc)%s|", "log --format=%<(20,ltrunc)%s|", "log --format=%<(20,mtrunc)%s|", "log --format=%>(21,mtrunc)%s|",
	"log --format=%<(1,trunc)%s|", "log --format=%<(2,trunc)%s|", "log --format=%<(3,mtrunc)%s|", "log --format=%<|(40)%h%s",
	"log --format=%h%<|(40)%s|", "log --format=%h%>|(40)%s|", "log --format=%h%>|(-10)%s|", "log --format=%h%<|(-100)%s|",
	"log --format=%h%<(-5)%s|", "log --format=%h%<(0)%s|", "log --format=%h%<()%s|", "log --format=%h%<(x)%s|",
	"log --format=%h%<(5x)%s|", "log --format=%h%<(10,foo)%s|", "log --format=%h%<(10,)%s|", "log --format=%h%<(10%s|",
	"log --format=%h%>>(10)%s|", "log --format=%h%>>(30)%s|", "log --format=%h\\s\\s\\s\\s%>>(30)%s|",
	"log --format=%h%Cred\\s\\s%Creset%>>(30)%s|", "log --format=%<(10)%Cred%h%Creset|", "log --format=%<(10)%C(auto)%h%Creset|",
	"log --color --format=%<(10)%C(auto)%h%d|", "log --color --format=%<(15,trunc)%C(auto)%d|", "log --format=%<(10)%%|",
	"log --format=%<(10)%Z|", "log --format=%<(10)x%s|", "log --format=%-<(10)%s|", "log --format=%+<(10)%s|",
	"log --format=%<(10)%+s|", "log --format=%<(10)%-b|", "log --format=%<(10)%<(20)%s|", "log --format=%<(30,trunc)%b|",
	"log --format=%<(30,ltrunc)%b|", "log --format=%<(31,mtrunc)%b|", "log --format=%>(80,ltrunc)%b|",
	"log --color --format=%C(red)aaaa\\s\\s%C(reset)%>>(10)%h|", "log --format=%h\\s%>>|(30)%s|", "log --format=%<(5)%x00|",
	"log --format=%<(12)%ad|%<(12)%ar|", "log --format=%<(10)%Cred%%h", "log --format=%<(10)", "log --format=%<(+10)%s|",
	"log --graph --format=%<|(30)%h%s", "log --graph --format=%h%<|(30)%s|", "log --graph --oneline --format=%h%>|(40)%s|",
	"log --format=%<(10,trunc)%an%<(10,trunc)%ae|", "log --format=%>(8)%ad --date=short",
	// The wrap of %w().
	"log --format=%w(30)%s%n%b", "log --format=%w(30,2,4)%B", "log --format=%w(20,3)%s", "log --format=%w(0,4,8)%B",
	"log --format=%w()%B", "log --format=%w(,2)%B", "log --format=%w(30)%s%w(40,1,1)%b%w()%s", "log --format=%w(30)%s%w(30)%b",
	"log --format=%w(-1)%s", "log --format=%w(30,-1)%s", "log --format=%w(16385)%s", "log --format=%w(16384)%s",
	"log --format=%w(10x)%s", "log --format=%+w(10)%s", "log --format=%\\sw(10)%s", "log --format=%w(10%s", "log --format=%w(5)%x09%s",
	"log --format=%w(12)ab%x0acd%x0a%x0aef%x0a-gh%s", "log --format=%w(8)%x00%s", "log --format=%w(20)%b", "log --format=%w(21,1,2)%b",
	"log --color --format=%w(15)%C(red)%s%Creset", "log --format=%w(\\s10\\s,\\s2)%s", "log --format=%w(25,0,4)%s%n%w(25,2,2)%b",
	"log --format=%n%-", "log --format=a%n%-Zb", "log --format=a%n%+", "log --format=%", "log --format=x%",
	// --raw and the lengths of the short names.
	"log --raw", "log --raw --oneline", "log --raw -p", "log --raw --stat", "log --raw --numstat --summary", "log --raw --name-status",
	"log --raw --name-only", "log --raw -s", "log -s --raw", "log --raw --graph", "log --raw --graph --oneline", "log --raw --full-index -1",
	"log --raw --no-abbrev -1", "log --raw --abbrev=12 -1", "log --raw --format=%h", "log --raw --format=", "log --raw --first-parent",
	"log --raw --no-renames", "log --oneline --abbrev=12 -1", "log --oneline --no-abbrev -1", "log --abbrev=5 -p -1", "log --no-abbrev -p -1",
	"log --abbrev=50 --raw -1", "log --abbrev=2 --raw -1", "log --abbrev --raw -1", "log --abbrev=0 --raw -1", "log --abbrev=x --raw -1",
	"log --format=%h%x20%p --abbrev=9 -1", "log --abbrev=12 -2", "log --oneline --parents --abbrev=9", "log --graph --oneline --abbrev=10",
	"log --pretty=raw --abbrev=8 -2", "log --pretty=reference --abbrev=11 -2",
	// --check and the errors of white space.
	"log --check", "log --check --oneline", "log --check --graph --oneline", "log --check --graph", "log --check --color",
	"log --check --format=%h", "log --check --format=", "log --check -p", "log --check --stat", "log --check --raw", "log --check --name-only",
	"log --name-only --check", "log --check -s", "log -s --check", "log --check --first-parent", "log --check -- code.c", "log -p --color",
	"log -p --color --graph",
	// --word-diff in each form, with the regexes of the words.
	"log -p --word-diff", "log -p --word-diff=plain", "log -p --word-diff=porcelain", "log -p --word-diff=color", "log -p --word-diff=none",
	"log -p --word-diff=bad", "log -p --color-words", "log -p --color-words=.", "log -p --color-words=[a-z]+",
	"log -p --word-diff-regex=[^[:space:]]", "log -p --word-diff-regex=.", "log -p --word-diff-regex [a-z]", "log -p --word-diff --color",
	"log -p --word-diff=porcelain --color", "log -p --word-diff --graph", "log -p --word-diff --graph --oneline", "log -p --word-diff=porcelain --graph",
	"log -p --color-words --graph", "log -p --word-diff -U0", "log -p --word-diff -U1", "log -p --word-diff --stat", "log --word-diff",
	"log -p --word-diff --no-color", "log -p --color-words --no-color", "log -p --word-diff-regex=( -1", "log -p --word-diff --cc",
	"log -p --word-diff=plain --word-diff", "log -p --color-words=x --word-diff=plain",
}

// gitWhitespaceRules are values of core.whitespace for the errors of
// white space of gitFormsFixture.
var gitWhitespaceRules = []string{
	"indent-with-non-tab", "tab-in-indent", "-trailing-space", "cr-at-eol", "trailing-space,-space-before-tab,tabwidth=4,indent-with-non-tab",
	"tabwidth=99", "tab-in-indent,indent-with-non-tab", "blank-at-eol", "-blank-at-eof", "trail", "-,,space", "tabwidth=2,indent-with-non-tab",
	"-blank-at-eol",
}

// gitFormsFixture makes a repository for gitFormCases: long messages with
// wide characters, changes of words, errors of white space, a moved block,
// a copy, and a merge with a change of its own.
func gitFormsFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
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
		if out, err := cmd.CombinedOutput(); err != nil && args[0] != "merge" {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
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
	seq := func(from, to int) string {
		var b strings.Builder
		for i := from; i <= to; i++ {
			if i == 20 && to == 35 {
				b.WriteString("twenty\n")
				continue
			}
			fmt.Fprintf(&b, "%d\n", i)
		}
		return b.String()
	}
	run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	write("code.c", "int main(void)\n{\n\treturn 0;\n}\n")
	write("words.txt", "one two three\nfour five six\nseven eight nine\n")
	write("num.txt", seq(1, 30))
	write("greek.txt", "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\neta\ntheta\niota\nkappa\nlambda\nmu\n")
	commit("first: a subject that is long enough to need a wrap at forty columns and more",
		"The body has two paragraphs. Each one is long enough to wrap at a width of thirty columns or so.",
		"Second paragraph: cafe caf\xc3\xa9 and wide \xe4\xb8\xad\xe6\x96\x87 text.")
	write("code.c", "int main(void)\n{\n\tint x = 1;  \n        return x;\n \treturn 0;\n}\n\n\n")
	write("words.txt", "one 2 three\nfour five six seven\neight nine\n")
	commit("second: white space errors and word changes")
	write("greek.txt", "alpha\nbeta\nzeta\neta\ntheta\niota\nkappa\ngamma\ndelta\nepsilon\nlambda\nmu\n")
	write("numcopy.txt", seq(1, 30))
	commit("third: move a block and copy a file")
	run("checkout", "-q", "-b", "side", "HEAD~1")
	write("words.txt", "one two three\nfour 5 six\nseven eight nine\nten\n")
	write("side.txt", "side\n")
	commit("side: change words")
	run("checkout", "-q", "main")
	when += 3600
	run("merge", "-q", "--no-ff", "--no-commit", "side")
	write("words.txt", "one 2 three\nfour 5 six seven\neight nine\nten\n")
	commit("merge side")
	write("num.txt", seq(1, 35))
	commit("fourth: num", "Short body.")
	// Each error of white space, a conflict marker, a rename, a file with
	// no line end at its end, and a binary file.
	write("f.txt", "a\n\tTab\n        eight spaces\n    four\n  \tmix\n=======\n<<<<<<< ours\n>>>>>>>\n|||||||x\ntrail \r\ncr\r\nlast\t\n\n\n")
	run("mv", "num.txt", "renamed.txt")
	write("renamed.txt", seq(1, 35)+"y \n")
	write("nonl.txt", "no newline \t")
	write("bin.dat", "bin\x00ary \n")
	commit("fifth: white space")
	write("f.txt", "a\n\n  \n")
	commit("sixth: blank lines")
}

// TestSelfHostExampleGitForms runs each case of gitFormCases with git and
// with the port.
func TestSelfHostExampleGitForms(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			gitFormsFixture(t, oracle, dir, run)
			for _, c := range gitFormCases {
				args := strings.Fields(c)
				for i := range args {
					args[i] = strings.ReplaceAll(args[i], "\\s", " ")
				}
				gitCompare(t, oracle, port, dir, "", args...)
			}
			for _, c := range gitMergeCases {
				gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
			}
			fixed := t.TempDir()
			gitMergeFixture(t, oracle, fixed, run)
			for _, c := range gitMergeCases {
				gitCompare(t, oracle, port, fixed, "", strings.Fields(c)...)
			}
			movedDir := t.TempDir()
			gitMovedFixture(t, oracle, movedDir, run)
			for _, c := range gitMovedCases {
				args := strings.Fields(c)
				for i := range args {
					args[i] = strings.ReplaceAll(args[i], "\\s", " ")
				}
				gitCompare(t, oracle, port, movedDir, "", args...)
			}
			for _, setting := range [][]string{{"diff.colorMoved", "plain"}, {"diff.colorMoved", "dimmed-zebra"}, {"diff.colorMovedWS", "allow-indentation-change"}} {
				if _, stderr, code := oracle.run(t, movedDir, "", "config", setting[0], setting[1]); code != 0 {
					t.Fatalf("git config %v: %s", setting, stderr)
				}
				for _, c := range []string{"log -p --color", "log -p --color --color-moved", "log -p --color --color-moved=blocks"} {
					gitCompare(t, oracle, port, movedDir, "", strings.Fields(c)...)
				}
			}
			edits := t.TempDir()
			gitInit(t, oracle, edits, run)
			gitEdits(t, oracle, edits, 11, 30)
			for _, c := range []string{"log -p --word-diff", "log -p --word-diff=porcelain", "log -p --color-words", "log -p --word-diff-regex=[a-z]+",
				"log -p --word-diff -U1 --graph",
				"log -p --color --color-moved", "log -p --color --color-moved=dimmed-zebra", "log -p --color --color-moved=plain",
				"log -p --color --color-moved --color-moved-ws=allow-indentation-change", "log -p --color --color-moved --color-moved-ws=ignore-all-space"} {
				gitCompare(t, oracle, port, edits, "", strings.Fields(c)...)
			}
			merges := t.TempDir()
			gitRandomMerges(t, oracle, merges, 3, 25, run)
			for _, c := range []string{"log --cc", "log -c", "log --cc -U1", "log -c -U0", "log --cc -U0", "log --cc --raw --stat", "log -m --stat",
				"log -m -p --oneline", "log --cc --color", "log --cc --patience", "log -c --histogram"} {
				gitCompare(t, oracle, port, merges, "", strings.Fields(c)...)
			}
			for _, rule := range gitWhitespaceRules {
				if _, stderr, code := oracle.run(t, dir, "", "config", "core.whitespace", rule); code != 0 {
					t.Fatalf("git config core.whitespace %s: %s", rule, stderr)
				}
				for _, c := range []string{"log --check", "log -p --color", "log --check --color --graph"} {
					gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
				}
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
