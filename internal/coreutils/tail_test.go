package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tailCases is tail(1)'s corpus.
//
// The counts, the headers and the obsolete form are head's mirrored,
// and the corpus pins the two things tail adds to them: the seeking
// paths — a regular file is read from its END, in 8 KiB blocks for
// lines, so every count has a case that crosses a block boundary and
// a case fed through a pipe — and following. A follow case never ends
// on its own: the harness plays the writer, firing each step once the
// output has reached it, and stops both sides after `limit` bytes; the
// polling interval and `---disable-inotify` keep GNU on the same loop.
// `--pid` is parsed here but not followed with (#8767).
func tailCases(t *testing.T) []invocation {
	dir := t.TempDir()
	twelve := catFile(t, dir, "twelve", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n")
	f := catFile(t, dir, "f", "1\n2\n3\n4\n5\n")
	five := catFile(t, dir, "five", "abcde")
	nonl := catFile(t, dir, "nonl", "a\nb")
	x := catFile(t, dir, "x", "x\n")
	empty := catFile(t, dir, "e0", "")
	zeros := catFile(t, dir, "zeros", "a\x00b\x00c\x00d")
	spaced := catFile(t, dir, "f name", "x\n")
	quoted := catFile(t, dir, "f'n", "x\n")
	raw := catFile(t, dir, "na\xffme", "x\n")
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	// Past several 8 KiB blocks, so the backward walk and the forward
	// skip both cross boundaries; a line that straddles one too.
	big := catFile(t, dir, "big", seqLines(100000))
	longLines := catFile(t, dir, "longlines", strings.Repeat(strings.Repeat("y", 5000)+"\n", 10))
	bigStdin := seqLines(100000)

	// Followed files, one per case: the harness rewrites them.
	follow := func(name, content string) string { return catFile(t, dir, name, content) }
	g1 := follow("g1", "1\n2\n")
	g2 := follow("g2", "1\n2\n")
	g3 := follow("g3", "1\n2\n")
	g4 := follow("g4", "1\n2\n")
	g5 := follow("g5", "1\n2\n")
	g5new := catFile(t, dir, "g5new", "new\n")
	g6 := follow("g6", "1\n2\n")
	k6 := follow("k6", "k\n")
	g7 := follow("g7", "1\n2\n")
	g8 := follow("g8", "1\n2\n")
	g9 := follow("g9", "1\n2\n")
	g10 := follow("g10", "1\n2\n")
	g11 := follow("g11", "1\n2\n")
	poll := []string{"---disable-inotify", "-s", "0.05"}
	fol := func(args ...string) []string { return append(append([]string{}, poll...), args...) }

	return []invocation{
		// The last lines.
		{name: "default ten", args: []string{twelve}},
		{name: "n 3", args: []string{"-n", "3", f}},
		{name: "n 0", args: []string{"-n", "0", f}},
		{name: "n more than there are", args: []string{"-n", "100", f}},
		{name: "n minus", args: []string{"-n", "-2", f}},
		{name: "lines long", args: []string{"--lines", "2", f}},
		{name: "lines glued", args: []string{"--lines=2", f}},
		{name: "n glued", args: []string{"-n2", f}},
		{name: "unterminated last line", args: []string{"-n", "1", nonl}},
		{name: "unterminated two", args: []string{"-n", "2", nonl}},
		{name: "unterminated more", args: []string{"-n", "5", nonl}},
		{name: "empty file", args: []string{f, empty}},
		{name: "empty file alone", args: []string{empty}},
		{name: "big last 5", args: []string{"-n", "5", big}},
		{name: "big across blocks", args: []string{"-n", "20000", big}},
		{name: "big all", args: []string{"-n", "200000", big}},
		{name: "long lines", args: []string{"-n", "3", longLines}},
		{name: "long lines one", args: []string{"-n", "1", longLines}},

		// From the start.
		{name: "n plus", args: []string{"-n", "+2", f}},
		{name: "n plus zero", args: []string{"-n", "+0", f}},
		{name: "n plus one", args: []string{"-n", "+1", f}},
		{name: "n plus past end", args: []string{"-n", "+100", f}},
		{name: "n plus then minus keeps from start", args: []string{"-n", "+2", "-n", "3", f}},
		{name: "big from 50000", args: []string{"-n", "+50000", big}},
		{name: "c plus", args: []string{"-c", "+3", five}},
		{name: "c plus zero", args: []string{"-c", "+0", five}},
		{name: "c plus past end", args: []string{"-c", "+100", five}},
		{name: "big c from 300000", args: []string{"-c", "+300000", big}},

		// Bytes.
		{name: "c 2", args: []string{"-c", "2", five}},
		{name: "c 0", args: []string{"-c", "0", five}},
		{name: "c more than there are", args: []string{"-c", "100", five}},
		{name: "c minus", args: []string{"-c", "-2", five}},
		{name: "bytes long", args: []string{"--bytes=2", five}},
		{name: "c across newline", args: []string{"-c", "2", nonl}},
		{name: "big c 100000", args: []string{"-c", "100000", big}},
		{name: "c suffix k", args: []string{"-c", "1k", five}},
		{name: "c suffix b", args: []string{"-c", "1b", big}},

		// Streams: a pipe, and a file told to be one.
		{name: "stdin last 2", args: []string{"-n", "2"}, stdin: "1\n2\n3\n"},
		{name: "stdin dash", args: []string{"-n", "2", "-"}, stdin: "1\n2\n3\n"},
		{name: "stdin unterminated", args: []string{"-n", "2"}, stdin: "1\n2\n3"},
		{name: "stdin n 0", args: []string{"-n", "0"}, stdin: "1\n2\n3\n"},
		{name: "stdin empty", args: []string{"-n", "2"}, stdin: ""},
		{name: "stdin big last 5", args: []string{"-n", "5"}, stdin: bigStdin},
		{name: "stdin big across blocks", args: []string{"-n", "20000"}, stdin: bigStdin},
		{name: "stdin big all", args: []string{"-n", "200000"}, stdin: bigStdin},
		{name: "stdin from start", args: []string{"-n", "+2"}, stdin: "1\n2\n3\n"},
		{name: "stdin big from 50000", args: []string{"-n", "+50000"}, stdin: bigStdin},
		{name: "stdin c 2", args: []string{"-c", "2"}, stdin: "abcde"},
		{name: "stdin c big", args: []string{"-c", "100000"}, stdin: bigStdin},
		{name: "stdin c from start", args: []string{"-c", "+2"}, stdin: "abcde"},
		{name: "stdin c big from start", args: []string{"-c", "+300000"}, stdin: bigStdin},
		{name: "regular file on stdin", args: []string{"-n", "2"}, stdinPath: f},
		{name: "regular file on stdin bytes", args: []string{"-c", "2"}, stdinPath: five},
		{name: "regular file on stdin from start", args: []string{"-n", "+3"}, stdinPath: f},
		{name: "directory on stdin", args: []string{"-n", "1"}, stdinPath: d},
		{name: "presume pipe lines", args: []string{"---presume-input-pipe", "-n", "2", big}},
		{name: "presume pipe bytes", args: []string{"---presume-input-pipe", "-c", "2", five}},
		{name: "presume pipe from start", args: []string{"---presume-input-pipe", "-c", "+3", five}},

		// -z.
		{name: "zero terminated", args: []string{"-z", "-n", "2", zeros}},
		{name: "zero terminated from start", args: []string{"-z", "-n", "+2", zeros}},
		{name: "zero terminated long", args: []string{"--zero-terminated", "-n", "1", zeros}},
		{name: "zero terminated stdin", args: []string{"-z", "-n", "2"}, stdin: "a\x00b\x00c\x00d"},

		// Headers.
		{name: "two files", args: []string{"-n", "1", f, x}},
		{name: "stdin among files", args: []string{"-n", "1", f, "-", x}, stdin: "s\n"},
		{name: "quiet", args: []string{"-q", "-n", "1", f, x}},
		{name: "quiet long", args: []string{"--quiet", "-n", "1", f, x}},
		{name: "silent", args: []string{"--silent", "-n", "1", f, x}},
		{name: "verbose", args: []string{"-v", "-n", "1", f}},
		{name: "verbose long", args: []string{"--verbose", "-n", "1", f}},
		{name: "verbose then quiet", args: []string{"-n", "1", "-v", "-q", f, x}},
		{name: "quiet then verbose", args: []string{"-n", "1", "-q", "-v", f, x}},
		{name: "header spaced name", args: []string{"-n", "1", f, spaced}},
		{name: "header quoted name", args: []string{"-n", "1", f, quoted}},
		{name: "header raw name", args: []string{"-n", "1", f, raw}},
		{name: "header after a missing file", args: []string{"-n", "1", missing, f}},
		{name: "missing between files", args: []string{"-n", "1", f, missing, f}},

		// Errors.
		{name: "missing file", args: []string{missing}},
		{name: "missing raw name", args: []string{filepath.Join(dir, "no\xffsuch")}},
		{name: "missing quoted name", args: []string{filepath.Join(dir, "no'such")}},
		{name: "empty operand", args: []string{"-n", "1", ""}},
		{name: "empty operand after a file", args: []string{"-n", "1", f, ""}},
		{name: "directory", args: []string{"-n", "3", d}},
		{name: "directory bytes", args: []string{"-c", "3", d}},
		{name: "directory from start", args: []string{"-n", "+1", d}},
		{name: "directory from start then a file", args: []string{"-n", "+1", d, f}},
		{name: "directory bytes from start", args: []string{"-c", "+1", d, f}},
		{name: "double dash", args: []string{"-n", "2", "--", f}},
		{name: "double dash alone", args: []string{"--", f}},
		{name: "dash file after double dash", args: []string{"-n", "1", "--", "-"}, stdin: "s\n"},

		// Counts.
		{name: "n empty", args: []string{"-n", "", f}},
		{name: "n dash", args: []string{"-n", "-", f}},
		{name: "n dash alone", args: []string{"-n", "-"}},
		{name: "n plus alone", args: []string{"-n", "+"}},
		{name: "n plus blank", args: []string{"-n", "+ 3", f}},
		{name: "n minus blank", args: []string{"-n", "- 3", f}},
		{name: "n leading blank", args: []string{"-n", " 3", f}},
		{name: "n trailing blank", args: []string{"-n", "3 ", f}},
		{name: "n overflow", args: []string{"-n", "99999999999999999999", f}},
		{name: "n huge", args: []string{"-n", "18446744073709551615", f}},
		{name: "c overflow", args: []string{"-c", "99999999999999999999", f}},
		{name: "c bad suffix", args: []string{"-c", "1x", f}},
		{name: "c word", args: []string{"-c", "five", five}},
		{name: "n hex", args: []string{"-n", "0x3", f}},
		{name: "c kib", args: []string{"-c", "1KiB", big}},
		{name: "c kb", args: []string{"-c", "1kB", big}},

		// The obsolete form.
		{name: "obsolete bytes", args: []string{"-5c", five}},
		{name: "obsolete lines", args: []string{"-2", f}},
		{name: "obsolete plus", args: []string{"+2", f}},
		{name: "obsolete plus bytes", args: []string{"+2c", five}},
		{name: "obsolete l", args: []string{"-2l", f}},
		{name: "obsolete b", args: []string{"-1b", big}},
		{name: "obsolete b lines", args: []string{"-b", big}},
		{name: "obsolete l alone", args: []string{"-l", f}},
		{name: "obsolete c alone", args: []string{"-c", f}},
		{name: "obsolete c alone bytes", args: []string{"-c", five}},
		{name: "obsolete alone", args: []string{"-2"}, stdin: "1\n2\n3\n"},
		{name: "obsolete with dash", args: []string{"-2", "-"}, stdin: "1\n2\n3\n"},
		{name: "obsolete with double dash", args: []string{"-2", "--", f}},
		{name: "obsolete with double dash and two", args: []string{"-2", "--", f, x}},
		{name: "obsolete with double dash and three", args: []string{"-2", "--", f, x, f}},
		{name: "obsolete two files", args: []string{"-2", f, x}},
		{name: "obsolete then option", args: []string{"-2", "-q"}},
		{name: "obsolete bad suffix", args: []string{"-2x", f}},
		{name: "obsolete overflow", args: []string{"-99999999999999999999", f}},
		{name: "obsolete b overflow", args: []string{"-99999999999999999b", f}},
		// The obsolete form prints the raw errno, so where the overflow
		// happened decides whether strerror text follows: 2^64/512 fits
		// strtoumax and only the multiply overflows, 2^64 does not.
		{name: "obsolete b overflow at the multiply", args: []string{"-36028797018963968b", f}},
		{name: "obsolete b overflow in the digits", args: []string{"-18446744073709551616b", f}},
		{name: "obsolete b just under the multiply", args: []string{"-36028797018963967b", f}},
		{name: "digit after file", args: []string{f, "-5"}},
		{name: "digit in cluster", args: []string{"-q5", f}},
		{name: "digit first in cluster", args: []string{"-5q", f}},
		{name: "posix 200112 plus", args: []string{"+2", f}, env: []string{"_POSIX2_VERSION=200112"}},
		{name: "posix 200112 minus", args: []string{"-2", f}, env: []string{"_POSIX2_VERSION=200112"}},
		{name: "posix 200112 c alone", args: []string{"-c", f}, env: []string{"_POSIX2_VERSION=200112"}},
		{name: "posix 200112 dash alone", args: []string{"-", f}, env: []string{"_POSIX2_VERSION=200112"}},
		{name: "posix 199209 c alone", args: []string{"-c", f}, env: []string{"_POSIX2_VERSION=199209"}},
		{name: "posix 199209 dash alone", args: []string{"-", f}, env: []string{"_POSIX2_VERSION=199209"}},
		{name: "posix 199209 plus", args: []string{"+2", f}, env: []string{"_POSIX2_VERSION=199209"}},
		{name: "posix words", args: []string{"+2", f}, env: []string{"_POSIX2_VERSION=abc"}},
		{name: "posix leading blank", args: []string{"+2", f}, env: []string{"_POSIX2_VERSION= 200112"}},
		{name: "posix trailing junk", args: []string{"+2", f}, env: []string{"_POSIX2_VERSION=200112x"}},
		{name: "posix empty", args: []string{"+2", f}, env: []string{"_POSIX2_VERSION="}},
		{name: "posix negative", args: []string{"-c", f}, env: []string{"_POSIX2_VERSION=-5"}},

		// The follow options, parsed without following.
		{name: "retry without follow", args: []string{"--retry", "-n", "1", f}},
		{name: "pid without follow", args: []string{"--pid=1", "-n", "1", f}},
		{name: "pid words", args: []string{"--pid=x", f}},
		{name: "pid too large", args: []string{"--pid=99999999999", f}},
		{name: "pid huge", args: []string{"--pid=99999999999999999999", f}},
		{name: "pid zero", args: []string{"--pid=0", "-n", "1", f}},
		{name: "pid plus zero", args: []string{"--pid=+0", "-n", "1", f}},
		{name: "pid negative", args: []string{"--pid=-1", f}},
		{name: "pid suffix", args: []string{"--pid=1k", f}},
		{name: "sleep words", args: []string{"-s", "x", f}},
		{name: "sleep negative", args: []string{"-s", "-1", f}},
		{name: "sleep empty", args: []string{"-s", "", f}},
		{name: "sleep nan", args: []string{"-s", "nan", f}},
		{name: "sleep bare exponent", args: []string{"-s", "1e", f}},
		{name: "sleep leading blank", args: []string{"-s", " 1", "-n", "1", f}},
		{name: "sleep trailing blank", args: []string{"-s", "1 ", f}},
		{name: "sleep hex", args: []string{"-s", "0x1p-3", "-n", "1", f}},
		{name: "sleep bad hex", args: []string{"-s", "0x", f}},
		{name: "sleep inf", args: []string{"-s", "inf", "-n", "1", f}},
		{name: "sleep exponent", args: []string{"-s", "1.5e2", "-n", "1", f}},
		{name: "sleep point", args: []string{"-s", ".5", "-n", "1", f}},
		{name: "sleep trailing point", args: []string{"-s", "5.", "-n", "1", f}},
		{name: "sleep plus", args: []string{"-s", "+1", "-n", "1", f}},
		{name: "sleep minus zero", args: []string{"-s", "-0", "-n", "1", f}},
		{name: "sleep long", args: []string{"--sleep-interval=2", "-n", "1", f}},
		{name: "unchanged zero", args: []string{"--max-unchanged-stats=0", "-n", "1", f}},
		{name: "unchanged words", args: []string{"--max-unchanged-stats=x", f}},
		{name: "unchanged blank", args: []string{"--max-unchanged-stats= 5", "-n", "1", f}},
		{name: "unchanged overflow", args: []string{"--max-unchanged-stats=99999999999999999999", f}},
		{name: "unchanged negative", args: []string{"--max-unchanged-stats=-1", f}},
		{name: "unchanged suffix", args: []string{"--max-unchanged-stats=1k", f}},
		{name: "follow empty how", args: []string{"--follow=", f}},
		{name: "follow bad how", args: []string{"--follow=foo", f}},
		{name: "follow name on stdin", args: []string{"--follow=n", "-"}},
		{name: "follow name on stdin long", args: []string{"--follow=name", "-n", "1", "-"}},
		{name: "big f on stdin", args: []string{"-F", "-n", "1", "-"}},
		{name: "big f on stdin among files", args: []string{"-F", "-n", "1", f, "-"}},
		{name: "follow glued short", args: []string{"-fname", f}},
		{name: "disable inotify", args: []string{"---disable-inotify", "-n", "1", f}},
		{name: "follow stdin pipe ends at eof", args: []string{"-f"}, stdin: "a\nb\n"},
		{name: "follow stdin pipe dash ends at eof", args: []string{"-f", "-n", "1", "-"}, stdin: "a\nb\n"},
		{name: "follow missing file", args: []string{"-f", missing}},
		{name: "follow missing then missing", args: []string{"-f", missing, filepath.Join(dir, "nosuch2")}},
		{name: "follow directory", args: []string{"-f", "-n", "0", d}},
		{name: "follow directory by name no retry", args: []string{"--follow=name", "-n", "0", d}},
		{name: "follow name missing no retry", args: []string{"--follow=name", missing}},
		{name: "obsolete f missing", args: []string{"-5f", missing}},

		// Following.
		{name: "follow appends", args: fol("-f", "-n", "1", g1), limit: 4,
			follow: []followStep{{after: 2, act: "append", path: g1, data: "3\n"}}},
		// `--follow` bare: the long form's argument is optional, so the
		// next token is an operand rather than a value.
		{name: "follow bare long", args: fol("--follow", "-n", "1", g11), limit: 4,
			follow: []followStep{{after: 2, act: "append", path: g11, data: "3\n"}}},
		{name: "follow truncated", args: fol("-f", "-n", "1", g2), limit: 4,
			follow: []followStep{{after: 2, act: "truncate", path: g2, data: "x\n"}}},
		{name: "follow descriptor survives a rename", args: fol("-f", "-n", "1", g3), limit: 4,
			follow: []followStep{{after: 2, act: "rename", path: filepath.Join(dir, "g3moved"), data: g3}, {after: 2, act: "append", path: filepath.Join(dir, "g3moved"), data: "3\n"}}},
		{name: "follow name gives up on a renamed file", args: fol("--follow=name", "--max-unchanged-stats=1", "-n", "1", g4), limit: 100,
			follow: []followStep{{after: 2, act: "rename", path: filepath.Join(dir, "g4moved"), data: g4}}},
		{name: "follow name sees a replacement", args: fol("-F", "--max-unchanged-stats=1", "-n", "1", g5), limit: 6,
			follow: []followStep{{after: 2, act: "rename", path: g5, data: g5new}}},
		{name: "follow name sees removal and return", args: fol("-F", "--max-unchanged-stats=0", "-n", "1", g6, k6), limit: 43,
			follow: []followStep{{after: 22, act: "remove", path: g6}, {after: 22, act: "append", path: k6, data: "k2\n"}, {after: 36, act: "truncate", path: g6, data: "new\n"}}},
		{name: "follow two files", args: fol("-f", "-n", "1", g7, x), limit: 30,
			follow: []followStep{{after: 20, act: "append", path: g7, data: "3\n"}}},
		// The limit has to clear the WHOLE initial pass, not just reach the
		// step: stderr is compared however the child ends, and the child is
		// terminated on a stdout byte count, so a limit inside the startup
		// output makes the directory diagnostic a race between the two
		// implementations. Two `==> NAME <==` headers over temp paths are
		// already ~140 bytes, so this reads past anything the case can
		// produce and lets the deadline stop it.
		{name: "follow with a directory operand", args: fol("-f", "-n", "1", g8, d), limit: 512,
			follow: []followStep{{after: 27, act: "append", path: g8, data: "3\n"}}},
		{name: "follow stdin regular file", args: fol("-f", "-n", "1"), stdinPath: g9, limit: 4,
			follow: []followStep{{after: 2, act: "append", path: g9, data: "3\n"}}},
		{name: "follow retry on descriptor warns", args: fol("-f", "--retry", "-n", "1", g10), limit: 4,
			follow: []followStep{{after: 2, act: "append", path: g10, data: "3\n"}}},
		{name: "follow stdin pipe with a file", args: fol("-f", "-n", "1", "-", x), stdin: "s\n", limit: 36,
			follow: []followStep{{after: 34, act: "append", path: x, data: "a\n"}}},

		// Options.
		{name: "help ambiguous s", args: []string{"--s", f}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "unknown option", args: []string{"--frobnicate", f}},
		{name: "unknown short option", args: []string{"-y", f}},
		{name: "bytes needs a value", args: []string{"-c"}},
		{name: "lines needs a value", args: []string{"--lines"}},
		{name: "sleep needs a value", args: []string{"-s"}},
		{name: "quiet does not take a value", args: []string{"--quiet=x", f}},
		{name: "options after operands", args: []string{f, "-n", "1"}},
		{name: "posixly correct stops at the operand", args: []string{f, "-n", "1"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout full", args: []string{f}, stdout: stdoutFull},
		{name: "stdout full big", args: []string{"-n", "50000", big}, stdout: stdoutFull},
		{name: "stdout full from start", args: []string{"-n", "+1", big}, stdout: stdoutFull},
		{name: "stdout full with nothing to write", args: []string{"-n", "0", f}, stdout: stdoutFull},
		{name: "stdout closed", args: []string{f}, stdout: stdoutClosed},
		{name: "stdout closed with nothing to write", args: []string{"-n", "0", f}, stdout: stdoutClosed},
		{name: "stdout closed while following", args: []string{"-f", "-n", "0", x}, stdout: stdoutClosed},
		{name: "stdout closed while following with output", args: []string{"-f", "-n", "1", x}, stdout: stdoutClosed},
	}
}

// seqLines is `seq 1 n` as text.
func seqLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestTailParity(t *testing.T) {
	requireParity(t, "tail", tailCases(t))
}

func TestTailHelpVersion(t *testing.T) {
	requireHelp(t, "tail", []string{"--help"}, 0)
	requireHelp(t, "tail", []string{"--he"}, 0)
	requireHelp(t, "tail", []string{"--help", "ignored"}, 0)
	requireVersion(t, "tail", []string{"--version"}, 0)
	requireVersion(t, "tail", []string{"--vers"}, 0)
}
