package coreutils

import (
	"os/user"
	"testing"
)

func init() {
	registerCorpus("groups", groupsCases)
}

func groupsCases(t *testing.T) []invocation {
	self, other := idUserNames(t)
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone is the process form", args: []string{"--"}},

		{name: "own name", args: []string{self}},
		{name: "another name", args: []string{other}},
		{name: "the same name twice", args: []string{other, other}},
		{name: "two names", args: []string{other, self}},
		{name: "no such user", args: []string{"nosuchuser"}},
		{name: "good then bad", args: []string{other, "nosuchuser"}},
		{name: "bad then good", args: []string{"nosuchuser", other}},
		{name: "bad between two good", args: []string{other, "nosuchuser", self}},
		{name: "numeric operand is a name, not a uid", args: []string{"0"}},
		{name: "a uid no entry claims", args: []string{"99999"}},
		{name: "empty operand", args: []string{""}},
		{name: "blank operand", args: []string{" "}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "operand with a colon", args: []string{"a:b"}},
		{name: "operand after dashdash", args: []string{"--", other}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "short option that id has", args: []string{"-n"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{other, "--foo"}},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{other, "--help"}, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed with an operand", args: []string{other}, stdout: stdoutClosed},
		{name: "stdout full with a bad operand", args: []string{"nosuchuser"}, stdout: stdoutFull},
	}
}

func TestGroupsParity(t *testing.T) {
	if _, err := user.Current(); err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	requireParity(t, "groups", groupsCases(t))
}

func TestGroupsHelpVersion(t *testing.T) {
	requireHelp(t, "groups", []string{"--help"}, 0)
	requireHelp(t, "groups", []string{"--hel"}, 0)
	requireHelp(t, "groups", []string{"--help", "x"}, 0)
	requireHelp(t, "groups", []string{"x", "--help"}, 0)
	requireVersion(t, "groups", []string{"--version"}, 0)
	requireVersion(t, "groups", []string{"--vers"}, 0)
	requireVersion(t, "groups", []string{"--version", "x"}, 0)
}
