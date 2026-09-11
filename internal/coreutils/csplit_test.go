package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// csplitSeed fills a case's working directory with the inputs the corpus
// names. Every case gets its own directory per SIDE, so the pieces the
// two implementations write are compared without either seeing the
// other's; the inputs live in it too, and are named relatively, which is
// what makes the tree comparison meaningful.
func csplitSeed(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"ten":   "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n",
		"aab":   "a\na\nb\na\n",
		"abab":  "a\nb\na\nb\na\nb\n",
		"nonl":  "a\nb\nc",
		"empty": "",
		// A byte sequence that is not valid UTF-8, so an operand and a
		// line both carry one.
		"raw": "a\n\xff\xfe\nb\n",
		// Past one read block in both directions, so the piece boundary
		// and the buffer's own compaction cross a chunk.
		"big": strings.Repeat("line\n", 40000) + "MARK\n" + strings.Repeat("line\n", 40000),
		// A line whose only match begins past its first byte, with more
		// than one byte before it that no match can begin at.
		"mid": "xa1\nzz\nq\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// csplitCases is csplit(1)'s corpus.
//
// Almost all of it is about the two cursors: where a piece ends and
// where the next pattern starts looking are different lines, and every
// offset spelling moves them by different amounts. The cases that pin
// that are the `{*}` ones — a repeat that got either cursor wrong either
// loops forever or stops one piece early — and the ones that follow a
// pattern with a second pattern aimed at a line the first passed.
//
// The other half is which error path a failure takes, because each of
// the three leaves a different piece behind: a match not found and a
// line past the end flush the rest of the input into the open piece, an
// offset reaching back past the piece's first line does not, and an
// input with nothing left at all reports `input disappeared` and keeps
// the files rather than removing them.
func csplitCases(t *testing.T) []invocation {
	c := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, seedTree: csplitSeed}
	}
	cases := []invocation{
		// The line-number pattern.
		c("line number", "ten", "5"),
		c("line one", "ten", "1"),
		c("last line", "ten", "10"),
		c("past the end", "ten", "11"),
		c("well past the end", "ten", "12"),
		c("zero", "ten", "0"),
		c("leading zeros", "ten", "011"),
		c("more leading zeros", "ten", "0011"),
		c("leading blank", "ten", " 5"),
		c("trailing blank", "ten", "5 "),
		c("plus", "ten", "+5"),
		c("plus zero", "ten", "+0"),
		c("two zeros", "ten", "00"),
		c("a minus is an option", "ten", "-5"),
		c("not a number", "ten", "x"),
		c("past intmax", "ten", "99999999999999999999"),
		c("several", "ten", "3", "5", "8"),
		c("descending", "ten", "5", "3"),
		c("descending after a third", "ten", "3", "5", "2"),
		c("descending with zeros", "ten", "5", "003"),
		c("repeated line number", "ten", "1", "1"),
		c("repeated line number with zeros", "ten", "5", "05"),
		c("repeated mid file", "ten", "5", "5"),
		c("a line number after a regexp", "ten", "/5/", "3"),
		c("a line number the scan passed", "ten", "/2/", "5", "/4/"),
		c("a line number ahead of the scan", "ten", "/2/", "5", "/6/"),
		c("a line number after a skip", "ten", "%2%", "5", "/4/"),
		c("a line number after an ignored skip past the end", "ten", "%10%+1", "12"),
		c("the break line must exist", "ten", "10", "11"),
		c("three line numbers, the last past the end", "ten", "9", "10", "11"),
		c("an unterminated last line counts", "nonl", "3"),
		c("past an unterminated last line", "nonl", "4"),
		c("nothing left to read", "ten", "/10/+1", "12"),
		c("nothing left and the line is the next one", "ten", "/10/+1", "11"),
		c("nothing left after an unterminated file", "nonl", "/c/+1", "4"),

		// An empty input.
		c("empty input", "empty", "1"),
		c("empty input, line two", "empty", "2"),
		c("empty input, line five", "empty", "5"),
		c("empty input twice", "empty", "1", "1"),
		c("empty input quiet", "-s", "empty", "1"),
		c("empty input kept", "-k", "empty", "1"),
		c("empty input elided", "-z", "empty", "1"),
		c("empty input regexp", "empty", "/x/"),
		c("empty input skip", "empty", "%x%"),
		c("empty input regexp elided", "-z", "empty", "/x/"),

		// The regexp pattern.
		c("regexp", "ten", "/5/"),
		c("regexp first line", "ten", "/1/"),
		c("regexp last line", "ten", "/10/"),
		c("regexp no match", "ten", "/nope/"),
		c("empty regexp", "ten", "//"),
		c("anchored regexp", "ten", "/^5$/"),
		c("regexp end anchor only", "ten", "/^$/"),
		c("two regexps", "ten", "/5/", "/8/"),
		c("a bare star is a literal", "ten", "/*/"),
		c("alternation", "abab", `/a\|b/`),
		c("plus", "abab", `/a\+/`),
		c("question", "abab", `/a\?/`),
		c("interval", "ten", `/1\{1,2\}/`),
		c("backreference", "abab", `/\(a\)\1/`),
		c("case matters", "abab", "/A/"),

		// The scan seeds a match at every position of the line, so a
		// line whose first bytes can start none of them still has to be
		// searched to its end.
		c("a match past the first byte", "mid", "/[0-9]/"),
		c("a negated class past the first byte", "mid", "/[^xa]/"),
		c("a class the whole line fails", "mid", "/[A-Z]/"),
		c("the last delimiter closes", "ten", "/a/b/"),
		c("the last delimiter closes a digit", "ten", "/5/5/"),
		c("the last percent closes", "ten", "%5%5%"),
		c("no closing delimiter", "ten", "/5"),
		c("an unknown delimiter is a line number", "ten", "@5@"),
		c("bad regexp", "ten", "/[/"),
		c("unmatched group", "ten", `/\(/`),
		c("bad interval", "ten", `/a\{2,1\}/`),
		c("a bad regexp beats a bad offset", "ten", "/[/x"),

		// Offsets.
		c("positive offset", "ten", "/5/2"),
		c("signed positive offset", "ten", "/5/+2"),
		c("zero offset", "ten", "/5/0"),
		c("minus zero offset", "ten", "/5/-0"),
		c("negative offset", "ten", "/5/-2"),
		c("negative offset to line one", "ten", "/5/-4"),
		c("negative offset to line zero", "ten", "/5/-5"),
		c("negative offset below line zero", "ten", "/5/-6"),
		c("negative offset far below", "ten", "/5/-9"),
		c("negative offset far below, kept", "-k", "ten", "/5/-9"),
		c("negative offset far below with a pattern after it", "ten", "/5/-9", "/8/"),
		c("negative offset before the piece", "ten", "/3/", "/8/-6"),
		c("negative offset to the piece's first line", "ten", "/3/", "/8/-5"),
		c("negative offset before an ignored piece", "ten", "%3%", "/8/-6"),
		c("offset past the end", "ten", "/10/+5"),
		c("offset to the line after the last", "ten", "/10/+1"),
		c("offset to the last line", "ten", "/9/+1"),
		c("offset one past the last line", "ten", "/9/+2"),
		c("blank before the offset", "ten", "/5/ 2"),
		c("offset with trailing junk", "ten", "/5/+2x"),
		c("offset that is not a number", "ten", "/5/x"),
		c("a bare sign is not an offset", "ten", "/5/+"),
		c("offset past intmax", "ten", "/5/99999999999999999999"),
		c("the scan resumes past a negative offset", "ten", "/5/-2", "/3/"),
		c("the scan resumes past a negative offset, second line", "ten", "/5/-2", "/4/"),
		c("the scan resumes past a positive offset", "ten", "/5/+3", "/6/"),
		c("a pattern after a positive offset", "ten", "/5/+3", "/9/"),
		c("a line number the regexp already passed", "ten", "/5/", "3", "/5/"),
		c("a regexp after a passed line number", "ten", "/5/", "3", "/7/"),
		c("a regexp on the line a count stopped at", "ten", "5", "/5/"),
		c("a regexp after the line a count stopped at", "ten", "5", "/6/"),
		c("a regexp before the line a count stopped at", "ten", "5", "/3/"),
		c("a skip before the line a count stopped at", "ten", "5", "%3%"),

		// The %…% spelling throws its piece away.
		c("skip", "ten", "%5%"),
		c("skip with an offset", "ten", "%5%+2"),
		c("skip with a negative offset", "ten", "%5%-2"),
		c("skip with a negative offset past the start", "ten", "%5%-9"),
		c("skip no match", "ten", "%nope%"),
		c("a skip after a regexp", "ten", "/5/", "%8%"),

		// Repeats.
		c("repeat a regexp", "ten", "/5/", "{5}"),
		c("repeat a regexp once", "ten", "/5/", "{1}"),
		c("repeat a regexp forever", "ten", "/5/", "{*}"),
		c("repeat a matching regexp forever", "ten", "/[0-9]/", "{*}"),
		c("repeat with an offset forever", "ten", "/5/+1", "{*}"),
		c("repeat with a negative offset forever", "ten", "/5/-2", "{*}"),
		c("repeat an early match forever", "ten", "/2/+2", "{*}"),
		c("repeat a skip forever", "ten", "%5%", "{*}"),
		c("repeat a line number", "ten", "3", "{1}"),
		c("repeat a line number twice", "ten", "3", "{2}"),
		c("repeat a line number none", "ten", "5", "{0}"),
		c("repeat a line number forever", "ten", "3", "{*}"),
		c("repeat the last line number forever", "ten", "5", "{*}"),
		c("repeat a line number that reaches the end forever", "ten", "10", "{*}"),
		c("repeat a line number past the end", "ten", "11", "{*}"),
		c("repeat a line number forever elided", "-z", "ten", "3", "{*}"),
		c("repeat a line number forever kept", "-k", "ten", "3", "{*}"),
		c("repeat after two line numbers", "ten", "2", "3", "{1}"),
		c("repeat after two line numbers forever", "ten", "2", "3", "{*}"),
		c("a pattern after an exhausting repeat", "ten", "/5/", "{*}", "3"),
		c("a repeat with no pattern", "ten", "{3}"),
		c("two repeats", "ten", "5", "{2}", "{3}"),
		c("repeat without a brace", "ten", "5", "{3"),
		c("a bare brace", "ten", "5", "{"),
		c("an empty repeat", "ten", "5", "{}"),
		c("a star without a brace", "ten", "5", "{*"),
		c("two stars", "ten", "5", "{**}"),
		c("a spaced repeat", "ten", "5", "{ 3 }"),
		c("a negative repeat", "ten", "5", "{-1}"),
		c("a repeat that is not a number", "ten", "5", "{x}"),
		c("a repeat past intmax", "ten", "5", "{99999999999999999999}"),
		c("the offset cursor on a repeat", "aab", "/a/+2", "{*}"),
		c("the offset cursor without a repeat", "aab", "/a/+2"),
		c("a negative offset on a repeat", "abab", "/b/-1", "{*}"),
		c("a positive offset on a repeat", "abab", "/a/+2", "{*}"),
		c("a zero offset on a repeat", "abab", "/a/", "{*}"),
		c("a skipped positive offset on a repeat", "abab", "%a%+2", "{*}"),
		c("a skipped negative offset on a repeat", "abab", "%b%-1", "{*}"),

		// -f, -n, -b: the piece names.
		c("prefix", "-f", "yy", "ten", "5"),
		c("long prefix", "--prefix=yy", "ten", "5"),
		c("empty prefix", "-f", "", "ten", "5"),
		c("prefix naming a missing directory", "-f", "sub/", "ten", "5"),
		c("prefix naming a directory", "-f", "d/", "ten", "5"),
		c("digits", "-n", "3", "ten", "5"),
		c("long digits", "--digits=3", "ten", "5"),
		c("no digits", "-n", "0", "ten", "5"),
		c("one digit past ten pieces", "-n", "1", "ten", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10"),
		c("twenty digits", "-n", "20", "ten", "5"),
		c("digits blank", "-n", "", "ten", "5"),
		c("digits with a leading blank", "-n", " 3", "ten", "5"),
		c("digits signed", "-n", "+3", "ten", "5"),
		c("digits with junk", "-n", "3x", "ten", "5"),
		c("digits negative", "-n", "-1", "ten", "5"),
		c("digits negative with a blank", "-n", " -1", "ten", "5"),
		c("digits past intmax", "-n", "99999999999999999999", "ten", "5"),
		c("digits negative past intmax", "-n", "-99999999999999999999", "ten", "5"),
		c("digits past INT_MAX", "-n", "2147483648", "ten", "5"),
		c("suffix format", "-b", "%03d", "ten", "5"),
		c("long suffix format", "--suffix-format=%03d", "ten", "5"),
		c("suffix format overrides digits", "-n", "5", "-b", "%d", "ten", "5"),
		c("digits after the suffix format", "-b", "%d", "-n", "5", "ten", "5"),
		c("only the last suffix format is checked", "-b", "%s", "-b", "%d", "ten", "5"),
		c("suffix d", "-b", "%d", "ten", "5"),
		c("suffix i", "-b", "%i", "ten", "5"),
		c("suffix u", "-b", "%u", "ten", "5"),
		c("suffix o", "-b", "%o", "ten", "5"),
		c("suffix x", "-b", "%x", "ten", "5"),
		c("suffix X", "-b", "%X", "ten", "5"),
		c("suffix width and precision", "-b", "%5.3d", "ten", "5"),
		c("suffix left justified", "-b", "%-5d", "ten", "5"),
		c("suffix zero flag", "-b", "%0d", "ten", "5"),
		c("suffix minus and zero", "-b", "%-0d", "ten", "5"),
		c("suffix alt octal", "-b", "%#o", "ten", "5"),
		c("suffix alt hex", "-b", "%#x", "ten", "5"),
		c("suffix alt HEX", "-b", "%#X", "ten", "5"),
		c("suffix alt hex with precision", "-b", "%#5.3x", "ten", "5"),
		c("suffix grouping", "-b", "%'d", "ten", "5"),
		c("suffix grouping u", "-b", "%'u", "ten", "5"),
		c("suffix grouping i", "-b", "%'i", "ten", "5"),
		c("suffix repeated flags", "-b", "%--d", "ten", "5"),
		c("suffix repeated grouping", "-b", "%''d", "ten", "5"),
		c("suffix minus then grouping", "-b", "%-'d", "ten", "5"),
		c("suffix empty precision", "-b", "%.d", "ten", "5"),
		c("suffix zero precision", "-b", "%.0d", "ten", "5"),
		c("suffix text either side", "-b", "a%db", "ten", "5"),
		c("suffix alt with d", "-b", "%#d", "ten", "5"),
		c("suffix alt with i", "-b", "%#i", "ten", "5"),
		c("suffix alt with u", "-b", "%#u", "ten", "5"),
		c("suffix grouping with x", "-b", "%'x", "ten", "5"),
		c("suffix grouping with o", "-b", "%'o", "ten", "5"),
		c("suffix minus then alt with d", "-b", "%-#d", "ten", "5"),
		c("suffix no conversion", "-b", "xx", "ten", "5"),
		c("suffix escaped percent only", "-b", "%%d", "ten", "5"),
		c("suffix two conversions", "-b", "%d%d", "ten", "5"),
		c("suffix bare percent", "-b", "%", "ten", "5"),
		c("suffix width with no conversion", "-b", "%5", "ten", "5"),
		c("suffix precision with no conversion", "-b", "%.3", "ten", "5"),
		c("suffix string conversion", "-b", "%s", "ten", "5"),
		c("suffix char conversion", "-b", "%c", "ten", "5"),
		c("suffix star", "-b", "%*d", "ten", "5"),
		c("suffix length modifier", "-b", "%ld", "ten", "5"),
		c("suffix plus flag", "-b", "%+d", "ten", "5"),
		c("suffix space flag", "-b", "% d", "ten", "5"),
		c("suffix I flag", "-b", "%Id", "ten", "5"),
		c("suffix newline conversion", "-b", "%\n", "ten", "5"),
		c("suffix nul conversion", "-b", "%\x01", "ten", "5"),
		c("suffix width past INT_MAX", "-b", "%2147483648d", "ten", "5"),
		c("suffix precision past INT_MAX", "-b", "%.2147483648d", "ten", "5"),

		// -k, -s, -z, --suppress-matched.
		c("keep files", "-k", "ten", "11"),
		c("long keep files", "--keep-files", "ten", "11"),
		c("keep files on no match", "-k", "ten", "/nope/"),
		c("quiet", "-s", "ten", "5"),
		c("quiet long", "--quiet", "ten", "5"),
		c("silent long", "--silent", "ten", "5"),
		c("quiet short q", "-q", "ten", "5"),
		c("quiet on error", "-s", "ten", "11"),
		c("quiet and kept on no match", "-sk", "ten", "/nope/"),
		c("elide", "-z", "ten", "1", "2", "3"),
		c("elide long", "--elide-empty-files", "ten", "1", "2", "3"),
		c("elide a repeated line number", "-z", "ten", "1", "1"),
		c("elide and keep", "-kz", "ten", "1", "1"),
		c("elide with an error after", "-z", "ten", "5", "11"),
		c("elide a failing second regexp", "-z", "ten", "/5/", "/5/"),
		c("suppress matched", "--suppress-matched", "ten", "/5/"),
		c("suppress a line number", "--suppress-matched", "ten", "5"),
		c("suppress line one", "--suppress-matched", "ten", "1"),
		c("suppress the last line", "--suppress-matched", "ten", "10"),
		c("suppress past the last line", "--suppress-matched", "ten", "/10/+1"),
		c("suppress two regexps", "--suppress-matched", "ten", "/5/", "/8/"),
		c("suppress a negative offset", "--suppress-matched", "ten", "/5/-1"),
		c("suppress a positive offset", "--suppress-matched", "ten", "/5/+2"),
		c("suppress a skip", "--suppress-matched", "ten", "%5%"),
		c("suppress and elide", "--suppress-matched", "-z", "ten", "/1/"),
		c("suppress and elide a match", "-z", "--suppress-matched", "ten", "/5/"),
		c("suppress moves both cursors", "--suppress-matched", "ten", "5", "/5/"),
		c("suppress leaves the scan where it was", "--suppress-matched", "ten", "/5/", "/6/"),
		c("suppress then a later regexp", "--suppress-matched", "ten", "5", "/6/"),
		c("suppress then an offset regexp", "--suppress-matched", "ten", "5", "/6/+1"),
		c("suppress then a further regexp", "--suppress-matched", "ten", "5", "/7/"),
		c("suppress then a backward offset", "--suppress-matched", "ten", "5", "/6/-1"),
		c("suppress then a passed line number", "--suppress-matched", "ten", "/5/", "3"),
		c("suppress a negative offset then a regexp", "--suppress-matched", "ten", "/5/-1", "/4/"),
		c("suppress on a repeat", "--suppress-matched", "abab", "/a/", "{*}"),

		// Operands, stdin and the option scan.
		c("no operand"),
		c("only a file", "ten"),
		c("only options", "-s"),
		c("stdin", "-", "2"),
		c("stdin with a regexp", "-", "/b/"),
		c("stdin empty", "-", "1"),
		c("double dash before the file", "--", "ten", "5"),
		c("double dash after the file", "ten", "--", "5"),
		c("double dash last", "ten", "5", "--"),
		c("missing file", "nosuch", "5"),
		c("missing file beats a bad pattern", "nosuch", "xyz"),
		c("missing file beats a bad regexp", "nosuch", "/x"),
		c("missing file beats a bad repeat", "nosuch", "5", "{x}"),
		c("a bad option beats the file", "-n", "x", "nosuch", "5"),
		c("a bad format beats the file", "-b", "%s", "nosuch", "5"),
		c("a bad format beats a bad pattern", "-b", "%s", "ten", "zzz"),
		c("a bad pattern beats a bad repeat", "ten", "zzz", "{x}"),
		c("a directory operand", "d", "5"),
		c("a directory as stdin", "d", "/x/"),
		c("invalid option", "-Q", "ten", "5"),
		c("unrecognized long option", "--nope", "ten", "5"),
		c("ambiguous s", "--s", "ten", "5"),
		c("ambiguous su", "--su", "ten", "5"),
		c("unambiguous q", "--q", "ten", "5"),
		c("unambiguous k", "--k", "ten", "5"),
		c("prefix wants an argument", "--prefix"),
		c("digits wants an argument", "--digits"),
		c("suppress takes no argument", "--suppress-matched=1", "ten", "5"),
		c("help with a value", "--help=x", "ten", "5"),
		c("version with a value", "--vers=x", "ten", "5"),

		// Bytes that are not text.
		c("a non-UTF-8 operand", "raw", "/\xff/"),
		c("a non-UTF-8 prefix", "-f", "y\xffy", "raw", "2"),
		c("a non-UTF-8 line number", "ten", "5\xff"),

		// Past a read block, in every mode.
		c("a large file by line number", "big", "40001"),
		c("a large file by regexp", "big", "/MARK/"),
		c("a large file with a negative offset", "big", "/MARK/-3"),
		c("a large file with a positive offset", "big", "/MARK/+3"),
		c("a large file in many pieces", "-z", "big", "10000", "{*}"),
		c("a large file with no match", "big", "/nomatch/"),
		c("a large file skipped", "big", "%MARK%"),
	}

	// POSIXLY_CORRECT stops the option scan at the first operand, so the
	// options after it become patterns.
	cases = append(cases,
		invocation{name: "posixly correct", args: []string{"ten", "-s", "5"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: csplitSeed},
		invocation{name: "options before the file under POSIXLY_CORRECT", args: []string{"-s", "ten", "5"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: csplitSeed},
		// The counts are the only thing on stdout, so a closed one is
		// reported at exit and a quiet run never writes at all.
		invocation{name: "closed stdout", args: []string{"ten", "5"}, stdout: stdoutClosed, seedTree: csplitSeed},
		invocation{name: "closed stdout quiet", args: []string{"-s", "ten", "5"}, stdout: stdoutClosed, seedTree: csplitSeed},
		invocation{name: "closed stdout on error", args: []string{"ten", "11"}, stdout: stdoutClosed, seedTree: csplitSeed},
		invocation{name: "full stdout", args: []string{"ten", "5"}, stdout: stdoutFull, seedTree: csplitSeed},
		invocation{name: "full stdout quiet", args: []string{"-s", "ten", "5"}, stdout: stdoutFull, seedTree: csplitSeed},
	)
	return cases
}

func TestCsplit(t *testing.T) {
	requireParity(t, "csplit", csplitCases(t))
}

func TestCsplitHelpVersion(t *testing.T) {
	requireHelp(t, "csplit", []string{"--help"}, 0)
	requireVersion(t, "csplit", []string{"--version"}, 0)
}
