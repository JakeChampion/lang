package coreutils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mv(1) writes almost nothing, so nearly every case here is about the
// TREE the two implementations leave behind: which names still exist,
// which of them share an inode, and what a symbolic link holds.
// `seedTree` gives each side its own freshly built working directory and
// compares everything under it, so a stray temporary name fails as
// loudly as a file that failed to move.
//
// Three things this corpus is mostly about:
//
//   - GNU tries the rename BEFORE it asks anything about the
//     destination, with RENAME_NOREPLACE so an occupied one comes back
//     as EEXIST. The order shows: `mv a nosuchdir/` reports the
//     RENAME's errno and `mv a b/` a stat's, for the same ENOTDIR.
//   - -i, -f and -n are one setting and the LAST of them wins, while
//     --update is a second, independent one; -n beats --update=all
//     whichever order they are written in.
//   - The same-file refusal is three rules, not one, and a backup
//     switches between them: `mv a h` on a hard-link pair is refused,
//     `mv -b a h` is not, and `mv -b ./a a` is refused again.
//
// EXDEV is reached for real — `crossDev` puts the destination on another
// filesystem — but only under `--no-copy`, because the copy-then-unlink
// fallback GNU runs without it is not implemented: it preserves mode,
// timestamps, ownership and file type, which needs chmod, set_file_times,
// chown and mkfifo. The first two are native-only and the last two are
// not primitives at all. `mv -v a xdev/c` is the smallest input that
// diverges today.

func mvWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func mvMkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
}

func mvSymlink(t *testing.T, dir, target, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

func mvHardlink(t *testing.T, dir, old, name string) {
	t.Helper()
	if err := os.Link(filepath.Join(dir, old), filepath.Join(dir, name)); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

// mvTouch pins a file's timestamps. Every --update case needs them: the
// two sides run seconds apart, so a fixture that took the wall clock
// would have the source newer than the destination on one run and not on
// the other, and the case would be a coin toss rather than a comparison.
func mvTouch(t *testing.T, dir, name string, sec int64, nsec int64) {
	t.Helper()
	when := time.Unix(sec, nsec)
	if err := os.Chtimes(filepath.Join(dir, name), when, when); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

// mvBasic is the workhorse fixture: two plain files, a directory holding
// one of their names, a symbolic link to a file, a dangling one, and a
// symbolic link to a directory. `a` is the usual SOURCE and `b` the usual
// occupied destination.
func mvBasic(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "b", "B\n")
	mvMkdir(t, dir, "d")
	mvWrite(t, dir, "d/b", "D B\n")
	mvSymlink(t, dir, "a", "sl")
	mvSymlink(t, dir, "nowhere", "dangling")
	mvSymlink(t, dir, "d", "dl")
}

// mvOne is one file and nothing else — the fixture for the
// operand-count diagnostics, where anything more would be noise.
func mvOne(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
}

// mvDirs is the directory-onto-directory fixture: an empty one, a
// non-empty one, and a nest deep enough to move into itself.
func mvDirs(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "b", "B\n")
	mvMkdir(t, dir, "empty")
	mvMkdir(t, dir, "full")
	mvWrite(t, dir, "full/x", "X\n")
	mvMkdir(t, dir, "nest/sub")
	mvMkdir(t, dir, "deep/a")
	mvWrite(t, dir, "deep/z", "Z\n")
}

// mvTarget is the several-sources fixture: three files and a directory to
// put them in, one of whose names is already taken.
func mvTarget(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "b", "B\n")
	mvWrite(t, dir, "c", "C\n")
	mvMkdir(t, dir, "d")
	mvWrite(t, dir, "d/b", "old b\n")
	mvSymlink(t, dir, "d", "dl")
	mvSymlink(t, dir, "b", "bl")
}

// mvLinks is the same-file fixture: one inode under four names, reached
// directly, through a hard link, through two symbolic links and through
// one that spells its target with `..`.
func mvLinks(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvHardlink(t, dir, "a", "h")
	mvSymlink(t, dir, "a", "sl")
	mvSymlink(t, dir, "sl", "sl2")
	mvSymlink(t, dir, "./a", "sl3")
	mvSymlink(t, dir, "nowhere", "dang")
	mvMkdir(t, dir, "d")
	mvHardlink(t, dir, "a", "d/a")
	mvSymlink(t, dir, "../a", "d/up")
	mvSymlink(t, dir, "../h", "d/toh")
}

// mvSlash is the trailing-slash fixture: a directory and a file, so the
// same `x/` reads as a directory on one and as ENOTDIR on the other, plus
// a symbolic-link loop for the one errno that is neither.
func mvSlash(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "b", "B\n")
	mvMkdir(t, dir, "d")
	mvSymlink(t, dir, "l2", "l1")
	mvSymlink(t, dir, "l1", "l2")
}

// mvUpdate is the --update fixture: `a` is a day older than `b`, `n` a
// day newer, and `same` shares `b`'s timestamp to the nanosecond. `near`
// differs from `b` only below the second, which is the resolution the
// comparison actually runs at.
func mvUpdate(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "b", "B\n")
	mvWrite(t, dir, "n", "N\n")
	mvWrite(t, dir, "same", "S\n")
	mvWrite(t, dir, "near", "R\n")
	mvHardlink(t, dir, "a", "h")
	mvMkdir(t, dir, "d")
	mvWrite(t, dir, "d/a", "D A\n")
	mvTouch(t, dir, "b", 1500000000, 200000000)
	mvTouch(t, dir, "a", 1400000000, 0)
	mvTouch(t, dir, "h", 1400000000, 0)
	mvTouch(t, dir, "n", 1600000000, 0)
	mvTouch(t, dir, "same", 1500000000, 200000000)
	mvTouch(t, dir, "near", 1500000000, 500000000)
	mvTouch(t, dir, "d/a", 1600000000, 0)
}

// The backup fixtures. `b~` and the numbered neighbours are what the
// control picks between.
func mvBackupSimple(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "b", "B\n")
}

func mvBackupTaken(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvWrite(t, dir, "b~", "OLD\n")
}

// mvBackupDir is the destination whose backup name is a DIRECTORY: the
// rename that moves the destination aside is EISDIR.
func mvBackupDir(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvMkdir(t, dir, "b~")
}

func mvNumbered(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvWrite(t, dir, "b.~1~", "one\n")
}

func mvNumberedNine(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvWrite(t, dir, "b.~9~", "nine\n")
}

func mvNumberedGap(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvWrite(t, dir, "b.~3~", "three\n")
	mvWrite(t, dir, "b.~10~", "ten\n")
}

func mvNumberedJunk(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvWrite(t, dir, "b.~09~", "leading zero\n")
	mvWrite(t, dir, "b.~x~", "not a number\n")
	mvWrite(t, dir, "b.~-1~", "negative\n")
	mvWrite(t, dir, "b.~0~", "zero\n")
}

func mvNumberedDir(t *testing.T, dir string) {
	t.Helper()
	mvBackupSimple(t, dir)
	mvMkdir(t, dir, "b.~1~")
}

// mvBackupDirs is `-b` over a DIRECTORY destination, where the backup is
// the whole directory moved aside rather than a file copied.
func mvBackupDirs(t *testing.T, dir string) {
	t.Helper()
	mvMkdir(t, dir, "s")
	mvWrite(t, dir, "s/f", "S F\n")
	mvMkdir(t, dir, "t")
	mvWrite(t, dir, "t/g", "T G\n")
}

// mvQuoting is the names whose diagnostics differ between quote, quotef
// and quoteaf: a space, an apostrophe (which makes gnulib reach for
// double quotes), a leading `~`, a newline, and one that is not valid
// UTF-8.
func mvQuoting(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvWrite(t, dir, "sp ace", "S\n")
	mvWrite(t, dir, "ap'os", "Q\n")
	mvWrite(t, dir, "bad\xff", "N\n")
	mvMkdir(t, dir, "d x")
}

// mvPrompt is the `-i` fixture with several occupied destinations, so one
// decline among several answers still costs the exit status while the
// accepted ones move.
func mvPrompt(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a1", "A1\n")
	mvWrite(t, dir, "a2", "A2\n")
	mvMkdir(t, dir, "dd")
	mvWrite(t, dir, "dd/a1", "old 1\n")
	mvWrite(t, dir, "dd/a2", "old 2\n")
}

// mvHere is the working directory's own file, for the cross-device cases:
// everything else they need is on the other filesystem.
func mvHere(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "a", "A\n")
	mvMkdir(t, dir, "dd")
	mvWrite(t, dir, "dd/f", "F\n")
}

// mvThere seeds the OTHER filesystem, reached as `xdev/…`.
func mvThere(t *testing.T, dir string) {
	t.Helper()
	mvWrite(t, dir, "b", "B\n")
	mvMkdir(t, dir, "sub")
}

func init() {
	registerCorpus("mv", mvCases)
}

// mvCases is mv(1)'s corpus.
func mvCases(t *testing.T) []invocation {
	return []invocation{
		// ---- the three forms ---------------------------------------------
		{name: "a plain rename", args: []string{"-v", "a", "c"}, seedTree: mvBasic},
		{name: "over an existing file", args: []string{"-v", "a", "b"}, seedTree: mvBasic},
		{name: "into a directory", args: []string{"-v", "a", "d"}, seedTree: mvBasic},
		{name: "into a directory whose name is taken", args: []string{"-v", "b", "d"}, seedTree: mvBasic},
		{name: "several into a directory", args: []string{"-v", "a", "b", "c", "d"}, seedTree: mvTarget},
		{name: "a failing operand does not stop the rest", args: []string{"-v", "nosuch", "a", "d"}, seedTree: mvTarget},
		{name: "a failing operand in the middle", args: []string{"-v", "a", "nosuch/x", "d"}, seedTree: mvTarget},
		{name: "a directory into a directory", args: []string{"-v", "nest", "full"}, seedTree: mvDirs},
		{name: "the directory join uses one slash", args: []string{"-v", "a", "d///"}, seedTree: mvBasic},
		{name: "a source keeps its own trailing slashes", args: []string{"-v", "d/", "empty/"}, seedTree: mvDirs},
		{name: "into a symlinked directory", args: []string{"-v", "a", "dl"}, seedTree: mvBasic},
		{name: "into dot", args: []string{"-v", "a", "."}, seedTree: mvBasic},
		{name: "into dotdot", args: []string{"-v", "a", ".."}, seedTree: mvBasic},
		{name: "dot as a source", args: []string{"-v", ".", "x"}, seedTree: mvBasic},
		{name: "dotdot as a source", args: []string{"-v", "..", "x"}, seedTree: mvBasic},
		{name: "a symbolic link moves as itself", args: []string{"-v", "sl", "c"}, seedTree: mvBasic},
		{name: "a dangling symbolic link moves too", args: []string{"-v", "dangling", "c"}, seedTree: mvBasic},
		{name: "over a symbolic link", args: []string{"-v", "a", "sl"}, seedTree: mvBasic},

		// ---- the rename-first order ---------------------------------------
		{name: "a missing source", args: []string{"nosuch", "x"}, seedTree: mvBasic},
		{name: "a missing parent in the destination", args: []string{"a", "nosuch/x"}, seedTree: mvBasic},
		{name: "a plain file as a destination parent", args: []string{"a", "b/x"}, seedTree: mvBasic},
		{name: "a trailing slash on an existing file", args: []string{"a", "b/"}, seedTree: mvSlash},
		{name: "two trailing slashes on an existing file", args: []string{"a", "b//"}, seedTree: mvSlash},
		{name: "a trailing slash on a missing name", args: []string{"a", "nosuchdir/"}, seedTree: mvSlash},
		{name: "a trailing slash on a directory", args: []string{"-v", "a", "d/"}, seedTree: mvSlash},
		{name: "a trailing slash on the source", args: []string{"-v", "a//", "c"}, seedTree: mvSlash},
		{name: "a symlink loop in the destination", args: []string{"a", "l1/x"}, seedTree: mvSlash},
		{name: "a symlink loop as the source", args: []string{"a", "l1"}, seedTree: mvSlash},
		{name: "a directory over a plain file", args: []string{"d", "b"}, seedTree: mvSlash},
		{name: "a directory into itself", args: []string{"nest", "nest/sub"}, seedTree: mvDirs},
		{name: "a directory into a missing name under itself", args: []string{"nest", "nest/sub/x"}, seedTree: mvDirs},
		{name: "a directory onto its own trailing-slash self", args: []string{"nest", "nest/"}, seedTree: mvDirs},
		{name: "a file onto a directory reached by joining", args: []string{"a", "deep"}, seedTree: mvDirs},
		{name: "a directory onto a name inside a directory", args: []string{"nest", "deep"}, seedTree: mvDirs},

		// ---- the empty operand --------------------------------------------
		{name: "an empty destination", args: []string{"a", ""}, seedTree: mvBasic},
		{name: "an empty destination under -T", args: []string{"-T", "a", ""}, seedTree: mvBasic},
		{name: "an empty destination for a directory", args: []string{"d", ""}, seedTree: mvBasic},
		{name: "an empty destination under -n", args: []string{"-n", "a", ""}, seedTree: mvBasic},
		{name: "an empty destination under --update=none", args: []string{"--update=none", "-v", "a", ""}, seedTree: mvBasic},
		{name: "an empty destination under -i", args: []string{"-i", "a", ""}, stdin: "y\n", seedTree: mvBasic},
		{name: "an empty source", args: []string{"", "b"}, seedTree: mvBasic},
		{name: "two empty operands", args: []string{"", ""}, seedTree: mvBasic},
		{name: "an empty target directory", args: []string{"-t", "", "a"}, seedTree: mvBasic},
		{name: "an empty operand among three", args: []string{"a", "", "b"}, seedTree: mvBasic},

		// ---- -f / -i / -n, and their order --------------------------------
		{name: "-f", args: []string{"-fv", "a", "b"}, seedTree: mvBasic},
		{name: "-i declining at end of input", args: []string{"-i", "a", "b"}, seedTree: mvBasic},
		{name: "-i accepting", args: []string{"-iv", "a", "b"}, stdin: "y", seedTree: mvBasic},
		{name: "-i accepting with a newline", args: []string{"-iv", "a", "b"}, stdin: "Y\n", seedTree: mvBasic},
		{name: "-i accepting a whole word", args: []string{"-iv", "a", "b"}, stdin: "yes\n", seedTree: mvBasic},
		{name: "-i declining a whole word", args: []string{"-iv", "a", "b"}, stdin: "no\n", seedTree: mvBasic},
		{name: "-i on an unclear answer", args: []string{"-iv", "a", "b"}, stdin: "maybe\n", seedTree: mvBasic},
		{name: "-i on an empty line", args: []string{"-iv", "a", "b"}, stdin: "\n", seedTree: mvBasic},
		{name: "-i where nothing is in the way", args: []string{"-iv", "a", "fresh"}, seedTree: mvBasic},
		{name: "-i once per operand", args: []string{"-iv", "a1", "a2", "dd"}, stdin: "n\ny\n", seedTree: mvPrompt},
		{name: "-i onto a directory, accepted", args: []string{"-iv", "-T", "a", "d"}, stdin: "y\n", seedTree: mvBasic},
		{name: "-i onto a directory, declined", args: []string{"-iv", "-T", "a", "d"}, stdin: "n\n", seedTree: mvBasic},
		{name: "-n", args: []string{"-nv", "a", "b"}, seedTree: mvBasic},
		{name: "-n where nothing is in the way", args: []string{"-nv", "a", "fresh"}, seedTree: mvBasic},
		{name: "-n names the joined destination", args: []string{"-nv", "b", "d"}, seedTree: mvBasic},
		{name: "-n on a name it cannot stat", args: []string{"-n", "a", "b/x"}, seedTree: mvSlash},
		{name: "-n on a trailing slash", args: []string{"-n", "a", "b/"}, seedTree: mvSlash},
		{name: "-n on a symlink loop", args: []string{"-n", "a", "l1/x"}, seedTree: mvSlash},
		{name: "-if is force", args: []string{"-if", "-v", "a", "b"}, seedTree: mvBasic},
		{name: "-fi prompts", args: []string{"-fi", "a", "b"}, seedTree: mvBasic},
		{name: "-in is no-clobber", args: []string{"-in", "a", "b"}, seedTree: mvBasic},
		{name: "-ni prompts", args: []string{"-ni", "a", "b"}, seedTree: mvBasic},
		{name: "-fn is no-clobber", args: []string{"-fn", "a", "b"}, seedTree: mvBasic},
		{name: "-nf is force", args: []string{"-nf", "-v", "a", "b"}, seedTree: mvBasic},
		{name: "-nfi prompts", args: []string{"-nfi", "a", "b"}, seedTree: mvBasic},
		{name: "the last of four wins", args: []string{"-i", "-n", "-f", "-v", "a", "b"}, seedTree: mvBasic},
		{name: "the last of four wins the other way", args: []string{"-f", "-i", "-n", "-v", "a", "b"}, seedTree: mvBasic},
		{name: "long spellings take part", args: []string{"--force", "--no-clobber", "a", "b"}, seedTree: mvBasic},
		{name: "long spellings the other way", args: []string{"--no-clobber", "--interactive", "a", "b"}, stdin: "y\n", seedTree: mvBasic},

		// ---- --update -------------------------------------------------------
		{name: "-u skips an older source", args: []string{"-uv", "a", "b"}, seedTree: mvUpdate},
		{name: "-u takes a newer source", args: []string{"-uv", "n", "b"}, seedTree: mvUpdate},
		{name: "-u on an equal timestamp", args: []string{"-uv", "same", "b"}, seedTree: mvUpdate},
		{name: "-u counts nanoseconds", args: []string{"-uv", "near", "b"}, seedTree: mvUpdate},
		{name: "-u where nothing is in the way", args: []string{"-uv", "a", "fresh"}, seedTree: mvUpdate},
		{name: "-u into a directory", args: []string{"-uv", "a", "d"}, seedTree: mvUpdate},
		{name: "--update is -u", args: []string{"--update", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update takes no operand as its argument", args: []string{"--update", "n", "b"}, seedTree: mvUpdate},
		{name: "--update=older", args: []string{"--update=older", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=all replaces anyway", args: []string{"--update=all", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=none skips silently", args: []string{"--update=none", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=none where nothing is in the way", args: []string{"--update=none", "-v", "a", "fresh"}, seedTree: mvUpdate},
		{name: "--update=none names nothing on a directory join", args: []string{"--update=none", "-v", "a", "d"}, seedTree: mvUpdate},
		{name: "--update=bogus", args: []string{"--update=bogus", "a", "b"}, seedTree: mvUpdate},
		{name: "--update= is ambiguous", args: []string{"--update=", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=o is older", args: []string{"--update=o", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=n is none", args: []string{"--update=n", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=a is all", args: []string{"--update=a", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update is case sensitive", args: []string{"--update=ALL", "a", "b"}, seedTree: mvUpdate},
		{name: "-u beaten by a later --update=all", args: []string{"-u", "--update=all", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "--update=all beaten by a later -u", args: []string{"--update=all", "-u", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-n beats --update=all", args: []string{"--update=all", "-n", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-n beats --update=all whichever way round", args: []string{"-n", "--update=all", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-n beats --update=none", args: []string{"--update=none", "-n", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-u before the prompt", args: []string{"-u", "-i", "-v", "a", "b"}, stdin: "y\n", seedTree: mvUpdate},
		{name: "-i before -u makes no difference", args: []string{"-i", "-u", "-v", "a", "b"}, stdin: "y\n", seedTree: mvUpdate},
		{name: "-u does not save a same-file operand", args: []string{"-u", "a", "h"}, seedTree: mvUpdate},
		{name: "-u with -f", args: []string{"-u", "-f", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-u with a backup", args: []string{"-u", "-b", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-u with a backup that is taken", args: []string{"-u", "-b", "-v", "n", "b"}, seedTree: mvUpdate},
		{name: "-uv on a symbolic link's own timestamp", args: []string{"-uv", "a", "b"}, env: []string{"TZ=UTC"}, seedTree: mvUpdate},

		// ---- the same-file rule ---------------------------------------------
		{name: "a file onto itself", args: []string{"a", "a"}, seedTree: mvLinks},
		{name: "the same entry spelled differently", args: []string{"./a", "a"}, seedTree: mvLinks},
		{name: "the same entry through dotdot", args: []string{"d/../a", "a"}, seedTree: mvLinks},
		{name: "two names for one inode", args: []string{"a", "h"}, seedTree: mvLinks},
		{name: "two names for one inode the other way", args: []string{"h", "a"}, seedTree: mvLinks},
		{name: "two names for one inode in different directories", args: []string{"d/a", "a"}, seedTree: mvLinks},
		{name: "-f does not excuse the same file", args: []string{"-f", "a", "h"}, seedTree: mvLinks},
		{name: "-i does not excuse the same file", args: []string{"-i", "a", "h"}, stdin: "y\n", seedTree: mvLinks},
		{name: "a symbolic link onto its target", args: []string{"sl", "a"}, seedTree: mvLinks},
		{name: "a symbolic link chain onto its target", args: []string{"sl2", "a"}, seedTree: mvLinks},
		{name: "a symbolic link spelling a dot component", args: []string{"sl3", "a"}, seedTree: mvLinks},
		{name: "a symbolic link spelling dotdot", args: []string{"d/up", "a"}, seedTree: mvLinks},
		{name: "a symbolic link onto another name for its target", args: []string{"-v", "sl", "h"}, seedTree: mvLinks},
		{name: "a symbolic link onto the name it resolves to", args: []string{"d/toh", "h"}, seedTree: mvLinks},
		{name: "a symbolic link onto a sibling of the name it resolves to", args: []string{"-v", "d/toh", "a"}, seedTree: mvLinks},
		{name: "a target onto the symbolic link to it", args: []string{"-v", "a", "sl"}, seedTree: mvLinks},
		{name: "a symbolic link onto a symbolic link to it", args: []string{"-v", "sl", "sl2"}, seedTree: mvLinks},
		{name: "a symbolic link chain onto its first link", args: []string{"-v", "sl2", "sl"}, seedTree: mvLinks},
		{name: "a dangling symbolic link onto itself", args: []string{"dang", "dang"}, seedTree: mvLinks},
		{name: "a backup excuses one inode under two names", args: []string{"-bv", "a", "h"}, seedTree: mvLinks},
		{name: "a backup excuses a link onto its target", args: []string{"-bv", "sl", "a"}, seedTree: mvLinks},
		{name: "a backup does not excuse one entry", args: []string{"-b", "a", "a"}, seedTree: mvLinks},
		{name: "a backup does not excuse one entry spelled twice", args: []string{"-b", "./a", "a"}, seedTree: mvLinks},
		{name: "a backup does not excuse one entry through dotdot", args: []string{"-b", "d/../a", "a"}, seedTree: mvLinks},
		{name: "a backup does not excuse one link named twice", args: []string{"-b", "sl", "sl"}, seedTree: mvLinks},
		{name: "-n beats the same-file rule", args: []string{"-n", "a", "a"}, seedTree: mvLinks},
		{name: "the same file joined into a directory", args: []string{"a", "d"}, seedTree: mvLinks},

		// ---- directory against non-directory --------------------------------
		{name: "-T a file onto a directory", args: []string{"-T", "a", "d"}, seedTree: mvBasic},
		{name: "-T a directory onto a file", args: []string{"-T", "d", "b"}, seedTree: mvBasic},
		{name: "-T a directory onto an empty directory", args: []string{"-Tv", "nest", "empty"}, seedTree: mvDirs},
		{name: "-T a directory onto a non-empty directory", args: []string{"-T", "empty", "full"}, seedTree: mvDirs},
		{name: "-T a file onto a symlinked directory", args: []string{"-Tv", "a", "dl"}, seedTree: mvBasic},
		{name: "-T a file onto a directory with a trailing slash", args: []string{"-T", "a", "d/"}, seedTree: mvBasic},
		{name: "a file onto a directory already there", args: []string{"a", "deep"}, seedTree: mvDirs},

		// ---- -t / -T ---------------------------------------------------------
		{name: "-t", args: []string{"-vt", "d", "a", "c"}, seedTree: mvTarget},
		{name: "-t collapses trailing slashes", args: []string{"-vt", "d///", "a"}, seedTree: mvTarget},
		{name: "-t twice", args: []string{"-t", "d", "-t", "d", "a"}, seedTree: mvTarget},
		{name: "-t on a missing directory", args: []string{"-t", "nosuch", "a"}, seedTree: mvTarget},
		{name: "-t on a plain file", args: []string{"-t", "b", "a"}, seedTree: mvTarget},
		{name: "-t on a symlink to a file", args: []string{"-t", "bl", "a"}, seedTree: mvTarget},
		{name: "-t on a symlink to a directory", args: []string{"-vt", "dl", "a"}, seedTree: mvTarget},
		{name: "-t with no sources", args: []string{"-t", "d"}, seedTree: mvTarget},
		{name: "-t with no argument", args: []string{"-t"}, seedTree: mvTarget},
		{name: "--target-directory with no argument", args: []string{"--target-directory"}, seedTree: mvTarget},
		{name: "-t glued to a cluster", args: []string{"-vtd", "a"}, seedTree: mvTarget},
		{name: "-t split from its cluster", args: []string{"-vt", "d", "a"}, seedTree: mvTarget},
		{name: "-t and -T", args: []string{"-T", "-t", "d", "a"}, seedTree: mvTarget},
		{name: "-t and -T the other way round", args: []string{"-t", "d", "-T", "a"}, seedTree: mvTarget},
		{name: "-t and -T with no sources", args: []string{"-t", "d", "-T"}, seedTree: mvTarget},
		{name: "-t and -T beat a bad target", args: []string{"-T", "-t", "nosuch", "a"}, seedTree: mvTarget},
		{name: "-t and -T beat the backup conflict", args: []string{"-b", "-n", "-T", "-t", "d", "a"}, seedTree: mvTarget},
		{name: "-T", args: []string{"-Tv", "a", "new"}, seedTree: mvBasic},
		{name: "-T with one operand", args: []string{"-T", "a"}, seedTree: mvOne},
		{name: "-T with three operands", args: []string{"-T", "a", "b", "c"}, seedTree: mvTarget},
		{name: "-T twice", args: []string{"-T", "-T", "-v", "a", "new"}, seedTree: mvBasic},
		{name: "-T does not join into a directory", args: []string{"-T", "-v", "b", "empty"}, seedTree: mvDirs},

		// ---- the target-directory check ---------------------------------------
		{name: "three operands with a missing last", args: []string{"a", "b", "nosuch"}, seedTree: mvTarget},
		{name: "three operands with a file last", args: []string{"a", "b", "c"}, seedTree: mvTarget},
		{name: "three operands with a symlinked directory last", args: []string{"-v", "a", "c", "dl"}, seedTree: mvTarget},
		{name: "three operands with a trailing slash last", args: []string{"a", "b", "nosuchdir/"}, seedTree: mvTarget},
		{name: "three operands with a file inside a directory last", args: []string{"a", "b", "d/b"}, seedTree: mvTarget},

		// ---- --strip-trailing-slashes -------------------------------------------
		{name: "--strip-trailing-slashes on a directory", args: []string{"--strip-trailing-slashes", "-v", "d///", "empty"}, seedTree: mvDirs},
		{name: "--strip-trailing-slashes on a file", args: []string{"--strip-trailing-slashes", "-v", "a//", "c"}, seedTree: mvSlash},
		{name: "--strip-trailing-slashes changes the joined name", args: []string{"--strip-trailing-slashes", "-v", "a//", "d"}, seedTree: mvSlash},
		{name: "--strip-trailing-slashes leaves the destination alone", args: []string{"--strip-trailing-slashes", "a", "b/"}, seedTree: mvSlash},
		{name: "--strip-trailing-slashes under -t", args: []string{"--strip-trailing-slashes", "-vt", "d", "a//"}, seedTree: mvSlash},
		{name: "--strip-trailing-slashes on a root operand", args: []string{"--strip-trailing-slashes", "//", "x"}, seedTree: mvSlash},
		{name: "without it the slashes are kept", args: []string{"-v", "d///", "empty"}, seedTree: mvDirs},

		// ---- backups -------------------------------------------------------------
		{name: "-b", args: []string{"-bv", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-b over an existing backup", args: []string{"-bv", "a", "b"}, seedTree: mvBackupTaken},
		{name: "-b where nothing is in the way", args: []string{"-bv", "a", "fresh"}, seedTree: mvBackupSimple},
		{name: "-b onto a dangling symlink", args: []string{"-bv", "a", "dangling"}, seedTree: mvBasic},
		{name: "-b when the backup name is a directory", args: []string{"-bv", "a", "b"}, seedTree: mvBackupDir},
		{name: "-b inside a target directory", args: []string{"-bv", "b", "d"}, seedTree: mvTarget},
		{name: "-b moves a whole directory aside", args: []string{"-bv", "-T", "s", "t"}, seedTree: mvBackupDirs},
		{name: "-b and -f together", args: []string{"-bfv", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-f and -b together", args: []string{"-fbv", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-b and -i, accepted", args: []string{"-biv", "a", "b"}, stdin: "y\n", seedTree: mvBackupSimple},
		{name: "-b and -i, declined", args: []string{"-biv", "a", "b"}, stdin: "n\n", seedTree: mvBackupSimple},
		{name: "-b and -n are mutually exclusive", args: []string{"-bn", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-n and -b are mutually exclusive either way", args: []string{"-nb", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-b and -n with no operands is the count first", args: []string{"-bn"}, seedTree: mvBackupSimple},
		{name: "-b and -n with one operand is the count first", args: []string{"-bn", "a"}, seedTree: mvBackupSimple},
		{name: "-f after -n clears the conflict", args: []string{"-n", "-f", "-bv", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-n after -f keeps it", args: []string{"-f", "-n", "-b", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-i after -n clears the conflict", args: []string{"-n", "-i", "-bv", "a", "b"}, stdin: "y\n", seedTree: mvBackupSimple},
		{name: "--backup=none does not clear the conflict", args: []string{"--backup=none", "-n", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-S turns the conflict on", args: []string{"-S", ".x", "-n", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup takes no operand as its argument", args: []string{"--backup", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup= is existing", args: []string{"--backup=", "-v", "a", "b"}, seedTree: mvNumbered},
		{name: "--backup=numbered", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=numbered past nine", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: mvNumberedNine},
		{name: "--backup=numbered counts numerically", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: mvNumberedGap},
		{name: "--backup=numbered ignores malformed neighbours", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: mvNumberedJunk},
		{name: "--backup=numbered counts a directory", args: []string{"--backup=numbered", "-v", "a", "b"}, seedTree: mvNumberedDir},
		{name: "--backup=existing with none", args: []string{"--backup=existing", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=existing with one", args: []string{"--backup=existing", "-v", "a", "b"}, seedTree: mvNumbered},
		{name: "--backup=simple beside a numbered one", args: []string{"--backup=simple", "-v", "a", "b"}, seedTree: mvNumbered},
		{name: "--backup=none", args: []string{"--backup=none", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=off", args: []string{"--backup=off", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=never is simple", args: []string{"--backup=never", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=nil is existing", args: []string{"--backup=nil", "-v", "a", "b"}, seedTree: mvNumbered},
		{name: "--backup=t is numbered", args: []string{"--backup=t", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=none after -b", args: []string{"-b", "--backup=none", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-b after --backup=none", args: []string{"--backup=none", "-b", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup unique prefixes", args: []string{"--backup=nu", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=ne is simple", args: []string{"--backup=ne", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=ni is existing", args: []string{"--backup=ni", "-v", "a", "b"}, seedTree: mvNumbered},
		{name: "--backup=o is none", args: []string{"--backup=o", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=n is ambiguous", args: []string{"--backup=n", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=bogus", args: []string{"--backup=bogus", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup is case sensitive", args: []string{"--backup=NUMBERED", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup=T is not t", args: []string{"--backup=T", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--backup on a blank argument", args: []string{"--backup= ", "a", "b"}, seedTree: mvBackupSimple},

		// ---- -S / --suffix ---------------------------------------------------------
		{name: "-S turns backups on", args: []string{"-S", ".bak", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-S glued", args: []string{"-S.bak", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--suffix glued", args: []string{"--suffix=.bak", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--suffix split", args: []string{"--suffix", ".bak", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "the last -S wins", args: []string{"-S", ".x", "-S", ".y", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "an empty -S is ignored", args: []string{"-b", "-S", "", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "a -S with a slash inside is ignored", args: []string{"-b", "-S", "x/y", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "a leading-slash -S is ignored", args: []string{"-b", "-S", "/x", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "a lone slash -S is ignored", args: []string{"-b", "-S", "/", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "a trailing-slash -S is kept", args: []string{"-b", "-S", "x/", "a", "b"}, seedTree: mvBackupSimple},
		{name: "-S with --backup=none", args: []string{"--backup=none", "-S", ".bak", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--suffix with no argument", args: []string{"--suffix"}, seedTree: mvBackupSimple},
		{name: "-S with no argument", args: []string{"-S"}, seedTree: mvBackupSimple},
		{name: "-S eats the next operand", args: []string{"-S", "a", "b"}, seedTree: mvBackupSimple},

		// ---- the environment ----------------------------------------------------------
		{name: "VERSION_CONTROL", args: []string{"-bv", "a", "b"}, env: []string{"VERSION_CONTROL=numbered"}, seedTree: mvBackupSimple},
		{name: "--backup overrides VERSION_CONTROL", args: []string{"--backup=simple", "-v", "a", "b"}, env: []string{"VERSION_CONTROL=numbered"}, seedTree: mvBackupSimple},
		{name: "an empty VERSION_CONTROL is existing", args: []string{"-bv", "a", "b"}, env: []string{"VERSION_CONTROL="}, seedTree: mvBackupSimple},
		{name: "an invalid VERSION_CONTROL names the variable", args: []string{"-b", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: mvBackupSimple},
		{name: "VERSION_CONTROL is read only with backups on", args: []string{"-v", "a", "fresh"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: mvBackupSimple},
		{name: "VERSION_CONTROL is read for a bare -S", args: []string{"-S", ".z", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: mvBackupSimple},
		{name: "the backup conflict beats VERSION_CONTROL", args: []string{"-b", "-n", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: mvBackupSimple},
		{name: "the operand count beats VERSION_CONTROL", args: []string{"-b", "-n"}, env: []string{"VERSION_CONTROL=bogus"}, seedTree: mvBackupSimple},
		{name: "SIMPLE_BACKUP_SUFFIX", args: []string{"-bv", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=.S"}, seedTree: mvBackupSimple},
		{name: "-S overrides SIMPLE_BACKUP_SUFFIX", args: []string{"-b", "-S", ".T", "-v", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=.S"}, seedTree: mvBackupSimple},
		{name: "an empty SIMPLE_BACKUP_SUFFIX is the default", args: []string{"-bv", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX="}, seedTree: mvBackupSimple},
		{name: "an unusable SIMPLE_BACKUP_SUFFIX is the default", args: []string{"-bv", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=x/y"}, seedTree: mvBackupSimple},
		{name: "POSIXLY_CORRECT stops the scan", args: []string{"a", "c", "-v"}, env: []string{"POSIXLY_CORRECT="}, seedTree: mvBasic},
		{name: "POSIXLY_CORRECT set to one", args: []string{"a", "c", "-v"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: mvBasic},

		// ---- -Z, --debug, --no-copy ------------------------------------------------------
		{name: "-Z on a rename", args: []string{"-Zv", "a", "c"}, seedTree: mvBasic},
		{name: "-Z twice", args: []string{"-ZZ", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--context is -Z", args: []string{"--context", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--context takes no argument", args: []string{"--context=x", "a", "c"}, seedTree: mvBasic},
		{name: "-Z on a failure", args: []string{"-Z", "nosuch", "c"}, seedTree: mvBasic},
		{name: "--debug implies -v", args: []string{"--debug", "a", "c"}, seedTree: mvBasic},
		{name: "--debug with -v", args: []string{"--debug", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--debug over an existing file", args: []string{"--debug", "a", "b"}, seedTree: mvBasic},
		{name: "--debug with a backup", args: []string{"--debug", "-b", "a", "b"}, seedTree: mvBasic},
		{name: "--debug on a failure", args: []string{"--debug", "nosuch", "c"}, seedTree: mvBasic},
		{name: "--debug takes no argument", args: []string{"--debug=x", "a", "c"}, seedTree: mvBasic},
		{name: "--no-copy on a rename that works", args: []string{"--no-copy", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--no-copy takes no argument", args: []string{"--no-copy=x", "a", "c"}, seedTree: mvBasic},

		// ---- across filesystems -----------------------------------------------------------
		//
		// Only --no-copy, which is where GNU reports the EXDEV rather than
		// falling back to a copy. The fallback itself is not implemented —
		// see the note at the top of this file.
		{name: "--no-copy across filesystems", args: []string{"--no-copy", "-v", "a", "xdev/c"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--no-copy across filesystems onto an existing file", args: []string{"--no-copy", "-v", "a", "xdev/b"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--no-copy across filesystems into a directory", args: []string{"--no-copy", "-v", "a", "xdev/sub"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--no-copy across filesystems with a directory source", args: []string{"--no-copy", "-v", "dd", "xdev/dd"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--no-copy across filesystems the other way", args: []string{"--no-copy", "-v", "xdev/b", "here"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--no-copy across filesystems with a backup", args: []string{"--no-copy", "-bv", "a", "xdev/b"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--no-copy across filesystems under -T", args: []string{"--no-copy", "-Tv", "dd", "xdev/nope"}, seedTree: mvHere, crossDev: mvThere},
		{name: "-n stops before the filesystem boundary", args: []string{"-nv", "a", "xdev/b"}, seedTree: mvHere, crossDev: mvThere},
		{name: "--update=none stops before the filesystem boundary", args: []string{"--update=none", "-v", "a", "xdev/b"}, seedTree: mvHere, crossDev: mvThere},
		{name: "the same-file rule spans filesystems", args: []string{"xdev/b", "xdev/b"}, seedTree: mvHere, crossDev: mvThere},

		// ---- operand counts -----------------------------------------------------------------
		{name: "no operands"},
		{name: "one operand", args: []string{"a"}, seedTree: mvOne},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "a lone dash is an operand", args: []string{"-"}, seedTree: mvOne},
		{name: "a lone dash as a source", args: []string{"-", "c"}, seedTree: mvOne},
		{name: "dashdash then an option-looking source", args: []string{"--", "-v", "c"}, seedTree: mvOne},
		{name: "dashdash then a real pair", args: []string{"--", "a", "c"}, seedTree: mvOne},
		{name: "dashdash with one operand after it", args: []string{"--", "a"}, seedTree: mvOne},

		// ---- -v ------------------------------------------------------------------------------
		{name: "--verbose long", args: []string{"--verbose", "a", "c"}, seedTree: mvBasic},
		{name: "-v twice", args: []string{"-vv", "a", "c"}, seedTree: mvBasic},
		{name: "-v is silent on a failure", args: []string{"-v", "nosuch", "c"}, seedTree: mvBasic},
		{name: "-v is silent on a declined prompt", args: []string{"-vi", "a", "b"}, stdin: "n\n", seedTree: mvBasic},
		{name: "-v is silent on an update skip", args: []string{"-vu", "a", "b"}, seedTree: mvUpdate},

		// ---- quoting ---------------------------------------------------------------------------
		{name: "a name with a space", args: []string{"-v", "sp ace", "out"}, seedTree: mvQuoting},
		{name: "a name with an apostrophe", args: []string{"-v", "ap'os", "out"}, seedTree: mvQuoting},
		{name: "a name that is not valid UTF-8", args: []string{"-v", "bad\xff", "out"}, seedTree: mvQuoting},
		{name: "a missing name that is not valid UTF-8", args: []string{"nosuch\xff", "out"}, seedTree: mvQuoting},
		{name: "a destination that is not valid UTF-8", args: []string{"-v", "a", "o\xfft"}, seedTree: mvQuoting},
		{name: "a destination with a newline", args: []string{"-v", "a", "x\ny"}, seedTree: mvQuoting},
		{name: "a leading tilde", args: []string{"-v", "a", "~tilde"}, seedTree: mvQuoting},
		{name: "a leading hash", args: []string{"-v", "a", "#hash"}, seedTree: mvQuoting},
		{name: "a directory name needing quotes", args: []string{"-v", "a", "d x"}, seedTree: mvQuoting},
		{name: "an extra operand with a newline", args: []string{"-T", "a", "sp ace", "c\nd"}, seedTree: mvQuoting},
		{name: "a same-file pair needing quotes", args: []string{"sp ace", "./sp ace"}, seedTree: mvQuoting},

		// ---- the option scan --------------------------------------------------------------------
		{name: "an invalid short option", args: []string{"-x", "a", "b"}},
		{name: "an invalid short option in a cluster", args: []string{"-vx", "a", "b"}},
		{name: "an unrecognized long option", args: []string{"--foo"}},
		{name: "an unrecognized long option with a value", args: []string{"--foo=bar", "a", "b"}},
		{name: "the empty long option", args: []string{"--=x"}},
		{name: "--n is ambiguous", args: []string{"--n", "a", "b"}},
		{name: "--no is ambiguous", args: []string{"--no", "a", "b"}},
		{name: "--no- is ambiguous", args: []string{"--no-", "a", "b"}},
		{name: "--s is ambiguous", args: []string{"--s", "a", "b"}},
		{name: "--v is ambiguous", args: []string{"--v", "a", "b"}},
		{name: "--ve is ambiguous", args: []string{"--ve", "a", "b"}},
		{name: "--ver is ambiguous", args: []string{"--ver", "a", "b"}},
		{name: "--c is context", args: []string{"--c", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--d is debug", args: []string{"--d", "a", "c"}, seedTree: mvBasic},
		{name: "--b is backup", args: []string{"--b", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--f is force", args: []string{"--f", "-v", "a", "b"}, seedTree: mvBasic},
		{name: "--i is interactive", args: []string{"--i", "a", "b"}, stdin: "n\n", seedTree: mvBasic},
		{name: "--no-cl is no-clobber", args: []string{"--no-cl", "a", "b"}, seedTree: mvBasic},
		{name: "--no-co is no-copy", args: []string{"--no-co", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--no-t is no-target-directory", args: []string{"--no-t", "-v", "a", "c"}, seedTree: mvBasic},
		{name: "--st is strip-trailing-slashes", args: []string{"--st", "-v", "a//", "c"}, seedTree: mvSlash},
		{name: "--su is suffix", args: []string{"--su=.bak", "-v", "a", "b"}, seedTree: mvBackupSimple},
		{name: "--ta is target-directory", args: []string{"--ta", "d", "-v", "a"}, seedTree: mvTarget},
		{name: "--up is update", args: []string{"--up", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "clusters with a glued value", args: []string{"-vbS.bak", "a", "b"}, seedTree: mvBackupSimple},
		{name: "clusters with a split value", args: []string{"-bvS", ".bak", "a", "b"}, seedTree: mvBackupSimple},
		{name: "a uvT cluster", args: []string{"-uvT", "a", "b"}, seedTree: mvUpdate},
		{name: "-u glued to more", args: []string{"-uf", "-v", "a", "b"}, seedTree: mvUpdate},
		{name: "-u takes no glued value", args: []string{"-u=older", "a", "b"}, seedTree: mvUpdate},
		{name: "options are permuted out", args: []string{"a", "c", "-v"}, seedTree: mvBasic},
		{name: "an option between operands", args: []string{"a", "-v", "c"}, seedTree: mvBasic},
		{name: "an option after the operands", args: []string{"a", "c", "--foo"}, seedTree: mvBasic},
		{name: "multiple target directories", args: []string{"-t", "d", "-t", "empty", "a"}, seedTree: mvDirs},

		// ---- the write-failure paths -----------------------------------------------------------
		{name: "stdout closed with nothing to write", args: []string{"a", "c"}, seedTree: mvBasic, stdout: stdoutClosed},
		{name: "stdout closed with a verbose line", args: []string{"-v", "a", "c"}, seedTree: mvBasic, stdout: stdoutClosed},
		{name: "stdout closed on a fault", args: []string{"-v", "nosuch", "c"}, seedTree: mvBasic, stdout: stdoutClosed},
		{name: "stdout full with a verbose line", args: []string{"-v", "a", "c"}, seedTree: mvBasic, stdout: stdoutFull},
		{name: "stdout full with nothing to write", args: []string{"a", "c"}, seedTree: mvBasic, stdout: stdoutFull},
		{name: "stdout full on a fault", args: []string{"-v", "nosuch", "c"}, seedTree: mvBasic, stdout: stdoutFull},
	}
}

func TestMvParity(t *testing.T) {
	requireParity(t, "mv", mvCases(t))
}

func TestMvHelpVersion(t *testing.T) {
	requireHelp(t, "mv", []string{"--help"}, 0)
	requireHelp(t, "mv", []string{"--hel"}, 0)
	requireHelp(t, "mv", []string{"--help", "a", "b"}, 0)
	requireHelp(t, "mv", []string{"a", "b", "--help"}, 0)
	requireHelp(t, "mv", []string{"-b", "-n", "--help"}, 0)
	requireVersion(t, "mv", []string{"--version"}, 0)
	requireVersion(t, "mv", []string{"--vers"}, 0)
	requireVersion(t, "mv", []string{"a", "b", "--version"}, 0)
	requireVersion(t, "mv", []string{"-n", "--version", "-b"}, 0)
}
