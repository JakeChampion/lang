package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitDriverFiles are the files of gitDriverFixture. Each one has lines of
// functions with five lines after each, so a change of the fifth line
// gets the line of its function in the hunk header.
var gitDriverFiles = map[string]string{
	"src/a.c": "#include <stdio.h>\n\nstatic int helper(int x)\n{\n\tint a = 1;\n\tint b = 2;\n\tint c = 3;\n\tint d = 4;\n\treturn x + d;\n}\n\n" +
		"class Foo {\npublic:\n\tint method(int y)\n\t{\n\t\tint a = 1;\n\t\tint b = 2;\n\t\tint c = 3;\n\tlabel:\n\t\tint d = 4;\n\t\treturn y;\n\t}\n};\n\n" +
		"int main(void)\n{\n\tint q = 0;\n\tint r = 1;\n\tint s = 2;\n\tint d = 4;\n\treturn helper(q);\n}\n",
	"p.py": "import os\n\nclass Widget:\n    \"\"\"Doc.\"\"\"\n\n    x = 1\n    y = 2\n    z = 3\n\n    def area(self):\n        a = 1\n        b = 2\n        c = 3\n        d = 4\n        return a\n\n" +
		"    async def run(self):\n        a = 1\n        b = 2\n        c = 3\n        d = 4\n        return b\n",
	"doc.tex": "\\documentclass{article}\n\\begin{document}\n\\section{Intro}\none\ntwo\nthree\nfour\nd = 4\n\\subsection*{Deep part}\nalpha\nbeta\ngamma\ndelta\nd = 4\n\\end{document}\n",
	"m.f90":   "PROGRAM Main\n  x = 1\n  y = 2\n  z = 3\n  w = 4\n  d = 4\nEND PROGRAM\nSubroutine Foo(a)\n  x = 1\n  y = 2\n  z = 3\n  w = 4\n  d = 4\nEND\n",
	"n.txt":   gitDriverText,
	// The same text with each form of the attribute.
	"plain.txt": gitDriverText, "set.txt": gitDriverText, "nodrv.txt": gitDriverText, "bre.txt": gitDriverText, "neg.txt": gitDriverText,
	"grp.txt": gitDriverText, "ada.adb": gitDriverText,
}

const gitDriverText = "BEGIN block one\n  l1\n  l2\n  l3\n  l4\n  d = 4\nKEEP not this\n  m1\n  m2\n  m3\n  m4\n  d = 4\nbegin lower   \t \n  n1\n  n2\n  n3\n  n4\n  d = 4\n"

const gitDriverAttributes = "*.c diff=cpp\n*.py diff=python\n*.tex diff=tex\n*.f90 diff=fortran\nn.txt diff=mine\nset.txt diff\n" +
	"nodrv.txt diff=nosuch\nbre.txt diff=basic\nneg.txt diff=neg\ngrp.txt diff=grp\n"

// gitDriverFixture makes a repository whose files have drivers of the
// attribute diff: built-in drivers, drivers of the configuration with
// xfuncname and funcname, a driver with a line that refuses, the attribute
// with no value, and the name of no driver. The second commit changes a
// line below each function.
func gitDriverFixture(t *testing.T, oracle gitTool, dir string, run gitRun) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		if _, stderr, code := oracle.run(t, dir, "", args...); code != 0 {
			t.Fatalf("git %v: %s", args, stderr)
		}
	}
	write := func(change func(string) string) {
		t.Helper()
		for name, text := range gitDriverFiles {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(change(text)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	gitInit(t, oracle, dir, run)
	write(func(text string) string { return text })
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(gitDriverAttributes), 0o644); err != nil {
		t.Fatal(err)
	}
	git("config", "diff.mine.xfuncname", "^(BEGIN|begin) (.*)$")
	git("config", "diff.basic.funcname", "^\\(begin\\|KEEP\\) .*")
	git("config", "diff.neg.xfuncname", "!^BEGIN\n^[A-Za-z]+ [a-z]+")
	git("config", "diff.grp.xfuncname", "^[A-Z]+ (not )?(this|block)")
	id := []string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}
	git("add", "-A")
	git(append(id, "commit", "-q", "-m", "base")...)
	write(func(text string) string { return strings.ReplaceAll(text, "d = 4", "d = 5") })
	git("add", "-A")
	git(append(id, "commit", "-q", "-m", "change")...)
}

var gitDriverCases = []string{
	"log -p", "log -p -U1", "log -p -U0", "log -p -U1 --color", "log -p -U1 --stat", "log -p -U1 --graph", "log -p -U1 -- src",
	"log -L :helper:src/a.c", "log -L :method:src/a.c", "log -L :main:src/a.c", "log -L :label:src/a.c", "log -L :area:p.py", "log -L :run:p.py",
	"log -L :Widget:p.py", "log -L :Deep:doc.tex", "log -L :Intro:doc.tex", "log -L :block:n.txt", "log -L :lower:n.txt", "log -L :KEEP:n.txt",
	"log -L :KEEP:bre.txt", "log -L :KEEP:neg.txt", "log -L :BEGIN:neg.txt", "log -L :Foo:m.f90", "log -L :main:m.f90", "log -L :KEEP:set.txt",
	"log -L :KEEP:nodrv.txt", "log -L :KEEP:grp.txt", "log -L :begin:grp.txt", "log -L :BEGIN:ada.adb", "log -L /m3/,+2:n.txt", "log -L 9,11:neg.txt -U1",
}

// gitDriverSettings change the drivers of the configuration: a regex that
// is not sound, a last line that refuses, an empty regex, funcname after
// xfuncname, a regex for a built-in driver and for the driver `default`,
// and a driver with no regex.
var gitDriverSettings = [][]string{
	{"diff.mine.xfuncname", "^(BEGIN"}, {"diff.mine.xfuncname", "^x\n!^BEGIN"}, {"diff.mine.xfuncname", ""}, {"diff.mine.funcname", "^KEEP \\(not\\)"},
	{"diff.cpp.xfuncname", "^(int main)"}, {"diff.fortran.xfuncname", "^(program.*)"}, {"diff.default.xfuncname", "^(KEEP) "},
	{"diff.nosuch.textconv", "cat"}, {"diff.python.funcname", "^class"},
}

func TestSelfHostExampleGitDrivers(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			dir := t.TempDir()
			gitDriverFixture(t, oracle, dir, run)
			for _, c := range gitDriverCases {
				gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
			}
			for _, setting := range gitDriverSettings {
				if _, stderr, code := oracle.run(t, dir, "", "config", setting[0], setting[1]); code != 0 {
					t.Fatalf("git config %v: %s", setting, stderr)
				}
				for _, c := range []string{"log -p -U1", "log -L :KEEP:n.txt", "log -L :main:src/a.c", "log -L 1,2:n.txt", "log --stat"} {
					gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
				}
			}
			gitCompareBadConfig(t, oracle, port, dir, nil, "log --oneline", "log -p", "cat-file -t HEAD", "rev-parse HEAD", "ls-tree HEAD",
				"[diff \"mine\"]\n\txfuncname\n", "[diff \"q\"]\n\ttextconv\n", "[diff \"q\"]\n\twordRegex\n", "[diff \"q\"]\n\tbinary\n\tcachetextconv\n",
				"[diff]\n\tfuncname\n", "\n\n[diff \"q\"]\n\tfuncname = a\\\nb\n\tcommand")

			signedDir := t.TempDir()
			keys := gitSignedFixture(t, oracle, signedDir, run)
			gitCompareBadConfig(t, oracle, port, signedDir, []string{"GNUPGHOME=" + keys, "TZ=UTC"}, "log --show-signature", "log --format=%h|%G?",
				"log --oneline", "cat-file -t HEAD", "rev-parse HEAD",
				"[gpg]\n\tformat = bogus\n", "[gpg]\n\tformat = ssh\n\tminTrustLevel = wat\\\n  more\n", "[gpg]\n\tformat\n", "[gpg]\n\tminTrustLevel\n",
				"[gpg]\n\tprogram\n", "[gpg \"ssh\"]\n\tallowedSignersFile\n", "[user]\n\tsigningKey\n", "[gpg]\n\tformat = OpenPGP",
				"[gpg]\n\tminTrustLevel = Fully\n\tformat = x509\n")
		})
	}
}

// gitCompareBadConfig puts each text of tail at the end of the file config
// of the repository dir, and runs the five commands with git and with
// the port, at the top and in a directory below the top. The first
// command also runs with GIT_DIR, which changes the name of the file in
// the message. Then it puts the file back.
func gitCompareBadConfig(t *testing.T, oracle, port gitTool, dir string, env []string, c1, c2, c3, c4, c5 string, tail ...string) {
	t.Helper()
	file := filepath.Join(dir, ".git", "config")
	kept := fileBytes(t, file)
	below := filepath.Join(dir, "below")
	if err := os.MkdirAll(below, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, text := range tail {
		rewriteFile(t, file, append(append([]byte{}, kept...), text...))
		for _, c := range []string{c1, c2, c3, c4, c5} {
			gitCompareEnv(t, oracle, port, dir, env, strings.Fields(c)...)
			gitCompareEnv(t, oracle, port, below, env, strings.Fields(c)...)
		}
		gitCompareEnv(t, oracle, port, dir, append([]string{"GIT_DIR=./.git//"}, env...), strings.Fields(c1)...)
	}
	rewriteFile(t, file, kept)
}
