package coreutils

import (
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("users", usersCases)
}

func usersCases(t *testing.T) []invocation {
	dir := t.TempDir()
	mixed := utmpFile(t, dir, "mixed", utmpMixed()...)
	one := utmpFile(t, dir, "one", utmpRec{typ: utUserProcess, pid: 1, line: "pts/0", user: "alice", host: "10.0.0.5", sec: utmpWhen})
	empty := utmpRaw(t, dir, "empty", nil)

	unsorted := utmpFile(t, dir, "unsorted",
		utmpRec{typ: utUserProcess, pid: 2, line: "pts/0", user: "zoe", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 3, line: "pts/1", user: "adam", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 4, line: "pts/2", user: "bob", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 5, line: "pts/3", user: "adam", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 6, line: "pts/4", user: "Carl", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 7, line: "pts/5", user: "9nine", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 8, line: "pts/6", user: "_under", sec: utmpWhen},
	)

	noName := utmpFile(t, dir, "noname",
		utmpRec{typ: utUserProcess, pid: 9, line: "pts/2", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 10, line: "pts/3", user: "zed", sec: utmpWhen},
	)

	widths := utmpFile(t, dir, "widths",
		utmpRec{typ: utUserProcess, pid: 11, line: strings.Repeat("p", 32), user: strings.Repeat("u", 32), host: strings.Repeat("h", 256), sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 12, line: "pts/7", user: "pad   ", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 13, line: "pts/8", user: "  lead", sec: utmpWhen},
	)

	raw := utmpFile(t, dir, "raw",
		utmpRec{typ: utUserProcess, pid: 14, line: "pts/\xff", user: "a\xffb", host: "h\xfe", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 15, line: "pts/5", user: "   ", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 16, line: "pts/6", user: "a\tb", sec: utmpWhen},
	)

	truncated := utmpRaw(t, dir, "truncated", append(utmpDB(utmpMixed()...), "\x07\x00\x00\x00abc"...))
	stub := utmpRaw(t, dir, "stub", []byte{7, 0})
	text := utmpRaw(t, dir, "text", []byte(strings.Repeat("not a utmp file\n", utmpRecordSize/16)))

	missing := filepath.Join(dir, "nosuch")

	return []invocation{
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

		{name: "a missing database", args: []string{missing}},
		{name: "a directory", args: []string{"/etc"}},
		{name: "the default database", args: nil},
		{name: "dashdash alone reads the default", args: []string{"--"}},
		{name: "lone dash is a file name", args: []string{"-"}},
		{name: "empty operand", args: []string{""}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand after dashdash", args: []string{"--", mixed}},
		{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},

		{name: "two operands", args: []string{mixed, "extra"}},
		{name: "three operands report the second", args: []string{mixed, "extra", "more"}},
		{name: "two operands where the first is missing", args: []string{missing, "extra"}},
		{name: "empty second operand", args: []string{mixed, ""}},

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

		{name: "stdout closed", args: []string{mixed}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{mixed}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout full with nothing to write", args: []string{empty}, stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{mixed, "extra"}, stdout: stdoutClosed},
	}
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
