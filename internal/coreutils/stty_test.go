// The `stty` corpus. Every case runs with a REAL pseudo-terminal on fd 0,
// because that is the only descriptor shape the utility can answer for: on a
// pipe every invocation is `Inappropriate ioctl for device` and a piped
// corpus would prove one message and nothing else.
//
// `ttyState` is what makes the setting half gateable. `stty -echo` writes
// nothing at all, so without the terminal's own state in the comparison its
// case would compare two empty streams and pass on a utility that parsed the
// operand and made no call. With it, the four flag words, the line
// discipline, every control character and the size all have to match.
//
// `ttyPre` gives a case a starting state, established by the reference
// binary on the same terminal, so the deviations `stty` prints are the ones
// the case means rather than a fresh pty's. Several settings are only
// reachable that way: `-brkint` from a state where brkint is on, `raw`
// undoing an `iutf8`, the min/time line in the default form.
package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The boolean flags, in the order `-a` prints them. Every one is exercised
// set and cleared; the two a pseudo-terminal refuses — `parenb` and `-cread`
// — are exercised too, for their error rather than for their bit.
func sttyFlags() []string {
	return strings.Fields(`
		parenb parodd cmspar hupcl cstopb cread clocal crtscts
		ignbrk brkint ignpar parmrk inpck istrip inlcr igncr icrnl ixon
		ixoff iuclc ixany imaxbel iutf8
		opost olcuc ocrnl onlcr onocr onlret ofill ofdel
		isig icanon iexten echo echoe echok echonl noflsh xcase tostop
		echoprt echoctl echoke flusho extproc
		hup tandem ctlecho crterase crtkill prterase`)
}

// The multi-valued sets: the character size and the six delay styles. None
// takes a `-` spelling, which is half of what their cases check.
func sttyGroups() []string {
	return strings.Fields(`
		cs5 cs6 cs7 cs8 nl0 nl1 cr0 cr1 cr2 cr3 tab0 tab1 tab2 tab3
		bs0 bs1 vt0 vt1 ff0 ff1`)
}

// The combination settings, each of which expands to a list of the above.
func sttyCombos() []string {
	return strings.Fields(`
		sane raw -raw cooked -cooked cbreak -cbreak ek evenp -evenp oddp -oddp
		parity -parity nl -nl lcase -lcase LCASE -LCASE crt dec decctlq
		-decctlq tabs -tabs litout -litout pass8 -pass8 drain -drain`)
}

func sttyControlChars() []string {
	return strings.Fields(`
		intr quit erase kill eof eol eol2 swtch start stop susp rprnt
		werase lnext discard flush`)
}

// The starting states a case can ask for. Each is reachable with one
// reference invocation, and between them they turn every flag `sane` names
// both ways, so a `-brkint` case has a brkint to clear.
func sttyPres() map[string][]string {
	return map[string][]string{
		"fresh":    nil,
		"raw":      {"raw"},
		"sane":     {"sane"},
		"noncanon": {"-icanon", "min", "5", "time", "2"},
		"chars":    {"erase", "X", "intr", "^A"},
		"delays":   {"nl1", "cr3", "tab3", "bs1", "vt1", "ff1", "-opost", "olcuc"},
		"slow":     {"300"},
		"resized":  {"rows", "40", "columns", "100"},
		"iflags":   {"ixoff", "iutf8", "-icrnl"},
		"lflags":   {"-echo", "-echoe", "echonl", "tostop"},
	}
}

func init() {
	registerCorpus("stty", sttyCases)
}

func TestStty(t *testing.T) {
	requireParity(t, "stty", sttyCases(t))
}

func sttyCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, ttyIn: true, ttyState: true})
	}
	addFrom := func(name string, pre []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, ttyIn: true, ttyState: true, ttyPre: pre})
	}

	// Every flag, set and cleared, from a fresh terminal and from `raw` —
	// which has most of them the other way round, so the pair covers both
	// directions of every bit.
	for _, f := range sttyFlags() {
		add("set "+f, f)
		add("clear "+f, "-"+f)
		addFrom("raw then "+f, []string{"raw"}, f)
		addFrom("raw then -"+f, []string{"raw"}, "-"+f)
	}
	// Every member of every multi-valued set, and the `-` spelling none of
	// them accepts.
	for _, g := range sttyGroups() {
		add("set "+g, g)
		add("negate "+g, "-"+g)
	}
	// Every combination, from each starting state that makes it interesting.
	for _, c := range sttyCombos() {
		add("combo "+c, c)
		addFrom("combo "+c+" from raw", []string{"raw"}, c)
		addFrom("combo "+c+" from delays", []string{"nl1", "cr3", "tab3", "-opost", "olcuc"}, c)
		addFrom("combo "+c+" from iflags", []string{"ixoff", "iutf8", "-icrnl"}, c)
		addFrom("combo "+c+" from chars", []string{"erase", "X", "intr", "^A", "eof", "Z"}, c)
	}
	// Every control character, through every spelling a value has.
	for _, c := range sttyControlChars() {
		for _, v := range []string{"^C", "^?", "^-", "undef", "", "Z", "9", "0", "0x41", "65", "0177", "255", "^@", "^i"} {
			add("cchar "+c+" "+v, c, v)
		}
		add("cchar "+c+" out of range", c, "256")
		add("cchar "+c+" not a number", c, "ab")
		add("cchar "+c+" negative", c, "-1")
		add("cchar "+c+" past the parse limit", c, "1073741824")
		add("cchar "+c+" missing", c)
	}
	// Every speed, as the bare form and through both halves.
	for _, s := range strings.Fields(`0 50 75 110 134 134.5 150 200 300 600 1200 1800 2400
		4800 9600 19200 exta 38400 extb 57600 115200 230400 460800 500000 576000 921600
		1000000 1152000 1500000 2000000 2500000 3000000 3500000 4000000`) {
		add("speed "+s, s)
		add("ispeed "+s, "ispeed", s)
		add("ospeed "+s, "ospeed", s)
	}
	// The valued settings, over the values that decide which diagnostic they
	// reach: the kernel's own truncation, the two parse limits, and the two
	// range messages.
	for _, k := range []string{"rows", "cols", "columns", "line", "min", "time"} {
		for _, v := range []string{"0", "1", "40", "255", "256", "65535", "65536",
			"1073741823", "1073741824", "2147483647", "2147483648",
			"18446744073709551615", "18446744073709551616", "-1", "abc", "0x10", "010", ""} {
			add(k+" "+v, k, v)
		}
		add(k+" missing", k)
	}

	cases = append(cases, sttyOutputStyleCases()...)
	cases = append(cases, sttyOptionScanCases()...)
	cases = append(cases, sttySavedFormCases()...)
	cases = append(cases, sttySequenceCases()...)
	cases = append(cases, sttyDeviceCases(t)...)
	return cases
}

// The three print forms, from every starting state: the default's deviations
// from `sane`, `-a`'s full dump, and `-g`'s hex line.
func sttyOutputStyleCases() []invocation {
	var cases []invocation
	for name, pre := range sttyPres() {
		for _, style := range [][]string{nil, {"-a"}, {"-g"}, {"size"}, {"speed"}} {
			label := "default"
			if len(style) > 0 {
				label = style[0]
			}
			cases = append(cases, invocation{
				name: label + " from " + name, args: style,
				ttyIn: true, ttyState: true, ttyPre: pre,
			})
		}
	}
	return cases
}

// The option scan, which is not a getopt call: an element that looks like an
// option and is not becomes a mode, whole, and `--` means one thing before
// the first mode and another after it.
func sttyOptionScanCases() []invocation {
	args := [][]string{
		{"--all"}, {"--save"}, {"--al"}, {"--s"}, {"--a"},
		{"-ag"}, {"-ga"}, {"-a", "-g"}, {"-a", "-echo"}, {"-echo", "-a"},
		// `--h` and `--v` are not here: they print help and version, whose
		// comparison is the two exemptions in docs/COREUTILS.md rather than
		// byte-for-byte parity. TestSttyHelpVersion has them.
		{"erase", "-a"}, {"-aecho"}, {"-igna"}, {"-e"}, {"-x"}, {"--bogus"},
		{"-"}, {"--"}, {"--", "-echo"}, {"-echo", "--"}, {"-echo", "--", "-icanon"},
		{"erase", "X", "--"}, {"-a", "--"}, {"-a", "--", "-echo"},
		{"erase", "X", "--", "-echo"}, {"-g", "--"}, {"--all=x"}, {"--save=x"},
		{"--help=x"}, {"-a", "drain"}, {"-g", "drain"}, {"-a", "size"},
		{"size", "-a"}, {"-a", "bogus"}, {"bogus"}, {""}, {"1"}, {"99"},
		{"010"}, {"0x10"}, {"-cs8"}, {"-nl0"}, {"-sane"}, {"-crt"}, {"-intr"},
		{"-erase", "X"}, {"-rows", "40"}, {"-9600"}, {"ispeed", "bogus"},
		{"ospeed", "bogus"}, {"ispeed", "300", "ospeed", "9600"},
		{"ispeed", "300", "ospeed", "300"}, {"ispeed", "exta", "ospeed", "9600"},
	}
	var cases []invocation
	for _, a := range args {
		cases = append(cases, invocation{
			name: "scan " + strings.Join(a, " "), args: a, ttyIn: true, ttyState: true,
		})
	}
	return cases
}

// The `-g` round trip, which is the reason the words are the kernel's and not
// a numbering of ours: the line it prints has to be readable back.
func sttySavedFormCases() []invocation {
	const saved = "500:5:bf:8a3b:3:1c:7f:15:4:0:1:0:11:13:1a:0:12:f:17:16:0:0:0:0:0:0:0:0:0:0:0:0:0:0:0:0"
	f := strings.Split(saved, ":")
	args := [][]string{
		{saved},
		{saved, "-echo"},
		{"-echo", saved},
		{strings.Replace(saved, "8a3b", "8a33", 1)},
		{strings.Replace(saved, "500", "0x500", 1)},
		{strings.Replace(saved, ":3:", ":ff:", 1)},
		{"0:0:bf:0:" + strings.Repeat("0:", 31) + "0"},
		{strings.Join(f[:35], ":")},
		{saved + ":0"},
		{strings.Join(f[:20], ":")},
		{strings.Replace(saved, "500", "zzz", 1)},
		{strings.Replace(saved, "500", "", 1)},
		{"1:2:3"},
		{"rows", "40", "1:2:3"},
	}
	var cases []invocation
	for i, a := range args {
		cases = append(cases, invocation{
			name: "saved " + itoa(i), args: a, ttyIn: true, ttyState: true,
			ttyPre: []string{"raw", "erase", "X"},
		})
	}
	return cases
}

// Sequences, where the order decides what lands before the failure: the
// window size goes in as it is read while the flags go in once at the end,
// and one pass reads every value except the window size's.
func sttySequenceCases() []invocation {
	args := [][]string{
		{"rows", "40", "bogus"}, {"rows", "40", "cs7"}, {"rows", "40", "rows", "-1"},
		{"rows", "40", "cols", "-1"}, {"cols", "100", "rows", "-1"},
		{"rows", "40", "erase", "ab"}, {"rows", "40", "line", "abc"},
		{"rows", "40", "time", "256"}, {"erase", "ab", "rows", "40"},
		{"rows", "-1", "rows", "40"}, {"rows", "40", "-echo", "rows", "-1"},
		{"rows", "40", "size"}, {"size", "rows", "40"}, {"size", "speed"},
		{"speed", "size"}, {"9600", "speed"}, {"speed", "9600"},
		{"size", "-echo"}, {"-echo", "size"}, {"drain", "-echo"},
		{"-drain", "-echo"}, {"-echo", "-echo"}, {"-echo", "cs7"},
		{"cs5", "cs7"}, {"cs7", "echo", "eol", "^M"}, {"cs7", "eol", "^M"},
		{"sane", "-echo"}, {"-echo", "sane"}, {"sane", "size"},
		{"raw", "-echo", "erase", "^H"}, {"-icanon", "min", "3", "time", "7"},
		{"line", "3", "-echo"}, {"line", "300"}, {"line", "65536"},
		{"9600", "cs7", "-echo", "erase", "Z"},
	}
	var cases []invocation
	for _, a := range args {
		cases = append(cases, invocation{
			name: "sequence " + strings.Join(a, " "), args: a, ttyIn: true, ttyState: true,
		})
	}
	return cases
}

// `-F DEVICE` reads and writes a terminal the utility opened ITSELF, which is
// the half that needs the handle forms of the four primitives: a Reader
// surrenders no descriptor number. Every case here leaves fd 0 a pipe, so
// nothing could be answered by the standard descriptors instead.
func sttyDeviceCases(t *testing.T) []invocation {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := [][]string{
		{"-F", "/dev/null"}, {"-F", "/dev/null", "-a"}, {"-F", "/dev/null", "-g"},
		{"-F", "/dev/null", "-echo"}, {"-F", "/nonexistent"},
		{"-F", filepath.Join(dir, "fifo")}, {"-F", filepath.Join(dir, "dir")},
		{"-F", filepath.Join(dir, "plain")}, {"-F", filepath.Join(dir, "dangling")},
		{"-F", ""}, {"--file="}, {"--file=/dev/null"}, {"--fil=/dev/null"},
		{"-F/dev/null"}, {"-Fa"}, {"-F", "-a"}, {"-F"},
		{"-F", "/dev/null", "-F", "/dev/tty"},
		{"--file=/dev/null", "--file=/dev/null"},
		{"-echo", "-F", "/dev/null"}, {"bogus", "-F", "/dev/null"},
		{"-a", "bogus", "-F", "/dev/null"}, {"-F", "/dev/tty"},
		// With no -F at all, fd 0 is a pipe: the one message a non-terminal
		// has, under the name the utility uses when it was given none.
		nil, {"-a"}, {"-g"}, {"-echo"}, {"size"}, {"speed"},
		// /dev/ptmx is a terminal a case can have WITHOUT the harness's
		// pty: every open of it is a fresh pseudo-terminal MASTER, which
		// answers TCGETS, TCSETS and TIOCGWINSZ and dies with the process.
		// So these are the only cases that drive the whole of `-F` — the
		// print forms and the SETS — against something that answers, where
		// /dev/null answers ENOTTY to all of it. The master's own state is
		// gone by the time the run ends, so what they compare is the output
		// and the status; a fresh master's size is 0x0, which is what makes
		// them deterministic.
		{"-F", "/dev/ptmx", "-a"}, {"-F", "/dev/ptmx", "-g"},
		{"-F", "/dev/ptmx"}, {"-F", "/dev/ptmx", "size"},
		{"-F", "/dev/ptmx", "speed"}, {"-F", "/dev/ptmx", "-echo"},
		{"-F", "/dev/ptmx", "sane"}, {"-F", "/dev/ptmx", "raw"},
		{"-F", "/dev/ptmx", "cs7"}, {"-F", "/dev/ptmx", "cs5"},
		{"-F", "/dev/ptmx", "parenb"}, {"-F", "/dev/ptmx", "-cread"},
		{"-F", "/dev/ptmx", "oddp"}, {"-F", "/dev/ptmx", "0"},
		{"-F", "/dev/ptmx", "ispeed", "0"}, {"-F", "/dev/ptmx", "ospeed", "0"},
		{"-F", "/dev/ptmx", "rows", "40", "cols", "100"},
		{"-F", "/dev/ptmx", "-echo", "cs7"},
		{"-F", "/dev/ptmx", "erase", "X", "intr", "^A"},
		{"-F", "/dev/ptmx", "line", "300"},
		{"-F", "/dev/ptmx", "9600"}, {"-F", "/dev/ptmx", "bogus"},
	}
	var cases []invocation
	for _, a := range args {
		cases = append(cases, invocation{name: "device " + strings.Join(a, " "), args: a})
	}
	return cases
}

func TestSttyHelpVersion(t *testing.T) {
	requireHelp(t, "stty", []string{"--help"}, 0)
	requireVersion(t, "stty", []string{"--version"}, 0)
	// The unique-prefix spellings reach the same two, through the scan's own
	// long-option matching rather than through glibc's.
	requireHelp(t, "stty", []string{"--h"}, 0)
	requireVersion(t, "stty", []string{"--v"}, 0)
}
