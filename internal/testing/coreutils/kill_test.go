package coreutils

import "testing"

func init() {
	registerCorpus("kill", killCases)
}

func killCases(t *testing.T) []invocation {
	return []invocation{
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

		{name: "signal by name", args: []string{"-s", "TERM", "999999"}},
		{name: "signal by number", args: []string{"-s", "9", "999999"}},
		{name: "signal lower case", args: []string{"-s", "term", "999999"}},
		{name: "signal with the SIG prefix", args: []string{"-s", "SIGTERM", "999999"}},
		{name: "signal glued to the letter", args: []string{"-sTERM", "999999"}},
		{name: "undocumented n names a signal", args: []string{"-n", "9", "999999"}},
		{name: "long signal spelling", args: []string{"--signal=INT", "999999"}},
		{name: "abbreviated long signal", args: []string{"--sig=INT", "999999"}},
		{name: "invalid signal by name", args: []string{"-s", "NOPE", "999999"}},
		{name: "non-ASCII signal prefix", args: []string{"-l", "éTERM"}},
		{name: "non-ASCII signal infix", args: []string{"-l", "SIGéTERM"}},
		{name: "non-ASCII signal suffix", args: []string{"-l", "TERMé"}},
		{name: "invalid signal by number", args: []string{"-s", "99", "999999"}},
		{name: "invalid signal through n", args: []string{"-n", "NOPE", "999999"}},
		{name: "signal zero through s", args: []string{"-s", "0", "999999"}},
		{name: "a realtime signal with an offset", args: []string{"-s", "RTMIN+3", "999999"}},

		{name: "bare numeric signal", args: []string{"-0", "999999"}},
		{name: "bare named signal", args: []string{"-HUP", "999999"}},
		{name: "bare named signal with prefix", args: []string{"-SIGHUP", "999999"}},
		{name: "bare uppercase that names nothing", args: []string{"-NOPE", "999999"}},
		{name: "bare single uppercase that names nothing", args: []string{"-Z", "999999"}},
		{name: "bare truncated name", args: []string{"-HU", "999999"}},
		{name: "bare SIG alone", args: []string{"-SIG", "999999"}},
		{name: "digit led but not a number", args: []string{"-9x", "999999"}},

		{name: "later numeric is a pid not a signal", args: []string{"-0", "-999998", "999999"}},
		{name: "numeric signal then numeric pid", args: []string{"-0", "-0", "999999"}},
		{name: "named signal then numeric pid", args: []string{"-INT", "-999998"}},

		{name: "two bare signals", args: []string{"-HUP", "-INT", "999999"}},
		{name: "bare then explicit signal", args: []string{"-0", "-s", "TERM", "999999"}},
		{name: "two signals quotes the second as given", args: []string{"-HUP", "-SIGINT", "999999"}},
		{name: "alphabetic signal after an operand", args: []string{"-0", "999999", "-HUP"}},
		{name: "signal cannot combine with list", args: []string{"-0", "-l"}},
		{name: "signal cannot combine with table", args: []string{"-0", "-t"}},
		{name: "list then table", args: []string{"-l", "-t"}},
		{name: "table then list", args: []string{"-t", "-l"}},

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

		{name: "the whole table", args: []string{"-t"}},
		{name: "table long spelling", args: []string{"--table"}},
		{name: "undocumented L is the table", args: []string{"-L"}},
		{name: "table filtered to two signals", args: []string{"-t", "9", "15"}},
		{name: "table by name", args: []string{"-t", "KILL"}},
		{name: "table of an invalid signal", args: []string{"-t", "99"}},

		{name: "the mask at 255 and above is 0xFF not 0x7F", args: []string{"-l", "393"}},
		{name: "a masked number with no signal", args: []string{"-l", "384"}},
		{name: "255 masks to itself", args: []string{"-l", "255"}},
		{name: "a number has no upper bound", args: []string{"-l", "2000000000"}},
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
		{name: "a wait status is not masked behind SIG", args: []string{"-l", "SIG137"}},
		{name: "the historical name for ABRT", args: []string{"-l", "IOT"}},
		{name: "a name whose platform status differs", args: []string{"-l", "POLL"}},
		{name: "a System V alias Darwin lacks", args: []string{"-l", "CLD"}},
		{name: "a name is case insensitive", args: []string{"-l", "int"}},
		{name: "a name in mixed case", args: []string{"-l", "iNt"}},
		{name: "the far end of the realtime range", args: []string{"-l", "RTMAX-30"}},
		{name: "past the far end", args: []string{"-l", "RTMAX-31"}},
		{name: "past the near end", args: []string{"-l", "RTMIN+31"}},
		{name: "a realtime spelling behind SIG", args: []string{"-l", "SIGRTMIN+3"}},
		{name: "the table prints a row for zero", args: []string{"-t", "0"}},
		{name: "the table masks too", args: []string{"-t", "137"}},
		{name: "the table of a masked number with no signal", args: []string{"-t", "384"}},
		{name: "the table by the name of zero", args: []string{"-t", "EXIT"}},
		{name: "the table behind a SIG prefix", args: []string{"-t", "SIG9"}},
		{name: "zero among several table rows", args: []string{"-t", "0", "9"}},
		{name: "zero among several conversions", args: []string{"-l", "0", "9", "EXIT"}},
		{name: "the undocumented table twin on zero", args: []string{"-L", "0"}},
		{name: "a wait status as an explicit signal", args: []string{"-s", "137", "999999"}},
		{name: "EXIT as an explicit signal", args: []string{"-s", "EXIT", "999999"}},
		{name: "a number masking to zero as a signal", args: []string{"-s", "256", "999999"}},
		{name: "SIG and a number as a signal", args: []string{"-s", "SIG9", "999999"}},
		{name: "an explicit signal that masks to nothing", args: []string{"-s", "384", "999999"}},
		{name: "zero as an explicit signal", args: []string{"-s", "0", "999999"}},
		{name: "a wait status in first position", args: []string{"-137", "999999"}},
		{name: "EXIT as a bare signal", args: []string{"-EXIT", "999999"}},

		{name: "lone dash", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "not a number", args: []string{"abc"}},
		{name: "trailing junk on a number", args: []string{"12x"}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand after dashdash", args: []string{"--", "999999"}},
		{name: "a nonexistent pid", args: []string{"999999"}},
		{name: "two nonexistent pids", args: []string{"999999", "999998"}},

		{name: "signal zero to our own group", args: []string{"-0", "0"}},
	}
}

func TestKill(t *testing.T) {
	requireParity(t, "kill", killCases(t))
}

func TestKillHelpVersion(t *testing.T) {
	requireHelp(t, "kill", []string{"--help"}, 0)
	requireHelp(t, "kill", []string{"--hel"}, 0)
	requireHelp(t, "kill", []string{"--help", "x"}, 0)
	requireHelp(t, "kill", []string{"-l", "--help"}, 0)
	requireVersion(t, "kill", []string{"--version"}, 0)
	requireVersion(t, "kill", []string{"--vers"}, 0)
	requireVersion(t, "kill", []string{"--version", "x"}, 0)
}
