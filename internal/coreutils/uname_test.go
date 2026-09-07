package coreutils

import "testing"

// uname(1) prints the fields of the kernel's utsname record in one
// order whatever order the options were given in, space separated, with
// one trailing newline. Both sides read the same kernel, so the VALUES
// are compared, not just the shape.
//
// The two quirks the corpus is built around:
//
//   - `-a` is not the same as naming all eight options. GNU's `-a` is a
//     distinct value, and the omission rule ("drop -p and -i when they
//     are unknown") tests exactly that value, so `-s -n -r -v -m -p -i
//     -o` prints an unknown element where `-a` drops it. On Linux
//     neither is unknown, which is what makes the two lines identical
//     here and is itself worth pinning.
//   - `--sysname` and `--release` are obsolescent aliases GNU still
//     declares. They are why `--s` and `--r` resolve while `--k` is
//     ambiguous between three, listed in declaration order.
func unameCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments is the kernel name"},
		{name: "dashdash alone", args: []string{"--"}},

		// Every element on its own.
		{name: "-s", args: []string{"-s"}},
		{name: "-n", args: []string{"-n"}},
		{name: "-r", args: []string{"-r"}},
		{name: "-v", args: []string{"-v"}},
		{name: "-m", args: []string{"-m"}},
		{name: "-p", args: []string{"-p"}},
		{name: "-i", args: []string{"-i"}},
		{name: "-o", args: []string{"-o"}},
		{name: "-a", args: []string{"-a"}},

		// The long spellings, including the two obsolescent aliases.
		{name: "--kernel-name", args: []string{"--kernel-name"}},
		{name: "--sysname", args: []string{"--sysname"}},
		{name: "--nodename", args: []string{"--nodename"}},
		{name: "--kernel-release", args: []string{"--kernel-release"}},
		{name: "--release", args: []string{"--release"}},
		{name: "--kernel-version", args: []string{"--kernel-version"}},
		{name: "--machine", args: []string{"--machine"}},
		{name: "--processor", args: []string{"--processor"}},
		{name: "--hardware-platform", args: []string{"--hardware-platform"}},
		{name: "--operating-system", args: []string{"--operating-system"}},
		{name: "--all", args: []string{"--all"}},

		// Unique-prefix matching, and the prefixes the aliases save.
		{name: "--s is sysname", args: []string{"--s"}},
		{name: "--r is release", args: []string{"--r"}},
		{name: "--m", args: []string{"--m"}},
		{name: "--o", args: []string{"--o"}},
		{name: "--n", args: []string{"--n"}},
		{name: "--p", args: []string{"--p"}},
		{name: "--h is ambiguous with help", args: []string{"--h"}},
		{name: "--kernel- is ambiguous", args: []string{"--kernel-"}},
		{name: "--k is ambiguous between three", args: []string{"--k"}},
		{name: "--a is ambiguous with all", args: []string{"--a"}},

		// Combinations: the order is the record's, not the argv's.
		{name: "-sm", args: []string{"-sm"}},
		{name: "-ms is still kernel name first", args: []string{"-ms"}},
		{name: "-mnsrv", args: []string{"-mnsrv"}},
		{name: "every element named", args: []string{"-s", "-n", "-r", "-v", "-m", "-p", "-i", "-o"}},
		{name: "every element in one cluster", args: []string{"-snrvmpio"}},
		{name: "-a then -s adds nothing", args: []string{"-a", "-s"}},
		{name: "-s then -a is still all", args: []string{"-s", "-a"}},
		{name: "-a twice", args: []string{"-a", "-a"}},
		{name: "-s twice", args: []string{"-s", "-s"}},
		{name: "long and short mixed", args: []string{"--machine", "-s"}},
		{name: "alias and canonical name for one field", args: []string{"--sysname", "--kernel-name"}},

		// Operands, all of them faults.
		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a newline", args: []string{"a\nb"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "operand before an option is permuted out", args: []string{"x", "-s"}},
		{name: "option-looking operand after dashdash", args: []string{"--", "-s"}},

		// getopt faults.
		{name: "invalid short option", args: []string{"-z"}},
		{name: "invalid byte in a valid cluster", args: []string{"-sz"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "value on an option that takes none", args: []string{"--all=1"}},
		{name: "value on a long alias", args: []string{"--machine=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{"x", "--foo"}},
		{name: "bad option before an operand", args: []string{"--foo", "x"}},

		// The UNAME_* variables are honoured on Darwin only, so on
		// Linux they must change nothing on either side.
		{name: "UNAME_MACHINE is not honoured here", args: []string{"-m"}, env: []string{"UNAME_MACHINE=nonesuch"}},
		{name: "UNAME_SYSNAME is not honoured here", args: []string{"-s"}, env: []string{"UNAME_SYSNAME=nonesuch"}},

		// POSIXLY_CORRECT stops the scan at the first operand, which
		// turns a permuted fault into an extra-operand one.
		{name: "posixly correct stops at an operand", args: []string{"x", "-s"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths: one write, one strerror.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on -a", args: []string{"-a"}, stdout: stdoutClosed},
		{name: "stdout closed on a fault", args: []string{"x"}, stdout: stdoutClosed},
	}
}

func TestUnameParity(t *testing.T) {
	requireParity(t, "uname", unameCases(t))
}

func TestUnameHelpVersion(t *testing.T) {
	requireHelp(t, "uname", []string{"--help"}, 0)
	requireHelp(t, "uname", []string{"--hel"}, 0)
	requireHelp(t, "uname", []string{"--help", "x"}, 0)
	requireHelp(t, "uname", []string{"x", "--help"}, 0)
	requireHelp(t, "uname", []string{"-a", "--help"}, 0)
	requireVersion(t, "uname", []string{"--version"}, 0)
	requireVersion(t, "uname", []string{"--vers"}, 0)
	requireVersion(t, "uname", []string{"--version", "x"}, 0)
}
