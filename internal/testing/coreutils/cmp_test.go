package coreutils

import (
	"errors"
	"io"
	"os"
	"os/exec"
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
		{name: "a file with itself at different offsets", args: []string{a, a, "0", "1"}},
		{name: "a missing file with itself", args: []string{missing, missing}},
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
		{name: "conflicting modes before help", args: []string{"-ls", "--help"}},
		{name: "conflicting modes before version", args: []string{"--silent", "--verbose", "--version"}},
		{name: "conflicting modes before invalid limit", args: []string{"-sl", "-n", "bad"}},
		{name: "invalid limit before conflicting modes", args: []string{"-n", "bad", "-ls"}},

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
		{name: "larger second limit", args: []string{"-n", "7", "--bytes=8", a, b}},
		{name: "smaller second limit", args: []string{"-n", "8", "--bytes=7", a, b}},
		{name: "largest signed limit", args: []string{"-n", "9223372036854775807", a, b}},
		{name: "limit above signed range", args: []string{"-n", "9223372036854775808", a, b}},
		{name: "negative zero limit", args: []string{"-n", "-0KiB", a, b}},
		{name: "negative nonzero skip", args: []string{"-i", "-1", a, b}},
		{name: "sign before bare suffix", args: []string{"-n", "+K", a, b}},
		{name: "blank before bare suffix in second skip", args: []string{"-i", "1: K", a, b}},
		{name: "base zero binary limit", args: []string{"-n", "0b111", a, b}},
		{name: "base zero binary skip", args: []string{"-i", "0B100:0b10", a, b}},
		{name: "binary prefix without digits", args: []string{"-n", "0bK", a, b}},
		{name: "limit under verbose sets the width", args: []string{"-l", "-n", "3", a, b}},
		{name: "bad limit", args: []string{"-n", "1b", a, b}},
		{name: "limit that overflows", args: []string{"-n", "99999999999999999999", a, b}},

		{name: "big files", args: []string{big1, big2}},
		{name: "big files verbose", args: []string{"-l", big1, big2}},
		{name: "big files verbose with bytes", args: []string{"-lb", big1, big2}},
		{name: "big equal files", args: []string{big1, same}},
		{name: "big prefix", args: []string{big3, big1}},
		{name: "big skip past a difference", args: []string{"-i", "70000", big1, big2}},

		{name: "shared stdin mismatch", args: []string{"-n", "2", "-", "-", "0", "1"}, stdinPath: a},
		{name: "shared stdin verbose mismatch", args: []string{"-l", "-n", "2", "-", "-", "0", "1"}, stdinPath: a},
		{name: "shared stdin quiet bounded comparison", args: []string{"-s", "-n", "2", "-", "-", "0", "1"}, stdinPath: a},
		{name: "shared stdin zero limit", args: []string{"-n", "0", "-", "-", "0", "1"}, stdinPath: a},
		{name: "shared stdin EOF", args: []string{"-", "-", "0", "1"}, stdinPath: a},
		{name: "shared stdin equal bytes", args: []string{"-n", "1", "-", "-", "0", "2"}, stdinPath: a},
		{name: "shared stdin full stdout", args: []string{"-n", "2", "-", "-", "0", "1"}, stdinPath: a, stdout: stdoutFull},
		{name: "second file from stdin", args: []string{a}, stdin: "hello\nwurld\n"},
		{name: "dash is stdin", args: []string{"-", b}, stdin: "hello\nworld\n"},
		{name: "dash second", args: []string{a, "-"}, stdin: "hello\n"},
		{name: "skip on stdin", args: []string{"-i", "6", "-", b}, stdin: "hello\nwurld\n"},
		{name: "big stdin", args: []string{big1}, stdinPath: big2},

		{name: "missing first file", args: []string{missing, a}},
		{name: "missing second file", args: []string{a, missing}},
		{name: "directory", args: []string{a, d}},
		{name: "quiet reports a read error", args: []string{"-s", a, d}},
		{name: "quiet reports a read error while skipping", args: []string{"-s", "-i", "1", d, a}},
		{name: "zero limit still checks first input", args: []string{"-n", "0", d, a}},
		{name: "zero limit still checks second input", args: []string{"-n", "0", a, d}},
		{name: "zero limit with the same directory", args: []string{"-n", "0", d, d}},
		{name: "no operands", args: []string{}},
		{name: "extra operand", args: []string{a, b, "1", "2", "3"}},
		{name: "unknown option", args: []string{"-z", a, b}},
		{name: "unknown long option", args: []string{"--bogus", a, b}},
		{name: "ambiguous long option", args: []string{"--ver", a, b}},
		{name: "missing option value", args: []string{a, b, "-n"}},
		{name: "operands after --", args: []string{"--", a, b}},
		{name: "null stdout equal prefix", args: []string{short, a}, stdout: stdoutNull},
		{name: "null stdout partial line", args: []string{partial, a}, stdout: stdoutNull},
		{name: "null stdout verbose difference before EOF", args: []string{"-l", b, longer}, stdout: stdoutNull},
		{name: "null stdout empty input", args: []string{empty, a}, stdout: stdoutNull},
		{name: "null stdout quiet EOF", args: []string{"-s", short, a}, stdout: stdoutNull},
		{name: "full stdout", args: []string{a, b}, stdout: stdoutFull},
		{name: "verbose EOF before final output error", args: []string{"-l", b, longer}, stdout: stdoutFull},
		{name: "verbose second operand EOF before final output error", args: []string{"-lb", longer, b}, stdout: stdoutFull},
		{name: "full stdout under verbose", args: []string{"-l", big1, big2}, stdout: stdoutFull},
	}
}

func TestCmpParity(t *testing.T) {
	requireParity(t, "cmp", cmpCases(t))
}

// Read the rest of stdin after cmp exits, as the next command in a shell group does.
func TestCmpInputPosition(t *testing.T) {
	const data = "0123456789"
	dir := t.TempDir()
	path := writeFile(t, dir, "input", data)
	other := writeFile(t, dir, "copy", data)
	prefix := writeFile(t, dir, "prefix", "12")
	bin := fernBin(t, "cmp")
	for _, tc := range []struct {
		name   string
		pipe   bool
		start  int64
		args   []string
		status int
		rest   string
	}{
		{"bounded file", false, 0, []string{"-n", "3", "-", other}, 0, "3456789"},
		{"quiet unequal remaining sizes", false, 1, []string{"-s", "-", other}, 1, "123456789"},
		{"quiet unequal sizes after skip", false, 1, []string{"-s", "-i", "2", "-", other}, 1, "3456789"},
		{"quiet limit excludes extra byte", false, 1, []string{"-s", "-n", "2", "-", prefix}, 0, "3456789"},
		{"bounded pipe", true, 0, []string{"-n", "3", other, "-"}, 0, "3456789"},
		{"skip and compare pipe", true, 0, []string{"-i", "2", "-n", "3", "-", other}, 0, "56789"},
		{"skip only pipe", true, 0, []string{"-i", "2", "-n", "0", "-", other}, 0, "23456789"},
		{"same pipe with different skips", true, 0, []string{"-", "-", "1", "2"}, 0, data},
		{"shared file position after first skip", false, 1, []string{"-", "-", "2", "0"}, 0, "3456789"},
		{"shared file quiet remaining sizes", false, 1, []string{"-s", "-", "-", "0", "2"}, 1, "3456789"},
		{"same inode different positions", false, 1, []string{"-n", "2", "-", path}, 1, "3456789"},
		{"same inode equal positions after skips", false, 1, []string{path, "-", "3", "2"}, 0, "3456789"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in *os.File
			var err error
			if tc.pipe {
				var out *os.File
				in, out, err = os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer in.Close()
				_, err = io.WriteString(out, data)
				out.Close()
			} else {
				in, err = os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer in.Close()
				_, err = in.Seek(tc.start, io.SeekStart)
			}
			if err != nil {
				t.Fatal(err)
			}
			argv := crossArgv(bin, tc.args...)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin = in
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if err != nil && !errors.As(err, &exited) {
				t.Fatal(err)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.status {
				t.Fatalf("exit %d, want %d: %s", code, tc.status, output)
			}
			rest, err := io.ReadAll(in)
			if err != nil {
				t.Fatal(err)
			}
			if string(rest) != tc.rest {
				t.Errorf("remaining stdin %q, want %q", rest, tc.rest)
			}
		})
	}
}

// GNU cmp accepts two references to stdin at the same offset without reading it.
func TestCmpIdenticalClosedStdin(t *testing.T) {
	argv := crossArgv(fernBin(t, "cmp"), "-i", "1", "-", "-")
	args := append([]string{"-c", `exec 0<&-; exec "$@"`, "sh"}, argv...)
	if out, err := exec.Command("sh", args...).CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("cmp on identical stdin operands: %v, %s", err, out)
	}
}

func TestCmpVerbosePosition(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "input", strings.Repeat("a", 100))
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, err := in.Seek(98, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	other := writeFile(t, dir, "other", "b"+strings.Repeat("a", 99))
	argv := crossArgv(fernBin(t, "cmp"), "-l", "-", other, "1", "0")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = in
	output, err := cmd.Output()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || string(output) != "1 141 142\n" {
		t.Fatalf("cmp -l: %q, %v", output, err)
	}
}

func TestCmpHelpVersion(t *testing.T) {
	requireHelp(t, "cmp", []string{"--help"}, 0)
	requireHelp(t, "cmp", []string{"--he"}, 0)
	requireVersion(t, "cmp", []string{"--version"}, 0)
	requireVersion(t, "cmp", []string{"-v"}, 0)
	requireVersion(t, "cmp", []string{"--vers"}, 0)
}
