package coreutils

import "testing"

// Every interval here is zero or a few milliseconds: the corpus asserts
// what sleep(1) PRINTS and how it exits, and a case that actually paused
// would only make the gate slower. The one behaviour it cannot cover is a
// successful infinite sleep — `sleep inf` never returns, and the harness
// has nothing to bound a process that writes nothing — so `inf` appears
// here only in the spellings that are refused.
func TestSleepParity(t *testing.T) {
	requireParity(t, "sleep", []invocation{
		// Intervals that are accepted, and the suffixes.
		{name: "zero", args: []string{"0"}},
		{name: "zero seconds", args: []string{"0s"}},
		{name: "zero point zero", args: []string{"0.0"}},
		{name: "leading dot zero", args: []string{".0"}},
		{name: "zero minutes", args: []string{"0m"}},
		{name: "zero hours", args: []string{"0h"}},
		{name: "zero days", args: []string{"0d"}},
		{name: "every suffix summed", args: []string{"0m", "0h", "0d"}},
		{name: "a nanosecond", args: []string{"1e-9"}},
		{name: "hex zero", args: []string{"0x0"}},
		{name: "a billionth written out", args: []string{"0.000000001"}},
		{name: "leading blank", args: []string{" 0"}},
		{name: "trailing point", args: []string{"0."}},
		{name: "exponent", args: []string{"0.0e0"}},
		{name: "plus sign", args: []string{"+0"}},
		{name: "days then seconds", args: []string{"0d", "0"}},
		{name: "negative zero after dashdash", args: []string{"--", "-0"}},
		{name: "hex power", args: []string{"0x1p-30"}},
		{name: "hex leading point", args: []string{"0x.0"}},
		{name: "underflowing exponent", args: []string{"1e-400"}},
		{name: "underflowing exponent with a suffix", args: []string{"1e-4000d"}},
		{name: "hex with a suffix", args: []string{"0x1p1s"}},
		{name: "exponent with a suffix", args: []string{"1e-9m"}},
		{name: "leading dot with a suffix", args: []string{".5s"}},
		{name: "plus and a suffix", args: []string{"+0.0h"}},
		{name: "exponent zero with a suffix", args: []string{"0e0d"}},
		{name: "hex zero with a suffix", args: []string{"0x0s"}},
		{name: "exponent zero", args: []string{"0e0"}},
		{name: "two zeros", args: []string{"00"}},
		{name: "sum of tiny intervals", args: []string{"1e-9", "1e-9", "1e-9"}},
		{name: "mixed zero spellings", args: []string{"0", "0.0", "0m"}},
		{name: "tiny hex power", args: []string{"0x1p-40"}},
		{name: "very negative hex exponent", args: []string{"0x1.8p-1000000000"}},
		{name: "just under a millisecond", args: []string{"0.0009"}},
		{name: "just over a millisecond", args: []string{"0.0011"}},
		{name: "a millionth", args: []string{"0.000001"}},
		{name: "zero and a nanosecond", args: []string{"0", "1e-9"}},
		{name: "dashdash then zero", args: []string{"--", "0"}},
		{name: "zero then dashdash", args: []string{"0", "--"}},
		{name: "dashdash alone is not a missing operand", args: []string{"--"}},
		{name: "zero then a dashdash operand", args: []string{"--", "--"}},

		// Intervals that are refused, one line each, then the try line.
		{name: "a letter", args: []string{"x"}},
		{name: "negative", args: []string{"--", "-1"}},
		{name: "valid then invalid", args: []string{"1", "x"}},
		{name: "two invalid", args: []string{"x", "y"}},
		{name: "three invalid", args: []string{"x", "y", "z"}},
		{name: "invalid among valid", args: []string{"0", "x", "0", "y"}},
		{name: "empty operand", args: []string{""}},
		{name: "two empty operands", args: []string{"", ""}},
		{name: "trailing blank", args: []string{"0 "}},
		{name: "hex prefix alone", args: []string{"0x"}},
		{name: "exponent with no digits", args: []string{"0e"}},
		{name: "not a number", args: []string{"nan"}},
		{name: "not a number capitalised", args: []string{"NaN"}},
		{name: "not a number with a payload", args: []string{"nan(x)"}},
		{name: "capital suffix", args: []string{"0S"}},
		{name: "capital minutes", args: []string{"0M"}},
		{name: "capital hours", args: []string{"0H"}},
		{name: "capital days", args: []string{"0D"}},
		{name: "two letter suffix", args: []string{"0ms"}},
		{name: "digits after the suffix", args: []string{"0s0"}},
		{name: "suffix twice", args: []string{"0ss"}},
		{name: "unknown suffix", args: []string{"0.5x"}},
		{name: "suffix inside", args: []string{"5s5"}},
		{name: "infinity glued to a digit", args: []string{"0inf"}},
		{name: "infinity with a blank", args: []string{"inf s"}},
		{name: "point alone", args: []string{"."}},
		{name: "two points", args: []string{"0.0.0"}},
		{name: "hex power with no digits", args: []string{"0x0p"}},
		{name: "lone dash", args: []string{"-"}},
		{name: "newline in the operand", args: []string{"0\n"}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff"}},
		{name: "zero then an empty operand", args: []string{"0", ""}},
		{name: "invalid first", args: []string{"x", "0"}},
		{name: "overflowing then invalid", args: []string{"1e5000", "x"}},

		// The option scan: two options, permuting, and every fault.
		{name: "no operands"},
		{name: "negative number is an option", args: []string{"-1"}},
		{name: "negative zero is an option", args: []string{"-0"}},
		{name: "negative decimal is an option", args: []string{"-0.0"}},
		{name: "negative with a suffix is an option", args: []string{"-0s"}},
		{name: "negative infinity is an option", args: []string{"-inf"}},
		{name: "invalid short option", args: []string{"-w"}},
		{name: "suffix as a short option", args: []string{"-s"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized after an operand", args: []string{"0", "--foo"}},
		{name: "unrecognized digit option", args: []string{"--0"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "dashdash between operands", args: []string{"0", "--", "0"}},
		{name: "posix stops permuting", args: []string{"0", "--foo"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"--foo", "0"}, env: []string{"POSIXLY_CORRECT=1"}},

		// sleep writes nothing, so a closed or full stdout is only
		// visible on the paths that do write — the diagnostics.
		{name: "stdout closed", args: []string{"0"}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"0"}, stdout: stdoutFull},
		{name: "stdout closed on an invalid interval", args: []string{"x"}, stdout: stdoutClosed},
		{name: "stdout full on an invalid interval", args: []string{"x"}, stdout: stdoutFull},
		{name: "stdout closed with no operands", stdout: stdoutClosed},
		{name: "stdout full with no operands", stdout: stdoutFull},
	})
}

func TestSleepHelpVersion(t *testing.T) {
	requireHelp(t, "sleep", []string{"--help"}, 0)
	requireHelp(t, "sleep", []string{"--h"}, 0)
	requireHelp(t, "sleep", []string{"--help", "x"}, 0)
	requireHelp(t, "sleep", []string{"0", "--help"}, 0)
	requireHelp(t, "sleep", []string{"--help", "--foo"}, 0)
	requireVersion(t, "sleep", []string{"--version"}, 0)
	requireVersion(t, "sleep", []string{"--vers"}, 0)
	requireVersion(t, "sleep", []string{"--version", "x"}, 0)
}
