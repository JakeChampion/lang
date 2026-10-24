package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func teeSeed(t *testing.T, dir string) {
	t.Helper()
	for name, content := range map[string]string{
		"exists": "PREEXISTING\n",
		"f name": "",
		"f'n":    "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seedRawByteName(t, dir, "")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d2"), 0o755); err != nil {
		t.Fatal(err)
	}
}

var teePipeStdin = strings.Repeat("0123456789abcdef", 16384)

func init() {
	registerCorpus("tee", teeCases)
}

func teeCases(t *testing.T) []invocation {
	big := strings.Repeat("line\n", 40000)
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	raw := strings.Repeat(string(allBytes), 1025) + "\xff\xc0\x80\xed\xa0\x80\xf4\x90\x80\x80"
	aDir := t.TempDir()

	return []invocation{
		{name: "no operands", stdin: "hello\nworld\n"},
		{name: "one file", args: []string{"a"}, stdin: "hello\nworld\n", seedTree: teeSeed},
		{name: "two files", args: []string{"a", "b"}, stdin: "hello\nworld\n", seedTree: teeSeed},
		{name: "three files", args: []string{"a", "b", "c"}, stdin: "x\n", seedTree: teeSeed},
		{name: "empty stdin", stdin: ""},
		{name: "empty stdin one file", args: []string{"a"}, stdin: "", seedTree: teeSeed},
		{name: "no trailing newline", args: []string{"a"}, stdin: "x", seedTree: teeSeed},
		{name: "binary stdin", args: []string{"a"}, stdin: "\x00\x01\xff\xfe\n\x00", seedTree: teeSeed},
		{name: "raw byte fanout across blocks", args: []string{"a", "b"}, stdin: raw, seedTree: teeSeed},
		{name: "raw byte append across blocks", args: []string{"-a", "exists", "a"}, stdin: raw, seedTree: teeSeed},
		{name: "large stdin", args: []string{"a"}, stdin: big, seedTree: teeSeed},
		{name: "large stdin two files", args: []string{"a", "b"}, stdin: big, seedTree: teeSeed},
		{name: "large stdin no operands", stdin: big},
		{name: "same file twice", args: []string{"a", "a"}, stdin: "hello\n", seedTree: teeSeed},
		{name: "dash is a file called dash", args: []string{"-"}, stdin: "x\n", seedTree: teeSeed},
		{name: "truncates an existing file", args: []string{"exists"}, stdin: "x\n", seedTree: teeSeed},

		{name: "append", args: []string{"-a", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "append long", args: []string{"--append", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "append prefix", args: []string{"--ap", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "append creates a missing file", args: []string{"-a", "a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "append two files", args: []string{"-a", "exists", "a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "append the same file twice", args: []string{"-a", "exists", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "append a directory", args: []string{"-a", "d"}, stdin: "x\n", seedTree: teeSeed},

		{name: "directory", args: []string{"d"}, stdin: "hello\n", seedTree: teeSeed},
		{name: "directory then a file", args: []string{"d", "a"}, stdin: "hello\n", seedTree: teeSeed},
		{name: "file then a directory", args: []string{"a", "d"}, stdin: "hello\n", seedTree: teeSeed},
		{name: "two directories", args: []string{"d", "d2"}, stdin: "hello\n", seedTree: teeSeed},
		{name: "missing parent directory", args: []string{"nodir/f"}, stdin: "x\n", seedTree: teeSeed},
		{name: "empty operand", args: []string{""}, stdin: "x\n", seedTree: teeSeed},
		{name: "empty operand then a file", args: []string{"", "a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "name with a space", args: []string{"f name"}, stdin: "x\n", seedTree: teeSeed},
		{name: "name with a quote", args: []string{"f'n"}, stdin: "x\n", seedTree: teeSeed},
		{name: "name that is not valid UTF-8", args: []string{rawByteNameFixture}, stdin: "x\n", seedTree: teeSeed, rawByteName: true},
		{name: "missing name with a space", args: []string{"no dir/f"}, stdin: "x\n", seedTree: teeSeed},
		{name: "missing name that is not valid UTF-8", args: []string{"no\xffdir/f"}, stdin: "x\n", seedTree: teeSeed},
		{name: "dashdash then an option-looking name", args: []string{"--", "-a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "dashdash alone", args: []string{"--"}, stdin: "x\n", seedTree: teeSeed},

		{name: "exit stops at a failing open", args: []string{"--output-error=exit", "d", "a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "exit-nopipe stops at a failing open", args: []string{"--output-error=exit-nopipe", "d", "a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "exit stops after opening the good ones", args: []string{"--output-error=exit", "a", "d", "b"}, stdin: "x\n", seedTree: teeSeed},
		{name: "warn keeps opening after a failure", args: []string{"--output-error=warn", "a", "d", "b"}, stdin: "x\n", seedTree: teeSeed},
		{name: "p keeps opening after a failure", args: []string{"-p", "d", "a"}, stdin: "x\n", seedTree: teeSeed},

		{name: "directory on stdin", args: []string{"a"}, stdinPath: aDir, seedTree: teeSeed},
		{name: "directory on stdin no operands", stdinPath: aDir},

		{name: "stdout full", args: []string{"a"}, stdin: "hello\n", stdout: stdoutFull, seedTree: teeSeed},
		{name: "stdout full with p", args: []string{"-p", "a"}, stdin: "hello\n", stdout: stdoutFull, seedTree: teeSeed},
		{name: "stdout full with warn", args: []string{"--output-error=warn", "a"}, stdin: "hello\n", stdout: stdoutFull, seedTree: teeSeed},
		{name: "stdout full with warn-nopipe", args: []string{"--output-error=warn-nopipe", "a"}, stdin: "hello\n", stdout: stdoutFull, seedTree: teeSeed},
		{name: "stdout full with exit", args: []string{"--output-error=exit", "a"}, stdin: "hello\n", stdout: stdoutFull, seedTree: teeSeed},
		{name: "stdout full with exit-nopipe", args: []string{"--output-error=exit-nopipe", "a"}, stdin: "hello\n", stdout: stdoutFull, seedTree: teeSeed},
		{name: "stdout full no operands", stdin: "hello\n", stdout: stdoutFull},
		{name: "stdout full with nothing to write", stdin: "", stdout: stdoutFull},
		{name: "stdout closed", stdin: "hello\n", stdout: stdoutClosed},
		{name: "stdout closed with a file operand", args: []string{"a"}, stdin: "hello\n", stdout: stdoutClosed, seedTree: teeSeed},
		{name: "stdout closed with two file operands", args: []string{"a", "b"}, stdin: "hello\n", stdout: stdoutClosed, seedTree: teeSeed},
		{name: "stdout closed with nothing to write", stdin: "", stdout: stdoutClosed},
		{name: "stdout closed with exit", args: []string{"--output-error=exit"}, stdin: "hello\n", stdout: stdoutClosed},
		{name: "full file operand", args: []string{"/dev/full"}, stdin: "hello\n"},
		{name: "full file operand with exit", args: []string{"--output-error=exit", "/dev/full"}, stdin: "hello\n"},
		{name: "full file operand with p", args: []string{"-p", "/dev/full"}, stdin: "hello\n"},

		{name: "pipe closed", stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with p", args: []string{"-p"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with warn", args: []string{"--output-error=warn"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with warn-nopipe", args: []string{"--output-error=warn-nopipe"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with exit", args: []string{"--output-error=exit"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with exit-nopipe", args: []string{"--output-error=exit-nopipe"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with a bare output-error", args: []string{"--output-error"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with a prefixed mode", args: []string{"--output-error=warn-"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with exit- prefix", args: []string{"--output-error=exit-"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with p and a file", args: []string{"-p", "a"}, stdin: teePipeStdin, limit: 4096, seedTree: teeSeed},
		{name: "pipe closed with warn and a file", args: []string{"--output-error=warn", "a"}, stdin: teePipeStdin, limit: 4096, seedTree: teeSeed},
		{name: "pipe closed with warn-nopipe and a file", args: []string{"--output-error=warn-nopipe", "a"}, stdin: teePipeStdin, limit: 4096, seedTree: teeSeed},
		{name: "pipe closed with exit-nopipe and a file", args: []string{"--output-error=exit-nopipe", "a"}, stdin: teePipeStdin, limit: 4096, seedTree: teeSeed},

		{name: "sigint", stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i", args: []string{"-i"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with ignore-interrupts", args: []string{"--ignore-interrupts"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with the ignore prefix", args: []string{"--ig"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and p", args: []string{"-i", "-p"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with the ip cluster", args: []string{"-ip"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and warn", args: []string{"-i", "--output-error=warn"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and exit", args: []string{"-i", "--output-error=exit"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and exit-nopipe", args: []string{"-i", "--output-error=exit-nopipe"}, stdin: teePipeStdin, limit: 4096, sigint: true},

		{name: "output-error invalid", args: []string{"--output-error=bogus"}},
		{name: "output-error empty is ambiguous", args: []string{"--output-error="}},
		{name: "output-error w is ambiguous", args: []string{"--output-error=w"}},
		{name: "output-error e is ambiguous", args: []string{"--output-error=e"}},
		{name: "output-error wa is unique", args: []string{"--output-error=wa"}, stdin: "x\n"},
		{name: "output-error ex is unique", args: []string{"--output-error=ex"}, stdin: "x\n"},
		{name: "output-error exact warn beats its extension", args: []string{"--output-error=warn"}, stdin: "x\n"},
		{name: "output-error exact exit beats its extension", args: []string{"--output-error=exit"}, stdin: "x\n"},
		{name: "output-error uppercase", args: []string{"--output-error=WARN"}},
		{name: "output-error takes no separate argument", args: []string{"--output-error", "warn"}, stdin: "x\n", seedTree: teeSeed},
		{name: "output-error twice", args: []string{"--output-error=exit", "--output-error=warn"}, stdin: "x\n"},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option in a cluster", args: []string{"-ax"}},
		{name: "p takes no value", args: []string{"-p5"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "append rejects a glued value", args: []string{"--append=1"}},
		{name: "ignore-interrupts rejects a glued value", args: []string{"--ignore-interrupts=1"}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "empty long name is ambiguous", args: []string{"--=x"}},
		{name: "a is unique", args: []string{"--a", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "i is unique", args: []string{"--i"}, stdin: "x\n"},
		{name: "o is unique", args: []string{"--o"}, stdin: "x\n"},
		{name: "cluster ai", args: []string{"-ai", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "cluster ia", args: []string{"-ia", "exists"}, stdin: "x\n", seedTree: teeSeed},
		{name: "operand before an option is permuted out", args: []string{"a", "-a"}, stdin: "x\n", seedTree: teeSeed},
		{name: "posix stops at the first operand", args: []string{"a", "-a"}, stdin: "x\n", env: []string{"POSIXLY_CORRECT=1"}, seedTree: teeSeed},
		{name: "posix option before the operand", args: []string{"-a", "exists"}, stdin: "x\n", env: []string{"POSIXLY_CORRECT=1"}, seedTree: teeSeed},
	}
}

func TestTeeParity(t *testing.T) {
	requireParity(t, "tee", teeCases(t))
}

func TestTeeHelpVersion(t *testing.T) {
	requireHelp(t, "tee", []string{"--help"}, 0)
	requireHelp(t, "tee", []string{"--he"}, 0)
	requireHelp(t, "tee", []string{"--help", "ignored"}, 0)
	requireVersion(t, "tee", []string{"--version"}, 0)
	requireVersion(t, "tee", []string{"--vers"}, 0)
}
