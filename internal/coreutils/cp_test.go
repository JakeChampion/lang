package coreutils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// cp(1) writes almost nothing to stdout, so nearly every case here is
// about the TREE the two implementations leave behind: which names exist,
// what mode each carries, which of them share an inode, what a symbolic
// link holds, and — for the sparse cases — how many blocks a file
// actually occupies. `seedTree` gives each side its own freshly built
// working directory and compares everything under it, so a stray
// temporary name fails as loudly as a file that was never copied.
//
// Six things this corpus is mostly about, each one a behaviour the
// documentation does not give and a differential against the reference
// binary does:
//
//   - `-r` implies `-P` for the COMMAND LINE too, not just for the tree.
//     `cp sl out` follows a symbolic link and `cp -r sl out` copies the
//     link itself; `-H` is how the command-line-only reading is asked
//     for outright. cpLinks covers all four spellings both ways.
//   - the entry list is snapshotted BEFORE the destination is created.
//     `cp -r d d` makes `d/d` inside the directory being walked, and a
//     list read afterwards would hand the walk its own destination.
//   - copying a directory into itself REPORTS and CONTINUES: exit 1,
//     with `d/d` created and filled from that snapshot.
//   - a fresh destination takes the source's mode through the umask; an
//     EXISTING one keeps its own, because the file is truncated in place
//     rather than recreated. `-p` sets the source's mode verbatim on
//     both. cpModes is the fixture where the two differ visibly.
//   - `--strip-trailing-slashes` reaches only the forms that derive a
//     name from the source's basename: the direct two-operand form keeps
//     `ff/` and still reports `cannot stat 'ff/'`.
//   - `-b` with `-n` is not a conflict here, unlike `mv`: only the `-n`
//     warning appears and the status stays 0.
//
// Two traps this corpus is shaped around:
//
//   - `cp FIFO dest` with no writer BLOCKS FOREVER, on both sides. Every
//     case with a FIFO source is under `-r`/`-R`, which recreates the
//     FIFO instead of reading it. `--copy-contents` reads it even under
//     `-r`, so that option has no FIFO case at all — a `timeout` would
//     make it a comparison of two killed processes rather than of two
//     answers.
//   - the suite runs as root here, so every permission-denied path (`-f`
//     over a mode-000 file, an unreadable source, an unwritable
//     directory) succeeds on both sides and proves nothing. There are no
//     such cases: one would look like coverage while testing nothing.
//
//     `-f`'s unlink-and-retry is the casualty. It IS implemented and it
//     was verified by hand against GNU 9.4 on an immutable file (`chattr
//     +i`), the one unopenable-but-present destination a root-run suite
//     can build: without `-f` the answer is `cannot create regular file
//     'im': Operation not permitted` and with it the answer moves to
//     `cannot remove 'im': Operation not permitted`. That is left OUT of
//     the corpus rather than in it, because `chattr` needs both
//     CAP_LINUX_IMMUTABLE and a filesystem that supports the flag, and a
//     case that quietly does something else on overlayfs is worse than
//     an absent one. The dangling-symlink destination below is the part
//     of the family that IS reachable, so it is covered.
//
// `--debug` is held to its exit status and stream shape rather than its
// bytes, and docs/COREUTILS.md says why: GNU's second line names its own
// copy_file_range offload and SEEK_HOLE probing, which is a mechanism
// this copy does not use.

// cpBasic is the main fixture: two plain files with different modes, a
// directory with a nested one inside it, an empty directory to copy
// into, a symbolic link to a file, a dangling one, and a hard-link pair.
func cpBasic(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "f1", "hello\n")
	seedWrite(t, dir, "f2", "world!!\n")
	if err := os.Chmod(filepath.Join(dir, "f2"), 0o741); err != nil {
		t.Fatalf("chmod f2: %v", err)
	}
	seedMkdir(t, dir, "d1/sub")
	seedWrite(t, dir, "d1/a", "one\n")
	seedWrite(t, dir, "d1/sub/s1", "sub\n")
	seedMkdir(t, dir, "d2")
	seedSymlink(t, dir, "f1", "sl")
	seedSymlink(t, dir, "nowhere", "dangle")
	seedHardlink(t, dir, "f1", "hard")
}

// cpOne is one file and nothing else — the fixture for the operand-count
// diagnostics, where anything more would be noise.
func cpOne(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "f1", "hello\n")
}

// cpSelf is the copy-a-directory-into-itself fixture, in the three
// shapes that answer differently: an empty directory, one holding a
// file, and one that already holds a `d` of its own. What separates them
// is the entry list, which is read before the destination exists.
func cpSelf(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "empty")
	seedMkdir(t, dir, "full")
	seedWrite(t, dir, "full/x", "X\n")
	seedMkdir(t, dir, "taken/taken")
}

// cpLinks is the dereference fixture: a link to a file and a link to a
// directory, both named on the command line, plus links INSIDE a tree so
// the command-line rule and the recursion rule can be told apart.
func cpLinks(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "f1", "hello\n")
	seedSymlink(t, dir, "f1", "sl")
	seedSymlink(t, dir, "nowhere", "dangle")
	seedMkdir(t, dir, "tree")
	seedWrite(t, dir, "tree/t", "T\n")
	seedSymlink(t, dir, "t", "tree/ln")
	seedSymlink(t, dir, "nowhere", "tree/bad")
	seedSymlink(t, dir, "tree", "dl")
}

// cpModes is the mode fixture. `src` is 0777 so the umask has something
// to filter, `plain` is the fresh destination's name, and `taken` is an
// existing destination at 0700 — which keeps its own mode unless `-p`
// says otherwise. The directory is 0777 for the same reason.
func cpModes(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "src", "S\n")
	if err := os.Chmod(filepath.Join(dir, "src"), 0o777); err != nil {
		t.Fatalf("chmod src: %v", err)
	}
	seedWrite(t, dir, "taken", "T\n")
	if err := os.Chmod(filepath.Join(dir, "taken"), 0o700); err != nil {
		t.Fatalf("chmod taken: %v", err)
	}
	seedMkdir(t, dir, "sd")
	seedWrite(t, dir, "sd/x", "X\n")
	if err := os.Chmod(filepath.Join(dir, "sd"), 0o777); err != nil {
		t.Fatalf("chmod sd: %v", err)
	}
	// An EXISTING directory destination, at a mode the source's would
	// overwrite if the copy wrongly chmodded it.
	seedMkdir(t, dir, "sdtaken")
	if err := os.Chmod(filepath.Join(dir, "sdtaken"), 0o700); err != nil {
		t.Fatalf("chmod sdtaken: %v", err)
	}
	seedTouch(t, dir, "src", 1400000000, 123456789)
}

// cpUpdate is the -u / --update fixture: `old` is a day older than
// `dest`, `new` a day newer, and `same` shares its timestamp to the
// nanosecond — which is the resolution the comparison actually runs at,
// so `same` is what separates "not newer" from "older".
func cpUpdate(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "old", "OLD\n")
	seedWrite(t, dir, "new", "NEW\n")
	seedWrite(t, dir, "same", "SAME\n")
	seedWrite(t, dir, "dest", "DEST\n")
	seedTouch(t, dir, "dest", 1500000000, 200000000)
	seedTouch(t, dir, "old", 1400000000, 0)
	seedTouch(t, dir, "new", 1600000000, 0)
	seedTouch(t, dir, "same", 1500000000, 200000000)
}

// cpBackup is the backup fixture: a source, an occupied destination, and
// the neighbours the control picks between.
func cpBackup(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "a", "A\n")
	seedWrite(t, dir, "b", "B\n")
}

func cpBackupTaken(t *testing.T, dir string) {
	t.Helper()
	cpBackup(t, dir)
	seedWrite(t, dir, "b~", "OLD\n")
	seedWrite(t, dir, "b.~1~", "ONE\n")
	seedWrite(t, dir, "b.~2~", "TWO\n")
}

// cpSparse is the hole fixture: a 1 MiB file whose only data is three
// bytes at the very end, so the whole of it but the last block is a
// hole. It is the shape that separates the three `--sparse` modes, and
// the one that caught a copy punching holes at the READ size rather than
// the filesystem's block size: 256 blocks where GNU's costs 8.
func cpSparse(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "sp")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sp: %v", err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		t.Fatalf("truncate sp: %v", err)
	}
	if _, err := f.WriteAt([]byte("end"), (1<<20)-3); err != nil {
		t.Fatalf("write sp tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close sp: %v", err)
	}
	// A dense file of the same length, so a copy that made everything
	// sparse would fail here rather than pass everywhere.
	seedWrite(t, dir, "dense", "abcdefghij")
}

// cpGroup is the hard-link fixture: one inode under two names inside a
// directory, which is what `--preserve=links` reproduces and what its
// absence splits into two files.
func cpGroup(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "lk")
	seedWrite(t, dir, "lk/one", "shared\n")
	seedHardlink(t, dir, "lk/one", "lk/two")
	seedWrite(t, dir, "f1", "hello\n")
	seedHardlink(t, dir, "f1", "hard")
	seedMkdir(t, dir, "d2")
}

// cpFifo is the special-file fixture. Every case using it passes -r or
// -R: a plain `cp fifo out` blocks forever on both sides, with no writer
// to end the read.
func cpFifo(t *testing.T, dir string) {
	t.Helper()
	seedFifo(t, filepath.Join(dir, "fifo"))
	seedWrite(t, dir, "f1", "hello\n")
	seedMkdir(t, dir, "hold")
	seedFifo(t, filepath.Join(dir, "hold/inner"))
	seedWrite(t, dir, "hold/plain", "P\n")
}

// cpSlash is the trailing-slash fixture: a directory and a plain file, so
// the same `x/` reads as a directory on one and as ENOTDIR on the other.
func cpSlash(t *testing.T, dir string) {
	t.Helper()
	seedMkdir(t, dir, "d1/sub")
	seedWrite(t, dir, "d1/a", "A\n")
	seedWrite(t, dir, "ff", "F\n")
	seedMkdir(t, dir, "dst")
	seedSymlink(t, dir, "nowhere/deeper", "dangledest")
}

func init() {
	registerCorpus("cp", cpCases)
}

// cpWalk is the fixture for the destination-inside-the-source shapes and
// for the rule that a directory's `-v` line belongs to its CREATION.
//
// Every directory in it holds at most ONE entry, and that is the whole
// design. A recursive copy visits entries in ascending inode order, which
// two fresh working directories do not reproduce, so a case whose source
// directory holds two entries is not well posed: `cp -r full full` copies
// `full/x` when the destination it just made sorts after it and copies
// nothing when it sorts before, and GNU is no more reproducible about that
// than this implementation. With one entry per directory there is no order
// to disagree about.
//
// The order rule itself, and the abort shapes that need a populated tree,
// are pinned by TestCpWalkOrder and TestCpIntoItself, which run both
// implementations against one source tree and account for the destination
// inode allocated by each run.
func cpWalk(t *testing.T, dir string) {
	t.Helper()
	// A chain: one entry per level, so the walk order is forced.
	seedMkdir(t, dir, "chain/sub")
	seedWrite(t, dir, "chain/sub/leaf", "l\n")
	// An EMPTY directory to copy into: the destination made inside it is
	// then the only entry its walk sees, so the abort lands in one place.
	seedMkdir(t, dir, "nest/hole")
	// Two `-T` destinations: one empty, so every directory under it is
	// created, and one already holding `sub`, so that one is not.
	seedMkdir(t, dir, "empty")
	seedMkdir(t, dir, "part/sub")
	seedWrite(t, dir, "part/sub/old", "o\n")
}

func cpCases(t *testing.T) []invocation {
	t.Helper()
	var out []invocation

	// The operand-count and shape diagnostics.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no-operands", nil},
		{"one-operand", []string{"f1"}},
		{"missing-after-dashdash", []string{"--", "f1"}},
		{"target-not-a-directory", []string{"f1", "f1", "f1"}},
		{"target-missing", []string{"f1", "f1", "nosuchdir"}},
		{"t-not-a-directory", []string{"-t", "f1", "f1"}},
		{"t-and-T", []string{"-t", "d1", "-T", "f1"}},
		{"T-extra-operand", []string{"-T", "f1", "f1", "out"}},
		{"source-missing", []string{"nosuch", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpOne})
	}

	// The getopt surface: the ambiguity lists, which print their
	// candidates in the option table's declaration order, plus the
	// unknown spellings.
	for _, pre := range []string{"--a", "--c", "--co", "--d", "--n", "--no", "--p", "--r", "--re", "--s", "--v", "--ver", "--zzz", "--x", "--Z"} {
		out = append(out, invocation{name: "prefix" + pre, args: []string{pre, "f1", "out"}, seedTree: cpBasic})
	}
	out = append(out,
		invocation{name: "unknown-short", args: []string{"-k", "f1", "out"}, seedTree: cpBasic},
		invocation{name: "prefix-unique-backup", args: []string{"--b", "f1", "f2"}, seedTree: cpBasic},
		invocation{name: "prefix-unique-target", args: []string{"--ta", "d2", "f1"}, seedTree: cpBasic},
		invocation{name: "prefix-unique-strip", args: []string{"--st", "f1", "out"}, seedTree: cpBasic},
	)

	// The rejected argument lists, each printed in its own order —
	// `--reflink`'s is auto/always/never where `--sparse`'s is
	// never/auto/always, and the backup control's names itself
	// `backup type` rather than the option.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"bad-backup", []string{"--backup=zz", "f1", "f2"}},
		{"bad-preserve", []string{"--preserve=zz", "f1", "out"}},
		{"bad-no-preserve", []string{"--no-preserve=zz", "f1", "out"}},
		{"bad-update", []string{"--update=zz", "f1", "f2"}},
		{"bad-sparse", []string{"--sparse=zz", "f1", "out"}},
		{"bad-reflink", []string{"--reflink=zz", "f1", "out"}},
		{"ambiguous-sparse-value", []string{"--sparse", "a", "f1"}},
		{"prefix-sparse-value", []string{"--sparse=al", "f1", "out"}},
		{"prefix-reflink-value", []string{"--reflink=a", "f1", "out"}},
		{"prefix-update-value", []string{"--update=o", "f1", "f2"}},
		{"missing-no-preserve-arg", []string{"--no-preserve", "f1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	// The plain copies, and the three operand shapes.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"file-to-new", []string{"f1", "out"}},
		{"file-over-file", []string{"f1", "f2"}},
		{"file-into-directory", []string{"f1", "d2"}},
		{"file-into-directory-slash", []string{"f1", "d2/"}},
		{"two-into-directory", []string{"f1", "f2", "d2"}},
		{"target-directory", []string{"-t", "d2", "f1", "f2"}},
		{"target-directory-glued", []string{"-td2", "f1"}},
		{"target-directory-long", []string{"--target-directory=d2", "f1"}},
		{"target-directory-dashdash", []string{"-t", "d2", "--", "f1"}},
		{"no-target-directory", []string{"-T", "f1", "out"}},
		{"no-target-directory-over-dir", []string{"-T", "d1", "out"}},
		{"same-file", []string{"f1", "f1"}},
		{"same-file-dotted", []string{"f1", "./f1"}},
		{"same-file-hardlink", []string{"f1", "hard"}},
		{"directory-without-r", []string{"d1", "out"}},
		{"directory-without-r-into-dir", []string{"d1", "d2"}},
		{"recursive", []string{"-r", "d1", "out"}},
		{"recursive-upper", []string{"-R", "d1", "out"}},
		{"recursive-long", []string{"--recursive", "d1", "out"}},
		{"recursive-into-directory", []string{"-r", "d1", "d2"}},
		{"recursive-over-file", []string{"-r", "d1", "f1"}},
		{"recursive-to-missing-parent", []string{"-r", "d1", "d2/x/y"}},
		{"verbose", []string{"-v", "f1", "out"}},
		{"verbose-into-directory", []string{"-v", "f1", "f2", "d2"}},
		{"clustered", []string{"-rp", "d1", "out"}},
		{"clustered-upper", []string{"-Rp", "d1", "out"}},
		{"dashdash", []string{"--", "f1", "out"}},
		{"link", []string{"-l", "f1", "out"}},
		{"link-recursive", []string{"-l", "-r", "d1", "out"}},
		{"symbolic-link", []string{"-s", "f1", "out"}},
		{"attributes-only-new", []string{"--attributes-only", "f1", "out"}},
		{"attributes-only-existing", []string{"--attributes-only", "f1", "f2"}},
		{"remove-destination", []string{"--remove-destination", "f1", "f2"}},
		// `-v` is the point of these two: the removal ANNOUNCES itself,
		// and `-f`'s does not. Without them the corpus sees the same
		// tree either way and the line is invisible.
		{"remove-destination-verbose", []string{"-v", "--remove-destination", "f1", "f2"}},
		{"remove-destination-verbose-new", []string{"-v", "--remove-destination", "f1", "out"}},
		{"force-verbose", []string{"-v", "-f", "f1", "f2"}},
		{"one-file-system", []string{"-x", "-r", "d1", "out"}},
		{"selinux-Z", []string{"-Z", "f1", "out"}},
		{"selinux-context", []string{"--context", "f1", "out"}},
		{"selinux-context-value", []string{"--context=x", "f1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	// Copying a directory into itself: reported, and still copied.
	//
	// Only the shapes whose walk has nothing to disagree about. `-r full
	// full`, where `full` holds a file AND the destination just made
	// beside it, is not one: whether `full/x` is copied depends on which
	// of the two got the lower inode, and GNU is no more reproducible
	// about that than this implementation. TestCpIntoItself covers the
	// populated shapes by running both sides against one tree.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"self-empty", []string{"-r", "empty", "empty"}},
		{"self-empty-verbose", []string{"-rv", "empty", "empty"}},
		{"self-taken", []string{"-r", "taken", "taken"}},
		{"self-taken-verbose", []string{"-rv", "taken", "taken"}},
		{"self-into-subdir", []string{"-r", "full", "full/x"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpSelf})
	}

	// The dereference rules, in every spelling and both positions.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"link-default", []string{"sl", "out"}},
		{"link-P", []string{"-P", "sl", "out"}},
		{"link-d", []string{"-d", "sl", "out"}},
		{"link-H", []string{"-H", "sl", "out"}},
		{"link-L", []string{"-L", "sl", "out"}},
		{"dangling-default", []string{"dangle", "out"}},
		{"dangling-P", []string{"-P", "dangle", "out"}},
		{"dangling-L", []string{"-L", "dangle", "out"}},
		{"link-r-default", []string{"-r", "sl", "out"}},
		{"link-r-P", []string{"-rP", "sl", "out"}},
		{"link-r-H", []string{"-rH", "sl", "out"}},
		{"link-r-L", []string{"-rL", "sl", "out"}},
		{"tree-r-default", []string{"-r", "tree", "out"}},
		{"tree-r-H", []string{"-rH", "tree", "out"}},
		{"tree-r-L", []string{"-rL", "tree", "out"}},
		{"tree-r-P", []string{"-rP", "tree", "out"}},
		{"tree-archive", []string{"-a", "tree", "out"}},
		{"dirlink-r-default", []string{"-r", "dl", "out"}},
		{"dirlink-r-H", []string{"-rH", "dl", "out"}},
		{"dirlink-r-L", []string{"-rL", "dl", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpLinks})
	}

	// Modes and timestamps. A fresh destination takes the source's mode
	// through the umask; an existing one keeps its own.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"mode-fresh", []string{"src", "plain"}},
		{"mode-existing", []string{"src", "taken"}},
		// The EXISTING-destination half of each --preserve. These are the
		// shape that caught an unconditional chmod: a caller that had just
		// decided not to preserve the mode still passed the source's bits
		// to apply_attrs, so `--preserve=timestamps src taken` re-moded
		// `taken` from 0700 to the source's 0644. Every case above this
		// used the fresh destination, which is why a green run missed it.
		{"mode-existing-preserve-timestamps", []string{"--preserve=timestamps", "src", "taken"}},
		{"mode-existing-preserve-ownership", []string{"--preserve=ownership", "src", "taken"}},
		{"mode-existing-preserve-mode", []string{"--preserve=mode", "src", "taken"}},
		{"mode-existing-preserve-all", []string{"--preserve=all", "src", "taken"}},
		{"mode-existing-attributes-only", []string{"--attributes-only", "src", "taken"}},
		{"mode-existing-attributes-only-p", []string{"--attributes-only", "-p", "src", "taken"}},
		// -T is what makes the existing DIRECTORY the destination rather
		// than its parent: without it `cp -r sd sdtaken` is the
		// into-a-directory form and never reaches copy_dir's
		// existing-destination branch at all. Measured — GNU leaves
		// `sdtaken` at 0700 under both plain and --preserve=timestamps,
		// and moves it to the source's 0777 only under -p.
		{"mode-dir-existing-timestamps", []string{"-rT", "--preserve=timestamps", "sd", "sdtaken"}},
		{"mode-dir-existing-plain", []string{"-rT", "sd", "sdtaken"}},
		{"mode-dir-existing-p", []string{"-rT", "-p", "sd", "sdtaken"}},
		{"mode-dir-existing-ownership", []string{"-rT", "--preserve=ownership", "sd", "sdtaken"}},
		{"mode-fresh-p", []string{"-p", "src", "plain"}},
		{"mode-existing-p", []string{"-p", "src", "taken"}},
		{"mode-preserve-mode", []string{"--preserve=mode", "src", "plain"}},
		{"mode-preserve-timestamps", []string{"--preserve=timestamps", "src", "plain"}},
		{"mode-preserve-all", []string{"--preserve=all", "src", "plain"}},
		{"mode-preserve-bare", []string{"--preserve", "src", "plain"}},
		{"mode-no-preserve-mode", []string{"-p", "--no-preserve=mode", "src", "plain"}},
		{"mode-no-preserve-all", []string{"-a", "--no-preserve=all", "sd", "out"}},
		{"mode-dir-fresh", []string{"-r", "sd", "out"}},
		{"mode-dir-p", []string{"-rp", "sd", "out"}},
		{"mode-dir-archive", []string{"-a", "sd", "out"}},
		{"mode-remove-destination", []string{"--remove-destination", "src", "taken"}},
		{"mode-attributes-only", []string{"--attributes-only", "src", "plain"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpModes})
	}

	// -i, -n and --update. The answers are fed on stdin; declining an
	// -i prompt exits 1, which is the case the corpus is here to pin.
	for _, c := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"interactive-yes", []string{"-i", "old", "dest"}, "y\n"},
		{"interactive-no", []string{"-i", "old", "dest"}, "n\n"},
		{"interactive-eof", []string{"-i", "old", "dest"}, ""},
		{"interactive-upper", []string{"-i", "old", "dest"}, "Y\n"},
		{"interactive-word", []string{"-i", "old", "dest"}, "yes\n"},
		{"interactive-other", []string{"-i", "old", "dest"}, "q\n"},
		{"interactive-verbose", []string{"-iv", "old", "dest"}, "y\n"},
		{"interactive-then-force", []string{"-i", "-f", "old", "dest"}, ""},
		{"force-then-interactive", []string{"-f", "-i", "old", "dest"}, "n\n"},
	} {
		out = append(out, invocation{name: c.name, args: c.args, stdin: c.stdin, seedTree: cpUpdate})
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no-clobber", []string{"-n", "old", "dest"}},
		{"no-clobber-long", []string{"--no-clobber", "old", "dest"}},
		{"update-none", []string{"--update=none", "old", "dest"}},
		{"update-all", []string{"--update=all", "old", "dest"}},
		{"update-older-older", []string{"-u", "old", "dest"}},
		{"update-older-newer", []string{"-u", "new", "dest"}},
		{"update-older-same", []string{"-u", "same", "dest"}},
		{"update-bare-long", []string{"--update", "old", "dest"}},
		{"update-verbose-skip", []string{"-uv", "same", "dest"}},
		{"update-verbose-copy", []string{"-uv", "new", "dest"}},
		{"no-clobber-then-interactive", []string{"-n", "-i", "old", "dest"}},
		{"interactive-then-no-clobber", []string{"-i", "-n", "old", "dest"}},
		{"backup-with-no-clobber", []string{"-b", "-n", "old", "dest"}},
		{"no-clobber-with-backup", []string{"-n", "-b", "old", "dest"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpUpdate})
	}

	// Backups, and the environment that names the control.
	for _, c := range []struct {
		name string
		args []string
		env  []string
	}{
		{name: "backup-simple", args: []string{"-b", "a", "b"}},
		{name: "backup-verbose", args: []string{"-vb", "a", "b"}},
		{name: "backup-long", args: []string{"--backup", "a", "b"}},
		{name: "backup-suffix", args: []string{"-b", "-S", ".bak", "a", "b"}},
		{name: "backup-suffix-long", args: []string{"--suffix=.bak", "a", "b"}},
		{name: "backup-suffix-empty", args: []string{"-S", "", "a", "b"}},
		{name: "backup-suffix-slash", args: []string{"-S", "x/", "a", "b"}},
		{name: "backup-numbered", args: []string{"--backup=numbered", "a", "b"}},
		{name: "backup-t", args: []string{"--backup=t", "a", "b"}},
		{name: "backup-existing", args: []string{"--backup=existing", "a", "b"}},
		{name: "backup-nil", args: []string{"--backup=nil", "a", "b"}},
		{name: "backup-simple-word", args: []string{"--backup=simple", "a", "b"}},
		{name: "backup-never", args: []string{"--backup=never", "a", "b"}},
		{name: "backup-none", args: []string{"--backup=none", "a", "b"}},
		{name: "backup-off", args: []string{"--backup=off", "a", "b"}},
		{name: "backup-env-control", args: []string{"-b", "a", "b"}, env: []string{"VERSION_CONTROL=numbered"}},
		{name: "backup-env-suffix", args: []string{"-b", "a", "b"}, env: []string{"SIMPLE_BACKUP_SUFFIX=.env"}},
		{name: "backup-env-bogus", args: []string{"-b", "a", "b"}, env: []string{"VERSION_CONTROL=bogus"}},
		{name: "backup-env-unused", args: []string{"a", "b"}, env: []string{"VERSION_CONTROL=bogus"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, env: c.env, seedTree: cpBackup})
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"backup-existing-taken", []string{"--backup=existing", "a", "b"}},
		{"backup-numbered-taken", []string{"--backup=numbered", "a", "b"}},
		{"backup-simple-taken", []string{"--backup=simple", "a", "b"}},
		{"backup-default-taken", []string{"-b", "a", "b"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBackupTaken})
	}

	// Sparse files. These are the only cases that put the block count
	// into the comparison, and without it the three modes are
	// indistinguishable.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"sparse-default", []string{"sp", "out"}},
		{"sparse-auto", []string{"--sparse=auto", "sp", "out"}},
		{"sparse-never", []string{"--sparse=never", "sp", "out"}},
		{"sparse-always", []string{"--sparse=always", "sp", "out"}},
		{"sparse-preserve", []string{"-p", "sp", "out"}},
		{"sparse-dense-default", []string{"dense", "out"}},
		{"sparse-dense-always", []string{"--sparse=always", "dense", "out"}},
		{"sparse-into-directory", []string{"sp", "dense", "out"}},
	} {
		inv := invocation{name: c.name, args: c.args, seedTree: cpSparse, sparse: true}
		if c.name == "sparse-into-directory" {
			inv.seedTree = func(t *testing.T, dir string) {
				cpSparse(t, dir)
				seedMkdir(t, dir, "out")
			}
		}
		out = append(out, inv)
	}

	// Hard-link structure, with and without --preserve=links.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"links-plain", []string{"-r", "lk", "out"}},
		{"links-preserve", []string{"-r", "--preserve=links", "lk", "out"}},
		{"links-archive", []string{"-a", "lk", "out"}},
		{"links-d", []string{"-rd", "lk", "out"}},
		{"links-two-operands", []string{"--preserve=links", "f1", "hard", "d2"}},
		{"links-two-operands-plain", []string{"f1", "hard", "d2"}},
		{"links-hardlink-option", []string{"-l", "f1", "hard", "d2"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpGroup})
	}

	// Special files. Every case is recursive: a plain copy of a FIFO
	// never returns.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"fifo-recursive", []string{"-r", "fifo", "out"}},
		{"fifo-recursive-upper", []string{"-R", "fifo", "out"}},
		{"fifo-in-tree", []string{"-r", "hold", "out"}},
		{"fifo-in-tree-archive", []string{"-a", "hold", "out"}},
		{"fifo-preserve", []string{"-rp", "fifo", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpFifo})
	}

	// --parents, and the trailing-slash rules.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"parents-flat", []string{"--parents", "ff", "dst"}},
		{"parents-nested", []string{"--parents", "d1/a", "dst"}},
		{"parents-deep", []string{"--parents", "d1/sub", "dst"}},
		{"parents-recursive", []string{"--parents", "-r", "d1/sub", "dst"}},
		{"parents-verbose", []string{"--parents", "-v", "d1/a", "dst"}},
		{"parents-not-a-directory", []string{"--parents", "ff", "ff"}},
		{"parents-missing-directory", []string{"--parents", "ff", "nosuchdir"}},
		{"parents-target-directory", []string{"--parents", "-t", "dst", "d1/a"}},
		{"parents-with-T", []string{"--parents", "-T", "ff", "out"}},
		{"slash-directory-no-r", []string{"d1/", "out"}},
		{"slash-directory-r", []string{"-r", "d1/", "out"}},
		{"slash-directory-into", []string{"-r", "d1/", "dst"}},
		{"slash-file", []string{"ff/", "out"}},
		{"strip-directory-no-r", []string{"--strip-trailing-slashes", "d1/", "out"}},
		{"strip-directory-into", []string{"--strip-trailing-slashes", "-r", "d1/", "dst"}},
		{"strip-file-direct", []string{"--strip-trailing-slashes", "ff/", "out"}},
		{"strip-file-into", []string{"--strip-trailing-slashes", "ff/", "dst"}},
		{"strip-target-directory", []string{"--strip-trailing-slashes", "-t", "dst", "ff/"}},
		{"no-strip-target-directory", []string{"-t", "dst", "ff/"}},
		{"slash-missing-source", []string{"-r", "nosuch/", "dst"}},
		// A destination that is a symlink to something missing is its own
		// refusal — `not writing through dangling symlink` — and `-f` does
		// NOT override it. Reachable as root, unlike the rest of the
		// unopenable-destination family.
		{"dangling-dest", []string{"ff", "dangledest"}},
		{"dangling-dest-force", []string{"-f", "ff", "dangledest"}},
		{"dangling-dest-remove-destination", []string{"--remove-destination", "ff", "dangledest"}},
		{"strip-missing-source", []string{"--strip-trailing-slashes", "-r", "nosuch/", "dst"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpSlash})
	}

	// --reflink. There is no clone primitive, so `always` reports the
	// failure GNU reports where the filesystem cannot clone — which is
	// what the filesystem under this test directory answers.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"reflink-auto", []string{"--reflink=auto", "f1", "out"}},
		{"reflink-never", []string{"--reflink=never", "f1", "out"}},
		{"reflink-always", []string{"--reflink=always", "f1", "out"}},
		{"reflink-bare", []string{"--reflink", "f1", "out"}},
		{"reflink-auto-recursive", []string{"--reflink=auto", "-r", "d1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	// Every recursive row that names MORE THAN ONE entry of a directory
	// on either stream — the `-v` lines, and the per-entry refusals of a
	// `--reflink=always` that cannot clone or a `-s` whose relative
	// target does not resolve. Their lines are the comparison and their
	// order is the filesystem's. See invocation.unordered.
	for _, c := range []struct {
		name    string
		args    []string
		fixture func(*testing.T, string)
	}{
		{"verbose-recursive", []string{"-v", "-r", "d1", "out"}, cpBasic},
		{"clustered-verbose", []string{"-vrp", "d1", "out"}, cpBasic},
		{"reflink-always-recursive", []string{"--reflink=always", "-r", "d1", "out"}, cpBasic},
		{"fifo-in-tree-verbose", []string{"-rv", "hold", "out"}, cpFifo},
		{"strip-directory-verbose", []string{"--strip-trailing-slashes", "-vr", "d1/", "dst"}, cpSlash},
		{"symbolic-link-recursive", []string{"-s", "-r", "d1", "out"}, cpBasic},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: c.fixture, unordered: true})
	}

	// The destination inside the source, and the rule that a directory's
	// `-v` line is its creation's. One entry per directory throughout, so
	// nothing here depends on the walk order — see cpWalk.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"walk-into-nested", []string{"-v", "-r", "nest", "nest/hole"}},
		{"walk-into-nested-plain", []string{"-r", "nest", "nest/hole"}},
		{"walk-into-nested-archive", []string{"-a", "-v", "nest", "nest/hole"}},
		{"walk-into-nested-then-operand", []string{"-v", "-r", "nest", "chain", "nest/hole"}},
		{"walk-chain-into-directory", []string{"-v", "-r", "chain", "empty"}},
		{"walk-dir-line-on-creation", []string{"-v", "-r", "-T", "chain", "empty"}},
		{"walk-dir-line-not-on-existing", []string{"-v", "-r", "-T", "chain", "part"}},
		{"walk-dir-line-archive", []string{"-a", "-v", "-T", "chain", "part"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpWalk})
	}

	// The ORDER of the two streams, which every case above is blind to:
	// they compare stdout and stderr separately, so a `-v` line and a
	// diagnostic can be individually right and still reach a terminal the
	// wrong way round. gnulib's error() flushes stdout first, so the line
	// describing a copy always precedes a later failure; a utility that
	// buffered its own stdout and flushed at exit prints them inverted.
	//
	// `merged` is what sees it. The failure-then-success row is here on
	// purpose: without it the whole block would pass on an implementation
	// that simply wrote every verbose line before every diagnostic.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"order-copy-then-failure", []string{"-v", "f1", "nosuch", "d2"}},
		{"order-failure-then-copy", []string{"-v", "nosuch", "f1", "d2"}},
		{"order-between-two-copies", []string{"-v", "f1", "nosuch", "f2", "d2"}},
		{"order-parents", []string{"--parents", "-v", "d1/a", "nosuch", "d2"}},
		{"order-no-verbose", []string{"f1", "nosuch", "d2"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic, merged: true})
	}

	// The same question for a recursive copy and for the into-itself
	// diagnostic, which arrives after the whole operand rather than
	// before it. On cpWalk rather than cpBasic: `unordered` is no help to
	// a merged stream — sorting its lines destroys the very order the
	// case is about — so these need a tree with nothing to disagree
	// about, which is one entry per directory.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"order-recursive", []string{"-v", "-r", "chain", "nosuch", "empty"}},
		{"order-into-itself", []string{"-v", "-r", "nest", "nest/hole"}},
		{"order-into-itself-then-operand", []string{"-v", "-r", "nest", "chain", "nest/hole"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpWalk, merged: true})
	}

	return out
}

func TestCpParity(t *testing.T) {
	requireParity(t, "cp", cpCases(t))
}

func TestCpHelpVersion(t *testing.T) {
	requireHelp(t, "cp", []string{"--help"}, 0)
	requireHelp(t, "cp", []string{"--hel"}, 0)
	requireHelp(t, "cp", []string{"--help", "a", "b"}, 0)
	requireHelp(t, "cp", []string{"a", "b", "--help"}, 0)
	requireHelp(t, "cp", []string{"-r", "--help"}, 0)
	requireVersion(t, "cp", []string{"--version"}, 0)
	requireVersion(t, "cp", []string{"--vers"}, 0)
	requireVersion(t, "cp", []string{"a", "b", "--version"}, 0)
	requireVersion(t, "cp", []string{"-p", "--version", "-r"}, 0)
}

// runCpIn runs one cp invocation in `dir` with its two streams merged, and
// returns what it printed. argv[0] is spelled `cp` for both binaries, so a
// diagnostic's program-name prefix compares directly.
func runCpIn(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Args = append([]string{"cp"}, args...)
	cmd.Dir = dir
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	var exit int
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		exit = ee.ExitCode()
	}
	return fmt.Sprintf("exit %d\n%s", exit, out)
}

// inodeOrder lists the entries of `dir` by ascending inode, which is the
// order a recursive copy visits them in. Read from the tree itself rather
// than assumed from the seeding order: an inode is reused after a delete,
// so "created later" does not mean "numbered higher".
func inodeOrder(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	type ent struct {
		name string
		ino  uint64
	}
	var es []ent
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			t.Fatalf("stat %s/%s: %v", dir, de.Name(), err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("no stat for %s/%s", dir, de.Name())
		}
		es = append(es, ent{de.Name(), st.Ino})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].ino < es[j].ino })
	var names []string
	for _, e := range es {
		names = append(names, e.name)
	}
	return names
}

// readdirOrder lists the entries of `dir` as the filesystem hands them
// back, which is the order the removal half of a cross-device move walks
// in. Reproducible between two directories holding the same names — the
// filesystem derives it from the names and its own seed — and, on a tree
// built to make it so, NOT the ascending-inode order the copy half uses.
func readdirOrder(t *testing.T, dir string) []string {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		t.Fatalf("readdirnames %s: %v", dir, err)
	}
	return names
}

// seedWalkOrderTree builds a directory whose ascending-INODE order is
// none of the orders it could be confused with: not the names sorted, not
// the filesystem's readdir order, and not the order the entries were
// created in.
//
// That last one is why it recycles inodes rather than just scrambling
// names. A filesystem that indexes directories hashes readdir order away
// from creation order on its own, but one that does not — ext4 with
// dir_index off, and whatever pullfrog's runner gave a five-entry
// directory on #9319 — hands back creation order, and there inode order
// and readdir order coincide however the names are chosen. Making a
// batch, deleting half of it and then creating the replacements puts the
// LATE entries on the freed low inodes, so inode order disagrees with
// creation order by construction and the two walks stay distinguishable
// either way.
//
// Returns the names it left behind, all of them directly under `dir`.
func seedWalkOrderTree(t *testing.T, dir string) []string {
	t.Helper()
	var filler []string
	for i := 0; i < 12; i++ {
		filler = append(filler, fmt.Sprintf("f%02d", i))
	}
	for i, n := range filler {
		// Two of them are directories, so "directories first" is ruled
		// out as well — one early in the batch and one late.
		if i == 2 || i == 9 {
			seedMkdir(t, dir, n)
			seedWrite(t, dir, n+"/inner", "i\n")
			continue
		}
		seedWrite(t, dir, n, n+"\n")
	}
	kept := append([]string(nil), filler[6:]...)
	for _, n := range filler[:6] {
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			t.Fatalf("remove %s: %v", n, err)
		}
	}
	for i := 0; i < 6; i++ {
		n := fmt.Sprintf("z%02d", i)
		if i == 3 {
			seedMkdir(t, dir, n)
			seedWrite(t, dir, n+"/inner", "i\n")
		} else {
			seedWrite(t, dir, n, n+"\n")
		}
		kept = append(kept, n)
	}
	return kept
}

// requireDistinctOrders fails when the tree cannot tell the two walks
// apart. A filesystem that returned its entries in inode order would
// leave an implementation that walked either way passing, so this is a
// fault in the fixture's reach on that host rather than something to
// skip past.
func requireDistinctOrders(t *testing.T, dir string, byInode, byReaddir []string) {
	t.Helper()
	if strings.Join(byInode, "\x00") == strings.Join(byReaddir, "\x00") {
		t.Fatalf("%s handed its entries back in inode order (%v), so the case cannot tell the two walks apart — the fixture needs a different construction on this filesystem", dir, byReaddir)
	}
	if sort.StringsAreSorted(byInode) {
		t.Fatalf("%s numbered its entries in name order (%v), so the case cannot tell inode order from sorted names", dir, byInode)
	}
}

// TestCpWalkOrder pins the order a recursive copy visits a directory's
// entries in: ascending INODE, which is neither readdir order nor the
// names sorted nor directories first.
//
// It is not a corpus case because a corpus case cannot see it. The two
// sides there each get their own working directory and inode numbers are
// not reproducible between two of them, so a `cp -rv` naming several
// entries compares unequal for reasons that are not the utility's. Here
// both binaries run against ONE source tree — GNU first, then its output
// removed, then ours — so they see the same inodes, and the expected
// order is computed from that tree rather than assumed.
func TestCpWalkOrder(t *testing.T) {
	gnu := referenceBin(t, "cp")
	ours := fernBin(t, "cp")
	dir := t.TempDir()

	seedMkdir(t, dir, "src")
	seedWalkOrderTree(t, filepath.Join(dir, "src"))

	srcDir := filepath.Join(dir, "src")
	want := inodeOrder(t, srcDir)
	// Ruling out readdir order matters as much as ruling out sorted
	// names: a readdir-order walk is the implementation this replaced,
	// and on a filesystem handing entries back in creation order the two
	// coincide and the case would pass on either.
	requireDistinctOrders(t, srcDir, want, readdirOrder(t, srcDir))

	var lines []string
	for _, name := range want {
		lines = append(lines, fmt.Sprintf("'src/%s' -> 'out/%s'", name, name))
	}

	for _, side := range []struct {
		label string
		bin   string
	}{{"gnu", gnu}, {"fern", ours}} {
		if err := os.RemoveAll(filepath.Join(dir, "out")); err != nil {
			t.Fatalf("clear out: %v", err)
		}
		got := runCpIn(t, side.bin, dir, "-v", "-r", "src", "out")
		// Only the top-level entries are checked: a subdirectory's own
		// lines follow its creation line, and interleaving them here
		// would say nothing more about the rule. A line is kept when
		// the name on its SOURCE side is one of them — the destination
		// side carries a slash of its own, so the whole line cannot be
		// tested for one.
		top := map[string]bool{}
		for _, name := range want {
			top["'src/"+name+"'"] = true
		}
		var seen []string
		for _, ln := range strings.Split(got, "\n") {
			src, _, ok := strings.Cut(ln, " -> ")
			if ok && top[src] {
				seen = append(seen, ln)
			}
		}
		if strings.Join(seen, "\n") != strings.Join(lines, "\n") {
			t.Errorf("%s did not walk src by inode\n want: %v\n  got: %v", side.label, lines, seen)
		}
	}
}

// TestCpIntoItself pins the destination-inside-the-source shapes on a
// POPULATED tree, which the corpus cannot: where the walk meets the
// destination decides which entries were copied before it stopped, and
// that depends on inode numbers. The source tree is shared, but deleting
// the first run's destination does not guarantee the next mkdir reuses its
// inode. Each run is checked against the prefix implied by its own newly
// allocated destination inode. The same expectation is checked against GNU,
// so the fixture's model must agree with the reference as well as Fern.
func TestCpIntoItself(t *testing.T) {
	gnu := referenceBin(t, "cp")
	ours := fernBin(t, "cp")

	for _, c := range []struct {
		name string
		args []string
		// made is what a run adds and the restore removes, so the
		// second side meets the tree the first one did.
		made []string
	}{
		{"flat", []string{"-v", "-r", "src", "src"}, []string{"src/src"}},
		{"nested", []string{"-v", "-r", "src", "src/bdir"}, []string{"src/bdir/src"}},
		{"nested-plain", []string{"-r", "src", "src/bdir"}, []string{"src/bdir/src"}},
		{"nested-archive", []string{"-a", "-v", "src", "src/bdir"}, []string{"src/bdir/src"}},
		{"then-operand", []string{"-v", "-r", "src", "other", "src/bdir"}, []string{"src/bdir/src", "src/bdir/other"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			seedMkdir(t, dir, "src/adir")
			seedWrite(t, dir, "src/adir/x", "x\n")
			seedMkdir(t, dir, "src/bdir")
			seedWrite(t, dir, "src/bdir/inner", "i\n")
			seedWrite(t, dir, "src/zfile", "z\n")
			seedMkdir(t, dir, "other")
			seedWrite(t, dir, "other/o", "o\n")
			original := treePaths(t, dir)
			entries := make(map[string]cpOracleEntry)
			contents := make(map[string]string)
			for _, p := range original {
				info, err := os.Stat(filepath.Join(dir, p))
				if err != nil {
					t.Fatal(err)
				}
				entries[p] = cpOracleEntry{ino: info.Sys().(*syscall.Stat_t).Ino, dir: info.IsDir()}
				if !info.IsDir() {
					data, err := os.ReadFile(filepath.Join(dir, p))
					if err != nil {
						t.Fatal(err)
					}
					contents[p] = string(data)
				}
			}

			restore := func() {
				for _, p := range c.made {
					if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
						t.Fatalf("restore %s: %v", p, err)
					}
				}
			}
			for _, side := range []struct{ name, bin string }{{"gnu", gnu}, {"fern", ours}} {
				restore()
				got := runCpIn(t, side.bin, dir, c.args...)
				target := c.made[0]
				info, err := os.Stat(filepath.Join(dir, target))
				if err != nil {
					t.Fatalf("%s did not create %s: %v", side.name, target, err)
				}
				entries[target] = cpOracleEntry{ino: info.Sys().(*syscall.Stat_t).Ino, dir: true}
				prefix, stopped := cpOraclePrefix(entries, "src", target)
				delete(entries, target)
				if !stopped {
					t.Fatal("fixture did not encounter its destination")
				}
				verbose := false
				for _, arg := range c.args {
					verbose = verbose || arg == "-v"
				}
				want := "exit 1\n"
				paths := append([]string(nil), original...)
				checkCopies := func(copies []cpOracleCopy) {
					for _, copy := range copies {
						if verbose {
							want += fmt.Sprintf("'%s' -> '%s'\n", copy.src, copy.dest)
						}
						paths = append(paths, copy.dest)
						if content, file := contents[copy.src]; file {
							data, err := os.ReadFile(filepath.Join(dir, copy.dest))
							if err != nil || string(data) != content {
								t.Errorf("%s copied %s incorrectly: %q, %v", side.name, copy.dest, data, err)
							}
						}
					}
				}
				checkCopies(prefix)
				want += fmt.Sprintf("cp: cannot copy a directory, 'src', into itself, '%s'\n", target)
				if len(c.made) == 2 {
					checkCopies([]cpOracleCopy{{"other", c.made[1]}, {"other/o", c.made[1] + "/o"}})
				}
				for path, content := range contents {
					data, err := os.ReadFile(filepath.Join(dir, path))
					if err != nil || string(data) != content {
						t.Errorf("%s changed source %s: %q, %v", side.name, path, data, err)
					}
				}
				sort.Strings(paths)
				if got != want {
					t.Errorf("%s output for cp %v\nwant: %q\n got: %q", side.name, c.args, want, got)
				}
				if gotTree := treePaths(t, dir); strings.Join(paths, "\n") != strings.Join(gotTree, "\n") {
					t.Errorf("%s copied the wrong inode-ordered prefix\nwant: %v\n got: %v", side.name, paths, gotTree)
				}
			}
		})
	}
}

type cpOracleEntry struct {
	ino uint64
	dir bool
}

type cpOracleCopy struct{ src, dest string }

// The expected observable prefix is an inode-ordered preorder, ending before
// the newly created destination. Only the original tree and that one inode
// participate: generated descendants are never visited after the stop.
func cpOraclePrefix(entries map[string]cpOracleEntry, source, destination string) ([]cpOracleCopy, bool) {
	var out []cpOracleCopy
	var visit func(string, string) bool
	visit = func(src, dest string) bool {
		if src == destination {
			return true
		}
		out = append(out, cpOracleCopy{src, dest})
		if !entries[src].dir {
			return false
		}
		var children []string
		for p := range entries {
			if filepath.Dir(p) == src {
				children = append(children, p)
			}
		}
		sort.Slice(children, func(i, j int) bool { return entries[children[i]].ino < entries[children[j]].ino })
		for _, p := range children {
			if visit(p, filepath.Join(dest, filepath.Base(p))) {
				return true
			}
		}
		return false
	}
	stopped := visit(source, destination)
	return out, stopped
}

func TestCpOraclePrefixUsesAllocatedDestinationInode(t *testing.T) {
	for _, tc := range []struct {
		name string
		ino  uint64
		want []cpOracleCopy
	}{
		{"before children", 5, []cpOracleCopy{{"src", "src/src"}}},
		{"between children", 15, []cpOracleCopy{{"src", "src/src"}, {"src/a", "src/src/a"}}},
		{"after children", 25, []cpOracleCopy{{"src", "src/src"}, {"src/a", "src/src/a"}, {"src/b", "src/src/b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := map[string]cpOracleEntry{"src": {dir: true}, "src/a": {ino: 10}, "src/b": {ino: 20}, "src/src": {ino: tc.ino, dir: true}}
			got, stopped := cpOraclePrefix(entries, "src", "src/src")
			if !stopped || !slices.Equal(got, tc.want) {
				t.Fatalf("prefix = %v, stopped=%v; want %v", got, stopped, tc.want)
			}
		})
	}
}

// treePaths lists every path under root, relative and sorted, so two runs
// can be compared without caring which order the walk found them in.
func treePaths(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel != "." {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}
