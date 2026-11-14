package e2ecompiler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

// ripgrep is the oracle of the rg example. The test fails without it.
const ripgrep = "/usr/bin/rg"

func TestSelfHostExampleRgUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/rg/rg_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// rgCase is one command line, with its standard input, and the directory
// below the fixtures where it runs. With noStdin, the standard input is
// /dev/null. With anyOrder, the lines of the output can come in any order,
// as they do from ripgrep with more than one thread. With pty, the standard
// output is a terminal, and TERM is set when term is not "". Each entry of
// env is NAME=VALUE, with DIR in VALUE for the directory of the fixtures.
type rgCase struct {
	args     []string
	stdin    string
	dir      string
	noStdin  bool
	anyOrder bool
	pty      bool
	term     string
	env      []string
}

func rgArgs(line string) []string { return strings.Fields(line) }

// rgFixtures writes the files that the cases search.
func rgFixtures(t *testing.T, dir string) {
	t.Helper()
	var many strings.Builder
	for i := 1; i <= 300; i++ {
		if i%13 == 0 || i%29 == 0 {
			fmt.Fprintf(&many, "line %d foo %d\n", i, i*7)
		} else {
			fmt.Fprintf(&many, "line %d bar\n", i)
		}
	}
	long := strings.Repeat("abcdefghij", 20000) + " needle " + strings.Repeat("xyz", 3000) + "\nshort needle\n"
	files := map[string]string{
		"a.txt":       "alpha beta\ngamma foo\nfoo bar foo\nnothing here\nFoo again\nlast foo",
		"w.txt":       "foobar foo\nxfoo foo_x\n-foo- (foo)\nfoo\nfoofoo\n",
		"upper.txt":   "Foo\nFOO\nfoo\nfOo bar\nnone\n",
		"crlf.txt":    "one\r\ntwo foo\r\nthree\r\n",
		"crlf2.txt":   "one\r\ntwo foo\r\nthree",
		"nul.dat":     "one\x00two foo\x00three\x00",
		"long.md":     "ééééé foo ééé\nab foo cd foo ef foo gh\n  gamma foo foo\n",
		"pats.txt":    "foo\r\n\nbe+ta\n",
		"bad.pat":     "fo\xffo\n",
		"none.pat":    "",
		"latin.txt":   "caf\xe9 foo \x80\n",
		"u16le.txt":   "\xff\xfec\x00a\x00f\x00\xe9\x00 \x00f\x00o\x00o\x00\n\x00b\x00a\x00r\x00\n\x00",
		"u16be.txt":   "\xfe\xff\x00c\x00a\x00f\x00\xe9\x00 \x00f\x00o\x00o\x00\n",
		"u16nb.txt":   "c\x00a\x00f\x00\xe9\x00 \x00f\x00o\x00o\x00\n\x00",
		"bom8.txt":    "\xef\xbb\xbfcaf\xe9 foo \xe2\x82\n",
		"ml.txt":      "one\nabc\ndefxxxabc\ndefxxx\nlast abc\nmid\nabc\ndef\n",
		"hw.txt":      "hello\nworld\n",
		"empty.txt":   "",
		"nonl.txt":    "foo",
		"blank.txt":   "\n\nfoo\n\n",
		"uni.txt":     "café au lait\nnaïve foo\nψυχή καὶ σῶμα\nplain\n",
		"many.txt":    many.String(),
		"long.txt":    long,
		"bin.dat":     "abc\x00def foo\nmore foo\n",
		"bin2.dat":    "foo 1\nbar\nabc\x00def\nfoo 2\n",
		"bin3.dat":    "abc\x00def\n",
		"bin4.dat":    "a\x00foo\nzz\n",
		"ab.txt":      "bbabb\nba\nfoobar xfoo\n",
		"sjis.txt":    "\x84Y\x84u\x84\x82\x84|\x84\x80\x84{ \x84V\x84\x80\x84|\x84}\x84\x83 \x82\xa0\x82\xa2\x82\xa4 foo\x0a\x94\xbc\x8ap\xb6\xc5 bar\x0a\x81\x0a",
		"eucjp.txt":   "\xb4\xc1\xbb\xfa foo\x0a\xa4\xab\xa4\xca\x8f\xb0\xa1\x0a",
		"euckr.txt":   "\xc7\xd1\xb1\xb9\xbe\xee foo\x0a",
		"gbk.txt":     "\xd6\xd0\xce\xc4 foo\x81\x30\x81\x30\x0a",
		"big5.txt":    "\xa4\xa4\xa4\xe5 foo\x88\x62\x0a",
		"koi8.txt":    "\xf0\xd2\xc9\xd7\xc5\xd4 foo\x0a",
		"iso2022.txt": "\x1b$BF|K\x5c\x1b(B foo\x1b(J\x5c\x0a",
		"z/t.gz":      "\x1f\x8b\x08\x08\x59\x78\xf8\x6a\x00\x03\x66\x2e\x74\x78\x74\x00\x4b\xcb\xcf\x57\x48\x4a\x2c\xe2\x4a\x4a\xac\xe2\x4a\x03\xb2\x13\xd3\x13\x33\xf3\xb8\x00\x62\xef\x63\xd7\x16\x00\x00\x00",
		"z/t.bz2":     "\x42\x5a\x68\x39\x31\x41\x59\x26\x53\x59\x1a\xa7\xf9\x1c\x00\x00\x06\x51\x80\x00\x10\x40\x00\x31\xa1\x90\x10\x20\x00\x21\x28\x4c\x8d\x94\x20\x1a\x69\xa1\x34\xcc\x5a\x57\xa3\x47\xd1\x9c\x3e\x2e\xe4\x8a\x70\xa1\x20\x35\x4f\xf2\x38",
		"z/t.xz":      "\xfd\x37\x7a\x58\x5a\x00\x00\x04\xe6\xd6\xb4\x46\x02\x00\x21\x01\x16\x00\x00\x00\x74\x2f\xe5\xa3\x01\x00\x15\x66\x6f\x6f\x20\x62\x61\x72\x0a\x62\x61\x7a\x0a\x66\x6f\x6f\x20\x61\x67\x61\x69\x6e\x0a\x00\x00\x00\x5e\x1b\xb1\xee\xdd\xd5\x4e\xd5\x00\x01\x2e\x16\x56\x09\x55\xdf\x1f\xb6\xf3\x7d\x01\x00\x00\x00\x00\x04\x59\x5a",
		"z/t.lzma":    "\x5d\x00\x00\x80\x00\xff\xff\xff\xff\xff\xff\xff\xff\x00\x33\x1b\xec\x40\x62\x31\x1f\xff\x69\xb1\xfe\x32\xd8\x28\xd6\x38\xaa\xb7\x76\xf4\x9a\x27\x05\xff\xfe\x12\x5c\x00",
		"z/t.Z":       "\x1f\x9d\x90\x66\xde\xbc\x01\x21\x26\x8c\x1c\x05\x05\xf5\x28\x08\x38\x30\xcc\x99\x30\x69\xdc\x28\x00",
		"z/trunc.xz":  "\xfd\x37\x7a\x58\x5a\x00\x00\x04\xe6\xd6\xb4\x46\x02\x00\x21\x01\x16\x00\x00\x00\x74\x2f\xe5\xa3\x01\x00\x15\x66\x6f\x6f",
		"z/bad.gz":    "garbage",
		"z/bad.bz2":   "garbage",
		"z/bad.xz":    "garbage",
		"z/plain.txt": "foo plain\n",
		"rgconfig":    "# A comment\n\n  --colors=match:fg:green  \n-n\n--color=always\n",
		"badconfig":   "--bogus\n",
	}
	if err := os.MkdirAll(filepath.Join(dir, "z"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scripts := map[string]string{
		"pre.sh":   "#!/bin/sh\necho \"pre: $1\"\ncat\n",
		"fail.sh":  "#!/bin/sh\necho foo one\necho oops >&2\nexit 1\n",
		"quiet.sh": "#!/bin/sh\nexit 3\n",
	}
	for name, text := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// rgCases are the command lines of the differential test. A case with
// several files passes -j1: with more threads, ripgrep writes the files in
// the order that their searches end.
func rgCases(dir string) []rgCase {
	stdinOf := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			panic(err)
		}
		return string(data)
	}
	lines := []string{
		// Selection, names and line numbers.
		"foo a.txt",
		"-j1 foo a.txt w.txt",
		"-n foo a.txt",
		"-N foo a.txt",
		"-n -N foo a.txt",
		"-N -n foo a.txt",
		"-H foo a.txt",
		"-j1 -I foo a.txt w.txt",
		"-j1 --no-filename -n foo a.txt crlf.txt",
		"--with-filename --line-number foo a.txt",
		"-j1 -H -I foo a.txt",
		"-j1 -I -H foo a.txt",
		"-i FOO upper.txt",
		"--ignore-case -n foo upper.txt",
		"-i -s foo upper.txt",
		"-v foo a.txt",
		"-vn --invert-match foo a.txt",
		"-w foo w.txt",
		"-wn -e -foo w.txt",
		"--word-regexp -i FOO w.txt",
		"-w o a.txt",
		"-F (foo) w.txt",
		"-F -e . a.txt",
		"-Fi FOO.BAR a.txt",
		"--fixed-strings -e foo -e (foo) w.txt",
		// Counts and file names.
		"-c foo a.txt",
		"-j1 -c foo a.txt crlf.txt empty.txt w.txt",
		"-c zzz a.txt",
		"-cv foo a.txt",
		"-co foo w.txt",
		"-c -o -v foo a.txt",
		"--count -i foo upper.txt",
		"-l foo a.txt",
		"-j1 -l foo a.txt crlf.txt empty.txt bin.dat",
		"-lv foo w.txt",
		"-c -l foo a.txt",
		"-l -c foo a.txt",
		"--files-with-matches zzz a.txt",
		// Only the matches.
		"-o foo a.txt",
		"-on o+ a.txt",
		"-o x* w.txt",
		"-o -n -H fo* w.txt",
		"-ov foo a.txt",
		"-ow foo w.txt",
		"--only-matching -i f.o upper.txt",
		// Context.
		"-A1 foo a.txt",
		"-B1 -n foo a.txt",
		"-C1 -n foo a.txt",
		"-n -C3 foo many.txt",
		"-C 2 -A 0 foo many.txt",
		"-A2 -B1 -n 1[05] many.txt",
		"--after-context 1 --before-context=2 foo many.txt",
		"--context=1 -c foo many.txt",
		"-j1 -A1 foo a.txt empty.txt crlf.txt",
		"-j1 -A1 foo a.txt bin.dat crlf.txt",
		"-C0 foo a.txt",
		"-A1 -l foo a.txt",
		"-B1 -o -n foo a.txt",
		"-C1 -v foo a.txt",
		"-nA1 foo a.txt",
		"-A 99999999999 foo a.txt",
		// Color.
		"--color always foo a.txt",
		"--color=always -n -H foo a.txt",
		"-j1 --color always -n -C1 foo a.txt crlf.txt",
		"--color always -o -n foo a.txt",
		"-j1 --color always -c foo a.txt crlf.txt",
		"--color always -c foo a.txt",
		"--color always -l foo a.txt",
		"--color always -v foo a.txt",
		"--color always -C1 -v -n foo a.txt",
		"--color=ansi -w foo w.txt",
		"--color never foo a.txt",
		"--color auto foo a.txt",
		"--color always -H foo bin.dat",
		"--color always o* w.txt",
		// Patterns.
		"-e foo -e bar a.txt",
		"-e foo a.txt -e gamma",
		"--regexp=foo --regexp alpha a.txt",
		"-efoo a.txt",
		"-- -foo w.txt",
		"f[aeiou]o upper.txt",
		"^foo w.txt",
		"foo$ w.txt",
		`\bfoo\b w.txt`,
		`-c \d+ many.txt`,
		"-o (ab)+c? long.txt",
		// Leftmost-first: the first alternative and the loop that ends on an
		// empty repeat win, where leftmost-longest gives a longer match.
		"-o foo|foobar w.txt",
		"-o -e foo -e foobar ab.txt",
		"-o -e foobar -e foo ab.txt",
		"--color always x*|foo w.txt",
		"--color always -n (?:foo|foob)a? ab.txt",
		"-o (?:b?(?:|a))* ab.txt",
		"-o b(|a)+ ab.txt",
		"-c o{2,} a.txt",
		"two.foo$ crlf.txt",
		"café uni.txt",
		"-n naïve uni.txt",
		"-c ψυχή uni.txt",
		// Edges.
		"foo empty.txt",
		"-v foo empty.txt",
		"-c foo empty.txt",
		"foo nonl.txt",
		"-n -v foo blank.txt",
		"-n needle long.txt",
		"-c needle long.txt",
		"-o needle long.txt",
		// Binary files.
		"foo bin.dat",
		"-c foo bin.dat",
		"-n foo bin2.dat",
		"-o foo bin2.dat",
		"-H -A1 bar bin2.dat",
		"zzz bin3.dat",
		"-v zzz bin3.dat",
		"-v foo bin4.dat",
		"-c -v foo bin4.dat",
		"-l foo bin.dat",
		// A missing file.
		"foo nofile",
		"-j1 foo nofile",
		"-j1 foo nofile a.txt",
		"-j1 -c foo nofile",
		"-j1 -l -n foo a.txt nofile",
		// Errors on the command line.
		"",
		"-n",
		"-Z foo a.txt",
		"--bogus foo a.txt",
		"--bogus=1 foo a.txt",
		"--count=1 foo a.txt",
		"-A",
		"foo --color",
		"-A x foo a.txt",
		"--after-context x foo a.txt",
		"-A -1 foo a.txt",
		"-C1foo a.txt",
		"-A 99999999999999999999 foo a.txt",
		"--color bogus foo a.txt",
		"-j x foo a.txt",
	}
	var cases []rgCase
	for _, l := range lines {
		cases = append(cases, rgCase{args: rgArgs(l)})
	}
	// The standard input.
	cases = append(cases,
		rgCase{args: rgArgs("foo"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-n foo -"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-j1 foo - a.txt"), stdin: stdinOf("w.txt")},
		rgCase{args: rgArgs("-H foo"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-c foo"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("-l foo -"), stdin: stdinOf("a.txt")},
		rgCase{args: rgArgs("foo"), stdin: stdinOf("bin.dat")},
		rgCase{args: rgArgs("-o -n o+"), stdin: stdinOf("crlf.txt")},
		rgCase{args: rgArgs("foo"), stdin: ""},
	)
	cases = append(cases, rgFlagCases()...)
	cases = append(cases, rgLateCases()...)
	return append(cases, rgWalkCases()...)
}

// rgFlagCases give the output flags, the input flags, --stats, --json and
// the defaults on a terminal.
func rgFlagCases() []rgCase {
	lines := []string{
		"--column foo a.txt", "--column -o foo w.txt", "-b -C1 foo a.txt", "-n -b --column -o foo long.md",
		"--column -N foo a.txt", "--column -v foo a.txt", "--vimgrep foo w.txt", "--vimgrep -j1 foo a.txt w.txt",
		"--vimgrep -A1 foo a.txt", "--vimgrep -N --no-column foo a.txt", "--no-column --column foo a.txt",
		"-r X foo a.txt", "-o -r X foo w.txt", "-r [$0|$1|${x}|$$|$2x] f(o)(?P<x>o) a.txt", "-r X -C1 foo a.txt",
		"-r X -v foo a.txt", "-o -b --column -r XY foo long.md", "--replace= foo a.txt", "--color always -r X foo a.txt",
		"--trim foo long.md", "--trim -n -C1 foo long.md", "--trim --column -b foo long.md", "--trim -o \\s*foo long.md",
		"-M 10 foo long.md", "-M 10 --max-columns-preview foo long.md", "-M 2 -o foo long.md", "-M 5 -C1 foo a.txt",
		"-M 12 --max-columns-preview --color always foo long.md", "-M 9 -r X foo long.md", "-M 0 foo a.txt", "-M x foo a.txt",
		"--passthru foo a.txt", "--passthru -n -o foo a.txt", "--passthrough -c foo a.txt", "--stop-on-nonmatch foo a.txt",
		"--stop-on-nonmatch -A1 foo a.txt", "--stop-on-nonmatch -c foo a.txt", "--null-data -n foo nul.dat",
		"--null-data -o fo nul.dat", "--null-data -c foo nul.dat", "--null-data --crlf foo nul.dat", "--crlf foo$ crlf2.txt",
		"foo$ crlf2.txt", "--crlf -o o crlf2.txt", "--crlf -c foo crlf2.txt", "--crlf -l foo a.txt",
		"--crlf --color always x? empty.txt crlf.txt", "-f pats.txt a.txt", "-f none.pat a.txt", "-f pats.txt -e nothing a.txt",
		"-f nosuch.pat a.txt", "-F -f pats.txt a.txt", "-f bad.pat a.txt", "foo u16le.txt", "-b caf u16le.txt", "foo u16be.txt",
		"foo u16nb.txt", "-E utf-16le foo u16nb.txt", "-E none foo u16le.txt", "-E latin1 foo latin.txt",
		"-E utf-8 foo latin.txt", "-E bogus foo latin.txt", "-E UTF-16LE foo u16nb.txt", "-E latin1 caf u16le.txt",
		"--no-encoding foo u16le.txt", "foo bom8.txt", "-E none caf bom8.txt", "-p foo a.txt", "--no-unicode \\w+ uni.txt",
		"-c foo a.txt --stats", "foo a.txt --stats", "-j1 foo a.txt nothing.txt --stats", "--stats zzz a.txt",
		"--stats -q foo a.txt", "--stats -l foo a.txt", "--stats -v foo a.txt", "--stats -o foo a.txt",
		"--stats -C1 -j1 foo a.txt w.txt", "--stats --heading -j1 foo a.txt w.txt", "--json foo a.txt", "--json -C1 foo a.txt",
		"--json -c foo a.txt", "--json zzz a.txt", "--json -v foo a.txt", "--json -n --column -b foo w.txt",
		"--json --stats foo w.txt", "--json foo bom8.txt", "--json -j1 foo a.txt w.txt",
		"-U abc\\ndef ml.txt", "-Un abc\\ndef ml.txt hw.txt", "-Uc abc\\ndef ml.txt", "-U --count-matches abc\\ndef ml.txt",
		"-Uno abc\\ndef ml.txt", "-Uno -b --column c\\nd ml.txt", "-Uno --column -r X c\\nd ml.txt",
		"-Un --column abc\\ndef ml.txt", "-Un --vimgrep c\\nd ml.txt", "-Un -r X abc\\ndef ml.txt", "-Unv abc\\ndef ml.txt",
		"-Uvc abc\\ndef ml.txt", "-Un -C1 abc\\ndef ml.txt", "-Un -m1 abc\\ndef ml.txt", "-U --json abc\\ndef ml.txt",
		"-Un c$ ml.txt", "-Un --column xxx ml.txt", "-Un -r ? \\n hw.txt", "-Un --multiline-dotall one.*?xxx ml.txt",
		"-Un one.*?xxx ml.txt", "-Un --passthru c\\nd ml.txt", "-Un --trim -M8 --max-columns-preview .*c\\nd.* ml.txt",
		"-Uo -M3 --max-columns-preview c\\nd ml.txt", "-Un --crlf two\\r?\\nthree crlf.txt", "-U --stats c\\nd ml.txt",
		"--multiline --no-multiline c\\nd ml.txt", "-U --color always c\\nd ml.txt", "-l -U c\\nd ml.txt hw.txt",
		"c\\nd ml.txt", "--null-data a\\x00b ml.txt", "--null-data -c c\\nd ml.txt", "--crlf c\\rd ml.txt",
		"-c [\\n]|abc ml.txt", "-c \\s ml.txt", "-U -c [\\n] ml.txt", "-c \\x0a ml.txt",
	}
	var cases []rgCase
	for _, l := range lines {
		cases = append(cases, rgCase{args: rgArgs(l)})
	}
	return append(cases,
		rgCase{args: rgArgs("--json foo -"), stdin: "x foo\n"},
		rgCase{args: rgArgs("-f - a.txt"), stdin: "gamma\nlast\n"},
		rgCase{args: rgArgs("foo a.txt"), pty: true},
		rgCase{args: rgArgs("-j1 foo a.txt w.txt"), pty: true},
		rgCase{args: rgArgs("-j1 -N --no-heading foo a.txt w.txt"), pty: true},
		rgCase{args: rgArgs("-c -j1 foo a.txt w.txt"), pty: true},
		rgCase{args: rgArgs("foo a.txt"), pty: true, term: "xterm"},
		rgCase{args: rgArgs("foo a.txt"), pty: true, term: "dumb"},
	)
}

// rgLateCases give --colors, the links, the field separators, the special
// modes, the config file, the flags of the engine and the flags that
// ripgrep suggests for a name that it does not know.
func rgLateCases() []rgCase {
	lines := []string{
		"--color always --colors match:fg:blue -n foo a.txt",
		"--color always --colors match:bg:0x10 --colors match:style:underline --colors match:style:intense -n foo a.txt",
		"--color always --colors line:fg:1,2,3 --colors column:fg:yellow --column -n foo a.txt",
		"--color always --colors path:none -j1 -H foo a.txt w.txt",
		"--color always --colors match:none -n -o foo a.txt",
		"--color always --colors match:none -M 5 foo long.md",
		"--color always --colors MATCH:STYLE:NOBOLD --colors match:fg:+5 foo a.txt",
		"--colors match:fg:purple foo a.txt", "--colors match:fg:300 foo a.txt", "--colors match:fg:1,2 foo a.txt",
		"--colors bogus:fg:red foo a.txt", "--colors match:xx:red foo a.txt", "--colors match:fg foo a.txt",
		"--colors match:style:x foo a.txt",
		"--color always --hyperlink-format default -H foo a.txt",
		"--color always --hyperlink-format vscode -j1 -n -H foo a.txt w.txt",
		"--color always --hyperlink-format vscode -j1 --heading -n foo a.txt w.txt",
		"--color always --hyperlink-format file -j1 --heading -n foo a.txt w.txt",
		"--color always --hyperlink-format kitty -j1 -c foo a.txt w.txt",
		"--color always --hyperlink-format kitty -j1 -l foo a.txt w.txt",
		"--color always --hyperlink-format vscode --vimgrep foo a.txt",
		"--color always --hyperlink-format vscode -o -b -H foo a.txt",
		"--color always --hyperlink-format vscode -A1 -n -H foo a.txt",
		"--color always --hyperlink-format grep+ -H -U foo\\nnothing a.txt",
		"--color always --hyperlink-format vscode -n -H --colors match:none foo a.txt",
		"--color always --hyperlink-format x:{{path}}{path} --null -H foo a.txt",
		"--color always --hyperlink-format kitty --hostname-bin nosuchprogram -H foo a.txt",
		"--color always --hyperlink-format kitty -H foo bin.dat",
		"--hyperlink-format vscode -n -H foo a.txt",
		"--hyperlink-format foo foo a.txt", "--hyperlink-format x://{line} foo a.txt", "--hyperlink-format {path} foo a.txt",
		"--hyperlink-format x:{path}} foo a.txt", "--hyperlink-format x:{path foo a.txt", "--hyperlink-format x:{bad} foo a.txt",
		"--hyperlink-format x://{path}{column} foo a.txt",
		"--vimgrep -r X foo a.txt", "--color always --vimgrep -r X foo a.txt", "--vimgrep -M 8 --max-columns-preview foo a.txt",
		"--color always --vimgrep -M 8 --max-columns-preview foo a.txt", "-o -M 2 --max-columns-preview foo a.txt",
		"--column -M 3 foo a.txt", "--column -M 8 --max-columns-preview foo a.txt",
		"--field-match-separator @@ --field-context-separator ## -n -C1 -H foo a.txt",
		"--field-match-separator @@ -c -H foo a.txt", "--field-match-separator @@ --vimgrep foo a.txt",
		"--field-match-separator @@ --heading -n -H foo a.txt", "--color always --field-match-separator @@ -n -H --column foo a.txt",
		"--field-match-separator \\x41\\t -n foo a.txt", "--field-context-separator \\xff -n -C1 foo a.txt",
		"-h", "--help", "-V", "-hV foo", "--help --bogus", "--generate man",
		"--generate complete-bash", "--generate complete-zsh", "--generate complete-fish", "--generate complete-powershell",
		"--generate bogus", "--generate man -c",
		"--engine bogus foo a.txt", "--engine default foo a.txt", "--engine auto foo a.txt", "--dfa-size-limit 10 foo a.txt",
		"--dfa-size-limit 1Q foo a.txt", "--regex-size-limit 99999999999999999999 foo a.txt", "--regex-size-limit 1 foo a.txt",
		"--line-buffered foo a.txt", "--block-buffered foo a.txt", "--no-line-buffered --no-block-buffered foo a.txt",
		"--debug-x foo a.txt", "--hiden foo a.txt", "--colr foo a.txt", "--pcre foo a.txt", "--no-vimgrep foo a.txt",
		"--one-file-system foo a.txt", "--no-one-file-system foo a.txt", "--no-config foo a.txt",
		"--auto-hybrid-regex --no-auto-hybrid-regex foo a.txt", "--no-pcre2-unicode \\w+ uni.txt", "--pcre2-unicode \\w+ uni.txt",
		// The errors of a pattern, as regex-syntax writes them, and the names
		// of the Unicode properties.
		"* a.txt", "a{2,1} a.txt", "(?P<n>a)(?P<n>b) a.txt", "(?iii) a.txt", "\\p{Foo=bar} a.txt", "\\p{sc=Foo} a.txt",
		"-w [z-a] a.txt", "--no-unicode \\p{L} a.txt", "( a.txt", "-i -e foo -e \\x{110000} a.txt",
		"[a-\\w] a.txt", "a{1 a.txt", "\\b{foo} a.txt", "(?x)a{ 1 } a.txt", "[o--f] a.txt", "\\p{Grek}+ uni.txt",
		"\\p{IsGreek}+ uni.txt", "\\p{sc!=Greek}+ uni.txt", "\\P{sc!=Greek}+ uni.txt", "\\p{Emoji} uni.txt", "\\p{wspace} uni.txt",
		// The encodings of the Encoding Standard.
		"-E sjis foo sjis.txt", "-E Shift_JIS -o \\p{Hiragana}+ sjis.txt", "-E windows-31j --passthru x sjis.txt",
		"-E euc-jp --passthru x eucjp.txt", "-E euc-kr foo euckr.txt", "-E gbk --passthru x gbk.txt",
		"-E gb18030 --passthru x gbk.txt", "-E big5 --passthru x big5.txt", "-E koi8-r -o \\w+ koi8.txt",
		"-E iso-2022-jp --passthru x iso2022.txt", "-E csiso2022kr foo a.txt", "-E auto foo a.txt", "-E NONE foo a.txt",
		"-E x-user-defined --passthru x latin.txt", "-E windows-1251 --passthru x latin.txt", "-E iso-8859-8-i --passthru x latin.txt",
		// The preprocessor of --pre, and -z.
		"--pre ./pre.sh foo a.txt", "--pre ./pre.sh -j1 pre a.txt w.txt", "--pre ./fail.sh foo a.txt", "--pre ./fail.sh -c foo a.txt",
		"--pre ./fail.sh -l foo a.txt", "--pre ./fail.sh -l zzz a.txt", "--pre ./quiet.sh foo a.txt", "--pre nosuchprogram foo a.txt",
		"--pre ./pre.sh --pre-glob *.md -j1 foo a.txt long.md", "--pre ./pre.sh --pre-glob !*.md -j1 pre a.txt long.md",
		"--pre ./pre.sh foo nosuch.txt", "--pre ./pre.sh --no-pre pre a.txt", "--pre= pre a.txt", "--pre ./pre.sh -z pre a.txt",
		"-z foo z/t.gz", "-z -c foo z/t.bz2", "-z -n foo z/t.xz", "-z foo z/t.lzma", "-z foo z/t.Z", "-z foo z/trunc.xz",
		"-z foo z/bad.gz", "-z foo z/bad.bz2", "-z foo z/bad.xz", "-z -j1 --sort path foo z", "--search-zip -l foo z/t.gz",
		"-z --no-search-zip foo z/t.xz", "-z foo a.txt",
	}
	var cases []rgCase
	for _, l := range lines {
		cases = append(cases, rgCase{args: rgArgs(l)})
	}
	config := func(line, file string) rgCase {
		return rgCase{args: rgArgs(line), env: []string{"RIPGREP_CONFIG_PATH=DIR/" + file}}
	}
	return append(cases,
		config("foo a.txt", "rgconfig"),
		config("--no-config foo a.txt", "rgconfig"),
		config("-N --colors match:fg:red foo a.txt", "rgconfig"),
		config("foo a.txt", "badconfig"),
		config("foo a.txt", "nosuchconfig"),
		config("--no-messages foo a.txt", "nosuchconfig"),
		config("-V", "badconfig"),
	)
}

// rgTimes match the times of --stats and --json, which differ from run to
// run.
var rgTimes = regexp.MustCompile(`"elapsed(_total)?":\{[^}]*\}|(?m)^[0-9]+\.[0-9]+ seconds`)

func rgMasked(text string) string { return rgTimes.ReplaceAllString(text, "TIME") }

type rgResult struct {
	stdout, stderr string
	code           int
}

// sorted is the result with the lines of the stdout in order.
func (r rgResult) sorted() rgResult {
	lines := strings.SplitAfter(r.stdout, "\n")
	sort.Strings(lines)
	return rgResult{strings.Join(lines, ""), r.stderr, r.code}
}

// rgRun runs one case in `dir`, or in the directory of the case below it.
// The home directory is empty, so that no global gitignore applies.
func rgRun(t *testing.T, cmd *exec.Cmd, dir string, c rgCase) rgResult {
	t.Helper()
	cmd.Dir = filepath.Join(dir, c.dir)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(dir, "home")}
	if c.term != "" {
		cmd.Env = append(cmd.Env, "TERM="+c.term)
	}
	for _, e := range c.env {
		cmd.Env = append(cmd.Env, strings.ReplaceAll(e, "DIR", dir))
	}
	if !c.noStdin && !c.pty {
		cmd.Stdin = strings.NewReader(c.stdin)
	}
	if c.pty {
		return rgRunPty(t, cmd)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", cmd.Path, &stderr)
	}
	return rgResult{rgMasked(stdout.String()), stderr.String(), cmd.ProcessState.ExitCode()}
}

// rgRunPty runs a case with its standard output on a terminal.
func rgRunPty(t *testing.T, cmd *exec.Cmd) rgResult {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("open a pty: %v", err)
	}
	defer master.Close()
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = slave, &stderr
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatalf("%s did not start: %v", cmd.Path, err)
	}
	slave.Close()
	out := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(master)
		out <- b
	}()
	_ = cmd.Wait()
	return rgResult{string(<-out), stderr.String(), cmd.ProcessState.ExitCode()}
}

// rgTree writes the tree that the walk cases search. `tree` is a git
// repository with each kind of ignore file. `plain` has a .gitignore and
// no .git, so that ripgrep does not use the .gitignore.
func rgTree(t *testing.T, dir string) {
	t.Helper()
	var big strings.Builder
	for i := 0; big.Len() < 70000; i++ {
		fmt.Fprintf(&big, "line %d of the text\n", i)
		if i%500 == 0 {
			big.WriteString("foo before the NUL byte\n")
		}
	}
	big.WriteString("abc\x00def foo\nfoo after the NUL byte\n")
	files := map[string]string{
		"home/.keep":             "",
		"tree/.git/info/exclude": "excluded.txt\n",
		"tree/.gitignore":        "*.log\n/build/\n!keep.log\nsub/deep/\n# a comment\n\\#hash.txt\n",
		"tree/.ignore":           "dot_ignored.txt\n",
		"tree/.rgignore":         "!dot_ignored.txt\nrg_ignored.txt\n",
		"tree/a.txt":             "foo here\nbar\n",
		"tree/b.log":             "foo log\n",
		"tree/keep.log":          "foo keep\n",
		"tree/#hash.txt":         "foo hash\n",
		"tree/build/out.txt":     "foo build\n",
		"tree/src/build/x.txt":   "foo src build\n",
		"tree/src/main.rs":       "fn foo() {}\n",
		"tree/src/lib.c":         "int foo;\n",
		"tree/src/Makefile":      "foo: bar\n",
		"tree/sub/.gitignore":    "*.md\n",
		"tree/sub/readme.md":     "foo md\n",
		"tree/sub/y.txt":         "foo y\nfoo y again\n",
		"tree/sub/deep/z.txt":    "foo deep\n",
		"tree/.hidden.txt":       "foo hidden\n",
		"tree/.hdir/h.txt":       "foo hdir\n",
		"tree/dot_ignored.txt":   "foo dot\n",
		"tree/rg_ignored.txt":    "foo rg\n",
		"tree/excluded.txt":      "foo excluded\n",
		"tree/bin.dat":           "foo\x00bar\n",
		"tree/big.txt":           big.String(),
		"tree/extra.ignore":      "a.txt\n",
		"plain/.gitignore":       "*.txt\n",
		"plain/p.txt":            "foo plain\n",
		"plain/q.md":             "foo q\n",
		"plain/nested/.ignore":   "r.md\n",
		"plain/nested/r.md":      "foo r\n",
		"plain/nested/s.md":      "foo s\n",
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "tree", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(dir, "tree", "linkdir")); err != nil {
		t.Fatal(err)
	}
}

// rgWalkCases search the trees of rgTree. ripgrep writes the files of a
// walk in the order of the walk only with one thread, so each case gives
// -j1 or --sort. A case with no path runs with no standard input, as
// ripgrep then searches the current directory.
func rgWalkCases() []rgCase {
	in := func(dir, line string) rgCase { return rgCase{args: rgArgs(line), dir: dir, noStdin: true} }
	return []rgCase{
		in("tree", "-j1 foo"),
		in("tree", "--sort path foo"),
		in("tree", "--sort path -n foo ."),
		in("tree/src", "--sort path foo"),
		in("tree/sub", "--sort path foo ../src"),
		in("", "--sort path foo tree"),
		in("", "--sort path foo tree/"),
		in("", "--sortr path foo tree"),
		in("", "--sort path foo tree plain"),
		in("", "--sort path foo tree/a.txt tree/b.log tree/.hidden.txt"),
		in("", "--sort path --hidden foo tree"),
		in("", "--sort path -. foo tree"),
		in("", "--sort path --no-ignore foo tree"),
		in("", "--sort path --no-ignore --ignore foo tree"),
		in("", "--sort path -u foo tree"),
		in("", "--sort path -uu foo tree"),
		in("", "--sort path -uuu foo tree"),
		in("", "--sort path -uuuu foo tree"),
		in("", "--sort path --no-ignore-vcs foo tree"),
		in("", "--sort path --no-ignore-dot foo tree"),
		in("", "--sort path --no-ignore-exclude foo tree"),
		in("", "--sort path --no-ignore-parent foo"),
		in("plain", "--sort path foo"),
		in("plain", "--sort path --no-require-git foo"),
		in("", "--sort path --ignore-file tree/extra.ignore foo tree"),
		in("", "--sort path --ignore-file nofile foo tree"),
		in("", "--sort path --max-depth 1 foo tree"),
		in("", "--sort path --maxdepth=0 foo tree"),
		in("", "--sort path -L foo tree"),
		in("", "--sort path -g *.txt foo tree"),
		in("", "--sort path -g !*.txt foo tree"),
		in("", "--sort path -g *.txt -g !sub/** foo tree"),
		in("", "--sort path -g *.log foo tree"),
		in("", "--sort path --iglob *.TXT foo tree"),
		in("", "--sort path --glob-case-insensitive -g *.TXT foo tree"),
		in("", "--sort path -g [ foo tree"),
		in("", "--sort path -t rust foo tree"),
		in("", "--sort path -t c -t make foo tree"),
		in("", "--sort path -T txt foo tree"),
		in("", "--sort path --type-add log:*.log -t log --no-ignore foo tree"),
		in("", "--sort path --type-add bad foo tree"),
		in("", "--sort path -t bogus foo tree"),
		in("", "--sort path --type-clear rust -t rust foo tree"),
		in("", "--type-add zz:*.zz --type-list"),
		in("", "--sort path --files tree"),
		in("", "--sort path --files --null tree"),
		in("", "--sort path --files -t rust tree"),
		in("", "--sortr path --files tree plain"),
		in("", "--sort path -l foo tree"),
		in("", "--sort path -l --null foo tree"),
		in("", "--sort path --files-without-match here tree"),
		in("", "--sort path -c foo tree"),
		in("", "--sort path -c --null foo tree"),
		in("", "--sort path -co foo tree"),
		in("", "--sort path -q foo tree"),
		in("", "--sort path -q zzz tree"),
		in("", "--sort path -m1 foo tree"),
		in("", "--sort path -m1 -A1 foo tree/sub"),
		in("", "--sort path -m0 foo tree"),
		in("", "--sort path -x foo.here tree"),
		in("", "--sort path -n --null foo tree/sub"),
		in("", "--sort path --heading foo tree"),
		in("", "--sort path --heading -n -A1 foo tree/sub tree/a.txt"),
		in("", "--sort path --no-heading foo tree/sub"),
		in("", "--sort path --path-separator \\ foo tree/sub"),
		in("", "--sort path -C1 foo tree/sub tree/a.txt"),
		in("", "--sort path --mmap foo tree"),
		in("", "--sort path --no-mmap foo tree"),
		in("", "--sort path foo tree/big.txt"),
		in("", "--sort path --no-mmap foo tree/big.txt"),
		in("", "--sort path -c foo tree/big.txt"),
		in("", "--sort path --no-mmap -c foo tree/big.txt"),
		in("", "--sort path -g big.txt foo tree"),
		in("", "--sort path -g big.txt -c foo tree"),
		in("", "--sort path -g big.txt -l foo tree"),
		in("", "--sort path -g big.txt --binary foo tree"),
		in("", "--sort path -g big.txt -a -c foo tree"),
		in("", "--sort path -g big.txt -B1 foo tree"),
		in("", "--sort path --max-filesize 70 foo tree"),
		in("", "--sort path --max-filesize 1Q foo tree"),
		in("", "--sort path -c --include-zero foo tree/sub tree/src"),
		in("", "--sort path -C1 --context-separator=@@ foo tree/sub tree/a.txt"),
		in("", "--sort path -A1 --no-context-separator foo tree/sub tree/a.txt"),
		in("", "--sort path foo nosuchdir tree/sub"),
		in("tree", "-j1 -g nomatch foo"),
		in("", "foo tree/sub"),
		{args: rgArgs("-j2 -c foo tree"), noStdin: true, anyOrder: true},
		{args: rgArgs("-j3 -n foo tree"), noStdin: true, anyOrder: true},
		{args: rgArgs("-j4 -l foo tree plain"), noStdin: true, anyOrder: true},
		{args: rgArgs("-j2 --files tree"), noStdin: true, anyOrder: true},
	}
}

// rgOracle fails the test when /usr/bin/rg is not ripgrep 14.1.0.
func rgOracle(t *testing.T) {
	t.Helper()
	version, err := exec.Command(ripgrep, "--version").Output()
	if err != nil {
		t.Fatalf("ripgrep 14.1.0 is required at %s: %v", ripgrep, err)
	}
	if !strings.HasPrefix(string(version), "ripgrep 14.1.0") {
		t.Fatalf("the oracle is ripgrep 14.1.0, but %s is %q", ripgrep, strings.SplitN(string(version), "\n", 2)[0])
	}
}

// rgPort builds the port for `target` and gives the command that runs it.
func rgPort(t *testing.T, cli *selfHostCLI, target string) func(args ...string) *exec.Cmd {
	t.Helper()
	src, err := filepath.Abs("../../../examples/rg/rg.fern")
	if err != nil {
		t.Fatal(err)
	}
	if target == "x86-64-linux" {
		bin := cli.x86Binary(t, src)
		return func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
	}
	_, qemu := arm64Tooling(t)
	bin := cli.arm64Binary(t, src)
	return func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
}

// TestSelfHostExampleRgAgainstRipgrep runs the port and ripgrep 14.1.0 with
// the same command lines on the same files. The stdout, the stderr and the
// exit status must be the same.
func TestSelfHostExampleRgAgainstRipgrep(t *testing.T) {
	rgOracle(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	rgFixtures(t, dir)
	rgTree(t, dir)
	cases := rgCases(dir)
	if len(cases) < 60 {
		t.Fatalf("%d cases, fewer than 60", len(cases))
	}
	want := make([]rgResult, len(cases))
	for i, c := range cases {
		want[i] = rgRun(t, exec.Command(ripgrep, c.args...), dir, c)
		if c.anyOrder {
			want[i] = want[i].sorted()
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := rgPort(t, cli, target)
			for i, c := range cases {
				got := rgRun(t, port(c.args...), dir, c)
				if c.anyOrder {
					got = got.sorted()
				}
				if got != want[i] {
					t.Errorf("rg %q in %q, stdin %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
						c.args, c.dir, c.stdin, got.code, got.stdout, got.stderr, want[i].code, want[i].stdout, want[i].stderr)
				}
			}
			t.Logf("%d command lines give the output of ripgrep", len(cases))
		})
	}
}

// TestSelfHostExampleRgWithoutPcre2 checks that the port behaves as
// ripgrep 14.1.0 built without PCRE2. The oracle has PCRE2, so the test
// compares the port with the text of that build.
func TestSelfHostExampleRgWithoutPcre2(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	rgFixtures(t, dir)
	const noPcre2 = "PCRE2 is not available in this build of ripgrep."
	divider := strings.Repeat("~", 79)
	cases := []struct {
		args string
		want rgResult
	}{
		{"-P foo a.txt", rgResult{"", "rg: " + noPcre2 + "\n", 2}},
		{"--engine pcre2 foo a.txt", rgResult{"", "rg: " + noPcre2 + "\n", 2}},
		{"--pcre2-version", rgResult{noPcre2 + "\n", "", 1}},
		{"--engine auto -c foo a.txt", rgResult{"3\n", "", 0}},
		{"--engine auto a{2,1} a.txt", rgResult{"", "rg: regex could not be compiled with either the default regex engine or with PCRE2.\n\n" +
			"default regex engine error:\n" + divider + "\nregex parse error:\n    (?:a{2,1})\n        ^^^^^\n" +
			"error: invalid repetition count range, the start must be <= the end\n" + divider + "\n\nPCRE2 regex engine error:\n" + noPcre2 + "\n", 2}},
		{"\\1 a.txt", rgResult{"", "rg: regex parse error:\n    (?:\\1)\n       ^^\nerror: backreferences are not supported\n", 2}},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := rgPort(t, cli, target)
			for _, c := range cases {
				got := rgRun(t, port(rgArgs(c.args)...), dir, rgCase{args: rgArgs(c.args)})
				if got != c.want {
					t.Errorf("rg %s:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
						c.args, got.code, got.stdout, got.stderr, c.want.code, c.want.stdout, c.want.stderr)
				}
			}
		})
	}
}

// rgVendored is a case of ripgrep's integration tests, as
// examples/rg/testdata/ripgrep-tests.json holds it.
type rgVendored struct {
	Name  string        `json:"name"`
	Args  []string      `json:"args"`
	Cwd   string        `json:"cwd"`
	Stdin *rgVendorFile `json:"stdin"`
	Setup []rgVendorOp  `json:"setup"`
}

// rgVendorFile is the content of a file: text, base64 bytes, a constant of
// the tests, a file of testdata, or a count of NUL bytes.
type rgVendorFile struct {
	Text   *string `json:"text"`
	Base64 string  `json:"base64"`
	Const  string  `json:"const"`
	Data   string  `json:"data"`
	Size   int64   `json:"size"`
}

// rgVendorOp is one step of a case: make a file or a directory, remove a
// file, or make a symbolic link to a path in the directory of the case.
type rgVendorOp struct {
	Op     string `json:"op"`
	Path   string `json:"path"`
	Target string `json:"target"`
	rgVendorFile
}

type rgVendorSuite struct {
	Consts   map[string]string `json:"consts"`
	Cases    []rgVendored      `json:"cases"`
	testdata string
}

func (s *rgVendorSuite) bytes(t *testing.T, f rgVendorFile) []byte {
	t.Helper()
	switch {
	case f.Text != nil:
		return []byte(*f.Text)
	case f.Base64 != "":
		b, err := base64.StdEncoding.DecodeString(f.Base64)
		if err != nil {
			t.Fatal(err)
		}
		return b
	case f.Const != "":
		return []byte(s.Consts[f.Const])
	case f.Data != "":
		b, err := os.ReadFile(filepath.Join(s.testdata, f.Data))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return make([]byte, f.Size)
}

func (s *rgVendorSuite) setup(t *testing.T, dir string, c rgVendored) {
	t.Helper()
	for _, op := range c.Setup {
		path := filepath.Join(dir, op.Path)
		var err error
		switch op.Op {
		case "file":
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				err = os.WriteFile(path, s.bytes(t, op.rgVendorFile), 0o644)
			}
		case "dir":
			err = os.MkdirAll(path, 0o755)
		case "remove":
			err = os.Remove(path)
		case "symlink":
			err = os.Symlink(filepath.Join(dir, op.Target), path)
		default:
			t.Fatalf("%s: unknown step %q", c.Name, op.Op)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
	}
}

// run runs one case in a new directory. Each run gets --path-separator /
// first, as ripgrep's harness gives it, and -j1, so that ripgrep writes the
// files in the order of its walk. The home directory is empty, so that no
// global gitignore applies, and the standard input is /dev/null unless the
// case gives one.
func (s *rgVendorSuite) run(t *testing.T, prog func(args ...string) *exec.Cmd, c rgVendored) rgResult {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	s.setup(t, dir, c)
	cmd := prog(append([]string{"--path-separator", "/", "-j1"}, c.Args...)...)
	cmd.Dir = filepath.Join(dir, c.Cwd)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(s.bytes(t, *c.Stdin))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", cmd.Path, &stderr)
	}
	// The temporary directory in a message differs from run to run.
	return rgResult{stdout.String(), strings.ReplaceAll(stderr.String(), dir, "DIR"), cmd.ProcessState.ExitCode()}
}

// TestSelfHostExampleRgVendoredCases runs the cases of the integration
// tests of ripgrep 14.1.1 with the port and with ripgrep 14.1.0. The
// stdout, the stderr and the exit status must be the same for exactly the
// cases that testdata/ripgrep-pass.txt names. A named case that fails is a
// regression, and a case that passes must be named.
func TestSelfHostExampleRgVendoredCases(t *testing.T) {
	rgOracle(t)
	testdata, err := filepath.Abs("../../../examples/rg/testdata")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(testdata, "ripgrep-tests.json"))
	if err != nil {
		t.Fatal(err)
	}
	suite := rgVendorSuite{testdata: testdata}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	pinned := rgPinned(t, filepath.Join(testdata, "ripgrep-pass.txt"))
	oracle := func(args ...string) *exec.Cmd { return exec.Command(ripgrep, args...) }
	want := make([]rgResult, len(suite.Cases))
	for i, c := range suite.Cases {
		want[i] = suite.run(t, oracle, c)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := rgPort(t, cli, target)
			pass := 0
			for i, c := range suite.Cases {
				got := suite.run(t, port, c)
				same := got == want[i]
				if same {
					pass++
				}
				if same && !pinned[c.Name] {
					t.Errorf("%s passes, but testdata/ripgrep-pass.txt does not name it", c.Name)
				}
				if !same && pinned[c.Name] {
					t.Errorf("%s regressed: rg %q:\ngot  exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
						c.Name, c.Args, got.code, got.stdout, got.stderr, want[i].code, want[i].stdout, want[i].stderr)
				}
			}
			t.Logf("%d of %d vendored cases give the output of ripgrep", pass, len(suite.Cases))
		})
	}
}

// rgPinned reads the names of the cases that must pass.
func rgPinned(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			pinned[line] = true
		}
	}
	return pinned
}
