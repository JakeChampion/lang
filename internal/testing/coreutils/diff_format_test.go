package coreutils

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// prDate is the date in the page header of pr, which is the time of the run.
var prDate = regexp.MustCompile(`\d{4}-\d\d-\d\d \d\d:\d\d`)

func maskPrDate(s string) string {
	return prDate.ReplaceAllString(s, "DATE")
}

// diffFormatCases are the output formats of diff other than normal, context
// and unified: the ed scripts, RCS, side by side, -D and the group and line
// formats.
func diffFormatCases(t *testing.T) []invocation {
	dir := t.TempDir()
	w := func(name, content string) {
		t.Helper()
		writeFile(t, dir, name, content)
	}
	w("a", "a\nb\nc\nd\ne\n")
	w("b", "a\nB\nc\nd\ne\nf\n")
	w("nonl", "a\nb\nc")
	w("nonl2", "a\nb\nd")
	w("nl3", "a\nb\nc\n")
	w("dot", "x\n.\ny\n")
	w("dots", ".\n.\nz\n.\n")
	w("tabs1", "x\tab\n\n\tq\nz\n")
	w("tabs2", "x\tAb\n\n\tQ\nz\nw\n")
	w("y1", "a\nb\nc\nd\ne\nq\n")
	w("y2", "a\nB\nc\ne\nf\n")
	w("ctl1", "a\x01b\n\xe9\xe9\n\x80x\n\x7fz\n\x0c\x01\na\x08\x08\x01\nx\ry\n")
	w("ctl2", "q\n")
	w("tab4", "\t\x01\n\t\xe9\n")
	w("long1", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnop\nshort\n")
	w("long2", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP\nshort\n")
	w("bin1", "\x00a\n")
	w("bin2", "\x00b\n")
	w("m1", "a\nb\nc\na\nb\nb\na\n")
	w("m2", "c\nb\na\nb\na\nc\n")
	diffMkdir(t, dir, "d1", "d2")
	w("d1/f", "x\ny\n")
	w("d2/f", "x\nY\n")

	c := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir}
	}
	in := func(name, stdin string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, stdin: stdin}
	}
	return []invocation{
		c("ed script", "-e", "a", "b"),
		c("ed script reversed", "--ed", "b", "a"),
		c("ed script with a dot line", "-e", "a", "dot"),
		c("ed script with dot lines in a row", "-e", "a", "dots"),
		c("ed script from a dot line", "-e", "dot", "a"),
		c("ed script with no newline at the end of both", "-e", "nonl", "nonl2"),
		c("ed script with no newline at the end of one", "-e", "a", "nonl"),
		c("ed script with no newline at the end of the other", "-e", "nonl", "a"),
		c("ed script with no newline at the end, equal", "-e", "nonl", "nonl"),
		c("ed script with no newline, label", "-e", "--label", "X", "nonl", "a"),
		in("ed script with no newline on stdin", "a\nb\nc", "-e", "-", "a"),
		c("ed script with an incomplete last line", "-e", "nl3", "nonl2"),
		c("ed script, brief", "-e", "-q", "a", "nonl"),
		c("ed script for equal files", "-e", "a", "a"),
		c("ed script for binary files", "-e", "bin1", "bin2"),
		c("ed script for binary files as text", "-e", "-a", "nonl", "bin1"),
		c("ed script in a directory", "-e", "-r", "d1", "d2"),
		c("forward ed script", "-f", "a", "b"),
		c("forward ed script reversed", "--forward-ed", "b", "a"),
		c("forward ed script with a dot line", "-f", "a", "dot"),
		c("forward ed script with no newline", "-f", "nonl", "a"),
		c("RCS", "-n", "a", "b"),
		c("RCS reversed", "--rcs", "b", "a"),
		c("RCS with a dot line", "-n", "a", "dot"),
		c("RCS with no newline at the end of both", "-n", "nonl", "nonl2"),
		c("RCS with no newline at the end of one", "-n", "a", "nonl"),
		c("RCS in a directory", "-n", "-r", "d1", "d2"),
		c("RCS with a minimal diff", "-n", "m1", "m2"),

		c("side by side", "-y", "a", "b"),
		c("side by side long option", "--side-by-side", "y1", "y2"),
		c("side by side width 30", "-y", "-W", "30", "y1", "y2"),
		c("side by side width long option", "-y", "--width=40", "tabs1", "tabs2"),
		c("side by side width 1", "-y", "-W", "1", "ctl1", "ctl2"),
		c("side by side width 2", "-y", "-W", "2", "y1", "y2"),
		c("side by side width 3", "-y", "-W", "3", "ctl1", "ctl2"),
		c("side by side width 9", "-y", "-W", "9", "ctl1", "ctl2"),
		c("side by side width 18", "-y", "-W", "18", "ctl1", "ctl2"),
		c("side by side tab at the column end", "-y", "-W", "12", "--tabsize=4", "tab4", "ctl2"),
		c("side by side long lines", "-y", "-W", "40", "long1", "long2"),
		c("side by side left column", "-y", "-W", "30", "--left-column", "y1", "y2"),
		c("side by side suppress common lines", "-y", "-W", "30", "--suppress-common-lines", "y1", "y2"),
		c("side by side expand tabs", "-y", "-W", "40", "-t", "tabs1", "tabs2"),
		c("side by side tab size", "-y", "-W", "40", "--tabsize=3", "tabs1", "tabs2"),
		c("side by side tab size and expand", "-y", "-W", "40", "--tabsize=3", "-t", "tabs1", "tabs2"),
		c("side by side initial tab", "-y", "-W", "40", "-T", "tabs1", "tabs2"),
		c("side by side no newline", "-y", "-W", "30", "nonl", "nl3"),
		c("side by side no newline other side", "-y", "-W", "30", "nl3", "nonl"),
		c("side by side no newline both", "-y", "-W", "30", "nonl", "nonl2"),
		c("side by side equal files", "-y", "a", "a"),
		c("side by side equal files reported", "-y", "-s", "a", "a"),
		c("side by side equal files, brief", "-y", "-q", "a", "a"),
		c("side by side, brief", "-y", "-q", "a", "b"),
		c("side by side binary files", "-y", "bin1", "bin2"),
		c("side by side binary files equal", "-y", "bin1", "bin1"),
		c("side by side ignore blank lines", "-y", "-W", "30", "-B", "tabs1", "tabs2"),
		c("side by side in a directory", "-y", "-r", "-W", "20", "d1", "d2"),
		c("side by side without the style", "-W", "30", "--left-column", "--suppress-common-lines", "a", "b"),
		c("merge assist", "--sdiff-merge-assist", "y1", "y2"),
		c("merge assist width and left column", "--sdiff-merge-assist", "-W", "20", "--left-column", "y1", "y2"),
		c("merge assist suppress common lines", "--sdiff-merge-assist", "--suppress-common-lines", "y1", "y2"),
		c("merge assist with side by side", "--sdiff-merge-assist", "-y", "y1", "y2"),
		c("merge assist binary files", "--sdiff-merge-assist", "bin1", "bin2"),
		c("merge assist equal files reported", "--sdiff-merge-assist", "-s", "a", "a"),

		c("ifdef", "-D", "X", "a", "b"),
		c("ifdef long option", "--ifdef=NAME", "y1", "y2"),
		c("ifdef equal files", "-DX", "a", "a"),
		in("ifdef equal to stdin", "a\nb\nc\nd\ne\n", "-DX", "a", "-"),
		c("ifdef no newline", "-DX", "nonl", "nl3"),
		c("ifdef binary files", "-DX", "bin1", "bin2"),
		c("ifdef twice with one name", "-DX", "-DX", "a", "b"),
		c("ifdef twice with two names", "-DX", "-DY", "a", "b"),
		c("ifdef directories", "-DX", "d1", "d2"),
		c("ifdef directories recursive", "-r", "-DX", "d1", "d2"),
		c("ifdef and a group format", "-DX", "--old-group-format=O%<", "a", "b"),
		c("group format and ifdef", "--old-group-format=O%<", "-DX", "a", "b"),
		c("ifdef and the same unchanged format", "--unchanged-group-format=%=", "-DX", "a", "b"),
		c("ifdef and another unchanged format", "--unchanged-group-format=x", "-DX", "a", "b"),
		c("ifdef with ignore blank lines", "-DX", "-B", "tabs1", "tabs2"),
		c("ifdef with expand tabs", "-DX", "-t", "tabs1", "tabs2"),
		c("old group format", "--old-group-format=O", "a", "b"),
		c("old group format reversed", "--old-group-format=O%<", "b", "a"),
		c("new group format", "--new-group-format=N%>", "b", "a"),
		c("changed group format", "--changed-group-format=C%<%>", "a", "b"),
		c("changed group format reversed", "--changed-group-format=C%<%>", "b", "a"),
		c("changed group format and no unchanged lines", "--unchanged-group-format=", "--changed-group-format=C%<%>", "b", "a"),
		c("group letters", "--changed-group-format=[%df %dl %dn %de %dm|%dF %dL %dN %dE %dM]\n", "a", "b"),
		c("group printf specs", "--changed-group-format=[%5dF|%-5dF|%.3dF|%5.3xF|%XN|%oN|%-08.2xF|%0dF|%.dF|%.0de|%10.5dF|%'dF]\n", "a", "b"),
		c("group bad printf specs", "--changed-group-format=[%+dF|%#xF|% dF|%5d|%z|%F]\n", "a", "b"),
		c("group char literals", "--changed-group-format=%c'x'%c'\\101'%c'\\0'%c'\\777'%c'ab'%c'\\08'%c'\\''%c''|%5c'q'\n", "a", "b"),
		c("group percent", "--changed-group-format=%%%", "a", "b"),
		c("group conditions", "--changed-group-format=%(f=2?two:other)%(f=e?a:b)%(00=0?eq:ne)%(-1=-1?a:b)%(12=12?t:f)\n", "a", "b"),
		c("group nested conditions", "--changed-group-format=%(F=2?two:%(N>1?x:y))\n", "a", "b"),
		c("group condition without an else", "--changed-group-format=%(0=0?a)|%(0=0?a:b|%(00=0?eq:ne)|x\n", "a", "b"),
		c("group condition that overflows", "--changed-group-format=%(99999999999999999999=1?a:b)\n", "a", "b"),
		c("group common lines in a change", "--changed-group-format=%=|\n", "a", "b"),
		c("line format", "--line-format=%L", "a", "b"),
		c("line format without the newline", "--line-format=%l|", "a", "b"),
		c("line format numbers", "--line-format=%dn %5dn %-3xn|%L", "a", "b"),
		c("line format bad specs", "--line-format=%n %x %< %= %(n=1?a:b) %c'Z' %%|%l\n", "a", "b"),
		c("line format no newline", "--line-format=[%L]", "nonl", "nl3"),
		c("line format no newline, short", "--line-format=[%l]", "nonl", "nonl2"),
		c("line formats of each kind", "--unchanged-line-format=  %l\n", "--old-line-format=- %L", "--new-line-format=+ %L", "a", "b"),
		c("line format and old line format", "--line-format=%L", "--old-line-format=<%L", "a", "b"),
		c("old line format and line format", "--old-line-format=<%L", "--line-format=%L", "a", "b"),
		c("line format twice", "--line-format=a", "--line-format=b", "a", "b"),
		c("unchanged line format empty for equal files", "--unchanged-line-format=", "a", "a"),
		c("unchanged group format empty for equal files", "--unchanged-group-format=", "-DX", "a", "a"),
		c("line format with expand tabs", "--line-format=%5dn %L", "-t", "tabs1", "tabs2"),
		c("conflicting styles ed and side by side", "-y", "-e", "a", "b"),
		c("conflicting styles ed and RCS", "-e", "-n", "a", "b"),
		c("conflicting styles forward ed and ifdef", "-f", "-D", "x", "a", "b"),
		c("conflicting styles merge assist and unified", "--sdiff-merge-assist", "-u", "a", "b"),
		c("conflicting styles line format and context", "--line-format=%L", "-c", "a", "b"),
		c("invalid width", "-W", "x", "a", "b"),
		c("zero width", "-W", "0", "a", "b"),
		c("negative width", "-W", "-1", "a", "b"),
		c("huge width", "-W", "99999999999999999999999", "a", "b"),
		c("conflicting widths", "-W", "10", "-W", "20", "a", "b"),
		c("one width twice", "-W", "10", "-W", "10", "-y", "a", "b"),
		c("missing ifdef name", "a", "b", "-D"),
	}
}

// diffOptionCases are the options that change the lines, the output and the
// files that diff compares.
func diffOptionCases(t *testing.T) []invocation {
	dir := t.TempDir()
	w := func(name, content string) {
		t.Helper()
		writeFile(t, dir, name, content)
	}
	w("a", "a\nb\nc\nd\ne\n")
	w("b", "a\nB\nc\nd\ne\nf\n")
	w("nonl", "a\nb\nc")
	w("nonl2", "a\nb\nd")
	w("crlf", "a\r\nB\r\nc\r\n")
	w("cr1", "a \r\n")
	w("cr2", "a\n")
	w("cr3", "a\r")
	w("tabs1", "x\tab\n\n\tq\nz\n")
	w("tabs2", "x\tAb\n\n\tQ\nz\nw\n")
	w("sp1", "a\tb\n  c\nd  \n\te\n")
	w("sp2", "a       b\n\tc\nd\n        e\n")
	w("tr1", "a \nb\t\nc\n")
	w("tr2", "a\nb\nc  \n")
	w("tr3", "a")
	w("tr4", "a \n")
	w("f1", "int main(void)\n{\n  x0;\n  x1;\n  x2;\n  x3;\n  x4;\n  x5;\n  x6;\n  x7;\n}\n  static int foo_bar_baz_qux_quux_corge_grault_garply(int)   \n{\n  y0;\n  y1;\n  y2;\n  y3;\n  y4;\n  y5;\n  y6;\n  y7;\n}\n")
	w("f2", "int main(void)\n{\n  x0;\n  x1;\n  x2;\n  CHANGED;\n  x4;\n  x5;\n  x6;\n  x7;\n}\n  static int foo_bar_baz_qux_quux_corge_grault_garply(int)   \n{\n  y0;\n  y1;\n  y2;\n  y3;\n  CHANGED2;\n  y5;\n  y6;\n  CHANGED3;\n}\n")
	w("blank1", "a\n\nb\n  \nc\n")
	w("blank2", "a\nb\n\t\nc\nd\n")
	w("re1", "a\nbb\nc\n# x\n")
	w("re2", "a\nb+\nc\n# y\n")
	w("ex", "x\n  \nm*\n")
	w("bin1", "\x00a\n")
	w("bin2", "\x00b\n")
	diffMkdir(t, dir, "dA/sub", "dB/sub", "cA", "cB", "sA/a", "sA/m", "sA/z", "sB/a", "sB/m", "sB/z", "lA", "lB")
	for _, d := range []string{"dA", "dB"} {
		for _, n := range []string{"x", "y", "m.txt", "Mixed"} {
			w(d+"/"+n, d+" "+n+"\n")
		}
		w(d+"/sub/z", "z "+d+"\n")
		w(d+"/same", "s\n")
	}
	w("dA/only", "only\n")
	w("cA/Foo", "1\n")
	w("cB/foo", "2\n")
	w("cA/bar", "3\n")
	w("cB/BAR", "4\n")
	for _, d := range []string{"a", "m", "z"} {
		for _, f := range []string{"a", "m", "z"} {
			w("sA/"+d+"/"+f, d+f+"\n")
			w("sB/"+d+"/"+f, "X"+d+f+"\n")
		}
		w("sA/"+d+".txt", "t"+d+"\n")
		w("sB/"+d+".txt", "T"+d+"\n")
	}
	for _, l := range [][2]string{{"lA/l", "../a"}, {"lB/l", "../b"}, {"lA/same", "../a"}, {"lB/same", "../a"}, {"lA/dang", "nosuch"}, {"lB/dang", "nosuch2"}, {"lA/mixed", "../a"}, {"linka", "a"}, {"linkb", "b"}, {"linka2", "a"}} {
		if err := os.Symlink(l[1], filepath.Join(dir, l[0])); err != nil {
			t.Fatal(err)
		}
	}
	w("lB/mixed", "a\nb\nc\nd\ne\n")

	c := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir}
	}
	in := func(name, stdin string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, stdin: stdin}
	}
	paged := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: dir, mask: maskPrDate}
	}
	return []invocation{
		c("show c function", "-p", "f1", "f2"),
		c("show c function, unified", "-p", "-u", "f1", "f2"),
		c("show c function, ed", "-p", "-e", "f1", "f2"),
		c("show c function, normal after", "-p", "--normal", "f1", "f2"),
		c("show c function, normal before", "--normal", "-p", "f1", "f2"),
		c("show c function, obsolete context", "-p", "-1", "f1", "f2"),
		c("show c function, no context", "-p", "-U0", "f1", "f2"),
		c("show function line", "-F", "x", "f1", "f2"),
		c("show function line, context", "-F", "x", "-c", "f1", "f2"),
		c("show function line, two expressions", "-U1", "-F", "^ *s", "-F", "^i", "f1", "f2"),
		c("show function line, long line", "-U1", "--show-function-line=^ *static", "f1", "f2"),
		c("show function line, alternation", "-u", "-F", "x1\\|^i", "f1", "f2"),
		c("show function line, colors", "--color=always", "-u", "-p", "f1", "f2"),
		c("show function line, invalid expression", "-F", "[", "a", "b"),
		c("show function line, unmatched paren", "-F", "\\)", "a", "b"),

		c("initial tab", "-T", "tabs1", "tabs2"),
		c("initial tab, unified", "-T", "-u", "tabs1", "tabs2"),
		c("initial tab, context", "--initial-tab", "-c", "tabs1", "tabs2"),
		c("initial tab, ed", "-T", "-e", "tabs1", "tabs2"),
		c("initial tab, RCS", "-T", "-n", "tabs1", "tabs2"),
		c("expand tabs", "-t", "tabs1", "tabs2"),
		c("expand tabs, unified", "--expand-tabs", "-u", "tabs1", "tabs2"),
		c("expand tabs and initial tab", "-T", "-t", "tabs1", "tabs2"),
		c("expand tabs with a tab size", "--tabsize=3", "-t", "-u", "tabs1", "tabs2"),
		c("expand tabs with carriage returns", "-t", "-c", "crlf", "b"),
		c("suppress blank empty", "--suppress-blank-empty", "tabs1", "tabs2"),
		c("suppress blank empty, unified", "--suppress-blank-empty", "-u", "tabs1", "tabs2"),
		c("suppress blank empty, context", "--suppress-blank-empty", "-c", "tabs1", "tabs2"),
		c("suppress blank empty and initial tab", "--suppress-blank-empty", "-T", "-u", "tabs1", "tabs2"),
		c("invalid tab size", "--tabsize=x", "a", "b"),
		c("zero tab size", "--tabsize=0", "a", "b"),
		c("negative tab size", "--tabsize=-1", "a", "b"),
		c("conflicting tab sizes", "--tabsize=4", "--tabsize=5", "a", "b"),
		c("huge tab size", "--tabsize=99999999999999999999", "a", "b"),

		paged("paginate", "-l", "a", "b"),
		paged("paginate side by side", "-l", "-y", "a", "b"),
		paged("paginate directories", "-l", "-r", "dA", "dB"),
		paged("paginate with labels", "-l", "-u", "--label", "L1", "--label", "L2", "a", "b"),
		paged("paginate equal files", "--paginate", "a", "a"),
		paged("paginate equal files reported", "-l", "-s", "a", "a"),
		paged("paginate brief", "-l", "-q", "a", "b"),
		paged("paginate binary files", "-l", "bin1", "bin2"),
		paged("paginate an ed script", "-l", "-e", "a", "b"),

		c("color", "--color=always", "a", "b"),
		c("color unified", "--color=always", "-u", "a", "b"),
		c("color context", "--color=always", "-c", "a", "b"),
		c("color side by side", "--color=always", "-y", "-W", "30", "tabs1", "tabs2"),
		c("color side by side left column", "--color=always", "-y", "-W", "30", "--left-column", "tabs1", "tabs2"),
		c("color ed script", "--color=always", "-e", "a", "b"),
		c("color ifdef", "--color=always", "-DX", "a", "b"),
		c("color no newline", "--color=always", "nonl", "nonl2"),
		c("color no newline, unified", "--color=always", "-u", "nonl", "nonl2"),
		c("color no newline, context", "--color=always", "-c", "nonl", "nonl2"),
		c("color no newline, side by side", "--color=always", "-y", "nonl", "nonl2"),
		c("color directories", "--color=always", "-r", "dA", "dB"),
		c("color auto to a pipe", "--color", "a", "b"),
		c("color never", "--color=never", "a", "b"),
		c("color last option wins", "--color=always", "--color=never", "a", "b"),
		c("invalid color", "--color=bad", "a", "b"),
		c("empty color", "--color=", "a", "b"),
		c("color in capitals", "--color=ALWAYS", "a", "b"),
		c("palette", "--color=always", "--palette=ad=1;2:de=4:hd=7:ln=33:rs=9", "-u", "a", "b"),
		c("palette escapes", "--color=always", "--palette=*.c=5:ad=\\x41\\101^A^?\\_\\e:de=\\e[", "a", "b"),
		c("palette escape sequence parts", "--color=always", "--palette=ec=5:lc=<:rc=>", "a", "b"),
		c("palette empty codes", "--color=always", "--palette=hd=:ad=", "-u", "a", "b"),
		c("palette twice", "--color=always", "--palette=ad=1", "--palette=de=2", "a", "b"),
		c("palette colons", "--color=always", "--palette=:::", "a", "b"),
		c("palette unknown prefix", "--color=always", "--palette=ad=1:xx=3", "a", "b"),
		c("palette without a value", "--color=always", "--palette=ad", "a", "b"),
		c("palette one letter", "--color=always", "--palette=a", "a", "b"),
		c("palette bad caret", "--color=always", "--palette=ad=^", "a", "b"),
		c("palette trailing backslash", "--color=always", "--palette=ad=\\", "a", "b"),
		c("palette extension only", "--color=always", "--palette=*x", "a", "b"),
		c("palette without colors", "--color=never", "--palette=zz", "a", "b"),

		c("ignore tab expansion", "-E", "sp1", "sp2"),
		c("ignore tab expansion, unified", "--ignore-tab-expansion", "-u", "sp1", "sp2"),
		c("ignore tab expansion with a tab size", "-E", "--tabsize=4", "sp1", "sp2"),
		c("ignore trailing space", "-Z", "tr1", "tr2"),
		c("ignore trailing space and no newline", "-Z", "tr3", "tr4"),
		c("ignore tab expansion and trailing space", "-EZ", "sp1", "sp2"),
		c("ignore tab expansion and case", "-E", "-i", "tabs1", "tabs2"),
		c("ignore trailing space with blank lines", "-Z", "-B", "blank1", "blank2"),
		c("ignore matching lines", "-I", "B", "a", "b"),
		c("ignore matching lines, all", "-I", "^[bBf]$", "a", "b"),
		c("ignore matching lines, unified", "--ignore-matching-lines=^#", "-u", "re1", "re2"),
		c("ignore matching lines, two expressions", "-I", "^#", "-I", "b", "re1", "re2"),
		c("ignore matching lines and blank lines", "-I", "c", "-B", "-U1", "blank1", "blank2"),
		c("ignore matching lines, brief", "-q", "-I", "B", "a", "b"),
		c("ignore matching lines, side by side", "-I", "b", "-y", "-W", "30", "a", "b"),
		c("ignore matching lines, BRE plus", "-I", "b\\+", "re1", "re2"),
		c("ignore matching lines, literal plus", "-I", "b+", "re1", "re2"),
		c("ignore matching lines, interval", "-I", "b\\{2\\}", "re1", "re2"),
		c("ignore matching lines, word", "-I", "\\w", "a", "b"),
		c("ignore matching lines, leading star", "-I", "*b", "a", "b"),
		c("ignore matching lines, alternation", "-I", "a\\|b", "a", "b"),
		c("ignore matching lines, bracket", "-I", "[bB]", "a", "b"),
		c("ignore matching lines, invalid", "-I", "[", "a", "b"),
		c("ignore matching lines, unmatched bracket", "-I", "[b", "a", "b"),
		c("ignore matching lines, unmatched paren", "-I", "\\(", "a", "b"),
		c("ignore matching lines, unmatched brace", "-I", "b\\{", "a", "b"),
		c("ignore matching lines, bad interval", "-I", "x\\{2,1\\}", "a", "b"),
		c("strip trailing cr", "--strip-trailing-cr", "crlf", "b"),
		c("strip trailing cr, unified", "--strip-trailing-cr", "-u", "crlf", "b"),
		c("strip trailing cr, brief", "--strip-trailing-cr", "-q", "crlf", "b"),
		c("strip trailing cr before a space", "--strip-trailing-cr", "cr1", "cr2"),
		c("strip trailing cr at the end", "--strip-trailing-cr", "cr3", "cr2"),
		c("inhibit hunk merge", "--inhibit-hunk-merge", "-U1", "f1", "f2"),

		c("exclude", "-r", "-x", "m*", "dA", "dB"),
		c("exclude the operand", "-r", "-x", "dA", "dA", "dB"),
		c("exclude a directory", "-r", "--exclude=sub", "dA", "dB"),
		c("exclude twice", "-r", "-x", "x", "-x", "y", "dA", "dB"),
		c("exclude all", "-r", "--exclude=*", "dA", "dB"),
		c("exclude a file operand", "-x", "a", "a", "b"),
		c("exclude an invalid pattern", "-x", "[", "a", "b"),
		c("exclude from a file", "-r", "-X", "ex", "dA", "dB"),
		c("exclude from a file twice", "-r", "--exclude-from=ex", "-X", "ex", "dA", "dB"),
		c("exclude from a missing file", "-X", "nosuch", "a", "b"),
		c("starting file", "-r", "-S", "m", "sA", "sB"),
		c("starting file with a suffix", "-r", "--starting-file=m.txt", "sA", "sB"),
		c("starting file past the end", "-r", "-S", "zzz", "sA", "sB"),
		c("starting file in a subdirectory", "-S", "m", "sA/a", "sB/a"),
		c("starting file twice", "-S", "z", "-S", "y", "a", "b"),
		c("from file", "--from-file=a", "b", "a", "nosuch", "nonl"),
		c("from file alone", "--from-file=a"),
		c("from file twice", "--from-file=a", "--from-file=b", "a"),
		c("from file a directory", "--from-file=dA", "dB"),
		c("from file recursive", "-r", "--from-file=dA", "dB", "a"),
		in("from file stdin", "a\nX\n", "--from-file=-", "a"),
		in("from file stdin to stdin", "a\n", "--from-file=-", "-"),
		c("from file missing", "--from-file=nosuch", "a", "b"),
		c("to file", "--to-file=b", "a", "b", "nonl"),
		c("to file a directory", "--to-file=dA", "a", "x"),
		c("to file twice, one name", "--to-file=a", "--to-file=a", "a", "b"),
		c("from file and to file", "--from-file=a", "--to-file=b"),
		c("no dereference", "--no-dereference", "lA", "lB"),
		c("no dereference recursive", "-r", "--no-dereference", "lA", "lB"),
		c("no dereference links", "--no-dereference", "linka", "linkb"),
		c("no dereference same links", "--no-dereference", "linka", "linka2"),
		c("no dereference brief", "--no-dereference", "-q", "linka", "linkb"),
		c("no dereference link and file", "--no-dereference", "linka", "b"),
		c("no dereference file and link", "--no-dereference", "a", "linkb"),
		c("dereference links", "linka", "linkb"),
		c("ignore file name case", "--ignore-file-name-case", "cA", "cB"),
		c("ignore file name case off again", "--ignore-file-name-case", "--no-ignore-file-name-case", "cA", "cB"),
		c("file name case", "cA", "cB"),
		c("ignore file name case and exclude", "-r", "--ignore-file-name-case", "-x", "f*", "cA", "cB"),
		c("ignore file name case and exclude upper", "--ignore-file-name-case", "-x", "B*", "cA", "cB"),
		c("ignore file name case and starting file", "--ignore-file-name-case", "-r", "-S", "MIXED", "dA", "dB"),
		c("no directory", "---no-directory", "dA", "dB"),
		c("no directory file and directory", "---no-directory", "a", "dA"),
		c("no directory directory and file", "---no-directory", "dA", "a"),
		c("ambiguous ignore", "--ignore", "a", "b"),
		c("ambiguous no", "--no", "a", "b"),
		c("ambiguous s", "--s", "a", "b"),
		c("ambiguous line", "--line", "a", "b"),
		c("ambiguous old", "--old", "a", "b"),
		c("prefix of unchanged", "--unch", "a", "b"),
		c("prefix of tabsize", "--tab=4", "-t", "a", "b"),
		c("prefix of from file", "--from=a", "b"),
	}
}
