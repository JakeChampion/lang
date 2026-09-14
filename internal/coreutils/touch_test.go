package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// touch(1) leaves a tree behind and says little, so the corpus is
// what it creates and refuses — files, directories, links, FIFOs, the
// `-` operand, missing names under -c and -h — plus the option faults,
// the `--time` words and the invalid spellings of `-t` and `-d`. The
// harness compares the tree's names, kinds, modes and contents but not
// its timestamps, so the times the options set are checked by
// TestTouchTimestamps, which runs both binaries over identical trees
// and reads the stamps back.

func init() {
	registerCorpus("touch", touchCases)
}

// touchTree holds one of every operand kind: a file, a directory, a
// symlink to each, a dangling symlink, a FIFO, a nested file and a
// reference file with fixed times.
func touchTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("data\n"), 0o644); err != nil {
		t.Fatalf("write f: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir d: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "inner"), []byte("inner\n"), 0o600); err != nil {
		t.Fatalf("write d/inner: %v", err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "lf")); err != nil {
		t.Fatalf("symlink lf: %v", err)
	}
	if err := os.Symlink("d", filepath.Join(dir, "ld")); err != nil {
		t.Fatalf("symlink ld: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("symlink dangling: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "p"), 0o644); err != nil {
		t.Fatalf("mkfifo p: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ref"), []byte("r"), 0o644); err != nil {
		t.Fatalf("write ref: %v", err)
	}
	if err := os.Chtimes(filepath.Join(dir, "ref"), time.Unix(1000000000, 111111111), time.Unix(1600000000, 222222222)); err != nil {
		t.Fatalf("chtimes ref: %v", err)
	}
}

func touchCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }
	tree := func(name string, args ...string) {
		add(invocation{name: name, args: args, seedTree: touchTree})
	}

	// ---- creation ----
	tree("create one", "new")
	tree("create two", "new1", "new2")
	tree("create nested", "d/new")
	tree("create in a missing directory", "nodir/new")
	tree("create under a file", "f/new")
	tree("create through a directory symlink", "ld/new")
	tree("create a quoted name", "it's new")
	tree("create with -f", "-f", "new")
	tree("create with -a", "-a", "new")
	tree("create with -m", "-m", "new")
	tree("create with -am", "-am", "new")
	tree("create with -t", "-t", "202406151234.56", "new")
	tree("create with -d", "-d", "2024-06-15 12:34:56", "new")
	tree("create with -r", "-r", "ref", "new")
	tree("create with -r -d", "-r", "ref", "-d", "+1 day", "new")
	tree("create the same name twice", "new", "new")
	tree("an empty name", "")
	tree("an empty name among others", "new", "", "new2")
	add(invocation{name: "create under umask 077", args: []string{"new"}, seedTree: touchTree, umask: withMask(0o077)})
	add(invocation{name: "create under umask 0", args: []string{"new"}, seedTree: touchTree, umask: withMask(0)})
	add(invocation{name: "create under umask 027", args: []string{"new", "d/new"}, seedTree: touchTree, umask: withMask(0o027)})
	tree("no create on missing", "-c", "new")
	tree("no create on missing and existing", "-c", "new", "f")
	tree("no create on a missing directory component", "-c", "nodir/new")
	tree("--no-create", "--no-create", "new")
	tree("--no-c prefix", "--no-c", "new")
	tree("--n is ambiguous", "--n", "new")
	tree("-h on missing", "-h", "new")
	tree("-h -c on missing", "-h", "-c", "new")
	tree("--no-dereference on missing", "--no-dereference", "new")
	tree("--no-d prefix", "--no-d", "new")

	// ---- every operand kind under the ways of choosing a time ----
	for _, mode := range [][]string{nil, {"-a"}, {"-m"}, {"-h"}, {"-c"}, {"-ch"}, {"-t", "202406151234"}, {"-d", "2024-06-15 12:34:56.5"}, {"-r", "ref"}, {"-r", "ref", "-d", "+1 day"}, {"-h", "-r", "ref"}, {"--time=atime"}, {"--time=modify"}} {
		label := "default"
		if mode != nil {
			label = mode[0]
		}
		for _, op := range []string{"f", "d", "d/inner", "lf", "ld", "dangling", "p", "missing", "d/", "f/", "lf/", ".", "..", "/", "/dev/null"} {
			add(invocation{name: label + " on " + op, args: append(append([]string{}, mode...), op), seedTree: touchTree})
		}
		add(invocation{name: label + " on the operand tree", args: append(append([]string{}, mode...), "f", "d", "p", "missing", "d/inner", "lf", "dangling"), seedTree: touchTree})
	}
	tree("-h on a dangling symlink then the target", "-h", "dangling", "nowhere")
	tree("a failure between successes", "f", "nodir/x", "d")
	tree("a directory then a file", "d", "f")

	// ---- the -d and -t spellings ----
	for _, s := range []string{"", "now", "today", "tomorrow", "2024-06-15", "2024-06-15 12:34:56", "2024-06-15T12:34:56Z", "@0", "@1718434196.5", "next monday", "3 days ago", "-1 day", "x", "foo bar", "2024-13-01", "12:00:60", "TZ=\"Asia/Tokyo\" 2024-06-15 12:00", "2024-06-15 12:00 EDT", "24:00", "1 jan 12:00"} {
		tree("-d "+s, "-d", s, "f", "new")
		tree("-d "+s+" with -r", "-r", "ref", "-d", s, "f", "new")
		tree("-d "+s+" in New York", "-d", s, "f", "new")
		cases[len(cases)-1].env = []string{"TZ=America/New_York"}
	}
	tree("--date", "--date=2024-06-15", "f")
	tree("--da", "--da", "2024-06-15", "f")
	tree("--d is ambiguous", "--d", "2024-06-15", "f")
	tree("-d needs a value", "-d")
	tree("--date needs a value", "--date")
	tree("-d then no file", "-d", "2024-06-15")
	for _, s := range []string{"202406151234", "202406151234.56", "202406151234.60", "202406151234.61", "202406151234.6", "202406151234.", "2406151234", "06151234", "06151234.30", "6151234", "2024061512345", "20240615123456", "0002406151234", "202413151234", "202406321234", "202406152534", "202406151260", "20240615123x", "", " ", "-202406151234", "+202406151234", "202402291234", "202302291234", "196912312359.59", "203801190314.08", "999912312359.59", "000001010000", "691231120000", "681231120000", "12312359", "02301200", "20240615123456.5"} {
		tree("-t "+s, "-t", s, "f", "new")
		tree("-t "+s+" in New York", "-t", s, "f", "new")
		cases[len(cases)-1].env = []string{"TZ=America/New_York"}
	}
	tree("-t needs a value", "-t")
	tree("-t then no file", "-t", "202406151234")
	tree("-t and -d", "-t", "202406151234", "-d", "2024-06-15", "f")
	tree("-d and -t", "-d", "2024-06-15", "-t", "202406151234", "f")
	tree("-t and -r", "-t", "202406151234", "-r", "ref", "f")
	tree("-r and -t", "-r", "ref", "-t", "202406151234", "f")
	tree("-t -d -r", "-t", "202406151234", "-d", "x", "-r", "ref", "f")
	tree("-t twice", "-t", "202406151234", "-t", "202406151235", "f")
	tree("-d twice", "-d", "2024-06-15", "-d", "x", "f")
	tree("-r twice", "-r", "missing", "-r", "ref", "f")
	tree("-r a missing file", "-r", "missing", "f")
	tree("-r a quoted missing name", "-r", "mis'sing", "f")
	tree("-r a dangling symlink", "-r", "dangling", "f")
	tree("-r a dangling symlink with -h", "-h", "-r", "dangling", "f")
	tree("-r a directory", "-r", "d", "f")
	tree("-r a FIFO", "-r", "p", "f")
	tree("-r an empty name", "-r", "", "f")
	tree("--reference", "--reference=ref", "f")
	tree("--ref", "--ref", "ref", "f")
	tree("--r", "--r", "ref", "f")
	tree("-r needs a value", "-r")
	tree("-r then no file", "-r", "ref")

	// ---- --time ----
	for _, w := range []string{"atime", "access", "use", "mtime", "modify", "a", "ac", "m", "mod", "u", "x", "", "modif", "atime2", "ATIME", "a'b", "access,use"} {
		tree("--time="+w, "--time="+w, "f", "new")
	}
	tree("--time separate", "--time", "atime", "f")
	tree("--time needs a value", "--time")
	tree("--time then no file", "--time=atime")
	tree("--ti prefix", "--ti=atime", "f")
	tree("--t is ambiguous", "--t=atime", "f")
	tree("--time twice", "--time=atime", "--time=mtime", "f")
	tree("--time and -a -m", "--time=atime", "-m", "f")

	// ---- getopt faults ----
	tree("-x", "-x", "f")
	tree("--foo", "--foo", "f")
	tree("the empty long option", "--=x", "f")
	tree("-cx cluster", "-cx", "f")
	tree("-acmfh cluster", "-acmfh", "f")
	tree("-dfoo glued", "-d2024-06-15", "f")
	tree("-tfoo glued", "-t202406151234", "f")
	tree("-rref glued", "-rref", "f")
	tree("option after operand", "f", "-c", "new")
	add(invocation{name: "POSIXLY_CORRECT stops at the operand", args: []string{"f", "-c", "new"}, seedTree: touchTree, env: []string{"POSIXLY_CORRECT=1"}})
	tree("dashdash then an option-shaped name", "--", "-c")
	tree("dashdash then dashdash", "--", "--")
	tree("help with a value", "--help=x")
	tree("version with a value", "--version=1")
	add(invocation{name: "no operands"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "-c alone", args: []string{"-c"}})
	add(invocation{name: "-a -m alone", args: []string{"-a", "-m"}})

	// ---- the obsolete MMDDhhmm[YY] form ----
	for _, env := range [][]string{nil, {"_POSIX2_VERSION=199209"}, {"_POSIX2_VERSION=200112"}, {"_POSIX2_VERSION=199209", "POSIXLY_CORRECT=1"}, {"_POSIX2_VERSION=x"}} {
		label := "default posix"
		if env != nil {
			label = env[0]
		}
		for _, args := range [][]string{{"01011200", "f"}, {"01011200", "new"}, {"01011200", "f", "new"}, {"0101120024", "f"}, {"0101120068", "f"}, {"0101120069", "f"}, {"01011200", "f", "01011201"}, {"01011200"}, {"13011200", "f"}, {"01011200.30", "f"}, {"-c", "01011200", "f"}, {"-t", "01011200", "01011201", "f"}, {"-d", "2024-06-15", "01011200", "f"}, {"-a", "01011200", "f"}, {"1234567", "f"}, {"123456789", "f"}, {"010112002024", "f"}} {
			add(invocation{name: label + " " + quoteArgs(args), args: args, seedTree: touchTree, env: env})
		}
	}

	// ---- standard output as the operand ----
	tree("dash", "-")
	tree("dash with -c", "-c", "-")
	tree("dash with -a -m", "-a", "-m", "-")
	tree("dash among files", "f", "-", "new")
	tree("dash with -h", "-h", "-")
	tree("dash with -t", "-t", "202406151234", "-")
	sink := filepath.Join(t.TempDir(), "sink")
	if err := os.WriteFile(sink, []byte("s"), 0o644); err != nil {
		t.Fatalf("write sink: %v", err)
	}
	add(invocation{name: "dash to a file", args: []string{"-"}, stdoutPath: sink})
	add(invocation{name: "dash to a file with -c", args: []string{"-c", "-"}, stdoutPath: sink})
	add(invocation{name: "dash to a file with -t", args: []string{"-t", "202406151234", "-"}, stdoutPath: sink})
	add(invocation{name: "dash to a closed stdout", args: []string{"-"}, stdout: stdoutClosed})
	add(invocation{name: "dash to a closed stdout with -c", args: []string{"-c", "-"}, stdout: stdoutClosed})
	add(invocation{name: "dash to a closed stdout with -h", args: []string{"-h", "-"}, stdout: stdoutClosed})
	add(invocation{name: "dash to a closed stdout among files", args: []string{"f", "-", "new"}, seedTree: touchTree, stdout: stdoutClosed})
	add(invocation{name: "dash to /dev/full", args: []string{"-"}, stdout: stdoutFull})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"nodir/x"}, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"f"}, seedTree: touchTree, stdout: stdoutFull})

	return cases
}

func TestTouchParity(t *testing.T) {
	requireParity(t, "touch", touchCases(t))
}

func TestTouchHelpVersion(t *testing.T) {
	requireHelp(t, "touch", []string{"--help"}, 0)
	requireHelp(t, "touch", []string{"--hel"}, 0)
	requireHelp(t, "touch", []string{"--help", "extra"}, 0)
	requireHelp(t, "touch", []string{"f", "--help"}, 0)
	requireVersion(t, "touch", []string{"--version"}, 0)
	requireVersion(t, "touch", []string{"--vers"}, 0)
	requireVersion(t, "touch", []string{"--version", "f"}, 0)
}

// stampOf reads a path's access and modification times without
// following a symlink.
func stampOf(t *testing.T, path string) (string, bool) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return err.Error(), false
	}
	return time.Unix(st.Atim.Sec, st.Atim.Nsec).UTC().Format("2006-01-02T15:04:05.000000000") + " " +
		time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC().Format("2006-01-02T15:04:05.000000000"), true
}

// TestTouchTimestamps runs GNU touch and Fern's over identical trees
// for every way of naming a time that does not read the clock — `-t`
// stamps, absolute `-d` strings, `-r` with and without a relative
// `-d`, `-a`, `-m` and `-h` — under three zones, and compares the
// times each leaves on every entry. The reference file's own stamps
// are the base of the relative strings, so a DST gap and a repeated
// hour in the named zone are both crossed.
func TestTouchTimestamps(t *testing.T) {
	gnu := referenceBin(t, "touch")
	ours := fernBin(t, "touch")
	type stampCase struct {
		env  []string
		args []string
	}
	var cases []stampCase
	for _, zone := range []string{"UTC0", "America/New_York", "Australia/Sydney"} {
		env := []string{"TZ=" + zone}
		for _, args := range [][]string{
			{"-t", "202406151234", "f", "new"},
			{"-t", "202406151234.56", "f", "lf", "d"},
			{"-t", "202406151234.60", "f"},
			{"-t", "2406151234", "f"},
			{"-t", "06151234", "f"},
			{"-t", "6912312359.59", "f"},
			{"-t", "6801010000", "f"},
			{"-t", "202403100230", "f"},
			{"-t", "202411030130", "f"},
			{"-a", "-t", "202001010000", "f"},
			{"-m", "-t", "202001010000", "f"},
			{"-h", "-t", "202001010000", "lf", "dangling"},
			{"-c", "-t", "202001010000", "f", "missing"},
			{"-d", "2024-06-15 12:34:56.123456789", "f", "new"},
			{"-d", "@1718434196.987654321", "f"},
			{"-d", "2024-06-15T12:34:56Z", "f"},
			{"-d", "2024-06-15 12:34:56 +0530", "f"},
			{"-d", "TZ=\"Asia/Tokyo\" 2024-06-15 12:34:56", "f"},
			{"-d", "2024-03-10 02:30 -0500", "f"},
			{"-d", "2024-11-03 01:30", "f"},
			{"-d", "2024-11-03 01:30 EST", "f"},
			{"-d", "2024-11-03 01:30 EDT", "f"},
			{"-d", "2024-03-09 02:30 tomorrow", "f"},
			{"-d", "1969-12-31 23:59:59.5", "f"},
			{"-d", "0001-01-01", "f"},
			{"-d", "99999-12-31 23:59:59", "f"},
			{"-r", "ref", "f", "new", "lf"},
			{"-r", "ref", "-a", "f"},
			{"-r", "ref", "-m", "f"},
			{"-r", "ref", "-h", "lf", "dangling"},
			{"-h", "-r", "lref", "f"},
			{"-r", "lref", "f"},
			{"-r", "ref", "-d", "+1 day", "f"},
			{"-r", "ref", "-d", "-1 day", "f"},
			{"-r", "ref", "-d", "1 year ago", "f"},
			{"-r", "ref", "-d", "+1 month -2 hours", "f"},
			{"-r", "ref", "-d", "next monday", "f"},
			{"-r", "ref", "-d", "12:00", "f"},
			{"-r", "ref", "-d", "tomorrow", "f"},
			{"-r", "ref", "-d", "0.5 seconds", "f"},
			{"-r", "ref", "-d", "-0.000000001 seconds", "f"},
			{"-r", "ref", "-d", "now", "f"},
			{"-r", "ref", "-d", "", "f"},
			{"-r", "ref", "-d", "@0", "f"},
			{"-r", "ref", "-d", "2024-06-15", "f"},
			{"-r", "ref", "-a", "-d", "+1 day", "f"},
			{"-r", "ref", "-m", "-d", "+1 day", "f"},
			{"-r", "gap", "-d", "+1 day", "f"},
			{"-r", "gap", "-d", "tomorrow", "f"},
			{"-r", "gap", "-d", "12:00", "f"},
			{"-r", "gap", "-d", "next sunday", "f"},
			{"-r", "fold", "-d", "+1 hour", "f"},
			{"-r", "fold", "-d", "tomorrow", "f"},
			{"-r", "fold", "-d", "yesterday", "f"},
			{"-r", "fold", "-d", "01:30", "f"},
			{"-r", "fold", "-d", "-1 day", "f"},
			{"-r", "fold", "-d", "EDT", "f"},
			{"-r", "fold", "-d", "EST", "f"},
			{"-r", "fold", "-a", "-d", "EDT", "f"},
			{"-r", "split", "-d", "tomorrow", "f"},
			{"-r", "split", "-d", "1 hour ago", "f"},
			{"-r", "split", "-a", "-d", "1 hour ago", "f"},
		} {
			cases = append(cases, stampCase{env: env, args: args})
		}
	}
	cases = append(cases, stampCase{env: []string{"TZ=UTC0", "_POSIX2_VERSION=199209"}, args: []string{"01011200", "f", "new"}})
	cases = append(cases, stampCase{env: []string{"TZ=America/New_York", "_POSIX2_VERSION=199209"}, args: []string{"0101120069", "f"}})

	seed := func(dir string) {
		touchTree(t, dir)
		// A reference whose access time sits in the New York gap of
		// 2024-03-10 and whose modification time sits in the repeated
		// hour of 2024-11-03, and one whose two times are on either
		// side of a change, so a relative string moves them apart.
		for _, r := range []struct {
			name   string
			at, mt time.Time
		}{
			{"gap", time.Unix(1710055800, 0), time.Unix(1710055800, 5)},
			{"fold", time.Unix(1730611800, 0), time.Unix(1730615400, 0)},
			{"split", time.Unix(1710050400, 0), time.Unix(1710057600, 0)},
		} {
			if err := os.WriteFile(filepath.Join(dir, r.name), []byte("r"), 0o644); err != nil {
				t.Fatalf("write %s: %v", r.name, err)
			}
			if err := os.Chtimes(filepath.Join(dir, r.name), r.at, r.mt); err != nil {
				t.Fatalf("chtimes %s: %v", r.name, err)
			}
		}
		if err := os.Symlink("ref", filepath.Join(dir, "lref")); err != nil {
			t.Fatalf("symlink lref: %v", err)
		}
		if err := os.Chtimes(filepath.Join(dir, "f"), time.Unix(1500000000, 333333333), time.Unix(1500000001, 444444444)); err != nil {
			t.Fatalf("chtimes f: %v", err)
		}
	}
	// Only the operands are compared: an entry the case does not name
	// keeps the instant its tree was seeded at, and the two trees are
	// seeded moments apart.
	operands := func(args []string) []string {
		var names []string
		skip := false
		for _, a := range args {
			if skip {
				skip = false
				continue
			}
			if a == "-r" || a == "-d" || a == "-t" {
				skip = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			names = append(names, a)
		}
		return names
	}
	run := func(bin, dir string, c stampCase) string {
		cmd := exec.Command(bin, c.args...)
		cmd.Dir = dir
		cmd.Env = append(baseEnv(), c.env...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	for _, c := range cases {
		name := quoteArgs(append(append([]string{}, c.env...), c.args...))
		want := t.TempDir()
		got := t.TempDir()
		seed(want)
		seed(got)
		wantOut := run(gnu, want, c)
		gotOut := run(ours, got, c)
		for _, n := range operands(c.args) {
			ws, wok := stampOf(t, filepath.Join(want, n))
			gs, gok := stampOf(t, filepath.Join(got, n))
			// A symlink's own access time moves on the first lookup
			// through it, at whatever instant that lookup happens; only
			// -h names the link itself.
			if (n == "lf" || n == "lref" || n == "dangling") && c.args[0] != "-h" && wok && gok {
				ws = ws[len(ws)/2:]
				gs = gs[len(gs)/2:]
			}
			if wok != gok || (wok && ws != gs) {
				t.Errorf("%s: %s\n  gnu:  %s\n  fern: %s\n  gnu said:  %q\n  fern said: %q", name, n, ws, gs, wantOut, gotOut)
			}
		}
	}
}
