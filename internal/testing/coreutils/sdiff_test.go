package coreutils

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func init() {
	registerCorpus("sdiff", sdiffCases)
}

func sdiffCases(t *testing.T) []invocation {
	dir := t.TempDir()
	tmp := t.TempDir()
	w := func(name, content string) {
		t.Helper()
		writeFile(t, dir, name, content)
	}
	w("a", "a\nb\nc\nd\ne\nq\n")
	w("b", "a\nB\nc\ne\nf\n")
	w("tabs1", "x\tab\n\n\tq\nz\n")
	w("tabs2", "x\tAb\n\n\tQ\nz\nw\n")
	w("nonl", "a\nb\nc")
	w("nonl2", "a\nb\nd")
	w("crlf", "a\r\nB\r\nc\r\n")
	w("ws1", "a  b\n\nC\n")
	w("ws2", "a b\nc\n")
	w("bin1", "\x00a\n")
	w("bin2", "\x00b\n")
	diffMkdir(t, dir, "dA", "dB")
	w("dA/x", "x\n")
	w("dB/x", "y\n")
	path := diffutilsPath(t)
	env := func(extra ...string) []string {
		return append([]string{path, "TMPDIR=" + tmp, "EDITOR=cat"}, extra...)
	}

	c := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, env: env()}
	}
	in := func(name, stdin string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, stdin: stdin, env: env()}
	}
	n := 0
	// merge runs the interactive merge into a file of its own, which the
	// harness compares.
	merge := func(name, stdin string, extra []string, args ...string) invocation {
		n++
		out := "out" + strconv.Itoa(n)
		return invocation{
			name: name, args: append([]string{"-o", out}, args...), dir: dir, stdin: stdin, env: env(extra...),
			artifacts: []string{out},
			prepare: func(t *testing.T) {
				if err := os.Remove(filepath.Join(dir, out)); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			},
		}
	}
	return []invocation{
		c("side by side", "a", "b"),
		c("width", "-w", "30", "a", "b"),
		c("width long option", "--width=20", "tabs1", "tabs2"),
		c("left column and suppress common lines", "-s", "-l", "-w", "30", "a", "b"),
		c("long options", "--width=20", "--left-column", "--suppress-common-lines", "--expand-tabs", "a", "b"),
		c("expand tabs and tab size", "-t", "--tabsize=3", "-w", "40", "tabs1", "tabs2"),
		c("ignore all space", "-W", "ws1", "ws2"),
		c("ignore options", "--ignore-all-space", "--ignore-blank-lines", "--ignore-case", "--ignore-matching-lines=x", "-w", "30", "ws1", "ws2"),
		c("more ignore options", "--ignore-space-change", "--ignore-tab-expansion", "--ignore-trailing-space", "--minimal", "--speed-large-files", "--text", "--strip-trailing-cr", "crlf", "a"),
		c("short ignore options", "-i", "-b", "-B", "-E", "-Z", "-d", "-H", "-a", "-I", "a", "ws1", "ws2"),
		c("no newline", "-w", "30", "nonl", "nonl2"),
		c("equal files", "a", "a"),
		c("binary files", "bin1", "bin2"),
		c("directories", "dA", "dB"),
		c("a file and a directory", "a", "dA"),
		c("missing file", "a", "nosuch"),
		in("stdin", "a\nX\n", "-", "b"),
		c("invalid width", "-w", "x", "a", "b"),
		c("invalid expression", "-I", "[", "a", "b"),
		c("invalid tab size", "--tabsize=0", "a", "b"),
		c("diff program not found", "--diff-program=nosuchprog", "a", "b"),
		c("diff program diff", "--diff-program=diff", "a", "b"),
		c("diff program echo", "--diff-program=echo", "a", "b"),
		c("diff program not executable", "--diff-program=/etc/passwd", "a", "b"),
		c("one operand", "a"),
		c("three operands", "a", "b", "c"),
		c("no operands"),
		c("unknown option", "-x", "a", "b"),
		c("width takes the next operand", "--w", "a", "b"),
		c("ambiguous s", "--s", "a", "b"),
		c("ambiguous ignore", "--ignore", "a", "b"),
		c("ambiguous i", "--i", "a", "b"),
		c("ambiguous t", "--t", "a", "b"),
		c("operands after a double dash", "--", "a", "b"),
		c("missing output", "-o"),

		merge("merge left then right", "l\nr\n", nil, "a", "b"),
		merge("merge at the end of input", "", nil, "a", "b"),
		merge("merge equal files", "", nil, "a", "a"),
		merge("merge binary files", "", nil, "bin1", "bin2"),
		merge("merge directories", "", nil, "dA", "dB"),
		merge("merge a file and a directory", "", nil, "a", "dA"),
		merge("merge a directory and a file", "", nil, "dA", "a"),
		merge("merge a directory and the file in another", "l\n", nil, "dA", "dB/x"),
		merge("merge a directory with a slash", "r\n", nil, "dA/", "dB/x"),
		merge("merge a missing file", "", nil, "a", "nosuch"),
		merge("merge a missing first file", "", nil, "nosuch", "a"),
		merge("merge stdin", "l\n", nil, "-", "b"),
		merge("merge into stdin", "l\n", nil, "a", "-"),
		merge("merge with an invalid width", "", nil, "-w", "x", "a", "b"),
		merge("merge with an invalid expression", "", nil, "-I", "[", "a", "b"),
		merge("merge with a missing diff program", "", nil, "--diff-program=nosuchprog", "a", "b"),
		merge("merge with a diff program that fails", "", nil, "--diff-program=false", "a", "b"),
		merge("merge with a diff program that succeeds", "", nil, "--diff-program=true", "a", "b"),
		merge("merge with a diff program that echoes", "", nil, "--diff-program=echo", "a", "b"),
		merge("merge with diff as the diff program", "l\nr\n", nil, "--diff-program=diff", "a", "b"),
		merge("merge with cat as the diff program", "", nil, "--diff-program=cat", "a", "b"),
		merge("merge suppressing common lines", "1\n2\n", nil, "-s", "a", "b"),
		merge("merge with the editor and headers", "ed\neb\n", nil, "a", "b"),
		merge("merge with the editor, one side", "e1\ne2\n", nil, "a", "b"),
		merge("merge with the editor, letters", "el\ner\n", nil, "a", "b"),
		merge("merge with the editor, nothing", "e\ned\n", nil, "a", "b"),
		merge("merge with an editor that fails", "ed\n", []string{"EDITOR=false"}, "a", "b"),
		merge("merge with an editor that is missing", "ed\n", []string{"EDITOR=nosuchcmd"}, "a", "b"),
		merge("merge with an editor command line", "ed\n", []string{"EDITOR=sed -i s/^/Z/"}, "a", "b"),
		merge("merge with an editor that is not executable", "ed\n", []string{"EDITOR=/etc/passwd"}, "a", "b"),
		merge("merge with no temporary directory", "ed\n", []string{"TMPDIR=/nonexist"}, "a", "b"),
		merge("merge with an editor that keeps the file", "ed\neb\n", []string{"EDITOR=true"}, "a", "b"),
		merge("merge with help", "x\n1\n2\nq\n", nil, "a", "b"),
		merge("merge silent then verbose", "s\nl\nv\nl\nr\n", nil, "a", "b"),
		merge("merge with blanks and bad commands", " l \n\tr\nl x\nel x\nex\n\n1\n2\n", nil, "a", "b"),
		merge("merge and quit", "q\n", nil, "a", "b"),
		merge("merge with no newline after the command", "l", nil, "a", "b"),
		merge("merge with a width and left column", "r\nl\n", nil, "-w", "30", "-l", "a", "b"),
		merge("merge no newline", "l\nr\n", nil, "nonl", "nonl2"),
		merge("merge long option", "2\n", nil, "--ignore-case", "nonl", "nonl2"),
		{name: "full stdout", args: []string{"a", "b"}, dir: dir, env: env(), stdout: stdoutFull},
	}
}

func TestSdiffParity(t *testing.T) {
	requireParity(t, "sdiff", sdiffCases(t))
}

func TestSdiffHelpVersion(t *testing.T) {
	requireHelp(t, "sdiff", []string{"--help"}, 0)
	requireHelp(t, "sdiff", []string{"--he"}, 0)
	requireVersion(t, "sdiff", []string{"--version"}, 0)
	requireVersion(t, "sdiff", []string{"-v"}, 0)
}
