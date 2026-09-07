package coreutils

import (
	"os/user"
	"testing"
)

// groups(1) — the process's own group names, or one line per named
// user.
//
// The two forms ask different questions and the corpus keeps them
// apart: with no operand the set is the kernel's (getgroups(2) with the
// effective gid at the front), and with an operand it is the group
// database's, which is why a user added to a group since login shows in
// one and not the other.
//
// The operand is looked up by NAME only — `groups 0` is
// `'0': no such user` where `id 0` is root — and a failed operand does
// not end the run: the diagnostic goes out, the status turns 1, and the
// operands after it are still printed.
func groupsCases(t *testing.T) []invocation {
	self, other := idUserNames(t)
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone is the process form", args: []string{"--"}},

		// Operands.
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

		// getopt faults. groups declares no options of its own, so
		// every one of these is a fault.
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

		// The write-failure paths.
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
