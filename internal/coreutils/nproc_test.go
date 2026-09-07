package coreutils

import "testing"

// nproc(1) answers two different questions. The default is how many
// units THIS PROCESS may run on — the affinity mask, overridable by the
// two OpenMP variables — and `--all` is how many the machine has
// installed, which those variables do NOT override. Both sides run on
// the same machine under the same environment, so the numbers are
// compared, not just the shape.
//
// The corners the corpus is built around:
//
//   - OMP_NUM_THREADS replaces the count and OMP_THREAD_LIMIT caps it,
//     parsed the lenient way OpenMP asks for: blanks either side, a
//     nested-level list accepted for its first element, and anything
//     else — junk, a leading sign, an empty value — read as "not set"
//     rather than as an error.
//   - A value past `unsigned long` saturates instead of failing,
//     because strtoul saturates and nothing looks at the ERANGE.
//   - `--ignore=N` subtracts with a floor of 1, and its diagnostic is
//     NOT a usage error: `invalid number: 'x'` and nothing else, no
//     `Try …` line.
func nprocCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "--all", args: []string{"--all"}},
		{name: "--a is all", args: []string{"--a"}},

		// --ignore, its arithmetic and its floor.
		{name: "ignore zero", args: []string{"--ignore=0"}},
		{name: "ignore one", args: []string{"--ignore=1"}},
		{name: "ignore more than there are", args: []string{"--ignore=1000"}},
		{name: "ignore the largest unsigned long", args: []string{"--ignore=18446744073709551615"}},
		{name: "ignore past unsigned long", args: []string{"--ignore=18446744073709551616"}},
		{name: "ignore with a plus sign", args: []string{"--ignore=+1"}},
		{name: "ignore with leading blanks", args: []string{"--ignore= 1"}},
		{name: "ignore is not negative", args: []string{"--ignore=-1"}},
		{name: "ignore is not a suffix count", args: []string{"--ignore=1k"}},
		{name: "ignore with trailing junk", args: []string{"--ignore=1x"}},
		{name: "ignore of nothing at all", args: []string{"--ignore="}},
		{name: "ignore of a blank", args: []string{"--ignore= "}},
		{name: "ignore twice keeps the last", args: []string{"--ignore=1", "--ignore=2"}},
		{name: "ignore as a separate token", args: []string{"--ignore", "1"}},
		{name: "ignore with no value at all", args: []string{"--ignore"}},
		{name: "--i is ignore", args: []string{"--i=1"}},
		{name: "all and ignore together", args: []string{"--all", "--ignore=1"}},
		{name: "ignore then all", args: []string{"--ignore=1", "--all"}},

		// The OpenMP variables, which the default honours and --all does not.
		{name: "OMP_NUM_THREADS replaces the count", env: []string{"OMP_NUM_THREADS=2"}},
		{name: "OMP_NUM_THREADS does not reach --all", args: []string{"--all"}, env: []string{"OMP_NUM_THREADS=2"}},
		{name: "OMP_NUM_THREADS of one", env: []string{"OMP_NUM_THREADS=1"}},
		{name: "OMP_NUM_THREADS above the machine", env: []string{"OMP_NUM_THREADS=1000"}},
		{name: "OMP_NUM_THREADS of zero is not set", env: []string{"OMP_NUM_THREADS=0"}},
		{name: "OMP_NUM_THREADS empty is not set", env: []string{"OMP_NUM_THREADS="}},
		{name: "OMP_NUM_THREADS junk is not set", env: []string{"OMP_NUM_THREADS=abc"}},
		{name: "OMP_NUM_THREADS with trailing junk is not set", env: []string{"OMP_NUM_THREADS=5x"}},
		{name: "OMP_NUM_THREADS signed is not set", env: []string{"OMP_NUM_THREADS=+5"}},
		{name: "OMP_NUM_THREADS with leading zeros", env: []string{"OMP_NUM_THREADS=0005"}},
		{name: "OMP_NUM_THREADS blanks either side", env: []string{"OMP_NUM_THREADS= 5 "}},
		{name: "OMP_NUM_THREADS nested level list", env: []string{"OMP_NUM_THREADS=3,5"}},
		{name: "OMP_NUM_THREADS nested list with blanks", env: []string{"OMP_NUM_THREADS= 5 , 3"}},
		{name: "OMP_NUM_THREADS past unsigned long saturates", env: []string{"OMP_NUM_THREADS=99999999999999999999"}},
		{name: "OMP_THREAD_LIMIT caps the count", env: []string{"OMP_THREAD_LIMIT=1"}},
		{name: "OMP_THREAD_LIMIT above the machine", env: []string{"OMP_THREAD_LIMIT=1000"}},
		{name: "OMP_THREAD_LIMIT of zero is no limit", env: []string{"OMP_THREAD_LIMIT=0", "OMP_NUM_THREADS=3"}},
		{name: "OMP_THREAD_LIMIT caps OMP_NUM_THREADS", env: []string{"OMP_THREAD_LIMIT=1", "OMP_NUM_THREADS=3"}},
		{name: "OMP_THREAD_LIMIT above OMP_NUM_THREADS", env: []string{"OMP_THREAD_LIMIT=9", "OMP_NUM_THREADS=3"}},
		{name: "OMP_THREAD_LIMIT does not reach --all", args: []string{"--all"}, env: []string{"OMP_THREAD_LIMIT=1"}},
		{name: "OMP_THREAD_LIMIT junk is no limit", env: []string{"OMP_THREAD_LIMIT=abc"}},
		{name: "the OpenMP count then ignored", args: []string{"--ignore=1"}, env: []string{"OMP_NUM_THREADS=3"}},

		// Operands, all of them faults.
		{name: "an operand", args: []string{"x"}},
		{name: "two operands report the first", args: []string{"x", "y"}},
		{name: "lone dash is an operand", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand after dashdash", args: []string{"--", "x"}},
		{name: "operand after an option", args: []string{"--all", "x"}},
		{name: "operand before an option is permuted out", args: []string{"x", "--all"}},

		// getopt faults. nproc declares no short option at all, so
		// every short byte is invalid.
		{name: "invalid short option", args: []string{"-a"}},
		{name: "invalid short option n", args: []string{"-n"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "value on an option that takes none", args: []string{"--all=1"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad number before a bad option", args: []string{"--ignore=x", "--foo"}},
		{name: "bad option before a bad number", args: []string{"--foo", "--ignore=x"}},

		// POSIXLY_CORRECT stops the scan at the first operand.
		{name: "posixly correct stops at an operand", args: []string{"x", "--all"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths: one write, one strerror.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{"x"}, stdout: stdoutClosed},
		{name: "stdout closed on a bad number", args: []string{"--ignore=x"}, stdout: stdoutClosed},
	}
}

func TestNprocParity(t *testing.T) {
	requireParity(t, "nproc", nprocCases(t))
}

func TestNprocHelpVersion(t *testing.T) {
	requireHelp(t, "nproc", []string{"--help"}, 0)
	requireHelp(t, "nproc", []string{"--hel"}, 0)
	requireHelp(t, "nproc", []string{"--help", "x"}, 0)
	requireHelp(t, "nproc", []string{"x", "--help"}, 0)
	requireVersion(t, "nproc", []string{"--version"}, 0)
	requireVersion(t, "nproc", []string{"--vers"}, 0)
	requireVersion(t, "nproc", []string{"--version", "x"}, 0)
}
