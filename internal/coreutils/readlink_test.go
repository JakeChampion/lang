package coreutils

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Two different programs share the name `readlink`, and the corpus covers
// both. Without -f / -e / -m it is one readlink(2), where a path that is not
// a symlink is EINVAL rather than an empty answer. With any of them it is
// the canonicalisation walk, and the three differ only in what they demand
// of a component that is not there — all of them, all but the last, none.
//
// The walk is where the cases concentrate, because every one of its rules
// is invisible in the output until it is wrong: `.` dropped, `..` popped
// off the answer rather than resolved against the filesystem, a relative
// link target continuing from where the link sits and an absolute one
// restarting at the root, and a trailing slash demanding that the final
// component be a directory — which is a DIFFERENT test from "a further
// component follows", and conflating the two is silent.
//
// Loop detection is by revisit rather than by a count, so both directions
// are here: a two-link cycle must be ELOOP and a sixty-link chain must
// resolve.

// readlinkFixture builds the fixture ONCE and answers the directory both
// sides run in.
//
// One shared directory rather than the per-side `tree` pair the mutating
// utilities use, and for two reasons that are really one: readlink writes
// nothing, so there is nothing to keep apart — and its canonicalised output
// is an ABSOLUTE path, so two directories would make the two sides differ on
// every -f / -e / -m case by the accident of their own names.
func readlinkFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	readlinkTree(t, dir)
	return dir
}

// readlinkTree is the fixture. Every shape the walk decides on has an entry:
// a link to a file, a dangling one, a link to a directory, a relative and an
// absolute target, a two-link cycle, a self-link, and a chain long enough
// that a MAXSYMLINKS counter would refuse it.
func readlinkTree(t *testing.T, dir string) {
	t.Helper()
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "d/sub"), 0o755), "mkdir d/sub")
	must(os.WriteFile(filepath.Join(dir, "d/f"), []byte("x"), 0o644), "write d/f")
	must(os.Symlink("f", filepath.Join(dir, "d/tof")), "symlink d/tof")
	must(os.Symlink("nowhere", filepath.Join(dir, "d/dang")), "symlink d/dang")
	must(os.Symlink("d/tof", filepath.Join(dir, "rel")), "symlink rel")
	must(os.Symlink(filepath.Join(dir, "d/f"), filepath.Join(dir, "abs")), "symlink abs")
	must(os.Symlink("d", filepath.Join(dir, "dlink")), "symlink dlink")
	must(os.Symlink("loop2", filepath.Join(dir, "loop1")), "symlink loop1")
	must(os.Symlink("loop1", filepath.Join(dir, "loop2")), "symlink loop2")
	must(os.Symlink("self", filepath.Join(dir, "self")), "symlink self")
	must(os.Symlink("d/f", filepath.Join(dir, "sp ace")), "symlink sp ace")
	must(os.Symlink("d/f", filepath.Join(dir, "q'2")), "symlink q'2")
	must(os.Symlink("d/f", filepath.Join(dir, "nl\nx")), "symlink nl")
	// Sixty distinct links: a chain, not a loop. A counter bounded at
	// MAXSYMLINKS would refuse this; revisit detection resolves it.
	must(os.WriteFile(filepath.Join(dir, "chain0"), []byte("end"), 0o644), "write chain0")
	for i := 1; i <= 60; i++ {
		prev := "chain" + strconv.Itoa(i-1)
		must(os.Symlink(prev, filepath.Join(dir, "chain"+strconv.Itoa(i))), "symlink chain")
	}
}

// readlinkOperands are the paths every mode is asked about, so each rule is
// exercised four times — once bare and once under each of -f / -e / -m.
var readlinkOperands = []struct{ name, path string }{
	{"a link to a file", "d/tof"},
	{"a dangling link", "d/dang"},
	{"a missing name", "nosuch"},
	{"a missing intermediate", "d/nosuch/x"},
	{"a directory", "d"},
	{"a regular file", "d/f"},
	{"dot", "."},
	{"the root", "/"},
	{"a dot component and a doubled slash", "./d//tof"},
	{"a dotdot that climbs back", "d/../d/tof"},
	{"a dotdot off the end of the answer", "d/sub/.."},
	{"dotdots through names that do not exist", "a/b/../../c"},
	{"a link with an absolute target", "abs"},
	{"a link with a relative target", "rel"},
	{"a link to a directory", "dlink"},
	{"a name under a link to a directory", "dlink/f"},
	{"a two-link cycle", "loop1"},
	{"a self-link", "self"},
	{"a cycle in an intermediate", "self/x"},
	{"a name under a regular file", "d/f/x"},
	{"a trailing slash on a link to a file", "d/tof/"},
	{"a trailing slash on a missing name", "d/nosuch/"},
	{"a sixty-link chain", "chain60"},
	{"the empty operand", ""},
}

func readlinkCases(t *testing.T) []invocation {
	dir := readlinkFixture(t)
	var cases []invocation
	for _, mode := range []struct{ flag, label string }{
		{"", "bare"}, {"-f", "-f"}, {"-e", "-e"}, {"-m", "-m"},
	} {
		for _, op := range readlinkOperands {
			args := []string{}
			if mode.flag != "" {
				args = append(args, mode.flag)
			}
			args = append(args, op.path)
			cases = append(cases, invocation{
				name: mode.label + " " + op.name, args: args, dir: dir,
			})
		}
	}
	return append(cases, []invocation{
		// -n suppresses the delimiter for ONE operand and is refused for
		// more than one, with a warning, rather than applied to the last.
		{name: "-n on one operand", args: []string{"-n", "d/tof"}, dir: dir},
		{name: "-n on two operands warns", args: []string{"-n", "d/tof", "d/dang"}, dir: dir},
		{name: "-n on a failure", args: []string{"-n", "nosuch"}, dir: dir},
		{name: "-z ends with NUL", args: []string{"-z", "d/tof"}, dir: dir},
		{name: "-z over two operands", args: []string{"-z", "d/tof", "d/dang"}, dir: dir},
		{name: "-nz is -n", args: []string{"-nz", "d/tof"}, dir: dir},

		// The quiet trio. Silence is the DEFAULT, so -q and -s do nothing
		// and only the last of -q / -s / -v decides.
		{name: "-v names the failure", args: []string{"-v", "nosuch"}, dir: dir},
		{name: "-v on a directory", args: []string{"-v", "d"}, dir: dir},
		{name: "-v with -f on a cycle", args: []string{"-v", "-f", "loop1"}, dir: dir},
		{name: "-v with -e on a dangling link", args: []string{"-v", "-e", "d/dang"}, dir: dir},
		{name: "-v with -f on a missing intermediate", args: []string{"-v", "-f", "d/nosuch/x"}, dir: dir},
		{name: "-q after -v silences", args: []string{"-v", "-q", "nosuch"}, dir: dir},
		{name: "-v after -q speaks", args: []string{"-q", "-v", "nosuch"}, dir: dir},
		{name: "-s after -v silences", args: []string{"-v", "-s", "nosuch"}, dir: dir},
		{name: "-v quotes a name with a space", args: []string{"-v", "sp ace/nolink"}, dir: dir},
		{name: "-v quotes a name with an apostrophe", args: []string{"-v", "q'2/x"}, dir: dir},
		{name: "-v quotes a name with a newline", args: []string{"-v", "nl\nx/y"}, dir: dir},
		{name: "-v quotes a name that is not valid UTF-8", args: []string{"-v", "\xff\xfe"}, dir: dir},

		// The three canonicalisation flags are last-wins.
		{name: "-f then -e", args: []string{"-f", "-e", "d/dang"}, dir: dir},
		{name: "-e then -m", args: []string{"-e", "-m", "nosuch"}, dir: dir},
		{name: "-m then -f", args: []string{"-m", "-f", "d/nosuch/x"}, dir: dir},

		// Several operands: each is answered, and one failure only decides
		// the exit status.
		{name: "three operands, one failing", args: []string{"d/tof", "d/dang", "nosuch"}, dir: dir},
		{name: "-f over three operands", args: []string{"-f", "d/tof", "d/dang", "nosuch"}, dir: dir},
		{name: "-e stops at nothing", args: []string{"-e", "d/tof", "nosuch"}, dir: dir},

		// Long forms and the option terminator.
		{name: "--canonicalize", args: []string{"--canonicalize", "d/tof"}, dir: dir},
		{name: "--canonicalize-existing", args: []string{"--canonicalize-existing", "d/tof"}, dir: dir},
		{name: "--canonicalize-missing", args: []string{"--canonicalize-missing", "nosuch"}, dir: dir},
		{name: "--no-newline", args: []string{"--no-newline", "d/tof"}, dir: dir},
		{name: "--zero", args: []string{"--zero", "d/tof"}, dir: dir},
		{name: "--verbose", args: []string{"--verbose", "nosuch"}, dir: dir},
		{name: "dashdash then an operand", args: []string{"--", "d/tof"}, dir: dir},
		{name: "an option after an operand permutes", args: []string{"d/tof", "-f"}, dir: dir},
		{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"d/tof", "-f"}, env: []string{"POSIXLY_CORRECT=1"}, dir: dir},

		// getopt faults. `--c` is ambiguous across the three canonicalize
		// names; `--can` still is.
		{name: "no operands"},
		{name: "invalid short option", args: []string{"-x", "d/tof"}},
		{name: "invalid byte in a cluster", args: []string{"-fx", "d/tof"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "ambiguous canonicalize prefix", args: []string{"--c", "d/tof"}},
		{name: "still ambiguous at --can", args: []string{"--can", "d/tof"}},
		{name: "canonicalize with a value", args: []string{"--canonicalize=x", "d/tof"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},

		// The write-failure paths.
		{name: "stdout closed on success", args: []string{"d/tof"}, dir: dir, stdout: stdoutClosed},
		{name: "stdout full on success", args: []string{"d/tof"}, dir: dir, stdout: stdoutFull},
		{name: "stdout closed on a fault", args: []string{"nosuch"}, dir: dir, stdout: stdoutClosed},
		{name: "stdout closed with -v", args: []string{"-v", "nosuch"}, dir: dir, stdout: stdoutClosed},
		{name: "stdout closed on a usage error", stdout: stdoutClosed},
	}...)
}

func TestReadlinkParity(t *testing.T) {
	requireParity(t, "readlink", readlinkCases(t))
}

func TestReadlinkHelpVersion(t *testing.T) {
	requireHelp(t, "readlink", []string{"--help"}, 0)
	requireHelp(t, "readlink", []string{"--hel"}, 0)
	requireHelp(t, "readlink", []string{"--help", "x"}, 0)
	requireHelp(t, "readlink", []string{"x", "--help"}, 0)
	requireVersion(t, "readlink", []string{"--version"}, 0)
	requireVersion(t, "readlink", []string{"--versio"}, 0)
	requireVersion(t, "readlink", []string{"x", "--version"}, 0)
}
