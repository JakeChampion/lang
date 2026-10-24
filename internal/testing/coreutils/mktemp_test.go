package coreutils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mktempSeed(t *testing.T, dir string) {
	t.Helper()
	at := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	for _, d := range []string{"td1", "td1/a", "td2", "dXXXX"} {
		if err := os.MkdirAll(at(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(at("afile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	links := [][2]string{
		{"afile", "symtofile"},
		{"loopa", "loopb"},
		{"loopb", "loopa"},
		{"/nonexistent-target", "danglingdir"},
	}
	for _, l := range links {
		if err := os.Symlink(l[0], at(l[1])); err != nil {
			t.Fatal(err)
		}
	}
}

const alnum62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func inAlnum62(b byte) bool { return strings.IndexByte(alnum62, b) >= 0 }

func maskRandom(run, sufLen int) func(string) string {
	return func(s string) string {
		body, nl := s, ""
		if strings.HasSuffix(body, "\n") {
			body, nl = body[:len(body)-1], "\n"
		}
		end := len(body) - sufLen
		start := end - run
		if start < 0 || end > len(body) {
			return s
		}
		b := []byte(body)
		for i := start; i < end; i++ {
			if inAlnum62(b[i]) {
				b[i] = 'X'
			}
		}
		return string(b) + nl
	}
}

func mktempCase(name string, args ...string) invocation {
	return invocation{name: name, args: args, seedTree: mktempSeed}
}

func mktempShape(name string, run, sufLen int, args ...string) invocation {
	inv := mktempCase(name, args...)
	inv.mask = maskRandom(run, sufLen)
	return inv
}

func mktempEnv(inv invocation, env ...string) invocation {
	inv.env = env
	return inv
}

func init() {
	registerCorpus("mktemp", mktempCases)
}

func mktempCases(*testing.T) []invocation {
	long := strings.Repeat("a", 300)
	return []invocation{
		mktempShape("no operand", 10, 0, "-u"),
		mktempShape("dash dash alone", 10, 0, "-u", "--"),
		mktempShape("dash dash with -d", 10, 0, "-u", "-d", "--"),
		mktempCase("two templates", "aXXX", "bXXX"),
		mktempCase("three templates", "aXXX", "bXXX", "cXXX"),
		mktempCase("two templates after dash dash", "-u", "--", "a", "b"),
		mktempCase("quiet does not cover too many templates", "-q", "aXXX", "bXXX"),
		mktempCase("lone dash is an operand", "-"),
		mktempCase("lone dash after dash dash", "--", "-"),
		mktempCase("dash d after dash dash", "--", "-d"),
		mktempShape("leading dash operand", 6, 0, "-u", "--", "-XXXXXX"),
		mktempCase("leading dash without dash dash", "-XXXXXX"),

		mktempShape("only the last run is replaced", 3, 0, "-u", "fooXXXXbarXXX"),
		mktempShape("run before a literal tail", 3, 3, "-u", "XXXfoo"),
		mktempShape("three is the minimum", 3, 0, "-u", "XXX"),
		mktempShape("twenty random characters", 20, 0, "-u", "fooXXXXXXXXXXXXXXXXXXXX"),
		mktempShape("forty random characters", 40, 0, "-u", strings.Repeat("X", 40)),
		mktempShape("a space is part of the suffix", 6, 1, "-u", "XXXXXX "),
		mktempShape("an interior space keeps the run at the end", 3, 0, "-u", "XXX XXX"),
		mktempShape("leading spaces are literal", 3, 0, "-u", "  spaceXXX"),
		mktempCase("an earlier run does not count", "fooXXXbarXX"),
		mktempCase("too few X", "XX"),
		mktempCase("no X at all", "foo"),
		mktempCase("no X and a separator", "a/b"),
		mktempCase("no X and absolute", "/tmp/foo"),
		mktempCase("a lone slash", "/"),
		mktempCase("an empty template", ""),
		mktempCase("quiet does not cover too few X", "-q", "XX"),

		mktempCase("a trailing slash is the suffix", "XXX/"),
		mktempCase("one X still reaches the suffix check", "X/"),
		mktempCase("the derived suffix names the text", "dXXXX/foo"),

		mktempShape("glued suffix", 6, 4, "-u", "--suffix=.txt", "fooXXXXXX"),
		mktempShape("separate suffix", 6, 2, "-u", "--suffix", ".x", "fooXXXXXX"),
		mktempShape("last suffix wins", 6, 2, "-u", "--suffix=.a", "--suffix=.b", "fooXXXXXX"),
		mktempShape("an empty suffix is no suffix", 6, 0, "-u", "--suffix=", "fooXXXXXX"),
		mktempShape("a suffix of X is literal", 3, 1, "-u", "--suffix=X", "fooXXX"),
		mktempShape("suffix on the default template", 10, 2, "-u", "--suffix=.x"),
		mktempShape("suffix on the default template with -d", 10, 2, "-u", "-d", "--suffix=.x"),
		mktempCase("suffix requires an argument", "--suffix"),
		mktempCase("suffix abbreviated to --su", "--su"),
		mktempCase("suffix abbreviated to --s", "--s"),
		mktempCase("suffix with a separator", "--suffix=/x", "fooXXXXXX"),
		mktempCase("quiet does not cover an invalid suffix", "-q", "--suffix=/x", "fooXXXXXX"),
		mktempCase("must end in X beats the separator", "--suffix=/x", "XXXXXXfoo"),
		mktempCase("the separator beats too few X", "--suffix=/x", "XX"),
		mktempCase("the suffix is concatenated in the message", "--suffix=.z", "fooXX"),
		mktempCase("a suffix of X on a template without one", "--suffix=XXX", "foo"),
		mktempCase("a suffix of X does not lengthen the run", "--suffix=XXX", "fooX"),
		mktempCase("suffix on an empty template", "--suffix=.x", ""),
		mktempShape("suffix swallows --help", 10, 6, "-u", "--suffix", "--help"),

		mktempShape("short p", 6, 0, "-u", "-p", "td1", "XXXXXX"),
		mktempShape("short p glued", 3, 0, "-u", "-ptd1", "fooXXX"),
		mktempShape("short p takes the rest of a cluster", 3, 0, "-u", "-pt", "fooXXX"),
		mktempShape("short p takes an equals sign literally", 6, 0, "-u", "-p=td1", "XXXXXX"),
		mktempShape("short p swallows an option", 6, 0, "-u", "-p", "-d", "XXXXXX"),
		mktempShape("long tmpdir glued", 6, 0, "-u", "--tmpdir=td1", "XXXXXX"),
		mktempShape("tmpdir abbreviated", 10, 0, "-u", "--t"),
		mktempShape("bare tmpdir resets the directory", 6, 0, "-u", "--tmpdir=td1", "--tmpdir", "XXXXXX"),
		mktempShape("bare tmpdir first", 6, 0, "-u", "--tmpdir", "--tmpdir=td1", "XXXXXX"),
		mktempShape("bare tmpdir resets a -p", 6, 0, "-u", "-p", "td1", "--tmpdir", "XXXXXX"),
		mktempShape("a -p after a bare tmpdir", 6, 0, "-u", "--tmpdir", "-p", "td1", "XXXXXX"),
		mktempShape("last -p wins", 6, 0, "-u", "-p", "td1", "-p", "td2", "XXXXXX"),
		mktempShape("an empty -p is no directory", 6, 0, "-u", "-p", "", "XXXXXX"),
		mktempCase("short p requires an argument", "-p"),
		mktempCase("the long tmpdir does not take a separate token", "--tmpdir", "td1", "fooXXX"),
		mktempCase("absolute under tmpdir", "--tmpdir=", "/abs/XXXXXX"),
		mktempCase("absolute under an empty -p", "-p", "", "/abs/XXXXXX"),
		mktempCase("too few X beats the absolute check", "--tmpdir", "/abs/XX"),

		mktempShape("trailing slashes are dropped", 6, 0, "-u", "-p", "td1//", "XXXXXX"),
		mktempShape("one trailing slash is dropped", 6, 0, "-u", "-p", "td1/", "XXXXXX"),
		mktempShape("an all-slash directory is kept", 6, 0, "-u", "-p", "/", "XXXXXX"),
		mktempShape("three slashes are kept", 6, 0, "-u", "-p", "///", "XXXXXX"),
		mktempShape("two slashes are kept", 6, 0, "-u", "-p", "//", "XXXXXX"),
		mktempShape("interior slashes are left alone", 6, 0, "-u", "-p", "a//b//", "XXXXXX"),
		mktempShape("a dot directory", 6, 0, "-u", "-p", ".", "XXXXXX"),
		mktempShape("a dotdot directory", 6, 0, "-u", "-p", "..", "XXXXXX"),
		mktempShape("the template's own dot is not normalised", 3, 0, "-u", "--tmpdir=td1", "./fooXXX"),
		mktempShape("the template's own dotdot is not normalised", 3, 0, "-u", "--tmpdir=td1", "../fooXXX"),
		mktempEnv(mktempShape("a TMPDIR of trailing slashes", 10, 0, "-u"), "TMPDIR=/tmp///"),
		mktempEnv(mktempShape("a TMPDIR of two slashes", 10, 0, "-u"), "TMPDIR=//"),

		mktempShape("-t with no TMPDIR", 3, 0, "-u", "-t", "fooXXX"),
		mktempShape("-t with no operand", 10, 0, "-u", "-t"),
		mktempShape("-t with -d and no operand", 10, 0, "-u", "-d", "-t"),
		mktempShape("-t falls back to -p", 3, 0, "-u", "-t", "-p", "td1", "fooXXX"),
		mktempShape("-t with an empty -p", 3, 0, "-u", "-t", "-p", "", "fooXXX"),
		mktempShape("-t in a cluster", 3, 0, "-tu", "fooXXX"),
		mktempShape("-t after --", 3, 0, "-u", "-t", "--", "-XXX"),
		mktempEnv(mktempShape("TMPDIR outranks --tmpdir under -t", 3, 0, "-u", "-t", "--tmpdir=/P", "fooXXX"), "TMPDIR=/T"),
		mktempEnv(mktempShape("-t is sticky whatever the order", 3, 0, "-u", "--tmpdir=/P", "-t", "fooXXX"), "TMPDIR=/T"),
		mktempEnv(mktempShape("--tmpdir outranks TMPDIR without -t", 3, 0, "-u", "--tmpdir=/P", "fooXXX"), "TMPDIR=/T"),
		mktempEnv(mktempShape("-p outranks TMPDIR without -t", 3, 0, "-u", "-p", "td1", "fooXXX"), "TMPDIR=/T"),
		mktempEnv(mktempShape("TMPDIR outranks -p under -t", 10, 0, "-u", "-t"), "TMPDIR=/T"),
		mktempEnv(mktempShape("an empty TMPDIR falls through to -p", 3, 0, "-u", "-t", "-p", "td1", "fooXXX"), "TMPDIR="),
		mktempEnv(mktempShape("an empty TMPDIR is /tmp", 10, 0, "-u"), "TMPDIR="),
		mktempCase("-t refuses a separator", "-t", "a/bXXX"),
		mktempCase("-t reports the concatenated template", "--suffix=.x", "-t", "a/bXXX"),
		mktempCase("too few X beats the -t separator check", "-t", "a/bXX"),
		mktempCase("the suffix check beats the -t separator check", "-t", "XXX/"),
		mktempCase("-t with an absolute -p", "-t", "--tmpdir=/d", "aXXXX"),

		mktempShape("create a file", 6, 0, "XXXXXX"),
		mktempShape("create a directory", 6, 0, "-d", "XXXXXX"),
		mktempShape("-d twice", 6, 0, "-d", "-d", "XXXXXX"),
		mktempShape("create with three X", 3, 0, "XXX"),
		mktempShape("create with twenty X", 20, 0, "fooXXXXXXXXXXXXXXXXXXXX"),
		mktempShape("create with a literal tail", 3, 3, "XXXfoo"),
		mktempShape("create with only the last run replaced", 3, 0, "fooXXXXbarXXX"),
		mktempShape("create with a suffix", 6, 4, "--suffix=.txt", "fooXXXXXX"),
		mktempShape("create under -p", 6, 0, "-p", "td1", "XXXXXX"),
		mktempShape("create a directory under -p", 6, 0, "-d", "-p", "td1", "XXXXXX"),
		mktempShape("only the final component is created", 6, 0, "-p", "td1", "a/XXXXXX"),
		mktempShape("create quietly", 6, 0, "-q", "XXXXXX"),
		mktempEnv(mktempShape("create under TMPDIR", 10, 0), "TMPDIR=."),
		mktempEnv(mktempShape("create a directory under TMPDIR", 10, 0, "-d"), "TMPDIR=."),
		mktempEnv(mktempShape("create under -t", 3, 0, "-t", "fooXXX"), "TMPDIR=."),
		mktempCase("a missing directory", "-p", "/nonexistent", "XXXXXX"),
		mktempCase("quiet covers a missing directory", "-q", "-p", "/nonexistent", "XXXXXX"),
		mktempShape("-u does not mind a missing directory", 6, 0, "-u", "-p", "/nonexistent", "XXXXXX"),
		mktempCase("a missing component of the template", "-p", "td1", "b/XXXXXX"),
		mktempCase("the message shows the joined template", "--tmpdir=td1", "--suffix=.x", "sub/fooXXX"),
		mktempCase("a file where a directory is wanted", "afile/XXXXXX"),
		mktempCase("a file where a directory is wanted, -d", "-d", "afile/XXXXXX"),
		mktempCase("a symlink to a file as -p", "-p", "symtofile", "XXXXXX"),
		mktempCase("a symlink loop", "loopa/XXXXXX"),
		mktempCase("a symlink loop, -d", "-d", "loopa/XXXXXX"),
		mktempCase("a name too long", long+"XXXXXX"),
		mktempCase("a name too long, -d", "-d", long+"XXXXXX"),
		mktempCase("a name too long, -u", "-u", long+"XXXXXX"),
		mktempEnv(mktempCase("TMPDIR is not a directory"), "TMPDIR=./afile"),

		mktempCase("-u meets ENOTDIR", "-u", "afile/XXXXXX"),
		mktempCase("-u meets ENOTDIR with -d", "-u", "-d", "afile/XXXXXX"),
		mktempCase("quiet covers the -u lstat failure", "-u", "-q", "afile/XXXXXX"),
		mktempCase("-u meets ELOOP", "-u", "loopa/XXXXXX"),
		mktempCase("-u through a symlink to a file", "-u", "symtofile/XXXXXX"),
		mktempShape("-u through a dangling symlink", 6, 0, "-u", "danglingdir/XXXXXX"),

		mktempCase("directory takes no argument", "--directory=1"),
		mktempCase("dry-run takes no argument", "--dry-run=x"),
		mktempCase("quiet takes no argument", "--quiet=1"),
		mktempCase("help takes no argument", "--help=x"),
		mktempCase("version takes no argument", "--version=x"),
		mktempCase("--d is ambiguous", "--d"),
		mktempCase("there is no --u", "--u"),
		mktempCase("an unrecognized option reports the whole token", "--foo=bar"),
		mktempCase("an invalid short option", "-x"),
		mktempCase("an invalid short option before -V", "-xV"),
		mktempCase("getopt runs before the operand count", "-u", "a", "b", "-x"),
		mktempShape("quiet twice", 10, 0, "-u", "-q", "-q"),
		mktempShape("quiet alone", 10, 0, "-u", "-q"),
		mktempShape("dry run and directory clustered", 6, 0, "-ud", "fooXXXXXX"),
		mktempShape("an option after the operand is permuted out", 6, 0, "-u", "fooXXXXXX", "-d"),
		mktempEnv(mktempCase("POSIXLY_CORRECT stops at the operand", "fooXXXXXX", "-d"), "POSIXLY_CORRECT=1"),
		mktempEnv(mktempShape("POSIXLY_CORRECT with the option first", 6, 0, "-d", "fooXXXXXX"), "POSIXLY_CORRECT=1"),

		mktempCase("an apostrophe in the template", "fo'oXX"),
		mktempCase("a tab in the template", "a\tbXX"),
		mktempCase("a backslash in the template", "a\\bXX"),
		mktempCase("DEL in the template", "a\x7fbXX"),
		mktempCase("SOH in the template", "a\x01bXX"),
		mktempCase("ESC in the template", "a\x1bbXX"),
		mktempCase("BEL in the template", "a\x07bXX"),
		mktempCase("backspace in the template", "a\bbXX"),
		mktempCase("form feed in the template", "a\fbXX"),
		mktempCase("carriage return in the template", "a\rbXX"),
		mktempCase("vertical tab in the template", "a\vbXX"),
		mktempCase("UTF-8 in the template", "caf\xc3\xa9XX"),
		mktempCase("bytes that are not UTF-8", "\xff\xfeXX"),
		mktempShape("a raw byte reaches stdout", 6, 0, "-u", "\xffXXXXXX"),

		writeFail("write error", stdoutClosed, "zzXXXXXX"),
		writeFail("write error under -q", stdoutClosed, "-q", "zzXXXXXX"),
		writeFail("write error under -u is not suppressed", stdoutClosed, "-q", "-u", "zzXXXXXX"),
		writeFail("write error under -u", stdoutClosed, "-u", "zzXXXXXX"),
		writeFail("write error with -d", stdoutClosed, "-d", "zzXXXXXX"),
		writeFail("write error with -d under -q", stdoutClosed, "-q", "-d", "zzXXXXXX"),
		writeFail("no space left", stdoutFull, "zzXXXXXX"),
		writeFail("no space left under -q", stdoutFull, "-q", "zzXXXXXX"),
		writeFail("no space left under -u", stdoutFull, "-u", "zzXXXXXX"),
		writeFail("no space left with -d", stdoutFull, "-d", "zzXXXXXX"),
		writeFail("help to a closed stdout", stdoutClosed, "--help"),
		writeFail("help to a closed stdout under -q", stdoutClosed, "-q", "--help"),
		writeFail("version to a closed stdout", stdoutClosed, "--version"),
		writeFail("version to a full device", stdoutFull, "--version"),
		writeFail("help to a full device", stdoutFull, "--help"),
	}
}

func writeFail(name string, mode stdoutMode, args ...string) invocation {
	inv := mktempCase(name, args...)
	inv.stdout = mode
	return inv
}

func TestMktempParity(t *testing.T) {
	requireParity(t, "mktemp", mktempCases(t))
}

func TestMktempHelpVersion(t *testing.T) {
	requireHelp(t, "mktemp", []string{"--help"}, 0)
	requireHelp(t, "mktemp", []string{"--hel"}, 0)
	requireHelp(t, "mktemp", []string{"--h"}, 0)
	requireHelp(t, "mktemp", []string{"XX", "--help"}, 0)
	requireHelp(t, "mktemp", []string{"-d", "XXXXXX", "--help"}, 0)
	requireHelp(t, "mktemp", []string{"--help", "--version"}, 0)
	requireVersion(t, "mktemp", []string{"--version"}, 0)
	requireVersion(t, "mktemp", []string{"--vers"}, 0)
	requireVersion(t, "mktemp", []string{"--v"}, 0)
	requireVersion(t, "mktemp", []string{"--version", "--help"}, 0)
	requireVersion(t, "mktemp", []string{"--version", "-x"}, 0)
	requireVersion(t, "mktemp", []string{"-V"}, 0)
	requireVersion(t, "mktemp", []string{"-dV"}, 0)
	requireVersion(t, "mktemp", []string{"-Vd"}, 0)
	requireVersion(t, "mktemp", []string{"-Vx"}, 0)
}

func TestMktempRandomAlphabet(t *testing.T) {
	bin := fernBin(t, "mktemp")
	const runs, width = 40, 10
	seen := map[byte]bool{}
	names := map[string]bool{}
	for i := 0; i < runs; i++ {
		inv := invocation{args: []string{"-u", strings.Repeat("X", width)}}
		got := inv.run(t, bin, "mktemp")
		if got.exit != 0 || len(got.stderr) != 0 {
			t.Fatalf("mktemp -u: %s, stderr %s", got.how(), quote(got.stderr))
		}
		name := strings.TrimSuffix(string(got.stdout), "\n")
		if len(name) != width {
			t.Fatalf("mktemp -u %s printed %q: %d characters, want %d", strings.Repeat("X", width), name, len(name), width)
		}
		for i := 0; i < len(name); i++ {
			if !inAlnum62(name[i]) {
				t.Fatalf("mktemp -u printed %q: %q is not one of %s", name, name[i], alnum62)
			}
			seen[name[i]] = true
		}
		names[name] = true
	}
	if len(seen) < 55 {
		t.Errorf("%d of the 62 characters appeared over %d names; the alphabet is narrower than GNU's", len(seen), runs)
	}
	if len(names) != runs {
		t.Errorf("%d distinct names out of %d: the name is not being redrawn", len(names), runs)
	}
}

func TestMktempRetryExhaustion(t *testing.T) {
	dir := t.TempDir()
	for _, a := range alnum62 {
		for _, b := range alnum62 {
			var names []string
			for _, c := range alnum62 {
				names = append(names, string([]rune{a, b, c}))
			}
			for _, n := range names {
				f, err := os.OpenFile(filepath.Join(dir, n), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if errors.Is(err, fs.ErrExist) {
					continue
				}
				if err != nil {
					t.Fatalf("fill %s: %v", dir, err)
				}
				f.Close()
			}
		}
	}
	var fault *mktempRetryFault
	if runtime.GOOS == "darwin" {
		fault = newMktempRetryFault(t)
	}
	for _, args := range [][]string{{"XXX"}, {"-d", "XXX"}, {"-u", "XXX"}, {"-u", "-d", "XXX"}, {"-q", "XXX"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			inv := invocation{args: args, dir: dir}
			var want, got outcome
			if fault != nil {
				want, got = fault.run(t, inv, 0)
			} else {
				want = inv.run(t, referenceBin(t, "mktemp"), "mktemp")
				got = inv.run(t, fernBin(t, "mktemp"), "mktemp")
			}
			if want.how() != got.how() {
				t.Errorf("status differs: gnu %s, fern %s", want.how(), got.how())
			}
			if string(want.stderr) != string(got.stderr) {
				t.Errorf("stderr differs\n gnu: %s\nfern: %s", quote(want.stderr), quote(got.stderr))
			}
			if len(got.stdout) != 0 {
				t.Errorf("fern wrote %s to stdout with every name taken", quote(got.stdout))
			}
		})
	}
	inv := invocation{args: []string{"-u", "XXXX"}, dir: dir, mask: maskRandom(4, 0)}
	want := inv.run(t, referenceBin(t, "mktemp"), "mktemp")
	got := inv.run(t, fernBin(t, "mktemp"), "mktemp")
	if want.how() != got.how() || string(want.stdout) != string(got.stdout) {
		t.Errorf("mktemp -u XXXX in a saturated directory: gnu %s %s, fern %s %s", want.how(), quote(want.stdout), got.how(), quote(got.stdout))
	}
}
