package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// teeSeed writes the files a case starts from into its working
// directory. Every operand here is relative, so the tree the two sides
// leave behind is comparable.
func teeSeed(t *testing.T, dir string) {
	t.Helper()
	for name, content := range map[string]string{
		"exists":   "PREEXISTING\n",
		"f name":   "",
		"f'n":      "",
		"na\xffme": "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d2"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// teePipeStdin is long enough that the child is still writing when the
// harness closes the read end, so the broken-pipe cases reach a write
// that fails rather than finishing first. A pipe holds 64 KiB.
var teePipeStdin = strings.Repeat("0123456789abcdef", 16384) // 256 KiB

// teeCases is tee(1)'s corpus.
//
// Three things need pinning beyond the copy. The FILEs are opened
// before the first read, so which of them exist after a failure is
// part of the answer and the cases carry a working directory whose
// tree is compared. The `--output-error` family is a 2x2 of "does a
// failure get a diagnostic" and "does it end the program", with a
// broken pipe the one error the `nopipe` half passes over, and its
// fifth state is the absence of the option — SIGPIPE left at its
// default, so tee dies of it. And `-i` is a signal disposition whose
// only observable is what happens when the signal arrives, which is
// the `sigint` cases: with `-i` the SIGINT is ignored and the closing
// read end kills with SIGPIPE instead, which is the same run proving
// that -i did not touch SIGPIPE.
//
// Every EPIPE case with a file operand is in a mode that KEEPS GOING,
// where the file ends up with all of stdin whatever the block size. In
// a mode that stops, how much reached the file is a race, so those
// cases write to stdout alone.
//
// `tee FILE >&-` is not here: an output file opened while fd 1 is
// closed takes descriptor 1 and aliases stdout (#8823).
func teeCases(t *testing.T) []invocation {
	big := strings.Repeat("line\n", 40000)
	// A directory to hand a case as its stdin. The per-case working
	// directory is made inside the run, after stdinPath is opened, so
	// this one is separate and absolute.
	aDir := t.TempDir()

	return []invocation{
		// The copy.
		{name: "no operands", stdin: "hello\nworld\n"},
		{name: "one file", args: []string{"a"}, stdin: "hello\nworld\n", dir: teeSeed},
		{name: "two files", args: []string{"a", "b"}, stdin: "hello\nworld\n", dir: teeSeed},
		{name: "three files", args: []string{"a", "b", "c"}, stdin: "x\n", dir: teeSeed},
		{name: "empty stdin", stdin: ""},
		{name: "empty stdin one file", args: []string{"a"}, stdin: "", dir: teeSeed},
		{name: "no trailing newline", args: []string{"a"}, stdin: "x", dir: teeSeed},
		{name: "binary stdin", args: []string{"a"}, stdin: "\x00\x01\xff\xfe\n\x00", dir: teeSeed},
		{name: "large stdin", args: []string{"a"}, stdin: big, dir: teeSeed},
		{name: "large stdin two files", args: []string{"a", "b"}, stdin: big, dir: teeSeed},
		{name: "large stdin no operands", stdin: big},
		{name: "same file twice", args: []string{"a", "a"}, stdin: "hello\n", dir: teeSeed},
		{name: "dash is a file called dash", args: []string{"-"}, stdin: "x\n", dir: teeSeed},
		{name: "truncates an existing file", args: []string{"exists"}, stdin: "x\n", dir: teeSeed},

		// -a.
		{name: "append", args: []string{"-a", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "append long", args: []string{"--append", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "append prefix", args: []string{"--ap", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "append creates a missing file", args: []string{"-a", "a"}, stdin: "x\n", dir: teeSeed},
		{name: "append two files", args: []string{"-a", "exists", "a"}, stdin: "x\n", dir: teeSeed},
		{name: "append the same file twice", args: []string{"-a", "exists", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "append a directory", args: []string{"-a", "d"}, stdin: "x\n", dir: teeSeed},

		// Operands that will not open. The later ones are still opened
		// and the copy still runs; the exit status is 1.
		{name: "directory", args: []string{"d"}, stdin: "hello\n", dir: teeSeed},
		{name: "directory then a file", args: []string{"d", "a"}, stdin: "hello\n", dir: teeSeed},
		{name: "file then a directory", args: []string{"a", "d"}, stdin: "hello\n", dir: teeSeed},
		{name: "two directories", args: []string{"d", "d2"}, stdin: "hello\n", dir: teeSeed},
		{name: "missing parent directory", args: []string{"nodir/f"}, stdin: "x\n", dir: teeSeed},
		{name: "empty operand", args: []string{""}, stdin: "x\n", dir: teeSeed},
		{name: "empty operand then a file", args: []string{"", "a"}, stdin: "x\n", dir: teeSeed},
		{name: "name with a space", args: []string{"f name"}, stdin: "x\n", dir: teeSeed},
		{name: "name with a quote", args: []string{"f'n"}, stdin: "x\n", dir: teeSeed},
		{name: "name that is not valid UTF-8", args: []string{"na\xffme"}, stdin: "x\n", dir: teeSeed},
		{name: "missing name with a space", args: []string{"no dir/f"}, stdin: "x\n", dir: teeSeed},
		{name: "missing name that is not valid UTF-8", args: []string{"no\xffdir/f"}, stdin: "x\n", dir: teeSeed},
		{name: "dashdash then an option-looking name", args: []string{"--", "-a"}, stdin: "x\n", dir: teeSeed},
		{name: "dashdash alone", args: []string{"--"}, stdin: "x\n", dir: teeSeed},

		// An open failure under a mode that exits: the operands after
		// it are never created, and nothing is copied at all.
		{name: "exit stops at a failing open", args: []string{"--output-error=exit", "d", "a"}, stdin: "x\n", dir: teeSeed},
		{name: "exit-nopipe stops at a failing open", args: []string{"--output-error=exit-nopipe", "d", "a"}, stdin: "x\n", dir: teeSeed},
		{name: "exit stops after opening the good ones", args: []string{"--output-error=exit", "a", "d", "b"}, stdin: "x\n", dir: teeSeed},
		{name: "warn keeps opening after a failure", args: []string{"--output-error=warn", "a", "d", "b"}, stdin: "x\n", dir: teeSeed},
		{name: "p keeps opening after a failure", args: []string{"-p", "d", "a"}, stdin: "x\n", dir: teeSeed},

		// Read failures.
		{name: "directory on stdin", args: []string{"a"}, stdinPath: aDir, dir: teeSeed},
		{name: "directory on stdin no operands", stdinPath: aDir},

		// Write failures that are not a broken pipe: diagnosed in every
		// mode, and every mode exits 1. A mode that exits does it before
		// the FILE is written, which the tree shows.
		{name: "stdout full", args: []string{"a"}, stdin: "hello\n", stdout: stdoutFull, dir: teeSeed},
		{name: "stdout full with p", args: []string{"-p", "a"}, stdin: "hello\n", stdout: stdoutFull, dir: teeSeed},
		{name: "stdout full with warn", args: []string{"--output-error=warn", "a"}, stdin: "hello\n", stdout: stdoutFull, dir: teeSeed},
		{name: "stdout full with warn-nopipe", args: []string{"--output-error=warn-nopipe", "a"}, stdin: "hello\n", stdout: stdoutFull, dir: teeSeed},
		{name: "stdout full with exit", args: []string{"--output-error=exit", "a"}, stdin: "hello\n", stdout: stdoutFull, dir: teeSeed},
		{name: "stdout full with exit-nopipe", args: []string{"--output-error=exit-nopipe", "a"}, stdin: "hello\n", stdout: stdoutFull, dir: teeSeed},
		{name: "stdout full no operands", stdin: "hello\n", stdout: stdoutFull},
		{name: "stdout full with nothing to write", stdin: "", stdout: stdoutFull},
		{name: "stdout closed", stdin: "hello\n", stdout: stdoutClosed},
		{name: "stdout closed with nothing to write", stdin: "", stdout: stdoutClosed},
		{name: "stdout closed with exit", args: []string{"--output-error=exit"}, stdin: "hello\n", stdout: stdoutClosed},
		{name: "full file operand", args: []string{"/dev/full"}, stdin: "hello\n"},
		{name: "full file operand with exit", args: []string{"--output-error=exit", "/dev/full"}, stdin: "hello\n"},
		{name: "full file operand with p", args: []string{"-p", "/dev/full"}, stdin: "hello\n"},

		// A broken stdout. Without a mode SIGPIPE keeps its default and
		// kills; each mode below turns it into an EPIPE it decides
		// about. The file operand appears only where the mode keeps
		// going, so what the file holds is not a race.
		{name: "pipe closed", stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with p", args: []string{"-p"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with warn", args: []string{"--output-error=warn"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with warn-nopipe", args: []string{"--output-error=warn-nopipe"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with exit", args: []string{"--output-error=exit"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with exit-nopipe", args: []string{"--output-error=exit-nopipe"}, stdin: teePipeStdin, limit: 4096},
		// Bare `--output-error` is warn-nopipe, not warn: the long name
		// carries an OPTIONAL value and shares its id with -p, so with
		// no value it is the same request.
		{name: "pipe closed with a bare output-error", args: []string{"--output-error"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with a prefixed mode", args: []string{"--output-error=warn-"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with exit- prefix", args: []string{"--output-error=exit-"}, stdin: teePipeStdin, limit: 4096},
		{name: "pipe closed with p and a file", args: []string{"-p", "a"}, stdin: teePipeStdin, limit: 4096, dir: teeSeed},
		{name: "pipe closed with warn and a file", args: []string{"--output-error=warn", "a"}, stdin: teePipeStdin, limit: 4096, dir: teeSeed},
		{name: "pipe closed with warn-nopipe and a file", args: []string{"--output-error=warn-nopipe", "a"}, stdin: teePipeStdin, limit: 4096, dir: teeSeed},
		{name: "pipe closed with exit-nopipe and a file", args: []string{"--output-error=exit-nopipe", "a"}, stdin: teePipeStdin, limit: 4096, dir: teeSeed},

		// -i. The signal arrives while the child is blocked on the full
		// pipe; the read end closes after it, so a tee that ignored
		// SIGINT meets SIGPIPE next and a tee that did not is already
		// gone.
		{name: "sigint", stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i", args: []string{"-i"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with ignore-interrupts", args: []string{"--ignore-interrupts"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with the ignore prefix", args: []string{"--ig"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and p", args: []string{"-i", "-p"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with the ip cluster", args: []string{"-ip"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and warn", args: []string{"-i", "--output-error=warn"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and exit", args: []string{"-i", "--output-error=exit"}, stdin: teePipeStdin, limit: 4096, sigint: true},
		{name: "sigint with i and exit-nopipe", args: []string{"-i", "--output-error=exit-nopipe"}, stdin: teePipeStdin, limit: 4096, sigint: true},

		// --output-error's argmatch.
		{name: "output-error invalid", args: []string{"--output-error=bogus"}},
		{name: "output-error empty is ambiguous", args: []string{"--output-error="}},
		{name: "output-error w is ambiguous", args: []string{"--output-error=w"}},
		{name: "output-error e is ambiguous", args: []string{"--output-error=e"}},
		{name: "output-error wa is unique", args: []string{"--output-error=wa"}, stdin: "x\n"},
		{name: "output-error ex is unique", args: []string{"--output-error=ex"}, stdin: "x\n"},
		{name: "output-error exact warn beats its extension", args: []string{"--output-error=warn"}, stdin: "x\n"},
		{name: "output-error exact exit beats its extension", args: []string{"--output-error=exit"}, stdin: "x\n"},
		{name: "output-error uppercase", args: []string{"--output-error=WARN"}},
		{name: "output-error takes no separate argument", args: []string{"--output-error", "warn"}, stdin: "x\n", dir: teeSeed},
		{name: "output-error twice", args: []string{"--output-error=exit", "--output-error=warn"}, stdin: "x\n"},

		// getopt.
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
		{name: "a is unique", args: []string{"--a", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "i is unique", args: []string{"--i"}, stdin: "x\n"},
		{name: "o is unique", args: []string{"--o"}, stdin: "x\n"},
		{name: "cluster ai", args: []string{"-ai", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "cluster ia", args: []string{"-ia", "exists"}, stdin: "x\n", dir: teeSeed},
		{name: "operand before an option is permuted out", args: []string{"a", "-a"}, stdin: "x\n", dir: teeSeed},
		{name: "posix stops at the first operand", args: []string{"a", "-a"}, stdin: "x\n", env: []string{"POSIXLY_CORRECT=1"}, dir: teeSeed},
		{name: "posix option before the operand", args: []string{"-a", "exists"}, stdin: "x\n", env: []string{"POSIXLY_CORRECT=1"}, dir: teeSeed},
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
