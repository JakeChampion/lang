package coreutils

import "testing"

func init() {
	registerCorpus("hostid", hostidCases)
}

func hostidCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone still prints the id", args: []string{"--"}},

		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{"x", "--foo"}},

		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{"x"}, stdout: stdoutClosed},
	}
}

func TestHostidParity(t *testing.T) {
	requireParity(t, "hostid", hostidCases(t))
}

func TestHostidHelpVersion(t *testing.T) {
	requireHelp(t, "hostid", []string{"--help"}, 0)
	requireHelp(t, "hostid", []string{"--hel"}, 0)
	requireHelp(t, "hostid", []string{"--help", "x"}, 0)
	requireHelp(t, "hostid", []string{"x", "--help"}, 0)
	requireVersion(t, "hostid", []string{"--version"}, 0)
	requireVersion(t, "hostid", []string{"--vers"}, 0)
	requireVersion(t, "hostid", []string{"--version", "x"}, 0)
}
