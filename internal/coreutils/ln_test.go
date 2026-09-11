package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// ln(1) writes almost nothing, so nearly every case here is about the TREE
// the two implementations leave behind: which names exist, which of them
// share an inode, and what a symbolic link holds. `seedTree` gives each side
// its own freshly built working directory and compares everything under it,
// so a stray temporary name fails as loudly as a missing link.
//
// The three places that comparison earns its keep:
//
//   - A backup is the destination MOVED, not copied. `ln a b; ln -b a b`
//     leaves a, b and b~ all on one inode with three links, and an
//     implementation that copied or re-linked would show two groups.
//   - GNU's replace path makes the new link beside the destination and
//     renames it over — so `ln -sf <a target too long> b` leaves b exactly
//     as it was, and the temporary name is never in the tree.
//   - `ln -f d/a a` relinks one name of a pair to the same inode. rename(2)
//     does nothing when both names already reach one file, so the temporary
//     name has to be dealt with rather than renamed away.

func lnWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func lnMkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
}

func lnSymlink(t *testing.T, dir, target, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

func lnHardlink(t *testing.T, dir, old, name string) {
	t.Helper()
	if err := os.Link(filepath.Join(dir, old), filepath.Join(dir, name)); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

// lnBasic is the workhorse fixture: two plain files, a directory, a symlink
// to a file and a dangling one. `a` is the usual TARGET and `b` the usual
// occupied destination.
func lnBasic(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnWrite(t, dir, "b", "B\n")
	lnMkdir(t, dir, "adir")
	lnSymlink(t, dir, "a", "sl")
	lnSymlink(t, dir, "nowhere", "dangling")
}

// lnOne is one file and nothing else — the fixture for the operand-count
// diagnostics, where anything more would be noise.
func lnOne(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
}

// lnDir is a target DIRECTORY and two files to put in it, one of which is
// already there so the 3rd form has a failing operand among the good ones.
func lnDir(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnWrite(t, dir, "b", "B\n")
	lnWrite(t, dir, "c", "C\n")
	lnMkdir(t, dir, "d")
	lnWrite(t, dir, "d/b", "old b\n")
}

// lnSymDir is the `-n` fixture: a directory and a symbolic link to it, which
// is the one destination whose meaning the option changes.
func lnSymDir(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnMkdir(t, dir, "d")
	lnSymlink(t, dir, "d", "dl")
}

// lnPair is two names on ONE inode, plus a directory holding a third. The
// same-file check is about directory ENTRIES, not inodes, and this is what
// tells the two apart.
func lnPair(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnMkdir(t, dir, "d")
	lnHardlink(t, dir, "a", "d/a")
}

// lnBackupSimple is the plain backup fixture, and lnBackupTaken adds a `b~`
// that the backup has to replace.
func lnBackupSimple(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnWrite(t, dir, "b", "B\n")
}

func lnBackupTaken(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnWrite(t, dir, "b~", "OLD\n")
}

// lnBackupDir is the destination whose backup name is a DIRECTORY: the
// rename is EISDIR, which nothing built out of link(2) and unlink(2) can
// produce.
func lnBackupDir(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnMkdir(t, dir, "b~")
}

// lnLinked is a destination that is already a second name for the target —
// the case where the backup moves the shared inode and the new link raises
// its count to three.
func lnLinked(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnHardlink(t, dir, "a", "b")
}

// The numbered-backup fixtures. GNU takes max(N)+1 over names matching
// exactly `NAME.~<digits>~` with no leading zero, counts a DIRECTORY named
// that way, and compares numerically rather than lexically.
func lnNumbered(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnWrite(t, dir, "b.~1~", "one\n")
}

func lnNumberedNine(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnWrite(t, dir, "b.~9~", "nine\n")
}

func lnNumberedGap(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnWrite(t, dir, "b.~3~", "three\n")
	lnWrite(t, dir, "b.~10~", "ten\n")
}

func lnNumberedJunk(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnWrite(t, dir, "b.~09~", "leading zero\n")
	lnWrite(t, dir, "b.~x~", "not a number\n")
	lnWrite(t, dir, "b.~-1~", "negative\n")
	lnWrite(t, dir, "b.~0~", "zero\n")
}

func lnNumberedDir(t *testing.T, dir string) {
	t.Helper()
	lnBackupSimple(t, dir)
	lnMkdir(t, dir, "b.~1~")
}

// lnNested is the `-r` fixture: two branches deep enough that the relative
// answer needs `..`, plus a symbolic link on each side so the
// canonicalisation is visible.
func lnNested(t *testing.T, dir string) {
	t.Helper()
	lnMkdir(t, dir, "x/y")
	lnWrite(t, dir, "x/y/f", "F\n")
	lnMkdir(t, dir, "p/q")
	lnWrite(t, dir, "a", "A\n")
	lnMkdir(t, dir, "d")
	lnWrite(t, dir, "d/f", "DF\n")
	lnSymlink(t, dir, "d", "dl")
	lnSymlink(t, dir, "x", "xl")
}

// lnLoop is a pair of symbolic links pointing at each other. Canonicalising
// through them runs out of link budget, and GNU keeps the component as
// written rather than failing — `realpath -m l1` answers `<cwd>/l1`.
func lnLoop(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnSymlink(t, dir, "l2", "l1")
	lnSymlink(t, dir, "l1", "l2")
	lnMkdir(t, dir, "sub")
}

// lnSrcSlash is a target reached through a path with trailing slashes: the
// destination's name drops them and a symbolic link's stored text keeps them.
func lnSrcSlash(t *testing.T, dir string) {
	t.Helper()
	lnMkdir(t, dir, "s")
	lnWrite(t, dir, "s/f", "F\n")
	lnMkdir(t, dir, "d")
}

// lnNotDir is a plain file where a directory operand is expected, the fault
// GNU words two different ways depending on which form reached it.
func lnNotDir(t *testing.T, dir string) {
	t.Helper()
	lnMkdir(t, dir, "n1")
	lnWrite(t, dir, "n1/f", "F\n")
	lnWrite(t, dir, "a", "A\n")
	lnWrite(t, dir, "b", "B\n")
}

// lnQuoting is the names whose diagnostics differ between quote, quotef and
// quoteaf: a space, an apostrophe (which makes gnulib reach for double
// quotes), a leading `~`, and one that is not valid UTF-8.
func lnQuoting(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a", "A\n")
	lnWrite(t, dir, "sp ace", "S\n")
	lnWrite(t, dir, "ap'os", "Q\n")
	lnWrite(t, dir, "bad\xff", "N\n")
	lnMkdir(t, dir, "d x")
}

// lnPrompt is the `-i` fixture with several occupied destinations, so one
// decline among several answers still costs the exit status while the
// accepted ones are relinked.
func lnPrompt(t *testing.T, dir string) {
	t.Helper()
	lnWrite(t, dir, "a1", "A1\n")
	lnWrite(t, dir, "a2", "A2\n")
	lnMkdir(t, dir, "dd")
	lnWrite(t, dir, "dd/a1", "old 1\n")
	lnWrite(t, dir, "dd/a2", "old 2\n")
}

func init() {
	registerCorpus("ln", lnCases)
}

// lnCases is ln(1)'s corpus.
func lnCases(t *testing.T) []invocation {
	return []invocation{
		// ---- the four forms ---------------------------------------------
		{name: "two operands", args: []string{"a", "new"}, seedTree: lnBasic},
		{name: "one operand", args: []string{"s/f"}, seedTree: lnSrcSlash},
		{name: "one operand is verbose as ./NAME", args: []string{"-v", "s/f"}, seedTree: lnSrcSlash},
		{name: "one operand keeps the target's trailing slashes", args: []string{"-sv", "s/f///"}, seedTree: lnSrcSlash},
		{name: "one operand whose base name is taken", args: []string{"a"}, seedTree: lnOne},
		{name: "one operand dotdot", args: []string{"-sv", ".."}, seedTree: lnOne},
		{name: "many operands into a directory", args: []string{"-v", "a", "c", "d"}, seedTree: lnDir},
		{name: "a failing operand does not stop the rest", args: []string{"-v", "a", "b", "c", "d"}, seedTree: lnDir},
		{name: "two operands where the last is a directory", args: []string{"-v", "a", "d"}, seedTree: lnDir},
		{name: "the directory join uses one slash", args: []string{"-v", "s/f", "d///"}, seedTree: lnSrcSlash},
		{name: "a directory operand repeated as a target", args: []string{"-v", "a", "d", "d"}, seedTree: lnDir},

		// ---- -s ----------------------------------------------------------
		{name: "symbolic", args: []string{"-sv", "a", "link"}, seedTree: lnBasic},
		{name: "symbolic to a missing target", args: []string{"-sv", "nowhere-at-all", "link"}, seedTree: lnBasic},
		{name: "symbolic into a directory", args: []string{"-sv", "a", "d"}, seedTree: lnDir},
		{name: "symbolic is repeatable", args: []string{"-ss", "a", "link"}, seedTree: lnBasic},
		{name: "symbolic long is repeatable", args: []string{"--sym", "--sym", "a", "link"}, seedTree: lnBasic},
		{name: "symbolic takes no argument", args: []string{"--sym=", "a", "link"}, seedTree: lnBasic},
		{name: "symbolic over an existing name", args: []string{"-s", "a", "b"}, seedTree: lnBasic},
		{name: "symbolic with an empty target", args: []string{"-s", "", "link"}, seedTree: lnBasic},
		{name: "symbolic with an empty destination", args: []string{"-s", "a", ""}, seedTree: lnBasic},
		{name: "symbolic ignores -L", args: []string{"-sLv", "sl", "link"}, seedTree: lnBasic},
		{name: "symbolic ignores -P", args: []string{"-sPv", "sl", "link"}, seedTree: lnBasic},

		// ---- -L / -P -----------------------------------------------------
		{name: "a hard link to a symlink is a second symlink", args: []string{"-v", "sl", "h"}, seedTree: lnBasic},
		{name: "-L follows the target", args: []string{"-Lv", "sl", "h"}, seedTree: lnBasic},
		{name: "-P is the default", args: []string{"-Pv", "sl", "h"}, seedTree: lnBasic},
		{name: "-L then -P is physical", args: []string{"-L", "-P", "-v", "sl", "h"}, seedTree: lnBasic},
		{name: "-P then -L is logical", args: []string{"-P", "-L", "-v", "sl", "h"}, seedTree: lnBasic},
		{name: "-L on a dangling symlink cannot access it", args: []string{"-L", "dangling", "h"}, seedTree: lnBasic},
		{name: "-P on a dangling symlink links the link", args: []string{"-Pv", "dangling", "h"}, seedTree: lnBasic},
		{name: "-L names the operand as typed", args: []string{"-Lfv", "sl", "h"}, seedTree: lnBasic},

		// ---- -f / -i -----------------------------------------------------
		{name: "-f replaces", args: []string{"-fv", "a", "b"}, seedTree: lnBasic},
		{name: "-f replaces a symlink", args: []string{"-fv", "a", "sl"}, seedTree: lnBasic},
		{name: "-f replaces a dangling symlink", args: []string{"-fv", "a", "dangling"}, seedTree: lnBasic},
		{name: "without -f an existing name is refused", args: []string{"a", "b"}, seedTree: lnBasic},
		{name: "-f then -i prompts", args: []string{"-f", "-i", "a", "b"}, seedTree: lnBasic},
		{name: "-i then -f does not", args: []string{"-i", "-f", "a", "b"}, seedTree: lnBasic},
		{name: "-i declining at end of input", args: []string{"-i", "a", "b"}, seedTree: lnBasic},
		{name: "-i accepting", args: []string{"-i", "a", "b"}, stdin: "y", seedTree: lnBasic},
		{name: "-i accepting with a newline", args: []string{"-i", "a", "b"}, stdin: "Y\n", seedTree: lnBasic},
		{name: "-i accepting a whole word", args: []string{"-i", "a", "b"}, stdin: "yes\n", seedTree: lnBasic},
		{name: "-i declining a whole word", args: []string{"-i", "a", "b"}, stdin: "no\n", seedTree: lnBasic},
		{name: "-i on an unclear answer", args: []string{"-i", "a", "b"}, stdin: "maybe\n", seedTree: lnBasic},
		{name: "-i on an empty line", args: []string{"-i", "a", "b"}, stdin: "\n", seedTree: lnBasic},
		{name: "-i where nothing is in the way", args: []string{"-iv", "a", "fresh"}, seedTree: lnBasic},
		{name: "-i once per operand", args: []string{"-iv", "a1", "a2", "dd"}, stdin: "n\ny\n", seedTree: lnPrompt},
		{name: "-i with backups", args: []string{"-ivb", "a1", "a2", "dd"}, stdin: "y\ny\n", seedTree: lnPrompt},

		// ---- the same-file rule ------------------------------------------
		{name: "-f a file onto itself", args: []string{"-f", "a", "a"}, seedTree: lnOne},
		{name: "-f the same entry spelled differently", args: []string{"-f", "./a", "a"}, seedTree: lnOne},
		{name: "-f the same entry through dotdot", args: []string{"-f", "d/../a", "a"}, seedTree: lnPair},
		{name: "-f a different entry on one inode", args: []string{"-fv", "d/a", "a"}, seedTree: lnPair},
		{name: "without -f the same name is only EEXIST", args: []string{"a", "a"}, seedTree: lnOne},
		{name: "-i onto itself, accepted", args: []string{"-i", "a", "a"}, stdin: "y\n", seedTree: lnOne},
		{name: "-i onto itself, declined", args: []string{"-i", "a", "a"}, stdin: "n\n", seedTree: lnOne},
		{name: "-sf a symlink onto itself", args: []string{"-sfv", "sl", "sl"}, seedTree: lnBasic},
		{name: "-Lf a symlink onto itself", args: []string{"-Lfv", "sl", "sl"}, seedTree: lnBasic},
		{name: "-Pf a symlink onto itself", args: []string{"-Pf", "sl", "sl"}, seedTree: lnBasic},
		{name: "-sf a missing name onto itself", args: []string{"-sfv", "nosuch", "nosuch"}, seedTree: lnBasic},
		{name: "-sf a dereferenced source onto itself", args: []string{"-sf", "./a", "a"}, seedTree: lnOne},

		// ---- -n ------------------------------------------------------------
		{name: "-n treats a symlinked directory as a file", args: []string{"-n", "a", "dl"}, seedTree: lnSymDir},
		{name: "without -n the link lands inside", args: []string{"-v", "a", "dl"}, seedTree: lnSymDir},
		{name: "-sfn replaces the symlink", args: []string{"-sfnv", "a", "dl"}, seedTree: lnSymDir},
		{name: "-sf without -n writes through it", args: []string{"-sfv", "a", "dl"}, seedTree: lnSymDir},
		{name: "-n with a backup", args: []string{"-nbv", "a", "dl"}, seedTree: lnSymDir},
		{name: "-n on a plain destination", args: []string{"-nv", "a", "fresh"}, seedTree: lnBasic},
		{name: "-n and -t together lstat the directory", args: []string{"-nt", "dl", "a"}, seedTree: lnSymDir},
		{name: "-t before -n still lstats", args: []string{"-t", "dl", "-n", "a"}, seedTree: lnSymDir},
		{name: "-t on a symlinked directory follows it", args: []string{"-vt", "dl", "a"}, seedTree: lnSymDir},

		// ---- -r ------------------------------------------------------------
		{name: "-r without -s", args: []string{"-r", "a", "l"}, seedTree: lnNested},
		{name: "-r across two branches", args: []string{"-srv", "x/y/f", "p/q/l"}, seedTree: lnNested},
		{name: "-r into the target's own directory", args: []string{"-srv", "d", "d/self"}, seedTree: lnNested},
		{name: "-r producing dotdot", args: []string{"-srv", ".", "d/x"}, seedTree: lnNested},
		{name: "-r canonicalises a symlinked source path", args: []string{"-srv", "xl/y/f", "l"}, seedTree: lnNested},
		{name: "-r canonicalises a symlinked destination path", args: []string{"-srv", "d/f", "dl/g"}, seedTree: lnNested},
		{name: "-r drops the target's trailing slashes", args: []string{"-srv", "d/f///", "l"}, seedTree: lnNested},
		{name: "-r applies after -t", args: []string{"-srvt", "d", "a"}, seedTree: lnNested},
		{name: "-r with -T", args: []string{"-srvT", "a", "d/l"}, seedTree: lnNested},
		{name: "-r on a name that does not exist", args: []string{"-srv", "nosuch-abc", "l"}, seedTree: lnNested},
		{name: "-r through a dot component", args: []string{"-srv", "././a", "l"}, seedTree: lnNested},
		{name: "-r through a missing dotdot component", args: []string{"-srv", "nosuch/../a", "l"}, seedTree: lnNested},
		{name: "-r into a directory operand", args: []string{"-srv", "a", "d"}, seedTree: lnNested},
		{name: "-r out of a symlink loop", args: []string{"-srv", "l1", "sub/b"}, seedTree: lnLoop},
		{name: "-r out of a symlink loop with a tail", args: []string{"-srv", "l2/x", "sub/b"}, seedTree: lnLoop},
		{name: "-r with the plain -s answer for comparison", args: []string{"-sv", "x/y/f", "p/q/l"}, seedTree: lnNested},

		// ---- -t / -T -------------------------------------------------------
		{name: "-t", args: []string{"-vt", "d", "a", "c"}, seedTree: lnDir},
		{name: "-t collapses trailing slashes", args: []string{"-vt", "d///", "s/f"}, seedTree: lnSrcSlash},
		{name: "-t twice", args: []string{"-t", "d", "-t", "d", "a"}, seedTree: lnDir},
		{name: "-t on a missing directory", args: []string{"-t", "nosuch", "a"}, seedTree: lnDir},
		{name: "-t on a plain file", args: []string{"-t", "b", "a"}, seedTree: lnDir},
		{name: "-t with no targets", args: []string{"-t", "d"}, seedTree: lnDir},
		{name: "-t and -T", args: []string{"-T", "-t", "d", "a"}, seedTree: lnDir},
		{name: "-t and -T the other way round", args: []string{"-t", "d", "-T", "a"}, seedTree: lnDir},
		{name: "-t on a plain file beats the -T conflict", args: []string{"-t", "b", "-T", "a"}, seedTree: lnDir},
		{name: "-t glued to a cluster", args: []string{"-vtd", "a"}, seedTree: lnDir},
		{name: "-t eats the rest of its cluster", args: []string{"-tn", "dl", "a"}, seedTree: lnSymDir},
		{name: "-t with no argument", args: []string{"-t"}, seedTree: lnDir},
		{name: "--target-directory with no argument", args: []string{"--target-directory"}, seedTree: lnDir},
		{name: "-T", args: []string{"-Tv", "a", "new"}, seedTree: lnBasic},
		{name: "-T onto a directory", args: []string{"-T", "a", "d"}, seedTree: lnDir},
		{name: "-fT onto a directory", args: []string{"-fT", "a", "d"}, seedTree: lnDir},
		{name: "-bT onto a directory", args: []string{"-bT", "a", "d"}, seedTree: lnDir},
		{name: "-iT onto a directory", args: []string{"-iT", "a", "d"}, seedTree: lnDir},
		{name: "-sfT onto a directory", args: []string{"-sfT", "a", "d"}, seedTree: lnDir},
		{name: "-T with one operand", args: []string{"-T", "a"}, seedTree: lnOne},
		{name: "-T with three operands", args: []string{"-T", "a", "b", "c"}, seedTree: lnDir},
		{name: "-T twice", args: []string{"-T", "-T", "a", "b"}, seedTree: lnBasic},
		{name: "-T keeps the trailing slash", args: []string{"-T", "a", "d/"}, seedTree: lnDir},

		// ---- -d / -F -------------------------------------------------------
		{name: "a directory target is refused", args: []string{"adir", "e"}, seedTree: lnBasic},
		{name: "-d asks the kernel anyway", args: []string{"-d", "adir", "e"}, seedTree: lnBasic},
		{name: "-F is -d", args: []string{"-F", "adir", "e"}, seedTree: lnBasic},
		{name: "--directory is -d", args: []string{"--directory", "adir", "e"}, seedTree: lnBasic},
		{name: "-d on a plain file", args: []string{"-dv", "a", "e"}, seedTree: lnBasic},
		{name: "-d is ignored under -s", args: []string{"-sdv", "adir", "e"}, seedTree: lnBasic},
		{name: "a directory name needing quotes", args: []string{"d x", "q"}, seedTree: lnQuoting},
		{name: "-fT over a directory needing quotes", args: []string{"-fT", "a", "d x"}, seedTree: lnQuoting},

		// ---- backups -------------------------------------------------------
		{name: "-b", args: []string{"-bv", "a", "b"}, seedTree: lnBackupSimple},
		{name: "-b over an existing backup", args: []string{"-bv", "a", "b"}, seedTree: lnBackupTaken},
		{name: "-b moves the destination", args: []string{"-bv", "a", "b"}, seedTree: lnLinked},
		{name: "-b where nothing is in the way", args: []string{"-bv", "a", "fresh"}, seedTree: lnBackupSimple},
		{name: "-b onto a dangling symlink", args: []string{"-bv", "a", "dangling"}, seedTree: lnBasic},
		{name: "-b onto a symlink under -s", args: []string{"-bsv", "a", "sl"}, seedTree: lnBasic},
		{name: "-b when the backup name is a directory", args: []string{"-bv", "a", "b"}, seedTree: lnBackupDir},
		{name: "-b inside a target directory", args: []string{"-bv", "b", "d"}, seedTree: lnDir},
		{name: "-b and -f together", args: []string{"-bfv", "a", "b"}, seedTree: lnBackupSimple},
		{name: "-f and -b together", args: []string{"-fbv", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup takes no operand as its argument", args: []string{"--backup", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup= is existing", args: []string{"--backup=", "-v", "a", "b"}, seedTree: lnNumbered},
		{name: "--backup=numbered", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=numbered past nine", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: lnNumberedNine},
		{name: "--backup=numbered counts numerically", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: lnNumberedGap},
		{name: "--backup=numbered ignores malformed neighbours", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: lnNumberedJunk},
		{name: "--backup=numbered counts a directory", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: lnNumberedDir},
		{name: "--backup=existing with none", args: []string{"--backup=existing", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=existing with one", args: []string{"--backup=existing", "-v", "a", "b"}, seedTree: lnNumbered},
		{name: "--backup=simple beside a numbered one", args: []string{"--backup=simple", "-v", "a", "b"}, seedTree: lnNumbered},
		{name: "--backup=none", args: []string{"--backup=none", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=off", args: []string{"--backup=off", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=never is simple", args: []string{"--backup=never", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=nil is existing", args: []string{"--backup=nil", "-v", "a", "b"}, seedTree: lnNumbered},
		{name: "--backup=t is numbered", args: []string{"--backup=t", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=none after -b", args: []string{"-b", "--backup=none", "a", "b"}, seedTree: lnBackupSimple},
		{name: "-b after --backup=none", args: []string{"--backup=none", "-b", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup unique prefixes", args: []string{"--backup=nu", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=ne is simple", args: []string{"--backup=ne", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=ni is existing", args: []string{"--backup=ni", "-v", "a", "b"}, seedTree: lnNumbered},
		{name: "--backup=o is none", args: []string{"--backup=o", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=n is ambiguous", args: []string{"--backup=n", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup is case sensitive", args: []string{"--backup=NUMBERED", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup=T is not t", args: []string{"--backup=T", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--backup on a blank argument", args: []string{"--backup= ", "a", "b"}, seedTree: lnBackupSimple},

		// ---- -S / --suffix -------------------------------------------------
		{name: "-S turns backups on", args: []string{"-S", ".bak", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "-S glued", args: []string{"-S.bak", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--suffix glued", args: []string{"--suffix=.bak", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--suffix split", args: []string{"--suffix", ".bak", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "the last -S wins", args: []string{"-S", ".x", "-S", ".y", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "an empty -S is ignored", args: []string{"-b", "-S", "", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "a -S with a slash inside is ignored", args: []string{"-b", "-S", "x/y", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "a leading-slash -S is ignored", args: []string{"-b", "-S", "/x", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "a lone slash -S is ignored", args: []string{"-b", "-S", "/", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "a trailing-slash -S is kept", args: []string{"-b", "-S", "x/", "a", "b"}, seedTree: lnBackupSimple},
		{name: "-S with --backup=none", args: []string{"--backup=none", "-S", ".bak", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--suffix with no argument", args: []string{"--suffix"}, seedTree: lnBackupSimple},
		{name: "-S with no argument", args: []string{"-S"}, seedTree: lnBackupSimple},

		// ---- the environment -----------------------------------------------
		{name: "VERSION_CONTROL", args: []string{"-bv", "a", "b"}, env: []string{"VERSION_CONTROL=numbered"}, seedTree: lnBackupSimple},
		{name: "--backup overrides VERSION_CONTROL", args: []string{"--backup=simple", "-v", "a", "b"}, env: []string{"VERSION_CONTROL=numbered"}, seedTree: lnBackupSimple},
		{name: "an empty VERSION_CONTROL is existing", args: []string{"-bv", "a", "b"}, env: []string{"VERSION_CONTROL="}, seedTree: lnBackupSimple},
		{name: "an invalid VERSION_CONTROL names the variable", args: []string{"-b", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: lnBackupSimple},
		{name: "VERSION_CONTROL is read only with backups on", args: []string{"-v", "a", "fresh"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: lnBackupSimple},
		{name: "VERSION_CONTROL is read for a bare -S", args: []string{"-S", ".z", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: lnBackupSimple},
		{name: "--backup= silences VERSION_CONTROL", args: []string{"--backup=simple", "-v", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: lnBackupSimple},
		{name: "SIMPLE_BACKUP_SUFFIX", args: []string{"-bv", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=.S"}, seedTree: lnBackupSimple},
		{name: "-S overrides SIMPLE_BACKUP_SUFFIX", args: []string{"-b", "-S", ".T", "-v", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=.S"}, seedTree: lnBackupSimple},
		{name: "an empty SIMPLE_BACKUP_SUFFIX is the default", args: []string{"-bv", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX="}, seedTree: lnBackupSimple},
		{name: "an unusable SIMPLE_BACKUP_SUFFIX is the default", args: []string{"-bv", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=x/y"}, seedTree: lnBackupSimple},

		// ---- the two arrow rules --------------------------------------------
		{name: "EEXIST has no arrow", args: []string{"a", "b"}, seedTree: lnBasic},
		{name: "ENOENT has one", args: []string{"a", "nodir/x"}, seedTree: lnBasic},
		{name: "ENOTDIR has one", args: []string{"a", "a/x"}, seedTree: lnBasic},
		{name: "EPERM has one", args: []string{"-d", "adir", "e"}, seedTree: lnBasic},
		{name: "ELOOP has one", args: []string{"a", "l1/x"}, seedTree: lnLoop},
		{name: "a symbolic link's EEXIST has no arrow", args: []string{"-s", "a", "b"}, seedTree: lnBasic},
		{name: "a symbolic link's empty destination has no arrow", args: []string{"-s", "a", ""}, seedTree: lnBasic},
		{name: "a symbolic link's empty target has one", args: []string{"-s", "", "link"}, seedTree: lnBasic},

		// ---- the target's own access check -----------------------------------
		{name: "a missing target", args: []string{"nosuch", "new"}, seedTree: lnBasic},
		{name: "a missing target with a directory destination", args: []string{"nosuch", "adir"}, seedTree: lnBasic},
		{name: "a missing target with an occupied destination", args: []string{"nosuch", "b"}, seedTree: lnBasic},
		{name: "a missing target under -d", args: []string{"-d", "nosuch", "b"}, seedTree: lnBasic},
		{name: "an empty target", args: []string{"", "new"}, seedTree: lnBasic},
		{name: "an empty destination", args: []string{"a", ""}, seedTree: lnBasic},
		{name: "a target with trailing slashes", args: []string{"s/f///", "d"}, seedTree: lnSrcSlash},
		{name: "a symbolic link needs no target", args: []string{"-sv", "nosuch", "new"}, seedTree: lnBasic},

		// ---- the target-directory check --------------------------------------
		{name: "three operands with a missing last", args: []string{"a", "b", "nosuch"}, seedTree: lnNotDir},
		{name: "three operands with a file last", args: []string{"a", "b", "n1/f"}, seedTree: lnNotDir},
		{name: "three operands with a trailing slash last", args: []string{"a", "b", "nosuchdir/"}, seedTree: lnNotDir},
		{name: "-t on a file inside a directory", args: []string{"-t", "n1/f", "a"}, seedTree: lnNotDir},
		{name: "two operands with a trailing slash last", args: []string{"a", "nosuchdir/"}, seedTree: lnNotDir},
		{name: "two operands under a plain file", args: []string{"a", "b/x"}, seedTree: lnNotDir},

		// ---- operand counts ---------------------------------------------------
		{name: "no operands"},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "a lone dash is an operand", args: []string{"-"}, seedTree: lnOne},
		{name: "dashdash then an option-looking target", args: []string{"--", "-s", "b"}, seedTree: lnOne},
		{name: "dashdash then a real target", args: []string{"--", "a", "new"}, seedTree: lnBasic},
		{name: "one symbolic operand after dashdash", args: []string{"-s", "--", "-x"}, seedTree: lnOne},

		// ---- -v ----------------------------------------------------------------
		{name: "-v on a hard link", args: []string{"-v", "a", "new"}, seedTree: lnBasic},
		{name: "-v on a symbolic link", args: []string{"-sv", "a", "new"}, seedTree: lnBasic},
		{name: "-v with a backup", args: []string{"-vb", "a", "b"}, seedTree: lnBackupSimple},
		{name: "-v with a symbolic backup", args: []string{"-vbs", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--verbose long", args: []string{"--verbose", "a", "new"}, seedTree: lnBasic},
		{name: "-v is silent on a failure", args: []string{"-v", "a", "b"}, seedTree: lnBasic},

		// ---- quoting -------------------------------------------------------------
		{name: "a name with a space", args: []string{"-v", "sp ace", "out"}, seedTree: lnQuoting},
		{name: "a name with an apostrophe", args: []string{"-v", "ap'os", "out"}, seedTree: lnQuoting},
		{name: "a name that is not valid UTF-8", args: []string{"-v", "bad\xff", "out"}, seedTree: lnQuoting},
		{name: "a missing name that is not valid UTF-8", args: []string{"nosuch\xff", "out"}, seedTree: lnQuoting},
		{name: "a destination that is not valid UTF-8", args: []string{"-v", "a", "o\xfft"}, seedTree: lnQuoting},
		{name: "a destination with a newline", args: []string{"-v", "a", "x\ny"}, seedTree: lnQuoting},
		{name: "a leading tilde", args: []string{"-v", "a", "~tilde"}, seedTree: lnQuoting},
		{name: "a leading hash", args: []string{"-v", "a", "#hash"}, seedTree: lnQuoting},
		{name: "an extra operand with a newline", args: []string{"-T", "a", "b", "c\nd"}, seedTree: lnQuoting},

		// ---- the option scan --------------------------------------------------------
		{name: "an invalid short option", args: []string{"-x", "a", "b"}},
		{name: "an invalid short option in a cluster", args: []string{"-sx", "a", "b"}},
		{name: "an unrecognized long option", args: []string{"--foo"}},
		{name: "an unrecognized long option with a value", args: []string{"--foo=bar", "a", "b"}},
		{name: "the empty long option", args: []string{"--=x"}},
		{name: "--s is ambiguous", args: []string{"--s", "a", "b"}},
		{name: "--n is ambiguous", args: []string{"--n", "a", "b"}},
		{name: "--no is ambiguous", args: []string{"--no", "a", "b"}},
		{name: "--v is ambiguous", args: []string{"--v", "a", "b"}},
		{name: "--ve is ambiguous", args: []string{"--ve", "a", "b"}},
		{name: "--ver is ambiguous", args: []string{"--ver", "a", "b"}},
		{name: "--nod is not a prefix", args: []string{"--nod", "a", "b"}},
		{name: "--b is backup", args: []string{"--b", "-v", "a", "b"}, seedTree: lnBackupSimple},
		{name: "--d is directory", args: []string{"--d", "-v", "a", "new"}, seedTree: lnBasic},
		{name: "--f is force", args: []string{"--f", "-v", "a", "b"}, seedTree: lnBasic},
		{name: "--i is interactive", args: []string{"--i", "a", "b"}, seedTree: lnBasic},
		{name: "--l is logical", args: []string{"--l", "-v", "sl", "h"}, seedTree: lnBasic},
		{name: "--p is physical", args: []string{"--p", "-v", "sl", "h"}, seedTree: lnBasic},
		{name: "--r is relative", args: []string{"--r", "-sv", "a", "d/l"}, seedTree: lnNested},
		{name: "--t is target-directory", args: []string{"--t", "d", "-v", "a"}, seedTree: lnDir},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "clusters with a glued value", args: []string{"-vbS.bak", "a", "b"}, seedTree: lnBackupSimple},
		{name: "clusters with a split value", args: []string{"-bvS", ".bak", "a", "b"}, seedTree: lnBackupSimple},
		{name: "an sfn cluster", args: []string{"-sfnv", "a", "dl"}, seedTree: lnSymDir},
		{name: "an srt cluster", args: []string{"-srvt", "d", "a"}, seedTree: lnNested},
		{name: "options are permuted out", args: []string{"a", "new", "-v"}, seedTree: lnBasic},
		{name: "an option between operands", args: []string{"a", "-s", "new"}, seedTree: lnBasic},
		{name: "POSIXLY_CORRECT stops the scan", args: []string{"a", "-s", "new"}, env: []string{"POSIXLY_CORRECT="}, seedTree: lnBasic},
		{name: "POSIXLY_CORRECT set to one", args: []string{"a", "new", "-v"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: lnBasic},
		{name: "an option after the operands", args: []string{"a", "new", "--foo"}, seedTree: lnBasic},

		// ---- the write-failure paths -------------------------------------------------
		{name: "stdout closed with nothing to write", args: []string{"a", "b"}, seedTree: lnBasic, stdout: stdoutClosed},
		{name: "stdout closed with a verbose line", args: []string{"-v", "a", "new"}, seedTree: lnBasic, stdout: stdoutClosed},
		{name: "stdout closed on a symbolic verbose line", args: []string{"-sv", "a", "new"}, seedTree: lnBasic, stdout: stdoutClosed},
		{name: "stdout closed on a fault", args: []string{"-v", "nosuch", "new"}, seedTree: lnBasic, stdout: stdoutClosed},
		{name: "stdout full with a verbose line", args: []string{"-v", "a", "new"}, seedTree: lnBasic, stdout: stdoutFull},
		{name: "stdout full with nothing to write", args: []string{"a", "new"}, seedTree: lnBasic, stdout: stdoutFull},
	}
}

func TestLnParity(t *testing.T) {
	requireParity(t, "ln", lnCases(t))
}

func TestLnHelpVersion(t *testing.T) {
	requireHelp(t, "ln", []string{"--help"}, 0)
	requireHelp(t, "ln", []string{"--hel"}, 0)
	requireHelp(t, "ln", []string{"--help", "a", "b"}, 0)
	requireHelp(t, "ln", []string{"a", "b", "--help"}, 0)
	requireVersion(t, "ln", []string{"--version"}, 0)
	requireVersion(t, "ln", []string{"--vers"}, 0)
	requireVersion(t, "ln", []string{"a", "b", "--version"}, 0)
}
