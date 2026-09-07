package coreutils

import "testing"

// logname(1) — getlogin(3), which is the name the SESSION was logged in
// under and not the effective user: after `su` it still says who logged
// in, and where there is no login it says so rather than falling back to
// the euid's name.
//
// Under the harness there is no login: fd 0 is a pipe, so there is no
// controlling terminal to name, and the audit loginuid of a test process
// is the unset sentinel. Both sides therefore take the same "no login
// name" path — which is the answer the corpus is comparing, and the
// reason the exit status is in it: this is the one utility whose normal
// case on a build machine is a failure.
func lognameCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone", args: []string{"--"}},

		// Operands, all of them faults: logname takes none.
		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "a user name is still an operand", args: []string{"root"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

		// getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{"x", "--foo"}},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{"x", "--help"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths. The diagnostic path writes nothing to
		// stdout, so a closed one is only visible on the success side.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{"x"}, stdout: stdoutClosed},
	}
}

func TestLognameParity(t *testing.T) {
	requireParity(t, "logname", lognameCases(t))
}

func TestLognameHelpVersion(t *testing.T) {
	requireHelp(t, "logname", []string{"--help"}, 0)
	requireHelp(t, "logname", []string{"--hel"}, 0)
	requireHelp(t, "logname", []string{"--help", "x"}, 0)
	requireHelp(t, "logname", []string{"x", "--help"}, 0)
	requireVersion(t, "logname", []string{"--version"}, 0)
	requireVersion(t, "logname", []string{"--vers"}, 0)
	requireVersion(t, "logname", []string{"--version", "x"}, 0)
}
