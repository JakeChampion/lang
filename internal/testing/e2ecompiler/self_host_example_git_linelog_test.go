package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitLineLogCases are the forms of `log -L` for the repository of
// gitLineLogFixture. Each case is split at spaces, and `\s` in a case is a
// space.
var gitLineLogCases = []string{
	// The forms of a range: numbers, offsets, regexes and function names.
	"log -L 4,12:b.c", "log -L 4,+9:b.c", "log -L 12,-9:b.c", "log -L /int\\sf/,/^}/:b.c", "log -L :f:b.c", "log -L :main:b.c",
	"log -L 1,+4:b.c", "log -L 30:b.c", "log -L ,5:b.c", "log -L 8,:b.c", "log -L 9,3:b.c", "log -L /return/,+1:b.c",
	"log -L ^/return/,+1:b.c -L /return/,+1:b.c", "log -L :int\\sg:b.c -L :main:b.c", "log -L :$:b.c", "log -L :^:b.c", "log -L :.*:b.c",
	"log -L :ma.n:b.c", "log -L /\\}/,+2:b.c", "log -L 4,12:b.c -L :main:b.c", "log -L 4,22:b.c -L ^:main:b.c", "log -L :main:b.c -L 4,22:b.c",
	"log -L 4:b.c -L 8,12:b.c", "log -L 8,12:b.c -L 4:b.c", "log -L :f:b.c -L 1,2:other.txt", "log -L 3,4:n.c", "log -L 1:n.c",
	"log -L 2,4:big.txt -L 300,+3:big.txt", "log -L 1:bin.dat", "log -L :f:a.c HEAD~6", "log -L 1,3:empty",
	// Directory syntax must survive normalization, even on a path naming a blob.
	"log -L 1:b.c/", "log -L 1:b.c/.", "log -L 1:b.c/sub/..", "log -L 1:d/", "log -L 1:d/..",
	// The forms of the output.
	"log -L :f:b.c --oneline", "log -L :f:b.c --graph", "log -L :f:b.c --graph --oneline", "log -L :main:b.c --parents --oneline",
	"log -L :f:b.c -s", "log -L :f:b.c --no-patch --format=%h|%p|%s", "log -L :f:b.c -s --parents --format=%h|%p|%s",
	"log -L :f:b.c --raw", "log -L :f:b.c --raw -p", "log -L :f:b.c --name-only", "log -L :f:b.c --name-status --oneline",
	"log -L :f:b.c --summary", "log -L :f:b.c --summary -p", "log -L :f:b.c --format=", "log -L :f:b.c --format=%s",
	// The rename of a.c to b.c changes main, and the pair loses the status of a rename.
	"log -L :main:b.c --name-status --oneline", "log -L :main:b.c --raw --oneline", "log -L :main:b.c --summary --oneline",
	"log -L :f:b.c --word-diff", "log -L :f:b.c --word-diff=porcelain", "log -L :f:b.c --color-words", "log -L :f:b.c --color",
	"log -L :int\\sg:b.c --color --graph", "log -L :f:b.c --color --color-moved", "log -L :f:b.c -U1", "log -L :f:b.c -U0",
	"log -L :f:b.c --no-prefix", "log -L :f:b.c --src-prefix=old/ --dst-prefix=new/", "log -L :f:b.c --full-index",
	"log -L :f:b.c --abbrev=10", "log -L :f:b.c --histogram", "log -L :f:b.c --decorate", "log -L 5,7:b.c --pretty=raw",
	// The walk: merges, renames, limits and orders.
	"log -L :int\\sg:b.c --oneline", "log -L :main:b.c --oneline --first-parent", "log -L :main:b.c --first-parent", "log -L :int\\sg:b.c --graph --oneline",
	"log -L :main:b.c --reverse --oneline", "log -L :main:b.c -n2 --oneline", "log -L :main:b.c --skip=1 --oneline",
	"log -L :main:b.c --date-order --oneline", "log -L :main:b.c --topo-order --oneline", "log -L :f:b.c --before=1700020000 --format=%h",
	"log -L :f:b.c --since=1700010000 --format=%h", "log -L :f:b.c --author=nobody", "log -L :f:b.c --grep=f --oneline",
	"log -L :f:b.c --no-renames --oneline", "log -L :f:b.c -M --oneline", "log -L :f:b.c -C --oneline", "log -L :f:b.c -S long --oneline",
	"log -L :f:b.c -S long", "log -L :f:b.c -G s\\+\\+", "log -L :f:b.c -S nomatch", "log -L :f:b.c side", "log -L 1:other.txt --oneline",
	"log -L 1,2:b.c side..main --oneline", "log -L 1,2:b.c ^side --oneline", "log -L1,3:b.c --all",
	// The errors of git.
	"log -L", "log -L b.c", "log -L 1:", "log -L 1:nonexistent", "log -L 1:d", "log -L /foo:b.c", "log -L 1000:b.c", "log -L :b.c",
	"log -L :foo:b.c", "log -L 0:b.c", "log -L 1,+0:b.c", "log -L 1,x:b.c", "log -L /[/:b.c", "log -L :[:b.c", "log -L /zz/:b.c",
	"log -L :}:b.c", "log -L 3:../b.c", "log -L 1:b.c --follow", "log -L 1:b.c -- b.c", "log -L 1:b.c --stat", "log -L 1:b.c --numstat",
	"log -L 1:b.c --shortstat", "log -L 1:b.c --check", "log -L 1:b.c --full-diff", "log -L 1:b.c --patch-with-stat", "log -L 1:b.c HEAD main",
	"log -L 1:b.c ^HEAD", "log -L 1,2:b.c main side", "log --grep -L 1:b.c", "log -L 1:b.c -g",
}

// gitLineLogFixture makes a repository for gitLineLogCases: the functions
// of a C file that change, move and go, a merge of changes of two
// functions, a merge where one side holds each change, a rename with a
// change, a change of white space, a file with no line end at its end, a
// large file, a binary file, an empty file and a file in a directory.
func gitLineLogFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
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
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		t.Helper()
		when += 3600
		run("add", "-A")
		run("commit", "-q", "-m", msg)
	}
	f := func(kind, body string) string {
		return kind + " f(" + kind + " x)\n{\n\tint s = 0;\n\twhile (x) {\n\t\tx >>= 1;\n\t\ts++;\n\t}\n" + body + "}\n"
	}
	g := "int g(void)\n{\n\treturn 1;\n}\n"
	h := "int h(void)\n{\n\treturn 2;\n}\n"
	main := func(arg string) string {
		return "int main(void)\n{\n\tprintf(\"%d\\n\", f(" + arg + "));\n\treturn 0;\n}\n"
	}
	code := func(parts ...string) string {
		return "#include <stdio.h>\n\n" + strings.Join(parts, "\n")
	}
	var big strings.Builder
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&big, "line %d\n", i)
	}
	run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	write("a.c", code(f("int", "\treturn s;\n"), g, main("15")))
	write("other.txt", "one\ntwo\n")
	write("n.c", "int top()\n{\n\treturn 1;\n}\n\nint bot()\n{\n\treturn 2;\n}")
	write("big.txt", big.String())
	write("bin.dat", "bin\x00ary\n")
	write("empty", "")
	write("d/sub.c", "int sub(void)\n{\n\treturn 3;\n}\n")
	commit("initial")
	write("a.c", code(f("long", "\treturn s;\n"), g, main("15")))
	commit("f: long")
	write("a.c", code(f("long", "\treturn s;\n"), g, main("16")))
	write("other.txt", "one\nTWO\n")
	commit("main: 16")
	write("a.c", code(f("long", "\treturn s;\n"), g, h, main("16")))
	commit("add h")
	write("other.txt", "one\nTWO\nthree\n")
	write("n.c", "int top()\n{\n\treturn 1;\n}\n\nint bot()\n{\n\treturn 22;\n}")
	commit("other and bot")
	run("checkout", "-q", "-b", "side")
	write("a.c", code(f("long", "\treturn s;\n"), "int g(void)\n{\n\treturn 10;\n}\n", h, main("16")))
	commit("side: g")
	run("checkout", "-q", "main")
	write("a.c", code(f("long", "\ts += 0;\n\treturn s;\n"), g, h, main("16")))
	commit("f: s += 0")
	when += 3600
	run("merge", "-q", "--no-ff", "-m", "merge side", "side")
	run("checkout", "-q", "-b", "other", "main")
	write("other.txt", "zero\none\nTWO\nthree\n")
	commit("other: zero")
	run("checkout", "-q", "main")
	write("a.c", code(f("long", "\ts += 0;\n\treturn s;\n"), "int g(void)\n{\n\treturn 10;\n}\n", h, main("17")))
	commit("main: 17")
	when += 3600
	run("merge", "-q", "--no-ff", "-m", "merge other", "other")
	run("mv", "a.c", "b.c")
	write("b.c", code(f("long", "\ts += 0;\n\treturn s;\n"), "int g(void)\n{\n\treturn 10;\n}\n", h, main("18")))
	commit("rename to b.c")
	write("b.c", code(f("long", "\ts += 0;\n\treturn s;\n"), "int g(void)\n{\n    return 10;\n}\n", h, main("18")))
	write("n.c", "int top()\n{\n\treturn 1;\n}\n\nint bot()\n{\n\treturn 33;\n}\n")
	write("big.txt", strings.Replace(strings.Replace(big.String(), "line 3\n", "line three\n", 1), "line 301\n", "line 301 changed\n", 1))
	write("bin.dat", "bin\x00ary\nmore\n")
	commit("white space of g and the end of n.c")
	write("b.c", code(f("long", "\ts += 0;\n\treturn s;\n"), "int g(void)\n{\n    return 10;\n}\n", main("18")))
	commit("drop h")
}

// TestSelfHostExampleGitLineLog runs `log -L` with git and with the port,
// at the top of the worktree and in a directory below it, and with a
// commit graph that has generation numbers.
func TestSelfHostExampleGitLineLog(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			gitLineLogFixture(t, oracle, dir, run)
			for _, c := range gitLineLogCases {
				args := strings.Fields(c)
				for i := range args {
					args[i] = strings.ReplaceAll(args[i], "\\s", " ")
				}
				gitCompare(t, oracle, port, dir, "", args...)
			}
			sub := filepath.Join(dir, "d")
			for _, c := range []string{"log -L 1,2:sub.c", "log -L :sub:sub.c --oneline", "log -L 1:../b.c --oneline -s", "log -L 1:../../b.c",
				"log -L 1:./sub.c -L 3:sub.c --format=%s"} {
				gitCompare(t, oracle, port, sub, "", strings.Fields(c)...)
			}
			if _, stderr, code := oracle.run(t, dir, "", "commit-graph", "write", "--reachable"); code != 0 {
				t.Fatalf("git commit-graph write: %s", stderr)
			}
			for _, c := range []string{"log -L :f:b.c", "log -L :int\\sg:b.c --oneline", "log -L :main:b.c --graph --oneline", "log -L :f:b.c --before=1700020000 --format=%h",
				"log -L :main:b.c -n2 --oneline"} {
				args := strings.Fields(c)
				for i := range args {
					args[i] = strings.ReplaceAll(args[i], "\\s", " ")
				}
				gitCompare(t, oracle, port, dir, "", args...)
			}
		})
	}
}
