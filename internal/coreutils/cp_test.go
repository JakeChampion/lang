package coreutils

import (
	"os"
	"path/filepath"
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

func cpMkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}

// cpFifo is the special-file fixture. Every case using it passes -r or
// -R: a plain `cp fifo out` blocks forever on both sides, with no writer
// to end the read.
func cpFifo(t *testing.T, dir string) {
	t.Helper()
	cpMkfifo(t, filepath.Join(dir, "fifo"))
	seedWrite(t, dir, "f1", "hello\n")
	seedMkdir(t, dir, "hold")
	cpMkfifo(t, filepath.Join(dir, "hold/inner"))
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
}

func init() {
	registerCorpus("cp", cpCases)
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
		{"verbose-recursive", []string{"-v", "-r", "d1", "out"}},
		{"verbose-into-directory", []string{"-v", "f1", "f2", "d2"}},
		{"clustered", []string{"-rp", "d1", "out"}},
		{"clustered-upper", []string{"-Rp", "d1", "out"}},
		{"clustered-verbose", []string{"-vrp", "d1", "out"}},
		{"dashdash", []string{"--", "f1", "out"}},
		{"link", []string{"-l", "f1", "out"}},
		{"link-recursive", []string{"-l", "-r", "d1", "out"}},
		{"symbolic-link", []string{"-s", "f1", "out"}},
		{"symbolic-link-recursive", []string{"-s", "-r", "d1", "out"}},
		{"attributes-only-new", []string{"--attributes-only", "f1", "out"}},
		{"attributes-only-existing", []string{"--attributes-only", "f1", "f2"}},
		{"remove-destination", []string{"--remove-destination", "f1", "f2"}},
		{"one-file-system", []string{"-x", "-r", "d1", "out"}},
		{"selinux-Z", []string{"-Z", "f1", "out"}},
		{"selinux-context", []string{"--context", "f1", "out"}},
		{"selinux-context-value", []string{"--context=x", "f1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
	}

	// Copying a directory into itself: reported, and still copied.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"self-empty", []string{"-r", "empty", "empty"}},
		{"self-full", []string{"-r", "full", "full"}},
		{"self-taken", []string{"-r", "taken", "taken"}},
		{"self-verbose", []string{"-rv", "full", "full"}},
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
		{"fifo-in-tree-verbose", []string{"-rv", "hold", "out"}},
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
		{"strip-directory-verbose", []string{"--strip-trailing-slashes", "-vr", "d1/", "dst"}},
		{"strip-file-direct", []string{"--strip-trailing-slashes", "ff/", "out"}},
		{"strip-file-into", []string{"--strip-trailing-slashes", "ff/", "dst"}},
		{"strip-target-directory", []string{"--strip-trailing-slashes", "-t", "dst", "ff/"}},
		{"no-strip-target-directory", []string{"-t", "dst", "ff/"}},
		{"slash-missing-source", []string{"-r", "nosuch/", "dst"}},
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
		{"reflink-always-recursive", []string{"--reflink=always", "-r", "d1", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: cpBasic})
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
