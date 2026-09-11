package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fmtFile writes `content` under `dir` as `name` and returns its path.
func fmtFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("fmt", fmtCases)
}

// fmtCases is fmt(1)'s corpus.
//
// The heart of it is the break chooser: a backward dynamic program whose
// cost function has seven terms, and the cases below are the invocations
// that separate them from each other. A line's distance from the GOAL is
// squared, a line costs a constant on top of that, the difference between
// two adjacent lines costs half a squared column, and the last line of a
// paragraph is free — which is why the slack collects at the FRONT of a
// paragraph and why greedy filling is wrong here. Four more terms read the
// punctuation around a break: a sentence end is a bonus, a period that did
// NOT end one is a near-refusal, other trailing punctuation is a small
// bonus and an opening bracket is another, and the two words a break can
// strand — the one that ends a sentence and the one after a sentence's
// first word — cost a term that shrinks with the word's own length.
//
// The rest is the reader: what a paragraph is under each of -c / -t / -s,
// where the indent comes from, how a tab is measured and written back, and
// the sticky flag one anywhere in the input sets.
func fmtCases(t *testing.T) []invocation {
	dir := t.TempDir()

	words := fmtFile(t, dir, "words", "one two three four five six seven eight nine ten\n")
	prose := fmtFile(t, dir, "prose", strings.Repeat("the quick brown fox jumps over the lazy dog. ", 12)+"\n")
	two := fmtFile(t, dir, "two", "aaa bbb\nccc ddd\n")
	nonl := fmtFile(t, dir, "nonl", "aaa bbb")
	empty := fmtFile(t, dir, "e0", "")
	blank := fmtFile(t, dir, "blank", "\n\n\n")
	wsonly := fmtFile(t, dir, "wsonly", "   \n")
	indented := fmtFile(t, dir, "indented", "  aa bb cc dd ee ff gg hh\n  ii jj kk ll mm nn\n")
	tagged := fmtFile(t, dir, "tagged", "  aa bb cc dd ee ff gg hh\n    ii jj kk ll mm nn\n")
	tabs := fmtFile(t, dir, "tabs", "\tfoo bar baz qux quux corge grault garply waldo fred\n")
	quoted := fmtFile(t, dir, "quoted", "> aaa bbb ccc ddd eee fff ggg hhh\n> iii jjj kkk lll\nnot quoted at all\n")
	comment := fmtFile(t, dir, "comment", "#    aaa bbb ccc ddd eee\n#  fff ggg hhh iii\n")
	raw := fmtFile(t, dir, "na\xffme", "aaa bbb ccc\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	big := fmtFile(t, dir, "big", strings.Repeat("alpha beta gamma delta epsilon zeta eta theta\n\n", 20000))
	// 900 one-character words: long enough to exercise the chooser over a
	// whole paragraph and short enough to stay inside GNU's own word
	// buffer, which holds 997 (see docs/COREUTILS.md).
	long := fmtFile(t, dir, "long", strings.Repeat("a ", 899)+"a\n")
	// The last paragraph GNU formats in one piece.
	brim := fmtFile(t, dir, "brim", strings.Repeat("a ", 996)+"a\n")

	return []invocation{
		// The default: width 75, goal 75*187/200 = 70.
		{name: "default width", args: []string{words}},
		{name: "default width prose", args: []string{prose}},
		{name: "no operand reads stdin", args: []string{}, stdin: "aaa bbb ccc ddd eee fff\n"},
		{name: "lone dash", args: []string{"-"}, stdin: "aaa bbb ccc ddd eee fff\n"},

		// -w in every spelling, and the last one winning.
		{name: "short width glued", args: []string{"-w20", words}},
		{name: "short width spaced", args: []string{"-w", "20", words}},
		{name: "long width equals", args: []string{"--width=20", words}},
		{name: "long width spaced", args: []string{"--width", "20", words}},
		{name: "unique prefix width", args: []string{"--wid=20", words}},
		{name: "last width wins", args: []string{"-w", "40", "-w", "20", words}},
		{name: "width one", args: []string{"-w1", words}},
		{name: "width zero", args: []string{"-w0", words}},
		{name: "width two", args: []string{"-w2", words}},
		{name: "width eight", args: []string{"-w8", words}},
		{name: "the largest width", args: []string{"-w", "2500", words}},
		{name: "leading blank in the width", args: []string{"-w", " 20", words}},
		{name: "leading tab in the width", args: []string{"-w", "\t20", words}},
		{name: "plus in the width", args: []string{"-w", "+20", words}},
		{name: "leading zeros in the width", args: []string{"-w", "0020", words}},

		// Width faults: one line, no `Try …` line.
		{name: "width not a number", args: []string{"-w", "x", words}},
		{name: "width empty", args: []string{"-w", "", words}},
		{name: "width blank", args: []string{"-w", " ", words}},
		{name: "width negative", args: []string{"-w", "-1", words}},
		{name: "width minus zero", args: []string{"-w", "-0", words}},
		{name: "width trailing garbage", args: []string{"-w", "20x", words}},
		{name: "width trailing blank", args: []string{"-w", "20 ", words}},
		{name: "width hex", args: []string{"-w", "0x10", words}},
		{name: "width float", args: []string{"-w", "20.0", words}},
		{name: "width one past the largest", args: []string{"-w", "2501", words}},
		{name: "width at the parse bound", args: []string{"-w", "1073741823", words}},
		{name: "width one past the parse bound", args: []string{"-w", "1073741824", words}},
		{name: "width past INT_MAX", args: []string{"-w", "2147483648", words}},
		{name: "width past UINT_MAX", args: []string{"-w", "4294967296", words}},
		{name: "width absurd", args: []string{"-w", "99999999999999999999999", words}},
		{name: "width not valid UTF-8", args: []string{"-w", "\xff\xfe", words}},

		// -g, including its two rules: it is checked against the FINAL
		// width, and without -w it sets the width to goal + 10.
		{name: "goal glued", args: []string{"-g30", "-w", "40", words}},
		{name: "goal spaced", args: []string{"-g", "30", "-w", "40", words}},
		{name: "long goal", args: []string{"--goal=30", "-w", "40", words}},
		{name: "unique prefix goal", args: []string{"--g=30", "-w", "40", words}},
		{name: "goal alone sets the width", args: []string{"-g", "20", long}},
		{name: "goal zero alone", args: []string{"-g", "0", words}},
		{name: "goal zero with a width", args: []string{"-g", "0", "-w", "40", words}},
		{name: "goal equal to the width", args: []string{"-g", "40", "-w", "40", words}},
		{name: "goal past the default width", args: []string{"-g", "76", words}},
		{name: "goal at the default width", args: []string{"-g", "75", words}},
		{name: "goal past an explicit width", args: []string{"-w", "10", "-g", "20", words}},
		{name: "goal before an explicit width", args: []string{"-g", "20", "-w", "10", words}},
		{name: "goal legal once the width rises", args: []string{"-g", "100", "-w", "200", long}},
		{name: "goal legal with the width first", args: []string{"-w", "200", "-g", "100", long}},
		{name: "goal out of range", args: []string{"-g", "2501", words}},
		{name: "goal not a number", args: []string{"-g", "x", words}},
		{name: "goal large with no width", args: []string{"-g", "2490", words}},
		{name: "width and goal both out of range", args: []string{"-w", "2501", "-g", "2502", words}},

		// The obsolete -WIDTH, which is argv[1] and nothing else.
		{name: "obsolete width", args: []string{"-20", words}},
		{name: "obsolete width one digit", args: []string{"-8", words}},
		{name: "obsolete width leading zeros", args: []string{"-00020", words}},
		{name: "obsolete width zero", args: []string{"-0", words}},
		{name: "obsolete then explicit", args: []string{"-20", "-w", "40", words}},
		{name: "obsolete with a suffix letter", args: []string{"-20x", words}},
		{name: "obsolete out of range", args: []string{"-2501", words}},
		{name: "obsolete with other options after", args: []string{"-20", "-u", words}},
		{name: "digits after an operand", args: []string{words, "-20"}},
		{name: "digits after another option", args: []string{"-w", "40", "-20", words}},
		{name: "digits inside a cluster", args: []string{"-c20", words}},
		{name: "digits twice", args: []string{"-10", "-20", words}},
		{name: "digit alone late", args: []string{"-u", "-5", words}},
		{name: "obsolete after dashdash is a file", args: []string{"--", "-20"}},
		{name: "long digits are unrecognized", args: []string{"--20", words}},

		// Greedy and optimal differ. Each of these is a case where filling
		// each line as far as it goes gives a different answer.
		{name: "slack moves to the front", args: []string{"-w", "10"}, stdin: "000 001 002\n"},
		{name: "slack moves to the front wider", args: []string{"-w", "11"}, stdin: "aaa bbb ccc ddd eee\n"},
		{name: "three ways to break five words", args: []string{"-w", "12"}, stdin: "aaaa bbbb cccc dddd eeee\n"},
		{name: "last line is free", args: []string{"-w", "40", "-g", "5"}, stdin: "aaa bbb ccc ddd eee fff\n"},
		{name: "goal five over twenty words", args: []string{"-w", "40", "-g", "5"}, stdin: strings.Repeat("a ", 39) + "a\n"},
		{name: "goal thirteen over forty words", args: []string{"-w", "40", "-g", "13"}, stdin: strings.Repeat("a ", 39) + "a\n"},
		{name: "goal fourteen over forty words", args: []string{"-w", "40", "-g", "14"}, stdin: strings.Repeat("a ", 39) + "a\n"},
		{name: "one line short of the width", args: []string{"-w", "40", "-g", "32"}, stdin: strings.Repeat("a ", 39) + "a\n"},
		{name: "the line cost decides", args: []string{"-w", "26", "-g", "15"}, stdin: strings.Repeat("a ", 24) + "a\n"},
		{name: "the line cost decides just over", args: []string{"-w", "26", "-g", "16"}, stdin: strings.Repeat("a ", 24) + "a\n"},
		{name: "ragged cost over unequal words", args: []string{"-w", "24", "-g", "14"}, stdin: "quick x of hello a, the]\nfox fox of the! a and)\n"},
		{name: "a nine hundred word paragraph", args: []string{long}},
		{name: "a nine hundred word paragraph narrow", args: []string{"-w", "20", long}},
		{name: "a nine hundred word paragraph at goal ten", args: []string{"-w", "40", "-g", "10", long}},
		{name: "the last paragraph GNU formats whole", args: []string{brim}},
		{name: "the last paragraph GNU formats whole narrow", args: []string{"-w", "20", brim}},

		// The orphan term: what it costs to strand the word that ends a
		// sentence, which shrinks as that word grows.
		{name: "orphan one char", args: []string{"-w", "100", "-g", "100"}, stdin: strings.Repeat("xxxxxxxxxxxxxxxxxxxxxxxx ", 3) + "xxxxxxxxxxxxxxxxxxxxxxxx y\n"},
		{name: "orphan three chars", args: []string{"-w", "100", "-g", "100"}, stdin: strings.Repeat("xxxxxxxxxxxxxxxxxxxxxxxx ", 3) + "xxxxxxxxxxxxxxxxxxxxxxxx yyy\n"},
		{name: "orphan ten chars", args: []string{"-w", "100", "-g", "100"}, stdin: strings.Repeat("xxxxxxxxxxxxxxxxxxxxxxxx ", 3) + "xxxxxxxxxxxxxxxxxxxxxxxx yyyyyyyyyy\n"},
		{name: "orphan at the tipping point", args: []string{"-w", "50", "-g", "25"}, stdin: strings.Repeat("x", 20) + " yyyyyyyy " + strings.Repeat("z", 25) + "\n"},
		{name: "orphan one past the tipping point", args: []string{"-w", "50", "-g", "26"}, stdin: strings.Repeat("x", 20) + " yyyyyyyy " + strings.Repeat("z", 25) + "\n"},

		// The widow term: a break after the first word of a sentence.
		{name: "widow two chars", args: []string{"-w", "50", "-g", "39"}, stdin: strings.Repeat("x", 19) + ".  yy " + strings.Repeat("z", 25) + "\n"},
		{name: "widow two chars one goal lower", args: []string{"-w", "50", "-g", "38"}, stdin: strings.Repeat("x", 19) + ".  yy " + strings.Repeat("z", 25) + "\n"},
		{name: "widow five chars", args: []string{"-w", "50", "-g", "30"}, stdin: strings.Repeat("x", 19) + ".  yyyyy " + strings.Repeat("z", 25) + "\n"},
		{name: "widow five chars one goal lower", args: []string{"-w", "50", "-g", "29"}, stdin: strings.Repeat("x", 19) + ".  yyyyy " + strings.Repeat("z", 25) + "\n"},
		{name: "no widow without the sentence", args: []string{"-w", "50", "-g", "30"}, stdin: strings.Repeat("x", 20) + " yyyyy " + strings.Repeat("z", 25) + "\n"},
		{name: "punctuation outranks the widow", args: []string{"-w", "19", "-g", "10", "-c"}, stdin: "dog.\"\nab\" over zzzz\n"},
		{name: "a plain word takes the widow", args: []string{"-w", "19", "-g", "10", "-c"}, stdin: "dog.\"\nabx over zzzz\n"},

		// The sentence bonus, the no-break refusal, and the two small
		// bonuses for punctuation.
		{name: "break at a sentence end", args: []string{"-w", "20"}, stdin: "one two.  three four five six seven eight nine ten\n"},
		{name: "no sentence without two spaces", args: []string{"-w", "20"}, stdin: "one two. three four five six seven eight nine ten\n"},
		{name: "sentence end at the line end", args: []string{"-w", "20"}, stdin: "one two.\nthree four five six seven eight nine ten\n"},
		{name: "punctuation bonus", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbb, zzzzz\n"},
		{name: "no punctuation bonus", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbbx zzzzz\n"},
		{name: "paren bonus", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbb (zzzzz\n"},
		{name: "bracket bonus", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbb [zzzzz\n"},
		{name: "backtick bonus", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbb `zzzzz\n"},
		{name: "quote bonus", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbb \"zzzzz\n"},
		{name: "brace is not a paren", args: []string{"-w", "40"}, stdin: strings.Repeat("a", 30) + " bbb {zzzzz\n"},
		{name: "paren outranks the orphan", args: []string{"-w", "50", "-g", "24"}, stdin: strings.Repeat("x", 20) + " yyyyyyyy (" + strings.Repeat("z", 24) + "\n"},

		// Sentence detection: the closing run and the two-blank rule.
		{name: "sentence spacing under -u", args: []string{"-u", "-w", "200"}, stdin: "a.  b a?  b a!  b a:  b a,  b a)  b\n"},
		{name: "closers after the period", args: []string{"-u", "-w", "200"}, stdin: "a.)  b a.]  b a.\"  b a.'  b a.)]  b\n"},
		{name: "closers that are not closers", args: []string{"-u", "-w", "200"}, stdin: "a.}  b a.>  b a.,  b a.;  b\n"},
		{name: "one space is not a sentence", args: []string{"-u", "-w", "200"}, stdin: "a. b a? b a! b\n"},
		{name: "three spaces collapse to two", args: []string{"-u", "-w", "200"}, stdin: "a.   b\n"},
		{name: "a tab after the period", args: []string{"-u", "-w", "200"}, stdin: "a.\tb\n"},
		{name: "a bare period is a sentence", args: []string{"-u", "-w", "200"}, stdin: ".  b !  b ..  b\n"},
		{name: "closers with no period", args: []string{"-u", "-w", "200"}, stdin: ")  b ]  b \"  b\n"},
		{name: "abbreviations are sentences too", args: []string{"-u", "-w", "200"}, stdin: "Mr.  b e.g.  b A.  b\n"},
		{name: "sentence spacing when lines join", args: []string{"-w", "60"}, stdin: "Foo bar.\nBaz qux.\nEnd\n"},
		{name: "no sentence spacing on one line", args: []string{"-w", "60"}, stdin: "Foo bar. Baz qux. End\n"},

		// -u.
		{name: "uniform spacing", args: []string{"-u", "-w", "60"}, stdin: "Hello   there.   Next  sentence   here.  Done\n"},
		{name: "long uniform spacing", args: []string{"--uniform-spacing", "-w", "60"}, stdin: "Hello   there.   Next  sentence   here.  Done\n"},
		{name: "without -u spacing is the input's", args: []string{"-w", "60"}, stdin: "Hello   there.   Next  sentence   here.  Done\n"},
		{name: "inter-word spacing is kept when it fits", args: []string{"-w", "14"}, stdin: "aaaa    bbbb    cccc    dddd\n"},
		{name: "uniform spacing with tabs", args: []string{"-u", "-w", "40"}, stdin: "aaa    bbb\tccc  ddd\n"},

		// -c.
		{name: "crown margin", args: []string{"-c", "-w", "30"}, stdin: "    This is an indented first line of a paragraph that\n  continues here with a different indent and keeps going.\n"},
		{name: "crown margin without -c", args: []string{"-w", "30"}, stdin: "    This is an indented first line of a paragraph that\n  continues here with a different indent and keeps going.\n"},
		{name: "long crown margin", args: []string{"--crown-margin", "-w", "20", indented}},
		{name: "crown a one line paragraph", args: []string{"-c", "-w", "20"}, stdin: "  one two three four five six seven\n"},
		{name: "crown three lines", args: []string{"-c", "-w", "40"}, stdin: "  aaa\n    bbb\n      ccc\n"},
		{name: "crown rejects an argument", args: []string{"--crown-margin=3", words}},

		// -t.
		{name: "tagged equal indents split", args: []string{"-t", "-w", "20", indented}},
		{name: "tagged unequal indents join", args: []string{"-t", "-w", "20", tagged}},
		{name: "long tagged", args: []string{"--tagged-paragraph", "-w", "20", tagged}},
		{name: "tagged one line at indent zero", args: []string{"-t", "-w", "20"}, stdin: "one two three four five six seven\n"},
		{name: "tagged one line at indent one", args: []string{"-t", "-w", "20"}, stdin: " one two three four five six seven\n"},
		{name: "tagged one line at indent two", args: []string{"-t", "-w", "20"}, stdin: "  one two three four five six seven\n"},
		{name: "tagged carries the other indent forward", args: []string{"-t", "-w", "20"}, stdin: "aa\n  bb\none two three four five six seven\n"},
		{name: "tagged carries a deeper indent forward", args: []string{"-t", "-w", "20"}, stdin: "aa\n    bb\none two three four five six seven\n"},
		{name: "tagged falls back when it would match", args: []string{"-t", "-w", "20"}, stdin: "aa\n  bb\n\n  cc dd ee ff gg hh ii jj kk\n"},
		{name: "crown wins over tagged", args: []string{"-ct", "-w", "20", indented}},
		{name: "tagged wins nothing over crown", args: []string{"-tc", "-w", "20", indented}},

		// -s: long lines are split, short ones are never joined, and the
		// chooser still decides where the split goes.
		{name: "split only", args: []string{"-s", "-w", "40", two}},
		{name: "long split only", args: []string{"--split-only", "-w", "40", two}},
		{name: "split only breaks optimally", args: []string{"-s", "-w", "10"}, stdin: "000 001 002\n"},
		{name: "split only wide", args: []string{"-s", "-w", "12"}, stdin: "aaaa bbbb cccc dddd eeee ffff gggg\n"},
		{name: "split only keeps the indent", args: []string{"-s", "-w", "20"}, stdin: "  one two three four five six seven eight\n"},
		{name: "split only keeps spacing", args: []string{"-s", "-w", "40"}, stdin: "aaa    bbb\tccc\n"},
		{name: "split only with -u", args: []string{"-s", "-u", "-w", "12"}, stdin: "aaa   bbb.   ccc  ddd eee fff ggg hhh\n"},
		{name: "split only beats crown", args: []string{"-sc", "-w", "20", indented}},
		{name: "split only beats tagged", args: []string{"-st", "-w", "20", indented}},
		{name: "split only beats both", args: []string{"-cstu", "-w", "20", indented}},

		// -p.
		{name: "prefix", args: []string{"-p", ">", "-w", "20", quoted}},
		{name: "prefix with a trailing space", args: []string{"-p", "> ", "-w", "20", quoted}},
		{name: "long prefix", args: []string{"--prefix=>", "-w", "20", quoted}},
		{name: "glued short prefix", args: []string{"-p>", "-w", "20", quoted}},
		{name: "empty prefix matches everything", args: []string{"--prefix=", "-w", "20", two}},
		{name: "prefix trailing spaces are matched", args: []string{"-p", "# ", "-w", "60"}, stdin: "#aaa bbb\n#ccc ddd\n"},
		{name: "prefix without the trailing space", args: []string{"-p", "#", "-w", "60"}, stdin: "#aaa bbb\n#ccc ddd\n"},
		{name: "prefix leading spaces are a minimum", args: []string{"-p", "    # ", "-w", "60"}, stdin: "    # aaa bbb\n    # ccc ddd\n"},
		{name: "prefix leading spaces too many", args: []string{"-p", "     # ", "-w", "60"}, stdin: "    # aaa bbb\n    # ccc ddd\n"},
		{name: "prefix leading spaces change nothing", args: []string{"-p", "  # ", "-w", "60"}, stdin: "    # aaa bbb\n    # ccc ddd\n"},
		{name: "the indent before the prefix must match", args: []string{"-p", "#", "-w", "60"}, stdin: "  # aaa bbb\n    # ccc ddd\n"},
		{name: "the indent before the prefix matching", args: []string{"-p", "#", "-w", "60"}, stdin: "  # aaa bbb\n  # ccc ddd\n"},
		{name: "the indent after the prefix must match", args: []string{"-p", "# ", "-w", "60"}, stdin: "# aaa bbb ccc\n    # ddd eee\n"},
		{name: "the indent before the prefix counts", args: []string{"-p", "#", "-w", "20"}, stdin: "          # aaa bbb ccc ddd eee\n"},
		{name: "a doubled prefix", args: []string{"-p", ">", "-w", "20"}, stdin: ">> aaa bbb ccc ddd eee fff\n"},
		{name: "a prefix only line", args: []string{"-p", "#", "-w", "40"}, stdin: "# a b\n#   \n# c d\n"},
		{name: "a line without the prefix is verbatim", args: []string{"-p", "#", "-w", "20"}, stdin: "plain one two three four five six seven eight\n"},
		{name: "a blank line without the prefix", args: []string{"-p", "#", "-w", "40"}, stdin: "   \n# foo bar\n"},
		{name: "a blank unterminated line without the prefix", args: []string{"-p", "#", "-w", "40"}, stdin: "# foo bar\n   "},
		{name: "an unterminated verbatim line", args: []string{"-p", "#", "-w", "40"}, stdin: "# foo bar\nplain"},
		{name: "prefix with -c", args: []string{"-p", "#", "-c", "-w", "20", comment}},
		{name: "prefix without -c", args: []string{"-p", "#", "-w", "20", comment}},
		{name: "prefix with -u", args: []string{"-p", "#", "-u", "-w", "60"}, stdin: "#  aaa    bbb.   ccc\n"},
		{name: "prefix with -s", args: []string{"-p", "#", "-s", "-w", "20"}, stdin: "# aaa bbb ccc ddd eee fff\n# ggg\n"},
		{name: "last prefix wins", args: []string{"-p", "#", "-p", "//", "-w", "20", comment}},
		{name: "prefix is not valid UTF-8", args: []string{"-p", "\xff", "-w", "20"}, stdin: "\xff aaa bbb\n"},
		{name: "prefix of blanks only", args: []string{"-p", "   ", "-w", "40"}, stdin: "   aaa bbb\n  ccc ddd\n"},
		{name: "a blank prefix is that many columns", args: []string{"-p", "   ", "-w", "20"}, stdin: "  aaa bbb ccc ddd eee fff ggg hhh\n  iii jjj\n"},
		{name: "a blank prefix met exactly", args: []string{"-p", "   ", "-w", "20"}, stdin: "    aaa bbb ccc ddd eee fff ggg hhh\n    iii jjj\n"},
		{name: "a blank prefix a tab straddles", args: []string{"-p", "   ", "-w", "20"}, stdin: "\tx y\n"},
		{name: "a blank line under a blank prefix", args: []string{"-p", "  ", "-w", "40"}, stdin: "a\n   \nb\n"},
		{name: "a blank line exactly the blank prefix", args: []string{"-p", "  ", "-w", "40"}, stdin: "a\n  \nb\n"},
		{name: "a blank line shorter than the blank prefix", args: []string{"-p", "  ", "-w", "40"}, stdin: "a\n \nb\n"},
		{name: "a tab-only line under a blank prefix", args: []string{"-p", "   ", "-w", "40"}, stdin: "a\n\t\nb\n"},
		{name: "the prefix loses its trailing blanks", args: []string{"-p", "# ", "-w", "60"}, stdin: "# \tab cd\n"},
		{name: "the prefix keeps the blanks after it", args: []string{"-p", "# ", "-w", "60"}, stdin: "#   aaa bbb\n"},
		{name: "a prefix line too shallow is copied", args: []string{"-p", "  # ", "-w", "20"}, stdin: "# aaa bbb ccc ddd eee fff ggg hhh\n# iii jjj\n"},
		{name: "a shallow prefix line keeps its blanks", args: []string{"-p", "  # ", "-w", "60"}, stdin: "x\ty\n\n# \tand bbb\n"},
		{name: "an unmatched line re-renders its indent", args: []string{"-p", "#", "-w", "40"}, stdin: "x\ty\n\n  \tabc def\n"},
		{name: "an unmatched line keeps its tail", args: []string{"-p", "#", "-w", "40"}, stdin: "x\ty\n\na  b   c   \n"},
		{name: "an unterminated blank line under a prefix", args: []string{"-p", "//", "-w", "40"}, stdin: "aaa bbb\n   "},
		{name: "an unterminated blank line with no prefix", args: []string{"-w", "40"}, stdin: "aaa bbb\n   "},
		{name: "tagged under a prefix", args: []string{"-t", "-p", ">", "-w", "20"}, stdin: ">zz\n\n> one two three four five six seven\n"},
		{name: "tagged under a prefix carries the column", args: []string{"-t", "-p", ">", "-w", "20"}, stdin: ">aa\n>  bb\n\n>  one two three four five six seven\n"},
		{name: "tagged under a prefix repeats the column", args: []string{"-t", "-p", ">", "-w", "20"}, stdin: ">aa\n>  bb\n\n> one two three four five six seven\n"},
		{name: "tagged under a prefix at a narrow width", args: []string{"-t", "-p", "//", "-w", "30", "-g", "2"}, stdin: "    //  0123456789  x   the ab word\tabc    quick  word   abc hello quick  abc\n"},

		// Paragraphs.
		{name: "blank line separates", args: []string{"-w", "40"}, stdin: "aaa bbb\n\nccc ddd\n"},
		{name: "runs of blank lines", args: []string{"-w", "40", blank}},
		{name: "whitespace only line is blank", args: []string{"-w", "40"}, stdin: "a b\n   \nc d\n"},
		{name: "tab only line is blank", args: []string{"-w", "40"}, stdin: "a b\n\t\nc d\n"},
		{name: "a whitespace only file", args: []string{"-w", "40", wsonly}},
		{name: "indent change separates", args: []string{"-w", "40"}, stdin: "aa bb\n  cc dd\nee ff\n"},
		{name: "deepening indent", args: []string{"-w", "40"}, stdin: "a\n  b\n    c\n  d\na\n"},
		{name: "empty file", args: []string{"-w", "40", empty}},
		{name: "no final newline", args: []string{"-w", "40", nonl}},
		{name: "trailing blank unterminated line", args: []string{"-w", "40"}, stdin: "aaa bbb\n   "},
		{name: "leading blank lines", args: []string{"-w", "40"}, stdin: "\n\naaa bbb\n"},

		// Tabs: width, the sticky flag, and the way a run is written back.
		{name: "tab indent", args: []string{"-w", "20", tabs}},
		{name: "tab indent is eight columns", args: []string{"-w", "12"}, stdin: "\ta b c d e\n"},
		{name: "eight spaces are not a tab", args: []string{"-w", "12"}, stdin: "        a b c d e\n"},
		{name: "tab separator width", args: []string{"-w", "10"}, stdin: "a\tb\n"},
		{name: "tab separator width one under", args: []string{"-w", "9"}, stdin: "a\tb\n"},
		{name: "tab separator at column seven", args: []string{"-w", "11"}, stdin: "abcdefg\tab\n"},
		{name: "tab separator at column eight", args: []string{"-w", "19"}, stdin: "abcdefgh\tab\n"},
		{name: "the tabs flag is sticky", args: []string{"-w", "40"}, stdin: "x\ty\n\na          b\n"},
		{name: "the tabs flag does not reach back", args: []string{"-w", "40"}, stdin: "a          b\n\nx\ty\n"},
		{name: "a tab in the next indent reaches back", args: []string{"-w", "40"}, stdin: "a          b\n\tc d\n"},
		{name: "a tab in the next words does not", args: []string{"-w", "40"}, stdin: "a          b\n  c\td\n"},
		{name: "a tab in a verbatim line sets nothing", args: []string{"-p", "#", "-w", "40"}, stdin: "a          b\nx\ty\n# c          d\n"},
		{name: "a single blank on a stop stays a blank", args: []string{"-w", "60"}, stdin: "x\ty\n\nabcdefg b\n"},
		{name: "two blanks reaching a stop become a tab", args: []string{"-w", "60"}, stdin: "x\ty\n\nabcdef  b\n"},
		{name: "a run that crosses two stops", args: []string{"-w", "60"}, stdin: "x\ty\n\nabcdefg" + strings.Repeat(" ", 9) + "b\n"},
		{name: "indent rendered with tabs", args: []string{"-w", "40"}, stdin: "x\ty\n\n          a b c\n"},
		{name: "an indent of two spaces and a tab", args: []string{"-w", "22"}, stdin: "  \tone two three four five six\n"},
		{name: "an indent of a tab and a space", args: []string{"-w", "22"}, stdin: "\t one two three four five six\n"},

		// Bytes that are not separators.
		{name: "form feed is a word byte", args: []string{"-w", "40"}, stdin: "a b\fc d\n"},
		{name: "carriage return is a word byte", args: []string{"-w", "40"}, stdin: "a b\rc d\n"},
		{name: "NUL is a word byte", args: []string{"-w", "40"}, stdin: "a\x00b c\n"},
		{name: "vertical tab is a word byte", args: []string{"-w", "40"}, stdin: "a\vb c\n"},
		{name: "backspace is a word byte", args: []string{"-w", "40"}, stdin: "a\bb c\n"},
		{name: "escape is a word byte", args: []string{"-w", "40"}, stdin: "a\x1bb c\n"},
		{name: "columns are bytes not characters", args: []string{"-w", "8"}, stdin: "\xc3\xa9\xc3\xa9 bbb ccc ddd\n"},
		{name: "invalid UTF-8 passes through", args: []string{"-w", "5"}, stdin: "a\xff\xfe b \xc3 ccc\n"},

		// Long and unbreakable.
		{name: "a word longer than the width", args: []string{"-w", "10"}, stdin: strings.Repeat("x", 30) + " y\n"},
		{name: "an indent wider than the width", args: []string{"-w", "10"}, stdin: strings.Repeat(" ", 30) + "a b c\n"},
		{name: "two over-long words", args: []string{"-w", "10"}, stdin: strings.Repeat("x", 30) + " " + strings.Repeat("y", 30) + "\n"},

		// Operands.
		{name: "two files", args: []string{"-w", "40", two, words}},
		{name: "paragraphs do not span files", args: []string{"-w", "40", nonl, two}},
		{name: "dash among files", args: []string{"-w", "40", two, "-", words}, stdin: "from stdin here\n"},
		{name: "dashdash", args: []string{"-w", "40", "--", two}},
		{name: "empty operand", args: []string{"-w", "40", ""}},
		{name: "operand that is not valid UTF-8", args: []string{"-w", "40", raw}},
		{name: "missing file", args: []string{"-w", "40", missing}},
		{name: "missing then good", args: []string{"-w", "40", missing, two}},
		{name: "missing name that needs quoting", args: []string{filepath.Join(dir, "no such")}},
		{name: "not a directory", args: []string{filepath.Join(two, "sub")}},
		{name: "directory", args: []string{"-w", "40", d}},
		{name: "directory then a file", args: []string{"-w", "40", d, two}},
		{name: "file then a directory", args: []string{"-w", "40", two, d}},
		{name: "stdin is a directory", args: []string{"-w", "40"}, stdinPath: d},
		{name: "stdin is a regular file", args: []string{"-w", "20"}, stdinPath: words},

		// getopt.
		{name: "invalid short option", args: []string{"-Z", words}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "width needs a value", args: []string{"-w"}},
		{name: "long width needs a value", args: []string{"--width"}},
		{name: "abbreviated width needs a value", args: []string{"--wid"}},
		{name: "goal needs a value", args: []string{"-g"}},
		{name: "long goal needs a value", args: []string{"--goal"}},
		{name: "prefix needs a value", args: []string{"-p"}},
		{name: "long prefix needs a value", args: []string{"--prefix"}},
		{name: "flag rejects a glued value", args: []string{"--split-only=x", words}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unique prefix crown", args: []string{"--c", "-w", "20", indented}},
		{name: "unique prefix split", args: []string{"--sp", "-w", "40", two}},
		{name: "unique prefix tagged", args: []string{"--ta", "-w", "20", tagged}},
		{name: "unique prefix uniform", args: []string{"--un", "-w", "40"}, stdin: "a   b\n"},
		{name: "unique prefix prefix", args: []string{"--pr", "#", "-w", "20", comment}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "clustered flags", args: []string{"-ut", "-w", "20", tagged}},
		{name: "clustered with a value", args: []string{"-uw20", words}},
		{name: "posix operand then option", args: []string{words, "-w", "20"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-w", "20", words}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix help after an operand", args: []string{words, "--help"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix2 version does not permute", args: []string{words, "-w", "20"}, env: []string{"_POSIX2_VERSION=199209"}},
		{name: "COLUMNS is not read", args: []string{words}, env: []string{"COLUMNS=20"}},

		// Write failures.
		{name: "stdout closed", args: []string{"-w", "20", words}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"-w", "20", words}, stdout: stdoutFull},
		{name: "stdout closed with a large output", args: []string{"-w", "20", big}, stdout: stdoutClosed},
		{name: "stdout full with a large output", args: []string{"-w", "20", big}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},
		{name: "stdout full with a missing file", args: []string{missing}, stdout: stdoutFull},

		// Bulk.
		{name: "large file", args: []string{"-w", "30", big}},
		{name: "large file with -u", args: []string{"-u", "-w", "30", big}},
		{name: "large file with -s", args: []string{"-s", "-w", "30", big}},
		{name: "a line longer than one read block", args: []string{"-w", "140"}, stdin: strings.Repeat(strings.Repeat("z", 100)+" ", 650) + "end\n"},
		{name: "a single word longer than one read block", args: []string{"-w", "40"}, stdin: strings.Repeat("z", 70000) + "\n"},
		{name: "many short paragraphs", args: []string{"-w", "30"}, stdin: strings.Repeat("aaa bbb ccc ddd eee\n\n", 10000)},
	}
}

func TestFmtParity(t *testing.T) {
	requireParity(t, "fmt", fmtCases(t))
}

func TestFmtHelpVersion(t *testing.T) {
	requireHelp(t, "fmt", []string{"--help"}, 0)
	requireHelp(t, "fmt", []string{"--hel"}, 0)
	requireHelp(t, "fmt", []string{"--h"}, 0)
	requireVersion(t, "fmt", []string{"--version"}, 0)
	requireVersion(t, "fmt", []string{"--vers"}, 0)
	requireVersion(t, "fmt", []string{"--v"}, 0)
	requireVersion(t, "fmt", []string{"-20", "--version"}, 0)
	requireHelp(t, "fmt", []string{"--help", "extra"}, 0)
}
