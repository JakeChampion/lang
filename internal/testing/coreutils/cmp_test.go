package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("cmp", cmpCases)
}

func cmpCases(t *testing.T) []invocation {
	dir := t.TempDir()
	a := writeFile(t, dir, "a", "hello\nworld\n")
	b := writeFile(t, dir, "b", "hello\nwurld\n")
	short := writeFile(t, dir, "short", "hello\n")
	partial := writeFile(t, dir, "partial", "hello\nwo")
	longer := writeFile(t, dir, "longer", "hello\nworld\nextra")
	empty := writeFile(t, dir, "empty", "")
	x := writeFile(t, dir, "x", "\x00\x7f\x80\xff abc\t\n")
	y := writeFile(t, dir, "y", "\x01\x7e\x81\xfe abd\n\n")
	spaced := writeFile(t, dir, "f name", "hello\nworld\n")
	missing := filepath.Join(dir, "nosuch")
	d := filepath.Join(dir, "d")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	// Two files of 300000 bytes that differ at bytes 70000, 140001 and the
	// last one. The differences cross the 64 KiB read chunks.
	base := []byte(strings.Repeat("line of text\n", 300000/13+1)[:300000])
	other := append([]byte(nil), base...)
	other[69999] = 'X'
	other[140000] = 'Y'
	other[len(other)-1] = 'Z'
	big1 := writeFile(t, dir, "big1", string(base))
	big2 := writeFile(t, dir, "big2", string(other))
	big3 := writeFile(t, dir, "big3", string(base[:200000]))
	same := writeFile(t, dir, "same", string(base))

	return []invocation{
		{name: "equal files", args: []string{a, spaced}},
		{name: "a file with itself", args: []string{a, a}},
		{name: "a file with a link to it", args: []string{a, link}},
		{name: "first difference", args: []string{a, b}},
		{name: "first difference with bytes", args: []string{"-b", a, b}},
		{name: "bytes of the control and high range", args: []string{"-b", x, y}},
		{name: "verbose", args: []string{"-l", a, b}},
		{name: "verbose with bytes", args: []string{"-lb", x, y}},
		{name: "verbose of the control and high range", args: []string{"-l", x, y}},
		{name: "long options", args: []string{"--print-bytes", "--verbose", a, b}},
		{name: "long option prefixes", args: []string{"--print-b", "--verb", a, b}},
		{name: "ambiguous prefix of the old -c spelling", args: []string{"--print", a, b}},
		{name: "the old -c spelling", args: []string{"-c", a, b}},
		{name: "the old --print-chars spelling", args: []string{"--print-chars", a, b}},

		{name: "EOF on the shorter file at a line end", args: []string{a, longer}},
		{name: "EOF on the shorter second file", args: []string{longer, a}},
		{name: "EOF inside a line", args: []string{partial, a}},
		{name: "EOF on a prefix file", args: []string{short, a}},
		{name: "EOF on an empty file", args: []string{empty, a}},
		{name: "EOF on an empty second file", args: []string{a, empty}},
		{name: "two empty files", args: []string{empty, empty}},
		{name: "EOF under verbose", args: []string{"-l", a, longer}},
		{name: "EOF after differences under verbose", args: []string{"-l", b, longer}},
		{name: "verbose width from the shorter file", args: []string{"-l", short, b}},

		{name: "quiet differ", args: []string{"-s", a, b}},
		{name: "quiet EOF", args: []string{"-s", a, longer}},
		{name: "quiet equal", args: []string{"--quiet", a, spaced}},
		{name: "silent", args: []string{"--silent", a, b}},
		{name: "quiet hides an open error", args: []string{"-s", a, missing}},
		{name: "verbose and quiet", args: []string{"-l", "-s", a, b}},

		{name: "skip both", args: []string{"-i", "6", a, b}},
		{name: "skip each", args: []string{"-i", "6:6", a, b}},
		{name: "skip unequal", args: []string{"-i", "1:2", a, b}},
		{name: "skip past both ends", args: []string{"-i", "100", a, b}},
		{name: "skip operands", args: []string{a, b, "6", "6"}},
		{name: "skip operand for the first file only", args: []string{a, b, "8"}},
		{name: "skip option and operand take the larger", args: []string{"-i", "3", a, b, "8", "1"}},
		{name: "skip with a suffix", args: []string{"--ignore-initial=1k", big1, big2}},
		{name: "skip with a decimal suffix", args: []string{"-i", "100kB", big1, big2}},
		{name: "skip in hexadecimal", args: []string{"-i", "0x10", big1, big2}},
		{name: "skip in octal", args: []string{"-i", "010", a, b}},
		{name: "skip of a bare suffix", args: []string{"-i", "K", big1, big2}},
		{name: "bad first skip", args: []string{"-i", "1x:2", a, b}},
		{name: "bad second skip", args: []string{"-i", "1:2x", a, b}},
		{name: "skip that overflows", args: []string{"-i", "99999999999999999999", a, b}},
		{name: "bad skip operand", args: []string{a, b, "x"}},
		{name: "skip operand with a suffix", args: []string{big1, big2, "1MiB"}},

		{name: "limit before the difference", args: []string{"-n", "7", a, b}},
		{name: "limit at the difference", args: []string{"-n", "8", a, b}},
		{name: "limit with a suffix", args: []string{"--bytes=1KiB", a, b}},
		{name: "limit of zero", args: []string{"-n", "0", a, b}},
		{name: "limit with blanks and a sign", args: []string{"-n", " +5", a, b}},
		{name: "limit under verbose sets the width", args: []string{"-l", "-n", "3", a, b}},
		{name: "bad limit", args: []string{"-n", "1b", a, b}},
		{name: "limit that overflows", args: []string{"-n", "99999999999999999999", a, b}},

		{name: "big files", args: []string{big1, big2}},
		{name: "big files verbose", args: []string{"-l", big1, big2}},
		{name: "big files verbose with bytes", args: []string{"-lb", big1, big2}},
		{name: "big equal files", args: []string{big1, same}},
		{name: "big prefix", args: []string{big3, big1}},
		{name: "big skip past a difference", args: []string{"-i", "70000", big1, big2}},

		{name: "second file from stdin", args: []string{a}, stdin: "hello\nwurld\n"},
		{name: "dash is stdin", args: []string{"-", b}, stdin: "hello\nworld\n"},
		{name: "dash second", args: []string{a, "-"}, stdin: "hello\n"},
		{name: "skip on stdin", args: []string{"-i", "6", "-", b}, stdin: "hello\nwurld\n"},
		{name: "big stdin", args: []string{big1}, stdinPath: big2},

		{name: "missing first file", args: []string{missing, a}},
		{name: "missing second file", args: []string{a, missing}},
		{name: "directory", args: []string{a, d}},
		{name: "no operands", args: []string{}},
		{name: "extra operand", args: []string{a, b, "1", "2", "3"}},
		{name: "unknown option", args: []string{"-z", a, b}},
		{name: "unknown long option", args: []string{"--bogus", a, b}},
		{name: "ambiguous long option", args: []string{"--ver", a, b}},
		{name: "missing option value", args: []string{a, b, "-n"}},
		{name: "operands after --", args: []string{"--", a, b}},
		{name: "full stdout", args: []string{a, b}, stdout: stdoutFull},
		{name: "full stdout under verbose", args: []string{"-l", big1, big2}, stdout: stdoutFull},
	}
}

func TestCmpParity(t *testing.T) {
	requireParity(t, "cmp", cmpCases(t))
}

func TestCmpHelpVersion(t *testing.T) {
	requireHelp(t, "cmp", []string{"--help"}, 0)
	requireHelp(t, "cmp", []string{"--he"}, 0)
	requireVersion(t, "cmp", []string{"--version"}, 0)
	requireVersion(t, "cmp", []string{"-v"}, 0)
	requireVersion(t, "cmp", []string{"--vers"}, 0)
}
