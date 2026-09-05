package coreutils

import (
	"strings"
	"testing"
)

// yes(1) never stops, so every output case bounds the read. The bound
// doubles as the SIGPIPE check: the harness closes the read end after
// the prefix, and both sides have to die of it.
func TestYesParity(t *testing.T) {
	requireParity(t, "yes", []invocation{
		{name: "no arguments", limit: 64},
		{name: "one operand", args: []string{"hello"}, limit: 64},
		{name: "two operands", args: []string{"a", "b"}, limit: 64},
		{name: "empty operand", args: []string{""}, limit: 64},
		{name: "lone dash is an operand", args: []string{"-"}, limit: 64},
		{name: "operand with a newline in it", args: []string{"a\nb"}, limit: 64},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}, limit: 64},
		// `--` ends the options, so an option-looking token after it is
		// an ordinary operand.
		{name: "dashdash alone", args: []string{"--"}, limit: 64},
		{name: "dashdash then an option", args: []string{"--", "--help"}, limit: 64},
		{name: "dashdash twice", args: []string{"--", "--", "x"}, limit: 64},
		// The scan permutes: an option after an operand is still an
		// option, and only `--` protects what follows it.
		{name: "operand then a bad option", args: []string{"x", "-z"}},
		{name: "operand then dashdash then an option", args: []string{"x", "--", "-z"}, limit: 64},
		{name: "operands either side of dashdash", args: []string{"a", "--", "b"}, limit: 64},
		// One operand longer than the write block, so the block holds a
		// single copy of the line rather than many.
		{name: "operand longer than the write block", args: []string{strings.Repeat("z", 70000)}, limit: 4096},

		// Write failures: `yes: standard output: <strerror>`, exit 1. The
		// text is the runtime's IoError.Other message (#8265).
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed with an operand", args: []string{"hello"}, stdout: stdoutClosed},

		// Usage errors: the message, the `Try …` line, exit 1.
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option in a cluster", args: []string{"-xy"}},
		{name: "empty long option name", args: []string{"--=x"}},
		// Left to right: the first fault wins over a later --help, and a
		// later fault loses to an earlier --help (TestYesHelpVersion).
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "bad short option before help", args: []string{"x", "-z", "--help"}},
		// `--help` and `--version` take no argument, so a glued value
		// makes them unrecognized rather than a help request.
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=x"}},
		// POSIXLY_CORRECT ends the options at the first operand.
		{name: "posix operand then help", args: []string{"x", "--help"}, env: []string{"POSIXLY_CORRECT=1"}, limit: 64},
		{name: "posix option before the operand", args: []string{"--foo", "x"}, env: []string{"POSIXLY_CORRECT=1"}},
	})
}

// The unique-prefix rule and the option scan that reaches past an
// earlier option, checked on the two outputs whose text is ours.
func TestYesHelpVersion(t *testing.T) {
	requireHelp(t, "yes", []string{"--help"}, 0)
	requireHelp(t, "yes", []string{"--h"}, 0)
	requireHelp(t, "yes", []string{"--help", "ignored"}, 0)
	requireHelp(t, "yes", []string{"operand", "--help"}, 0)
	requireHelp(t, "yes", []string{"--help", "--foo"}, 0)
	requireVersion(t, "yes", []string{"--version"}, 0)
	requireVersion(t, "yes", []string{"--vers"}, 0)
	requireVersion(t, "yes", []string{"--version", "ignored"}, 0)
	requireVersion(t, "yes", []string{"operand", "--version"}, 0)
}
