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

var utmpRecordSize = utmpRecordSizeFor(runtime.GOOS, runtime.GOARCH)

func utmpRecordSizeFor(goos, goarch string) int {
	if goos == "darwin" {
		return 628
	}
	switch goarch {
	case "amd64", "386":
		return 384
	case "arm64":
		return 400
	}
	panic("coreutils: no struct utmp layout recorded for GOARCH " + goarch +
		" — read __WORDSIZE_TIME64_COMPAT32 for it and add the row, rather than letting the corpus run at the wrong stride")
}

var utmpTimeCompat32 = utmpRecordSize == 384

var utmpDarwin = runtime.GOOS == "darwin"

const (
	utEmpty        = 0
	utRunLvl       = 1
	utBootTime     = 2
	utInitProcess  = 5
	utLoginProcess = 6
	utUserProcess  = 7
	utDeadProcess  = 8
	utSignature    = 10
)

var utNewTime, utOldTime = utClockChangeTypes(utmpDarwin)

func utClockChangeTypes(darwin bool) (int, int) {
	if darwin {
		return 4, 3
	}
	return 3, 4
}

type utmpRec struct {
	typ  int
	pid  int32
	line string
	id   string
	user string
	host string
	sec  int32
	exit [2]int16
}

func (r utmpRec) bytes() []byte {
	b := make([]byte, utmpRecordSize)
	le := binary.LittleEndian
	if utmpDarwin {
		copy(b[0:256], r.user)
		copy(b[256:256+4], r.id)
		copy(b[260:260+32], r.line)
		le.PutUint32(b[292:], uint32(r.pid))
		le.PutUint16(b[296:], uint16(r.typ))
		le.PutUint32(b[300:], uint32(r.sec))
		copy(b[308:308+256], r.host)
		return b
	}
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

func utmpDB(recs ...utmpRec) []byte {
	var buf bytes.Buffer
	if utmpDarwin {
		buf.Write(utmpRec{typ: utSignature, user: "utmpx-1.00"}.bytes())
	}
	for _, r := range recs {
		buf.Write(r.bytes())
	}
	return buf.Bytes()
}

func utmpFile(t *testing.T, dir, name string, recs ...utmpRec) string {
	t.Helper()
	return utmpRaw(t, dir, name, utmpDB(recs...))
}

func utmpRaw(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	if utmpDarwin {
		name += "x"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const utmpWhen = 1614834367

func utmpMixed() []utmpRec {
	return []utmpRec{
		{typ: utBootTime, line: "~", id: "~~", user: "reboot", sec: utmpWhen - 10000},
		{typ: utRunLvl, pid: 0x3553, line: "~", id: "~~", user: "runlevel", sec: utmpWhen - 9999},
		{typ: utUserProcess, pid: 1001, line: "pts/0", id: "ts/0", user: "alice", host: "10.0.0.5", sec: utmpWhen},
		{typ: utUserProcess, pid: 1002, line: "ttyQ", id: "ttyQ", user: "bob", sec: utmpWhen + 60},
		{typ: utUserProcess, pid: 1003, line: "pts/1", id: "ts/1", user: "alice", host: "example.com", sec: utmpWhen + 120},
		{typ: utLoginProcess, pid: 900, line: "tty2", id: "tty2", user: "LOGIN", sec: utmpWhen - 5000},
		{typ: utDeadProcess, pid: 800, line: "pts/9", id: "ts/9", user: "carol", sec: utmpWhen - 4000, exit: [2]int16{3, 9}},
		{typ: utInitProcess, pid: 700, id: "si", sec: utmpWhen - 6000},
		{typ: utEmpty},
		{typ: utNewTime, line: "|", id: "{", user: "date", sec: utmpWhen - 3000},
		{typ: utOldTime, line: "}", id: "{", user: "date", sec: utmpWhen - 3001},
	}
}

func TestUtmpRecordLayoutMatchesTheHost(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skipf("no C compiler, so the host's C library cannot be consulted: %v", err)
	}
	if utmpDarwin {
		checkUtmpxRoundTrip(t, cc)
		return
	}

	const probe = `#include <utmp.h>
#include <stddef.h>
#include <stdio.h>
int main(void){printf("%zu %zu\n", sizeof(struct utmp), offsetof(struct utmp, ut_tv));return 0;}
`
	out := runUtmpProbe(t, cc, probe)
	var size, tvOff int
	if _, err := fmt.Sscanf(out, "%d %d", &size, &tvOff); err != nil {
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

func checkUtmpxRoundTrip(t *testing.T, cc string) {
	const probe = `#include <utmpx.h>
#include <stdio.h>
int main(int argc, char **argv){
  struct utmpx *u;
  if (!utmpxname(argv[1])) return 2;
  setutxent();
  while ((u = getutxent()) != NULL)
    printf("%d|%d|%.*s|%.*s|%.*s|%.*s|%ld\n", u->ut_type, (int)u->ut_pid,
      (int)sizeof u->ut_user, u->ut_user, (int)sizeof u->ut_id, u->ut_id,
      (int)sizeof u->ut_line, u->ut_line, (int)sizeof u->ut_host, u->ut_host,
      (long)u->ut_tv.tv_sec);
  endutxent();
  return 0;
}
`
	db := utmpFile(t, t.TempDir(), "probe",
		utmpRec{typ: utBootTime, line: "~", id: "~~", user: "reboot", sec: utmpWhen - 10000},
		utmpRec{typ: utUserProcess, pid: 1001, line: "ttys000", id: "s000", user: "alice", host: "10.0.0.5", sec: utmpWhen},
	)
	got := runUtmpProbe(t, cc, probe, db)
	want := fmt.Sprintf("%d|0|reboot|~~|~||%d\n%d|1001|alice|s000|ttys000|10.0.0.5|%d\n", utBootTime, utmpWhen-10000, utUserProcess, utmpWhen)
	if got != want {
		t.Errorf("getutxent(3) read the fixture as\n%s\nwant\n%s", got, want)
	}
}

func runUtmpProbe(t *testing.T, cc, src string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	cfile := filepath.Join(dir, "probe.c")
	if err := os.WriteFile(cfile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(cc, "-o", bin, cfile).CombinedOutput(); err != nil {
		t.Fatalf("compile the probe: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		t.Fatalf("run the probe: %v", err)
	}
	return string(out)
}
