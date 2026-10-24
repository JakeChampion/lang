package coreutils

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("rm", rmCases)
}

func rmFlat(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "a", "")
	seedWrite(t, dir, "b", "abc")
	seedWrite(t, dir, "c", "")
	seedMkdir(t, dir, "adir")
	seedSymlink(t, dir, "a", "sym")
	seedSymlink(t, dir, "nowhere", "dang")
	seedFifo(t, filepath.Join(dir, "fifo"))
}

func rmTree(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d")
	seedWrite(t, dir, "d/x", "")
	seedMkdir(t, dir, "d/e")
	seedWrite(t, dir, "d/e/y", "")
	seedWrite(t, dir, "d/z", "")
}

func rmDash(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "-x", "")
	seedWrite(t, dir, "-r", "")
	seedWrite(t, dir, "-v", "")
	seedWrite(t, dir, "--foo", "")
	seedSymlink(t, dir, "nowhere", "-z")
}

func rmWide(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d")
	for c := 'z'; c >= 'a'; c-- {
		seedWrite(t, dir, "d/"+string(c), "")
	}
}

func rmMixed(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d")
	seedWrite(t, dir, "d/zz", "")
	seedMkdir(t, dir, "d/aa")
	seedWrite(t, dir, "d/aa/1", "")
	seedSymlink(t, dir, "zz", "d/ll")
	seedMkdir(t, dir, "d/bb")
	seedWrite(t, dir, "d/mm", "")
}

func rmDeep(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d/a/b/c")
	seedWrite(t, dir, "d/a/b/c/x", "")
	seedWrite(t, dir, "d/a/y", "")
	seedWrite(t, dir, "d/z", "")
}

func rmBeyondPathMax(t *testing.T, dir string) {
	t.Helper()
	d := strings.Repeat("d", 100)
	cmd := exec.Command("/bin/sh", "-c", `set -e
mkdir long && cd long
i=0
while [ $i -lt 80 ]; do mkdir `+d+` && cd -P `+d+`; i=$((i + 1)); done
printf leaf > leaf
`)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building long: %v\n%s", err, out)
	}
}

func rmEmpty(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "emp")
}

func rmSymDir(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "sd")
	seedWrite(t, dir, "sd/x", "")
	seedSymlink(t, dir, "sd", "sl")
}

func rmDangling(t *testing.T, dir string) {
	t.Helper()
	seedSymlink(t, dir, "nowhere", "sl")
}

func rmLoop(t *testing.T, dir string) {
	t.Helper()
	seedSymlink(t, dir, "loop", "loop")
}

func rmNested(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d")
	seedWrite(t, dir, "d/f", "")
	seedMkdir(t, dir, "d/e")
}

func rmNestedDirs(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d/e/g")
}

func rmNestedFile(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d/e")
	seedWrite(t, dir, "d/e/x", "")
}

func rmYes(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += "y\n"
	}
	return out
}

func rmCases(t *testing.T) []invocation {
	long := ""
	for i := 0; i < 300; i++ {
		long += "a"
	}
	return []invocation{
		{name: "no operands"},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "force with no operands", args: []string{"-f"}},
		{name: "force with dashdash", args: []string{"-f", "--"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "invalid short cluster", args: []string{"-vq"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "the ambiguous prefix", args: []string{"--v"}},
		{name: "the empty long option", args: []string{"--=x"}},
		{name: "force takes no argument", args: []string{"--force=x"}},
		{name: "recursive takes no argument", args: []string{"--recursive=x"}},
		{name: "dir takes no argument", args: []string{"--dir=x"}},
		{name: "one-file-system takes no argument", args: []string{"--one-file-system=x"}},
		{name: "no-preserve-root takes no argument", args: []string{"--no-preserve-root=x"}},
		{name: "the hidden option takes no argument", args: []string{"---presume-input-tty=x"}},
		{name: "three dashes are the hidden option", args: []string{"---", "a"}, seedTree: rmFlat},
		{name: "three dashes alone", args: []string{"---"}},
		{name: "four dashes", args: []string{"----"}},

		{name: "the hint names the faulting element", args: []string{"-x"}, seedTree: rmDash},
		{name: "the hint names a later element", args: []string{"-x", "-r"}, seedTree: rmDash},
		{name: "the hint after an ambiguous prefix", args: []string{"--v", "-v"}, seedTree: rmDash},
		{name: "the hint after a glued value", args: []string{"--dir=x", "-v"}, seedTree: rmDash},
		{name: "the hint after an unrecognized option", args: []string{"--foo"}, seedTree: rmDash},
		{name: "the hint looks past dashdash", args: []string{"--foo", "--", "-v"}, seedTree: rmDash},
		{name: "the hint finds a dangling symlink", args: []string{"-Q", "-z"}, seedTree: rmDash},
		{name: "a lone dash is not hinted", args: []string{"-Q", "-"}, seedTree: rmDashFile},
		{name: "no hint for the abbreviation refusal", args: []string{"--no", "-v"}, seedTree: rmDash},
		{name: "no hint for a preserve-root value", args: []string{"--preserve-root=q", "-v"}, seedTree: rmDash},
		{name: "no hint for an interactive value", args: []string{"--interactive=q", "-v"}, seedTree: rmDash},

		{name: "no-preserve-root may not be abbreviated", args: []string{"--no"}},
		{name: "nor further along", args: []string{"--no-p"}},
		{name: "nor as one letter", args: []string{"--n"}},
		{name: "spelled in full it parses", args: []string{"--no-preserve-root"}},
		{name: "preserve-root MAY be abbreviated", args: []string{"--p=all"}},
		{name: "and reports the canonical name", args: []string{"--p=al"}},
		{name: "preserve-root takes all and nothing else", args: []string{"--preserve-root=x"}},
		{name: "an empty preserve-root value", args: []string{"--preserve-root="}},
		{name: "preserve-root is case sensitive", args: []string{"--preserve-root=ALL"}},
		{name: "preserve-root=all", args: []string{"--preserve-root=all"}},
		{name: "bare preserve-root", args: []string{"--preserve-root"}},

		{name: "an interactive value that is nothing", args: []string{"--interactive=bogus"}},
		{name: "argmatch is case sensitive", args: []string{"--interactive=NEVER"}},
		{name: "an empty interactive value is ambiguous", args: []string{"--interactive="}},
		{name: "a prefix of three synonyms is accepted", args: []string{"--interactive=n"}},
		{name: "a prefix of once", args: []string{"--interactive=o"}},
		{name: "a prefix of always", args: []string{"--interactive=a"}},
		{name: "a prefix of yes", args: []string{"--interactive=y"}},
		{name: "interactive never consumes the next token", args: []string{"--interactive", "never"}},

		{name: "force then i", args: []string{"-f", "-i", "nosuchz"}},
		{name: "i then force", args: []string{"-i", "-f", "nosuchz"}},
		{name: "force then I", args: []string{"-f", "-I", "nosuchz"}},
		{name: "force then interactive never", args: []string{"-f", "--interactive=never", "nosuchz"}},
		{name: "interactive never alone", args: []string{"--interactive=never", "nosuchz"}},
		{name: "repeated flags are idempotent", args: []string{"-vv", "-ff", "-rr", "-dd", "a"}, seedTree: rmFlat},

		{name: "one file", args: []string{"a"}, seedTree: rmFlat},
		{name: "two files verbosely", args: []string{"-v", "a", "b"}, seedTree: rmFlat},
		{name: "a symlink, not its target", args: []string{"-v", "sym"}, seedTree: rmFlat},
		{name: "a dangling symlink", args: []string{"-v", "dang"}, seedTree: rmFlat},
		{name: "a fifo", args: []string{"-v", "fifo"}, seedTree: rmFlat},
		{name: "a directory without -r", args: []string{"adir"}, seedTree: rmFlat},
		{name: "a directory among files", args: []string{"-v", "a", "adir", "b"}, seedTree: rmFlat},

		{name: "a missing file", args: []string{"nosuchz"}, seedTree: rmFlat},
		{name: "a missing file under -f", args: []string{"-f", "nosuchz"}, seedTree: rmFlat},
		{name: "a missing parent", args: []string{"nodir/x"}, seedTree: rmFlat},
		{name: "a parent that is a file", args: []string{"a/x"}, seedTree: rmFlat},
		{name: "a parent that is a file under -f", args: []string{"-f", "a/x"}, seedTree: rmFlat},
		{name: "the empty operand", args: []string{""}, seedTree: rmFlat},
		{name: "the empty operand under -f", args: []string{"-f", ""}, seedTree: rmFlat},
		{name: "a symlink loop", args: []string{"loop/x"}, seedTree: rmLoop},
		{name: "a symlink loop under -f", args: []string{"-f", "loop/x"}, seedTree: rmLoop},
		{name: "a name too long", args: []string{long}, seedTree: rmFlat},
		{name: "a name too long under -f", args: []string{"-f", long}, seedTree: rmFlat},

		{name: "a name with a space", args: []string{"no such name"}, seedTree: rmFlat},
		{name: "a name with an apostrophe", args: []string{"a'b"}, seedTree: rmFlat},
		{name: "a name with a newline", args: []string{"a\nb"}, seedTree: rmFlat},
		{name: "a name that is not valid UTF-8", args: []string{"\xff\xfe"}, seedTree: rmFlat},
		{name: "a lone dash names a file called -", args: []string{"-"}, seedTree: rmFlat},

		{name: "a file with a trailing slash", args: []string{"-v", "a/"}, seedTree: rmFlat},
		{name: "a file with three", args: []string{"-v", "a///"}, seedTree: rmFlat},
		{name: "and -f passes over that too", args: []string{"-f", "a/"}, seedTree: rmFlat},
		{name: "a directory with three", args: []string{"-rv", "d///"}, seedTree: rmTree},
		{name: "interior slashes survive", args: []string{"-rv", "d//e//"}, seedTree: rmTree},
		{name: "a missing name with three", args: []string{"-rv", "nosuchz///"}, seedTree: rmFlat},
		{name: "a symlink to a directory, with a slash", args: []string{"-rv", "sl/"}, seedTree: rmSymDir},
		{name: "the same under -d", args: []string{"-dv", "sl/"}, seedTree: rmSymDir},
		{name: "a dangling symlink with a slash", args: []string{"-rv", "sl/"}, seedTree: rmDangling},
		{name: "a dangling symlink with a slash under -f", args: []string{"-rf", "sl/"}, seedTree: rmDangling},
		{name: "a dangling symlink with a slash under -i", args: []string{"-ri", "sl/"}, stdin: "y\n", seedTree: rmDangling},
		{name: "a file with a slash under -i", args: []string{"-i", "a/"}, stdin: "y\n", seedTree: rmFlat},
		{name: "a missing file under -i", args: []string{"-i", "nosuchz"}, stdin: "y\n", seedTree: rmFlat},
		{name: "-f does not quiet the prompt's stat", args: []string{"-f", "--interactive=always", "nosuchz"}, stdin: "y\n", seedTree: rmFlat},

		{name: "dot under -r", args: []string{"-r", "."}, seedTree: rmFlat},
		{name: "dot with a slash", args: []string{"-r", "./"}, seedTree: rmFlat},
		{name: "dot with two", args: []string{"-r", ".//"}, seedTree: rmFlat},
		{name: "dotdot under -rf", args: []string{"-rf", ".."}, seedTree: rmFlat},
		{name: "dotdot with two slashes", args: []string{"-r", "..//"}, seedTree: rmFlat},
		{name: "a directory reached as dot", args: []string{"-r", "d/./"}, seedTree: rmTree},
		{name: "a directory reached as dotdot", args: []string{"-r", "d/e/.."}, seedTree: rmTree},
		{name: "dot without -r is EISDIR", args: []string{"."}, seedTree: rmFlat},
		{name: "dot under -d in an empty directory", args: []string{"-d", "."}, seedTree: rmEmptyCwd},
		{name: "dot under -d in a full one", args: []string{"-d", "."}, seedTree: rmFlat},
		{name: "dotdot under -d", args: []string{"-d", ".."}, seedTree: rmFlat},
		{name: "a missing path ending in dot", args: []string{"-r", "nosuchz/."}, seedTree: rmFlat},
		{name: "the same under -f", args: []string{"-rf", "nosuchz/."}, seedTree: rmFlat},
		{name: "a file ending in dot", args: []string{"-r", "a/."}, seedTree: rmFlat},
		{name: "three dots is an ordinary name", args: []string{"-rv", "..."}, seedTree: rmDots},
		{name: "a name that starts with dotdot", args: []string{"-rv", "d/..x"}, seedTree: rmDotPrefix},

		{name: "the root directory", args: []string{"-rf", "/"}, seedTree: rmFlat},
		{name: "two slashes keep the same-as note", args: []string{"-r", "//"}, seedTree: rmFlat},
		{name: "three collapse to one", args: []string{"-r", "///"}, seedTree: rmFlat},
		{name: "four as well", args: []string{"-r", "////"}, seedTree: rmFlat},
		{name: "the root without -r is EISDIR", args: []string{"/"}, seedTree: rmFlat},
		{name: "the root under -d is ENOTEMPTY", args: []string{"-d", "/"}, seedTree: rmFlat},
		{name: "the root as dot", args: []string{"-r", "/."}, seedTree: rmFlat},
		{name: "the root as dotdot", args: []string{"-r", "/.."}, seedTree: rmFlat},
		{name: "the root reached through a child", args: []string{"-r", rootThroughChild(t)}, seedTree: rmFlat},
		{name: "preserve-root is the default", args: []string{"--preserve-root", "-r", "/"}, seedTree: rmFlat},
		{name: "and preserve-root=all keeps it", args: []string{"--preserve-root=all", "-r", "/"}, seedTree: rmFlat},
		{name: "the failsafe is not quieted by -v", args: []string{"-rv", "/"}, seedTree: rmFlat},

		{name: "a tree", args: []string{"-rv", "d"}, seedTree: rmTree},
		{name: "a tree with -R", args: []string{"-Rv", "d"}, seedTree: rmTree},
		{name: "a tree with --recursive", args: []string{"--recursive", "-v", "d"}, seedTree: rmTree},
		{name: "a deep tree unwinds post-order", args: []string{"-rv", "d"}, seedTree: rmDeep},
		{name: "a tree past PATH_MAX goes whole", args: []string{"-rf", "long"}, seedTree: rmBeyondPathMax},
		{name: "a tree past PATH_MAX, verbose", args: []string{"-rv", "long"}, seedTree: rmBeyondPathMax},
		{name: "26 names in readdir order", args: []string{"-rv", "d"}, seedTree: rmWide},
		{name: "files and directories interleaved", args: []string{"-rv", "d"}, seedTree: rmMixed},
		{name: "a symlink inside is not descended", args: []string{"-rv", "d"}, seedTree: rmSymInTree},
		{name: "an empty directory under -r", args: []string{"-rv", "emp"}, seedTree: rmEmpty},
		{name: "an empty directory under -d", args: []string{"-dv", "emp"}, seedTree: rmEmpty},
		{name: "a full directory under -d", args: []string{"-dv", "d"}, seedTree: rmTree},
		{name: "and -f does not help", args: []string{"-fdv", "d"}, seedTree: rmTree},
		{name: "a tree and a file", args: []string{"-rv", "d", "a"}, seedTree: rmTreeAndFlat},

		{name: "the same file twice", args: []string{"-v", "a", "a"}, seedTree: rmFlat},
		{name: "and under -f", args: []string{"-fv", "a", "a"}, seedTree: rmFlat},
		{name: "a tree and something inside it", args: []string{"-rv", "d", "d/x"}, seedTree: rmTree},
		{name: "and the other order", args: []string{"-rv", "d/x", "d"}, seedTree: rmTree},
		{name: "the same tree twice", args: []string{"-rv", "d", "d"}, seedTree: rmTree},
		{name: "the empty operand under -r", args: []string{"-rv", ""}, seedTree: rmFlat},
		{name: "several operands, some missing", args: []string{"-v", "a", "nosuchz", "b", "nosuchy"}, seedTree: rmFlat},
		{name: "a fifo inside a tree", args: []string{"-rv", "d"}, seedTree: rmFifoTree},
		{name: "a directory whose name is an option", args: []string{"-rv", "--", "-d"}, seedTree: rmOptDir},
		{name: "a symlink to an empty directory under -d", args: []string{"-dv", "sl"}, seedTree: rmSymEmpty},
		{name: "the same with a trailing slash", args: []string{"-dv", "sl/"}, seedTree: rmSymEmpty},
		{name: "once with -r and -v", args: []string{"--interactive=once", "-rv", "d"}, stdin: "y\n", seedTree: rmTree},

		{name: "an option after the operand", args: []string{"d", "-r"}, seedTree: rmTree},
		{name: "POSIXLY_CORRECT stops at the operand", args: []string{"d", "-r"}, seedTree: rmTree, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "an empty POSIXLY_CORRECT counts", args: []string{"d", "-r"}, seedTree: rmTree, env: []string{"POSIXLY_CORRECT="}},
		{name: "dashdash is an operand under it", args: []string{"a", "--", "-r"}, seedTree: rmFlat, env: []string{"POSIXLY_CORRECT=1"}},

		{name: "three operands do not reach it", args: []string{"-I", "a", "b", "c"}, seedTree: rmFlat},
		{name: "four do, declined", args: []string{"-I", "a", "b", "c", "d"}, stdin: "n\n", seedTree: rmFlat},
		{name: "four accepted", args: []string{"-I", "a", "b", "c", "d"}, stdin: "y\n", seedTree: rmFlat},
		{name: "dashdash is not an operand", args: []string{"-I", "--", "a", "b", "c", "d"}, stdin: "n\n", seedTree: rmFlat},
		{name: "nor an option between them", args: []string{"-I", "a", "b", "-v", "c", "d"}, stdin: "n\n", seedTree: rmFlat},
		{name: "under -r one operand is enough", args: []string{"-I", "-r", "d"}, stdin: "n\n", seedTree: rmTree},
		{name: "and accepted removes the tree", args: []string{"-I", "-r", "d"}, stdin: "y\n", seedTree: rmTree},
		{name: "three under -r still prompt", args: []string{"-I", "-r", "a", "b", "c"}, stdin: "n\n", seedTree: rmFlat},
		{name: "end of input declines", args: []string{"-I", "a", "b", "c", "d"}, seedTree: rmFlat},
		{name: "-I with no operands", args: []string{"-I"}},
		{name: "-I then -f", args: []string{"-I", "-f"}},
		{name: "-f then -I", args: []string{"-f", "-I"}},
		{name: "the long spelling of -I", args: []string{"--interactive=once", "a", "b", "c", "d"}, stdin: "n\n", seedTree: rmFlat},

		{name: "both accepted", args: []string{"-iv", "a", "b"}, stdin: "y\ny\n", seedTree: rmFlat},
		{name: "the first declined", args: []string{"-iv", "a", "b"}, stdin: "n\ny\n", seedTree: rmFlat},
		{name: "one line answers one prompt", args: []string{"-i", "a", "b"}, stdin: "yy\n", seedTree: rmFlat},
		{name: "two lines answer two", args: []string{"-i", "a", "b"}, stdin: "y y\ny\n", seedTree: rmFlat},
		{name: "an upper-case yes", args: []string{"-i", "a"}, stdin: "Y\n", seedTree: rmFlat},
		{name: "only the first byte counts", args: []string{"-i", "a"}, stdin: " y\n", seedTree: rmFlat},
		{name: "end of input is no", args: []string{"-i", "a"}, seedTree: rmFlat},
		{name: "q is no", args: []string{"-i", "a"}, stdin: "q\n", seedTree: rmFlat},
		{name: "a very long yes", args: []string{"-i", "a"}, stdin: rmLongLine("y", 70000), seedTree: rmFlat},
		{name: "a very long no", args: []string{"-i", "a"}, stdin: rmLongLine("x", 70000) + "y\n", seedTree: rmFlat},
		{name: "every file kind declined", args: []string{"-i", "-d", "a", "b", "adir", "sym", "dang", "fifo"}, stdin: "n\nn\nn\nn\nn\nn\n", seedTree: rmFlat},
		{name: "every file kind accepted", args: []string{"-i", "-d", "a", "b", "adir", "sym", "dang", "fifo"}, stdin: rmYes(6), seedTree: rmFlat},
		{name: "-i on a directory without -r", args: []string{"-i", "adir"}, stdin: "y\n", seedTree: rmFlat},
		{name: "bare --interactive is -i", args: []string{"--interactive", "a"}, stdin: "y\n", seedTree: rmFlat},
		{name: "-i then never", args: []string{"-i", "--interactive=never", "a"}, seedTree: rmFlat},
		{name: "-f then always", args: []string{"-f", "--interactive=always", "a"}, stdin: "y\n", seedTree: rmFlat},

		{name: "a tree with every prompt accepted", args: []string{"-riv", "d"}, stdin: rmYes(8), seedTree: rmTree},
		{name: "the descent declined", args: []string{"-riv", "d"}, stdin: "n\n", seedTree: rmTree},
		{name: "a child declined", args: []string{"-riv", "d"}, stdin: "y\nn\ny\ny\ny\n", seedTree: rmTree},
		{name: "an empty child accepted", args: []string{"-riv", "d"}, stdin: "y\ny\ny\n", seedTree: rmNested},
		{name: "an empty child declined", args: []string{"-riv", "d"}, stdin: "y\ny\nn\ny\n", seedTree: rmNested},
		{name: "a declined grandchild silences both above it", args: []string{"-riv", "d"}, stdin: "y\ny\nn\ny\ny\ny\n", seedTree: rmNestedDirs},
		{name: "a declined file still asks the parent", args: []string{"-riv", "d"}, stdin: "y\nn\ny\ny\n", seedTree: rmNested},
		{name: "and the failure silences the parent above", args: []string{"-riv", "d"}, stdin: "y\ny\nn\ny\ny\n", seedTree: rmNestedFile},
		{name: "an empty directory under -r -i", args: []string{"-riv", "emp"}, stdin: "y\n", seedTree: rmEmpty},
		{name: "declined", args: []string{"-riv", "emp"}, stdin: "n\n", seedTree: rmEmpty},
		{name: "an empty directory under -d -i", args: []string{"-div", "emp"}, stdin: "y\n", seedTree: rmEmpty},

		{name: "the hidden option on a plain file", args: []string{"---presume-input-tty", "-v", "a"}, seedTree: rmFlat},
		{name: "the hidden option over a tree", args: []string{"-rv", "---presume-input-tty", "d"}, seedTree: rmTree},
		{name: "one-file-system on a plain file", args: []string{"--one-file-system", "-v", "a"}, seedTree: rmFlat},
		{name: "one-file-system within one device", args: []string{"-rv", "--one-file-system", "d"}, seedTree: rmTree},
		{name: "preserve-root=all within one device", args: []string{"-rv", "--preserve-root=all", "d"}, seedTree: rmTree},
		{name: "no-preserve-root elsewhere", args: []string{"-rv", "--no-preserve-root", "d"}, seedTree: rmTree},
		{name: "no-preserve-root then preserve-root", args: []string{"--no-preserve-root", "--preserve-root", "-r", "/"}, seedTree: rmFlat},
		{name: "no-preserve-root then preserve-root=all", args: []string{"--no-preserve-root", "--preserve-root=all", "-r", "/"}, seedTree: rmFlat},
		{name: "both, over an ordinary tree", args: []string{"--preserve-root=all", "--no-preserve-root", "-r", "d"}, seedTree: rmTree},
		{name: "no-preserve-root on a missing path", args: []string{"-rv", "--no-preserve-root", "/nonexistentzz"}, seedTree: rmFlat},

		{name: "verbose to a closed stdout", args: []string{"-v", "a", "b"}, seedTree: rmFlat, stdout: stdoutClosed},
		{name: "verbose to a full stdout", args: []string{"-v", "a", "b"}, seedTree: rmFlat, stdout: stdoutFull},
		{name: "a diagnostic flushes the buffer first", args: []string{"-v", "a", "nosuchz"}, seedTree: rmFlat, stdout: stdoutFull},
		{name: "the same on a closed stdout", args: []string{"-v", "a", "nosuchz"}, seedTree: rmFlat, stdout: stdoutClosed},
		{name: "a silent run to a closed stdout", args: []string{"a"}, seedTree: rmFlat, stdout: stdoutClosed},
		{name: "a silent run to a full stdout", args: []string{"a"}, seedTree: rmFlat, stdout: stdoutFull},
		{name: "a usage error to a closed stdout", args: []string{"--foo"}, stdout: stdoutClosed},
		{name: "a missing operand to a closed stdout", stdout: stdoutClosed},
		{name: "-f with no operands to a closed stdout", args: []string{"-f"}, stdout: stdoutClosed},
		{name: "a declined -I to a closed stdout", args: []string{"-I", "a", "b", "c", "d"}, stdin: "n\n", seedTree: rmFlat, stdout: stdoutClosed},
		{name: "prompts and a full stdout", args: []string{"-iv", "a", "b"}, stdin: "y\ny\n", seedTree: rmFlat, stdout: stdoutFull},
	}
}

func rmEmptyCwd(t *testing.T, dir string) {
	t.Helper()
}

func rmDashFile(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "-", "")
}

func rmFifoTree(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d")
	seedFifo(t, filepath.Join(dir, "d/p"))
	seedWrite(t, dir, "d/f", "")
}

func rmOptDir(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "-d")
	seedWrite(t, dir, "-d/x", "")
}

func rmSymEmpty(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "sd")
	seedSymlink(t, dir, "sd", "sl")
}

func rmDots(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "...")
}

func rmDotPrefix(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d/..x")
}

func rmSymInTree(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d/sub")
	seedWrite(t, dir, "d/sub/x", "")
	seedSymlink(t, dir, "sub", "d/link")
}

func rmTreeAndFlat(t *testing.T, dir string) {
	t.Helper()
	rmTree(t, dir)
	rmFlat(t, dir)
}

func rmLongLine(b string, n int) string {
	out := make([]byte, n+1)
	for i := 0; i < n; i++ {
		out[i] = b[0]
	}
	out[n] = '\n'
	return string(out)
}

func TestRmParity(t *testing.T) {
	requireParity(t, "rm", rmCases(t))
}

func TestRmHelpVersion(t *testing.T) {
	requireHelp(t, "rm", []string{"--help"}, 0)
	requireHelp(t, "rm", []string{"--h"}, 0)
	requireHelp(t, "rm", []string{"--he"}, 0)
	requireHelp(t, "rm", []string{"--help", "x"}, 0)
	requireHelp(t, "rm", []string{"x", "--help"}, 0)
	requireVersion(t, "rm", []string{"--version"}, 0)
	requireVersion(t, "rm", []string{"--vers"}, 0)
	requireVersion(t, "rm", []string{"x", "--version"}, 0)
}
