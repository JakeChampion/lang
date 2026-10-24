package coreutils

import (
	"regexp"
	"testing"
)

func init() {
	registerCorpus("pinky", pinkyCases)
}

var pinkyIdleField = regexp.MustCompile(`(?m)^(.*) .{6} ([A-Z][a-z]{2} [ 0-9][0-9] [0-9]{2}:[0-9]{2})`)

func pinkyIdle(out string) string {
	return pinkyIdleField.ReplaceAllString(out, "$1 ~idle~ $2")
}

func pinkyCases(t *testing.T) []invocation {
	cases := []invocation{
		{name: "no arguments"},
		{name: "dashdash alone", args: []string{"--"}},
	}

	for _, opt := range []string{"-s", "-f", "-w", "-i", "-q", "-b", "-h", "-p"} {
		cases = append(cases,
			invocation{name: "option " + opt, args: []string{opt}},
			invocation{name: "option " + opt + " filtered to nobody logged in", args: []string{opt, "fernnosuchuser"}},
		)
	}

	cases = append(cases,
		invocation{name: "-w then -q", args: []string{"-wq"}},
		invocation{name: "-q then -w", args: []string{"-qw"}},
		invocation{name: "-i then -q", args: []string{"-iq"}},
		invocation{name: "-q then -i", args: []string{"-qi"}},
		invocation{name: "-w with no heading", args: []string{"-wf"}},
		invocation{name: "-q with no heading", args: []string{"-fq"}},
		invocation{name: "every short-format option at once", args: []string{"-sfwiq"}},
		invocation{name: "the long-format options in short format", args: []string{"-bhp"}},

		invocation{name: "an operand nobody is logged in under", args: []string{"fernnosuchuser"}},
		invocation{name: "two such operands", args: []string{"fernnosuchuser", "fernother"}},
		invocation{name: "an empty operand", args: []string{""}},
		invocation{name: "an operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		invocation{name: "lone dash is an operand", args: []string{"-"}},
		invocation{name: "operand after dashdash", args: []string{"--", "fernnosuchuser"}},
		invocation{name: "option-looking operand after dashdash", args: []string{"--", "-l"}},
		invocation{name: "a name with a trailing blank", args: []string{"root "}},

		invocation{name: "long format for one user", args: []string{"-l", "root"}},
		invocation{name: "long format for several users", args: []string{"-l", "root", "daemon", "nobody"}},
		invocation{name: "long format repeats a user", args: []string{"-l", "root", "root"}},
		invocation{name: "long format for a user with no entry", args: []string{"-l", "fernnosuchuser"}},
		invocation{name: "long format mixes known and unknown", args: []string{"-l", "root", "fernnosuchuser", "daemon"}},
		invocation{name: "long format for an empty name", args: []string{"-l", ""}},
		invocation{name: "long format for a name that is not valid UTF-8", args: []string{"-l", "\xff\xfe"}},
		invocation{name: "long format for a name with a trailing blank", args: []string{"-l", "root "}},
		invocation{name: "long format without the directory and shell", args: []string{"-l", "-b", "root"}},
		invocation{name: "long format without the project", args: []string{"-l", "-h", "root"}},
		invocation{name: "long format without the plan", args: []string{"-l", "-p", "root"}},
		invocation{name: "long format with nothing but the name", args: []string{"-l", "-b", "-h", "-p", "root"}},
		invocation{name: "the same as a cluster", args: []string{"-lbhp", "root"}},
		invocation{name: "long format ignores the short-format switches", args: []string{"-l", "-f", "-w", "-i", "-q", "root"}},
		invocation{name: "-s after -l is the short format", args: []string{"-l", "-s", "root"}},
		invocation{name: "-l after -s is the long format", args: []string{"-s", "-l", "root"}},
		invocation{name: "-l with a dash operand", args: []string{"-l", "-"}},
		invocation{name: "-l after dashdash is an operand", args: []string{"--", "-l"}},

		invocation{name: "long format with no operand", args: []string{"-l"}},
		invocation{name: "long format with no operand after -s", args: []string{"-s", "-l"}},
		invocation{name: "long format with no operand and a heading off", args: []string{"-lf"}},
		invocation{name: "long format with only dashdash", args: []string{"-l", "--"}},

		invocation{name: "TZ empty is UTC", args: []string{"fernnosuchuser"}, env: []string{"TZ="}},
		invocation{name: "TZ names a zone", args: []string{"fernnosuchuser"}, env: []string{"TZ=America/New_York"}},
		invocation{name: "TZ is a rule string", args: []string{"fernnosuchuser"}, env: []string{"TZ=EST5EDT,M3.2.0,M11.1.0"}},
		invocation{name: "TZ names nothing", args: []string{"fernnosuchuser"}, env: []string{"TZ=Bogus"}},

		invocation{name: "invalid short option", args: []string{"-x"}},
		invocation{name: "invalid short option cluster", args: []string{"-xy"}},
		invocation{name: "invalid option after a valid one", args: []string{"-lx", "root"}},
		invocation{name: "an option who has and pinky does not", args: []string{"-u"}},
		invocation{name: "unrecognized long option", args: []string{"--foo"}},
		invocation{name: "long option with a value", args: []string{"--foo=bar"}},
		invocation{name: "empty long option is ambiguous", args: []string{"--=x"}},
		invocation{name: "a long form of a short option is not one", args: []string{"--long"}},
		invocation{name: "help with a value", args: []string{"--help=x"}},
		invocation{name: "version with a value", args: []string{"--version=1"}},
		invocation{name: "bad option before help", args: []string{"--foo", "--help"}},
		invocation{name: "operand before a bad option", args: []string{"root", "--foo"}},
		invocation{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{"root", "-q"}, env: []string{"POSIXLY_CORRECT=1"}},

		invocation{name: "lookup", args: []string{"--lookup", "root"}},
		invocation{name: "lookup in the long format", args: []string{"--lookup", "-l", "root"}},
		invocation{name: "lookup abbreviated", args: []string{"--look", "root"}},
		invocation{name: "lookup rejects a value", args: []string{"--lookup=x", "root"}},
		invocation{name: "l is ambiguous between lookup and nothing", args: []string{"--l", "root"}},

		invocation{name: "stdout closed", args: []string{"fernnosuchuser"}, stdout: stdoutClosed},
		invocation{name: "stdout full", args: []string{"fernnosuchuser"}, stdout: stdoutFull},
		invocation{name: "stdout closed in the long format", args: []string{"-l", "root"}, stdout: stdoutClosed},
		invocation{name: "stdout full in the long format", args: []string{"-l", "root"}, stdout: stdoutFull},
		invocation{name: "stdout closed with no heading", args: []string{"-f", "fernnosuchuser"}, stdout: stdoutClosed},
		invocation{name: "stdout closed on a fault", args: []string{"-l"}, stdout: stdoutClosed},
	)
	for i := range cases {
		cases[i].mask = pinkyIdle
	}
	return cases
}

func TestPinkyParity(t *testing.T) {
	requireParity(t, "pinky", pinkyCases(t))
}

func TestPinkyHelpVersion(t *testing.T) {
	requireHelp(t, "pinky", []string{"--help"}, 0)
	requireHelp(t, "pinky", []string{"--hel"}, 0)
	requireHelp(t, "pinky", []string{"--help", "x"}, 0)
	requireHelp(t, "pinky", []string{"x", "--help"}, 0)
	requireVersion(t, "pinky", []string{"--version"}, 0)
	requireVersion(t, "pinky", []string{"--vers"}, 0)
	requireVersion(t, "pinky", []string{"--version", "x"}, 0)
}

func TestPinkyIdleMask(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"runner   Anka                *console  00:09  Oct 23 14:33\n", "runner   Anka                *console  ~idle~ Oct 23 14:33\n"},
		{"runner   Anka                *console         Oct 23 14:33\n", "runner   Anka                *console  ~idle~ Oct 23 14:33\n"},
		{"root     root                 pts/0    3d     Oct  2 09:01 host\n", "root     root                 pts/0    ~idle~ Oct  2 09:01 host\n"},
		{"Login    Name                 TTY      Idle   When         Where\n", "Login    Name                 TTY      Idle   When         Where\n"},
		{"Login name: root                        In real life:  root\n", "Login name: root                        In real life:  root\n"},
	} {
		if got := pinkyIdle(tc.in); got != tc.want {
			t.Errorf("pinkyIdle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
