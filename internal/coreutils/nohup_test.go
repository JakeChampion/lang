package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// nohup(1) is three conditional redirections and an exec, and WHICH
// descriptors are terminals decides all of it. That is why this corpus needs
// `ttyIn` / `ttyOut` / `ttyErr`: with fds 0-2 pipes, seven of the eight rows
// below collapse into the one that does nothing.
//
// The rule behind the message, rather than eight strings:
//
//   - stdin a terminal: reopened from /dev/haven'tread — the clause is
//     `ignoring input`, and what lands on fd 0 is /dev/null opened
//     WRITE-only, so a command that reads it gets EBADF rather than
//     end-of-input. That is the `cat` case below.
//   - stdout a terminal: appended to nohup.out, created 0600 — the clause is
//     `appending output to 'nohup.out'`.
//   - stderr a terminal AND stdout not: the clause is `redirecting stderr to
//     stdout`. When stdout IS a terminal there is no separate stderr clause;
//     stderr follows stdout into nohup.out and only the append is announced.
//   - the clauses join with a literal ` and `, and the line goes to the
//     ORIGINAL stderr, before fd 2 is replaced.
//
// The wording is 9.4's. Coreutils 9.10 spells the stderr clause
// `redirecting standard error to standard output`; the corpus compares
// against the installed binary, so 9.4's shorter form is what the
// implementation emits — docs/COREUTILS.md carries the difference.
//
// `seedTree` on every terminal-stdout case, not `artifacts`: nohup.out's
// twelve-bit MODE is half of what those cases prove, and a fresh directory
// per side is also what stops the GNU run's output being appended to by the
// Fern one.

func init() {
	registerCorpus("nohup", nohupCases)
}

// nohupProbe writes to both streams, so a case can see which of them was
// redirected and which was not. Run through `sh` because the streams have to
// be written in a known order by something both sides invoke identically.
func nohupProbe() []string {
	return []string{"sh", "-c", "echo body; echo oops >&2"}
}

// emptyTree is the seed for a case that only writes nohup.out: the working
// directory starts empty and everything in it afterwards is compared.
func emptyTree(t *testing.T, dir string) {}

// blockedTree puts a DIRECTORY where nohup.out would go, which is how the
// open fails for root as well — the runner here is root, so a read-only
// directory would not do it. EISDIR is what both sides then report.
func blockedTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "nohup.out"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// blockedWithHome is the same, plus a writable directory for $HOME to name
// so the fallback can succeed. Relative, so it stays inside the per-side
// tree and joins the comparison.
func blockedWithHome(t *testing.T, dir string) {
	t.Helper()
	blockedTree(t, dir)
	if err := os.Mkdir(filepath.Join(dir, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func nohupCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- the eight combinations ----
	for _, c := range []struct {
		name         string
		in, out, err bool
	}{
		{"tty none", false, false, false},
		{"tty err", false, false, true},
		{"tty out", false, true, false},
		{"tty out+err", false, true, true},
		{"tty in", true, false, false},
		{"tty in+err", true, false, true},
		{"tty in+out", true, true, false},
		{"tty all three", true, true, true},
	} {
		add(invocation{
			name: c.name, args: nohupProbe(),
			ttyIn: c.in, ttyOut: c.out, ttyErr: c.err,
			seedTree: emptyTree,
		})
	}

	// ---- what lands on fd 0 ----
	// /dev/null opened WRITE-only, so `cat` fails with EBADF and a write to
	// fd 0 succeeds. A redirect from a readable /dev/null would make the
	// first succeed with no output and the second fail, so this case
	// separates the two.
	add(invocation{
		name:     "stdin is write-only",
		args:     []string{"sh", "-c", "cat; echo cat=$?; echo hi >&0; echo wrote=$?"},
		ttyIn:    true,
		seedTree: emptyTree,
	})

	// ---- nohup.out, and the $HOME fallback ----
	// An existing file is APPENDED to and keeps its own mode: 0600 is the
	// creation mode, not something nohup enforces on reuse.
	add(invocation{
		name: "appends to an existing nohup.out", args: nohupProbe(), ttyOut: true,
		seedTree: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "nohup.out"), []byte("PRIOR\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	})
	// The fallback's message names the FULL path where the working
	// directory's names the bare `nohup.out`, and the first failure is not
	// reported at all when the fallback saves it.
	add(invocation{
		name: "falls back to $HOME", args: nohupProbe(), ttyOut: true,
		env: []string{"HOME=home"}, seedTree: blockedWithHome,
	})
	// Both unopenable: two lines, in order, and 125.
	add(invocation{
		name: "both unopenable", args: nohupProbe(), ttyOut: true,
		env: []string{"HOME=."}, seedTree: blockedTree,
	})
	// An EMPTY $HOME is not an unset one: the fallback path is then the bare
	// `nohup.out` — gnulib's file_name_concat rule — so the second attempt
	// is the same file and both failures name it.
	add(invocation{
		name: "empty $HOME names the same file", args: nohupProbe(), ttyOut: true,
		env: []string{"HOME="}, seedTree: blockedTree,
	})
	// Unset, which is the harness's default: one line only, because there is
	// no second path to try.
	add(invocation{
		name: "unset $HOME has no fallback", args: nohupProbe(), ttyOut: true,
		seedTree: blockedTree,
	})

	// ---- the option scan ----
	add(invocation{name: "no operand"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "unknown short", args: []string{"-z", "echo", "hi"}})
	add(invocation{name: "unknown long", args: []string{"--bogus", "echo", "hi"}})
	add(invocation{name: "help with an argument", args: []string{"--help=x"}})
	add(invocation{name: "version with an argument", args: []string{"--version=x"}})
	// Options are honoured only BEFORE the command, so these reach echo.
	add(invocation{name: "the command's own --help", args: []string{"echo", "--help"}})
	add(invocation{name: "the command's own -n", args: []string{"echo", "-n", "hi"}})

	// ---- the three statuses ----
	add(invocation{name: "dash as the command", args: []string{"-"}})
	add(invocation{name: "an empty operand", args: []string{""}})
	add(invocation{name: "not on PATH", args: []string{"definitely-not-a-command"}})
	add(invocation{name: "PATH is searched", args: []string{"echo", "hello"}})
	add(invocation{name: "the command's status passes through", args: []string{"sh", "-c", "exit 7"}})
	add(invocation{
		name: "no execute bit", args: []string{"./x"},
		seedTree: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "x"), []byte("#!/bin/sh\necho ran\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	})
	add(invocation{
		name: "a directory", args: []string{"./dir"},
		seedTree: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	})

	return cases
}

func TestNohup(t *testing.T) {
	requireParity(t, "nohup", nohupCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md).
// Everything else about them still has to match, including that they
// abbreviate all the way down to `--h` and `--v` — which they can only do
// because nohup has no other long option for those to be a prefix of — and
// that they are honoured with operands after them.
func TestNohupHelpVersion(t *testing.T) {
	requireHelp(t, "nohup", []string{"--help"}, 0)
	requireHelp(t, "nohup", []string{"--hel"}, 0)
	requireHelp(t, "nohup", []string{"--h"}, 0)
	requireHelp(t, "nohup", []string{"--help", "extra"}, 0)
	requireVersion(t, "nohup", []string{"--version"}, 0)
	requireVersion(t, "nohup", []string{"--ver"}, 0)
	requireVersion(t, "nohup", []string{"--v"}, 0)
	requireVersion(t, "nohup", []string{"--version", "extra"}, 0)
}
