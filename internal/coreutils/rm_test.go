package coreutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// rm(1) is the first utility in the corpus that both MUTATES a tree and
// PROMPTS, so nearly every case below runs under `seedTree`: the comparison
// is of the two directories left behind as well as of the two streams, and
// a run that removed one file too many fails even when it said nothing.
//
// Two things make the prompts deterministic from a pipe. The `-i` and `-I`
// prompts do not consult the terminal at all, so feeding stdin is enough;
// the DEFAULT prompt — the one for a write-protected file — does, and the
// hidden `---presume-input-tty` is what forces GNU's answer to that
// question, so the cases that want it say so. As root nothing is
// write-protected on either side, so those cases prove the option parses
// and that both implementations agree; as an ordinary user they reach the
// `remove write-protected …` wording.
//
// What is NOT here, and why: `--one-file-system` and `--preserve-root=all`
// only DO anything across a mount point, and the harness cannot mount one,
// so they appear as the inert invocations that prove they parse and change
// nothing. The paths that need a file the test user cannot read or write
// are the same — as root there is no such file — and the `attempt removal
// of inaccessible directory` prompt GNU carries could not be reached from
// any uid. docs/COREUTILS.md records the gap.

func init() {
	registerCorpus("rm", rmCases)
}

// rmFlat is the one-level fixture: files with and without content, a
// directory, a symlink and a dangling one, and a fifo — one of each kind
// the per-file prompt names.
func rmFlat(t *testing.T, dir string) {
	t.Helper()
	rmWriteFile(t, dir, "a", "")
	rmWriteFile(t, dir, "b", "abc")
	rmWriteFile(t, dir, "c", "")
	rmMkdirAll(t, dir, "adir")
	rmSymlink(t, "a", dir, "sym")
	rmSymlink(t, "nowhere", dir, "dang")
	rmMkfifo(t, dir, "fifo")
}

// rmTree is the recursion fixture: a directory holding a file, a
// subdirectory with a file of its own, and a second file after it, so the
// depth-first order and the children-before-the-directory rule are both
// visible in `-v`.
func rmTree(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d")
	rmWriteFile(t, dir, "d/x", "")
	rmMkdirAll(t, dir, "d/e")
	rmWriteFile(t, dir, "d/e/y", "")
	rmWriteFile(t, dir, "d/z", "")
}

// rmDash is the fixture for the leading-hyphen hint: names that getopt
// would read as options, including a DANGLING symlink, which the hint's
// lstat finds where a stat would not.
func rmDash(t *testing.T, dir string) {
	t.Helper()
	rmWriteFile(t, dir, "-x", "")
	rmWriteFile(t, dir, "-r", "")
	rmWriteFile(t, dir, "-v", "")
	rmWriteFile(t, dir, "--foo", "")
	rmSymlink(t, "nowhere", dir, "-z")
}

// rmWide is 26 files created in reverse alphabetical order. rm passes fts
// no comparator, so `-v` prints them in the order the kernel hands them
// back rather than a sorted one, and creating them out of order is what
// tells the two apart.
func rmWide(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d")
	for c := 'z'; c >= 'a'; c-- {
		rmWriteFile(t, dir, "d/"+string(c), "")
	}
}

// rmMixed interleaves files, directories and a symlink so the walk's order
// is not simply "files then directories".
func rmMixed(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d")
	rmWriteFile(t, dir, "d/zz", "")
	rmMkdirAll(t, dir, "d/aa")
	rmWriteFile(t, dir, "d/aa/1", "")
	rmSymlink(t, "zz", dir, "d/ll")
	rmMkdirAll(t, dir, "d/bb")
	rmWriteFile(t, dir, "d/mm", "")
}

// rmDeep is four levels, to prove the post-order unwinding.
func rmDeep(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d/a/b/c")
	rmWriteFile(t, dir, "d/a/b/c/x", "")
	rmWriteFile(t, dir, "d/a/y", "")
	rmWriteFile(t, dir, "d/z", "")
}

// rmEmpty is one empty directory: the shape `-d` exists for, and the one
// whose `-r -i` prompt is `remove directory` rather than `descend into`.
func rmEmpty(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "emp")
}

// rmSymDir is a symlink to a non-empty directory, for the trailing-slash
// rule: `rm -r sl/` descends through the link and then fails to rmdir it.
func rmSymDir(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "sd")
	rmWriteFile(t, dir, "sd/x", "")
	rmSymlink(t, "sd", dir, "sl")
}

func rmDangling(t *testing.T, dir string) {
	t.Helper()
	rmSymlink(t, "nowhere", dir, "sl")
}

func rmLoop(t *testing.T, dir string) {
	t.Helper()
	rmSymlink(t, "loop", dir, "loop")
}

// rmNested is a directory holding a file and an empty directory, for the
// rule that declining a CHILD DIRECTORY's first prompt leaves its
// ancestors alone while declining a child FILE's does not.
func rmNested(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d")
	rmWriteFile(t, dir, "d/f", "")
	rmMkdirAll(t, dir, "d/e")
}

// rmNestedDirs is three levels of empty directories, so a decline at the
// deepest one is visible in what the two above it are never asked.
func rmNestedDirs(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d/e/g")
}

// rmNestedFile is a file one level down, so declining it and then
// declining its directory's post-order prompt still leaves the
// grandparent asked.
func rmNestedFile(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d/e")
	rmWriteFile(t, dir, "d/e/x", "")
}

func rmWriteFile(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func rmMkdirAll(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
}

func rmSymlink(t *testing.T, target, dir, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

func rmMkfifo(t *testing.T, dir, name string) {
	t.Helper()
	if err := syscall.Mkfifo(filepath.Join(dir, name), 0o644); err != nil {
		t.Fatalf("mkfifo %s: %v", name, err)
	}
}

// rmYes is one prompt answer per line, for a case that will be asked n times.
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
		// ---- the arities and the two standard options ----
		{name: "no operands"},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "force with no operands", args: []string{"-f"}},
		{name: "force with dashdash", args: []string{"-f", "--"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},

		// ---- getopt faults, and the hint GNU inserts before the Try line ----
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
		// `---` is the unique prefix `-` of the hidden option's own
		// leading-dash long name, so it is accepted and does nothing;
		// a fourth dash is not a prefix of anything.
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

		// ---- the two options with wordings of rm's own ----
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

		// ---- --interactive goes through argmatch over a synonym table ----
		{name: "an interactive value that is nothing", args: []string{"--interactive=bogus"}},
		{name: "argmatch is case sensitive", args: []string{"--interactive=NEVER"}},
		{name: "an empty interactive value is ambiguous", args: []string{"--interactive="}},
		{name: "a prefix of three synonyms is accepted", args: []string{"--interactive=n"}},
		{name: "a prefix of once", args: []string{"--interactive=o"}},
		{name: "a prefix of always", args: []string{"--interactive=a"}},
		{name: "a prefix of yes", args: []string{"--interactive=y"}},
		// The value is glued only: `never` here stays an operand.
		{name: "interactive never consumes the next token", args: []string{"--interactive", "never"}},

		// ---- -f / -i / -I / --interactive: last wins, and what each sets ----
		{name: "force then i", args: []string{"-f", "-i", "nosuchz"}},
		{name: "i then force", args: []string{"-i", "-f", "nosuchz"}},
		{name: "force then I", args: []string{"-f", "-I", "nosuchz"}},
		{name: "force then interactive never", args: []string{"-f", "--interactive=never", "nosuchz"}},
		{name: "interactive never alone", args: []string{"--interactive=never", "nosuchz"}},
		{name: "repeated flags are idempotent", args: []string{"-vv", "-ff", "-rr", "-dd", "a"}, seedTree: rmFlat},

		// ---- the plain removals, one per file kind ----
		{name: "one file", args: []string{"a"}, seedTree: rmFlat},
		{name: "two files verbosely", args: []string{"-v", "a", "b"}, seedTree: rmFlat},
		{name: "a symlink, not its target", args: []string{"-v", "sym"}, seedTree: rmFlat},
		{name: "a dangling symlink", args: []string{"-v", "dang"}, seedTree: rmFlat},
		{name: "a fifo", args: []string{"-v", "fifo"}, seedTree: rmFlat},
		{name: "a directory without -r", args: []string{"adir"}, seedTree: rmFlat},
		{name: "a directory among files", args: []string{"-v", "a", "adir", "b"}, seedTree: rmFlat},

		// ---- the error paths, each with its own errno ----
		{name: "a missing file", args: []string{"nosuchz"}, seedTree: rmFlat},
		{name: "a missing file under -f", args: []string{"-f", "nosuchz"}, seedTree: rmFlat},
		{name: "a missing parent", args: []string{"nodir/x"}, seedTree: rmFlat},
		{name: "a parent that is a file", args: []string{"a/x"}, seedTree: rmFlat},
		// -f passes over ENOTDIR as well as ENOENT: both mean "not there".
		{name: "a parent that is a file under -f", args: []string{"-f", "a/x"}, seedTree: rmFlat},
		{name: "the empty operand", args: []string{""}, seedTree: rmFlat},
		{name: "the empty operand under -f", args: []string{"-f", ""}, seedTree: rmFlat},
		// ELOOP and ENAMETOOLONG are NOT passed over.
		{name: "a symlink loop", args: []string{"loop/x"}, seedTree: rmLoop},
		{name: "a symlink loop under -f", args: []string{"-f", "loop/x"}, seedTree: rmLoop},
		{name: "a name too long", args: []string{long}, seedTree: rmFlat},
		{name: "a name too long under -f", args: []string{"-f", long}, seedTree: rmFlat},

		// ---- the names diagnostics and -v have to quote ----
		{name: "a name with a space", args: []string{"no such name"}, seedTree: rmFlat},
		{name: "a name with an apostrophe", args: []string{"a'b"}, seedTree: rmFlat},
		{name: "a name with a newline", args: []string{"a\nb"}, seedTree: rmFlat},
		{name: "a name that is not valid UTF-8", args: []string{"\xff\xfe"}, seedTree: rmFlat},
		{name: "a lone dash names a file called -", args: []string{"-"}, seedTree: rmFlat},

		// ---- fts trims a trailing RUN of slashes to one ----
		{name: "a file with a trailing slash", args: []string{"-v", "a/"}, seedTree: rmFlat},
		{name: "a file with three", args: []string{"-v", "a///"}, seedTree: rmFlat},
		{name: "and -f passes over that too", args: []string{"-f", "a/"}, seedTree: rmFlat},
		{name: "a directory with three", args: []string{"-rv", "d///"}, seedTree: rmTree},
		{name: "interior slashes survive", args: []string{"-rv", "d//e//"}, seedTree: rmTree},
		{name: "a missing name with three", args: []string{"-rv", "nosuchz///"}, seedTree: rmFlat},
		// A symlink to a directory with a trailing slash is descended
		// through and then fails to rmdir: the CONTENTS go.
		{name: "a symlink to a directory, with a slash", args: []string{"-rv", "sl/"}, seedTree: rmSymDir},
		{name: "the same under -d", args: []string{"-dv", "sl/"}, seedTree: rmSymDir},
		{name: "a dangling symlink with a slash", args: []string{"-rv", "sl/"}, seedTree: rmDangling},
		{name: "a dangling symlink with a slash under -f", args: []string{"-rf", "sl/"}, seedTree: rmDangling},
		// The prompt's own stat is what reports here, so the errno is
		// the STAT's rather than the unlink's.
		{name: "a dangling symlink with a slash under -i", args: []string{"-ri", "sl/"}, stdin: "y\n", seedTree: rmDangling},
		{name: "a file with a slash under -i", args: []string{"-i", "a/"}, stdin: "y\n", seedTree: rmFlat},
		{name: "a missing file under -i", args: []string{"-i", "nosuchz"}, stdin: "y\n", seedTree: rmFlat},
		{name: "-f does not quiet the prompt's stat", args: []string{"-f", "--interactive=always", "nosuchz"}, stdin: "y\n", seedTree: rmFlat},

		// ---- . and .. ----
		{name: "dot under -r", args: []string{"-r", "."}, seedTree: rmFlat},
		{name: "dot with a slash", args: []string{"-r", "./"}, seedTree: rmFlat},
		{name: "dot with two", args: []string{"-r", ".//"}, seedTree: rmFlat},
		{name: "dotdot under -rf", args: []string{"-rf", ".."}, seedTree: rmFlat},
		{name: "dotdot with two slashes", args: []string{"-r", "..//"}, seedTree: rmFlat},
		{name: "a directory reached as dot", args: []string{"-r", "d/./"}, seedTree: rmTree},
		{name: "a directory reached as dotdot", args: []string{"-r", "d/e/.."}, seedTree: rmTree},
		{name: "dot without -r is EISDIR", args: []string{"."}, seedTree: rmFlat},
		{name: "dot under -d in an empty directory", args: []string{"-d", "."}, seedTree: rmEmptyCwd},
		// The emptiness check runs BEFORE the dot check, so a non-empty
		// `.` is ENOTEMPTY rather than the refusal.
		{name: "dot under -d in a full one", args: []string{"-d", "."}, seedTree: rmFlat},
		{name: "dotdot under -d", args: []string{"-d", ".."}, seedTree: rmFlat},
		// The stat happens first: a missing path never reaches the check.
		{name: "a missing path ending in dot", args: []string{"-r", "nosuchz/."}, seedTree: rmFlat},
		{name: "the same under -f", args: []string{"-rf", "nosuchz/."}, seedTree: rmFlat},
		{name: "a file ending in dot", args: []string{"-r", "a/."}, seedTree: rmFlat},
		{name: "three dots is an ordinary name", args: []string{"-rv", "..."}, seedTree: rmDots},
		{name: "a name that starts with dotdot", args: []string{"-rv", "d/..x"}, seedTree: rmDotPrefix},

		// ---- the root failsafe, by device and inode ----
		{name: "the root directory", args: []string{"-rf", "/"}, seedTree: rmFlat},
		{name: "two slashes keep the same-as note", args: []string{"-r", "//"}, seedTree: rmFlat},
		{name: "three collapse to one", args: []string{"-r", "///"}, seedTree: rmFlat},
		{name: "four as well", args: []string{"-r", "////"}, seedTree: rmFlat},
		{name: "the root without -r is EISDIR", args: []string{"/"}, seedTree: rmFlat},
		{name: "the root under -d is ENOTEMPTY", args: []string{"-d", "/"}, seedTree: rmFlat},
		// The dot check runs first, so these never reach the failsafe.
		{name: "the root as dot", args: []string{"-r", "/."}, seedTree: rmFlat},
		{name: "the root as dotdot", args: []string{"-r", "/.."}, seedTree: rmFlat},
		{name: "the root reached through a child", args: []string{"-r", "/tmp/.."}, seedTree: rmFlat},
		{name: "preserve-root is the default", args: []string{"--preserve-root", "-r", "/"}, seedTree: rmFlat},
		{name: "and preserve-root=all keeps it", args: []string{"--preserve-root=all", "-r", "/"}, seedTree: rmFlat},
		{name: "the failsafe is not quieted by -v", args: []string{"-rv", "/"}, seedTree: rmFlat},

		// ---- recursion ----
		{name: "a tree", args: []string{"-rv", "d"}, seedTree: rmTree},
		{name: "a tree with -R", args: []string{"-Rv", "d"}, seedTree: rmTree},
		{name: "a tree with --recursive", args: []string{"--recursive", "-v", "d"}, seedTree: rmTree},
		{name: "a deep tree unwinds post-order", args: []string{"-rv", "d"}, seedTree: rmDeep},
		// Neither side sorts, so `-v` prints raw readdir order.
		{name: "26 names in readdir order", args: []string{"-rv", "d"}, seedTree: rmWide},
		{name: "files and directories interleaved", args: []string{"-rv", "d"}, seedTree: rmMixed},
		{name: "a symlink inside is not descended", args: []string{"-rv", "d"}, seedTree: rmSymInTree},
		{name: "an empty directory under -r", args: []string{"-rv", "emp"}, seedTree: rmEmpty},
		{name: "an empty directory under -d", args: []string{"-dv", "emp"}, seedTree: rmEmpty},
		{name: "a full directory under -d", args: []string{"-dv", "d"}, seedTree: rmTree},
		{name: "and -f does not help", args: []string{"-fdv", "d"}, seedTree: rmTree},
		{name: "a tree and a file", args: []string{"-rv", "d", "a"}, seedTree: rmTreeAndFlat},

		// ---- repeated and overlapping operands ----
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

		// ---- operand permutation ----
		{name: "an option after the operand", args: []string{"d", "-r"}, seedTree: rmTree},
		{name: "POSIXLY_CORRECT stops at the operand", args: []string{"d", "-r"}, seedTree: rmTree, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "an empty POSIXLY_CORRECT counts", args: []string{"d", "-r"}, seedTree: rmTree, env: []string{"POSIXLY_CORRECT="}},
		{name: "dashdash is an operand under it", args: []string{"a", "--", "-r"}, seedTree: rmFlat, env: []string{"POSIXLY_CORRECT=1"}},

		// ---- -I: one prompt, before anything is looked at ----
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

		// ---- -i: one prompt per file, answered by rpmatch ----
		{name: "both accepted", args: []string{"-iv", "a", "b"}, stdin: "y\ny\n", seedTree: rmFlat},
		{name: "the first declined", args: []string{"-iv", "a", "b"}, stdin: "n\ny\n", seedTree: rmFlat},
		// One LINE is consumed per prompt however long it is, so `yy`
		// answers the first prompt and the second meets end of input.
		{name: "one line answers one prompt", args: []string{"-i", "a", "b"}, stdin: "yy\n", seedTree: rmFlat},
		{name: "two lines answer two", args: []string{"-i", "a", "b"}, stdin: "y y\ny\n", seedTree: rmFlat},
		{name: "an upper-case yes", args: []string{"-i", "a"}, stdin: "Y\n", seedTree: rmFlat},
		{name: "only the first byte counts", args: []string{"-i", "a"}, stdin: " y\n", seedTree: rmFlat},
		{name: "end of input is no", args: []string{"-i", "a"}, seedTree: rmFlat},
		{name: "q is no", args: []string{"-i", "a"}, stdin: "q\n", seedTree: rmFlat},
		{name: "a very long yes", args: []string{"-i", "a"}, stdin: rmLongLine("y", 70000), seedTree: rmFlat},
		{name: "a very long no", args: []string{"-i", "a"}, stdin: rmLongLine("x", 70000) + "y\n", seedTree: rmFlat},
		// The per-file wording, one prompt per file kind.
		{name: "every file kind declined", args: []string{"-i", "-d", "a", "b", "adir", "sym", "dang", "fifo"}, stdin: "n\nn\nn\nn\nn\nn\n", seedTree: rmFlat},
		{name: "every file kind accepted", args: []string{"-i", "-d", "a", "b", "adir", "sym", "dang", "fifo"}, stdin: rmYes(6), seedTree: rmFlat},
		// A directory without -r or -d never reaches a prompt.
		{name: "-i on a directory without -r", args: []string{"-i", "adir"}, stdin: "y\n", seedTree: rmFlat},
		{name: "bare --interactive is -i", args: []string{"--interactive", "a"}, stdin: "y\n", seedTree: rmFlat},
		{name: "-i then never", args: []string{"-i", "--interactive=never", "a"}, seedTree: rmFlat},
		{name: "-f then always", args: []string{"-f", "--interactive=always", "a"}, stdin: "y\n", seedTree: rmFlat},

		// ---- -r -i: descend, then remove on the way back up ----
		{name: "a tree with every prompt accepted", args: []string{"-riv", "d"}, stdin: rmYes(8), seedTree: rmTree},
		{name: "the descent declined", args: []string{"-riv", "d"}, stdin: "n\n", seedTree: rmTree},
		{name: "a child declined", args: []string{"-riv", "d"}, stdin: "y\nn\ny\ny\ny\n", seedTree: rmTree},
		// An EMPTY directory gets one prompt, at its first visit, and
		// declining it leaves its ancestors unasked.
		{name: "an empty child accepted", args: []string{"-riv", "d"}, stdin: "y\ny\ny\n", seedTree: rmNested},
		{name: "an empty child declined", args: []string{"-riv", "d"}, stdin: "y\ny\nn\ny\n", seedTree: rmNested},
		{name: "a declined grandchild silences both above it", args: []string{"-riv", "d"}, stdin: "y\ny\nn\ny\ny\ny\n", seedTree: rmNestedDirs},
		// A declined FILE does not: the directory above it is still
		// asked, and its rmdir then fails.
		{name: "a declined file still asks the parent", args: []string{"-riv", "d"}, stdin: "y\nn\ny\ny\n", seedTree: rmNested},
		{name: "and the failure silences the parent above", args: []string{"-riv", "d"}, stdin: "y\ny\nn\ny\ny\n", seedTree: rmNestedFile},
		{name: "an empty directory under -r -i", args: []string{"-riv", "emp"}, stdin: "y\n", seedTree: rmEmpty},
		{name: "declined", args: []string{"-riv", "emp"}, stdin: "n\n", seedTree: rmEmpty},
		{name: "an empty directory under -d -i", args: []string{"-div", "emp"}, stdin: "y\n", seedTree: rmEmpty},

		// ---- the hidden option, and the options that need a mount to act ----
		{name: "the hidden option on a plain file", args: []string{"---presume-input-tty", "-v", "a"}, seedTree: rmFlat},
		{name: "the hidden option over a tree", args: []string{"-rv", "---presume-input-tty", "d"}, seedTree: rmTree},
		{name: "one-file-system on a plain file", args: []string{"--one-file-system", "-v", "a"}, seedTree: rmFlat},
		{name: "one-file-system within one device", args: []string{"-rv", "--one-file-system", "d"}, seedTree: rmTree},
		{name: "preserve-root=all within one device", args: []string{"-rv", "--preserve-root=all", "d"}, seedTree: rmTree},
		{name: "no-preserve-root elsewhere", args: []string{"-rv", "--no-preserve-root", "d"}, seedTree: rmTree},
		// --no-preserve-root clears the `/` failsafe and a later
		// --preserve-root puts it back. The OTHER order is deliberately
		// absent: `--preserve-root=all --no-preserve-root -r /` really
		// does remove the root on both implementations — verified in a
		// chroot rather than here — so it is not a case to run on a
		// machine one wants afterwards.
		{name: "no-preserve-root then preserve-root", args: []string{"--no-preserve-root", "--preserve-root", "-r", "/"}, seedTree: rmFlat},
		{name: "no-preserve-root then preserve-root=all", args: []string{"--no-preserve-root", "--preserve-root=all", "-r", "/"}, seedTree: rmFlat},
		{name: "both, over an ordinary tree", args: []string{"--preserve-root=all", "--no-preserve-root", "-r", "d"}, seedTree: rmTree},
		{name: "no-preserve-root on a missing path", args: []string{"-rv", "--no-preserve-root", "/nonexistentzz"}, seedTree: rmFlat},

		// ---- the write-failure paths ----
		// -v writes to stdout, and the failure is only found at exit —
		// after every removal, so the files are gone either way.
		{name: "verbose to a closed stdout", args: []string{"-v", "a", "b"}, seedTree: rmFlat, stdout: stdoutClosed},
		{name: "verbose to a full stdout", args: []string{"-v", "a", "b"}, seedTree: rmFlat, stdout: stdoutFull},
		// A diagnostic flushes stdout first, so the failure is met
		// before the exit and is then reported WITHOUT an errno.
		{name: "a diagnostic flushes the buffer first", args: []string{"-v", "a", "nosuchz"}, seedTree: rmFlat, stdout: stdoutFull},
		{name: "the same on a closed stdout", args: []string{"-v", "a", "nosuchz"}, seedTree: rmFlat, stdout: stdoutClosed},
		// Without -v nothing is written, so nothing fails.
		{name: "a silent run to a closed stdout", args: []string{"a"}, seedTree: rmFlat, stdout: stdoutClosed},
		{name: "a silent run to a full stdout", args: []string{"a"}, seedTree: rmFlat, stdout: stdoutFull},
		{name: "a usage error to a closed stdout", args: []string{"--foo"}, stdout: stdoutClosed},
		{name: "a missing operand to a closed stdout", stdout: stdoutClosed},
		{name: "-f with no operands to a closed stdout", args: []string{"-f"}, stdout: stdoutClosed},
		{name: "a declined -I to a closed stdout", args: []string{"-I", "a", "b", "c", "d"}, stdin: "n\n", seedTree: rmFlat, stdout: stdoutClosed},
		{name: "prompts and a full stdout", args: []string{"-iv", "a", "b"}, stdin: "y\ny\n", seedTree: rmFlat, stdout: stdoutFull},
	}
}

// rmEmptyCwd leaves the working directory as it found it: empty.
func rmEmptyCwd(t *testing.T, dir string) {
	t.Helper()
}

// rmDashFile is a file named `-`, which the hint skips: it wants an
// element longer than one byte.
func rmDashFile(t *testing.T, dir string) {
	t.Helper()
	rmWriteFile(t, dir, "-", "")
}

// rmFifoTree puts a kind the walk has to unlink rather than descend
// into next to a plain file.
func rmFifoTree(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d")
	rmMkfifo(t, dir, "d/p")
	rmWriteFile(t, dir, "d/f", "")
}

// rmOptDir is a directory whose name getopt would read as an option.
func rmOptDir(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "-d")
	rmWriteFile(t, dir, "-d/x", "")
}

// rmSymEmpty is a symlink to an EMPTY directory: `-d sl` removes the
// link, `-d sl/` reaches the directory and fails to rmdir it.
func rmSymEmpty(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "sd")
	rmSymlink(t, "sd", dir, "sl")
}

func rmDots(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "...")
}

func rmDotPrefix(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d/..x")
}

func rmSymInTree(t *testing.T, dir string) {
	t.Helper()
	rmMkdirAll(t, dir, "d/sub")
	rmWriteFile(t, dir, "d/sub/x", "")
	rmSymlink(t, "sub", dir, "d/link")
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
