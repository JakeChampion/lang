package coreutils

import "testing"

// printenv(1) — the whole environment in the vector's own order, or the
// value of each named variable.
//
// The harness runs both sides with the same fixed environment plus
// whatever a case adds, so the dump is byte-comparable: the ORDER is
// the one execve was handed, which is what makes `environ()` and not a
// loop of lookups the right primitive.
//
// Three rules the cases are here for:
//
//   - Options are scanned in argv order, so `printenv A -0` asks for a
//     variable named `-0` rather than turning on NUL termination.
//   - An operand that names nothing costs the exit status and nothing
//     else; the ones that do name something are still printed, in order,
//     and a repeated name is printed twice.
//   - printenv's FAILURE status is 2, not 1 — a usage error and a failed
//     write both exit 2, while a missing variable exits 1.
func printenvCases(t *testing.T) []invocation {
	// A duplicate name is legal in an environment vector and the kernel
	// preserves it; only the dump can see the second one.
	dup := []string{"FERN_DUP=first", "FERN_DUP=second"}
	one := []string{"FERN_A=1", "FERN_B=", "FERN_EQ=a=b"}
	return []invocation{
		{name: "whole environment", env: one},
		{name: "whole environment with a duplicate name", env: dup},
		{name: "whole environment NUL terminated", args: []string{"-0"}, env: one},
		{name: "whole environment long null", args: []string{"--null"}, env: one},
		{name: "whole environment long prefix", args: []string{"--nu"}, env: one},
		{name: "whole environment after dashdash", args: []string{"--"}, env: one},

		// Named variables.
		{name: "one name", args: []string{"FERN_A"}, env: one},
		{name: "an empty value", args: []string{"FERN_B"}, env: one},
		{name: "a value containing an equals sign", args: []string{"FERN_EQ"}, env: one},
		{name: "a name containing an equals sign", args: []string{"FERN_EQ=a"}, env: one},
		{name: "two names", args: []string{"FERN_A", "FERN_B"}, env: one},
		{name: "the same name twice", args: []string{"FERN_A", "FERN_A"}, env: one},
		{name: "a duplicate name resolves to the first", args: []string{"FERN_DUP"}, env: dup},
		{name: "a name that is not set", args: []string{"FERN_NOPE"}, env: one},
		{name: "set then unset", args: []string{"FERN_A", "FERN_NOPE"}, env: one},
		{name: "unset then set", args: []string{"FERN_NOPE", "FERN_A"}, env: one},
		{name: "unset between two set", args: []string{"FERN_A", "FERN_NOPE", "FERN_B"}, env: one},
		{name: "empty name", args: []string{""}, env: one},
		{name: "lone dash is a name", args: []string{"-"}, env: one},
		{name: "name that is not valid UTF-8", args: []string{"\xff\xfe"}, env: one},
		{name: "name after dashdash", args: []string{"--", "FERN_A"}, env: one},
		{name: "option-looking name after dashdash", args: []string{"--", "--null"}, env: one},
		{name: "names NUL terminated", args: []string{"-0", "FERN_A", "FERN_B"}, env: one},
		{name: "unset name NUL terminated", args: []string{"-0", "FERN_NOPE"}, env: one},

		// The `+` optstring: an option after the first operand is an
		// operand, whatever it looks like.
		{name: "option after an operand is an operand", args: []string{"FERN_A", "-0"}, env: one},
		{name: "long option after an operand is an operand", args: []string{"FERN_A", "--null"}, env: one},
		{name: "option twice", args: []string{"-0", "-0", "FERN_A"}, env: one},
		{name: "clustered with itself", args: []string{"-00", "FERN_A"}, env: one},

		// getopt faults. printenv exits 2 for these.
		{name: "invalid short option", args: []string{"-x"}, env: one},
		{name: "invalid short option cluster", args: []string{"-0x"}, env: one},
		{name: "digit option that is not zero", args: []string{"-1"}, env: one},
		{name: "unrecognized long option", args: []string{"--foo"}, env: one},
		{name: "long option with a value it does not take", args: []string{"--null=x"}, env: one},
		{name: "empty long option is ambiguous", args: []string{"--=x"}, env: one},
		{name: "help with a value", args: []string{"--help=x"}, env: one},
		{name: "version with a value", args: []string{"--version=1"}, env: one},
		{name: "bad option before help", args: []string{"--foo", "--help"}, env: one},
		{name: "POSIXLY_CORRECT changes nothing for an in-order scan",
			args: []string{"FERN_A", "-0"}, env: append(one, "POSIXLY_CORRECT=1")},

		// The write-failure paths, which exit 2 here rather than 1.
		{name: "stdout closed", stdout: stdoutClosed, env: one},
		{name: "stdout full", stdout: stdoutFull, env: one},
		{name: "stdout closed with a name", args: []string{"FERN_A"}, stdout: stdoutClosed, env: one},
		{name: "stdout full with a name", args: []string{"FERN_A"}, stdout: stdoutFull, env: one},
		{name: "stdout closed on a fault", args: []string{"-x"}, stdout: stdoutClosed, env: one},
		// --help and --version are text-exempt, but a write that FAILS
		// while printing either produces no text at all — so these two
		// are byte-comparable, and they are what pins the 2.
		{name: "stdout closed on help", args: []string{"--help"}, stdout: stdoutClosed, env: one},
		{name: "stdout closed on version", args: []string{"--version"}, stdout: stdoutClosed, env: one},
		{name: "stdout full on help", args: []string{"--help"}, stdout: stdoutFull, env: one},
	}
}

func TestPrintenvParity(t *testing.T) {
	requireParity(t, "printenv", printenvCases(t))
}

// printenv's `--help` and `--version` exit 0, but a WRITE that fails
// while printing either exits 2 — gnulib's exit_failure, which this
// utility sets rather than leaving at 1.
func TestPrintenvHelpVersion(t *testing.T) {
	requireHelp(t, "printenv", []string{"--help"}, 0)
	requireHelp(t, "printenv", []string{"--hel"}, 0)
	requireHelp(t, "printenv", []string{"--help", "x"}, 0)
	requireVersion(t, "printenv", []string{"--version"}, 0)
	requireVersion(t, "printenv", []string{"--vers"}, 0)
	requireVersion(t, "printenv", []string{"--version", "x"}, 0)
}
