package coreutils

import "testing"

// hostid(1) has no options and takes no operand: the id is glibc's
// gethostid — /etc/hostid if it holds four bytes, else the hostname's
// IPv4 address with its halves swapped, else 0 — printed as eight hex
// digits. Every operand is `extra operand`, and the scan is getopt_long
// with only --help / --version declared, so the standard options are
// found anywhere and `--=x` lists both as ambiguous.
//
// Both sides read the same /etc/hostid, /etc/nsswitch.conf, /etc/hosts and
// /etc/resolv.conf, so the number itself is compared too.
func hostidCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone still prints the id", args: []string{"--"}},

		// Operands, all of them faults.
		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

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
