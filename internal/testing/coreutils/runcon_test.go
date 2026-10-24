package coreutils

import "testing"

func init() {
	registerCorpus("runcon", runconCases)
}

func runconCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone"},
		{name: "compute alone", args: []string{"-c"}},
		{name: "compute twice", args: []string{"-c", "-c"}},
		{name: "compute long", args: []string{"--compute"}},
		{name: "a type with no command", args: []string{"-t", "x"}},
		{name: "a glued type with no command", args: []string{"-tx"}},
		{name: "a type then dashdash", args: []string{"-t", "x", "--"}},
		{name: "all four valued options", args: []string{"-r", "a", "-t", "b", "-u", "c", "-l", "d"}},
		{name: "all four long spellings", args: []string{"--role=a", "--type=b", "--user=c", "--range=d"}},
		{name: "a cluster whose last option eats the rest", args: []string{"-clx"}},
		{name: "an empty option value", args: []string{"-r", ""}},
		{name: "a value that is not valid UTF-8", args: []string{"-t", "\xff\xfe"}},

		{name: "a lone context", args: []string{"foo"}},
		{name: "an empty context", args: []string{""}},
		{name: "a lone dash is a context", args: []string{"-"}},
		{name: "a context after dashdash", args: []string{"--", "foo"}},
		{name: "a context that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "a context with a newline", args: []string{"a\nb"}},

		{name: "a context and a command", args: []string{"foo", "bar"}},
		{name: "a context, a command and its arguments", args: []string{"foo", "bar", "baz"}},
		{name: "a command after dashdash", args: []string{"--", "foo", "bar"}},
		{name: "compute makes the first operand the command", args: []string{"-c", "foo"}},
		{name: "compute then dashdash then the command", args: []string{"-c", "--", "foo"}},
		{name: "a type makes the first operand the command", args: []string{"-t", "x", "foo"}},
		{name: "a type, a command and its arguments", args: []string{"-t", "x", "foo", "bar"}},
		{name: "a role makes the first operand the command", args: []string{"-r", "a", "foo"}},
		{name: "a command that is not valid UTF-8", args: []string{"foo", "\xff\xfe"}},

		{name: "an option after an operand is the command", args: []string{"foo", "-x"}},
		{name: "POSIXLY_CORRECT changes nothing, the scan already stops",
			args: []string{"foo", "-x"}, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "two roles", args: []string{"-r", "a", "-r", "b"}},
		{name: "two roles with a command", args: []string{"-r", "a", "-r", "b", "/bin/true"}},
		{name: "two types", args: []string{"-t", "a", "-t", "b"}},
		{name: "two users", args: []string{"-u", "a", "-u", "b"}},
		{name: "two ranges", args: []string{"-l", "a", "-l", "b"}},
		{name: "two roles spelled long", args: []string{"--role=a", "--role=b"}},
		{name: "two empty roles still count", args: []string{"-r", "", "-r", ""}},
		{name: "the first repeat wins", args: []string{"-r", "a", "-r", "b", "-t", "c", "-t", "d"}},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "the empty long option is ambiguous", args: []string{"--=x"}},
		{name: "a missing short value", args: []string{"-u"}},
		{name: "a missing short value at the end of a cluster", args: []string{"-cl"}},
		{name: "a missing value for type", args: []string{"-t"}},
		{name: "a missing value for role", args: []string{"-r"}},
		{name: "a missing value for range", args: []string{"-l"}},
		{name: "a missing long value names the canonical option", args: []string{"--user"}},
		{name: "a missing long value for role", args: []string{"--role"}},
		{name: "a missing long value for range", args: []string{"--range"}},
		{name: "a missing long value for type", args: []string{"--type"}},
		{name: "an ambiguous prefix lists in declaration order", args: []string{"--r"}},
		{name: "range as a unique prefix", args: []string{"--ra"}},
		{name: "role as a unique prefix", args: []string{"--ro"}},
		{name: "user as a unique prefix", args: []string{"--u"}},
		{name: "type as a unique prefix", args: []string{"--t"}},
		{name: "there is no long option starting with l", args: []string{"--l"}},
		{name: "compute as a unique prefix", args: []string{"--c"}},
		{name: "compute as a longer prefix", args: []string{"--com"}},
		{name: "a flag refuses a value", args: []string{"--compute=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=x"}},

		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed with options", args: []string{"-t", "x"}, stdout: stdoutClosed},
		{name: "stdout closed on a usage error", args: []string{"foo"}, stdout: stdoutClosed},
		{name: "stdout closed on the SELinux refusal", args: []string{"foo", "bar"}, stdout: stdoutClosed},
		{name: "stdout closed under help", args: []string{"--help"}, stdout: stdoutClosed},
		{name: "stdout full under help", args: []string{"--help"}, stdout: stdoutFull},
		{name: "stdout closed under version", args: []string{"--version"}, stdout: stdoutClosed},
		{name: "stdout full under version", args: []string{"--version"}, stdout: stdoutFull},
	}
}

func TestRunconParity(t *testing.T) {
	requireParity(t, "runcon", runconCases(t))
}

func TestRunconHelpVersion(t *testing.T) {
	requireHelp(t, "runcon", []string{"--help"}, 0)
	requireHelp(t, "runcon", []string{"--hel"}, 0)
	requireHelp(t, "runcon", []string{"--h"}, 0)
	requireVersion(t, "runcon", []string{"--version"}, 0)
	requireVersion(t, "runcon", []string{"--vers"}, 0)
	requireVersion(t, "runcon", []string{"--v"}, 0)
}
