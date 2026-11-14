package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("diff", diffCases)
}

// diffBig gives a file of n numbered lines and a copy with an edit at a few
// hundred scattered places: changed, deleted and inserted lines.
func diffBig(n int) (string, string) {
	var a, b strings.Builder
	for i := 1; i <= n; i++ {
		line := fmt.Sprintf("line %d of the big file\n", i)
		a.WriteString(line)
		switch {
		case i%997 == 0:
			fmt.Fprintf(&b, "line %d was changed\n", i)
		case i%1499 == 0:
		case i%2003 == 0:
			b.WriteString(line)
			fmt.Fprintf(&b, "inserted after %d\n", i)
		default:
			b.WriteString(line)
		}
	}
	return a.String(), b.String()
}

func diffSeq(n int, edit map[int]string) string {
	var s strings.Builder
	for i := 1; i <= n; i++ {
		if e, ok := edit[i]; ok {
			s.WriteString(e)
			continue
		}
		fmt.Fprintf(&s, "%d\n", i)
	}
	return s.String()
}

func diffMkdir(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func diffCases(t *testing.T) []invocation {
	dir := t.TempDir()
	w := func(name, content string) {
		t.Helper()
		writeFile(t, dir, name, content)
	}
	w("a", "a\nb\nc\nd\ne\n")
	w("b", "a\nB\nc\nd\ne\nf\n")
	w("a2", "a\nb\nc\nd\ne\n")
	w("empty", "")
	w("nonl1", "a\nb\nc")
	w("nonl2", "a\nb\nd")
	w("nonl3", "a\nb\nc\n")
	w("nonl4", "a\nb\nc")
	w("crlf1", "a\r\nb\r\nc\r\n")
	w("crlf2", "a\nb\r\nc  \r\n")
	w("ws1", "int  main ( )\n\tx = 1;\n  y\nz\n")
	w("ws2", "int main()\n x = 1;\ny\nz \n")
	w("ws3", "int main ( )\n\tx  =  1;\n  y  \nz\n")
	w("case1", "Hello\nWORLD\nmixed Case\n")
	w("case2", "hello\nworld\nMIXED case\n")
	w("blank1", "a\n\nb\n\n\nc\nd\n")
	w("blank2", "a\nb\n\nc\n  \nd\n")
	w("blank3", "a\n\n\nb\nc\nd\n")
	w("s30", diffSeq(30, nil))
	w("s30near", diffSeq(30, map[int]string{5: "five\n", 12: "twelve\n"}))
	w("s30far", diffSeq(30, map[int]string{5: "five\n", 13: "thirteen\n"}))
	w("s30ends", diffSeq(30, map[int]string{1: "", 30: "thirty\nextra\n"}))
	w("m1", "a\nb\nc\na\nb\nb\na\n")
	w("m2", "c\nb\na\nb\na\nc\n")
	w("m3", "x\ny\nx\ny\nx\n")
	w("m4", "y\nx\ny\nx\ny\n")
	w("m5", "{\nfoo\n}\n{\nbar\n}\n")
	w("m6", "{\nfoo\n}\n{\nbaz\n}\n{\nbar\n}\n")
	w("m7", "a\na\na\nb\na\na\n")
	w("m8", "a\nb\na\na\na\na\na\n")
	big1, big2 := diffBig(50000)
	w("big1", big1)
	w("big2", big2)
	w("bin1", "\x00abc\n")
	w("bin2", "\x00abd\n")
	w("bin3", "\x00abc\n")
	w("binlong", "\x00abcdef\n")
	late := strings.Repeat("x", 5000) + "\n"
	w("late1", late+"\x00one\n")
	w("late2", late+"\x00two\n")
	w("s p", "space one\n")
	w("t\tq", "tab two\n")
	w("gone", "")
	if err := os.Chmod(filepath.Join(dir, "gone"), 0); err != nil {
		t.Fatal(err)
	}

	diffMkdir(t, dir, "d1/sub", "d2/sub", "d1/only1", "d1/onlydir", "d1/kind", "d2/deep/er", "dd", "e1", "e2")
	w("d1/f", "x\n")
	w("d2/f", "y\n")
	w("d1/o", "z\n")
	w("d2/p", "z\n")
	w("d1/same", "s\n")
	w("d2/same", "s\n")
	w("d1/bin", "\x00a\n")
	w("d2/bin", "\x00b\n")
	w("d1/sub/s", "q\n")
	w("d2/sub/s", "r\n")
	w("d1/onlydir/x", "x\n")
	w("d1/kind/k", "k\n")
	w("d2/kind", "k\n")
	w("d2/deep/er/z", "z\n")
	w("d1/s p", "one\n")
	w("d2/s p", "two\n")
	w("dd/a", "a\nb\nc\nd\ne\n")
	w("dd/b", "other\n")
	w("e1/x", "same\n")
	w("e2/x", "same\n")

	c := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir}
	}
	in := func(name, stdin string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, stdin: stdin}
	}
	cases := []invocation{
		c("normal change and append", "a", "b"),
		c("normal reversed", "b", "a"),
		c("identical files", "a", "a2"),
		c("a file with itself", "a", "a"),
		c("report identical", "-s", "a", "a2"),
		c("report identical when different", "-s", "a", "b"),
		c("report identical for a file with itself", "--report-identical-files", "a", "a"),
		c("brief", "-q", "a", "b"),
		c("brief and equal", "-q", "a", "a2"),
		c("brief long option", "--brief", "a", "b"),
		c("brief with report identical", "-qs", "a", "a2"),
		c("brief with different sizes", "-q", "a", "b"),
		c("brief under ignore case", "-qi", "case1", "case2"),
		c("brief under ignore all space", "-qw", "ws1", "ws3"),

		c("unified", "-u", "a", "b"),
		c("unified zero context", "-U", "0", "a", "b"),
		c("unified one line", "-U1", "a", "b"),
		c("unified long option with a value", "--unified=5", "a", "b"),
		c("unified long option alone", "--unified", "a", "b"),
		c("unified hunks that join", "-u", "s30", "s30near"),
		c("unified hunks that stay apart", "-u", "s30", "s30far"),
		c("unified at both ends", "-u", "s30", "s30ends"),
		c("unified from an empty file", "-u", "empty", "a"),
		c("unified to an empty file", "-u", "a", "empty"),
		c("unified huge context", "-U", "99999999999999999999", "s30", "s30far"),
		c("unified larger context wins", "-U1", "-U5", "s30", "s30far"),
		c("unified flag after a small context", "-U1", "-u", "s30", "s30far"),
		c("unified with an obsolete digit context", "-u", "-1", "s30", "s30far"),
		c("unified with two digits", "-u12", "s30", "s30far"),
		c("unified explicit context beats a digit", "-U5", "-1", "s30", "s30far"),
		c("unified empty context value", "-U", "", "a", "b"),
		c("unified context with a sign and blanks", "-U", " +2", "a", "b"),
		c("unified with labels", "-u", "--label", "old", "--label=new", "a", "b"),
		c("unified with one label", "-u", "-L", "only one", "a", "b"),
		c("unified names that need quotes", "-u", "s p", "t\tq"),
		c("unified horizon lines", "-u", "--horizon-lines=4", "s30", "s30ends"),

		c("context", "-c", "a", "b"),
		c("context one line", "-C", "1", "s30", "s30near"),
		c("context long option with a value", "--context=0", "a", "b"),
		c("context long option alone", "--context", "s30", "s30far"),
		c("context hunks that join", "-c", "s30", "s30near"),
		c("context at both ends", "-c", "s30", "s30ends"),
		c("context from an empty file", "-c", "empty", "a"),
		c("context to an empty file", "-c", "a", "empty"),
		c("context only deletions", "-c", "b", "a2"),
		c("context with labels", "-c", "--label", "x y", "--label", "z", "a", "b"),
		c("context names that need quotes", "-c", "s p", "t\tq"),

		c("normal from an empty file", "empty", "a"),
		c("normal to an empty file", "a", "empty"),
		c("two empty files", "empty", "empty"),
		c("no newline at the end of both", "nonl1", "nonl2"),
		c("no newline at the end, unified", "-u", "nonl1", "nonl2"),
		c("no newline at the end, context", "-c", "nonl1", "nonl2"),
		c("only the last newline differs", "nonl1", "nonl3"),
		c("only the last newline differs, unified", "-u", "nonl3", "nonl1"),
		c("only the last newline differs, context", "-c", "nonl1", "nonl3"),
		c("no newline at the end, equal", "nonl1", "nonl4"),
		c("missing newline under space change", "-b", "nonl1", "nonl3"),

		c("carriage returns", "crlf1", "crlf2"),
		c("carriage returns, unified", "-u", "crlf1", "crlf2"),
		c("carriage returns under space change", "-b", "crlf1", "crlf2"),
		c("carriage returns under all space", "-w", "crlf1", "crlf2"),
		c("white space normal", "ws1", "ws2"),
		c("ignore space change", "-b", "ws1", "ws2"),
		c("ignore space change, equal", "-b", "ws1", "ws3"),
		c("ignore all space", "-w", "ws1", "ws2"),
		c("ignore all space, unified", "-wu", "ws1", "ws2"),
		c("ignore all space long option", "--ignore-all-space", "ws2", "ws3"),
		c("ignore space change long option", "--ignore-space-change", "ws2", "ws3"),
		c("ignore case", "-i", "case1", "case2"),
		c("ignore case, unified", "-iu", "case1", "case2"),
		c("ignore case and space", "-ib", "ws1", "case2"),
		c("ignore blank lines", "-B", "blank1", "blank2"),
		c("ignore blank lines, all ignored", "-B", "blank1", "blank3"),
		c("ignore blank lines, unified", "-Bu", "blank1", "blank2"),
		c("ignore blank lines, context", "-B", "-c", "blank1", "blank2"),
		c("ignore blank lines and all space", "-Bw", "blank1", "blank2"),
		c("ignore blank lines, unified, all ignored", "-B", "-u", "blank3", "blank1"),

		c("a minimal diff with choices 1", "m1", "m2"),
		c("a minimal diff with choices 2", "-u", "m2", "m1"),
		c("a minimal diff with choices 3", "m3", "m4"),
		c("a minimal diff with choices 4", "-u", "m5", "m6"),
		c("a minimal diff with choices 5", "m7", "m8"),
		c("a minimal diff with choices 6", "-c", "m8", "m7"),
		c("minimal", "-d", "m1", "m2"),
		c("minimal long option", "--minimal", "-u", "m5", "m6"),

		c("big files", "big1", "big2"),
		c("big files unified", "-u", "big1", "big2"),
		c("big files context", "-C", "2", "big1", "big2"),
		c("big files brief", "-q", "big1", "big2"),
		c("big files minimal", "-d", "big2", "big1"),
		c("big files fast heuristic", "-H", "big1", "big2"),
		c("big files equal", "big1", "big1"),

		c("binary files", "bin1", "bin2"),
		c("binary files equal", "bin1", "bin3"),
		c("binary files equal, reported", "-s", "bin1", "bin3"),
		c("binary files brief", "-q", "bin1", "bin2"),
		c("binary files of different sizes", "bin1", "binlong"),
		c("binary files as text", "-a", "bin1", "bin2"),
		c("binary files as text, unified", "--text", "-u", "bin1", "bin2"),
		c("binary and text", "bin1", "a"),
		c("a NUL after the first block is text", "late1", "late2"),

		c("directories", "d1", "d2"),
		c("directories recursive", "-r", "d1", "d2"),
		c("directories recursive unified", "-ru", "d1", "d2"),
		c("directories recursive with switches", "-r", "-U", "1", "--horizon-lines=1", "d1", "d2"),
		c("directories with a double dash", "-r", "--", "d1", "d2"),
		c("directories new file", "-N", "d1", "d2"),
		c("directories recursive new file", "-rN", "d1", "d2"),
		c("directories recursive new file reversed", "-rN", "d2", "d1"),
		c("directories unidirectional new file", "-r", "--unidirectional-new-file", "d1", "d2"),
		c("directories unidirectional reversed", "-rP", "d2", "d1"),
		c("directories brief", "-rq", "d1", "d2"),
		c("directories report identical", "-rs", "d1", "d2"),
		c("directories with labels", "-r", "--label", "L1", "--label", "L2", "-u", "d1", "d2"),
		c("equal directories", "-r", "e1", "e2"),
		c("equal directories reported", "-rs", "e1", "e2"),
		c("a directory with itself", "-r", "d1", "d1"),
		c("trailing slashes on directories", "-r", "d1/", "d2//"),
		c("a file and a directory", "a", "dd"),
		c("a directory and a file", "dd", "b"),
		c("a file and a directory without the file", "s30", "dd"),
		c("a file and a directory, reported", "-s", "a", "dd"),

		c("missing first file", "nosuch", "a"),
		c("missing second file", "a", "nosuch"),
		c("missing both files", "nosuch", "nosuch2"),
		c("missing first file as new", "-N", "nosuch", "a"),
		c("missing second file as new", "--new-file", "a", "nosuch"),
		c("missing first file as new, unified", "-u", "-N", "nosuch", "a"),
		c("missing both files as new", "-N", "nosuch", "nosuch2"),
		c("missing first file, unidirectional", "-P", "nosuch", "a"),
		c("missing second file, unidirectional", "-P", "a", "nosuch"),
		c("an empty file without permissions as new", "-N", "-u", "gone", "a"),
		c("a missing file in a directory operand", "nosuch", "dd"),

		in("first file from stdin", "a\nb\nX\n", "-", "a"),
		in("second file from stdin", "a\nb\nX\n", "a", "-"),
		in("stdin with labels", "a\nb\nX\n", "-u", "--label", "in", "--label", "file", "-", "a"),
		in("stdin with itself", "a\n", "-", "-"),
		in("stdin brief", "zzz\n", "-q", "-", "a"),
		in("stdin binary", "\x00\x01\n", "-", "a"),
		in("stdin and a directory", "a\n", "-", "dd"),
		in("a directory and stdin", "a\n", "dd", "-"),
		{name: "stdin from a file", args: []string{"-", "b"}, dir: dir, stdinPath: filepath.Join(dir, "a")},
		{name: "stdin from a file, brief", args: []string{"-q", "-", "b"}, dir: dir, stdinPath: filepath.Join(dir, "a")},

		c("no operands"),
		c("one operand", "a"),
		c("extra operand", "a", "b", "c"),
		c("conflicting styles", "-c", "-u", "a", "b"),
		c("conflicting with normal", "-u", "--normal", "a", "b"),
		c("normal style", "--normal", "a", "b"),
		c("invalid context length", "-U", "x", "a", "b"),
		c("negative context length", "-C", "-1", "a", "b"),
		c("invalid long context length", "--context=2x", "a", "b"),
		c("invalid horizon length", "--horizon-lines=x", "a", "b"),
		c("missing option value", "a", "b", "-U"),
		c("too many labels", "--label", "1", "--label", "2", "--label", "3", "a", "b"),
		c("unknown option", "-j", "a", "b"),
		c("unknown long option", "--bogus", "a", "b"),
		c("ambiguous long option", "--ignore", "a", "b"),
		c("ambiguous help prefix", "--h", "a", "b"),
		c("value for a flag", "--brief=1", "a", "b"),
		c("obsolete no-op options", "-h", "--binary", "a", "b"),
		c("options after operands", "a", "b", "-u"),
		c("operands after a double dash", "--", "a", "b"),
		{name: "full stdout", args: []string{"a", "b"}, dir: dir, stdout: stdoutFull},
		{name: "full stdout when identical and reported", args: []string{"-s", "a", "a2"}, dir: dir, stdout: stdoutFull},
	}
	cases = append(cases, diffFormatCases(t)...)
	return append(cases, diffOptionCases(t)...)
}

func TestDiffParity(t *testing.T) {
	requireParity(t, "diff", diffCases(t))
}

func TestDiffHelpVersion(t *testing.T) {
	requireHelp(t, "diff", []string{"--help"}, 0)
	requireHelp(t, "diff", []string{"--he"}, 0)
	requireVersion(t, "diff", []string{"--version"}, 0)
	requireVersion(t, "diff", []string{"-v"}, 0)
	requireVersion(t, "diff", []string{"--vers"}, 0)
}
