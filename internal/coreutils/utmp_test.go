package coreutils

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// utmpRecordSize is the width of one entry on the HOST, which is what
// both sides of the oracle read. Everything up to ut_exit is the same
// everywhere; glibc sizes ut_session and ut_tv by
// __WORDSIZE_TIME64_COMPAT32, so that a 32-bit and a 64-bit process on one
// machine share a file format. That macro is 1 on an architecture with a
// 32-bit predecessor — x86-64 keeps the 32-bit widths, for 384 bytes — and
// 0 on one without, where ut_session is a `long` and ut_tv a real
// `struct timeval`, for 400.
//
// Writing 384 everywhere made GNU on the aarch64 runner read this corpus
// at the wrong stride: it found one record where eight were written and
// dated it from bytes that are ut_addr_v6 there.
// A map lookup would answer 0 for an architecture nobody listed, and a
// zero-width record is a corpus that writes nothing and compares empty
// against empty — so an unknown host is a panic at init, not a default.
var utmpRecordSize = utmpRecordSizeFor(runtime.GOARCH)

func utmpRecordSizeFor(goarch string) int {
	switch goarch {
	case "amd64", "386":
		return 384
	case "arm64":
		return 400
	}
	panic("coreutils: no struct utmp layout recorded for GOARCH " + goarch +
		" — read __WORDSIZE_TIME64_COMPAT32 for it and add the row, rather than letting the corpus run at the wrong stride")
}

// utmpTimeCompat32 is that macro: it decides both the width of ut_tv's
// seconds field and where it sits.
var utmpTimeCompat32 = utmpRecordSize == 384

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
// exit pair, the session, and the timeval, whose seconds field is a
// SIGNED count — 32-bit where the compat layout applies, which is why a
// record from before 1970 prints a 1969 date there rather than one in
// 2106, and 64-bit where it does not.
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
	if utmpTimeCompat32 {
		le.PutUint32(b[340:], uint32(r.sec))
	} else {
		le.PutUint64(b[344:], uint64(r.sec))
	}
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

// TestUtmpRecordLayoutMatchesTheHost checks the two constants above against
// the host's own <utmp.h>, which is the thing GNU was compiled against and
// therefore the only authority. Writing the x86-64 layout on aarch64 made
// GNU read the corpus at the wrong stride and cost 131 parity failures that
// named everything except the cause; this says it in one line instead.
func TestUtmpRecordLayoutMatchesTheHost(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		// Not a silent pass: the corpus itself still fails loudly on a
		// wrong stride, and this probe only makes that legible.
		t.Skipf("no C compiler, so the host's <utmp.h> cannot be consulted: %v", err)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "probe.c")
	const probe = `#include <utmp.h>
#include <stddef.h>
#include <stdio.h>
int main(void){printf("%zu %zu\n", sizeof(struct utmp), offsetof(struct utmp, ut_tv));return 0;}
`
	if err := os.WriteFile(src, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(cc, "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile the <utmp.h> probe: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run the <utmp.h> probe: %v", err)
	}

	var size, tvOff int
	if _, err := fmt.Sscanf(string(out), "%d %d", &size, &tvOff); err != nil {
		t.Fatalf("parse the probe's answer %q: %v", out, err)
	}
	if size != utmpRecordSize {
		t.Errorf("the host's struct utmp is %d bytes, the corpus writes %d — GNU will read this database at the wrong stride", size, utmpRecordSize)
	}
	wantTv := 344
	if utmpTimeCompat32 {
		wantTv = 340
	}
	if tvOff != wantTv {
		t.Errorf("the host puts ut_tv at %d, the corpus writes its seconds at %d", tvOff, wantTv)
	}
}
