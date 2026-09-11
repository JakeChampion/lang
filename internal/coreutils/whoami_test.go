package coreutils

import "testing"

func init() {
	registerCorpus("whoami", whoamiCases)
}

// whoami(1) is `id -un` with no options of its own: the effective uid's
// name, and every operand — including a lone `-`, which most utilities
// read as stdin — is `extra operand`.
//
// The name itself is machine-dependent, which is exactly why the harness
// runs both sides here and now rather than pinning a string: the two
// processes are siblings with the same credentials, so a divergence is
// the implementation and never the machine.
//
// That sibling-credentials property is also what puts one quirk out of
// reach: `cannot find name for user ID N` needs a uid with no passwd
// entry, and the harness runs as its own uid, which has one. Redirecting
// the lookup is not available either — pwdb.passwd_path is the literal
// `/etc/passwd` with no override — so the message is covered by the
// implementation and by nothing here.
func whoamiCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone still prints the name", args: []string{"--"}},

		// Operands, all of them faults.
		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},
		// A real user name is still an operand: whoami takes none.
		{name: "a user name is an operand", args: []string{"root"}},

		// getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "short option that id has", args: []string{"-u"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{"x", "--foo"}},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{"x", "--help"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths: one write, one strerror.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{"x"}, stdout: stdoutClosed},
	}
}

func TestWhoamiParity(t *testing.T) {
	requireParity(t, "whoami", whoamiCases(t))
}

func TestWhoamiHelpVersion(t *testing.T) {
	requireHelp(t, "whoami", []string{"--help"}, 0)
	requireHelp(t, "whoami", []string{"--hel"}, 0)
	requireHelp(t, "whoami", []string{"--help", "x"}, 0)
	requireHelp(t, "whoami", []string{"x", "--help"}, 0)
	requireVersion(t, "whoami", []string{"--version"}, 0)
	requireVersion(t, "whoami", []string{"--vers"}, 0)
	requireVersion(t, "whoami", []string{"--version", "x"}, 0)
}
