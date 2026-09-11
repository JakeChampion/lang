package coreutils

import "testing"

func init() {
	registerCorpus("tty", ttyCases)
}

// tty(1) is ttyname(0) and one write, and the interesting half is the exit
// status: 0 a terminal, 1 not one, 2 a usage error, 3 a failed write. The last
// two are different gnulib globals — `tty -x` exits 2 through usage() while
// `tty --help > /dev/full` exits 3 through exit_failure — so both appear below.
//
// The positive answer is reachable without a pseudo-terminal harness: opening
// /dev/ptmx allocates a pty MASTER, which isatty() answers true for, and
// ttyname(0) then reads /proc/self/fd/0 and says `/dev/ptmx`. That case is also
// what pins the /proc route as the FIRST one: /dev/pts/ptmx has the same device
// number, so a walk of /dev/pts alone answers the wrong name for it.
//
// `--quiet` is here as an option that works and is invisible in the ambiguity
// list: glibc names only the prefix candidates that differ from the first
// match, and `--quiet` is a second spelling of `--silent`.
func ttyCases(t *testing.T) []invocation {
	return []invocation{
		// Standard input is a pipe under the harness, so the default answer
		// is `not a tty` and exit 1.
		{name: "no arguments"},
		{name: "silent short", args: []string{"-s"}},
		{name: "silent long", args: []string{"--silent"}},
		{name: "quiet long", args: []string{"--quiet"}},
		{name: "silent as a unique prefix", args: []string{"--si"}},
		{name: "quiet as a unique prefix", args: []string{"--q"}},
		{name: "a one-letter prefix of silent", args: []string{"--s"}},
		{name: "the short option twice", args: []string{"-s", "-s"}},
		{name: "the short option clustered with itself", args: []string{"-ss"}},
		{name: "both spellings together", args: []string{"--si", "--qu"}},
		{name: "dashdash alone still answers"},

		// A terminal on standard input. /dev/ptmx is the one device that is a
		// terminal without a pty harness, and naming it is what separates the
		// /proc route from the /dev walk.
		{name: "a pseudo-terminal master", stdinPath: "/dev/ptmx"},
		{name: "a pseudo-terminal master under -s", args: []string{"-s"}, stdinPath: "/dev/ptmx"},

		// The other ways stdin is not a terminal: each is `not a tty`, and the
		// errno behind it never reaches the output.
		{name: "a character device that is not a terminal", stdinPath: "/dev/null"},
		{name: "a regular file", stdinPath: "/etc/hostname"},
		{name: "a directory", stdinPath: "/"},

		// Operands: tty takes none, and every shape of one is `extra operand`
		// with exit 2.
		{name: "an operand", args: []string{"foo"}},
		{name: "two operands report the first", args: []string{"foo", "bar"}},
		{name: "an operand under -s", args: []string{"-s", "foo"}},
		{name: "the empty operand", args: []string{""}},
		{name: "a lone dash is an operand", args: []string{"-"}},
		{name: "an operand after dashdash", args: []string{"--", "foo"}},
		{name: "an option-looking operand after dashdash", args: []string{"--", "-s"}},
		{name: "an operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "an operand with a newline", args: []string{"a\nb"}},

		// getopt faults, all exit 2.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option after a good one", args: []string{"-sx"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "the empty long option lists only the distinct spellings", args: []string{"--=x"}},
		{name: "a flag refuses a value", args: []string{"--silent=x"}},
		{name: "quiet refuses a value", args: []string{"--quiet=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "an option after an operand is permuted out", args: []string{"foo", "-s"}},
		{name: "a bad option after an operand", args: []string{"foo", "--badopt"}},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{"foo", "--badopt"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "POSIXLY_CORRECT before a good option",
			args: []string{"foo", "-s"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths, which are exit 3 and not 1. -s writes
		// nothing, so a closed stdout is no error there at all — and --help
		// and --version print text that is ours by design but reach the same
		// exit_failure, so a FAILED write of either compares byte for byte.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on a terminal", stdinPath: "/dev/ptmx", stdout: stdoutClosed},
		{name: "stdout full on a terminal", stdinPath: "/dev/ptmx", stdout: stdoutFull},
		{name: "stdout closed under -s", args: []string{"-s"}, stdout: stdoutClosed},
		{name: "stdout full under -s", args: []string{"-s"}, stdout: stdoutFull},
		{name: "stdout closed on a usage error", args: []string{"foo"}, stdout: stdoutClosed},
		{name: "stdout closed under help", args: []string{"--help"}, stdout: stdoutClosed},
		{name: "stdout full under help", args: []string{"--help"}, stdout: stdoutFull},
		{name: "stdout closed under version", args: []string{"--version"}, stdout: stdoutClosed},
		{name: "stdout full under version", args: []string{"--version"}, stdout: stdoutFull},
	}
}

func TestTtyParity(t *testing.T) {
	requireParity(t, "tty", ttyCases(t))
}

func TestTtyHelpVersion(t *testing.T) {
	requireHelp(t, "tty", []string{"--help"}, 0)
	requireHelp(t, "tty", []string{"--hel"}, 0)
	requireHelp(t, "tty", []string{"--help", "x"}, 0)
	requireHelp(t, "tty", []string{"x", "--help"}, 0)
	requireVersion(t, "tty", []string{"--version"}, 0)
	requireVersion(t, "tty", []string{"--vers"}, 0)
	requireVersion(t, "tty", []string{"--version", "x"}, 0)
}
