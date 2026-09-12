package coreutils

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	registerCorpus("uptime", uptimeCases)
}

// uptime(1) prints one line, and three of its fields move while the
// corpus is reading them. The GNU leg and the Fern leg run milliseconds
// apart over one machine: the clock advances a second, the elapsed time
// advances a minute, and the kernel recomputes the load averages every
// five seconds. Diffing those bytes would measure the gap between two
// processes rather than either implementation.
//
// So the mask canonicalises DIGITS and nothing else, by position:
//
//	 19:49:16  up  1:02,  3 users,  load average: 0.56, 0.81, 0.67
//	 00:00:00  up  0:00,  3 users,  load average: 0.00, 0.00, 0.00
//
// Everything that says whether the FORMAT is right survives it: the
// leading space, the colons, the two spaces after the clock, `up`
// followed by two spaces with no whole days and one with, the `%2d`
// hours column that keeps a space for a single digit, the `%02d`
// minutes, the comma-and-two-spaces between fields, the singular or
// plural of `user`, and the whole load-average clause including its
// two decimals. The digit COUNT survives too, so a `%2d` that became
// `%d` still fails.
//
// What the mask costs is the VALUES, and they are pinned separately:
// the boot times come from the fixture, so the elapsed time is chosen
// rather than observed, and TestUptimeReportsTheRealLoadAverages checks
// the printed numbers against /proc/loadavg beside the corpus, the way
// mktemp's randomness and df's reserve are checked.
//
// The database is a fixture handed to both sides as the FILE operand,
// which is what makes the rest deterministic. gnulib synthesises a boot
// record from /proc when the file it reads IS /var/run/utmp and not
// otherwise, so a named file is the whole answer and the session count
// and boot instant are ours to choose. `uptime` with no operand is
// therefore NOT in the corpus: it reads the machine's live login state,
// which is the reasoning `df`'s no-operand case records.

var (
	uptimeClockRe = regexp.MustCompile(`^ \d\d:\d\d:\d\d`)
	uptimeElapsed = regexp.MustCompile(` ?\d{1,2}:\d{2},`)
	uptimeLoadRe  = regexp.MustCompile(`load average: .*`)
	uptimeDigitRe = regexp.MustCompile(`\d`)
)

func uptimeZeroDigits(s string) string { return uptimeDigitRe.ReplaceAllString(s, "0") }

// maskUptime zeroes the digits of the three moving fields. The clock
// goes first: once it reads 00:00:00 it can no longer be mistaken for
// the elapsed time, which is the only other HH:MM run on the line.
func maskUptime(s string) string {
	s = uptimeClockRe.ReplaceAllStringFunc(s, uptimeZeroDigits)
	s = uptimeElapsed.ReplaceAllStringFunc(s, uptimeZeroDigits)
	s = uptimeLoadRe.ReplaceAllStringFunc(s, uptimeZeroDigits)
	return s
}

// uptimeDB writes a database whose boot record sits `ago` before now and
// which holds `sessions` logged-in users. `ago` is chosen per case so the
// elapsed time lands in the shape that case is about; it is relative to
// the clock because the elapsed time is, and a fixed instant would drift
// into a different shape as the file aged.
func uptimeDB(t *testing.T, dir, name string, ago time.Duration, sessions int, extra ...utmpRec) string {
	t.Helper()
	now := time.Now().Unix()
	recs := []utmpRec{
		{typ: utBootTime, line: "~", id: "~~", user: "reboot", sec: int32(now - int64(ago.Seconds()))},
	}
	for i := 0; i < sessions; i++ {
		recs = append(recs, utmpRec{
			typ: utUserProcess, pid: int32(1000 + i),
			line: "pts/" + string(rune('0'+i%10)), id: "ts/0",
			user: "user" + string(rune('a'+i%26)), host: "10.0.0.5",
			sec:  int32(now - 60),
		})
	}
	return utmpFile(t, dir, name, append(recs, extra...)...)
}

func uptimeCases(t *testing.T) []invocation {
	dir := t.TempDir()

	// The elapsed-time spellings. GNU has exactly three — under a day,
	// one day, and more than one — and the hours column is `%2d`, so
	// which side of ten the hour falls on is its own case.
	shapes := []struct {
		name string
		ago  time.Duration
	}{
		{"minutes only", 7 * time.Minute},
		{"a single-digit hour", 5*time.Hour + 30*time.Minute},
		{"a two-digit hour", 15*time.Hour + 4*time.Minute},
		{"just under a day", 23*time.Hour + 59*time.Minute},
		{"exactly one day, singular", 24*time.Hour + 2*time.Hour + 1*time.Minute},
		{"two days, plural", 2*24*time.Hour + 2*time.Hour + 1*time.Minute},
		{"a hundred days", 100*24*time.Hour + 9*time.Hour},
		{"a day and no hours", 24*time.Hour + 30*time.Second},
		{"zero elapsed", 0},
	}
	var cases []invocation
	for i, s := range shapes {
		db := uptimeDB(t, dir, "shape"+string(rune('a'+i)), s.ago, 2)
		cases = append(cases, invocation{name: s.name, args: []string{db}, mask: maskUptime})
	}

	// The session count, which is the plural and nothing else. Records
	// that are not USER_PROCESS, and a USER_PROCESS with an empty name,
	// do not count -- that is gnulib's IS_USER_PROCESS.
	for _, c := range []struct {
		name string
		n    int
	}{{"no users", 0}, {"one user, singular", 1}, {"two users", 2}, {"ten users", 10}} {
		db := uptimeDB(t, dir, "n"+c.name, 3*time.Hour, c.n)
		cases = append(cases, invocation{name: c.name, args: []string{db}, mask: maskUptime})
	}

	noise := uptimeDB(t, dir, "noise", 3*time.Hour, 1,
		utmpRec{typ: utRunLvl, pid: 0x3553, line: "~", id: "~~", user: "runlevel", sec: utmpWhen},
		utmpRec{typ: utLoginProcess, pid: 900, line: "tty2", id: "tty2", user: "LOGIN", sec: utmpWhen},
		utmpRec{typ: utDeadProcess, pid: 800, line: "pts/9", id: "ts/9", user: "carol", sec: utmpWhen},
		utmpRec{typ: utInitProcess, pid: 700, id: "si", sec: utmpWhen},
		utmpRec{typ: utEmpty},
		utmpRec{typ: utUserProcess, pid: 1010, line: "pts/8", id: "ts/8", user: "", sec: utmpWhen},
		utmpRec{typ: utNewTime, line: "|", id: "{", user: "date", sec: utmpWhen},
		utmpRec{typ: utOldTime, line: "}", id: "{", user: "date", sec: utmpWhen},
	)
	cases = append(cases, invocation{name: "only USER_PROCESS with a name counts", args: []string{noise}, mask: maskUptime})

	// The LAST boot record wins, which is what GNU's loop leaves behind.
	twoBoots := uptimeFile(t, dir, "twoboots",
		utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 90000)},
		utmpRec{typ: utUserProcess, pid: 1, line: "pts/0", user: "alice", sec: utmpWhen},
		utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 7200)},
	)
	cases = append(cases, invocation{name: "the last boot record wins", args: []string{twoBoots}, mask: maskUptime})

	// A boot time that is not usable. Both print the question-mark form
	// and exit 1, and the first prints a diagnostic before anything.
	future := uptimeFile(t, dir, "future",
		utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() + 3600)},
		utmpRec{typ: utUserProcess, pid: 1, line: "pts/0", user: "alice", sec: utmpWhen},
	)
	noBoot := uptimeFile(t, dir, "noboot",
		utmpRec{typ: utUserProcess, pid: 1, line: "pts/0", user: "alice", sec: utmpWhen},
		utmpRec{typ: utUserProcess, pid: 2, line: "pts/1", user: "bob", sec: utmpWhen},
	)
	empty := utmpRaw(t, dir, "empty", nil)
	short := utmpRaw(t, dir, "short", make([]byte, utmpRecordSize/2))
	trailing := utmpRaw(t, dir, "trailing", append(utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 3600)}.bytes(), 1, 2, 3))
	cases = append(cases,
		invocation{name: "a boot time in the future", args: []string{future}, mask: maskUptime},
		invocation{name: "no boot record at all", args: []string{noBoot}, mask: maskUptime},
		invocation{name: "an empty database", args: []string{empty}, mask: maskUptime},
		invocation{name: "a partial record is no record", args: []string{short}, mask: maskUptime},
		invocation{name: "a trailing partial record is dropped", args: []string{trailing}, mask: maskUptime},
		invocation{name: "a database that is not there", args: []string{dir + "/nosuchz"}, mask: maskUptime},
		invocation{name: "a directory as the database", args: []string{dir}, mask: maskUptime},
		invocation{name: "an empty operand", args: []string{""}, mask: maskUptime},
	)

	// The option surface, which is the two standard options and nothing
	// else. -p and -s belong to procps' uptime, not to this one.
	db := uptimeDB(t, dir, "opt", 3*time.Hour, 1)
	cases = append(cases,
		invocation{name: "pretty is not an option here", args: []string{"-p"}},
		invocation{name: "since is not either", args: []string{"-s"}},
		invocation{name: "invalid short option", args: []string{"-x"}},
		invocation{name: "invalid option cluster", args: []string{"-xy"}},
		invocation{name: "unrecognized long option", args: []string{"--foo"}},
		invocation{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		invocation{name: "empty long option", args: []string{"--="}},
		invocation{name: "help takes no value", args: []string{"--help=x"}},
		invocation{name: "version takes no value", args: []string{"--version=x"}},
		invocation{name: "two operands", args: []string{db, db}},
		invocation{name: "three operands report the second", args: []string{db, "b", "c"}},
		invocation{name: "an operand that is not valid UTF-8 in the report", args: []string{db, "b\xff\xfe"}},
		invocation{name: "an operand with a newline in the report", args: []string{db, "b\nc"}},
		invocation{name: "an operand with a quote in the report", args: []string{db, "b'c"}},
		invocation{name: "an empty second operand", args: []string{db, ""}},
		invocation{name: "a bad option after an operand", args: []string{db, "-x"}},
		invocation{name: "a lone dash is an operand", args: []string{"-"}, mask: maskUptime},
		invocation{name: "dashdash then the database", args: []string{"--", db}, mask: maskUptime},
		invocation{name: "dashdash protects an option-looking operand", args: []string{"--", "-p"}, mask: maskUptime},
		invocation{name: "an operand that is not valid UTF-8", args: []string{dir + "/no\xff\xfez"}, mask: maskUptime},
	)
	return cases
}

// uptimeFile is utmpFile under a name that says which corpus writes it.
func uptimeFile(t *testing.T, dir, name string, recs ...utmpRec) string {
	t.Helper()
	return utmpFile(t, dir, name, recs...)
}

func TestUptime(t *testing.T) {
	requireParity(t, "uptime", uptimeCases(t))
}

func TestUptimeHelp(t *testing.T) {
	requireHelp(t, "uptime", []string{"--help"}, 0)
	requireHelp(t, "uptime", []string{"--hel"}, 0)
	requireHelp(t, "uptime", []string{"--help", "extra"}, 0)
	requireVersion(t, "uptime", []string{"--version"}, 0)
	requireVersion(t, "uptime", []string{"--vers"}, 0)
}

// The corpus masks the load averages, so this is what says they are the
// machine's and not three zeroes: the printed numbers have to be values
// /proc/loadavg actually held. Reading the file either side of the run
// bounds what the child could legitimately have seen, so a value from
// neither sample is wrong rather than merely stale.
//
// It is the same shape as mktemp's randomness check and df's reserve
// check: a property no diff against GNU can see, asserted against the
// machine rather than against the reference.
func TestUptimeReportsTheRealLoadAverages(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("the load averages are /proc/loadavg; %s has none, and uptime refuses there", runtime.GOOS)
	}
	before, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatalf("/proc/loadavg: %v", err)
	}
	dir := t.TempDir()
	db := uptimeDB(t, dir, "loads", time.Hour, 1)
	inv := invocation{name: "loads", args: []string{db}}
	got := inv.run(t, fernBin(t, "uptime"), "uptime")
	after, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatalf("/proc/loadavg: %v", err)
	}
	if got.exit != 0 {
		t.Fatalf("uptime %v: %s, stderr %q", inv.args, got.how(), got.stderr)
	}
	printed := uptimeLoadRe.FindString(string(got.stdout))
	if printed == "" {
		t.Fatalf("no load-average clause in %q; on Linux there is always one", got.stdout)
	}
	printed = strings.TrimPrefix(printed, "load average: ")
	for i, f := range strings.Split(printed, ", ") {
		if !strings.Contains(string(before), f) && !strings.Contains(string(after), f) {
			t.Errorf("load average %d is %q, which /proc/loadavg held neither before (%q) nor after (%q) the run",
				i+1, f, strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
		}
	}
}
