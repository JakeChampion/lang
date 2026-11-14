package coreutils

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func init() {
	registerCorpus("diff3", diff3Cases)
}

// diffutilsPath puts the GNU diff of the oracle first in PATH. GNU diff3 and
// sdiff run the diff program that PATH gives, and on macOS /usr/bin/diff is
// not GNU diff.
func diffutilsPath(t *testing.T) string {
	t.Helper()
	ref := packageReferenceBin(t, packageOf(t, "diff"), "diff")
	return "PATH=" + filepath.Dir(ref) + ":/usr/bin:/bin"
}

func diff3Cases(t *testing.T) []invocation {
	dir := t.TempDir()
	w := func(name, content string) {
		t.Helper()
		writeFile(t, dir, name, content)
	}
	w("m3", "1\n2\n3\n4\n5\n")
	w("o3", "1\nX\n3\n4\n5\n")
	w("y3", "1\nY\n3\n4\nZ\n")
	w("m", "a\nb\nc\n")
	w("o", "a\nB\nc\n")
	w("y", "a\nb\nC\n")
	w("mn", "a\nb\nc")
	w("yn", "a\nb\nx")
	w("mA", "1\n2\n3\n4\n5")
	w("mB", "1\n2\n3\n4\nQ")
	w("oC", "1\nX\n3\n4\n5")
	w("mm", "a\nb")
	w("oo", "a\nb\n")
	w("yy", "a\nb\nc\n")
	w("same", "1\nY\n3\n4\n5\n")
	w("dot", "x\n.\ny\n")
	w("dots", ".\n.\nz\n.\n")
	w("crlf", "a\r\nb\r\nc\r\n")
	w("bin1", "\x00a\n")
	w("bin2", "\x00b\n")
	w("empty", "")
	w("tabs", "1\n\t2\n3\n")
	var big [3]strings.Builder
	for i := 1; i <= 3000; i++ {
		line := "line " + strconv.Itoa(i) + "\n"
		big[0].WriteString(line)
		switch {
		case i%101 == 0:
			big[1].WriteString("old " + strconv.Itoa(i) + "\n")
			big[2].WriteString(line)
		case i%103 == 0:
			big[1].WriteString(line)
			big[2].WriteString("yours " + strconv.Itoa(i) + "\n")
		case i%307 == 0:
			big[1].WriteString("both " + strconv.Itoa(i) + "\n")
			big[2].WriteString("both " + strconv.Itoa(i) + "\n")
		case i%311 == 0:
			big[1].WriteString("one " + strconv.Itoa(i) + "\n")
			big[2].WriteString("two " + strconv.Itoa(i) + "\n")
		case i%401 == 0:
		default:
			big[1].WriteString(line)
			big[2].WriteString(line)
		}
	}
	w("big0", big[0].String())
	w("big1", big[1].String())
	w("big2", big[2].String())
	diffMkdir(t, dir, "d")
	path := diffutilsPath(t)

	c := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, env: []string{path}}
	}
	in := func(name, stdin string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, stdin: stdin, env: []string{path}}
	}
	var cases []invocation
	for _, set := range [][]string{
		{"m3", "o3", "y3"}, {"m", "o", "y"}, {"mn", "o", "yn"}, {"mA", "o3", "y3"}, {"mB", "o3", "y3"},
		{"m3", "oC", "y3"}, {"mm", "oo", "yy"}, {"y3", "same", "m3"}, {"m3", "m3", "m3"}, {"big0", "big1", "big2"},
		{"big2", "big0", "big1"}, {"empty", "m", "y"}, {"m", "empty", "y"}, {"tabs", "m3", "o3"},
	} {
		for _, opts := range [][]string{
			{}, {"-e"}, {"-E"}, {"-A"}, {"-m"}, {"-x"}, {"-X"}, {"-3"}, {"-T"}, {"-i", "-e"}, {"-m", "-3"},
			{"-m", "-x"}, {"-m", "-E"}, {"-m", "-e"},
		} {
			name := strings.Join(append(append([]string{}, opts...), set...), " ")
			cases = append(cases, c(name, append(append([]string{}, opts...), set...)...))
		}
	}
	return append(cases, []invocation{
		c("show all long option", "--show-all", "m3", "o3", "y3"),
		c("ed long option", "--ed", "m3", "o3", "y3"),
		c("show overlap long option", "--show-overlap", "m3", "o3", "y3"),
		c("easy only long option", "--easy-only", "m3", "o3", "y3"),
		c("overlap only long option", "--overlap-only", "m3", "o3", "y3"),
		c("merge long option", "--merge", "m3", "o3", "y3"),
		c("initial tab long option", "--initial-tab", "m3", "o3", "y3"),
		c("ed with a final write and brackets", "-i", "-E", "m3", "o3", "y3"),
		c("labels", "-A", "-L", "L1", "-L", "L2", "-L", "L3", "m3", "o3", "y3"),
		c("one label", "-E", "--label=X", "m3", "o3", "y3"),
		c("label for a merge", "-m", "-L", "mine", "m3", "o3", "y3"),
		c("too many labels", "-L", "x", "-L", "y", "-L", "z", "-L", "w", "-E", "m3", "o3", "y3"),
		c("label without brackets", "-L", "x", "m3", "o3", "y3"),
		c("label with -X", "-X", "-L", "x", "m3", "o3", "y3"),
		c("ed and merge", "-e", "-m", "m3", "o3", "y3"),
		c("final write and merge", "-i", "-m", "m3", "o3", "y3"),
		c("two output options", "-e", "-E", "m3", "o3", "y3"),
		c("show all and easy only", "-A", "-3", "m3", "o3", "y3"),
		c("overlap only twice", "-x", "-X", "m3", "o3", "y3"),
		c("final write alone", "-i", "m3", "o3", "y3"),
		c("dot lines in an ed script", "-e", "m3", "o3", "dot"),
		c("dot lines with brackets", "-E", "dot", "o3", "dots"),
		c("dot lines in all files", "-A", "dots", "dot", "m3"),
		c("dot lines, older file", "-A", "m3", "dot", "y3"),
		c("binary files", "bin1", "bin2", "m"),
		c("binary files as text", "-a", "bin1", "bin2", "m"),
		c("text long option", "--text", "-m", "bin1", "bin2", "m"),
		c("carriage returns", "-m", "crlf", "m", "y"),
		c("strip trailing cr", "--strip-trailing-cr", "crlf", "m", "y"),
		c("strip trailing cr, merge", "--strip-trailing-cr", "-m", "crlf", "m", "y"),
		c("strip trailing cr, merge into the other", "--strip-trailing-cr", "-m", "m", "crlf", "y"),
		c("missing first file", "nosuch", "m", "y"),
		c("missing second file", "m", "nosuch", "y"),
		c("missing third file", "m", "y", "nosuch"),
		c("a directory", "d", "m", "y"),
		in("stdin twice", "a\n", "-", "-", "m"),
		in("stdin as the common file and another", "a\n", "m", "-", "-"),
		in("stdin as the third file", "x\nb\n", "m", "o", "-"),
		in("stdin as the common file of an ed script", "x\nb\n", "-e", "m", "-", "y"),
		in("stdin as the first file of a merge", "x\nb\n", "-m", "-", "o", "m"),
		in("stdin as the first file", "x\nb\n", "-", "o", "m"),
		c("two operands", "m", "o"),
		c("four operands", "m", "o", "y", "m3"),
		c("one operand", "m"),
		c("no operands"),
		c("operands after a double dash", "--", "m3", "o3", "y3"),
		c("a double dash between operands", "m3", "--", "o3", "y3"),
		c("ambiguous long option", "--s", "m3", "o3", "y3"),
		c("ambiguous e", "--e", "m3", "o3", "y3"),
		c("unknown option", "-q", "m3", "o3", "y3"),
		c("unknown long option", "--bogus", "m3", "o3", "y3"),
		c("missing label", "-L"),
		c("missing diff program", "--diff-program"),
		c("diff program not found", "--diff-program=nosuchprog", "m3", "o3", "y3"),
		c("diff program that fails", "--diff-program=/bin/false", "m3", "o3", "y3"),
		c("diff program that succeeds", "--diff-program=/bin/true", "m3", "o3", "y3"),
		c("diff program diff", "--diff-program=diff", "m3", "o3", "y3"),
		c("diff program diff, merge", "--diff-program=diff", "-m", "m3", "o3", "y3"),
		c("diff program echo", "--diff-program=echo", "m3", "o3", "y3"),
		c("diff program cat", "--diff-program=cat", "m3", "o3", "y3"),
		c("diff program not executable", "--diff-program=/etc/passwd", "m3", "o3", "y3"),
		{name: "full stdout", args: []string{"m3", "o3", "y3"}, dir: dir, env: []string{path}, stdout: stdoutFull},
	}...)
}

func TestDiff3Parity(t *testing.T) {
	requireParity(t, "diff3", diff3Cases(t))
}

func TestDiff3HelpVersion(t *testing.T) {
	requireHelp(t, "diff3", []string{"--help"}, 0)
	requireHelp(t, "diff3", []string{"--he"}, 0)
	requireVersion(t, "diff3", []string{"--version"}, 0)
	requireVersion(t, "diff3", []string{"-v"}, 0)
}
