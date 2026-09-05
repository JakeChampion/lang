package coreutils

import "testing"

func TestEchoParity(t *testing.T) {
	requireParity(t, "echo", []invocation{
		{name: "no arguments"},
		{name: "one operand", args: []string{"a"}},
		{name: "operands are space separated", args: []string{"a", "b", "c"}},
		{name: "empty operand", args: []string{""}},
		{name: "empty operands keep their separators", args: []string{"", "", ""}},
		{name: "no trailing newline", args: []string{"-n", "a"}},
		{name: "no operands at all after -n", args: []string{"-n"}},
		{name: "no operands at all after -e", args: []string{"-e"}},

		// The option scan: a run of e/E/n after the dash is an option
		// token, anything else is an operand, and the last flag wins.
		{name: "bundled flags", args: []string{"-en", `x\ny`}},
		{name: "bundled flags the other way", args: []string{"-ne", `x\ny`}},
		{name: "repeated flags", args: []string{"-n", "-e", "-n", `x\ny`}},
		{name: "escapes turned back off", args: []string{"-e", "-E", `a\tb`}},
		{name: "a bundle with a foreign letter is an operand", args: []string{"-nex", `x\ny`}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "dashdash is an operand", args: []string{"--"}},
		{name: "dashdash does not end the options", args: []string{"--", "x"}},
		{name: "an operand stops the option scan", args: []string{"x", "-n"}},

		// Long options: only as the sole argument.
		{name: "help with an operand after it", args: []string{"--help", "x"}},
		{name: "version with an operand after it", args: []string{"--version", "x"}},
		{name: "abbreviated help is an operand", args: []string{"--hel"}},

		// -E is the default.
		{name: "escapes off by default", args: []string{`a\tb`}},
		{name: "escapes off explicitly", args: []string{"-E", `a\tb`}},

		// The escape table.
		{name: "named escapes", args: []string{"-e", `a\tb\nc\rd\\e`}},
		{name: "the control escapes", args: []string{"-e", `\a\b\e\f\v`}},
		{name: "octal escapes with a leading zero", args: []string{"-e", `oct:\0 \01 \012 \0129 \0777 \08`}},
		{name: "octal escapes without one", args: []string{"-e", `oct:\7 \10 \101 \1017 \1234 \400`}},
		{name: "hex escapes", args: []string{"-e", `hex:\x \x4 \x41A \xzz \xff \xFf`}},
		{name: "an unknown escape keeps its backslash", args: []string{"-e", `a\z\8\`}},
		{name: "eight and nine are not octal", args: []string{"-e", `\8 \9`}},
		{name: "backslash c ends the output", args: []string{"-e", `\c`}},
		{name: "backslash c drops the rest", args: []string{"-e", `a\cb`, "c"}},
		{name: "backslash c with -n", args: []string{"-ne", `a\cb`}},
		{name: "escapes in a later operand", args: []string{"-e", "a", `b\tc`}},
		{name: "a byte that is not valid UTF-8", args: []string{"-e", "\xff\xfe"}},

		// POSIXLY_CORRECT: escapes are always on, and options are only
		// recognised when the first argument is exactly `-n`.
		{name: "posix escapes are always on", args: []string{`a\tb`}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix does not recognise -e", args: []string{"-e", `a\tb`}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix recognises a leading -n", args: []string{"-n", `a\tb`}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix -n then -e", args: []string{"-n", "-e", `a\tb`}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix cannot turn escapes off", args: []string{"-n", "-E", `a\tb`}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix does not answer --help", args: []string{"--help"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix empty value still counts as set", args: []string{`a\tb`}, env: []string{"POSIXLY_CORRECT="}},
	})
}

func TestEchoHelpVersion(t *testing.T) {
	requireHelp(t, "echo", []string{"--help"}, 0)
	requireVersion(t, "echo", []string{"--version"}, 0)
}
