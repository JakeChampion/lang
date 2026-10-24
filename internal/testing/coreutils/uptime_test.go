package coreutils

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	registerCorpus("uptime", uptimeCases)
}

var (
	uptimeClockRe = regexp.MustCompile(`^ \d\d:\d\d:\d\d`)
	uptimeElapsed = regexp.MustCompile(` ?\d{1,2}:\d{2},`)
	uptimeLoadRe  = regexp.MustCompile(`load average: .*`)
	uptimeDigitRe = regexp.MustCompile(`\d`)
	uptimeWholeRe = regexp.MustCompile(`\d+\.`)
)

func uptimeZeroDigits(s string) string { return uptimeDigitRe.ReplaceAllString(s, "0") }

func maskUptime(s string) string {
	s = uptimeClockRe.ReplaceAllStringFunc(s, uptimeZeroDigits)
	s = uptimeElapsed.ReplaceAllStringFunc(s, uptimeZeroDigits)
	s = uptimeLoadRe.ReplaceAllStringFunc(s, func(load string) string {
		return uptimeZeroDigits(uptimeWholeRe.ReplaceAllString(load, "0."))
	})
	return s
}

func TestMaskUptimeLoadWidth(t *testing.T) {
	line := func(loads string) string {
		return " 19:49:16  up  1:02,  2 users,  load average: " + loads + "\n"
	}
	want := maskUptime(line("9.87, 0.81, 0.67"))
	if got := maskUptime(line("10.02, 0.81, 0.67")); got != want {
		t.Errorf("a load of 10.02 masks to %q, 9.87 to %q", got, want)
	}
	for _, bad := range []string{"9.8, 0.81, 0.67", "9.87,0.81, 0.67", "9, 0.81, 0.67"} {
		if got := maskUptime(line(bad)); got == want {
			t.Errorf("load average %q masks to the well-formed %q", bad, got)
		}
	}
}

func maskBootErrno(s string) string {
	return uptimeBootErrno.ReplaceAllString(s, "couldn't get boot time\n")
}

var uptimeBootErrno = regexp.MustCompile(`couldn't get boot time: [^\n]*\n`)

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
			sec: int32(now - 60),
		})
	}
	return utmpFile(t, dir, name, append(recs, extra...)...)
}

func uptimeCases(t *testing.T) []invocation {
	dir := t.TempDir()

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

	twoBoots := uptimeFile(t, dir, "twoboots",
		utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 90000)},
		utmpRec{typ: utUserProcess, pid: 1, line: "pts/0", user: "alice", sec: utmpWhen},
		utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 7200)},
	)
	cases = append(cases, invocation{name: "the last boot record wins", args: []string{twoBoots}, mask: maskUptime})

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
	trailing := utmpRaw(t, dir, "trailing", append(utmpDB(utmpRec{typ: utBootTime, line: "~", user: "reboot", sec: int32(time.Now().Unix() - 3600)}), 1, 2, 3))
	cases = append(cases,
		invocation{name: "a boot time in the future", args: []string{future}, mask: maskUptime},
		invocation{name: "no boot record at all", args: []string{noBoot}, mask: maskUptime, stderrMask: maskBootErrno},
		invocation{name: "an empty database", args: []string{empty}, mask: maskUptime, stderrMask: maskBootErrno},
		invocation{name: "a partial record is no record", args: []string{short}, mask: maskUptime, stderrMask: maskBootErrno},
		invocation{name: "a trailing partial record is dropped", args: []string{trailing}, mask: maskUptime},
		invocation{name: "a database that is not there", args: []string{dir + "/nosuchz"}, mask: maskUptime},
		invocation{name: "a directory as the database", args: []string{dir}, mask: maskUptime},
		invocation{name: "an empty operand", args: []string{""}, mask: maskUptime},
	)

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

func TestUptimeReportsTheRealLoadAverages(t *testing.T) {
	loads := uptimeKernelLoads(t)
	before := loads()
	dir := t.TempDir()
	db := uptimeDB(t, dir, "loads", time.Hour, 1)
	inv := invocation{name: "loads", args: []string{db}}
	got := inv.run(t, fernBin(t, "uptime"), "uptime")
	after := loads()
	if got.exit != 0 {
		t.Fatalf("uptime %v: %s, stderr %q", inv.args, got.how(), got.stderr)
	}
	printed := uptimeLoadRe.FindString(string(got.stdout))
	if printed == "" {
		t.Fatalf("no load-average clause in %q; the kernel always has one", got.stdout)
	}
	printed = strings.TrimPrefix(printed, "load average: ")
	for i, f := range strings.Split(printed, ", ") {
		if !strings.Contains(before, f) && !strings.Contains(after, f) {
			t.Errorf("load average %d is %q, which the kernel reported neither before (%q) nor after (%q) the run",
				i+1, f, strings.TrimSpace(before), strings.TrimSpace(after))
		}
	}
}

func uptimeKernelLoads(t *testing.T) func() string {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		return func() string {
			b, err := os.ReadFile("/proc/loadavg")
			if err != nil {
				t.Fatalf("/proc/loadavg: %v", err)
			}
			return string(b)
		}
	case "darwin":
		return func() string {
			b, err := exec.Command("/usr/sbin/sysctl", "-n", "vm.loadavg").Output()
			if err != nil {
				t.Fatalf("sysctl -n vm.loadavg: %v", err)
			}
			return string(b)
		}
	}
	t.Skipf("uptime refuses on %s: it has no load averages to read", runtime.GOOS)
	return nil
}
