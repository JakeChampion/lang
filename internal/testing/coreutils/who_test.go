package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	registerCorpus("who", whoCases)
}

// shortTempDir is a working directory whose PATH fits in a utmp record's
// 32-byte ut_line field — `/tmp/wu1234567890` and a one-letter name is
// eighteen bytes, where `t.TempDir()` alone is already past thirty.
//
// It exists for one case that is worth the trouble: who reads the
// message status and the idle time off `stat(/dev/LINE)`, and a ut_line
// that is an ABSOLUTE path is used as it stands rather than under /dev.
// That is the only way a test can choose what those two columns say —
// otherwise they are whatever the build machine's terminals happen to
// be, which on a container is `?` for every row and leaves the three
// idle spellings and both message states unexercised.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wu")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// whoTerminal creates a stand-in for a terminal device: a file whose
// mode is the message status and whose atime is the last read.
func whoTerminal(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, nil, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// whoCases is who(1)'s corpus.
//
// The database is a fixture handed to both sides as the FILE operand
// (utmp_test.go says why), so every record shape and every column is
// compared byte for byte rather than reported about whoever is logged
// into the build machine.
//
// What the corpus has to pin, beyond the option surface:
//
//   - The row is ONE format with pieces left out. NAME is at least 8
//     wide and LINE at least 12, and a longer value pushes the rest of
//     its own row right without moving any other row — so the fixtures
//     carry a 32-byte name and a 32-byte line to prove the columns are
//     per-row minimum widths rather than a table.
//   - -s is not a last-one-wins flag. `-s -u` and `-u -s` are both the
//     short row; `-s -d` and `-d -s` are both the long one, because the
//     exit column carries the idle and pid columns with it.
//   - The two columns that come off the TERMINAL rather than the record:
//     the message status (+ / - / ?) and the idle time (`.`, HH:MM,
//     `old`, `?`). Both are reachable only through the absolute-ut_line
//     fixture, whose atimes `prepare` resets before each side so the
//     elapsed time is the same for both runs.
//   - The TIME column is local time, so TZ decides it: a named zone, a
//     POSIX rule string, a rule string with no file behind it, an
//     invalid name, and the empty value are each a case.
//
// Two things are deliberately absent, and both would be cases if they
// were deterministic:
//
//   - `--lookup` over a name that /etc/hosts does not answer. It sends
//     both sides to DNS, where the answer depends on the network and on
//     whether the two runs get the same one; the names below are the
//     ones the files backend settles, plus numeric addresses, which
//     getaddrinfo answers without a lookup at all.
//   - A TZ string that names a daylight zone but no usable pair of
//     dates (`TZ=ABC1DEF`, `TZ=EST5EDT,J0,J300`). glibc answers those
//     from /usr/share/zoneinfo/posixrules, a legacy file upstream
//     tzdata no longer ships; lib/tz.fern applies the POSIX default
//     dates instead. The two agree over the range those dates cover and
//     differ outside it — at the epoch boundary and past 2037.
func whoCases(t *testing.T) []invocation {
	dir := t.TempDir()
	mixed := utmpFile(t, dir, "mixed", utmpMixed()...)
	empty := utmpRaw(t, dir, "empty", nil)
	one := utmpFile(t, dir, "one", utmpRec{typ: utUserProcess, pid: 1001, line: "pts/0", id: "ts/0", user: "alice", host: "10.0.0.5", sec: utmpWhen})

	// Widths: a name and a line that fill their fields, beside a short
	// row, so a row that overflows is seen not to move the other one.
	widths := utmpFile(t, dir, "widths",
		utmpRec{typ: utUserProcess, pid: 7, line: "L", user: "U", host: "H", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 8, line: strings.Repeat("z", 32), user: strings.Repeat("u", 32), host: strings.Repeat("h", 256), sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 9, line: "abc   ", user: "abc" + strings.Repeat(" ", 20), host: " h ", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 2147483647, line: "big", user: "big", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: -1, line: "neg", user: "neg", sec: utmpWhen},
	)

	// The run level is two bytes of the pid: the level now and the one
	// before, which reads `S` when it was none and is left out when the
	// record carries none at all. A zero level prints as the empty byte
	// it is.
	runlevels := utmpFile(t, dir, "runlevels",
		utmpRec{typ: utRunLvl, pid: int32('N')<<8 | int32('3'), user: "runlevel", sec: utmpWhen},
		utmpRec{typ: utRunLvl, pid: int32('2')<<8 | int32('5'), user: "runlevel", sec: utmpWhen},
		utmpRec{typ: utRunLvl, pid: int32('5'), user: "runlevel", sec: utmpWhen},
		utmpRec{typ: utRunLvl, pid: int32('X') << 8, user: "runlevel", sec: utmpWhen},
		utmpRec{typ: utRunLvl, pid: 0, user: "runlevel", sec: utmpWhen},
	)

	// The other record kinds with short ids and a wait status, where the
	// comment column's width and the exit column's spacing show.
	kinds := utmpFile(t, dir, "kinds",
		utmpRec{typ: utDeadProcess, pid: 5, line: "p", id: "x", user: "carol", sec: utmpWhen, exit: [2]int16{1, 2}},
		utmpRec{typ: utDeadProcess, pid: 6, line: "q", id: "abcd", sec: utmpWhen, exit: [2]int16{-1, 32767}},
		utmpRec{typ: utLoginProcess, pid: 7, line: "r", id: "y", user: "LOGIN", sec: utmpWhen},
		utmpRec{typ: utInitProcess, pid: 8, id: "si", sec: utmpWhen},
	)

	// A user process with an empty name is not a login and no option
	// prints it.
	noName := utmpFile(t, dir, "noname", utmpRec{typ: utUserProcess, pid: 9, line: "pts/2", host: "h", sec: utmpWhen})

	// Non-text fields, and a file that is not a database.
	raw := utmpFile(t, dir, "raw", utmpRec{typ: utUserProcess, pid: 1, line: "pts/\xff", id: "\xff", user: "a\xffb", host: "h\xfe", sec: utmpWhen})
	truncated := utmpRaw(t, dir, "truncated", append(oneRecordBytes(utmpMixed()), "\x07\x00\x00\x00abc"...))
	text := utmpRaw(t, dir, "text", []byte(strings.Repeat("not a utmp file\n", utmpRecordSize/16)))
	missing := filepath.Join(dir, "nosuch")

	// The seconds field is a SIGNED 32-bit count, so the corpus holds
	// both ends of it and both sides of the epoch.
	times := utmpFile(t, dir, "times",
		utmpRec{typ: utUserProcess, pid: 4, line: "t0", user: "e", sec: 0},
		utmpRec{typ: utUserProcess, pid: 5, line: "t1", user: "e", sec: 2147483647},
		utmpRec{typ: utUserProcess, pid: 6, line: "t2", user: "e", sec: -1},
		utmpRec{typ: utUserProcess, pid: 7, line: "t3", user: "e", sec: 1},
		utmpRec{typ: utUserProcess, pid: 8, line: "t4", user: "e", sec: -2147483648},
	)

	// Times either side of a daylight-time change, which is what makes
	// the zone file's transition table observable: 2004 is under the
	// United States rule the file carries and not the 2007 one a TZ
	// string spells, and the two 2021 pairs are the second before and
	// the second of each change.
	transitions := utmpFile(t, dir, "transitions",
		utmpRec{typ: utUserProcess, pid: 500, line: "t0", user: "a", sec: 1079827200},
		utmpRec{typ: utUserProcess, pid: 501, line: "t1", user: "a", sec: 1081036800},
		utmpRec{typ: utUserProcess, pid: 502, line: "t2", user: "a", sec: 1081209600},
		utmpRec{typ: utUserProcess, pid: 503, line: "t3", user: "a", sec: 1099368000},
		utmpRec{typ: utUserProcess, pid: 504, line: "t4", user: "a", sec: 1099641600},
		utmpRec{typ: utUserProcess, pid: 505, line: "t5", user: "a", sec: 1615705199},
		utmpRec{typ: utUserProcess, pid: 506, line: "t6", user: "a", sec: 1615705200},
		utmpRec{typ: utUserProcess, pid: 507, line: "t7", user: "a", sec: 1636264799},
		utmpRec{typ: utUserProcess, pid: 508, line: "t8", user: "a", sec: 1636264800},
		utmpRec{typ: utUserProcess, pid: 509, line: "t9", user: "a", sec: 2140000000},
	)

	// The terminal columns. Each line is an absolute path so who stats
	// the file named rather than one under /dev.
	term := shortTempDir(t)
	writable := whoTerminal(t, term, "a", 0o660)
	quiet := whoTerminal(t, term, "b", 0o600)
	stale := whoTerminal(t, term, "c", 0o600)
	future := whoTerminal(t, term, "d", 0o600)
	justOver := whoTerminal(t, term, "e", 0o666)
	// The message column is S_IWGRP alone, and every mode above sets
	// S_IRGRP and S_IWGRP together or clears both — so they cannot tell
	// the two bits apart, and reading the wrong one was invisible here
	// while being wrong on every real terminal, which is 0620. These two
	// separate them.
	mesgOnly := whoTerminal(t, term, "f", 0o620)
	readOnly := whoTerminal(t, term, "g", 0o640)
	gone := filepath.Join(term, "nosuch")
	idle := utmpFile(t, dir, "idle",
		utmpRec{typ: utUserProcess, pid: 100, line: writable, user: "u0", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 101, line: quiet, user: "u1", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 102, line: stale, user: "u2", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 103, line: future, user: "u3", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 104, line: justOver, user: "u4", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 105, line: gone, user: "u5", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 106, line: mesgOnly, user: "u6", host: "h", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 107, line: readOnly, user: "u7", host: "h", sec: utmpWhen},
	)
	// The idle time is counted from a boot record SEEN SO FAR, so a
	// session listed above the boot that follows it is `old` however
	// recently its terminal was read.
	idleBoot := utmpFile(t, dir, "idleboot",
		utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 60)},
		utmpRec{typ: utUserProcess, pid: 120, line: quiet, user: "ub", sec: utmpWhen},
	)
	// Reset before each side so both see the same elapsed time. The two
	// offsets in the middle of their windows — thirty seconds into the
	// 02:00 minute, and one second past the minute that ends `.` — are
	// what keeps the answer the same for a run seconds later.
	touch := func(t *testing.T) {
		t.Helper()
		now := time.Now()
		for _, s := range []struct {
			path string
			at   time.Time
		}{
			{writable, now},
			{quiet, now.Add(-2*time.Hour - 30*time.Second)},
			{stale, now.Add(-25 * time.Hour)},
			{future, now.Add(100 * time.Hour)},
			{justOver, now.Add(-90 * time.Second)},
			{mesgOnly, now},
			{readOnly, now},
		} {
			if err := os.Chtimes(s.path, s.at, s.at); err != nil {
				t.Fatal(err)
			}
		}
	}

	// --lookup, over the names /etc/hosts settles and the numeric forms
	// getaddrinfo answers without asking anything.
	hosts := utmpFile(t, dir, "hosts",
		utmpRec{typ: utUserProcess, pid: 300, line: "p0", user: "a", host: "localhost", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 301, line: "p1", user: "a", host: "LOCALHOST", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 302, line: "p2", user: "a", host: "127.0.0.1", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 303, line: "p3", user: "a", host: "10.0.0.5", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 304, line: "p4", user: "a", host: "0.0.0.0", sec: utmpWhen},
		// An X display is written after a colon and is not part of the
		// name: only what comes before it is canonicalized.
		utmpRec{typ: utUserProcess, pid: 305, line: "p5", user: "a", host: "LOCALHOST:0.0", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 306, line: "p6", user: "a", host: "localhost:", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 307, line: "p7", user: "a", host: ":0", sec: utmpWhen},
	)

	cases := []invocation{
		// The default database, which on a build machine is missing.
		{name: "no arguments"},
		{name: "dashdash alone", args: []string{"--"}},
	}

	// Every option on its own, and every option with the heading, over
	// the database that has a record of every kind.
	for _, opt := range []string{"-a", "-b", "-d", "-H", "-l", "-m", "-p", "-q", "-r", "-s", "-t", "-T", "-u", "-w"} {
		cases = append(cases,
			invocation{name: "option " + opt, args: []string{opt, mixed}},
			invocation{name: "option " + opt + " with a heading", args: []string{"-H", opt, mixed}},
		)
	}

	cases = append(cases,
		// Combinations that change the row.
		invocation{name: "-a with a heading", args: []string{"-a", "-H", mixed}},
		invocation{name: "-u then -s", args: []string{"-H", "-u", "-s", mixed}},
		invocation{name: "-s then -u", args: []string{"-H", "-s", "-u", mixed}},
		invocation{name: "-s then -d keeps the long row", args: []string{"-H", "-s", "-d", mixed}},
		invocation{name: "-d then -s keeps the long row", args: []string{"-H", "-d", "-s", mixed}},
		invocation{name: "-a then -s keeps the long row", args: []string{"-H", "-a", "-s", mixed}},
		invocation{name: "-s twice then -u", args: []string{"-H", "-s", "-s", "-u", mixed}},
		invocation{name: "-T with -u", args: []string{"-H", "-T", "-u", mixed}},
		invocation{name: "-T with -d", args: []string{"-H", "-T", "-d", mixed}},
		invocation{name: "-q with a heading is still a count", args: []string{"-q", "-H", mixed}},
		invocation{name: "-q with -a", args: []string{"-q", "-a", mixed}},
		invocation{name: "a cluster of every short option", args: []string{"-abdHlpqrstTuw", mixed}},
		invocation{name: "-a clustered with -m", args: []string{"-am", mixed}},
		invocation{name: "-T before -a", args: []string{"-Ta", mixed}},
		invocation{name: "the long forms", args: []string{"--all", "--heading", mixed}},
		invocation{name: "--mesg and --writable are -T", args: []string{"--mesg", "--writable", mixed}},

		// Record shapes.
		invocation{name: "one login", args: []string{one}},
		invocation{name: "an empty database", args: []string{"-a", empty}},
		invocation{name: "an empty database with a heading", args: []string{"-H", empty}},
		invocation{name: "an empty database counted", args: []string{"-q", empty}},
		invocation{name: "field widths", args: []string{widths}},
		invocation{name: "field widths, every column", args: []string{"-a", "-H", widths}},
		invocation{name: "field widths counted", args: []string{"-q", widths}},
		invocation{name: "run levels", args: []string{"-r", runlevels}},
		invocation{name: "run levels with every column", args: []string{"-a", runlevels}},
		invocation{name: "the other record kinds", args: []string{"-a", "-H", kinds}},
		invocation{name: "dead processes alone", args: []string{"-d", kinds}},
		invocation{name: "login processes alone", args: []string{"-l", kinds}},
		invocation{name: "init processes alone", args: []string{"-p", kinds}},
		invocation{name: "a user process with no name", args: []string{"-a", noName}},
		invocation{name: "a user process with no name counted", args: []string{"-q", noName}},
		invocation{name: "fields that are not valid UTF-8", args: []string{"-a", raw}},
		invocation{name: "a partial record at the end is ignored", args: []string{"-a", truncated}},
		invocation{name: "a file of text the size of a record", args: []string{"-a", text}},
		invocation{name: "times at both ends of the field", args: []string{"-a", times}},

		// The terminal columns.
		invocation{name: "message status and idle time", args: []string{"-a", idle}, prepare: touch},
		invocation{name: "idle time without the message status", args: []string{"-u", idle}, prepare: touch},
		invocation{name: "message status without the idle time", args: []string{"-T", idle}, prepare: touch},
		invocation{name: "idle time measured from a later boot", args: []string{"-a", idleBoot}, prepare: touch},

		// The TIME column is local time.
		invocation{name: "TZ empty is UTC", args: []string{"-a", mixed}, env: []string{"TZ="}},
		invocation{name: "TZ names a zone file", args: []string{"-a", mixed}, env: []string{"TZ=America/New_York"}},
		invocation{name: "TZ names a zone file with a colon", args: []string{"-a", mixed}, env: []string{"TZ=:America/New_York"}},
		invocation{name: "TZ names a southern zone", args: []string{"-a", mixed}, env: []string{"TZ=Australia/Sydney"}},
		invocation{name: "TZ names a half-hour zone", args: []string{"-a", mixed}, env: []string{"TZ=Asia/Kolkata"}},
		invocation{name: "TZ is a rule string with a file behind it", args: []string{"-a", mixed}, env: []string{"TZ=EST5EDT"}},
		invocation{name: "TZ is a rule string with dates", args: []string{"-a", mixed}, env: []string{"TZ=EST5EDT,M3.2.0,M11.1.0"}},
		invocation{name: "TZ is a rule string with times on the dates", args: []string{"-a", mixed}, env: []string{"TZ=PST8PDT,M3.2.0/2,M11.1.0/2"}},
		invocation{name: "TZ is a southern rule string", args: []string{"-a", mixed}, env: []string{"TZ=NZST-12NZDT,M9.5.0,M4.1.0/3"}},
		invocation{name: "TZ dates are Julian days", args: []string{"-a", times}, env: []string{"TZ=EST5EDT,J60,J300"}},
		invocation{name: "TZ dates are zero-based days", args: []string{"-a", times}, env: []string{"TZ=EST5EDT,60,300"}},
		invocation{name: "TZ dates span the new year", args: []string{"-a", times}, env: []string{"TZ=EST5EDT,0,365"}},
		invocation{name: "TZ is an offset with no zone file", args: []string{"-a", mixed}, env: []string{"TZ=XXX+3"}},
		invocation{name: "TZ is a quoted zone name", args: []string{"-a", mixed}, env: []string{"TZ=<+05>-5"}},
		invocation{name: "TZ quotes a name too short to be one", args: []string{"-a", mixed}, env: []string{"TZ=<XX>5<YY>4,M3.2.0,M11.1.0"}},
		invocation{name: "TZ offset past the hour limit", args: []string{"-a", mixed}, env: []string{"TZ=AAA+25:59:59"}},
		invocation{name: "TZ offset with minutes and seconds", args: []string{"-a", mixed}, env: []string{"TZ=AAA+5:45:30"}},
		invocation{name: "TZ names nothing", args: []string{"-a", mixed}, env: []string{"TZ=Bogus"}},
		invocation{name: "TZ names a file by path", args: []string{"-a", mixed}, env: []string{"TZ=/etc/localtime"}},
		invocation{name: "TZ names a missing file by path", args: []string{"-a", mixed}, env: []string{"TZ=/nonexistent"}},
		// A TZ that names a file which is not a zone file, is a
		// directory, or is empty: each falls through to the rule parse,
		// which refuses it, and the zone is UTC.
		invocation{name: "TZ names a file that is not a zone", args: []string{"-a", mixed}, env: []string{"TZ=/etc/passwd"}},
		invocation{name: "TZ names a directory", args: []string{"-a", mixed}, env: []string{"TZ=/etc"}},
		invocation{name: "TZ names an empty file", args: []string{"-a", mixed}, env: []string{"TZ=/dev/null"}},
		invocation{name: "TZ is a relative path", args: []string{"-a", mixed}, env: []string{"TZ=.."}},
		invocation{name: "TZDIR moves the zone directory", args: []string{"-a", mixed}, env: []string{"TZ=America/New_York", "TZDIR=/nonexistent"}},
		invocation{name: "TZ is a zone with no daylight time", args: []string{"-a", times}, env: []string{"TZ=Etc/GMT-9"}},
		invocation{name: "transitions in a zone file", args: []string{transitions}, env: []string{"TZ=America/New_York"}},
		invocation{name: "transitions in a zone file named as a rule", args: []string{transitions}, env: []string{"TZ=EST5EDT"}},
		invocation{name: "transitions south of the equator", args: []string{transitions}, env: []string{"TZ=Australia/Sydney"}},
		invocation{name: "transitions in a zone half an hour off", args: []string{transitions}, env: []string{"TZ=Asia/Kolkata"}},
		invocation{name: "transitions under a rule with the 2007 dates", args: []string{transitions}, env: []string{"TZ=EST5EDT,M3.2.0,M11.1.0"}},
		invocation{name: "transitions under a rule with the older dates", args: []string{transitions}, env: []string{"TZ=EST5EDT,M4.1.0,M10.5.0"}},
		invocation{name: "transitions with no daylight time at all", args: []string{transitions}, env: []string{"TZ=XXX+3"}},

		// --lookup. The files backend settles every name here, so no
		// case reaches DNS.
		invocation{name: "option --lookup", args: []string{"--lookup", hosts}},
		invocation{name: "option --lookup with a heading", args: []string{"-H", "--lookup", hosts}},
		invocation{name: "lookup with every column", args: []string{"-a", "--lookup", hosts}},
		invocation{name: "the same names without lookup", args: []string{hosts}},

		// -m, which needs a terminal on stdin and does not have one
		// here: the heading is printed and no row is.
		invocation{name: "-m with no terminal", args: []string{"-m", mixed}},
		invocation{name: "-m with a heading", args: []string{"-H", "-m", mixed}},
		invocation{name: "-m with every column", args: []string{"-a", "-m", mixed}},
		invocation{name: "two operands presume -m", args: []string{"am", "i"}},
		invocation{name: "two other operands presume -m", args: []string{"mom", "likes"}},
		invocation{name: "two operands with -q", args: []string{"-q", "am", "i"}},

		// Operands.
		invocation{name: "a missing database", args: []string{missing}},
		invocation{name: "a directory", args: []string{"/etc"}},
		invocation{name: "a directory with every column", args: []string{"-a", "/etc"}},
		invocation{name: "lone dash is a file name", args: []string{"-"}},
		invocation{name: "empty operand", args: []string{""}},
		invocation{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		invocation{name: "operand after dashdash", args: []string{"--", mixed}},
		invocation{name: "option-looking operand after dashdash", args: []string{"--", "--help"}},
		invocation{name: "three operands name the third", args: []string{"a", "b", "c"}},
		invocation{name: "four operands still name the third", args: []string{"a", "b", "c", "d"}},
		invocation{name: "an option after two operands", args: []string{"a", "b", "-H"}},

		// getopt faults, including the two ambiguities the option list
		// makes and the one long option that is not there.
		invocation{name: "invalid short option", args: []string{"-x"}},
		invocation{name: "invalid short option cluster", args: []string{"-xy"}},
		invocation{name: "invalid option after a valid one", args: []string{"-ax"}},
		invocation{name: "unrecognized long option", args: []string{"--foo"}},
		invocation{name: "long option with a value", args: []string{"--foo=bar"}},
		invocation{name: "empty long option is ambiguous", args: []string{"--=x"}},
		invocation{name: "heading or help is ambiguous", args: []string{"--h", mixed}},
		invocation{name: "login or lookup is ambiguous", args: []string{"--l", mixed}},
		invocation{name: "a longer prefix is still ambiguous", args: []string{"--lo", mixed}},
		invocation{name: "an ambiguous prefix with a value", args: []string{"--lo=x", mixed}},
		invocation{name: "the prefix that settles on login", args: []string{"--log", mixed}},
		invocation{name: "the prefix that settles on lookup", args: []string{"--loo", hosts}},
		invocation{name: "message and mesg are the same option", args: []string{"--m", mixed}},
		invocation{name: "a longer prefix of mesg", args: []string{"--mes", mixed}},
		invocation{name: "writable has a prefix of its own", args: []string{"--w", mixed}},
		invocation{name: "count has no short long name", args: []string{"--q", mixed}},
		invocation{name: "a flag refuses a value", args: []string{"--all=x", mixed}},
		invocation{name: "help with a value", args: []string{"--help=x"}},
		invocation{name: "version with a value", args: []string{"--version=1"}},
		invocation{name: "bad option before help", args: []string{"--foo", "--help"}},
		invocation{name: "operand before a bad option", args: []string{mixed, "--foo"}},
		invocation{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{mixed, "-H"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths.
		invocation{name: "stdout closed", args: []string{"-a", mixed}, stdout: stdoutClosed},
		invocation{name: "stdout full", args: []string{"-a", mixed}, stdout: stdoutFull},
		invocation{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		invocation{name: "stdout full with a count", args: []string{"-q", empty}, stdout: stdoutFull},
		invocation{name: "stdout closed on a fault", args: []string{"a", "b", "c"}, stdout: stdoutClosed},
	)
	return cases
}

func TestWhoParity(t *testing.T) {
	requireParity(t, "who", whoCases(t))
}

func TestWhoHelpVersion(t *testing.T) {
	requireHelp(t, "who", []string{"--help"}, 0)
	requireHelp(t, "who", []string{"--hel"}, 0)
	requireHelp(t, "who", []string{"--help", "x"}, 0)
	requireHelp(t, "who", []string{"x", "--help"}, 0)
	requireVersion(t, "who", []string{"--version"}, 0)
	requireVersion(t, "who", []string{"--vers"}, 0)
	requireVersion(t, "who", []string{"--version", "x"}, 0)
}
