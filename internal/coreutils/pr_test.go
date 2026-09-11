package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prFile writes `content` under `dir` as `name` with a PINNED
// modification time and returns its path.
//
// The mtime is the whole of what makes pr's default header
// reproducible: with a named FILE operand the header's date is that
// file's st_mtime, not the wall clock, so two runs seconds apart agree
// byte for byte. Every fixture here gets one, and the few cases whose
// header cannot come from a file — standard input and -m, which have no
// one file to ask — carry `-D` with a format holding no time at all.
func prFile(t *testing.T, dir, name, content string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("pr", prCases)
}

// prCases is pr(1)'s corpus.
//
// Four behaviours carry most of it, and each is the reference binary's
// rather than the obvious reading:
//
//   - The DIGITS of -COLUMN accumulate over the whole scan, so `-2 -3`
//     is twenty-three columns.
//   - Whitespace pr GENERATES is written with output tabs while input
//     spaces are copied verbatim until -i; a TAB in the input is a
//     third thing again, copied as the byte in one column and turned
//     into generated whitespace in several — unless the separator is
//     the TAB that -s or -J leaves by default.
//   - Positions inside a column are LOCAL, so a tab stop, a -e
//     expansion and the -n separator all count from the column's start.
//   - A byte's display WIDTH is not one: a backspace is minus one
//     column and every other non-printable is zero, until -c or -v
//     replaces it with an escape whose printed length is the width.
func prCases(t *testing.T) []invocation {
	dir := t.TempDir()
	mt := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	f := prFile(t, dir, "f", "l1\nl2\nl3\n", mt)
	s10 := prFile(t, dir, "s10", seqLines(10), mt)
	s3 := prFile(t, dir, "s3", seqLines(3), mt)
	s12 := prFile(t, dir, "s12", seqLines(12), mt)
	s56 := prFile(t, dir, "s56", seqLines(56), mt)
	s57 := prFile(t, dir, "s57", seqLines(57), mt)
	s200 := prFile(t, dir, "s200", seqLines(200), mt)
	empty := prFile(t, dir, "e0", "", mt)
	other := prFile(t, dir, "other", "o1\no2\n", time.Date(1999, 12, 31, 23, 58, 59, 0, time.UTC))
	nsec := prFile(t, dir, "nsec", "z\n", time.Date(2001, 2, 3, 4, 5, 6, 123456789, time.UTC))
	epoch := prFile(t, dir, "epoch", "z\n", time.Unix(0, 0).UTC())
	pre70 := prFile(t, dir, "pre70", "z\n", time.Date(1958, 3, 9, 1, 2, 3, 0, time.UTC))
	y2100 := prFile(t, dir, "y2100", "z\n", time.Date(2100, 6, 15, 12, 34, 56, 0, time.UTC))
	tabbed := prFile(t, dir, "tabbed", "a\tb\tc\nxy\tz\n", mt)
	spaces := prFile(t, dir, "spaces", "a  b   c\n", mt)
	wide2 := prFile(t, dir, "wide2", "a\t\tb\n", mt)
	colon := prFile(t, dir, "colon", "a:b:c\nx\ty\n", mt)
	long := prFile(t, dir, "long", strings.Repeat("a", 36)+"\nbbbb\n"+strings.Repeat("c", 20)+"\nd\n", mt)
	ctrl := prFile(t, dir, "ctrl", "a\x01b\nc\x7fd\ne\x80f\n\tg\n", mt)
	bs := prFile(t, dir, "bs", "a\\b\nc\rd\n", mt)
	backsp := prFile(t, dir, "backsp", "ab\bcdefghijklmnop\n", mt)
	del := prFile(t, dir, "del", "a\x7fb\tc\bd\n", mt)
	wctl := prFile(t, dir, "wctl", "aa\abbbbbbbbbbbb\n", mt)
	ff := prFile(t, dir, "ff", "one\ntwo\n\fthree\nfour\n\f\ffive\n", mt)
	ffmid := prFile(t, dir, "ffmid", "one\ntw\fo\nthree\n", mt)
	ff1 := prFile(t, dir, "ff1", "\fa\n", mt)
	fftail := prFile(t, dir, "fftail", seqLines(56)+"\f", mt)
	nonl := prFile(t, dir, "nonl", "a\nb", mt)
	nonl2 := prFile(t, dir, "nonl2", "1\n2\n3", mt)
	blank := prFile(t, dir, "blank", "a\n\nb\n", mt)
	raw := prFile(t, dir, "na\xffme", "x\n", mt)
	big := prFile(t, dir, "big", seqLines(20000), mt)
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	if err := os.Symlink("nowhere", filepath.Join(dir, "blink")); err != nil {
		t.Fatal(err)
	}
	blink := filepath.Join(dir, "blink")

	tabs := "a\tb\nc\td\n"

	cases := []invocation{
		// The page: two blanks, the header, two blanks, 56 body lines,
		// five blanks — and the LAST page padded to the same length.
		{name: "one page", args: []string{f}},
		{name: "four pages", args: []string{s200}},
		{name: "exactly one page", args: []string{s56}},
		{name: "one line past a page", args: []string{s57}},
		{name: "empty input prints nothing", args: []string{empty}},
		{name: "empty then a file", args: []string{empty, f}},
		{name: "two files each start at page one", args: []string{f, s10}},
		{name: "two files with different mtimes", args: []string{f, other}},
		{name: "omit the header", args: []string{"-t", f}},
		{name: "omit the header over two files", args: []string{"-t", f, s10}},
		{name: "omit pagination", args: []string{"-T", f}},
		{name: "no final newline", args: []string{nonl}},
		{name: "no final newline with -t", args: []string{"-t", nonl}},

		// The header line: date, the name centred, `Page N`.
		{name: "date format", args: []string{"-D", "XY", f}},
		{name: "empty date format", args: []string{"-D", "", f}},
		{name: "header text", args: []string{"-h", "HELLO", f}},
		{name: "empty header text", args: []string{"-h", "", f}},
		{name: "glued header text", args: []string{"-hHELLO", f}},
		{name: "long header text", args: []string{"-h", strings.Repeat("H", 70), f}},
		{name: "header 47 wide", args: []string{"-h", strings.Repeat("a", 47), f}},
		{name: "header 48 wide", args: []string{"-h", strings.Repeat("a", 48), f}},
		{name: "header 49 overflows the page", args: []string{"-h", strings.Repeat("a", 49), f}},
		{name: "header 50 overflows the page", args: []string{"-h", strings.Repeat("a", 50), f}},
		{name: "header 51 overflows the page", args: []string{"-h", strings.Repeat("a", 51), f}},
		{name: "a control byte in the header is no columns", args: []string{"-h", "\t", f}},
		{name: "a control byte in the date is no columns", args: []string{"-D", "\t", f}},
		{name: "a high byte in the header is one column", args: []string{"-h", "\xff", f}},
		{name: "two high bytes in the date", args: []string{"-D", "\xc3\xa9", f}},
		{name: "a newline in the header splits it", args: []string{"-h", "A\nB", f}},
		{name: "narrow page keeps one space", args: []string{"-w", "20", f}},
		{name: "narrow page with a header", args: []string{"-w", "20", "-h", "xx", f}},
		{name: "page width one", args: []string{"-w", "1", f}},
		{name: "last header wins", args: []string{"-h", "A", "-h", "B", f}},
		{name: "last date wins", args: []string{"-D", "%Y", "-D", "Z", f}},
		{name: "-c never reaches the header", args: []string{"-c", "-h", "\x01", f}},
		{name: "-v never reaches the header", args: []string{"-v", "-h", "\x01", f}},

		// The offset.
		{name: "offset", args: []string{"-o", "5", f}},
		{name: "offset with -t", args: []string{"-o", "5", "-t", f}},
		{name: "offset zero", args: []string{"-o", "0", "-t", f}},
		{name: "offset on a blank line", args: []string{"-o", "5", "-t", blank}},
		{name: "offset with columns", args: []string{"-t", "-2", "-o", "3", s10}},
		{name: "offset of a whole tab", args: []string{"-t", "-o", "16", tabbed}},
		{name: "offset of a whole tab with columns", args: []string{"-t", "-o", "16", "-2", tabbed}},
		{name: "offset nine with output tabs", args: []string{"-t", "-o", "9", "-i.4", f}},
		{name: "offset nine with two columns", args: []string{"-t", "-2", "-o", "9", f}},

		// Page length.
		{name: "length ten implies -t", args: []string{"-l", "10", s200}},
		{name: "length eleven is one body line", args: []string{"-l", "11", s200}},
		{name: "length twelve", args: []string{"-l", "12", ff}},
		{name: "length twenty", args: []string{"-l", "20", s200}},
		{name: "length one", args: []string{"-l", "1", f}},
		{name: "length five", args: []string{"-l", "5", f}},

		// Form feeds: -f and -F are the same option.
		{name: "form feed", args: []string{"-f", f}},
		{name: "capital form feed", args: []string{"-F", f}},
		{name: "form feed over four pages", args: []string{"-F", s200}},
		{name: "form feed with a short length", args: []string{"-F", "-l", "20", s200}},
		{name: "form feed with one body line", args: []string{"-F", "-l", "11", s200}},
		{name: "form feed with -t emits none", args: []string{"-F", "-t", f}},
		{name: "form feed with -T emits none", args: []string{"-F", "-T", f}},
		{name: "lower form feed with a short length", args: []string{"-f", "-l", "20", s200}},

		// Form feeds in the INPUT.
		{name: "input form feeds break pages", args: []string{ff}},
		{name: "input form feeds with -t are text", args: []string{"-t", ff}},
		{name: "input form feeds with -T are dropped", args: []string{"-T", ff}},
		{name: "input form feeds with -t -T", args: []string{"-t", "-T", ff}},
		{name: "a form feed splits a line", args: []string{"-t", ffmid}},
		{name: "a form feed splits a line and the page", args: []string{ffmid}},
		{name: "a form feed splitting a line with a short page", args: []string{"-l", "12", ffmid}},
		{name: "a leading form feed is an empty page", args: []string{ff1}},
		{name: "a trailing form feed gains no page", args: []string{fftail}},
		{name: "a trailing form feed with -t is a byte", args: []string{"-t", fftail}},
		{name: "form feeds with -l 10", args: []string{"-l", "10", ff}},

		// Columns down.
		{name: "two columns", args: []string{"-t", "-2", s10}},
		{name: "three columns", args: []string{"-t", "-3", s10}},
		{name: "four columns", args: []string{"-t", "-4", s10}},
		{name: "five columns", args: []string{"-t", "-5", s10}},
		{name: "twenty columns", args: []string{"-t", "-20", s10}},
		{name: "two columns paginated", args: []string{"-2", s200}},
		{name: "three columns paginated", args: []string{"-3", s200}},
		{name: "three columns unpaginated", args: []string{"-t", "-3", s200}},
		{name: "one column explicitly", args: []string{"-1", f}},
		{name: "leading zero column", args: []string{"-01", f}},
		{name: "column balance one line", args: []string{"-t", "-3", s3}},
		{name: "column balance four lines", args: []string{"-t", "-3", s12}},
		{name: "column balance unterminated", args: []string{"-t", "-2", nonl2}},
		{name: "full pages are not balanced", args: []string{"-3", "-l", "16", s200}},
		{name: "digits accumulate across tokens", args: []string{"-2", "-3", f}},
		{name: "digits accumulate the other way", args: []string{"-3", "-2", "-t", s10}},
		{name: "digits accumulate to twelve", args: []string{"-12", "-t", s10}},
		{name: "digits accumulate with a zero", args: []string{"-1", "-0", "-t", s10}},
		{name: "digits glued to a flag", args: []string{"-2t", f}},
		{name: "flag glued to digits", args: []string{"-t5", f}},
		{name: "digits before a flag in one cluster", args: []string{"-5t", f}},
		{name: "digits around a bad byte", args: []string{"-1x2", f}},

		// Columns across.
		{name: "across two", args: []string{"-t", "-2", "-a", s10}},
		{name: "across three", args: []string{"-t", "-3", "-a", s10}},
		{name: "across four", args: []string{"-t", "-a", "-4", s10}},
		{name: "across paginated", args: []string{"-3", "-a", s200}},
		{name: "across with one column", args: []string{"-a", f}},
		{name: "across with a separator", args: []string{"-t", "-a", "-3", "-s:", s10}},
		{name: "across numbers every column", args: []string{"-t", "-a", "-3", "-n", s10}},

		// Parallel.
		{name: "merge two files", args: []string{"-t", "-m", s10, f}},
		{name: "merge three files", args: []string{"-t", "-m", s10, f, s10}},
		{name: "merge one file", args: []string{"-t", "-m", f}},
		{name: "merge with a header", args: []string{"-D", "X", "-m", f, other}},
		{name: "merge with -h", args: []string{"-D", "X", "-t", "-h", "HH", "-m", f, other}},
		{name: "merge numbers the first column", args: []string{"-t", "-m", "-n", s10, f}},
		{name: "merge numbers with a separator", args: []string{"-t", "-m", "-n:3", s10, f}},
		{name: "merge numbers three files", args: []string{"-t", "-m", "-n", s10, f, other}},
		{name: "merge numbers with a width", args: []string{"-t", "-m", "-n", "-W", "30", long, long}},
		{name: "merge with a width", args: []string{"-t", "-m", "-W", "30", long, long}},
		{name: "merge joins full lines", args: []string{"-t", "-m", "-J", s10, long}},
		{name: "merge truncates", args: []string{"-t", "-m", s10, long}},
		{name: "merge with a separator", args: []string{"-t", "-m", "-s:", s10, long}},
		{name: "merge packs failed opens out", args: []string{"-m", "-t", f, missing, missing, other}},
		{name: "merge packs failed opens out quietly", args: []string{"-m", "-t", "-r", f, missing, missing, other}},
		{name: "merge in a narrow page", args: []string{"-m", "-t", "-w", "3", f, other}},
		{name: "merge doubles spaces", args: []string{"-t", "-d", "-m", f, other}},
		{name: "merge with a directory is fatal first", args: []string{"-t", "-m", d, f}},

		// Line numbering.
		{name: "number lines", args: []string{"-t", "-n", s10}},
		{name: "number with a separator", args: []string{"-t", "-n:3", s10}},
		{name: "number with digits only", args: []string{"-t", "-n8", s10}},
		{name: "number with a letter separator", args: []string{"-t", "-nx3", s10}},
		{name: "number with a wide field", args: []string{"-t", "-n:20", s10}},
		{name: "number with a very wide field", args: []string{"-t", "-n:100", s10}},
		{name: "number one digit wraps", args: []string{"-t", "-n:1", s200}},
		{name: "number one digit truncates", args: []string{"-t", "-n1", s12}},
		{name: "number in two columns", args: []string{"-t", "-2", "-n", s10}},
		{name: "number in two columns with a separator", args: []string{"-t", "-2", "-n:3", s10}},
		{name: "number across pages", args: []string{"-n", s200}},
		{name: "number with a short page", args: []string{"-n", "-l", "20", s200}},
		{name: "first line number", args: []string{"-t", "-n", "-N", "3", s10}},
		{name: "first line number in columns", args: []string{"-t", "-2", "-n", "-N", "100", s10}},
		{name: "negative first line number", args: []string{"-t", "-n", "-N", "-1", s10}},
		{name: "zero first line number", args: []string{"-t", "-n", "-N", "0", s10}},
		{name: "first line number at INT_MAX", args: []string{"-t", "-n", "-N", "2147483647", s10}},
		{name: "first line number at INT_MIN", args: []string{"-t", "-n", "-N", "-2147483648", s10}},
		{name: "the counter wraps at INT_MAX", args: []string{"-t", "-n", "-N", "2147483645", s12}},
		{name: "number truncated to two digits", args: []string{"-t", "-n2", "-N", "-123", s3}},
		{name: "number truncated to three digits", args: []string{"-t", "-n3", "-N", "-123", s3}},
		{name: "number truncated to four digits", args: []string{"-t", "-n4", "-N", "-123", s3}},
		{name: "number truncated from eight digits", args: []string{"-t", "-n", "-N", "-12345678", s3}},
		{name: "number field wider than the column", args: []string{"-t", "-n", "-W", "3", s10}},
		{name: "number field wider than a narrow column", args: []string{"-t", "-2", "-W", "3", "-n", s10}},
		{name: "number leaves one text column", args: []string{"-t", "-n", "-W", "8", long}},
		{name: "number field width with a tab separator", args: []string{"-t", "-n", "-W", "30", long}},
		{name: "number field width with eight digits", args: []string{"-t", "-n8", "-W", "30", long}},
		{name: "number field width with a colon", args: []string{"-t", "-n:5", "-W", "30", long}},
		{name: "number field width with a wide colon", args: []string{"-t", "-n:8", "-W", "30", long}},
		{name: "number field width with a letter", args: []string{"-t", "-nx3", "-W", "30", long}},
		{name: "number with the tab width changed", args: []string{"-t", "-n", "-e4", "-W", "30", long}},
		{name: "number with a narrow page and columns", args: []string{"-t", "-n", "-2", "-w", "8", s10}},

		// Page ranges.
		{name: "page one", args: []string{"+1", f}},
		{name: "page two", args: []string{"+2", s200}},
		{name: "pages two to three", args: []string{"+2:3", s200}},
		{name: "page three only", args: []string{"+3:3", s200}},
		{name: "pages one to two", args: []string{"+1:2", s200}},
		{name: "page past the end warns", args: []string{"+5", s200}},
		{name: "page past the end of each file", args: []string{"+2", f, other}},
		{name: "page past the end of an empty file", args: []string{"+2", empty}},
		{name: "page past the end is not silenced by -r", args: []string{"-r", "+5", s200}},
		{name: "page range after an operand", args: []string{s200, "+2"}},
		{name: "page range with -t", args: []string{"+2", "-t", s200}},
		{name: "page range after the operand with -t", args: []string{"-t", s200, "+2"}},
		{name: "page range numbers from the input", args: []string{"+2", "-n", s200}},
		{name: "page range numbers from the page", args: []string{"+2", "-n", "-N", "1", s200}},
		{name: "long page range", args: []string{"--pages=1", f}},
		{name: "long page range to itself", args: []string{"--pages=1:1", f}},
		{name: "long page range with a plus", args: []string{"--pages=+1", f}},
		{name: "long page range with a space", args: []string{"--pages= 1", s200}},
		{name: "long page range to the maximum", args: []string{"--pages=1:18446744073709551615", f}},
		{name: "the last page range wins", args: []string{"--pages=1", "--pages=3", s200}},
		{name: "a long range outranks a plus", args: []string{"+1", "--pages=3", s200}},
		{name: "a plus after a long range is a file", args: []string{"--pages=3", "+1", s200}},
		{name: "a plus with a leading space", args: []string{"+ 1", s200}},
		{name: "a plus with two pluses", args: []string{"++1", s200}},
		{name: "a plus to a huge last page", args: []string{"+1:12345678901234567890", f}},

		// Page-range faults. A range that PARSES but is empty falls
		// through to being a file name for `+`, and is fatal for
		// --pages.
		{name: "page zero is a file", args: []string{"+0", f}},
		{name: "page zero to zero is a file", args: []string{"+0:0", f}},
		{name: "page zero to five is a file", args: []string{"+0:5", f}},
		{name: "a reversed range is a file", args: []string{"+2:1", f}},
		{name: "a range to zero is a file", args: []string{"+1:0", f}},
		{name: "a suffix on the first page is a file", args: []string{"+1x", f}},
		{name: "a b suffix is a file", args: []string{"+1b", f}},
		{name: "a K suffix is a file", args: []string{"+1K", f}},
		{name: "a trailing space is a file", args: []string{"+1 ", f}},
		{name: "a suffix before a colon is a file", args: []string{"+1x:2", f}},
		{name: "a plus with no number", args: []string{"+", f}},
		{name: "a plus with a word", args: []string{"+x", f}},
		{name: "a plus with a negative", args: []string{"+-1", f}},
		{name: "a plus with an empty last page", args: []string{"+1:", f}},
		{name: "a plus with an empty first page", args: []string{"+:2", f}},
		{name: "a suffix in the last page", args: []string{"+1:2x", f}},
		{name: "two colons in a plus", args: []string{"+1:2:3", f}},
		{name: "a plus past uintmax", args: []string{"+18446744073709551616", f}},
		{name: "a last page past uintmax", args: []string{"+1:18446744073709551616", f}},
		{name: "a first page below the page count", args: []string{"+12345678901234567890", f}},
		{name: "long range zero", args: []string{"--pages=0", f}},
		{name: "long range to zero", args: []string{"--pages=1:0", f}},
		{name: "long range with a word", args: []string{"--pages=x", f}},
		{name: "long range with an empty last page", args: []string{"--pages=1:", f}},
		{name: "long range empty", args: []string{"--pages=", f}},
		{name: "long range with a suffix", args: []string{"--pages=1b", f}},
		{name: "long range with a suffix before a colon", args: []string{"--pages=1x:2", f}},
		{name: "long range with a suffix in the last page", args: []string{"--pages=1:2x", f}},
		{name: "long range with two colons", args: []string{"--pages=1:2:3", f}},
		{name: "long range past uintmax", args: []string{"--pages=99999999999999999999", f}},
		{name: "long range takes its argument", args: []string{"--pages", "2", f}},
		{name: "long range needs an argument", args: []string{"--pages", f}},

		// Widths and truncation.
		{name: "page width with columns", args: []string{"-t", "-2", "-w", "20", s10}},
		{name: "page width always truncates", args: []string{"-t", "-2", "-W", "20", s10}},
		{name: "page width alone does not truncate", args: []string{"-t", "-w", "5", long}},
		{name: "page width truncates with -W", args: []string{"-t", "-W", "5", long}},
		{name: "-W value beats -w in either order", args: []string{"-t", "-2", "-w", "50", "-W", "40", "-w", "60", s10}},
		{name: "three columns truncate", args: []string{"-t", "-3", long}},
		{name: "three columns in twelve", args: []string{"-t", "-3", "-W", "12", long}},
		{name: "three columns in twelve with -w", args: []string{"-t", "-3", "-w", "12", long}},
		{name: "three columns in ten", args: []string{"-t", "-3", "-w", "10", long}},
		{name: "three columns in seven", args: []string{"-t", "-3", "-w", "7", long}},
		{name: "three columns in six", args: []string{"-t", "-3", "-w", "6", long}},
		{name: "three columns in five", args: []string{"-t", "-3", "-w", "5", long}},
		{name: "three columns in four is too narrow", args: []string{"-t", "-3", "-w", "4", long}},
		{name: "thirty-six columns fit", args: []string{"-t", "-36", s10}},
		{name: "thirty-seven columns do not", args: []string{"-t", "-37", s10}},
		{name: "the offset does not widen the columns", args: []string{"-t", "-37", "-o", "10", s10}},
		{name: "a hundred columns are too narrow", args: []string{"-100", "-t", s10}},
		{name: "two columns in two is too narrow", args: []string{"-t", "-2", "-w", "2", long}},
		{name: "-W one with two columns", args: []string{"-t", "-2", "-W", "1", long}},
		{name: "a very wide page", args: []string{"-w", "100000", "-2", "-t", s10}},
		{name: "join turns truncation off", args: []string{"-t", "-J", "-3", long}},
		{name: "join with one column", args: []string{"-t", "-J", s10}},
		{name: "join in parallel", args: []string{"-t", "-J", "-m", s10, f}},

		// Separators.
		{name: "separator with no argument", args: []string{"-t", "-2", "-s", s10}},
		{name: "separator colon", args: []string{"-t", "-2", "-s:", s10}},
		{name: "separator turns truncation off", args: []string{"-t", "-2", "-s", long}},
		{name: "separator with a width turns it on", args: []string{"-t", "-2", "-s", "-w", "20", s10}},
		{name: "separator with a width and a colon", args: []string{"-t", "-2", "-s:", "-w", "20", long}},
		{name: "separator with -W", args: []string{"-t", "-2", "-s", "-W", "20", s10}},
		{name: "a multi-byte separator", args: []string{"-t", "-2", "-sxy", s10}},
		{name: "a multi-byte separator in one column", args: []string{"-t", "-sxy", s10}},
		{name: "a multi-byte separator narrows the column", args: []string{"-t", "-2", "-sxy", "-w", "20", s10}},
		{name: "an empty long separator is a tab", args: []string{"--separator=", "-t", "-2", s10}},
		{name: "sep-string with no argument", args: []string{"-t", "-2", "-S", s10}},
		{name: "sep-string of two bytes", args: []string{"-t", "-2", "-SXY", s10}},
		{name: "sep-string of a colon", args: []string{"-t", "-2", "-S:", s10}},
		{name: "sep-string narrows the column", args: []string{"-t", "-3", "-S::", "-w", "20", long}},
		{name: "sep-string narrows the column further", args: []string{"-t", "-3", "-S::", "-w", "22", long}},
		{name: "an empty long sep-string", args: []string{"--sep-string=", "-t", "-2", s10}},
		{name: "sep-string takes no separate argument", args: []string{"-t", "-2", "-S", " ", s10}},
		{name: "join with a sep-string", args: []string{"-t", "-2", "-J", "-S:", tabbed}},
		{name: "join keeps its tab", args: []string{"-t", "-2", "-J", tabbed}},

		// Tabs in and out.
		{name: "input tabs pass through", args: []string{"-t", tabbed}},
		{name: "input tabs expand", args: []string{"-t", "-e", tabbed}},
		{name: "input tabs expand at four", args: []string{"-t", "-e4", tabbed}},
		{name: "input tabs with another char", args: []string{"-t", "-e.4", tabbed}},
		{name: "input tab char and width", args: []string{"-t", "-ex4", tabbed}},
		{name: "a real tab keeps its width of eight", args: []string{"-t", "-e:1", colon}},
		{name: "input tabs in two columns", args: []string{"-t", "-2", "-e", tabbed}},
		{name: "input tabs at four in two columns", args: []string{"-t", "-2", "-e4", tabbed}},
		{name: "output tabs", args: []string{"-t", "-2", "-i", s10}},
		{name: "output tab char", args: []string{"-t", "-2", "-i,", s10}},
		{name: "output tab char and width", args: []string{"-t", "-2", "-i.4", s10}},
		{name: "output tab dash", args: []string{"-t", "-2", "-i-4", s10}},
		{name: "output tab width only", args: []string{"-t", "-2", "-i4", s10}},
		{name: "output tab of a space", args: []string{"-t", "-2", "-i ", s10}},
		{name: "output tabs leave input spaces alone", args: []string{"-t", spaces}},
		{name: "output tabs compress input spaces", args: []string{"-t", "-i", spaces}},
		{name: "output tabs compress expanded tabs", args: []string{"-t", "-i", "-e", tabbed}},
		{name: "output tabs at four compress expanded tabs", args: []string{"-t", "-i.4", "-e", tabbed}},
		{name: "output tabs do not touch a real tab", args: []string{"-t", "-i.4", tabbed}},
		{name: "output tabs in two columns touch a real tab", args: []string{"-t", "-2", "-i.4", tabbed}},
		{name: "output tabs wider than the stop", args: []string{"-t", "-2", "-i.16", tabbed}},
		{name: "two tabs in a row", args: []string{"-t", wide2}},
		{name: "two tabs in a row truncated", args: []string{"-t", "-W", "3", wide2}},
		{name: "a tab is dropped when it does not fit", args: []string{"-t", "-W", "5", tabbed}},
		{name: "a tab that just fits", args: []string{"-t", "-W", "9", tabbed}},
		{name: "a tab that just fits with output tabs", args: []string{"-t", "-W", "9", "-i.4", tabbed}},
		{name: "a tab that just fits expanded", args: []string{"-t", "-W", "9", "-e4", tabbed}},
		{name: "the number separator is a real tab in one column", args: []string{"-t", "-n", "-i.4", tabbed}},
		{name: "the number separator with expanded tabs", args: []string{"-t", "-e", "-i.4", "-n", tabbed}},
		{name: "a letter number separator", args: []string{"-t", "-nS", tabbed}},
		{name: "a letter number separator in columns", args: []string{"-t", "-2", "-nS", s10}},
		{name: "numbers and output tabs in columns", args: []string{"-t", "-2", "-n", "-i.4", s10}},
		{name: "numbers, output tabs and input tabs", args: []string{"-t", "-2", "-n", "-i.4", "-e4", s10}},
		{name: "one column explicitly expands", args: []string{"-t", "-1", "-e", tabbed}},
		{name: "output tabs with a narrow page", args: []string{"-t", "-2", "-i.4", "-W", "12", tabbed}},

		// Double spacing.
		{name: "double space", args: []string{"-d", "-l", "14", s10}},
		{name: "double space with -t", args: []string{"-t", "-d", s10}},
		{name: "double space with -t and a length", args: []string{"-t", "-d", "-l", "14", s10}},
		{name: "double space in columns", args: []string{"-t", "-d", "-2", s10}},
		{name: "double space in columns paginated", args: []string{"-d", "-l", "16", "-2", s10}},
		{name: "double space with form feeds", args: []string{"-F", "-d", "-l", "14", s10}},
		{name: "double space in columns with form feeds", args: []string{"-F", "-d", "-2", "-l", "16", s10}},
		{name: "double space across", args: []string{"-t", "-d", "-a", "-2", s10}},
		{name: "double space in columns with -t and -F", args: []string{"-F", "-d", "-t", "-2", s10}},
		{name: "double space one line", args: []string{"-t", "-d", f}},

		// Control characters, and the display width they carry.
		{name: "control bytes pass through", args: []string{"-t", ctrl}},
		{name: "hat notation", args: []string{"-t", "-c", ctrl}},
		{name: "octal notation", args: []string{"-t", "-v", ctrl}},
		{name: "octal beats hat", args: []string{"-t", "-v", "-c", ctrl}},
		{name: "octal beats hat the other way", args: []string{"-t", "-c", "-v", ctrl}},
		{name: "hat notation in columns", args: []string{"-t", "-c", "-2", ctrl}},
		{name: "octal notation with numbers", args: []string{"-t", "-v", "-n", ctrl}},
		{name: "a backslash is not escaped", args: []string{"-t", "-v", bs}},
		{name: "a backslash is not escaped with -c", args: []string{"-t", "-c", bs}},
		{name: "a carriage return passes through", args: []string{"-t", bs}},
		{name: "escapes count for truncation", args: []string{"-t", "-2", "-c", "-W", "6", ctrl}},
		{name: "raw bytes count zero for truncation", args: []string{"-t", "-2", "-W", "6", ctrl}},
		{name: "raw bytes count zero with -e", args: []string{"-t", "-2", "-e", "-W", "6", ctrl}},
		{name: "a backspace is minus one column", args: []string{"-t", "-W", "6", backsp}},
		{name: "a backspace under -c", args: []string{"-t", "-c", "-W", "6", backsp}},
		{name: "a backspace under -v", args: []string{"-t", "-v", "-W", "6", backsp}},
		{name: "an escape widens the truncation", args: []string{"-t", "-W", "10", "-v", wctl}},
		{name: "a raw byte does not", args: []string{"-t", "-W", "10", wctl}},
		{name: "escapes move the tab stop", args: []string{"-t", "-c", "-e", del}},
		{name: "escapes without -e", args: []string{"-t", "-c", del}},
		{name: "octal escapes and expanded tabs", args: []string{"-t", "-v", "-e", ctrl}},

		// The -b option is accepted, undocumented and does nothing.
		{name: "balance is a no-op", args: []string{"-b", "-t", f}},
		{name: "balance twice", args: []string{"-b", "-b", "-t", f}},
		{name: "balance glued to digits", args: []string{"-2b", "-t", s10}},
		{name: "balance with a short page", args: []string{"-b", "-3", "-l", "16", "-t", s10}},

		// Operands.
		{name: "missing file", args: []string{missing}},
		{name: "missing file quietly", args: []string{"-r", missing}},
		{name: "missing then good", args: []string{missing, f}},
		{name: "missing then good quietly", args: []string{"-r", missing, f}},
		{name: "a directory is fatal", args: []string{d}},
		{name: "a directory is fatal even with -r", args: []string{"-r", d}},
		{name: "a directory before a file", args: []string{"-t", d, f}},
		{name: "a directory after a file", args: []string{"-t", f, d}},
		{name: "a broken symlink", args: []string{"-t", blink}},
		{name: "an empty operand", args: []string{"-t", ""}},
		{name: "an empty operand paginated", args: []string{""}},
		{name: "an operand that is not valid UTF-8", args: []string{"-t", raw}},
		{name: "a name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "dev null", args: []string{"-t", "/dev/null"}},
		{name: "dev null paginated", args: []string{"/dev/null"}},
		{name: "an option after an operand", args: []string{f, "-2"}},
		{name: "dashdash makes an option a file", args: []string{"-t", "--", "-2", s10}},
		{name: "dashdash makes a plus a file", args: []string{"-t", "--", "+1", s10}},
		{name: "dashdash twice", args: []string{"-t", "--", "--", s10}},

		// getopt.
		{name: "invalid short option", args: []string{"-x", f}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "ambiguous p", args: []string{"--p"}},
		{name: "ambiguous pag", args: []string{"--pag"}},
		{name: "ambiguous s", args: []string{"--s"}},
		{name: "ambiguous se", args: []string{"--se"}},
		{name: "ambiguous sep", args: []string{"--sep"}},
		{name: "ambiguous o", args: []string{"--o"}},
		{name: "ambiguous f", args: []string{"--f"}},
		{name: "ambiguous d", args: []string{"--d"}},
		{name: "ambiguous n", args: []string{"--n"}},
		{name: "ambiguous h", args: []string{"--h"}},
		{name: "ambiguous he", args: []string{"--he"}},
		{name: "t is no long option", args: []string{"--t"}},
		{name: "unique prefix across", args: []string{"--a", "-2", "-t", s10}},
		{name: "unique prefix columns", args: []string{"--c=2", "-t", s10}},
		{name: "unique prefix expand", args: []string{"--e", "-t", tabbed}},
		{name: "unique prefix join", args: []string{"--j", "-t", "-2", tabbed}},
		{name: "unique prefix merge", args: []string{"--m", "-t", f, other}},
		{name: "unique prefix length", args: []string{"--l=12", ff}},
		{name: "unique prefix width", args: []string{"--w=20", "-t", "-2", s10}},
		{name: "unique prefix indent", args: []string{"--i=5", "-t", f}},
		{name: "short option needs an argument", args: []string{"-h"}},
		{name: "date needs an argument", args: []string{"-D"}},
		{name: "length needs an argument", args: []string{"-l"}},
		{name: "first line needs an argument", args: []string{"-N"}},
		{name: "indent needs an argument", args: []string{"-o"}},
		{name: "long header needs an argument", args: []string{"--header"}},
		{name: "long length needs an argument", args: []string{"--length"}},
		{name: "long columns needs an argument", args: []string{"--columns"}},
		{name: "an optional argument is never separate", args: []string{"-t", "-n", "3", s10}},
		{name: "an optional expand argument is never separate", args: []string{"-t", "-e", "2", s10}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "a bad option beats the help", args: []string{"-x", "--help"}},
		{name: "POSIXLY_CORRECT does not end the scan", args: []string{"-t", s10, "-2"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "_POSIX2_VERSION changes nothing", args: []string{"-t", s10, "-2"}, env: []string{"_POSIX2_VERSION=199209"}},
		{name: "COLUMNS and LINES are not read", args: []string{f}, env: []string{"COLUMNS=40", "LINES=20"}},

		// Numeric option faults: one line, no `Try …`.
		{name: "width is not a number", args: []string{"-w", "f"}},
		{name: "width zero", args: []string{"-w", "0", f}},
		{name: "width negative", args: []string{"-w", "-1", f}},
		{name: "width is a word", args: []string{"-w", "x", f}},
		{name: "page width zero", args: []string{"-W", "0", f}},
		{name: "length zero", args: []string{"-l", "0", f}},
		{name: "length is a word", args: []string{"-l", "x", f}},
		{name: "length negative", args: []string{"-l", "-1", f}},
		{name: "indent is a word", args: []string{"-o", "x", f}},
		{name: "indent negative", args: []string{"-o", "-1", f}},
		{name: "first line is a word", args: []string{"-N", "x", f}},
		{name: "length at the ERANGE floor", args: []string{"-l", "-1073741824", f}},
		{name: "length one past the ERANGE floor", args: []string{"-l", "-1073741825", f}},
		{name: "indent at the ERANGE floor", args: []string{"-o", "-1073741824", f}},
		{name: "indent one past the ERANGE floor", args: []string{"-o", "-1073741825", f}},
		{name: "width at the ERANGE floor", args: []string{"-w", "-1073741824", f}},
		{name: "width one past the ERANGE floor", args: []string{"-w", "-1073741825", f}},
		{name: "page width at the ERANGE floor", args: []string{"-W", "-1073741824", f}},
		{name: "page width one past the ERANGE floor", args: []string{"-W", "-1073741825", f}},
		{name: "length at INT_MAX", args: []string{"-l", "2147483647", empty}},
		{name: "length one past INT_MAX", args: []string{"-l", "2147483648", f}},
		{name: "length far past INT_MAX", args: []string{"-l", "4294967296", f}},
		{name: "first line past INT_MAX", args: []string{"-N", "2147483648", f}},
		{name: "first line past INT_MIN", args: []string{"-N", "-2147483649", f}},
		{name: "first line ten digits", args: []string{"-N", "9999999999", f}},
		{name: "width past every type", args: []string{"-w", "99999999999999999999", f}},
		{name: "long length is named by its short spelling", args: []string{"--length=0", f}},
		{name: "long width is named by its short spelling", args: []string{"--width=0", f}},
		{name: "zero columns", args: []string{"-0", f}},
		{name: "two zero digits", args: []string{"-00", f}},
		{name: "long columns zero", args: []string{"--columns=0", f}},
		{name: "long columns is a word", args: []string{"--columns=abc", f}},
		{name: "long columns past every type", args: []string{"--columns=99999999999999999999", f}},
		{name: "columns past uintmax", args: []string{"-99999999999999999999", f}},
		{name: "columns past INT_MAX", args: []string{"-4294967296", f}},

		// The CHAR / WIDTH grammar of -e, -i and -n.
		{name: "expand width zero", args: []string{"-e0", f}},
		{name: "expand width one", args: []string{"-e1", f}},
		{name: "output tab width zero", args: []string{"-i0", f}},
		{name: "expand char and a bad width", args: []string{"-eab", f}},
		{name: "long expand is named by its short spelling", args: []string{"--expand-tabs=0", f}},
		{name: "long output tabs is named by its short spelling", args: []string{"--output-tabs=0", f}},
		{name: "long number lines is named by its short spelling", args: []string{"--number-lines=0", f}},
		{name: "number digits zero", args: []string{"-t", "-n:0", s10}},
		{name: "number digits is a word", args: []string{"-t", "-n:x", s10}},
		{name: "number separator and a colon width", args: []string{"-t", "-n::", s10}},
		{name: "number separator and a letter width", args: []string{"-t", "-nxx", s10}},
		{name: "number digits with a suffix", args: []string{"-t", "-n:5x", s10}},
		{name: "number width with a suffix", args: []string{"-t", "-n5x", s10}},
		{name: "expand char and a word width", args: []string{"-t", "-e:x", tabbed}},
		{name: "expand width with a suffix", args: []string{"-t", "-e4x", tabbed}},
		{name: "output tab char and a word width", args: []string{"-t", "-i:x", s10}},

		// The checks that follow the scan, in GNU's order.
		{name: "columns in parallel", args: []string{"-m", "-2", f}},
		{name: "columns in parallel the other way", args: []string{"-2", "-m", f}},
		{name: "across in parallel", args: []string{"-m", "-a", f, other}},
		{name: "across in parallel the other way", args: []string{"-a", "-m", f, other}},
		{name: "the column count is checked first", args: []string{"-m", "-a", "-0", f, other}},
		{name: "parallel is checked before the width", args: []string{"-m", "-2", "-w", "1", f, other}},
		{name: "the column value is checked after the scan", args: []string{"-0", "-x", f}},
		{name: "an option value is checked during the scan", args: []string{"-l", "0", "-x", f}},
		{name: "a bad option beats an option value", args: []string{"-x", "-l", "0", f}},

		// Write failures.
		{name: "stdout closed", args: []string{"-t", f}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"-t", f}, stdout: stdoutFull},
		{name: "stdout closed with a page", args: []string{f}, stdout: stdoutClosed},
		{name: "stdout full with a page", args: []string{f}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-t", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-t", big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},

		// Bulk.
		{name: "a large file", args: []string{big}},
		{name: "a large file in columns", args: []string{"-4", big}},
		{name: "a large file numbered", args: []string{"-n", big}},
		{name: "a large file with form feeds", args: []string{"-F", big}},
		{name: "a line longer than one read block", args: []string{"-t", "-W", "40"}, stdin: strings.Repeat("z", 70000) + "\n"},
		{name: "many lines from a pipe", args: []string{"-t", "-3"}, stdin: seqLines(20000)},
	}

	// Standard input and -m have no file to take a date from, so their
	// header carries the WALL CLOCK: every case here pins it with a -D
	// format that holds no time, or drops the header with -t.
	cases = append(cases,
		invocation{name: "stdin with -t", args: []string{"-t"}, stdin: tabs},
		invocation{name: "stdin named", args: []string{"-t", "-"}, stdin: tabs},
		invocation{name: "stdin twice is one stream", args: []string{"-t", "-", "-"}, stdin: "x\ny\n"},
		invocation{name: "stdin twice in parallel", args: []string{"-t", "-m", "-", "-"}, stdin: tabs},
		invocation{name: "stdin and a file in parallel", args: []string{"-t", "-m", "-", f}, stdin: tabs},
		invocation{name: "stdin has an empty name", args: []string{"-D", "X"}, stdin: "a\n"},
		invocation{name: "stdin named has an empty name", args: []string{"-D", "X", "-"}, stdin: "a\n"},
		invocation{name: "a file then stdin", args: []string{"-D", "X", f, "-"}, stdin: "q\n"},
		invocation{name: "parallel has an empty name", args: []string{"-D", "X", "-m", f, other}},
		invocation{name: "parallel with no operand reads stdin", args: []string{"-D", "X", "-m"}, stdin: "a\nb\n"},
		invocation{name: "stdin with a page range", args: []string{"-D", "X", "+2"}, stdin: seqLines(200)},
		invocation{name: "dashdash alone reads stdin", args: []string{"-t", "--"}, stdin: "a\n"},
		invocation{name: "stdin in two columns", args: []string{"-t", "-2", "-"}, stdin: tabs},
		invocation{name: "stdin spaces compress in columns", args: []string{"-t", "-2", "-"}, stdin: "        x\ny\n"},
		invocation{name: "stdin spaces stay in one column", args: []string{"-t", "-"}, stdin: "        x\ny\n"},
		invocation{name: "stdin tabs in two columns", args: []string{"-t", "-2", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs across", args: []string{"-t", "-2", "-a", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs joined", args: []string{"-t", "-2", "-J", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with a separator", args: []string{"-t", "-2", "-s:", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with a tab separator", args: []string{"-t", "-2", "-s\t", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with a sep-string tab", args: []string{"-t", "-2", "-S\t", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs joined with a sep-string tab", args: []string{"-t", "-2", "-J", "-S\t", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with two tab separators", args: []string{"-t", "-2", "-s\t\t", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with a long tab separator", args: []string{"-t", "-2", "--separator=\t", "-i.4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs numbered", args: []string{"-t", "-2", "-n", "-"}, stdin: tabs},
		invocation{name: "stdin tabs numbered with a separator", args: []string{"-t", "-2", "-n:3", "-"}, stdin: tabs},
		invocation{name: "stdin tabs numbered in one column", args: []string{"-t", "-n", "-"}, stdin: tabs},
		invocation{name: "stdin tabs numbered and expanded", args: []string{"-t", "-2", "-n", "-e4", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with an offset", args: []string{"-t", "-2", "-o", "3", "-"}, stdin: tabs},
		invocation{name: "stdin tabs with a two-byte sep-string", args: []string{"-t", "-2", "-S::", "-"}, stdin: tabs},
		invocation{name: "stdin expanded tab stops after a backspace", args: []string{"-t", "-e", "-"}, stdin: "ab\bc\tz\n"},
		invocation{name: "stdin expanded tab stops after a control byte", args: []string{"-t", "-e", "-"}, stdin: "ab\x01c\tz\n"},
		invocation{name: "stdin expanded tab stops plain", args: []string{"-t", "-e", "-"}, stdin: "abc\tz\n"},
		invocation{name: "stdin empty", args: []string{"-t", "-"}},
		invocation{name: "stdin empty paginated", args: []string{"-D", "X", "-"}},
		invocation{name: "stdin trailing form feed with -t", args: []string{"-t", "-"}, stdin: "a\n\f"},
		invocation{name: "stdin trailing form feed", args: []string{"-D", "X", "-"}, stdin: "a\n\f"},
		invocation{name: "stdin two leading form feeds", args: []string{"-D", "X", "-"}, stdin: "\f\fa\n"},
		invocation{name: "stdin two leading form feeds with -t", args: []string{"-t", "-"}, stdin: "\f\fa\n"},
		invocation{name: "stdin two leading form feeds with -T", args: []string{"-T", "-"}, stdin: "\f\fa\n"},
		invocation{name: "stdin form feeds with a short page", args: []string{"-D", "X", "-l", "12", "-"}, stdin: "one\ntwo\n\fthree\nfour\n\f\ffive\n"},
	)

	// A separator holding a TAB, which is where GNU's three kinds of
	// separator byte separate: a space always joins the gap, a tab
	// joins it only as the WHOLE separator of a padded layout, and
	// everything else is written as it stands.
	for _, sep := range []string{"-S\t", "-s\t", "-s\t\t", "-S\t\t", "-S ", "-S:", "-s:",
		"-S", "-s", "-Sx\ty", "-S \t", "-S\t ", "-s \t", "-S\t\t\t", "-Sxy"} {
		for _, extra := range [][]string{nil, {"-i.4"}, {"-J"}, {"-w", "20"}, {"-W", "20"}, {"-i,"}, {"-e"}} {
			for _, shape := range [][]string{{"-2"}, {"-3"}} {
				args := append([]string{"-t"}, shape...)
				args = append(args, sep)
				args = append(args, extra...)
				cases = append(cases, invocation{
					name:  "separator " + quote([]byte(sep)) + " " + strings.Join(append(shape, extra...), " "),
					args:  append(args, "-"),
					stdin: tabs,
				})
			}
			args := append([]string{"-t", "-m", sep}, extra...)
			cases = append(cases, invocation{
				name:  "parallel separator " + quote([]byte(sep)) + " " + strings.Join(extra, " "),
				args:  append(args, "-", "-"),
				stdin: tabs,
			})
		}
	}

	// -D is gnulib's nstrftime, and the header date is the FILE's
	// mtime, so each of these is a fixed instant in a fixed zone.
	formats := []string{
		"%a", "%A", "%b", "%B", "%c", "%C", "%d", "%D", "%e", "%F", "%g", "%G", "%h",
		"%H", "%I", "%j", "%k", "%l", "%m", "%M", "%n", "%N", "%p", "%P", "%q", "%r",
		"%R", "%s", "%S", "%t", "%T", "%u", "%U", "%V", "%w", "%W", "%x", "%X", "%y",
		"%Y", "%z", "%:z", "%::z", "%:::z", "%Z", "%%", "%f", "%Q", "%", "%-d", "%_d",
		"%0e", "%^a", "%#a", "%#Z", "%10Y", "%Ey", "%Od", "%-j", "%_j", "%03d", "%1N",
		"%3N", "%9N", "%12N", "%20N", "%0N", "%Y-%m-%d %H:%M", "%s.%N", "x%Zy", "%:",
		"%::", "%:q", "%E", "%O", "%-", "%_", "%2", "%5S", "%_5S", "%05S", "%^B",
		"%^p", "%-C", "%G-W%V-%u",
	}
	for _, spec := range formats {
		cases = append(cases, invocation{
			name: "date format " + quote([]byte(spec)),
			args: []string{"-D", spec, f},
		})
	}
	// The conversions that read the INSTANT rather than the format get
	// four more of them: the epoch itself, a date before it, one past
	// every transition table, and one carrying nanoseconds.
	for _, spec := range []string{
		"%F %T", "%s", "%j", "%N", "%s.%N", "%G-W%V-%u", "%U %W", "%C %y",
		"%a %A %b %B", "%c", "%x %X", "%Z %z", "%q", "%D %e %k %l %p %r",
	} {
		for _, file := range []string{epoch, pre70, y2100, nsec} {
			cases = append(cases, invocation{
				name: "date format " + quote([]byte(spec)) + " of " + filepath.Base(file),
				args: []string{"-D", spec, file},
			})
		}
	}

	// The zone is $TZ, read by `coreutils/lib/tz.fern` as glibc reads
	// it: a TZif file under /usr/share/zoneinfo, a POSIX string when
	// that is not one, and the footer of the file past its last
	// transition — which is what decides a 2038 or 2100 date. `%Z` is
	// the abbreviation that comes with the offset, so these cases are
	// the zone's DESIGNATION table as much as its transitions.
	//
	// Two TZ shapes are deliberately absent. One is a string whose
	// DAYLIGHT name is one or two characters ("ABC1x"): glibc leaves
	// the rule it never parsed unset, and the answer that falls out is
	// standard time at exactly the two instants 0 and 1 and daylight
	// time with an empty abbreviation everywhere else — including
	// 2020 — which no rule in the POSIX grammar can produce. The other
	// is a daylight name with NO dates after it ("ABC1DEF"), which is
	// the posixrules divergence docs/COREUTILS.md already records for
	// who(1).
	zones := []string{
		"UTC", "America/New_York", "Asia/Tokyo", "Europe/London", "Australia/Lord_Howe",
		"Asia/Kathmandu", "Pacific/Chatham", "America/Sao_Paulo", "Africa/Cairo",
		"Etc/GMT+5", "Europe/Dublin", "Asia/Kolkata",
		"EST5EDT,M3.2.0,M11.1.0", "UTC0", "GMT0", "EST5", "<+07>-7", "PST8PDT",
		"CET-1CEST,M3.5.0,M10.5.0/3", "NZST-12NZDT,M9.5.0,M4.1.0/3",
		"AEST-10AEDT,M10.1.0,M4.1.0/3", "XXX3:30", "ABC-5:45",
		"IST-2IDT,M3.4.4/26,M10.5.0", "EST5EDT,J60,J300", "EST5EDT,60,300",
		":UTC", ":/etc/localtime", "/usr/share/zoneinfo/Asia/Tokyo",
		"", "Nowhere/Nothing", "garbage", "X", "XY", "ABC", "A/B/C", "abc",
		"garbage1", "ABC+", "<ABC>", "Etc/Nope",
		// tzset tries a FILE before a rule, and the spellings that
		// decide which: a lone colon is the default file, a colon
		// before a name is that name, and a relative name hangs under
		// the zoneinfo directory however it is written.
		":", "::", ":Asia/Tokyo", "Asia//Tokyo", "./UTC", "../zoneinfo/UTC",
		"Asia/Tokyo/", "/", "//", "/nonexistent", " UTC", "UTC ", "utc",
		"<+07>-7<+08>,M3.2.0,M11.1.0", "AAA+25", "AAA+25:59:59",
		"AAA1BBB,M1.1.0,M12.5.0",
	}
	for _, zone := range zones {
		for _, file := range []string{f, y2100, pre70, epoch} {
			cases = append(cases, invocation{
				name: "zone " + quote([]byte(zone)) + " of " + filepath.Base(file),
				args: []string{"-D", "%F %T %Z %z %a %j", file},
				env:  []string{"TZ=" + zone},
			})
		}
	}

	return cases
}

func TestPrParity(t *testing.T) {
	requireParity(t, "pr", prCases(t))
}

func TestPrHelpVersion(t *testing.T) {
	requireHelp(t, "pr", []string{"--help"}, 0)
	requireHelp(t, "pr", []string{"--hel"}, 0)
	requireVersion(t, "pr", []string{"--version"}, 0)
	requireVersion(t, "pr", []string{"--vers"}, 0)
	// --help is honoured inside the scan, so an operand and a later
	// option do not get in its way, and the column count is validated
	// only after it.
	requireHelp(t, "pr", []string{"--help", "-x"}, 0)
	requireHelp(t, "pr", []string{"-0", "--help"}, 0)
	requireVersion(t, "pr", []string{"--version", "-x"}, 0)
}
