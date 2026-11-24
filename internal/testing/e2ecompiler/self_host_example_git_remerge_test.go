package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gitRemergeCases are the forms of `log` with the diff of a merge against
// the merge of its two parents: the option and its other spellings, each
// form of the diff, the options that select and hide the changes, the
// forms of the text of a commit, and the other diffs of a merge after or
// before the option. The last cases are the other diffs of a merge with
// no form of the diff, with the pickaxe and with an ancestry path.
var gitRemergeCases = []string{
	"log --remerge-diff", "log --diff-merges=remerge", "log --diff-merges=r", "log --remerge-diff --all", "log --diff-merges=remerge --all",
	"log --remerge-diff --all --stat", "log --remerge-diff --all --raw", "log --remerge-diff --all --numstat --shortstat --format=%s",
	"log --remerge-diff --all --summary --format=%h", "log --remerge-diff --all --patch-with-stat", "log --remerge-diff --all --stat --summary --raw -p",
	"log --remerge-diff --all --name-only --oneline", "log --remerge-diff --all --name-status --format=%h", "log --remerge-diff --all -s",
	"log --remerge-diff --all --check", "log --diff-merges=remerge --all --stat", "log --diff-merges=r --all --name-status",
	"log --remerge-diff --all --oneline", "log --remerge-diff --all --format=", "log --remerge-diff --all --format=%h%x20%s",
	"log --remerge-diff --all --pretty=raw --parents", "log --remerge-diff --all --pretty=fuller", "log --remerge-diff --all --graph",
	"log --remerge-diff --all --graph --oneline --stat", "log --diff-merges=remerge --all --graph --oneline", "log --remerge-diff --all --color",
	"log --remerge-diff --all --color --color-moved", "log --remerge-diff --all --word-diff", "log --remerge-diff --all --color --graph --oneline",
	"log --remerge-diff --all -U0", "log --remerge-diff --all -U1 --no-renames", "log --remerge-diff --all -M20% --oneline",
	"log --remerge-diff --all -C --name-status", "log --remerge-diff --all --no-prefix --full-index", "log --remerge-diff --all --abbrev=12 --oneline --raw",
	"log --remerge-diff --all --src-prefix=x/ --dst-prefix=y/ -U5", "log --remerge-diff --all --histogram", "log --remerge-diff --all --patience --inter-hunk-context=2",
	"log --remerge-diff --all --text", "log --remerge-diff --all --oneline -S RESOLVED", "log --remerge-diff --all --oneline -S evil",
	"log --remerge-diff --all --oneline -G ^one", "log --remerge-diff --all --oneline -S nosuchtext", "log --remerge-diff --all --format=%h -- text.txt",
	"log --remerge-diff --all --format=%h -- dir moved-dir", "log --remerge-diff --all --oneline -- gone.txt", "log --remerge-diff --all --oneline -- r1.txt r2.txt",
	"log --remerge-diff --all --full-diff --format=%h -- text.txt",
	"log --remerge-diff --all --parents --format=%h -- text.txt", "log --remerge-diff --all --simplify-merges --format=%h -- text.txt dir",
	"log --remerge-diff --all --merges", "log --remerge-diff --all --no-merges --oneline", "log --remerge-diff --all --min-parents=3",
	"log --remerge-diff --all --first-parent", "log --first-parent --remerge-diff clean-1", "log --remerge-diff --all --date-order --format=%h",
	"log --remerge-diff --all --topo-order --oneline", "log --remerge-diff -n 3 --skip=1 --all", "log --remerge-diff content-1 --not base",
	"log --remerge-diff -m --all --format=%h", "log -m --remerge-diff --all --format=%h", "log --remerge-diff -c --all --format=%h",
	"log -c --remerge-diff --all --format=%h", "log --cc --diff-merges=remerge --all --format=%h", "log --remerge-diff --cc --all --format=%h",
	"log --remerge-diff --dd --all --format=%h", "log --dd --remerge-diff --all --format=%h", "log --remerge-diff --no-diff-merges --all --format=%h",
	"log --no-diff-merges --remerge-diff --all --format=%h", "log --remerge-diff --diff-merges=off --all --format=%h",
	"log --remerge-diff --diff-merges=1 --all --format=%h", "log --diff-merges=separate --diff-merges=remerge --all --format=%h",
	"log --remerge-diff --diff-merges=on --all --format=%h", "log --remerge-diff --diff-merges=m --all --stat --format=%h",
	"log --diff-merges=bogus", "log --remerge-diff -p --all --format=%h", "log -p --diff-merges=remerge --all --format=%h",
	// The diffs of a merge that an earlier option of the same kind set.
	"log -c -m --all --format=%h", "log --cc --diff-merges=off --all --format=%h", "log --dd --no-diff-merges --all --format=%h",
	"log --cc -m -p --all --format=%h",
	// With no form of the diff, a merge shows one time for each parent
	// with a change, and a merge with a combined diff always shows.
	"log --diff-merges=separate -s --oneline --all", "log --diff-merges=m -s --all -n 6", "log --diff-merges=separate -s --graph --oneline --all",
	"log --diff-merges=on -s --format=%h%x20%p --all -- dir", "log -m --oneline -S evil --all", "log -m --all --graph -S line --oneline",
	"log -c --oneline -S evil --all", "log --cc --oneline -S nosuchtext --all", "log -c -p --oneline -S RESOLVED --all",
	"log --cc --stat --oneline -S line --all", "log -c --all --graph -S line --oneline",
	// A parent that is not on the ancestry path can make a merge the same
	// as its other parents for the paths.
	"log --all --ancestry-path cross-2~1..cross-3 --oneline -- cross-ren.txt", "log --ancestry-path base..cross-4 --oneline -m -- text.txt",
	"log --remerge-diff --ancestry-path cross-1..cross-3 --format=%h -- text.txt",
	// A diff from each parent keeps each merge in a walk with paths.
	"log -m --all --oneline -- gone.txt", "log --diff-merges=separate --all --oneline -- text.txt", "log --dd --all --oneline -- text.txt",
	"log --diff-merges=first-parent --all --format=%h -- dir", "log -m --all --name-status --format=%h -- r1.txt",
}

// gitRemergeRepo makes the commits of a repository for the cases of
// `--remerge-diff`. Each commit is one hour after the commit before it.
type gitRemergeRepo struct {
	t      *testing.T
	oracle gitTool
	dir    string
	when   int
}

// try runs git and gives its output and its exit status.
func (g *gitRemergeRepo) try(args ...string) (string, int) {
	g.t.Helper()
	cmd := g.oracle.cmd(args...)
	cmd.Dir = g.dir
	stamp := fmt.Sprintf("%d +0000", g.when)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
		"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL=c@example.com",
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp, "GIT_MERGE_AUTOEDIT=no")
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		g.t.Fatalf("git %v: %v", args, err)
	}
	return string(out), 0
}

func (g *gitRemergeRepo) git(args ...string) string {
	g.t.Helper()
	out, code := g.try(args...)
	if code != 0 {
		g.t.Fatalf("git %v: exit %d\n%s", args, code, out)
	}
	return strings.TrimSpace(out)
}

func (g *gitRemergeRepo) write(name, text string) {
	g.t.Helper()
	path := filepath.Join(g.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		g.t.Fatal(err)
	}
	os.Remove(path)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gitRemergeRepo) link(name, target string) {
	g.t.Helper()
	path := filepath.Join(g.dir, name)
	os.RemoveAll(path)
	if err := os.Symlink(target, path); err != nil {
		g.t.Fatal(err)
	}
}

// move renames a file or a directory, as `git mv` does.
func (g *gitRemergeRepo) move(from, to string) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(g.dir, to)), 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.git("mv", from, to)
}

func (g *gitRemergeRepo) mode(name string, mode os.FileMode) {
	g.t.Helper()
	if err := os.Chmod(filepath.Join(g.dir, name), mode); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gitRemergeRepo) commit(msg string) {
	g.t.Helper()
	g.when += 3600
	g.git("add", "-A")
	g.git("commit", "-q", "--allow-empty", "-m", msg)
}

// merge merges the commits `others` into the current branch. A merge
// with conflicts keeps the files that git leaves in the worktree, and
// `resolve`, when it is not nil, then changes them.
func (g *gitRemergeRepo) merge(msg string, resolve func(), others ...string) {
	g.t.Helper()
	g.when += 3600
	g.try(append([]string{"merge", "-q", "--no-ff", "--no-commit"}, others...)...)
	if _, code := g.try("rev-parse", "-q", "--verify", "MERGE_HEAD"); code != 0 {
		g.t.Fatalf("git did not start the merge %q", msg)
	}
	if resolve != nil {
		resolve()
	}
	g.git("add", "-A")
	g.git("commit", "-q", "-m", msg)
}

// scenario makes the branches NAME-1 and NAME-2 from the commit `from`
// with the changes of `one` and of `two`, and merges NAME-2 into NAME-1.
func (g *gitRemergeRepo) scenario(name, from string, one, two, resolve func()) {
	g.t.Helper()
	g.git("checkout", "-q", "-b", name+"-1", from)
	one()
	g.commit(name + " one")
	g.git("checkout", "-q", "-b", name+"-2", from)
	two()
	g.commit(name + " two")
	g.git("checkout", "-q", name+"-1")
	g.merge("merge of "+name, resolve, name+"-2")
}

func gitRemergeLines(prefix string, n int, changed map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if text, ok := changed[i]; ok {
			b.WriteString(text + "\n")
		} else {
			fmt.Fprintf(&b, "%s %d\n", prefix, i)
		}
	}
	return b.String()
}

// gitRemergeFixture makes a repository with each kind of merge of two
// parents: a clean merge, conflicts of content that the merge resolved,
// a merge that changes a file that no parent changed, the conflicts of
// added, deleted and renamed files, of a binary file, of modes, of links
// and of kinds of entry, a file and a directory at one path, renamed
// directories with a file in the way of their new place, two merge bases,
// and an octopus merge. The branch `main`
// keeps the commit `base` and has the directory `sub`.
func gitRemergeFixture(t *testing.T, oracle gitTool, dir string, run gitRun) *gitRemergeRepo {
	t.Helper()
	g := &gitRemergeRepo{t: t, oracle: oracle, dir: dir, when: 1700000000}
	gitInit(t, oracle, dir, run)
	text := func(changed map[int]string) string { return gitRemergeLines("line", 20, changed) }
	other := func(changed map[int]string) string { return gitRemergeLines("row", 15, changed) }
	g.write("text.txt", text(nil))
	g.write("keep.txt", "keep\n")
	g.write("gone.txt", "one\ntwo\nthree\n")
	g.write("ren.txt", other(nil))
	g.write("bin.dat", "bin\x00ary\x01\x02")
	g.write("tool.sh", "#!/bin/sh\necho tool\n")
	g.link("link", "first")
	g.write("dir/a.txt", gitRemergeLines("a", 12, nil))
	g.write("dir/b.txt", gitRemergeLines("b", 12, nil))
	g.write("dir/c.txt", gitRemergeLines("c", 12, nil))
	g.write("sub/x.txt", "x\n")
	g.write("union.txt", "u1\nu2\nu3\n")
	g.write("plain.txt", "p1\np2\np3\n")
	g.write("wide.txt", "w1\nw2\nw3\n")
	g.write("noeol.txt", "n1\nn2\nn3")
	g.commit("base")
	g.git("tag", "base")

	g.scenario("clean", "base", func() { g.write("text.txt", text(map[int]string{2: "one top"})) },
		func() { g.write("text.txt", text(map[int]string{19: "two bottom"})) }, nil)
	g.scenario("content", "base", func() { g.write("text.txt", text(map[int]string{10: "one ten", 11: "same"})) },
		func() { g.write("text.txt", text(map[int]string{10: "two ten", 11: "same"})) },
		func() { g.write("text.txt", text(map[int]string{10: "RESOLVED", 11: "same"})) })
	g.scenario("markers", "base", func() { g.write("text.txt", text(map[int]string{5: "one five", 15: "one fifteen"})) },
		func() { g.write("text.txt", text(map[int]string{5: "two five", 16: "two sixteen"})) }, nil)
	g.scenario("evil", "base", func() { g.write("text.txt", text(map[int]string{3: "one three"})) },
		func() { g.write("dir/a.txt", gitRemergeLines("a", 12, map[int]string{6: "two six"})) },
		func() { g.write("keep.txt", "evil\n") })
	g.scenario("addadd", "base", func() { g.write("new.txt", "top\none\nend\n"); g.write("same.txt", "same\n") },
		func() { g.write("new.txt", "top\ntwo\nend\n"); g.write("same.txt", "same\n") }, nil)
	g.scenario("moddel", "base", func() { g.write("gone.txt", "one\ntwo\nthree\nfour\n") },
		func() { os.Remove(filepath.Join(dir, "gone.txt")) }, nil)
	g.scenario("delmod", "base", func() { os.Remove(filepath.Join(dir, "gone.txt")) },
		func() { g.write("gone.txt", "one\n2\nthree\n") }, func() { os.Remove(filepath.Join(dir, "gone.txt")) })
	g.scenario("rename", "base", func() { g.move("ren.txt", "moved.txt") },
		func() { g.write("ren.txt", other(map[int]string{4: "two four"})) }, nil)
	g.scenario("renamed-conflict", "base",
		func() { g.move("ren.txt", "moved.txt"); g.write("moved.txt", other(map[int]string{4: "one four"})) },
		func() { g.write("ren.txt", other(map[int]string{4: "two four"})) }, nil)
	g.scenario("rename12", "base", func() { g.move("ren.txt", "r1.txt") }, func() { g.move("ren.txt", "r2.txt") }, nil)
	g.scenario("rename12-content", "base",
		func() { g.move("ren.txt", "r1.txt"); g.write("r1.txt", other(map[int]string{7: "one seven"})) },
		func() { g.move("ren.txt", "r2.txt"); g.write("r2.txt", other(map[int]string{7: "two seven"})) }, nil)
	g.scenario("rename11", "base",
		func() { g.move("ren.txt", "r1.txt"); g.write("r1.txt", other(map[int]string{2: "one two"})) },
		func() { g.move("ren.txt", "r1.txt"); g.write("r1.txt", other(map[int]string{2: "two two"})) }, nil)
	g.scenario("rename-delete", "base", func() { g.move("ren.txt", "r3.txt") }, func() { os.Remove(filepath.Join(dir, "ren.txt")) }, nil)
	g.scenario("rename-add", "base", func() { g.move("ren.txt", "col.txt") },
		func() {
			g.write("col.txt", "another\nfile\n")
			g.write("ren.txt", other(map[int]string{9: "two nine"}))
		}, nil)
	g.scenario("rename-add-conflict", "base", func() { g.move("ren.txt", "col.txt"); g.write("col.txt", other(map[int]string{9: "one nine"})) },
		func() {
			g.write("col.txt", "another\nfile\n")
			g.write("ren.txt", other(map[int]string{9: "two nine"}))
		}, nil)
	g.scenario("rename21", "base", func() { g.move("ren.txt", "one-place.txt") }, func() { g.move("text.txt", "one-place.txt") }, nil)
	g.scenario("binary", "base", func() { g.write("bin.dat", "bin\x00one") }, func() { g.write("bin.dat", "bin\x00two") }, nil)
	g.scenario("mode", "base", func() { g.mode("tool.sh", 0o755) }, func() { g.write("tool.sh", "#!/bin/sh\necho tool two\n") }, nil)
	g.scenario("mode-content", "base", func() {
		g.mode("tool.sh", 0o755)
		g.write("tool.sh", "#!/bin/sh\necho one\n")
		g.mode("tool.sh", 0o755)
	},
		func() { g.write("tool.sh", "#!/bin/sh\necho two\n") }, nil)
	g.scenario("links", "base", func() { g.link("link", "one") }, func() { g.link("link", "two") }, nil)
	g.scenario("kinds", "base", func() { g.link("thing", "target") }, func() { g.write("thing", "a file\n") }, nil)
	g.scenario("link-file", "base", func() { g.link("text.txt", "elsewhere") }, func() { g.write("text.txt", text(map[int]string{8: "two eight"})) }, nil)
	g.scenario("file-dir", "base", func() { os.RemoveAll(filepath.Join(dir, "dir")); g.write("dir", "now a file\n") },
		func() { g.write("dir/a.txt", gitRemergeLines("a", 12, map[int]string{3: "two three"})) }, nil)
	g.scenario("dir-file", "base", func() { g.write("place/in.txt", "in\n") }, func() { g.write("place", "a file\n") }, nil)
	g.scenario("dir-gone", "base", func() { g.write("text.txt", text(map[int]string{1: "one one"})); g.write("keep.txt", "one\n") },
		func() {
			os.Remove(filepath.Join(dir, "text.txt"))
			os.Remove(filepath.Join(dir, "sub/x.txt"))
			g.write("sub/x.txt/deep", "deep\n")
		}, nil)
	g.scenario("dir-rename", "base", func() { g.move("dir", "moved-dir") }, func() { g.write("dir/new.txt", "new\n") }, nil)
	g.scenario("dir-rename-edit", "base",
		func() {
			g.move("dir", "moved-dir")
			g.write("moved-dir/a.txt", gitRemergeLines("a", 12, map[int]string{2: "one two"}))
		},
		func() {
			g.write("dir/b.txt", gitRemergeLines("b", 12, map[int]string{5: "two five"}))
			g.write("dir/deep/new.txt", "deep\n")
			g.move("dir/c.txt", "dir/d.txt")
		}, nil)
	g.scenario("dir-rename-way", "base", func() { g.move("dir", "moved-dir"); g.write("moved-dir/new.txt", "one\n") },
		func() { g.write("dir/new.txt", "two\n") }, nil)
	g.scenario("dir-rename-existing", "base", func() { g.move("dir", "moved-dir") },
		func() { g.write("dir/new.txt", "two\n"); g.write("moved-dir/new.txt", "also two\n") }, nil)
	g.scenario("dir-rename-collide", "base", func() { g.move("dir", "joined"); g.move("sub/x.txt", "joined/x.txt") },
		func() { g.write("dir/same.txt", "of dir\n"); g.write("sub/same.txt", "of sub\n") }, nil)
	g.scenario("dir-split", "base", func() {
		g.move("dir/a.txt", "left/a.txt")
		g.move("dir/b.txt", "right/b.txt")
		os.Remove(filepath.Join(dir, "dir/c.txt"))
	},
		func() { g.write("dir/new.txt", "new\n") }, nil)
	// Paths that git quotes, and a `/` in the name of a parent, which is
	// in the new path of a file that moves.
	g.scenario("with/slash", "base",
		func() {
			g.write("sp ace.txt", "top\none\n")
			g.write("caf\xc3\xa9", "top\none\n")
			g.link("t\tb", "target")
		},
		func() {
			g.write("sp ace.txt", "top\ntwo\n")
			g.write("caf\xc3\xa9", "top\ntwo\n")
			g.write("t\tb", "a file\n")
		}, nil)
	g.scenario("empty", "base", func() { g.write("empty-one", ""); os.Remove(filepath.Join(dir, "keep.txt")) },
		func() { g.write("empty-two", ""); g.write("keep.txt", "keep\nmore\n") }, nil)
	g.scenario("noeol", "base", func() { g.write("noeol.txt", "n1\nn2\none") }, func() { g.write("noeol.txt", "n1\nn2\ntwo") }, nil)
	g.scenario("attributes", "base",
		func() {
			g.write("union.txt", "u1\none\nu3\n")
			g.write("plain.txt", "p1\none\np3\n")
			g.write("wide.txt", "w1\none\nw3\n")
		},
		func() {
			g.write("union.txt", "u1\ntwo\nu3\n")
			g.write("plain.txt", "p1\ntwo\np3\n")
			g.write("wide.txt", "w1\ntwo\nw3\n")
		}, nil)

	// The source of a rename has the same score for two added files of
	// two directories that git walks after the others: the order of its
	// table of directories selects the rename.
	for _, pair := range [][2]string{{"aa", "zz"}, {"d1", "d2"}, {"lib", "src"}} {
		name := "tie-" + pair[0]
		g.git("checkout", "-q", "-b", name, "base")
		g.write(pair[0]+"/keep", "keep\n")
		g.write(pair[1]+"/keep", "keep\n")
		g.commit(name + " base")
		g.scenario(name, name, func() { g.write("ren.txt", other(map[int]string{3: "one three"})) },
			func() {
				os.Remove(filepath.Join(dir, "ren.txt"))
				g.write(pair[0]+"/new.txt", other(map[int]string{3: "two three"})+"more\n")
				g.write(pair[1]+"/new.txt", other(map[int]string{3: "two three"})+"more\n")
			}, nil)
	}
	// git does the merge of the trees a second time when the walk of the
	// deferred directories gives three times the paths.
	g.git("checkout", "-q", "-b", "again", "base")
	g.write("deep/x/in", "in\n")
	g.write("deep/x/e1", "1\n")
	g.write("deep/x/e2", "2\n")
	for _, name := range []string{"text.txt", "keep.txt", "ren.txt", "bin.dat", "tool.sh", "link", "union.txt", "plain.txt", "wide.txt", "noeol.txt"} {
		os.RemoveAll(filepath.Join(dir, name))
	}
	os.RemoveAll(filepath.Join(dir, "dir"))
	os.RemoveAll(filepath.Join(dir, "sub"))
	g.commit("again base")
	g.scenario("again", "again", func() { g.write("gone.txt", "one\nTWO\nthree\n") },
		func() {
			os.Remove(filepath.Join(dir, "gone.txt"))
			os.Remove(filepath.Join(dir, "deep/x/in"))
			g.write("deep/x/in/in", "deep\n")
		}, nil)

	// Two merge bases: each of two branches merges the other, and then
	// the two merges are merged. The merge of the two bases has a
	// conflict, and so the base of the last merge has conflict markers.
	g.git("checkout", "-q", "-b", "cross-1", "base")
	g.write("text.txt", text(map[int]string{12: "cross one"}))
	g.commit("cross one")
	g.git("checkout", "-q", "-b", "cross-2", "base")
	g.write("text.txt", text(map[int]string{12: "cross two"}))
	g.move("ren.txt", "cross-ren.txt")
	g.commit("cross two")
	g.git("checkout", "-q", "-b", "cross-3", "cross-1")
	g.merge("cross: two into one", func() { g.write("text.txt", text(map[int]string{12: "cross one wins"})) }, "cross-2")
	g.git("checkout", "-q", "cross-2")
	g.merge("cross: one into two", func() { g.write("text.txt", text(map[int]string{12: "cross two wins"})) }, "cross-1")
	g.git("checkout", "-q", "cross-3")
	g.merge("cross: the two merges", nil, "cross-2")
	g.git("checkout", "-q", "-b", "cross-4", "cross-1")
	g.write("text.txt", text(map[int]string{12: "cross one", 2: "later"}))
	g.commit("cross one later")
	g.merge("cross: clean with two bases", func() { g.write("text.txt", text(map[int]string{12: "cross done", 2: "later"})) }, "cross-2")

	// A merge of three parents has no such diff.
	g.git("checkout", "-q", "-b", "octopus", "base")
	for _, name := range []string{"o1", "o2"} {
		g.git("checkout", "-q", "-b", name, "base")
		g.write(name+".txt", name+"\n")
		g.commit(name)
	}
	g.git("checkout", "-q", "octopus")
	g.merge("octopus", nil, "o1", "o2")
	// A merge of a commit and one of its ancestors.
	g.git("checkout", "-q", "-b", "noff", "base")
	g.merge("no fast-forward", nil, "clean-2")
	g.git("checkout", "-q", "main")
	return g
}

// gitRemergeRandom makes a repository with a random history from `seed`:
// branches whose files change, go, move and change their kind, and merges
// of two branches, of which some resolve their conflicts and some change
// a file more. Each new line of a file is not in another place: a line
// that comes two times can give the histogram diff of git 2.43 other
// conflicts than that of git 2.56.1.
func gitRemergeRandom(t *testing.T, oracle gitTool, dir string, seed int64, n int, run gitRun) {
	t.Helper()
	g := &gitRemergeRepo{t: t, oracle: oracle, dir: dir, when: 1700000000}
	rnd := rand.New(rand.NewSource(seed))
	gitInit(t, oracle, dir, run)
	count := 0
	words := []string{"alpha", "beta", "gamma", "delta", "eps", "zeta", "eta", "theta"}
	line := func() string {
		count++
		return fmt.Sprintf("%s %d", words[rnd.Intn(len(words))], count)
	}
	lines := func(n int) []string {
		var out []string
		for i := 0; i < n; i++ {
			out = append(out, line())
		}
		return out
	}
	files := func() []string {
		var out []string
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			if !info.IsDir() {
				rel, _ := filepath.Rel(dir, path)
				out = append(out, rel)
			}
			return nil
		})
		sort.Strings(out)
		return out
	}
	// put writes a file when no file is in the way of its directory.
	put := func(name string, text []string, end bool) {
		path := filepath.Join(dir, name)
		if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
			return
		}
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			return
		}
		os.Remove(path)
		data := strings.Join(text, "\n")
		if end && len(text) > 0 {
			data += "\n"
		}
		os.WriteFile(path, []byte(data), 0o644)
	}
	read := func(name string) []string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return []string{line()}
		}
		var out []string
		for _, l := range strings.Split(string(data), "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	isLink := func(name string) bool {
		info, err := os.Lstat(filepath.Join(dir, name))
		return err == nil && info.Mode()&os.ModeSymlink != 0
	}
	dirs := []string{"", "", "a/", "a/b/", "c/", "lib/", "lib/sub/"}
	stems := []string{"f", "g", "h", "main", "util", "x", "data"}
	ends := []string{".txt", ".c", ""}
	newName := func() string {
		return dirs[rnd.Intn(len(dirs))] + stems[rnd.Intn(len(stems))] + fmt.Sprint(rnd.Intn(7)) + ends[rnd.Intn(len(ends))]
	}
	mutate := func() {
		fs := files()
		op := rnd.Intn(100)
		if len(fs) == 0 {
			op = 99
		}
		pick := func() string { return fs[rnd.Intn(len(fs))] }
		switch {
		case op < 34:
			f := pick()
			if isLink(f) {
				return
			}
			text := read(f)
			for k := rnd.Intn(4) + 1; k > 0; k-- {
				at := rnd.Intn(len(text) + 1)
				switch how := rnd.Intn(10); {
				case how < 4 && at < len(text):
					text[at] = line()
				case how < 7:
					text = append(text[:at], append([]string{line()}, text[at:]...)...)
				case at < len(text) && len(text) > 1:
					text = append(text[:at], text[at+1:]...)
				}
			}
			put(f, text, true)
		case op < 42:
			os.Remove(filepath.Join(dir, pick()))
		case op < 56:
			f, to := pick(), newName()
			if _, err := os.Lstat(filepath.Join(dir, to)); err == nil || os.MkdirAll(filepath.Dir(filepath.Join(dir, to)), 0o755) != nil {
				return
			}
			os.Rename(filepath.Join(dir, f), filepath.Join(dir, to))
		case op < 60:
			f := pick()
			if info, err := os.Stat(filepath.Join(dir, f)); err == nil && !isLink(f) {
				os.Chmod(filepath.Join(dir, f), info.Mode().Perm()^0o111)
			}
		case op < 64:
			name := newName()
			if rnd.Intn(2) == 0 {
				name = pick()
			}
			path := filepath.Join(dir, name)
			if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
				return
			}
			if info, err := os.Lstat(path); err == nil && info.IsDir() {
				return
			}
			os.Remove(path)
			os.Symlink(words[rnd.Intn(len(words))], path)
		case op < 68:
			name := newName()
			if rnd.Intn(5) < 3 {
				name = pick()
			}
			data := make([]byte, rnd.Intn(30)+4)
			rnd.Read(data)
			path := filepath.Join(dir, name)
			if info, err := os.Lstat(path); (err == nil && info.IsDir()) || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
				return
			}
			os.Remove(path)
			os.WriteFile(path, append(data, 0), 0o644)
		case op < 74:
			// The files of one directory move to a new directory.
			from := filepath.Dir(pick())
			to := []string{"m", "a/n", "lib2", "c/d", "q/r"}[rnd.Intn(5)]
			if from == "." || strings.HasPrefix(to+"/", from+"/") || strings.HasPrefix(from+"/", to+"/") {
				return
			}
			if _, err := os.Lstat(filepath.Join(dir, to)); err == nil || os.MkdirAll(filepath.Dir(filepath.Join(dir, to)), 0o755) != nil {
				return
			}
			os.Rename(filepath.Join(dir, from), filepath.Join(dir, to))
		case op < 78:
			f, to := pick(), newName()
			if _, err := os.Lstat(filepath.Join(dir, to)); err == nil || isLink(f) {
				return
			}
			put(to, read(f), true)
		case op < 81:
			f := pick()
			if !isLink(f) {
				put(f, append(read(f), line()), false)
			}
		case op < 84:
			// A directory becomes a file.
			from := filepath.Dir(pick())
			if from != "." && os.RemoveAll(filepath.Join(dir, from)) == nil {
				put(from, lines(4), true)
			}
		case op < 87:
			// A file becomes a directory.
			f := pick()
			os.Remove(filepath.Join(dir, f))
			put(f+"/in", lines(5), true)
		case op < 89:
			put(newName(), nil, true)
		default:
			put(newName(), lines(rnd.Intn(28)+3), true)
		}
	}
	for i := rnd.Intn(7) + 4; i > 0; i-- {
		put(newName(), lines(rnd.Intn(32)+8), true)
	}
	g.commit("root")
	branches := []string{"main"}
	for i := 0; i < n; i++ {
		b := branches[rnd.Intn(len(branches))]
		g.git("checkout", "-q", b)
		switch r := rnd.Intn(100); {
		case r < 25 && len(branches) < 6:
			name := fmt.Sprintf("b%d", i)
			g.git("checkout", "-q", "-b", name)
			branches = append(branches, name)
			for k := rnd.Intn(3) + 1; k > 0; k-- {
				mutate()
			}
			g.commit(fmt.Sprintf("branch %s %d", name, i))
		case r < 60 && len(branches) > 1:
			other := branches[rnd.Intn(len(branches))]
			if other == b {
				continue
			}
			g.when += 3600
			g.try("merge", "-q", "--no-ff", "--no-commit", other)
			if _, code := g.try("rev-parse", "-q", "--verify", "MERGE_HEAD"); code != 0 {
				g.try("merge", "--abort")
				g.try("reset", "-q", "--hard")
				continue
			}
			switch how := rnd.Intn(100); {
			case how < 30:
				// The merge keeps the lines of the two sides.
				for _, f := range files() {
					if isLink(f) {
						continue
					}
					var kept []string
					marked := false
					for _, l := range read(f) {
						if strings.HasPrefix(l, "<<<<<<<") || strings.HasPrefix(l, "=======") || strings.HasPrefix(l, ">>>>>>>") || strings.HasPrefix(l, "|||||||") {
							marked = true
						} else {
							kept = append(kept, l)
						}
					}
					if marked {
						put(f, kept, true)
					}
				}
			case how < 45:
				mutate()
			}
			g.try("add", "-A")
			if _, code := g.try("commit", "-q", "-m", fmt.Sprintf("merge %s into %s (%d)", other, b, i)); code != 0 {
				g.try("merge", "--abort")
				g.try("reset", "-q", "--hard")
			}
		default:
			for k := rnd.Intn(3) + 1; k > 0; k-- {
				mutate()
			}
			g.commit(fmt.Sprintf("work on %s %d", b, i))
		}
	}
	g.git("checkout", "-q", "main")
}

// TestSelfHostExampleGitRemerge runs each form of `log --remerge-diff`
// with git and with the port: on each kind of merge, with each style of
// conflict and the other settings of a merge, and on random histories.
// It also runs the merges that the port refuses.
func TestSelfHostExampleGitRemerge(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for i, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			g := gitRemergeFixture(t, oracle, dir, run)
			for _, at := range []string{dir, filepath.Join(dir, "sub")} {
				for _, c := range gitRemergeCases {
					gitCompare(t, oracle, port, at, "", strings.Fields(c)...)
				}
			}
			for _, cols := range []string{"COLUMNS=60"} {
				gitCompareEnv(t, oracle, port, dir, []string{cols}, "log", "--remerge-diff", "--all", "--stat", "--graph")
			}
			gitCompareEnv(t, oracle, port, dir, []string{"GIT_MERGE_VERBOSITY=5"}, "log", "--remerge-diff", "--all", "--format=%h")
			short := []string{"log --remerge-diff --all", "log --remerge-diff --all --stat --oneline", "log --diff-merges=r --all --format=%h -- text.txt dir"}
			settings := [][]string{{"merge.conflictStyle", "diff3"}, {"merge.conflictStyle", "zdiff3"}, {"merge.conflictStyle", "merge"},
				{"merge.directoryRenames", "false"}, {"merge.directoryRenames", "true"}, {"merge.directoryRenames", "conflict"},
				{"merge.renameLimit", "1"}, {"merge.renameLimit", "0"}, {"merge.verbosity", "5"}, {"merge.verbosity", "2"},
				{"merge.default", "union"}, {"merge.default", "nosuch"}, {"merge.default", "text"}, {"diff.renames", "copies"}}
			for _, setting := range settings {
				g.git("config", setting[0], setting[1])
				for _, c := range short {
					gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
				}
			}
			// The value of `log.diffMerges` is the diff of `-m`.
			for _, value := range []string{"remerge", "r", "off", "combined", "first-parent"} {
				g.git("config", "log.diffMerges", value)
				for _, c := range []string{"log -m --all --format=%h", "log --diff-merges=on --all --format=%h", "log -m -p --all --format=%h -- text.txt", "log --all --format=%h",
					"log -m --all --oneline -S evil", "log -m -s --all --format=%h", "log -m --stat --all --format=%h"} {
					gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
				}
			}
			for _, setting := range [][]string{{"log.diffMerges", "bogus"}, {"merge.renameLimit", "abc"}, {"merge.renames", "maybe"}, {"merge.renormalize", "maybe"}, {"merge.verbosity", "x"}} {
				g.git("config", setting[0], setting[1])
				gitCompare(t, oracle, port, dir, "", "log", "--remerge-diff", "--all", "--format=%h")
				gitCompare(t, oracle, port, dir, "", "log", "--remerge-diff", "--format=%h", "base")
				g.git("config", "--unset", setting[0])
			}
			// The attributes of a path select the driver of its merge and
			// the size of its conflict markers.
			attributes := []string{"union.txt merge=union\nplain.txt -merge\nwide.txt conflict-marker-size=12\n", "*.txt merge=binary\n",
				"union.txt merge\nplain.txt merge=nosuch\nwide.txt conflict-marker-size=0\n", "wide.txt conflict-marker-size=3\ntext.txt conflict-marker-size=9\nnew.txt merge=union\n"}
			os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755)
			for _, text := range attributes {
				if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				for _, c := range []string{"log --remerge-diff --all --format=%h", "log --remerge-diff --format=%s attributes-1 addadd-1 content-1"} {
					gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
				}
			}
			os.Remove(filepath.Join(dir, ".git", "info", "attributes"))

			for _, seed := range []int64{1, 2, 3} {
				seed += int64(100 * i)
				random := t.TempDir()
				gitRemergeRandom(t, oracle, random, seed, 40, run)
				before := t.Failed()
				for _, c := range []string{"log --remerge-diff --all", "log --remerge-diff --all --stat --summary --raw", "log --diff-merges=remerge --all --oneline --graph",
					"log --remerge-diff --all --name-status --format=%h", "log --remerge-diff --all -U1 --no-renames", "log --remerge-diff --all --format=%h -- a lib"} {
					gitCompare(t, oracle, port, random, "", strings.Fields(c)...)
				}
				for _, setting := range [][]string{{"merge.conflictStyle", "zdiff3"}, {"merge.directoryRenames", "false"}, {"merge.directoryRenames", "true"}} {
					if _, stderr, code := oracle.run(t, random, "", "config", setting[0], setting[1]); code != 0 {
						t.Fatalf("git config %v: %s", setting, stderr)
					}
					gitCompare(t, oracle, port, random, "", "log", "--remerge-diff", "--all")
				}
				if t.Failed() && !before {
					t.Errorf("the random history of the seed %d differs", seed)
				}
			}
			gitRemergeGaps(t, oracle, port, run)
		})
	}
}

// gitRemergeGaps runs the merges that the port does not have: the port
// stops with a message that names the part, and prints no diff of the
// merge.
func gitRemergeGaps(t *testing.T, oracle, port gitTool, run gitRun) {
	t.Helper()
	dir := t.TempDir()
	g := &gitRemergeRepo{t: t, oracle: oracle, dir: dir, when: 1700000000}
	gitInit(t, oracle, dir, run)
	g.write("file.txt", "one\ntwo\nthree\n")
	g.commit("base")
	g.git("tag", "base")
	var links []string
	for _, name := range []string{"s0", "s1", "s2"} {
		g.git("checkout", "-q", "-B", "tmp", "base")
		g.write(name, name+"\n")
		g.commit("the commit of a submodule " + name)
		links = append(links, g.git("rev-parse", "HEAD"))
	}
	// Each side has another commit of one submodule.
	side := func(name, link, parent string) {
		g.git("checkout", "-q", "-B", name, parent)
		g.git("update-index", "--add", "--cacheinfo", "160000,"+link+",module")
		g.when += 3600
		g.git("commit", "-q", "-m", name)
	}
	side("module-base", links[0], "base")
	side("module-1", links[1], "module-base")
	side("module-2", links[2], "module-base")
	tree := g.git("rev-parse", "module-1^{tree}")
	g.git("update-ref", "refs/heads/module-merge", g.git("commit-tree", "-p", "module-1", "-p", "module-2", "-m", "merge of submodules", tree))
	g.scenario("driver", "base", func() { g.write("file.txt", "one\nONE\nthree\n") }, func() { g.write("file.txt", "one\nTWO\nthree\n") }, nil)
	g.git("checkout", "-q", "base")
	refuse := func(want string, args ...string) {
		t.Helper()
		stdout, stderr, code := port.run(t, dir, "", args...)
		if code != 128 || stderr != "fatal: the port does not have "+want+" in a merge (--remerge-diff)\n" || strings.Contains(stdout, "diff --git") {
			t.Errorf("git %q: got exit %d, stdout %q, stderr %q, want the refusal of %s", args, code, stdout, stderr, want)
		}
	}
	refuse("the merge of a submodule (module)", "log", "--remerge-diff", "--format=%s", "-n", "1", "module-merge")
	// With no form of the diff, the port does not do the merge.
	gitCompare(t, oracle, port, dir, "", "log", "--remerge-diff", "-s", "--format=%s", "module-merge")
	g.git("config", "merge.custom.driver", "true")
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("file.txt merge=custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refuse("the merge driver 'custom' of the configuration", "log", "--remerge-diff", "--format=%s", "-n", "1", "driver-1")
	os.Remove(filepath.Join(dir, ".git", "info", "attributes"))
	gitCompare(t, oracle, port, dir, "", "log", "--remerge-diff", "--format=%s", "-n", "1", "driver-1")
	g.git("config", "merge.renormalize", "true")
	refuse("merge.renormalize", "log", "--remerge-diff", "--format=%s", "-n", "1", "driver-1")
}
