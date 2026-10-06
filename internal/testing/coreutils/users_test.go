package coreutils

import (
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("users", usersCases)
}

// users(1) — the login names in the accounting database, sorted and
// space separated on one line.
//
// The whole utility is a filter and a sort, and the corpus is mostly
// about what does NOT reach the line: every record type that is not a
// user process, a user process whose name was cleared, a partial record
// at the end of the file, and a file that is not a database at all.
// Each one is a fixture built by utmp_test.go and handed to both sides
// as the FILE operand, so the comparison is byte-exact rather than a
// report about whoever happens to be logged into the build machine.
//
// One rule the sort makes visible: it is strcmp over the raw bytes, so
// in the C locale digits come before capitals before lowercase, and
// `9nine Carl adam` is sorted output rather than unsorted.
//
// The error handling is the other half, and there is almost none of it:
// a database that is missing, unreadable or a directory prints nothing
// and exits 0. That is not this implementation being lenient — the
// cases below hold GNU to it too.
func usersCases(t *testing.T) []invocation {
	dir := t.TempDir()
	mixed := utmpFile(t, dir, "mixed", utmpMixed()...)
	one := utmpFile(t, dir, "one", utmpRec{typ: utUserProcess, pid: 1, line: "pts/0", user: "alice", host: "10.0.0.5", sec: utmpWhen})
	empty := utmpRaw(t, dir, "empty", nil)

	// Sort order: unsorted input, a repeated name, and the three ASCII
	// classes whose order is what the C locale fixes.
	unsorted := utmpFile(t, dir, "unsorted",
		utmpRec{typ: utUserProcess, pid: 2, line: "pts/0", user: "zoe", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 3, line: "pts/1", user: "adam", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 4, line: "pts/2", user: "bob", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 5, line: "pts/3", user: "adam", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 6, line: "pts/4", user: "Carl", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 7, line: "pts/5", user: "9nine", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 8, line: "pts/6", user: "_under", sec: utmpWhen},
	)

	// A user process with an empty name is not a login and contributes
	// nothing — the half of the rule that is easy to miss, since the
	// record's TYPE says it is one.
	noName := utmpFile(t, dir, "noname",
		utmpRec{typ: utUserProcess, pid: 9, line: "pts/2", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 10, line: "pts/3", user: "zed", sec: utmpWhen},
	)

	// A name that fills its 32-byte field has no terminator, and one
	// padded with blanks rather than NULs is trimmed back.
	widths := utmpFile(t, dir, "widths",
		utmpRec{typ: utUserProcess, pid: 11, line: strings.Repeat("p", 32), user: strings.Repeat("u", 32), host: strings.Repeat("h", 256), sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 12, line: "pts/7", user: "pad   ", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 13, line: "pts/8", user: "  lead", sec: utmpWhen},
	)

	// Names that are not text: a high byte in the middle, and a name
	// that is only blanks, which trims to nothing and still counts as a
	// login.
	raw := utmpFile(t, dir, "raw",
		utmpRec{typ: utUserProcess, pid: 14, line: "pts/\xff", user: "a\xffb", host: "h\xfe", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 15, line: "pts/5", user: "   ", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 16, line: "pts/6", user: "a\tb", sec: utmpWhen},
	)

	// Trailing bytes that are not a whole record, and a file far too
	// short to hold one.
	truncated := utmpRaw(t, dir, "truncated", append(append([]byte{}, oneRecordBytes(utmpMixed())...), "\x07\x00\x00\x00abc"...))
	stub := utmpRaw(t, dir, "stub", []byte{7, 0})
	// A file whose length is a multiple of the record size but whose
	// content is text: every "record" has a type no utility prints.
	text := utmpRaw(t, dir, "text", []byte(strings.Repeat("not a utmp file\n", utmpRecordSize/16)))

	missing := filepath.Join(dir, "nosuch")

	return []invocation{
		// The database, read every way it can be shaped.
		{name: "a database with every record type", args: []string{mixed}},
		{name: "one login", args: []string{one}},
		{name: "an empty database", args: []string{empty}},
		{name: "names are sorted by strcmp", args: []string{unsorted}},
		{name: "a user process with no name is not a login", args: []string{noName}},
		{name: "names at the width of the field", args: []string{widths}},
		{name: "names that are not valid UTF-8", args: []string{raw}},
		{name: "a partial record at the end is ignored", args: []string{truncated}},
		{name: "a file too short to hold a record", args: []string{stub}},
		{name: "a file of text the size of a record", args: []string{text}},

		// No database, and the three ways a path fails to be one. None
		// of them is an error.
		{name: "a missing database", args: []string{missing}},
		{name: "a directory", args: []string{"/etc"}},
		{name: "the default database", args: nil},
		{name: "dashdash alone reads the default", args: []string{"--"}},
		{name: "lone dash is a file name", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand after dashdash", args: []string{"--", mixed}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

		// A second operand is the only usage error the utility has of
		// its own, and it names the SECOND one.
		{name: "two operands", args: []string{mixed, "extra"}},
		{name: "three operands report the second", args: []string{mixed, "extra", "more"}},
		{name: "two operands where the first is missing", args: []string{missing, "extra"}},
		{name: "empty second operand", args: []string{mixed, ""}},

		// getopt faults.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short option cluster", args: []string{"-xy"}},
		{name: "short option who has", args: []string{"-q"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before a bad option", args: []string{mixed, "--foo"}},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{mixed, "--help"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths: one write, one strerror. An empty
		// database writes nothing at all, so a closed stdout is not an
		// error there — which is the distinction close_stdout makes.
		{name: "stdout closed", args: []string{mixed}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{mixed}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout full with nothing to write", args: []string{empty}, stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{mixed, "extra"}, stdout: stdoutClosed},
	}
}

// oneRecordBytes is the fixture's records as the bytes of a database,
// for the cases that append something that is not a record.
func oneRecordBytes(recs []utmpRec) []byte {
	var out []byte
	for _, r := range recs {
		out = append(out, r.bytes()...)
	}
	return out
}

func TestUsersParity(t *testing.T) {
	requireParity(t, "users", usersCases(t))
}

func TestUsersHelpVersion(t *testing.T) {
	requireHelp(t, "users", []string{"--help"}, 0)
	requireHelp(t, "users", []string{"--hel"}, 0)
	requireHelp(t, "users", []string{"--help", "x"}, 0)
	requireHelp(t, "users", []string{"x", "--help"}, 0)
	requireVersion(t, "users", []string{"--version"}, 0)
	requireVersion(t, "users", []string{"--vers"}, 0)
	requireVersion(t, "users", []string{"--version", "x"}, 0)
}
