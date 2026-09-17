package coreutils

import "testing"

func init() {
	registerCorpus("kill", killCases)
}

// kill(1) sends signals, or lists what the signals are. Almost everything
// interesting is in the argument grammar, because a signal is spelled as an
// option, and the shapes GNU accepts there are not what getopt would do on
// its own.
//
// NOTHING HERE MAY DELIVER A REAL SIGNAL. Two ways to get that wrong, both
// avoided deliberately:
//
//   - an invocation with no signal defaults to TERM, so `kill 0` would TERM
//     the test runner's own process group and `kill -1` every process the
//     runner may signal. Neither appears below.
//   - a NEGATIVE pid is a process GROUP, so `-2` names group 2, which very
//     likely exists. Where a case needs a negative pid to pin the grammar it
//     uses -999998, a group number nothing owns.
//
// So every pid here is either nonexistent (999999, -999998) or paired with
// SIGNAL 0, which checks permission and delivers nothing. The one success
// case is `-0 0`: signal 0 to the caller's own group, which both sides answer
// silently with status 0.
//
// The quirks these cases exist for, all measured against GNU 9.4:
//
//   - an ALPHABETIC -NAME is a signal ANYWHERE, even after an operand;
//     a NUMERIC -N is a signal only as argv[1] and a PID after that.
//   - the bare-signal shape test is CASE, not validity: -Z and -NOPE answer
//     `'X': invalid signal`, -z and -ab answer `invalid option -- 'z'`.
//   - a NUMBER IS MASKED, because it may be a wait status: `-l 137` is KILL.
//     The mask is TWO masks (`& 0xFF` at 255 and above, `& 0x7F` below), so
//     `-l 393` is nothing at all where one mod-128 would call it KILL.
//   - `EXIT` is a NAME for signal 0, and numbers have no upper bound.
//   - an invalid -l argument does not stop the rest.
//   - the `Try …` line is not decided by the message text: -s NOPE and -l 99
//     print the same `'X': invalid signal` and only the first carries it.
//   - -n is an undocumented synonym for -s, -L for -t.
//
// ONE SHAPE IS HELD OUT, not dodged: `kill 999999 -999998`, where GNU reports
// only the second operand and we report both. GNU's own src/kill.c explains
// the mechanism — digits are declared short options, and the digit arm reads
//
//	if (optind != 2) { /* This option is actually a process-id.  */
//	                   optind--; goto no_more_options; }
//
// so "a numeric is a signal only in first position" is literally a test on
// optind. Whether optind is 2 when a non-option PRECEDES the digit depends on
// how glibc permutes argv, which cannot be settled by reading either source,
// and guessing it would encode the wrong rule for every other caller of this
// shape. Every case above has an operand-free or signal-first argv, so the
// grammar is otherwise pinned; this one wants an instrumented GNU run.
func killCases(t *testing.T) []invocation {
	return []invocation{
		// ---- usage faults ----
		{name: "no arguments"},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "invalid short option", args: []string{"-z"}},
		{name: "invalid short cluster", args: []string{"-ab"}},
		{name: "lowercase cluster is an option not a signal", args: []string{"-zz"}},
		{name: "signal option with no argument", args: []string{"-s"}},
		{name: "undocumented n with no argument", args: []string{"-n"}},
		{name: "long signal with no argument", args: []string{"--signal"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "help with a value", args: []string{"--help=x"}},

		// ---- the signal named by -s / -n / --signal ----
		{name: "signal by name", args: []string{"-s", "TERM", "999999"}},
		{name: "signal by number", args: []string{"-s", "9", "999999"}},
		{name: "signal lower case", args: []string{"-s", "term", "999999"}},
		{name: "signal with the SIG prefix", args: []string{"-s", "SIGTERM", "999999"}},
		{name: "signal glued to the letter", args: []string{"-sTERM", "999999"}},
		{name: "undocumented n names a signal", args: []string{"-n", "9", "999999"}},
		{name: "long signal spelling", args: []string{"--signal=INT", "999999"}},
		{name: "abbreviated long signal", args: []string{"--sig=INT", "999999"}},
		{name: "invalid signal by name", args: []string{"-s", "NOPE", "999999"}},
		{name: "invalid signal by number", args: []string{"-s", "99", "999999"}},
		{name: "invalid signal through n", args: []string{"-n", "NOPE", "999999"}},
		{name: "signal zero through s", args: []string{"-s", "0", "999999"}},
		{name: "a realtime signal with an offset", args: []string{"-s", "RTMIN+3", "999999"}},

		// ---- the bare -SIGNAL form ----
		{name: "bare numeric signal", args: []string{"-0", "999999"}},
		{name: "bare named signal", args: []string{"-HUP", "999999"}},
		{name: "bare named signal with prefix", args: []string{"-SIGHUP", "999999"}},
		{name: "bare uppercase that names nothing", args: []string{"-NOPE", "999999"}},
		{name: "bare single uppercase that names nothing", args: []string{"-Z", "999999"}},
		{name: "bare truncated name", args: []string{"-HU", "999999"}},
		{name: "bare SIG alone", args: []string{"-SIG", "999999"}},
		{name: "digit led but not a number", args: []string{"-9x", "999999"}},

		// A numeric -N is the signal in first position and a PID after it.
		// -999998 is a process group nothing owns, and signal 0 delivers
		// nothing regardless.
		{name: "later numeric is a pid not a signal", args: []string{"-0", "-999998", "999999"}},
		{name: "numeric signal then numeric pid", args: []string{"-0", "-0", "999999"}},
		{name: "named signal then numeric pid", args: []string{"-INT", "-999998"}},

		// ---- two signals, and signal against a listing mode ----
		{name: "two bare signals", args: []string{"-HUP", "-INT", "999999"}},
		{name: "bare then explicit signal", args: []string{"-0", "-s", "TERM", "999999"}},
		{name: "two signals quotes the second as given", args: []string{"-HUP", "-SIGINT", "999999"}},
		{name: "alphabetic signal after an operand", args: []string{"-0", "999999", "-HUP"}},
		{name: "signal cannot combine with list", args: []string{"-0", "-l"}},
		{name: "signal cannot combine with table", args: []string{"-0", "-t"}},
		{name: "list then table", args: []string{"-l", "-t"}},
		{name: "table then list", args: []string{"-t", "-l"}},

		// ---- -l, bare and converting ----
		{name: "list every signal", args: []string{"-l"}},
		{name: "list long spelling", args: []string{"--list"}},
		{name: "list abbreviated", args: []string{"--li"}},
		{name: "list shortest abbreviation", args: []string{"--l"}},
		{name: "number to name", args: []string{"-l", "9"}},
		{name: "name to number", args: []string{"-l", "KILL"}},
		{name: "several conversions", args: []string{"-l", "9", "15", "1"}},
		{name: "names to numbers", args: []string{"-l", "HUP", "INT"}},
		{name: "zero prints EXIT", args: []string{"-l", "0"}},
		{name: "a wait status is masked with 127", args: []string{"-l", "137"}},
		{name: "another wait status", args: []string{"-l", "143"}},
		{name: "128 masks to zero", args: []string{"-l", "128"}},
		{name: "256 masks to zero", args: []string{"-l", "256"}},
		{name: "top of the realtime range", args: []string{"-l", "64"}},
		{name: "past the realtime range", args: []string{"-l", "65"}},
		{name: "a realtime spelling", args: []string{"-l", "RTMIN+3"}},
		{name: "list an invalid number", args: []string{"-l", "99"}},
		{name: "list an invalid name", args: []string{"-l", "x"}},
		{name: "an invalid argument does not stop the rest", args: []string{"-l", "1", "999"}},
		{name: "list the numbers between the ranges", args: []string{"-l", "32"}},
		{name: "list the other gap", args: []string{"-l", "33"}},

		// ---- -t and its undocumented twin -L ----
		{name: "the whole table", args: []string{"-t"}},
		{name: "table long spelling", args: []string{"--table"}},
		{name: "undocumented L is the table", args: []string{"-L"}},
		{name: "table filtered to two signals", args: []string{"-t", "9", "15"}},
		{name: "table by name", args: []string{"-t", "KILL"}},
		{name: "table of an invalid signal", args: []string{"-t", "99"}},

		// ---- the shared operand2sig grammar (#9652) ----
		//
		// GNU parses every signal spelling — here, in env and in timeout —
		// with one `operand2sig`, and these are the parts of it that no
		// option's documentation implies. Each of the four ways kill spells a
		// signal is covered, because the bug being pinned was three of them
		// calling a name-only lookup instead.
		{name: "the mask at 255 and above is 0xFF not 0x7F", args: []string{"-l", "393"}},
		{name: "a masked number with no signal", args: []string{"-l", "384"}},
		{name: "255 masks to itself", args: []string{"-l", "255"}},
		{name: "a number has no upper bound", args: []string{"-l", "2000000000"}},
		// 1000000 masks to RTMAX, which is the exact value the cap this
		// replaced refused at.
		{name: "a million is a signal", args: []string{"-l", "1000000"}},
		{name: "a large number masking to a real signal", args: []string{"-l", "1048585"}},
		{name: "past the int GNU parses into", args: []string{"-l", "2147483648"}},
		{name: "past 32 bits", args: []string{"-l", "4294967296"}},
		{name: "a leading zero is not octal", args: []string{"-l", "09"}},
		{name: "hex is not a number", args: []string{"-l", "0x9"}},
		{name: "a signed number is not a number", args: []string{"-l", "+9"}},
		{name: "EXIT names signal zero", args: []string{"-l", "EXIT"}},
		{name: "EXIT lower case", args: []string{"-l", "exit"}},
		{name: "EXIT with the SIG prefix", args: []string{"-l", "SIGEXIT"}},
		{name: "SIG0 is signal zero", args: []string{"-l", "SIG0"}},
		{name: "the SIG prefix on a number", args: []string{"-l", "SIG9"}},
		{name: "SIG on the top of the range", args: []string{"-l", "SIG64"}},
		{name: "SIG past the range", args: []string{"-l", "SIG65"}},
		// The masked/unmasked asymmetry: the mask lives in operand2sig's
		// DIGIT arm, which a SIG prefix means we never enter.
		{name: "a wait status is not masked behind SIG", args: []string{"-l", "SIG137"}},
		{name: "a name is case insensitive", args: []string{"-l", "int"}},
		{name: "a name in mixed case", args: []string{"-l", "iNt"}},
		{name: "the far end of the realtime range", args: []string{"-l", "RTMAX-30"}},
		{name: "past the far end", args: []string{"-l", "RTMAX-31"}},
		{name: "past the near end", args: []string{"-l", "RTMIN+31"}},
		{name: "a realtime spelling behind SIG", args: []string{"-l", "SIGRTMIN+3"}},
		{name: "the table prints a row for zero", args: []string{"-t", "0"}},
		{name: "the table masks too", args: []string{"-t", "137"}},
		{name: "the table of a masked number with no signal", args: []string{"-t", "384"}},
		// The send paths. Every pid here is 999999, which does not exist, so
		// the KILL that 137 and SIG9 resolve to is delivered to nothing.
		{name: "a wait status as an explicit signal", args: []string{"-s", "137", "999999"}},
		{name: "EXIT as an explicit signal", args: []string{"-s", "EXIT", "999999"}},
		{name: "a number masking to zero as a signal", args: []string{"-s", "256", "999999"}},
		{name: "SIG and a number as a signal", args: []string{"-s", "SIG9", "999999"}},
		{name: "an explicit signal that masks to nothing", args: []string{"-s", "384", "999999"}},
		{name: "zero as an explicit signal", args: []string{"-s", "0", "999999"}},
		{name: "a wait status in first position", args: []string{"-137", "999999"}},
		{name: "EXIT as a bare signal", args: []string{"-EXIT", "999999"}},

		// ---- operands that are not pids ----
		{name: "lone dash", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "not a number", args: []string{"abc"}},
		{name: "trailing junk on a number", args: []string{"12x"}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand after dashdash", args: []string{"--", "999999"}},
		{name: "a nonexistent pid", args: []string{"999999"}},
		{name: "two nonexistent pids", args: []string{"999999", "999998"}},

		// The one case that succeeds: signal 0 to the caller's own process
		// group. Checks permission, delivers nothing, and both sides answer
		// silently with status 0.
		{name: "signal zero to our own group", args: []string{"-0", "0"}},
	}
}

func TestKill(t *testing.T) {
	requireParity(t, "kill", killCases(t))
}

// `--help` and `--version` carry our own text by design (docs/COREUTILS.md);
// everything else about them still matches, including that `--help` wins over
// a later usage error and loses to an earlier one, and that a listing mode
// does not suppress it.
func TestKillHelpVersion(t *testing.T) {
	requireHelp(t, "kill", []string{"--help"}, 0)
	requireHelp(t, "kill", []string{"--hel"}, 0)
	requireHelp(t, "kill", []string{"--help", "x"}, 0)
	requireHelp(t, "kill", []string{"-l", "--help"}, 0)
	requireVersion(t, "kill", []string{"--version"}, 0)
	requireVersion(t, "kill", []string{"--vers"}, 0)
	requireVersion(t, "kill", []string{"--version", "x"}, 0)
}
