package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sumFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("sum", sumCases)
}

func sumCases(t *testing.T) []invocation {
	dir := t.TempDir()
	f1 := sumFile(t, dir, "f1", "hello world\n")
	abc := sumFile(t, dir, "abc", "abc")
	empty := sumFile(t, dir, "e0", "")
	raw := sumFile(t, dir, "raw", "a\xffb\x00c\n")
	wrap := sumFile(t, dir, "wrap", strings.Repeat("\xfe\x7f\x01", 5000))
	z0 := sumFile(t, dir, "z0", "")
	z1 := sumFile(t, dir, "z1", strings.Repeat("x", 1))
	z511 := sumFile(t, dir, "z511", strings.Repeat("x", 511))
	z512 := sumFile(t, dir, "z512", strings.Repeat("x", 512))
	z513 := sumFile(t, dir, "z513", strings.Repeat("x", 513))
	z1023 := sumFile(t, dir, "z1023", strings.Repeat("x", 1023))
	z1024 := sumFile(t, dir, "z1024", strings.Repeat("x", 1024))
	z1025 := sumFile(t, dir, "z1025", strings.Repeat("x", 1025))
	wide := sumFile(t, dir, "wide", strings.Repeat("x", 100000))
	spaced := sumFile(t, dir, "f name", "x\n")
	quoted := sumFile(t, dir, "f'n", "x\n")
	rawName := rawByteFile(t, dir, "x\n")
	missing := filepath.Join(dir, "nosuch")
	missingSpaced := filepath.Join(dir, "no such")
	missingRaw := filepath.Join(dir, "ba\xffd")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")

	return []invocation{
		{name: "default is bsd", args: []string{f1}},
		{name: "explicit bsd", args: []string{"-r", f1}},
		{name: "sysv", args: []string{"-s", f1}},
		{name: "long sysv", args: []string{"--sysv", f1}},
		{name: "unique prefix sysv", args: []string{"--sy", f1}},
		{name: "shortest unique prefix", args: []string{"--s", f1}},

		{name: "cluster rs is sysv", args: []string{"-rs", f1}},
		{name: "cluster sr is bsd", args: []string{"-sr", f1}},
		{name: "separate s then r", args: []string{"-s", "-r", f1}},
		{name: "separate r then s", args: []string{"-r", "-s", f1}},
		{name: "long sysv then short r", args: []string{"--sysv", "-r", f1}},
		{name: "repeated r", args: []string{"-rr", f1}},
		{name: "repeated sysv", args: []string{"--sysv", "--sysv", f1}},
		{name: "option after the operand", args: []string{f1, "-s"}},

		{name: "empty file", args: []string{empty}},
		{name: "empty file sysv", args: []string{"-s", empty}},
		{name: "unterminated line", args: []string{abc}},
		{name: "unterminated line sysv", args: []string{"-s", abc}},
		{name: "invalid utf-8", args: []string{raw}},
		{name: "invalid utf-8 sysv", args: []string{"-s", raw}},
		{name: "wrapping accumulator", args: []string{wrap}},
		{name: "wrapping accumulator sysv", args: []string{"-s", wrap}},

		{name: "blocks 0", args: []string{z0}},
		{name: "blocks 0 sysv", args: []string{"-s", z0}},
		{name: "blocks 1", args: []string{z1}},
		{name: "blocks 1 sysv", args: []string{"-s", z1}},
		{name: "blocks 511", args: []string{z511}},
		{name: "blocks 511 sysv", args: []string{"-s", z511}},
		{name: "blocks 512", args: []string{z512}},
		{name: "blocks 512 sysv", args: []string{"-s", z512}},
		{name: "blocks 513", args: []string{z513}},
		{name: "blocks 513 sysv", args: []string{"-s", z513}},
		{name: "blocks 1023", args: []string{z1023}},
		{name: "blocks 1023 sysv", args: []string{"-s", z1023}},
		{name: "blocks 1024", args: []string{z1024}},
		{name: "blocks 1024 sysv", args: []string{"-s", z1024}},
		{name: "blocks 1025", args: []string{z1025}},
		{name: "blocks 1025 sysv", args: []string{"-s", z1025}},
		{name: "blocks past the field width", args: []string{wide}},
		{name: "blocks past the field width sysv", args: []string{"-s", wide}},

		{name: "no operand reads stdin", stdin: "hello world\n"},
		{name: "no operand reads stdin sysv", args: []string{"-s"}, stdin: "hello world\n"},
		{name: "no operand empty stdin", stdin: ""},
		{name: "no operand empty stdin sysv", args: []string{"-s"}, stdin: ""},
		{name: "dash names stdin", args: []string{"-"}, stdin: "hello world\n"},
		{name: "dash names stdin sysv", args: []string{"-s", "-"}, stdin: "hello world\n"},
		{name: "dash twice drains stdin once", args: []string{"-", "-"}, stdin: "hello world\n"},
		{name: "file and dash", args: []string{f1, "-"}, stdin: "abc"},
		{name: "dash and file", args: []string{"-", f1}, stdin: "abc"},
		{name: "two files", args: []string{f1, abc}},
		{name: "two files sysv", args: []string{"-s", f1, abc}},
		{name: "the same file twice", args: []string{f1, f1}},
		{name: "many files", args: []string{f1, abc, empty, raw, z512}},
		{name: "dashdash then a file", args: []string{"--", f1}},
		{name: "dashdash makes -s an operand", args: []string{"--", "-s"}},
		{name: "dashdash makes -r an operand", args: []string{"--", "-r"}},
		{name: "sysv before dashdash", args: []string{"-s", "--", f1}},
		{name: "stdin is a regular file", args: []string{"-"}, stdinPath: f1},

		{name: "missing file", args: []string{missing}},
		{name: "missing file sysv", args: []string{"-s", missing}},
		{name: "directory operand", args: []string{d}},
		{name: "directory operand sysv", args: []string{"-s", d}},
		{name: "directory on stdin", args: []string{"-"}, stdinPath: filepath.Join(dir, "d")},
		{name: "empty operand", args: []string{""}},
		{name: "empty operand sysv", args: []string{"-s", ""}},
		{name: "good then bad then good", args: []string{f1, missing, abc}},
		{name: "two failures", args: []string{missing, d}},

		{name: "name with a space", args: []string{spaced}},
		{name: "name with an apostrophe", args: []string{quoted}},
		{name: "name that is not utf-8", args: []string{rawName}, rawByteName: true},
		{name: "missing name with a space", args: []string{missingSpaced}},
		{name: "missing name that is not utf-8", args: []string{missingRaw}},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option in a cluster", args: []string{"-sx", f1}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "sysv rejects a value", args: []string{"--sysv=1", f1}},
		{name: "help rejects a value", args: []string{"--help=x"}},
		{name: "version rejects a value", args: []string{"--version=x"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "bare double dash prefix is ambiguous", args: []string{"--", "--"}},
		{name: "an option after a bad one is not reached", args: []string{"-x", "--version"}},

		{name: "posix operand then option", args: []string{f1, "-s"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-s", f1}, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "stdout closed", args: []string{f1}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{f1}, stdout: stdoutFull},
		{name: "stdout closed with only a failure", args: []string{missing}, stdout: stdoutClosed},
		{name: "stdout full with only a failure", args: []string{missing}, stdout: stdoutFull},
		{name: "stdout closed reading stdin", stdin: "hello world\n", stdout: stdoutClosed},
		{name: "stdout full past one block", args: sumRepeat(f1, 600), stdout: stdoutFull},
		{name: "stdout closed past one block", args: sumRepeat(f1, 600), stdout: stdoutClosed},
	}
}

func sumRepeat(path string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = path
	}
	return out
}

func TestSumParity(t *testing.T) {
	requireParity(t, "sum", sumCases(t))
}

func TestSumHelpVersion(t *testing.T) {
	requireHelp(t, "sum", []string{"--help"}, 0)
	requireHelp(t, "sum", []string{"--hel"}, 0)
	requireVersion(t, "sum", []string{"--version"}, 0)
	requireVersion(t, "sum", []string{"--vers"}, 0)
	requireHelp(t, "sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sum", []string{"x", "--version"}, 0)
	requireVersion(t, "sum", []string{"--version", "-x"}, 0)
	requireHelp(t, "sum", []string{"--help", "-x"}, 0)
	requireHelp(t, "sum", []string{"--help", "extra"}, 0)
}
