package coreutils

import "testing"

// factorCases is the corpus, shared by the GNU parity gate and the
// self-host leg so neither can test something narrower than the other.
func factorCases(t *testing.T) []invocation {
	t.Helper()
	return []invocation{
		// Small numbers, and the two that have no factors to print.
		{name: "twelve", args: []string{"12"}},
		{name: "zero", args: []string{"0"}},
		{name: "one", args: []string{"1"}},
		{name: "two", args: []string{"2"}},
		{name: "three", args: []string{"3"}},
		{name: "four", args: []string{"4"}},
		{name: "six", args: []string{"6"}},
		{name: "eight", args: []string{"8"}},
		{name: "nine", args: []string{"9"}},
		{name: "hundred", args: []string{"100"}},
		{name: "power of two", args: []string{"1024"}},
		{name: "sixteen bits", args: []string{"65535"}},
		{name: "sixteen bits exactly", args: []string{"65536"}},
		{name: "several operands", args: []string{"12", "15", "7"}},
		{name: "repeated operand", args: []string{"12", "12"}},

		// The spellings a number may have.
		{name: "plus sign", args: []string{"+5"}},
		{name: "leading blank", args: []string{" 5"}},
		{name: "blank then plus", args: []string{" +5"}},
		{name: "leading zeros", args: []string{"000012"}},
		{name: "leading zero is not octal", args: []string{"018"}},
		{name: "leading zeros on a prime", args: []string{"007"}},
		{name: "all zeros", args: []string{"000"}},
		{name: "plus zero", args: []string{"+0"}},

		// 64-bit numbers: trial division, Miller-Rabin, Pollard rho.
		{name: "nine digit prime", args: []string{"1000000007"}},
		{name: "semiprime of two nine digit primes", args: []string{"1000000016000000063"}},
		{name: "fermat number", args: []string{"4294967297"}},
		{name: "signed max", args: []string{"9223372036854775807"}},
		{name: "signed max plus one", args: []string{"9223372036854775808"}},
		{name: "largest sixty four bit prime", args: []string{"18446744073709551557"}},
		{name: "unsigned max", args: []string{"18446744073709551615"}},
		{name: "another large prime", args: []string{"18446744073709551533"}},
		{name: "power of two sixty", args: []string{"1152921504606846976"}},
		{name: "mersenne prime", args: []string{"2305843009213693951"}},
		{name: "seven digit prime", args: []string{"1000003"}},
		{name: "twelve digit prime", args: []string{"999999999989"}},
		{name: "eighteen digit prime", args: []string{"999999999999999989"}},

		// Past sixty four bits.
		{name: "two to the sixty four", args: []string{"18446744073709551616"}},
		{name: "twenty nines", args: []string{"99999999999999999999"}},
		{name: "two to the one twenty eight minus one", args: []string{"340282366920938463463374607431768211455"}},
		{name: "two to the one twenty eight", args: []string{"340282366920938463463374607431768211456"}},
		{name: "mersenne prime one twenty seven", args: []string{"170141183460469231731687303715884105727"}},
		{name: "mersenne prime eighty nine", args: []string{"618970019642690137449562111"}},
		{name: "mersenne prime one hundred seven", args: []string{"162259276829213363391578010288127"}},
		{name: "thirty one digit prime", args: []string{"1000000000000000000000000000057"}},
		{name: "repunit shaped", args: []string{"1000000000000000000000000000000000001"}},
		{name: "two to the hundred", args: []string{"1267650600228229401496703205376"}},
		{name: "ten to the fifty nine", args: []string{"100000000000000000000000000000000000000000000000000000000000"}},
		{name: "forty seven nines", args: []string{"99999999999999999999999999999999999999999999999"}},
		{name: "just under two to the one twenty seven", args: []string{"170141183460469231731687303715884105726"}},
		{name: "just over two to the one twenty seven", args: []string{"170141183460469231731687303715884105728"}},

		// The unbuffered line: a number at or above 2^127 reaches the
		// descriptor before the buffered ones do.
		{name: "buffered then unbuffered", args: []string{"15", "340282366920938463463374607431768211455"}},
		{name: "unbuffered then buffered", args: []string{"340282366920938463463374607431768211455", "15"}},
		{name: "buffered around unbuffered", args: []string{"15", "340282366920938463463374607431768211455", "21"}},
		{name: "two unbuffered", args: []string{"340282366920938463463374607431768211455", "170141183460469231731687303715884105728"}},

		// -h: repeated factors as p^e.
		{name: "exponents", args: []string{"-h", "8"}},
		{name: "exponents long", args: []string{"--exponents", "12"}},
		{name: "exponents on a prime", args: []string{"-h", "7"}},
		{name: "exponents abbreviated", args: []string{"--e", "8"}},
		{name: "exponents twice", args: []string{"-hh", "8"}},
		{name: "exponents after the operand", args: []string{"12", "-h"}},
		{name: "exponents among operands", args: []string{"3", "5", "-h", "8"}},
		{name: "exponents on zero", args: []string{"-h", "0"}},
		{name: "exponents on one", args: []string{"-h", "1"}},
		{name: "exponents on two", args: []string{"-h", "2"}},
		{name: "exponents on four", args: []string{"-h", "4"}},
		{name: "exponents on a power of two", args: []string{"-h", "1024"}},
		{name: "exponents past sixty four bits", args: []string{"-h", "18446744073709551616"}},
		{name: "exponents on a wide number", args: []string{"-h", "340282366920938463463374607431768211455"}},
		{name: "exponents on two numbers", args: []string{"-h", "12", "15"}},

		// Numbers that are not numbers: reported, and the rest still run.
		{name: "letter", args: []string{"x"}},
		{name: "between two numbers", args: []string{"12", "x", "15"}},
		{name: "trailing letter", args: []string{"5x"}},
		{name: "empty operand", args: []string{""}},
		{name: "exponent notation", args: []string{"1e3"}},
		{name: "hex", args: []string{"0x10"}},
		{name: "two numbers in one operand", args: []string{"12 15"}},
		{name: "trailing blank", args: []string{"5 "}},
		{name: "plus alone", args: []string{"+"}},
		{name: "dash alone", args: []string{"-"}},
		{name: "decimal point", args: []string{"1.5"}},
		{name: "plus then blank", args: []string{"+ 5"}},
		{name: "two plus signs", args: []string{"++5"}},
		{name: "tab before the digits", args: []string{"\t5"}},
		{name: "newline before the digits", args: []string{"\n5"}},
		{name: "newline after the digits", args: []string{"5\n"}},
		{name: "vertical tab", args: []string{"\v5"}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff"}},
		// A NUL cannot reach argv at all — exec refuses it — so the
		// cut-at-NUL rule is exercised from stdin below.
		{name: "negative after dashdash", args: []string{"--", "-1"}},
		{name: "negative after an operand", args: []string{"2", "--", "-h"}},
		{name: "every operand is invalid", args: []string{"a", "b"}},

		// Getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "negative number is an option", args: []string{"-1"}},
		{name: "exponents does not take a value", args: []string{"--exponents=x"}},
		{name: "exponents with an empty value", args: []string{"--exponents="}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "dashdash then an operand", args: []string{"12", "--", "15"}},
		{name: "debug without the extra dash", args: []string{"--debug", "12"}},
		{name: "posix stops at the first operand", args: []string{"2", "-h"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix options first still apply", args: []string{"-h", "12"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Numbers from stdin.
		{name: "stdin lines", stdin: "12 15\n7\n"},
		{name: "stdin tabs and blank lines", stdin: "12\t15  \n\n 7"},
		{name: "stdin no trailing newline", stdin: "12 15"},
		{name: "stdin with an invalid token", stdin: "x\n5\n"},
		{name: "stdin empty"},
		{name: "stdin blanks only", stdin: "  "},
		{name: "stdin NUL cuts a token", stdin: "1\x002\n"},
		{name: "stdin NUL alone", stdin: "\x00\n"},
		{name: "stdin NUL with no newline", stdin: "\x00"},
		{name: "stdin NUL inside a token", stdin: "1\x00\x002\n"},
		{name: "stdin letters", stdin: "a b\n"},
		{name: "stdin non UTF-8", stdin: "\xff\n"},
		{name: "stdin carriage return", stdin: "12\r\n"},
		{name: "stdin with exponents", stdin: "12\n", args: []string{"-h"}},
		{name: "stdin signed numbers", stdin: "+5\n-5\n"},
		{name: "stdin one number", stdin: "5"},
		{name: "stdin zero and one", stdin: "0\n1\n"},
		{name: "stdin past sixty four bits", stdin: "18446744073709551616\n"},
		{name: "stdin past one twenty seven bits", stdin: "340282366920938463463374607431768211456\n"},
		{name: "stdin exponent notation", stdin: "1e3\n"},
		{name: "stdin comma", stdin: "5,6\n"},
		{name: "stdin decimal", stdin: "5.0\n"},
		{name: "stdin hex", stdin: "0x10\n"},
		{name: "stdin vertical tab and form feed", stdin: "12\v13\f14\n"},
		{name: "stdin surrounded by blanks", stdin: "  12  \n"},
		{name: "operands win over stdin", stdin: "12", args: []string{"15"}},
		{name: "dash is not stdin", stdin: "12\n", args: []string{"-"}},
		{name: "stdin invalid then valid with exponents", stdin: "12 x\n", args: []string{"-h"}},
		{name: "stdin mixes the two engines", stdin: "18446744073709551615\n340282366920938463463374607431768211455\n", args: []string{"-h"}},
		{name: "stdin many numbers", stdin: "2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20\n"},

		// Write failures.
		{name: "stdout closed", args: []string{"12"}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{"12"}, stdout: stdoutFull},
		{name: "stdout closed on an invalid operand", args: []string{"x"}, stdout: stdoutClosed},
		{name: "stdout full on an invalid operand", args: []string{"x"}, stdout: stdoutFull},
		{name: "stdout full with a valid operand after", args: []string{"x", "12"}, stdout: stdoutFull},
		{name: "stdout closed from stdin", stdin: "12\n", stdout: stdoutClosed},
		{name: "stdout full from stdin", stdin: "12\n", stdout: stdoutFull},
		{name: "stdout full from stdin with an error", stdin: "12\nx\n", stdout: stdoutFull},
		{name: "stdout closed on an unbuffered line", args: []string{"340282366920938463463374607431768211455"}, stdout: stdoutClosed},
		{name: "stdout full on an unbuffered line", args: []string{"340282366920938463463374607431768211455"}, stdout: stdoutFull},
		{name: "stdout full over many lines", stdin: "2 3 4 5 6 7 8 9 10 11 12 13 14 15\n", stdout: stdoutFull},
	}
}

func TestFactorParity(t *testing.T) {
	requireParity(t, "factor", factorCases(t))
}

func TestFactorHelpVersion(t *testing.T) {
	requireHelp(t, "factor", []string{"--help"}, 0)
	requireHelp(t, "factor", []string{"--hel"}, 0)
	requireHelp(t, "factor", []string{"--help", "12"}, 0)
	requireHelp(t, "factor", []string{"-h", "--help"}, 0)
	requireHelp(t, "factor", []string{"--help", "--foo"}, 0)
	requireVersion(t, "factor", []string{"--version"}, 0)
	requireVersion(t, "factor", []string{"--v"}, 0)
	requireVersion(t, "factor", []string{"--version", "12"}, 0)
}
