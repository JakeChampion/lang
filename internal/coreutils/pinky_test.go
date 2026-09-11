package coreutils

import "testing"

func init() {
	registerCorpus("pinky", pinkyCases)
}

// pinky(1) — the accounting database joined to the passwd database, in
// a short format per SESSION and a long one per USER.
//
// This is the one utility of the three whose database the corpus cannot
// choose. `users` and `who` take it as an operand, so their cases write
// a file and hand both sides the path; pinky takes USER operands and
// reads /var/run/utmp and nothing else. What that costs, exactly:
//
//   - The SHORT format's ROWS are whatever the build machine has. On a
//     container there is no /var/run/utmp at all and every case below
//     prints its heading and stops; on a machine with logins both sides
//     read the same file at the same moment and still agree, which is
//     what the corpus asserts either way. What is NOT asserted there is
//     that a row is ever produced, so the row layout — the real-name
//     column, the message-status prefix, the four idle spellings — is
//     covered by the implementation and by nothing here. It was checked
//     against the reference the only way it can be: by installing a
//     fixture database at /var/run/utmp, comparing, and removing it,
//     which a test may not do to the machine it runs on.
//   - The one row-shaped thing the corpus DOES pin is the filter. A
//     USER operand that names nobody logged in prints the heading and no
//     rows whatever the machine's state, so every option's heading is
//     compared under an operand as well as without one.
//
// The LONG format has no such gap: it reads /etc/passwd and the user's
// own project and plan files, so `-l root` is the same question on both
// sides and every field of it is in the corpus.
//
// The other thing to know about pinky is that it does NOT trim the
// trailing blanks off a login name where `users` and `who -q` do — it
// looks the name up, matches an operand and prints the column under the
// field as it stands. That is invisible here for the same reason the
// rows are, and it is why the implementation keeps the raw field.
func pinkyCases(t *testing.T) []invocation {
	cases := []invocation{
		{name: "no arguments"},
		{name: "dashdash alone", args: []string{"--"}},
	}

	// Every short-format option, with and without an operand that
	// matches no session: the heading each one prints is the part that
	// does not depend on the machine.
	for _, opt := range []string{"-s", "-f", "-w", "-i", "-q", "-b", "-h", "-p"} {
		cases = append(cases,
			invocation{name: "option " + opt, args: []string{opt}},
			invocation{name: "option " + opt + " filtered to nobody logged in", args: []string{opt, "fernnosuchuser"}},
		)
	}

	cases = append(cases,
		// The short format's switches drop a suffix of the same list, so
		// the combinations that matter are the ones where a later option
		// would put a column back.
		invocation{name: "-w then -q", args: []string{"-wq"}},
		invocation{name: "-q then -w", args: []string{"-qw"}},
		invocation{name: "-i then -q", args: []string{"-iq"}},
		invocation{name: "-q then -i", args: []string{"-qi"}},
		invocation{name: "-w with no heading", args: []string{"-wf"}},
		invocation{name: "-q with no heading", args: []string{"-fq"}},
		invocation{name: "every short-format option at once", args: []string{"-sfwiq"}},
		invocation{name: "the long-format options in short format", args: []string{"-bhp"}},

		// An operand filters the sessions; one that names nobody leaves
		// the heading alone on the output.
		invocation{name: "an operand nobody is logged in under", args: []string{"fernnosuchuser"}},
		invocation{name: "two such operands", args: []string{"fernnosuchuser", "fernother"}},
		invocation{name: "an empty operand", args: []string{""}},
		invocation{name: "an operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		invocation{name: "lone dash is an operand", args: []string{"-"}},
		invocation{name: "operand after dashdash", args: []string{"--", "fernnosuchuser"}},
		invocation{name: "option-looking operand after dashdash", args: []string{"--", "-l"}},
		invocation{name: "a name with a trailing blank", args: []string{"root "}},

		// The long format, which is the passwd database and two files
		// under the home directory it names.
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
		// -s and -l are the one pair where the last one given wins.
		invocation{name: "-s after -l is the short format", args: []string{"-l", "-s", "root"}},
		invocation{name: "-l after -s is the long format", args: []string{"-s", "-l", "root"}},
		invocation{name: "-l with a dash operand", args: []string{"-l", "-"}},
		invocation{name: "-l after dashdash is an operand", args: []string{"--", "-l"}},

		// The long format is the one usage error of pinky's own.
		invocation{name: "long format with no operand", args: []string{"-l"}},
		invocation{name: "long format with no operand after -s", args: []string{"-s", "-l"}},
		invocation{name: "long format with no operand and a heading off", args: []string{"-lf"}},
		invocation{name: "long format with only dashdash", args: []string{"-l", "--"}},

		// The TIME column is local time.
		invocation{name: "TZ empty is UTC", args: []string{"fernnosuchuser"}, env: []string{"TZ="}},
		invocation{name: "TZ names a zone", args: []string{"fernnosuchuser"}, env: []string{"TZ=America/New_York"}},
		invocation{name: "TZ is a rule string", args: []string{"fernnosuchuser"}, env: []string{"TZ=EST5EDT,M3.2.0,M11.1.0"}},
		invocation{name: "TZ names nothing", args: []string{"fernnosuchuser"}, env: []string{"TZ=Bogus"}},

		// getopt faults. pinky declares no long option of its own, so
		// every `--` form is either the standard pair or unrecognized.
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

		// The write-failure paths.
		invocation{name: "stdout closed", args: []string{"fernnosuchuser"}, stdout: stdoutClosed},
		invocation{name: "stdout full", args: []string{"fernnosuchuser"}, stdout: stdoutFull},
		invocation{name: "stdout closed in the long format", args: []string{"-l", "root"}, stdout: stdoutClosed},
		invocation{name: "stdout full in the long format", args: []string{"-l", "root"}, stdout: stdoutFull},
		invocation{name: "stdout closed with no heading", args: []string{"-f", "fernnosuchuser"}, stdout: stdoutClosed},
		invocation{name: "stdout closed on a fault", args: []string{"-l"}, stdout: stdoutClosed},
	)
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
