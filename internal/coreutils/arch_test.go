package coreutils

import "testing"

func init() {
	registerCorpus("arch", archCases)
}

// arch(1) is `uname -m` with a different option table: coreutils builds
// it from the same source with the mode switched, so it declares only
// --help and --version, takes no operands, and every operand is
// `extra operand`. The scan permutes, so an option is found wherever it
// stands until `--`.
func archCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone still prints the machine", args: []string{"--"}},

		// Operands, all of them faults.
		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

		// uname's options are NOT arch's.
		{name: "-m is not an option here", args: []string{"-m"}},
		{name: "-a is not an option here", args: []string{"-a"}},
		{name: "--machine is not an option here", args: []string{"--machine"}},
		{name: "--all is not an option here", args: []string{"--all"}},

		// getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{"x", "--foo"}},

		// The write-failure paths: one write, one strerror.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{"x"}, stdout: stdoutClosed},
	}
}

func TestArchParity(t *testing.T) {
	requireParity(t, "arch", archCases(t))
}

func TestArchHelpVersion(t *testing.T) {
	requireHelp(t, "arch", []string{"--help"}, 0)
	requireHelp(t, "arch", []string{"--hel"}, 0)
	requireHelp(t, "arch", []string{"--help", "x"}, 0)
	requireHelp(t, "arch", []string{"x", "--help"}, 0)
	requireVersion(t, "arch", []string{"--version"}, 0)
	requireVersion(t, "arch", []string{"--vers"}, 0)
	requireVersion(t, "arch", []string{"--version", "x"}, 0)
}
