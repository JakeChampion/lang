package coreutils

import (
	"strings"
	"testing"
)

func init() {
	registerCorpus("yes", yesCases)
}

func yesCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments", limit: 64},
		{name: "one operand", args: []string{"hello"}, limit: 64},
		{name: "two operands", args: []string{"a", "b"}, limit: 64},
		{name: "empty operand", args: []string{""}, limit: 64},
		{name: "lone dash is an operand", args: []string{"-"}, limit: 64},
		{name: "operand with a newline in it", args: []string{"a\nb"}, limit: 64},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}, limit: 64},
		{name: "dashdash alone", args: []string{"--"}, limit: 64},
		{name: "dashdash then an option", args: []string{"--", "--help"}, limit: 64},
		{name: "dashdash twice", args: []string{"--", "--", "x"}, limit: 64},
		{name: "operand then a bad option", args: []string{"x", "-z"}},
		{name: "operand then dashdash then an option", args: []string{"x", "--", "-z"}, limit: 64},
		{name: "operands either side of dashdash", args: []string{"a", "--", "b"}, limit: 64},
		{name: "operand longer than the write block", args: []string{strings.Repeat("z", 70000)}, limit: 4096},

		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed with an operand", args: []string{"hello"}, stdout: stdoutClosed},

		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option in a cluster", args: []string{"-xy"}},
		{name: "empty long option name", args: []string{"--=x"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "bad short option before help", args: []string{"x", "-z", "--help"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=x"}},
		{name: "posix operand then help", args: []string{"x", "--help"}, env: []string{"POSIXLY_CORRECT=1"}, limit: 64},
		{name: "posix option before the operand", args: []string{"--foo", "x"}, env: []string{"POSIXLY_CORRECT=1"}},
	}
}

func TestYesParity(t *testing.T) {
	requireParity(t, "yes", yesCases(t))
}

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
