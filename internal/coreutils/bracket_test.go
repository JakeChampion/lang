package coreutils

import "testing"

// TestBracketParity holds coreutils/[.fern to GNU [. The expression
// corpus is test(1)'s with a `]` appended; the cases of its own are the
// closing bracket and the two options it honours where `test` does not.
// bracketCases is the corpus, shared by the GNU parity gate and the
// self-host leg so neither can test something narrower than the other.
func bracketCases(t *testing.T) []invocation {
	t.Helper()
	var cases []invocation
	for _, inv := range condCases(t) {
		inv.args = append(append([]string{}, inv.args...), "]")
		cases = append(cases, inv)
	}
	return append(cases,
		// The closing bracket.
		invocation{name: "no arguments at all"},
		invocation{name: "close bracket alone", args: []string{"]"}},
		invocation{name: "string without a close bracket", args: []string{"x"}},
		invocation{name: "empty without a close bracket", args: []string{""}},
		invocation{name: "bang without a close bracket", args: []string{"!"}},
		invocation{name: "comparison without a close bracket", args: []string{"x", "=", "x"}},
		invocation{name: "close bracket then string", args: []string{"]", "x"}},
		invocation{name: "close bracket in the middle", args: []string{"x", "]", "x"}},
		invocation{name: "two close brackets", args: []string{"]", "]"}},
		invocation{name: "three close brackets", args: []string{"]", "]", "]"}},
		invocation{name: "string then two close brackets", args: []string{"x", "]", "]"}},
		invocation{name: "double close bracket token", args: []string{"x", "]]"}},
		invocation{name: "close bracket with a space", args: []string{"x", "] "}},
		invocation{name: "open bracket then close bracket", args: []string{"[", "]"}},
		invocation{name: "dashdash then close bracket", args: []string{"--", "]"}},
		invocation{name: "dash then close bracket", args: []string{"-", "]"}},
		invocation{name: "error without a close bracket", args: []string{"x", "y"}},
		invocation{name: "missing bracket with stdout closed", args: []string{"x"}, stdout: stdoutClosed},

		// --help / --version: honoured as the sole argument and nowhere
		// else — with a `]` they are strings, with anything else they
		// are a missing bracket.
		invocation{name: "help then close bracket", args: []string{"--help", "]"}},
		invocation{name: "version then close bracket", args: []string{"--version", "]"}},
		invocation{name: "abbreviated help alone", args: []string{"--hel"}},
		invocation{name: "help with a value alone", args: []string{"--help=x"}},
		invocation{name: "upper case help alone", args: []string{"--HELP"}},
		invocation{name: "short h alone", args: []string{"-h"}},
		invocation{name: "help then string", args: []string{"--help", "x"}},
		invocation{name: "help then string then close bracket", args: []string{"--help", "x", "]"}},
		invocation{name: "help twice then close bracket", args: []string{"--help", "--help", "]"}},
		invocation{name: "help then version", args: []string{"--help", "--version"}},
		invocation{name: "version then help", args: []string{"--version", "--help"}},
		invocation{name: "version then string", args: []string{"--version", "x"}},
		invocation{name: "string then help", args: []string{"x", "--help"}},
		invocation{name: "string then version", args: []string{"x", "--version"}},
		invocation{name: "dashdash then help then close bracket", args: []string{"--", "--help", "]"}},
		invocation{name: "dash n of help then close bracket", args: []string{"-n", "--help", "]"}},
		invocation{name: "dash z of help then close bracket", args: []string{"-z", "--help", "]"}},
		invocation{name: "help under posix then close bracket", args: []string{"--help", "]"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The help and version text is ours, but a write failure is
		// reported like GNU's: `[: write error: <strerror>`, and with
		// test's own status of 2 rather than 1.
		invocation{name: "help with stdout closed", args: []string{"--help"}, stdout: stdoutClosed},
		invocation{name: "help with stdout full", args: []string{"--help"}, stdout: stdoutFull},
		invocation{name: "version with stdout closed", args: []string{"--version"}, stdout: stdoutClosed},
		invocation{name: "version with stdout full", args: []string{"--version"}, stdout: stdoutFull},
	)
}

func TestBracketParity(t *testing.T) {
	requireParity(t, "[", bracketCases(t))
}

func TestBracketHelp(t *testing.T) {
	requireHelp(t, "[", []string{"--help"}, 0)
	requireVersion(t, "[", []string{"--version"}, 0)
}
