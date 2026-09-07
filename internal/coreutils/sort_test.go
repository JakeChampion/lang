package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sortCases is sort(1)'s corpus.
//
// sort has more surface than anything else here, and most of it is in
// how a key is CUT rather than in how two keys compare: a field starts
// at the blanks in front of it, `-k1` is the whole line, `-k2,1` is
// empty, and every one of `bdfgiMhnRrV` can be attached to one end of
// one key and override the global option there. The corpus is built
// around that, then around the six ordering modes (each with the input
// that separates it from a byte comparison), then around the option
// parsing — glibc's ambiguity lists, the obsolete `+POS -POS` form and
// the environment that turns it off, and the four numeric options
// whose only observable effect is their own diagnostics.
//
// `-R` is deterministic only with --random-source, so every random case
// names one; the default source is the kernel's and is a shuffle.
func sortCases(t *testing.T) []invocation {
	dir := t.TempDir()
	ab := catFile(t, dir, "ab", "a\nb\n")
	ba := catFile(t, dir, "ba", "b\na\n")
	cd := catFile(t, dir, "cd", "c\nd\n")
	nonl := catFile(t, dir, "nonl", "b\na")
	empty := catFile(t, dir, "e0", "")
	blanksFile := catFile(t, dir, "blanks", "  b\n a\nc\n")
	nums := catFile(t, dir, "nums", "10\n9\n100\n1\n")
	dupes := catFile(t, dir, "dupes", "a\na\nb\nb\nb\nc\n")
	fields := catFile(t, dir, "fields", "b 2 x\na 3 y\nc 1 z\n")
	colons := catFile(t, dir, "colons", "b:2:x\na:3:y\nc:1:z\n")
	zeros := catFile(t, dir, "zeros", "b\x00a\x00c\x00")
	raw := catFile(t, dir, "raw", "\xff\n\x01\na\n")
	spaced := catFile(t, dir, "f name", "z\ny\n")
	quoted := catFile(t, dir, "f'n", "z\ny\n")
	rawName := catFile(t, dir, "na\xffme", "z\ny\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")

	// A fixed random source: sixteen zero bytes is what GNU reads out
	// of /dev/zero, so both sides seed the same MD5 state.
	randSrc := catFile(t, dir, "rand16", strings.Repeat("\x00", 64))
	randShort := catFile(t, dir, "rand2", "ab")

	// Names for --files0-from.
	names0 := catFile(t, dir, "names0", ab+"\x00"+cd+"\x00")
	namesEmpty := catFile(t, dir, "names1", ab+"\x00\x00")
	namesDash := catFile(t, dir, "names2", "-\x00")

	// Enough lines to cross a dozen merge passes, with duplicate keys so
	// stability is observable. Kept to five thousand because the
	// self-host leg runs the same corpus over a build with no -O, where
	// a comparison costs far more than it does natively.
	big := catFile(t, dir, "big", shuffledLines(5000))
	bigStdin := shuffledLines(5000)

	return []invocation{
		// The plain sort.
		{name: "stdin", args: nil, stdin: "b\na\nc\n"},
		{name: "one file", args: []string{ba}},
		{name: "two files", args: []string{ba, cd}},
		{name: "file and stdin", args: []string{ba, "-"}, stdin: "e\n"},
		{name: "dash alone", args: []string{"-"}, stdin: "b\na\n"},
		{name: "empty input", args: nil, stdin: ""},
		{name: "empty file", args: []string{empty}},
		{name: "empty file among files", args: []string{empty, ba}},
		{name: "no final newline", args: []string{nonl}},
		{name: "no final newline stdin", args: nil, stdin: "b\na"},
		{name: "two files one unterminated", args: []string{nonl, cd}},
		{name: "only newlines", args: nil, stdin: "\n\n\n"},
		{name: "one blank line", args: nil, stdin: "\n"},
		{name: "high bytes", args: []string{raw}},
		{name: "embedded nul", args: nil, stdin: "b\x00a\na\x00b\n"},
		{name: "long lines", args: nil, stdin: strings.Repeat("z", 5000) + "\n" + strings.Repeat("y", 5000) + "\n"},
		{name: "big file", args: []string{big}},
		{name: "big stdin", args: nil, stdin: bigStdin},

		// -r, -u, -s.
		{name: "reverse", args: []string{"-r", ba}},
		{name: "reverse long", args: []string{"--reverse", ba}},
		{name: "unique", args: []string{"-u", dupes}},
		{name: "unique long", args: []string{"--unique", dupes}},
		{name: "unique reverse", args: []string{"-ur", dupes}},
		{name: "stable", args: []string{"-s", ba}},
		{name: "stable long", args: []string{"--stable", ba}},
		{name: "stable with a key", args: []string{"-s", "-k1,1", fields}},
		{name: "unique with a key", args: []string{"-u", "-k1,1", fields}},
		{name: "unique folded", args: []string{"-uf"}, stdin: "a\nA\nB\nb\n"},
		{name: "unique numeric", args: []string{"-nu"}, stdin: "1\n1.0\n01\n2\n"},
		{name: "unique big", args: []string{"-u", big}},
		{name: "reverse big", args: []string{"-r", big}},

		// -z.
		{name: "zero terminated", args: []string{"-z", zeros}},
		{name: "zero terminated long", args: []string{"--zero-terminated", zeros}},
		{name: "zero terminated stdin", args: []string{"-z"}, stdin: "b\x00a\x00"},
		{name: "zero terminated unterminated", args: []string{"-z"}, stdin: "b\x00a"},
		{name: "zero terminated newlines in lines", args: []string{"-z"}, stdin: "b\nx\x00a\ny\x00"},
		{name: "zero terminated fields", args: []string{"-z", "-k2"}, stdin: "b\nx\x00a\ny\x00"},
		{name: "zero terminated empty", args: []string{"-z"}, stdin: ""},

		// -n.
		{name: "numeric", args: []string{"-n", nums}},
		{name: "numeric long", args: []string{"--numeric-sort", nums}},
		{name: "numeric signs", args: []string{"-n"}, stdin: "  -1\n  +1\n 0\n1e2\n.5\n-.5\n0.0\n-0\n"},
		{name: "numeric leading zeros", args: []string{"-n"}, stdin: "007\n7\n0007.0\n"},
		{name: "numeric fractions", args: []string{"-n"}, stdin: "1.5\n1.25\n1.250\n1.2\n1.\n1\n"},
		{name: "numeric no digits", args: []string{"-n"}, stdin: "x\ny\n-\n.\n-.\n\n"},
		{name: "numeric thousands", args: []string{"-n"}, stdin: "1,000\n999\n2\n"},
		{name: "numeric huge", args: []string{"-n"}, stdin: "99999999999999999999999\n99999999999999999999998\n1\n"},
		{name: "numeric negative zeros", args: []string{"-n"}, stdin: "-0\n-0.0\n0\n--1\n"},
		{name: "numeric blanks", args: []string{"-n"}, stdin: "\t3\n \t2\n"},
		{name: "numeric reverse", args: []string{"-nr", nums}},
		{name: "numeric stable", args: []string{"-ns"}, stdin: "1b\n1a\n"},
		{name: "numeric then reverse", args: []string{"-n", "-r"}, stdin: "1b\n1a\n"},
		{name: "numeric and version incompatible", args: []string{"-nV"}},
		{name: "numeric and month incompatible", args: []string{"-nM"}},
		{name: "numeric and general incompatible", args: []string{"-ng"}},
		{name: "numeric and dictionary incompatible", args: []string{"-nd"}},
		{name: "numeric and ignore incompatible", args: []string{"-ni"}},
		{name: "dictionary fold numeric incompatible", args: []string{"-dfn"}},
		{name: "month and random incompatible", args: []string{"-MR"}},
		{name: "random and version compatible", args: []string{"-RV", "--random-source=" + randSrc}, stdin: "a\nb\n"},
		{name: "random and dictionary compatible", args: []string{"-Rd", "--random-source=" + randSrc}, stdin: "a\nb\n"},
		{name: "version and dictionary compatible", args: []string{"-Vd"}, stdin: "a\nb\n"},
		{name: "dictionary and ignore compatible", args: []string{"-di"}, stdin: "a\nb\n"},

		// -h.
		{name: "human", args: []string{"-h"}, stdin: "1K\n1M\n1\n-1K\n0K\n1000\n1.5K\n"},
		{name: "human long", args: []string{"--human-numeric-sort"}, stdin: "2K\n1G\n"},
		{name: "human every suffix", args: []string{"-h"}, stdin: "1Q\n1R\n1Y\n1Z\n1E\n1P\n1T\n1G\n1M\n1K\n1k\n1\n"},
		{name: "human no unit after point", args: []string{"-h"}, stdin: "1.K\n1.5K\n2\n"},
		{name: "human negative", args: []string{"-h"}, stdin: "-1M\n-1K\n0\n1K\n"},
		{name: "human blanks", args: []string{"-h"}, stdin: "  2K\n 1M\n"},
		{name: "human zero has no unit", args: []string{"-h"}, stdin: "0K\n1\n0\n00K\n0.0K\n"},
		{name: "human fraction keeps its unit", args: []string{"-h"}, stdin: "0.5K\n1\n.5K\n"},
		{name: "human unit after a bare point", args: []string{"-h"}, stdin: "1.K\n2\n1\n"},
		{name: "human comma is not a separator", args: []string{"-h"}, stdin: "1,5K\n2\n"},
		{name: "human unknown suffix", args: []string{"-h"}, stdin: "1x\n2\n"},
		{name: "human iB", args: []string{"-h"}, stdin: "1KiB\n1K\n2K\n"},

		// -g.
		{name: "general", args: []string{"-g"}, stdin: "1e2\n99\n1e-3\n0\n"},
		{name: "general long", args: []string{"--general-numeric-sort"}, stdin: "2\n10\n"},
		{name: "general errors first", args: []string{"-g"}, stdin: "x\n1\ny\n-1\n"},
		{name: "general infinities", args: []string{"-g"}, stdin: "inf\n-inf\n1\ninfinity\n"},
		{name: "general hex", args: []string{"-g"}, stdin: "0x10\n15\n17\n"},
		{name: "general precision", args: []string{"-g"}, stdin: "1.0000000000000000001\n1\n"},
		{name: "general zeros", args: []string{"-g"}, stdin: "-0\n0\n0.0\n"},

		// -M.
		{name: "month", args: []string{"-M"}, stdin: "JAN\nDEC\nFEB\n"},
		{name: "month long", args: []string{"--month-sort"}, stdin: "MAR\nAPR\n"},
		{name: "month case and unknown", args: []string{"-M"}, stdin: "jan\nJAN\nFOO\nDEC\n\n  MAR x\n"},
		{name: "month every name", args: []string{"-M"}, stdin: "SEP\nOCT\nNOV\nDEC\nJAN\nFEB\nMAR\nAPR\nMAY\nJUN\nJUL\nAUG\n"},
		{name: "month prefixes", args: []string{"-M"}, stdin: "JANUARY\nJA\nJ\nDECEMBER\n"},

		// -V.
		{name: "version", args: []string{"-V"}, stdin: "v1.2\nv1.10\nv1.9\nv1.2.3\na\n"},
		{name: "version long", args: []string{"--version-sort"}, stdin: "1.2\n1.10\n"},
		{name: "version suffixes", args: []string{"-V"}, stdin: "1.0.tar.gz\n1.0.1.tar.gz\n1.0.tar\n"},
		{name: "version tilde", args: []string{"-V"}, stdin: "1.0~rc1\n1.0\n1.0~\n"},
		{name: "version dots", args: []string{"-V"}, stdin: ".\n..\n.a\n\na\n"},
		{name: "version leading zeros", args: []string{"-V"}, stdin: "a007\na7\na08\n"},
		{name: "version mixed", args: []string{"-V"}, stdin: "foo-1.0\nfoo-1.0.1\nfoo\nfoo.tar.gz\n"},

		// -d, -f, -i.
		{name: "dictionary", args: []string{"-d"}, stdin: "a-b\nab\na b\n"},
		{name: "dictionary long", args: []string{"--dictionary-order"}, stdin: "a.b\nab\n"},
		{name: "fold", args: []string{"-f"}, stdin: "b\nA\na\nB\n"},
		{name: "fold long", args: []string{"--ignore-case"}, stdin: "B\na\n"},
		{name: "ignore nonprinting", args: []string{"-i"}, stdin: "a\x01b\nab\n"},
		{name: "ignore nonprinting long", args: []string{"--ignore-nonprinting"}, stdin: "a\x7fb\nab\n"},
		{name: "dictionary and fold", args: []string{"-df"}, stdin: "A-b\nab\n"},
		{name: "dictionary and ignore", args: []string{"-di"}, stdin: "a\x01-b\nab\n"},
		{name: "ignore then dictionary", args: []string{"-id"}, stdin: "a\x01-b\nab\n"},
		{name: "fold and ignore", args: []string{"-fi"}, stdin: "A\x01b\nab\n"},
		{name: "ignore everything equal", args: []string{"-i"}, stdin: "\x01\n\x02\n"},

		// -b.
		{name: "ignore leading blanks", args: []string{"-b", blanksFile}},
		{name: "ignore leading blanks long", args: []string{"--ignore-leading-blanks", blanksFile}},
		{name: "without ignore leading blanks", args: []string{blanksFile}},

		// -k: what a key selects.
		{name: "key one is the whole line", args: []string{"-k1", blanksFile}},
		{name: "key one one", args: []string{"-k1,1", fields}},
		{name: "key two", args: []string{"-k2", fields}},
		{name: "key two two", args: []string{"-k2,2", fields}},
		{name: "key long", args: []string{"--key=2,2", fields}},
		{name: "key long spaced", args: []string{"--key", "2,2", fields}},
		{name: "key glued", args: []string{"-k2,2", fields}},
		{name: "key three", args: []string{"-k3", fields}},
		{name: "key past the end", args: []string{"-k9", fields}},
		{name: "key zero width", args: []string{"-k2,1", fields}},
		{name: "key char offsets", args: []string{"-k1.2,1.3"}, stdin: "abcd\nabbd\n"},
		{name: "key char offset past end", args: []string{"-k1.9,1.9"}, stdin: "ab\ncd\n"},
		{name: "key end char zero", args: []string{"-k1.2,2.0"}, stdin: "ab cd\nab ce\n"},
		{name: "key start blank sensitive", args: []string{"-k2,2"}, stdin: "b  a\na a\n"},
		{name: "key b at start", args: []string{"-k2b,2"}, stdin: "b  a\na a\n"},
		{name: "key b at end", args: []string{"-k2,2b"}, stdin: "b  a\na a\n"},
		{name: "key b both", args: []string{"-b", "-k2,2"}, stdin: "b  a\na a\n"},
		{name: "key end b stops at the field", args: []string{"-k2,2b"}, stdin: "x b  q\ny b a\n"},
		{name: "key end b with an offset", args: []string{"-k2,2.1b"}, stdin: "x  bq\ny  ba\n"},
		{name: "key end b whole line", args: []string{"-k1,1b"}, stdin: "a  z\na  y\n"},
		{name: "key end without b", args: []string{"-k2,2"}, stdin: "x b  q\ny b a\n"},
		{name: "two keys", args: []string{"-k2,2", "-k1,1", fields}},
		{name: "two keys second numeric", args: []string{"-k1,1", "-k2,2n", fields}},
		{name: "key numeric", args: []string{"-k2,2n", fields}},
		{name: "key reverse", args: []string{"-k2,2r", fields}},
		{name: "key numeric reverse", args: []string{"-k2,2nr", fields}},
		{name: "key spans fields numerically", args: []string{"-k1n"}, stdin: "  3x\n 10y\n"},
		{name: "key does not inherit under r", args: []string{"-n", "-k2r"}, stdin: "1b\n1a\n"},
		{name: "key inherits global n", args: []string{"-n", "-k2"}, stdin: "b 1\na 2\n"},
		{name: "key inherits global f", args: []string{"-f", "-k1,1"}, stdin: "B\na\n"},
		{name: "global r applies to last resort", args: []string{"-k1n", "-r"}, stdin: "1b\n1a\n"},
		{name: "key r applies to the key", args: []string{"-k1nr"}, stdin: "1b\n1a\n"},
		{name: "key month", args: []string{"-k2,2M"}, stdin: "x DEC\ny JAN\n"},
		{name: "key version", args: []string{"-k2,2V"}, stdin: "x 1.10\ny 1.9\n"},
		{name: "key human", args: []string{"-k2,2h"}, stdin: "x 1K\ny 1M\n"},
		{name: "key general", args: []string{"-k2,2g"}, stdin: "x 1e2\ny 99\n"},
		{name: "key dictionary", args: []string{"-k1,1d"}, stdin: "a-b\nab\n"},
		{name: "key ignore nonprinting", args: []string{"-k1,1i"}, stdin: "a\x01b\nab\n"},
		{name: "key fold", args: []string{"-k1,1f"}, stdin: "B\na\n"},
		{name: "key big file", args: []string{"-k2,2n", big}},

		// -t.
		{name: "tab colon", args: []string{"-t:", "-k2,2", colons}},
		{name: "tab colon spaced", args: []string{"-t", ":", "-k2,2", colons}},
		{name: "tab long", args: []string{"--field-separator=:", "-k2,2", colons}},
		{name: "tab empty fields", args: []string{"-t:", "-k2,2"}, stdin: "a::b\nc::d\n"},
		{name: "tab leading separator", args: []string{"-t:", "-k1,1"}, stdin: ":a\n:b\n"},
		{name: "tab leading separator key two", args: []string{"-t:", "-k2,2"}, stdin: ":a\n:b\n"},
		{name: "tab nul", args: []string{"-t", "\\0", "-k2,2"}, stdin: "b\x002\na\x003\n"},
		{name: "tab repeated same", args: []string{"-t:", "-t:", "-k2", colons}},
		{name: "tab is a blank", args: []string{"-t", " ", "-k2,2"}, stdin: "b  a\na a\n"},
		{name: "tab tab character", args: []string{"-t", "\t", "-k2,2"}, stdin: "b\t2\na\t3\n"},

		// -m.
		{name: "merge", args: []string{"-m", ab, cd}},
		{name: "merge long", args: []string{"--merge", ab, cd}},
		{name: "merge interleaves", args: []string{"-m", ba, ab}},
		{name: "merge one file", args: []string{"-m", ba}},
		{name: "merge unique", args: []string{"-m", "-u", ab, ab}},
		{name: "merge reverse", args: []string{"-m", "-r", cd, ab}},
		{name: "merge with a key", args: []string{"-m", "-k2,2n", fields, fields}},
		{name: "merge stdin", args: []string{"-m", ab, "-"}, stdin: "a\nz\n"},
		{name: "merge empty file", args: []string{"-m", empty, ab}},

		// -c and -C.
		{name: "check sorted", args: []string{"-c", ab}},
		{name: "check unsorted", args: []string{"-c", ba}},
		{name: "check stdin", args: []string{"-c"}, stdin: "b\na\n"},
		{name: "check long", args: []string{"--check", ba}},
		{name: "check diagnose first", args: []string{"--check=diagnose-first", ba}},
		{name: "check quiet", args: []string{"--check=quiet", ba}},
		{name: "check silent", args: []string{"--check=silent", ba}},
		{name: "check capital", args: []string{"-C", ba}},
		{name: "check capital sorted", args: []string{"-C", ab}},
		{name: "check unique", args: []string{"-c", "-u"}, stdin: "a\na\nb\n"},
		{name: "check unique capital", args: []string{"-C", "-u"}, stdin: "a\na\n"},
		{name: "check with a key", args: []string{"-c", "-k2,2n"}, stdin: "x 2\ny 1\n"},
		{name: "check reverse", args: []string{"-c", "-r"}, stdin: "b\na\n"},
		{name: "check empty", args: []string{"-c", empty}},
		{name: "check one line", args: []string{"-c"}, stdin: "a\n"},
		{name: "check disorder line has odd bytes", args: []string{"-c"}, stdin: "b\na\xffx\n"},
		{name: "check zero terminated", args: []string{"-c", "-z"}, stdin: "b\x00a\x00"},
		{name: "check names the file", args: []string{"-c", ba}},
		{name: "check missing file", args: []string{"-c", missing}},
		{name: "check directory", args: []string{"-c", d}},
		{name: "check two files", args: []string{"-c", ab, cd}},
		{name: "check capital two files", args: []string{"-C", ab, cd}},
		{name: "check and merge", args: []string{"-c", "-m", ba}},
		{name: "check and output", args: []string{"-c", "-o", "/dev/null", ab}},
		{name: "check capital and output", args: []string{"-C", "-o", "/dev/null", ab}},
		{name: "check big", args: []string{"-c", big}},

		// -o.
		{name: "output to a new file", args: []string{"-o", filepath.Join(dir, "out1"), ba}},
		{name: "output long", args: []string{"--output=" + filepath.Join(dir, "out2"), ba}},
		{name: "output same twice", args: []string{"-o", "/dev/null", "-o", "/dev/null", ba}},
		{name: "output twice differing", args: []string{"-o", "/dev/null", "-o", "/dev/zero", ba}},
		{name: "output to a directory", args: []string{"-o", d, ba}},
		{name: "output into a missing directory", args: []string{"-o", filepath.Join(missing, "x"), ba}},
		{name: "output to dev full", args: []string{"-o", "/dev/full", ba}},

		// --files0-from.
		{name: "files0", args: []string{"--files0-from=" + names0}},
		{name: "files0 zero length name", args: []string{"--files0-from=" + namesEmpty}},
		{name: "files0 dash name", args: []string{"--files0-from=" + namesDash}},
		{name: "files0 missing", args: []string{"--files0-from=" + missing}},
		{name: "files0 directory", args: []string{"--files0-from=" + d}},
		{name: "files0 empty", args: []string{"--files0-from=" + empty}},
		{name: "files0 with an operand", args: []string{"--files0-from=" + names0, ab}},
		{name: "files0 from stdin", args: []string{"--files0-from=-"}, stdin: ab + "\x00"},
		{name: "files0 with check", args: []string{"-c", "--files0-from=" + empty}},

		// --debug: the warnings, then one underline row per key and a
		// last-resort row unless -u or -s dropped it.
		{name: "debug", args: []string{"--debug"}, stdin: "a b c\n"},
		{name: "debug no input", args: []string{"--debug"}, stdin: ""},
		{name: "debug empty line", args: []string{"--debug", "-k1,1"}, stdin: "\n"},
		{name: "debug key one", args: []string{"--debug", "-k1"}, stdin: "a b c\n"},
		{name: "debug key one one", args: []string{"--debug", "-k1,1"}, stdin: "a b c\n"},
		{name: "debug key two", args: []string{"--debug", "-k2"}, stdin: "a b c\n"},
		{name: "debug key two two", args: []string{"--debug", "-k2,2"}, stdin: "a b c\n"},
		{name: "debug key char offsets", args: []string{"--debug", "-k1.2,1.3"}, stdin: "a b c\n"},
		{name: "debug key start offset only", args: []string{"--debug", "-k1.2"}, stdin: "a b c\n"},
		{name: "debug key b at start", args: []string{"--debug", "-k2b"}, stdin: "a b c\n"},
		{name: "debug key b at end", args: []string{"--debug", "-k2,2b"}, stdin: "a b c\n"},
		{name: "debug global b", args: []string{"--debug", "-b", "-k2,2"}, stdin: "a b c\n"},
		{name: "debug key b whole line", args: []string{"--debug", "-k1b,1"}, stdin: "  a b\n"},
		{name: "debug zero width", args: []string{"--debug", "-k2,1"}, stdin: "a b c\n"},
		{name: "debug two keys", args: []string{"--debug", "-k1", "-k2"}, stdin: "a b c\n"},
		{name: "debug key past the end", args: []string{"--debug", "-k5", "-k1,1"}, stdin: "a b\n"},
		{name: "debug char past the end", args: []string{"--debug", "-k1.3"}, stdin: "ab\n"},
		{name: "debug numeric spans", args: []string{"--debug", "-k1n"}, stdin: "a b c\n"},
		{name: "debug numeric one field", args: []string{"--debug", "-k1n,1"}, stdin: "a b c\n"},
		{name: "debug numeric fields", args: []string{"--debug", "-k2n,3n"}, stdin: "a b c\n"},
		{name: "debug numeric global", args: []string{"--debug", "-n"}, stdin: "  3x\n 10y\n"},
		{name: "debug numeric fraction", args: []string{"--debug", "-n"}, stdin: " 1.5x\n 1,5x\n-3x\n- 3x\n.5\n5.\n00\n"},
		{name: "debug human", args: []string{"--debug", "-h"}, stdin: "1K\n1x\n1KiB\n1.5K\n-1K\n0K\nK\n1.K\n"},
		{name: "debug general", args: []string{"--debug", "-g"}, stdin: "1e3x\n0x10z\ninfx\nq\n"},
		{name: "debug month", args: []string{"--debug", "-M"}, stdin: "JANx\n janx\nzz\n"},
		{name: "debug version", args: []string{"--debug", "-V"}, stdin: "a b\n"},
		{name: "debug random", args: []string{"--debug", "-R", "--random-source=" + randSrc}, stdin: "a b\n"},
		{name: "debug dictionary", args: []string{"--debug", "-d"}, stdin: "a b\n"},
		{name: "debug fold", args: []string{"--debug", "-f"}, stdin: "a b\n"},
		{name: "debug reverse", args: []string{"--debug", "-r"}, stdin: "a b\n"},
		{name: "debug unique", args: []string{"--debug", "-u"}, stdin: "a b\n"},
		{name: "debug stable", args: []string{"--debug", "-s"}, stdin: "a b\n"},
		{name: "debug unique with a key", args: []string{"--debug", "-u", "-k1,1"}, stdin: "a b\n"},
		{name: "debug stable with a key", args: []string{"--debug", "-s", "-k1,1"}, stdin: "a b\n"},
		{name: "debug reverse with a key", args: []string{"--debug", "-r", "-k1,1"}, stdin: "a b\n"},
		{name: "debug ignored global", args: []string{"--debug", "-f", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored globals", args: []string{"--debug", "-fd", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored globals with b", args: []string{"--debug", "-fdb", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored reverse", args: []string{"--debug", "-r", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored fold and reverse", args: []string{"--debug", "-fr", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored b", args: []string{"--debug", "-b", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored numeric", args: []string{"--debug", "-n", "-k1,1M"}, stdin: "a b\n"},
		{name: "debug ignored month", args: []string{"--debug", "-M", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored version", args: []string{"--debug", "-V", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug ignored ignore", args: []string{"--debug", "-i", "-k1,1n"}, stdin: "a b\n"},
		{name: "debug separator is the decimal point", args: []string{"--debug", "-t.", "-k1n,2n"}, stdin: "a.b.c\n"},
		{name: "debug separator decimal point one field", args: []string{"--debug", "-t.", "-k1n,1n"}, stdin: "a.b.c\n"},
		{name: "debug separator decimal point global", args: []string{"--debug", "-t.", "-n"}, stdin: "a.b.c\n"},
		{name: "debug separator is a minus", args: []string{"--debug", "-t-", "-k1n,2n"}, stdin: "a-b-c\n"},
		{name: "debug separator is a minus one field", args: []string{"--debug", "-t-", "-k1n,1n"}, stdin: "a-b-c\n"},
		{name: "debug separator is a digit", args: []string{"--debug", "-t5", "-k1n,2n"}, stdin: "a5b5c\n"},
		{name: "debug separator suppresses blanks warning", args: []string{"--debug", "-t:", "-k2,2"}, stdin: "a:b\n"},
		{name: "debug tab shown as an angle", args: []string{"--debug"}, stdin: "a\tb\n"},
		{name: "debug control bytes have no width", args: []string{"--debug"}, stdin: "a\x01b\n"},
		{name: "debug del has no width", args: []string{"--debug"}, stdin: "a\x7fb\n"},
		{name: "debug high bytes have width", args: []string{"--debug"}, stdin: "a\xffb\n"},
		{name: "debug zero terminated", args: []string{"--debug", "-z"}, stdin: "b\x00a\x00"},
		{name: "debug obsolete key", args: []string{"--debug", "+1"}, stdin: "a b c\n"},
		{name: "debug obsolete key with an end", args: []string{"--debug", "+1", "-2"}, stdin: "a b c\n"},
		{name: "debug obsolete key zero", args: []string{"--debug", "+0"}, stdin: "a b c\n"},
		{name: "debug obsolete key char", args: []string{"--debug", "+1.1"}, stdin: "a b c\n"},
		{name: "debug obsolete key end char", args: []string{"--debug", "+1", "-2.3"}, stdin: "a b c\n"},
		{name: "debug and check", args: []string{"--debug", "-c"}},
		{name: "debug and check quiet", args: []string{"--debug", "-C"}},
		{name: "debug and output", args: []string{"--debug", "-o", "/dev/null"}},
		{name: "debug big", args: []string{"--debug", "-k1,1n", big}},

		// -R.
		{name: "random", args: []string{"-R", "--random-source=" + randSrc}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "random groups equal keys", args: []string{"-R", "--random-source=" + randSrc}, stdin: "a\nb\na\nc\n"},
		{name: "random with a key", args: []string{"-R", "--random-source=" + randSrc, "-k1,1"}, stdin: "a x\nb y\na z\n"},
		{name: "random long", args: []string{"--random-sort", "--random-source=" + randSrc}, stdin: "a\nb\nc\n"},
		{name: "random sort word", args: []string{"--sort=random", "--random-source=" + randSrc}, stdin: "a\nb\nc\n"},
		{name: "random unique", args: []string{"-Ru", "--random-source=" + randSrc}, stdin: "a\nb\na\n"},
		{name: "random source too short", args: []string{"-R", "--random-source=" + randShort}, stdin: "a\nb\n"},
		{name: "random source missing", args: []string{"-R", "--random-source=" + missing}, stdin: "a\nb\n"},
		{name: "random source directory", args: []string{"-R", "--random-source=" + d}, stdin: "a\nb\n"},
		{name: "random source unused", args: []string{"--random-source=" + missing}, stdin: "b\na\n"},
		{name: "random big", args: []string{"-R", "--random-source=" + randSrc, big}},

		// --sort=WORD.
		{name: "sort word numeric", args: []string{"--sort=numeric", nums}},
		{name: "sort word abbreviated", args: []string{"--sort=n", nums}},
		{name: "sort word general", args: []string{"--sort=general-numeric"}, stdin: "1e2\n99\n"},
		{name: "sort word human", args: []string{"--sort=human-numeric"}, stdin: "1K\n1M\n"},
		{name: "sort word month", args: []string{"--sort=month"}, stdin: "JAN\nDEC\n"},
		{name: "sort word version", args: []string{"--sort=version"}, stdin: "1.10\n1.9\n"},
		{name: "sort word invalid", args: []string{"--sort=x"}},
		{name: "sort word empty", args: []string{"--sort="}},
		{name: "sort word ambiguous", args: []string{"--sort=" + "n"}},
		{name: "check word invalid", args: []string{"--check=x"}},
		{name: "check word empty", args: []string{"--check="}},
		{name: "check word abbreviated", args: []string{"--check=q", ba}},

		// The obsolete +POS -POS form.
		{name: "obsolete plus", args: []string{"+1"}, stdin: "b a\na b\n"},
		{name: "obsolete plus zero", args: []string{"+0"}, stdin: "b a\na b\n"},
		{name: "obsolete plus minus", args: []string{"+1", "-2"}, stdin: "b a c\na b d\n"},
		{name: "obsolete plus minus char", args: []string{"+1.1", "-1.3"}, stdin: "x abcd\ny abbd\n"},
		{name: "obsolete with flags", args: []string{"+1n"}, stdin: "x 10\ny 9\n"},
		{name: "obsolete not a number", args: []string{"+x"}},
		{name: "obsolete trailing junk", args: []string{"+1x"}},
		{name: "obsolete trailing dot", args: []string{"+1."}},
		{name: "obsolete stray in the end spec", args: []string{"+1", "-2q"}},
		{name: "obsolete bad end", args: []string{"+1", "-x"}},
		{name: "obsolete posix 199209", args: []string{"+1"}, stdin: "b a\na b\n", env: []string{"_POSIX2_VERSION=199209"}},
		{name: "obsolete posix 200112 with a file", args: []string{"+1", ab}, env: []string{"_POSIX2_VERSION=200112"}},
		{name: "obsolete posix 200112", args: []string{"+1"}, stdin: "b a\na b\n", env: []string{"_POSIX2_VERSION=200112"}},
		{name: "obsolete posix 200112 with end", args: []string{"+1", "-2"}, stdin: "b a\na b\n", env: []string{"_POSIX2_VERSION=200112"}},
		{name: "obsolete posixly correct", args: []string{"+1"}, stdin: "b a\na b\n", env: []string{"POSIXLY_CORRECT=1"}},
		{name: "obsolete posixly correct and 200112", args: []string{"+1", "-2"}, stdin: "b a\na b\n", env: []string{"POSIXLY_CORRECT=1", "_POSIX2_VERSION=200112"}},
		{name: "obsolete after double dash", args: []string{"--", "+1"}},

		// Option scanning.
		{name: "options after operands", args: []string{ba, "-r"}},
		{name: "posixly correct stops at the first operand", args: []string{ba, "-r"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posixly correct before an operand", args: []string{"-r", ba}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posixly correct obsolete key", args: []string{"+1", ba}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "double dash", args: []string{"--", ba}},
		{name: "double dash then an option", args: []string{"--", "-r"}},
		{name: "double dash twice", args: []string{"--", "--", ba}},
		{name: "cluster", args: []string{"-rn", nums}},
		{name: "unknown short", args: []string{"-Q"}},
		{name: "unknown short digit", args: []string{"-5"}},
		{name: "unknown long", args: []string{"--frobnicate"}},
		{name: "empty long name", args: []string{"--=x"}},
		{name: "ambiguous s", args: []string{"--s"}},
		{name: "ambiguous c", args: []string{"--c"}},
		{name: "ambiguous r", args: []string{"--r"}},
		{name: "ambiguous v", args: []string{"--v"}},
		{name: "ambiguous f", args: []string{"--f"}},
		{name: "ambiguous b", args: []string{"--b"}},
		{name: "ambiguous d", args: []string{"--d"}},
		{name: "ambiguous i", args: []string{"--i"}},
		{name: "ambiguous m", args: []string{"--m"}},
		{name: "ambiguous h", args: []string{"--h"}},
		{name: "unambiguous k", args: []string{"--k"}},
		{name: "unambiguous p", args: []string{"--p"}},
		{name: "unambiguous t", args: []string{"--t"}},
		{name: "unambiguous o", args: []string{"--o"}},
		{name: "unambiguous u", args: []string{"--u", ba}},
		{name: "unambiguous z", args: []string{"--z", ba}},
		{name: "unambiguous n", args: []string{"--n", nums}},
		{name: "unambiguous g", args: []string{"--g", nums}},
		{name: "key needs a value", args: []string{"-k"}},
		{name: "tab needs a value", args: []string{"-t"}},
		{name: "output needs a value", args: []string{"-o"}},
		{name: "buffer needs a value", args: []string{"-S"}},
		{name: "tmpdir needs a value", args: []string{"-T"}},
		{name: "ignored y needs a value", args: []string{"-y"}},
		{name: "ignored y glued", args: []string{"-y5", ba}},
		{name: "ignored y spaced", args: []string{"-y", "5", ba}},
		{name: "merge does not take a value", args: []string{"--merge=x"}},
		{name: "check takes an optional value", args: []string{"-c", "x"}},

		// -k errors.
		{name: "key zero", args: []string{"-k0"}},
		{name: "key char zero", args: []string{"-k1.0"}},
		{name: "key not a number", args: []string{"-kx"}},
		{name: "key end not a number", args: []string{"-k1,x"}},
		{name: "key char not a number", args: []string{"-k1.x"}},
		{name: "key end char not a number", args: []string{"-k1,1.x"}},
		{name: "key end zero", args: []string{"-k1,0"}},
		{name: "key stray", args: []string{"-k1q"}},
		{name: "key stray after end", args: []string{"-k1,1q"}},
		{name: "key negative", args: []string{"-k-1"}},
		{name: "key plus", args: []string{"-k+1", ba}},
		{name: "key blank", args: []string{"-k 1", ba}},
		{name: "key empty", args: []string{"-k", ""}},
		{name: "key huge", args: []string{"-k99999999999999999999", ba}},
		{name: "key end huge", args: []string{"-k1,99999999999999999999", ba}},
		{name: "key char large", args: []string{"-k1.100", ba}},
		{name: "key end char large", args: []string{"-k1,1.100", ba}},
		{name: "key incompatible orderings", args: []string{"-k1bdfgiMhnRrV,1", ba}},
		{name: "key numeric and month", args: []string{"-k1n,1M", ba}},
		{name: "key version and numeric", args: []string{"-k1V,1n", ba}},
		{name: "key version and dictionary", args: []string{"-k1Vd,1", ba}},
		{name: "key random and version", args: []string{"-k1RV,1", "--random-source=" + randSrc, ba}},
		{name: "two keys each with one ordering", args: []string{"-k1n", "-k2M", ba}},

		// -t errors.
		{name: "tab empty", args: []string{"-t", ""}},
		{name: "tab multi character", args: []string{"-tab"}},
		{name: "tab backslash zero", args: []string{"-t", "\\0", ba}},
		{name: "tab incompatible", args: []string{"-t:", "-t,"}},

		// -S, --parallel, --batch-size, --compress-program, -T.
		{name: "buffer size", args: []string{"-S", "1M", ba}},
		{name: "buffer size bare", args: []string{"-S", "1000", ba}},
		{name: "buffer size percent", args: []string{"-S", "50%", ba}},
		{name: "buffer size bytes", args: []string{"-S", "1000b", ba}},
		{name: "buffer size invalid", args: []string{"-S", "x"}},
		{name: "buffer size bad suffix", args: []string{"-S", "1x"}},
		{name: "buffer size too large", args: []string{"-S", "1Y"}},
		{name: "buffer size lower case", args: []string{"-S", "1k", ba}},
		{name: "buffer size long", args: []string{"--buffer-size=x"}},
		{name: "buffer size long bad suffix", args: []string{"--buffer-size=1x"}},
		{name: "buffer size long too large", args: []string{"--buffer-size=1Y"}},
		{name: "buffer size negative", args: []string{"-S", "-1"}},
		{name: "buffer size two suffixes", args: []string{"-S", "1kB"}},
		{name: "buffer size blank", args: []string{"-S", " 1k", ba}},
		{name: "buffer size plus", args: []string{"-S", "+1", ba}},
		{name: "buffer size empty", args: []string{"-S", ""}},
		{name: "parallel", args: []string{"--parallel=2", ba}},
		{name: "parallel one", args: []string{"--parallel=1", ba}},
		{name: "parallel zero", args: []string{"--parallel=0"}},
		{name: "parallel invalid", args: []string{"--parallel=x"}},
		{name: "parallel large", args: []string{"--parallel=1000", ba}},
		{name: "parallel plus", args: []string{"--parallel=+1", ba}},
		{name: "parallel blank", args: []string{"--parallel= 1", ba}},
		{name: "parallel suffix", args: []string{"--parallel=1k"}},
		{name: "batch size", args: []string{"--batch-size=2", ba}},
		{name: "batch size one", args: []string{"--batch-size=1"}},
		{name: "batch size zero", args: []string{"--batch-size=0"}},
		{name: "batch size invalid", args: []string{"--batch-size=x"}},
		{name: "batch size suffix", args: []string{"--batch-size=1k"}},
		{name: "batch size plus", args: []string{"--batch-size=+2", ba}},
		{name: "compress program", args: []string{"--compress-program=gzip", ba}},
		{name: "tmpdir", args: []string{"-T", "/tmp", ba}},
		{name: "tmpdir long", args: []string{"--temporary-directory=/tmp", ba}},
		{name: "tmpdir twice", args: []string{"-T", "/tmp", "-T", "/var/tmp", ba}},

		// Inputs that fail.
		{name: "missing file", args: []string{missing}},
		{name: "missing before an existing one", args: []string{missing, ab}},
		{name: "missing after an existing one", args: []string{ab, missing}},
		{name: "directory", args: []string{d}},
		{name: "directory then a file", args: []string{d, ab}},
		{name: "directory with merge", args: []string{"-m", d}},
		{name: "empty operand", args: []string{""}},
		{name: "empty operand after a file", args: []string{ab, ""}},
		{name: "spaced name", args: []string{spaced}},
		{name: "quoted name", args: []string{quoted}},
		{name: "raw name", args: []string{rawName}},
		{name: "missing raw name", args: []string{filepath.Join(dir, "no\xffsuch")}},
		{name: "missing quoted name", args: []string{filepath.Join(dir, "no'such")}},

		// Write failures.
		{name: "stdout full", args: []string{ba}, stdout: stdoutFull},
		{name: "stdout full big", args: []string{big}, stdout: stdoutFull},
		{name: "stdout full with nothing to write", args: []string{empty}, stdout: stdoutFull},
		{name: "stdout closed", args: []string{ba}, stdout: stdoutClosed},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "check stdout closed", args: []string{"-c", ba}, stdout: stdoutClosed},
	}
}

// shuffledLines is n lines with duplicate keys, in an order no ordering
// mode leaves alone.
func shuffledLines(n int) string {
	var b strings.Builder
	x := 12345
	for i := 0; i < n; i++ {
		x = (x*1103515245 + 12345) & 0x7fffffff
		b.WriteString(itoa(x % 1000))
		b.WriteByte(' ')
		b.WriteString(itoa(i % 97))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestSortParity(t *testing.T) {
	requireParity(t, "sort", sortCases(t))
}

func TestSortHelpVersion(t *testing.T) {
	requireHelp(t, "sort", []string{"--help"}, 0)
	requireHelp(t, "sort", []string{"--he"}, 0)
	requireHelp(t, "sort", []string{"--help", "ignored"}, 0)
	requireVersion(t, "sort", []string{"--version"}, 0)
}
