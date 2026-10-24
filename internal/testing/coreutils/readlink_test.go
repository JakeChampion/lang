package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

func readlinkTree(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "dir", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	names := []string{"file", "dir/f"}
	if rawByteNamesHeld(t) {
		names = append(names, "\xff\xfe")
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(base, name), []byte("payload\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	links := [][2]string{
		{"file", "link"},
		{"link", "link2"},
		{"nonexistent", "dangling"},
		{"dir", "dirlink"},
		{"dir/sub", "sublink"},
		{"/absolute/target", "abslink"},
		{".", "selfdir"},
		{"\xff\xfe", "badtarget"},
		{"loop2", "loop1"},
		{"loop1", "loop2"},
		{"a file", "spaced"},
		{"b", "a"},
		{"c", "b"},
		{"a", "c"},
	}
	for _, l := range links {
		if err := os.Symlink(l[0], filepath.Join(base, l[1])); err != nil {
			t.Fatalf("symlink %s: %v", l[1], err)
		}
	}
	if err := os.Mkdir(filepath.Join(base, "chain"), 0o755); err != nil {
		t.Fatalf("mkdir chain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "chain", "target"), nil, 0o644); err != nil {
		t.Fatalf("write chain/target: %v", err)
	}
	prev := "target"
	for i := 1; i <= 9; i++ {
		name := "l" + string(rune('0'+i))
		if err := os.Symlink(prev, filepath.Join(base, "chain", name)); err != nil {
			t.Fatalf("symlink chain/%s: %v", name, err)
		}
		prev = name
	}
	return base
}

func init() {
	registerCorpus("readlink", readlinkCases)
}

func readlinkCases(t *testing.T) []invocation {
	tree := readlinkTree(t)
	at := func(name string, args ...string) invocation {
		return invocation{name: name, args: args, dir: tree}
	}
	return []invocation{
		at("a link", "link"),
		at("a link to a link", "link2"),
		at("a dangling link", "dangling"),
		at("an absolute target", "abslink"),
		at("a link to a directory", "dirlink"),
		at("a link to dot", "selfdir"),
		at("a plain file is EINVAL", "file"),
		at("a directory is EINVAL", "dir"),
		at("a missing name is ENOENT", "missing"),
		at("a component that is not a directory", "file/x"),
		at("a link under a missing directory", "missing/link"),
		at("two operands, one bad", "link", "file"),
		at("two operands, both good", "link", "link2"),
		at("a trailing slash on a link", "link/"),
		at("a trailing slash on a directory link", "dirlink/"),

		at("verbose on EINVAL", "-v", "file"),
		at("verbose on ENOENT", "-v", "missing"),
		at("verbose on a directory", "-v", "dir"),
		at("verbose then quiet", "-v", "-q", "file"),
		at("quiet then verbose", "-q", "-v", "file"),
		at("verbose then silent", "-v", "-s", "file"),
		at("silent then verbose", "-s", "-v", "file"),
		at("quiet alone", "-q", "file"),
		at("silent alone", "-s", "file"),
		at("verbose over several operands", "-v", "link", "link2", "file", "missing"),
		at("verbose in one cluster", "-vq", "file"),
		at("silent in one cluster", "-qv", "file"),

		at("-n with one operand", "-n", "link"),
		at("-n with two operands", "-n", "link", "link2"),
		at("-n quietly with two operands", "-nq", "link", "link2"),
		at("-n verbosely with two operands", "-nv", "link", "link2"),
		at("-n on a failure", "-n", "missing"),
		at("-n verbose on a failure", "-n", "-v", "missing"),
		at("-n canonicalising two operands", "-f", "-n", "link", "link2"),

		at("-z with one operand", "-z", "link"),
		at("-z with two operands", "-z", "link", "link2"),
		at("-nz with one operand", "-nz", "link"),
		at("-nz with two operands", "-nz", "link", "link2"),
		at("-mnz in one cluster", "-mnz", "link", "link2"),
		at("-z on a failure", "-z", "missing"),

		at("-f a link", "-f", "link"),
		at("-f a link to a link", "-f", "link2"),
		at("-f a plain file", "-f", "file"),
		at("-f a directory", "-f", "dir"),
		at("-f a missing last component", "-f", "missing"),
		at("-f a missing last component under a directory", "-f", "dir/missing"),
		at("-f a missing middle component", "-f", "missing/missing2"),
		at("-f a missing component deeper", "-f", "dir/missing/x"),
		at("-f a dangling link", "-f", "dangling"),
		at("-f a cycle", "-f", "loop1"),
		at("-f under a plain file", "-f", "file/x"),
		at("-f a link to a directory", "-f", "dirlink"),
		at("-f an absolute target that is nowhere", "-f", "abslink"),
		at("-f a link to dot", "-f", "selfdir"),
		at("-f a nine-link chain", "-f", "chain/l9"),

		at("-e a link", "-e", "link"),
		at("-e a missing last component", "-e", "missing"),
		at("-e a missing component under a directory", "-e", "dir/missing"),
		at("-e a missing middle component", "-e", "missing/missing2"),
		at("-e a dangling link", "-e", "dangling"),
		at("-e a cycle", "-e", "loop1"),
		at("-e under a plain file", "-e", "file/x"),
		at("-e a directory", "-e", "dir"),
		at("-e dot", "-e", "."),

		at("-m a link", "-m", "link"),
		at("-m a missing last component", "-m", "missing"),
		at("-m a missing middle component", "-m", "missing/missing2"),
		at("-m a dangling link", "-m", "dangling"),
		at("-m under a dangling link", "-m", "dangling/x"),
		at("-m under a plain file", "-m", "file/x"),
		at("-m under a cycle", "-m", "loop1/x"),
		at("-m an absolute target that is nowhere", "-m", "abslink"),
		at("-m through dot links", "-m", "selfdir/selfdir/file"),

		at("-m a two-link cycle", "-m", "loop1"),
		at("-m a two-link cycle from the other side", "-m", "loop2"),
		at("-m a three-link cycle", "-m", "a"),
		at("-m a three-link cycle one step along", "-m", "b"),
		at("-m a three-link cycle two steps along", "-m", "c"),
		at("-m a three-link cycle with a suffix", "-m", "a/x"),

		at("-v -e a missing name", "-v", "-e", "missing"),
		at("-v -e a missing component", "-v", "-e", "dir/missing"),
		at("-v -f a missing middle component", "-v", "-f", "missing/missing2"),
		at("-v -e a dangling link", "-v", "-e", "dangling"),
		at("-v -f a cycle", "-v", "-f", "loop1"),
		at("-v -e a cycle", "-v", "-e", "loop1"),
		at("-v -f under a plain file", "-v", "-f", "file/x"),
		at("-v -e under a plain file", "-v", "-e", "file/x"),
		at("-v -m under a plain file", "-v", "-m", "file/x"),
		at("-v -f an absolute target that is nowhere", "-v", "-f", "abslink"),
		at("-v -e an absolute target that is nowhere", "-v", "-e", "abslink"),

		at("-f a trailing slash on a directory", "-f", "dir/"),
		at("-f a trailing slash on a file", "-f", "file/"),
		at("-e a trailing slash on a file", "-e", "file/"),
		at("-m a trailing slash on a file", "-m", "file/"),
		at("-v -f a trailing slash on a file", "-v", "-f", "file/"),
		at("-f a trailing slash on a missing name", "-f", "missing/"),
		at("-e a trailing slash on a missing name", "-e", "missing/"),
		at("-m a trailing slash on a missing name", "-m", "missing/"),
		at("-f a trailing slash under a directory", "-f", "dir/missing/"),
		at("-f a trailing slash on a dangling link", "-f", "dangling/"),
		at("-e a trailing slash on a dangling link", "-e", "dangling/"),
		at("-f a trailing slash on a link to a directory", "-f", "dirlink/"),

		at("-f dot", "-f", "."),
		at("-f dotdot", "-f", ".."),
		at("-f the root", "-f", "/"),
		at("-f dotdot inside the name", "-f", "dir/../file"),
		at("-f dotdot past a link to a directory", "-f", "dirlink/../file"),
		at("-f dotdot past a link two deep", "-f", "sublink/.."),
		at("-m dotdot past a link two deep", "-m", "sublink/.."),
		at("-f dotdot twice", "-f", "dir/sub/../.."),
		at("-m dotdot at the root", "-m", "/../.."),
		at("-m dotdot in an absolute name", "-m", "/a/b/../c"),
		at("-m dotdot past a plain file", "-m", "file/../x"),
		at("-f a doubled slash inside the name", "-f", "dir//f"),
		at("-f dot components inside the name", "-f", "././link"),
		at("-f a trailing slash on a link to a file", "-f", "link/"),
		at("-e a trailing slash on a link to a file", "-e", "link/"),
		at("-m a trailing slash on a link to a file", "-m", "link/"),
		at("-v -e a file two components from the end", "-v", "-e", "dir/f/x"),
		at("-f doubled slashes", "-f", "//"),
		at("-f leading slashes", "-f", "///a"),
		at("-m a doubled slash", "-m", "//a"),
		at("-m the root", "-m", "/"),

		at("-e then -f", "-v", "-e", "-f", "missing"),
		at("-f then -e", "-v", "-f", "-e", "missing"),
		at("-e then -m", "-v", "-e", "-m", "missing"),
		at("-m then -e", "-v", "-m", "-e", "missing"),
		at("-fe in one cluster", "-v", "-fe", "missing"),
		at("-ef in one cluster", "-v", "-ef", "missing"),

		at("--canonicalize", "--canonicalize", "missing"),
		at("--canonicalize-existing", "--canonicalize-existing", "missing"),
		at("--canonicalize-missing", "--canonicalize-missing", "missing"),
		at("--no-newline", "--no-newline", "link"),
		at("--quiet", "--quiet", "file"),
		at("--silent", "--silent", "file"),
		at("--verbose", "--verbose", "file"),
		at("--zero", "--zero", "link"),
		at("--canonicalize-e is a unique prefix", "--canonicalize-e", "link"),
		at("--n is a unique prefix", "--n", "link"),
		at("--no is a unique prefix", "--no", "link"),
		at("--q is a unique prefix", "--q", "file"),
		at("--s is a unique prefix", "--s", "file"),
		at("--z is a unique prefix", "--z", "link"),

		{name: "--c is ambiguous", args: []string{"--c", "link"}},
		{name: "--ca is ambiguous", args: []string{"--ca", "link"}},
		{name: "--can is ambiguous", args: []string{"--can", "link"}},
		{name: "--v is ambiguous", args: []string{"--v", "link"}},
		{name: "--ver is ambiguous", args: []string{"--ver", "link"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "invalid short option", args: []string{"-x", "link"}},
		{name: "invalid short option in a cluster", args: []string{"-fx", "link"}},
		{name: "unrecognized long option", args: []string{"--foo", "link"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", "link"}},
		{name: "zero with a value", args: []string{"--zero=1", "link"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},

		{name: "no operands"},
		{name: "no operands with a mode", args: []string{"-f"}},
		{name: "dashdash alone", args: []string{"--"}},
		at("dashdash then an operand", "--", "link"),
		at("dashdash then an option-shaped operand", "-v", "--", "-f"),
		at("a lone dash is an operand", "-v", "-f", "-"),
		at("an empty operand", ""),
		at("an empty operand under -f", "-f", ""),
		at("an empty operand under -e", "-e", ""),
		at("an empty operand under -m", "-m", ""),
		at("an empty operand reported", "-v", ""),
		at("an empty operand reported under -f", "-v", "-f", ""),
		at("an empty operand reported under -m", "-v", "-m", ""),
		rawByteCase(at("an operand that is not valid UTF-8", "-v", "\xff\xfe")),
		at("a target that is not valid UTF-8", "badtarget"),
		at("-f a target that is not valid UTF-8", "-f", "badtarget"),
		at("-e a target that is not valid UTF-8", "-e", "badtarget"),
		at("-m a target that is not valid UTF-8", "-m", "badtarget"),
		at("an operand with a newline", "-v", "a\nb"),
		at("an operand with a space", "-v", "a b"),
		at("an operand with an apostrophe", "-v", "a'b"),
		at("several empty operands", "-v", "", ""),
		{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"link", "-v"}, dir: tree, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "POSIXLY_CORRECT reports a failure", args: []string{"file"}, dir: tree, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "POSIXLY_CORRECT beats quiet", args: []string{"-q", "file"}, dir: tree, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "POSIXLY_CORRECT beats silent", args: []string{"-s", "file"}, dir: tree, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "a terminal quotes the answer", args: []string{"spaced"}, dir: tree, ttyOut: true},
		{name: "a terminal leaves a plain answer alone", args: []string{"link"}, dir: tree, ttyOut: true},
		{name: "a terminal under literal quoting", args: []string{"spaced"}, dir: tree, ttyOut: true, env: []string{"QUOTING_STYLE=literal"}},
		{name: "a terminal under c quoting", args: []string{"spaced"}, dir: tree, ttyOut: true, env: []string{"QUOTING_STYLE=c"}},
		{name: "a terminal under an invalid quoting style", args: []string{"spaced"}, dir: tree, ttyOut: true, env: []string{"QUOTING_STYLE=bogus"}},
		{name: "zero leaves a terminal answer unquoted", args: []string{"-z", "spaced"}, dir: tree, ttyOut: true},
		{name: "a pipe leaves the answer unquoted", args: []string{"spaced"}, dir: tree},

		{name: "stdout closed with output", args: []string{"link"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed with no output", args: []string{"file"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed with a diagnostic", args: []string{"-v", "file"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed canonicalising", args: []string{"-f", "link"}, dir: tree, stdout: stdoutClosed},
		{name: "stdout closed on a usage error", stdout: stdoutClosed},
		{name: "stdout full", args: []string{"link"}, dir: tree, stdout: stdoutFull},
		{name: "stdout full with a diagnostic", args: []string{"-v", "link", "file"}, dir: tree, stdout: stdoutFull},
		{name: "stdout full canonicalising", args: []string{"-f", "link"}, dir: tree, stdout: stdoutFull},
	}
}

func TestReadlinkParity(t *testing.T) {
	requireParity(t, "readlink", readlinkCases(t))
}

func TestReadlinkHelpVersion(t *testing.T) {
	requireHelp(t, "readlink", []string{"--help"}, 0)
	requireHelp(t, "readlink", []string{"--hel"}, 0)
	requireHelp(t, "readlink", []string{"--help", "link"}, 0)
	requireHelp(t, "readlink", []string{"link", "--help"}, 0)
	requireVersion(t, "readlink", []string{"--version"}, 0)
	requireVersion(t, "readlink", []string{"--vers"}, 0)
	requireVersion(t, "readlink", []string{"-f", "--version"}, 0)
}

func TestReadlinkUnlinkedWorkingDirectory(t *testing.T) {
	const enoent = "No such file or directory"
	requireUnlinkedParity(t, "readlink", []unlinkedCase{
		{[]string{"-fv", "x"}, "x: " + enoent},
		{[]string{"-ev", "x"}, "x: " + enoent},
		{[]string{"-mv", "x"}, "x: " + enoent},
		{[]string{"-f", "x"}, ""},
		{[]string{"-v", "x"}, "x: " + enoent},
		{[]string{"-fv", "/"}, ""},
	})
}
