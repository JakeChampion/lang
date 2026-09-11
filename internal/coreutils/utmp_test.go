package coreutils

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The login-accounting database the three utmp utilities read, built as
// a fixture so the corpus has something to compare over.
//
// It has to be a fixture. /var/run/utmp is the machine's live login
// state: a container has none at all, a developer's laptop has one that
// changes while the suite runs, and nothing a test may do can put a
// chosen record in it. What makes the family testable anyway is that
// `users` and `who` take the database as an OPERAND — GNU documents it
// as "/var/log/wtmp as FILE is common" — so a file this package writes
// is a database both sides read, and every record shape below becomes a
// byte-exact case rather than a hope about the host.
//
// `pinky` has no such operand and is the exception; pinky_test.go
// records what that costs.

// utmpRecordSize is the fixed width of one entry. Linux's `struct utmp`
// and `struct utmpx` are the same 384-byte record on every
// architecture the corpus runs on.
const utmpRecordSize = 384

// The ut_type values. USER_PROCESS is the only one that names a logged
// -in user; the rest describe the run level, the boot time, an init or
// login process not yet claimed, the remains of one that was, and a
// clock change.
const (
	utEmpty = iota
	utRunLvl
	utBootTime
	utNewTime
	utOldTime
	utInitProcess
	utLoginProcess
	utUserProcess
	utDeadProcess
)

// utmpRec is one entry, in the fields a utility prints or matches on.
// Everything else in the record — the session id, the address, the
// reserved tail — is written as zero, which is what the fields no
// utility reads are on a real database too.
type utmpRec struct {
	typ  int
	pid  int32
	line string
	id   string
	user string
	host string
	sec  int32
	// exit is ut_exit: the termination signal and the exit status of a
	// dead process, which `who -d` prints as `term=N exit=N`.
	exit [2]int16
}

// bytes lays the record out at the offsets the kernel writes: a 2-byte
// type in a 4-byte slot, the pid, then four NUL-padded char arrays, the
// exit pair, the session, and the timeval whose seconds field is a
// SIGNED 32-bit count — which is why a record from before 1970 prints a
// 1969 date rather than one in 2106.
func (r utmpRec) bytes() []byte {
	b := make([]byte, utmpRecordSize)
	le := binary.LittleEndian
	le.PutUint16(b[0:], uint16(r.typ))
	le.PutUint32(b[4:], uint32(r.pid))
	copy(b[8:8+32], r.line)
	copy(b[40:40+4], r.id)
	copy(b[44:44+32], r.user)
	copy(b[76:76+256], r.host)
	le.PutUint16(b[332:], uint16(r.exit[0]))
	le.PutUint16(b[334:], uint16(r.exit[1]))
	le.PutUint32(b[340:], uint32(r.sec))
	return b
}

// utmpFile writes a database of `recs` and answers its path.
func utmpFile(t *testing.T, dir, name string, recs ...utmpRec) string {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range recs {
		buf.Write(r.bytes())
	}
	return utmpRaw(t, dir, name, buf.Bytes())
}

// utmpRaw writes bytes that need not be whole records — a truncated
// database, or one that is not a database at all.
func utmpRaw(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// utmpWhen is the login time every fixture record carries unless it
// wants another: 2021-03-04 05:06:07 UTC, which renders as `Mar  4
// 05:06` and pins the month name, the space-padded day and the
// hour:minute the corpus compares.
const utmpWhen = 1614834367

// utmpMixed is the database most cases read: every record type, two
// users with three sessions between them, a host that is an address and
// one that is a name, and one session with no host at all.
func utmpMixed() []utmpRec {
	return []utmpRec{
		{typ: utBootTime, line: "~", id: "~~", user: "reboot", sec: utmpWhen - 10000},
		{typ: utRunLvl, pid: 0x3553, line: "~", id: "~~", user: "runlevel", sec: utmpWhen - 9999},
		{typ: utUserProcess, pid: 1001, line: "pts/0", id: "ts/0", user: "alice", host: "10.0.0.5", sec: utmpWhen},
		{typ: utUserProcess, pid: 1002, line: "tty1", id: "tty1", user: "bob", sec: utmpWhen + 60},
		{typ: utUserProcess, pid: 1003, line: "pts/1", id: "ts/1", user: "alice", host: "example.com", sec: utmpWhen + 120},
		{typ: utLoginProcess, pid: 900, line: "tty2", id: "tty2", user: "LOGIN", sec: utmpWhen - 5000},
		{typ: utDeadProcess, pid: 800, line: "pts/9", id: "ts/9", user: "carol", sec: utmpWhen - 4000, exit: [2]int16{3, 9}},
		{typ: utInitProcess, pid: 700, id: "si", sec: utmpWhen - 6000},
		{typ: utEmpty},
		{typ: utNewTime, line: "|", id: "{", user: "date", sec: utmpWhen - 3000},
		{typ: utOldTime, line: "}", id: "{", user: "date", sec: utmpWhen - 3001},
	}
}
