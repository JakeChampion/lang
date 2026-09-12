package coreutils

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func init() {
	registerCorpus("du", duCases)
}

// duWrite creates a file of `n` bytes and flushes it to the filesystem.
//
// The flush is not decoration. du reports `st_blocks`, and ext4's delayed
// allocation leaves a freshly written file at zero blocks until writeback:
// GNU and Fern run seconds apart over one tree, so a file that crossed
// writeback between them would report two different sizes and fail a case
// that has nothing to do with either implementation.
func duWrite(t *testing.T, path string, n int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		if _, err := f.Write(make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// duSparse creates a file whose SIZE is n and whose allocation is nothing,
// for the `--apparent-size` / `-h` scaling table: the numbers there are the
// byte count, so they are the same on every filesystem.
func duSparse(t *testing.T, path string, n int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(n); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func duMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func duLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func duRaw(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// duStamp pins a path's mtime AND its atime. Both, because `--time=atime`
// is in the corpus and a directory du walks would otherwise have its atime
// moved by the first side's own readdir: the two stamps here are in 2030, so
// relatime — which updates only when atime is older than mtime or older than
// a day — never fires.
func duStamp(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// duTree builds the one tree every case reads, in the harness's own temp
// directory, and hands back its path. Both implementations are pointed at
// the SAME tree rather than one each: du reports allocation, and two trees
// built identically can still differ in what the filesystem gave them.
func duTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	j := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }

	// `a` — the general tree: a subdirectory, an empty file, a one-byte
	// file, a file past one block, and a symlink to the subdirectory that
	// -L expands and -P does not.
	duMkdir(t, j("a"))
	duMkdir(t, j("a", "sub"))
	duWrite(t, j("a", "sub", "g"), 0)
	duWrite(t, j("a", "f1"), 1)
	duWrite(t, j("a", "f2"), 5000)
	duLink(t, "sub", j("a", "dirlink"))

	// `b` — two names for one inode, which du counts once and `-l` twice.
	duMkdir(t, j("b"))
	duWrite(t, j("b", "file"), 3)
	if err := os.Link(j("b", "file"), j("b", "hardlink")); err != nil {
		t.Fatal(err)
	}

	duMkdir(t, j("empty"))

	// `d` — pinned timestamps for --time. The stamps are in 2030 so that
	// walking the tree cannot move an atime (see duStamp).
	newer := time.Date(2030, 5, 6, 7, 8, 9, 0, time.UTC)
	older := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	duMkdir(t, j("d"))
	duMkdir(t, j("d", "sub"))
	duWrite(t, j("d", "sub", "new"), 0)
	duWrite(t, j("d", "old"), 0)
	duStamp(t, j("d", "old"), older)
	duStamp(t, j("d", "sub", "new"), newer)
	duStamp(t, j("d", "sub"), newer)
	duStamp(t, j("d"), newer)

	// The symlink family: to a directory, to a file, to nothing, and the
	// two loops -L has to survive.
	duLink(t, "a", j("slink"))
	duLink(t, "a/f1", j("flink"))
	duLink(t, "nowhere", j("dangling"))
	duMkdir(t, j("cyc"))
	duLink(t, ".", j("cyc", "self"))
	duLink(t, "../cyc", j("cyc", "up2"))
	// The other shape of the same loop: the link is a level down and
	// points at its grandparent, so the cycle is found on the way back up
	// rather than immediately.
	duMkdir(t, j("loop2"))
	duMkdir(t, j("loop2", "inner"))
	duLink(t, "../../loop2", j("loop2", "inner", "back"))
	duMkdir(t, j("sl"))
	duWrite(t, j("sl", "real"), 0)
	duLink(t, "nowhere", j("sl", "dang"))
	duLink(t, "loopb", j("sl", "loopa"))
	duLink(t, "loopa", j("sl", "loopb"))

	// The human-readable scaling table, as sparse files: `-h` and `--si`
	// read the byte count under --apparent-size, so these numbers are the
	// same wherever the suite runs. The set is the rounding boundaries —
	// just under and just over each scale, and the value that rounds up
	// into the next one (1048575 is `1.0M`, not `1024K`).
	duMkdir(t, j("hr"))
	for _, n := range []int64{0, 1, 999, 1000, 1023, 1024, 1025, 1536, 1587, 9999,
		10240, 10241, 102400, 1023999, 1048575, 1048576, 1073741824, 1128481882} {
		duSparse(t, j("hr", "f_"+strconv.FormatInt(n, 10)), n)
	}

	// A tree deeper than any --max-depth in the corpus.
	deep := j("deep")
	duMkdir(t, deep)
	for _, part := range []string{"l1", "l2", "l3"} {
		deep = filepath.Join(deep, part)
		duMkdir(t, deep)
		duWrite(t, filepath.Join(deep, "f"), 1)
	}

	// Names a diagnostic has to quote: a space, an apostrophe, a tab, and
	// bytes that are not UTF-8 at all.
	duMkdir(t, j("weird dir"))
	duWrite(t, j("weird dir", "x"), 1)
	duMkdir(t, j("dir\xffx"))
	duWrite(t, j("dir\xffx", "inner\xfe"), 1)

	// A FIFO, which du counts and does not open.
	if err := syscall.Mkfifo(j("fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --files0-from lists: two names, no trailing NUL, empty, an empty
	// NAME mid-list, a `-` name, a missing name, and a trailing slash.
	duRaw(t, j("f0"), "a\x00b\x00")
	duRaw(t, j("f0b"), "a\x00b")
	duRaw(t, j("fempty"), "")
	duRaw(t, j("fnul"), "a\x00\x00b\x00")
	duRaw(t, j("fdash"), "-\x00")
	duRaw(t, j("fmiss"), "nosuch\x00a\x00")
	duRaw(t, j("fsl"), "a/\x00empty\x00")

	// -X pattern files, including the three the format decides: a
	// carriage return is stripped, an embedded NUL truncates, and a final
	// line without a newline still counts.
	duRaw(t, j("y1"), "f1\n")
	duRaw(t, j("y2"), "f2\n")
	duRaw(t, j("y3"), "sub\ng\n")
	duRaw(t, j("x1"), "f2\r\n")
	duRaw(t, j("x2"), "\n\nf1\n")
	duRaw(t, j("x3"), "  f2  \n")
	duRaw(t, j("x4"), "f1\x00f2\n")
	duRaw(t, j("x5"), "f2")
	duRaw(t, j("x6"), "\rf2\n")
	return dir
}

// duCases is du(1)'s corpus.
//
// Two things shape it. The first is that du reports ALLOCATION — `st_blocks
// * 512` — which is the filesystem's answer and not the file's, so both
// sides are pointed at ONE tree built by duTree rather than a copy each, and
// the cases that pin exact arithmetic (`-h`, `--si`, `-B`'s ceiling
// division) go through `--apparent-size`, where the number is the byte count
// and the same everywhere. The second is that the walk's ORDER is raw
// readdir order with directories expanded in place and reported post-order,
// so the tree's shape is itself under test: `a` holds a subdirectory, a
// symlink to it, and files either side.
//
// The option surface is large and almost every pair of options composes, so
// the corpus is mostly combinations: the unit family is last-wins across six
// spellings, `-b` splits in half under a later `-B`, `--inodes` silently
// overrides the numeric block size and keeps the human one, and the four
// symlink modes are one three-valued setting. The error paths get the same
// treatment — the SIZE diagnostics quote differently from every other one,
// the two suffix alphabets are not the same set, and the validation ORDER is
// observable (a -B fault beats the -a/-s conflict, which beats the -s/-d
// one, which beats the `extra operand`).
func duCases(t *testing.T) []invocation {
	dir := duTree(t)
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir})
	}
	env := func(name string, envs []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir, env: envs})
	}

	// --- the walk -----------------------------------------------------
	add("plain", "a")
	add("all", "-a", "a")
	add("summarize", "-s", "a")
	add("empty-dir", "empty")
	add("file-operand", "a/f1")
	add("all-file-operand", "-a", "a/f1")
	add("no-operands", "-c")
	add("no-operands-s", "-c", "-s")
	add("dot", "-s", ".")
	add("dotdot-relative", "-s", "a/sub/..")
	add("two-operands", "-c", "-s", "a", "b")
	add("separate-dirs", "-S", "a")
	add("separate-dirs-all", "-S", "-a", "a")
	add("separate-dirs-total", "-c", "-S", "a")
	add("total-depth0", "-c", "-d", "0", "a")
	add("deep", "-a", "deep")
	add("deep-d1", "-d", "1", "deep")
	add("deep-d2", "-d", "2", "deep")
	add("deep-a-d1", "-a", "-d1", "deep")
	add("fifo-and-devices", "-a", "fifo", "/dev/null", "/dev/zero")
	add("weird-name", "-a", "weird dir")
	add("non-utf8-name", "-a", "dir\xffx")
	add("non-utf8-missing", "no\xffsuch")
	add("quoting-apostrophe", "it's")
	add("quoting-double", "a'b")
	add("quoting-space", "no such")
	add("quoting-tab", "tab\tx")
	add("zero-length-operand", "")
	add("zero-length-then-operand", "", "a")
	add("operand-then-zero-length", "a", "")
	add("missing-operand-keeps-going", "-s", "nosuchfile", "a")
	add("missing-operand-total", "-c", "nosuchfile")

	// --- apparent size ------------------------------------------------
	add("bytes-all", "-b", "-a", "a")
	add("bytes-empty-dir", "-b", "empty")
	add("apparent-summarize", "--apparent-size", "-s", "a")
	add("apparent-all", "--apparent-size", "-a", "a")
	add("apparent-total", "--apparent-size", "-c", "-s", "a", "b")
	add("apparent-block-1", "--apparent-size", "--block-size=1", "-s", "a")
	add("bytes-deref-all", "-b", "-a", "-L", "a")

	// --- the unit family ----------------------------------------------
	add("block-K", "-s", "-BK", "a")
	add("block-1K", "-s", "-B1K", "a")
	add("block-KiB", "-s", "-BKiB", "a")
	add("block-KB", "-s", "-BKB", "a")
	add("block-hex", "-s", "-B0x10", "a")
	add("block-octal", "-s", "-B010", "a")
	add("block-3-ceiling", "-s", "-B3", "a")
	add("block-M", "-s", "-BM", "a")
	add("block-1KB", "-s", "-B1KB", "a")
	add("block-1KiB", "-s", "-B1KiB", "a")
	add("block-leading-blank", "-s", "-B 1K", "a")
	add("block-grouping-digit", "-s", "-B'1", "a")
	add("block-grouping-unit", "-s", "-B'K", "a")
	add("block-human-word", "-s", "-Bhuman-readable", "a")
	add("block-si-word", "-s", "-Bsi", "a")
	add("block-long-separate", "--block-size", "1K", "-s", "a")
	for _, s := range []string{"k", "K", "m", "M", "g", "G", "t", "T", "p", "P",
		"e", "E", "z", "Z", "y", "Y", "r", "R", "q", "Q", "b", "B", "c"} {
		add("block-bare-"+s, "-s", "-B", s, "a")
	}
	add("human", "-h", "-s", "a")
	add("si", "--si", "-s", "a")
	add("human-then-si", "-h", "--si", "-s", "a")
	add("si-then-human", "--si", "-h", "-s", "a")
	add("bytes-then-human", "-b", "-h", "-s", "a")
	add("bytes-then-block", "-b", "--block-size=1K", "-s", "a")
	add("block-then-bytes", "--block-size=1K", "-b", "-s", "a")
	add("k-then-m", "-k", "-m", "-s", "a")
	add("m-then-k", "-m", "-k", "-s", "a")
	add("human-then-block", "-h", "-B", "M", "-s", "a")
	add("block-then-human", "-BM", "-h", "-s", "a")
	add("k-summarize", "-k", "-s", "a")
	add("m-summarize", "-m", "-s", "a")
	add("human-total", "-h", "-c", "-s", "a", "b")
	add("si-total", "--si", "-c", "-s", "a", "b")
	// The scaling table: one line per rounding boundary, read off the
	// apparent size so the numbers do not depend on the filesystem.
	add("human-table", "-h", "--apparent-size", "-a", "hr")
	add("si-table", "--si", "--apparent-size", "-a", "hr")
	add("bytes-table", "-b", "-a", "hr")
	add("block-table", "-B", "K", "--apparent-size", "-a", "hr")

	// --- inodes -------------------------------------------------------
	add("inodes-all", "--inodes", "-a", "a")
	add("inodes-summarize", "--inodes", "-s", "d")
	add("inodes-human", "--inodes", "-h", "-s", "d")
	add("inodes-si", "--inodes", "--si", "-s", "d")
	add("inodes-block-ignored", "--inodes", "-B1K", "-s", "d")
	add("inodes-block-unit-suffix", "--inodes", "-BK", "-s", "d")
	add("inodes-block-iB-suffix", "--inodes", "-BKiB", "-s", "d")
	add("inodes-bytes-warning", "--inodes", "-b", "-s", "d")
	add("inodes-apparent-warning", "--inodes", "--apparent-size", "-s", "d")
	add("apparent-inodes-warning", "--apparent-size", "--inodes", "-s", "d")
	add("inodes-threshold", "--inodes", "-t", "3", "a")
	add("inodes-separate-total", "--inodes", "-S", "-c", "a")
	add("inodes-count-links", "--inodes", "-a", "-l", "b")
	add("inodes-total", "--inodes", "-c", "-s", "a", "b")
	add("inodes-exclude", "--inodes", "--exclude=f1", "-a", "a")

	// --- hard links and the seen set ----------------------------------
	add("hardlink-once", "-a", "b")
	add("hardlink-counted", "-a", "-l", "b")
	add("repeat-operand", "-s", "a", "a")
	add("repeat-operand-count-links", "-l", "-s", "a", "a")
	add("repeat-operand-total", "-c", "-s", "a", "a")
	add("same-file-twice", "-s", "b/file", "b/file")
	add("hardlink-across-operands", "-s", "a/f1", "b/hardlink")
	add("hardlink-across-operands-reversed", "-s", "b/hardlink", "a/f1")

	// --- symlinks -----------------------------------------------------
	add("symlink-operand", "-s", "slink")
	add("symlink-trailing-slash", "-s", "slink/")
	add("symlink-two-slashes", "-s", "slink//")
	add("symlink-slash-dot", "-s", "slink/.")
	add("symlink-slash-under-P", "-P", "-s", "slink/")
	add("dirlink-trailing-slash", "-a", "a/dirlink/")
	add("file-symlink-slash", "-s", "flink/")
	add("dangling-slash", "-s", "dangling/")
	add("dangling-plain", "-s", "dangling")
	add("dangling-deref", "-L", "dangling")
	add("dangling-deref-args", "-D", "dangling")
	add("missing-deref", "-L", "nosuchfile")
	add("deref-args-symlink", "-s", "-D", "slink")
	add("deref-all-symlink", "-s", "-L", "slink")
	add("deref-L-then-P", "-L", "-P", "-s", "slink")
	add("deref-P-then-L", "-P", "-L", "-s", "slink")
	add("deref-D-then-L", "-D", "-L", "-s", "slink")
	add("deref-L-then-D", "-L", "-D", "-s", "slink")
	add("deref-H-then-P", "-H", "-P", "-s", "slink")
	add("deref-P-then-D", "-P", "-D", "-s", "slink")
	add("deref-loop-stopped", "-L", "cyc")
	add("deref-loop-all", "-L", "-a", "cyc")
	add("deref-loop-summarize", "-L", "-s", "cyc")
	// -l turns the seen set off, so the cycle has to be stopped by the
	// path instead — the ancestor check fts does with FTS_DC, silently.
	add("count-links-loop", "-L", "-l", "cyc")
	add("count-links-loop-all", "-L", "-l", "-a", "cyc")
	add("count-links-loop-depth", "-L", "-l", "-a", "-d2", "cyc")
	add("count-links-loop-total", "-L", "-l", "-c", "-s", "cyc", "a")
	add("count-links-loop-inodes", "-L", "-l", "--inodes", "-a", "cyc")
	add("count-links-no-loop", "-l", "-a", "cyc")
	add("grandparent-loop", "-L", "-a", "loop2")
	add("grandparent-loop-count-links", "-L", "-l", "-a", "loop2")
	add("grandparent-loop-depth", "-L", "-l", "-a", "-d3", "loop2")
	add("deref-all-tree", "-a", "-L", "a")
	add("deref-args-tree", "-a", "-D", "a")
	add("deref-count-links-tree", "-l", "-a", "-L", "a")
	add("symlinks-plain", "-a", "sl")
	add("symlinks-deref", "-a", "-L", "sl")
	add("symlinks-deref-args", "-a", "-D", "sl")
	add("elooop-operand", "-s", "-L", "sl/loopa")
	add("eloop-operand-slash", "-s", "sl/loopa/")
	add("dirlink-deref", "-a", "-L", "a/dirlink")
	add("dirlink-plain", "-s", "a/dirlink")
	add("dirlink-slash", "-s", "a/dirlink/")

	// --- trailing slashes ---------------------------------------------
	add("operand-slash", "-s", "a/")
	add("operand-two-slashes", "-s", "a//")
	add("operand-two-slashes-all", "-a", "a//")
	add("operand-dot-slash", "-s", "a/./")
	add("empty-dir-slash", "-s", "empty/")
	add("subdir-slash", "-a", "a/sub/")
	add("file-operand-slash", "-a", "a/f1/")

	// --- depth --------------------------------------------------------
	add("depth-0", "-d", "0", "a")
	add("depth-1-all", "-a", "-d1", "a")
	add("depth-negative", "-d", "-1", "a")
	add("depth-hex", "-d", "0x2", "a")
	add("depth-octal", "-d", "010", "deep")
	add("depth-leading-blank", "-d", " 1", "a")
	add("depth-plus", "-d", "+1", "a")
	add("depth-trailing-blank", "-d", "1 ", "a")
	add("depth-glued-junk", "-d1a", "a")
	add("depth-overflow", "-d", "99999999999999999999999", "a")
	add("depth-quoted-arg", "-d", "a'b", "a")
	add("depth-tab-arg", "-d", "tab\tx", "a")
	add("depth-backslash-arg", "-d", "x\\y", "a")
	add("summarize-depth-0", "-s", "-d", "0", "a")
	add("summarize-depth-0-glued", "-s", "-d0", "a")
	add("summarize-depth-2", "-s", "-d", "2", "a")
	add("summarize-depth-negative", "-s", "-d", "-1", "a")

	// --- threshold ----------------------------------------------------
	add("threshold-8", "-t", "8", "a")
	add("threshold-8K", "-t", "8K", "a")
	add("threshold-negative", "-t", "-8K", "-a", "a")
	add("threshold-apparent", "-t", "1", "--apparent-size", "a")
	add("threshold-total-unfiltered", "-c", "-t", "1M", "a")
	add("threshold-zero", "-s", "-t", "0", "a")
	add("threshold-minus-zero", "-s", "-t", "-0", "a")
	add("threshold-minus-zeros", "-s", "-t", "-000", "a")
	add("threshold-minus-zero-long", "-s", "--threshold=-0x0", "a")
	add("threshold-human", "-h", "-t", "8", "a")
	add("threshold-minus-one", "-a", "-t", "-1", "a")
	add("threshold-0-all", "-a", "-t", "0", "a")
	add("threshold-5000", "-s", "-t", "5000", "-a", "a")
	add("threshold-long-too-large", "--threshold=1R", "-s", "a")
	for _, s := range []string{"k", "K", "m", "M", "g", "G", "t", "T", "p", "P",
		"e", "E", "z", "Z", "y", "Y", "r", "R", "q", "Q", "b", "B", "c"} {
		add("threshold-bare-"+s, "-s", "-t", s, "a")
	}
	add("threshold-1g", "-s", "-t", "1g", "a")
	add("threshold-1G", "-s", "-t", "1G", "a")
	add("threshold-1r", "-s", "-t", "1r", "a")
	add("threshold-1e", "-s", "-t", "1e", "a")
	add("threshold-1b", "-s", "-t", "1b", "a")
	add("threshold-1kB", "-s", "-t", "1kB", "a")
	add("threshold-1KiB", "-s", "-t", "1KiB", "a")
	add("threshold-hex", "-s", "-t", "0x10", "a")
	add("threshold-octal", "-s", "-t", "010", "a")
	add("threshold-leading-blank", "-s", "-t", " 1", "a")
	add("threshold-trailing-blank", "-s", "-t", "1 ", "a")
	add("threshold-plus", "-s", "-t", "+1", "a")
	add("threshold-grouping", "-s", "-t", "'1", "a")
	add("threshold-human-word", "-s", "-t", "human-readable", "a")
	add("threshold-huge", "-s", "-t", "99999999999999999999", "a")
	add("threshold-huge-negative", "-s", "-t", "-99999999999999999999", "a")
	add("threshold-intmax", "-s", "-t", "9223372036854775807", "a")
	add("threshold-quoted-arg", "-s", "-t", "a'b", "a")
	add("threshold-tab-arg", "-s", "-t", "tab\tx", "a")
	add("threshold-backslash-arg", "-s", "-t", "x\\y", "a")
	add("block-quoted-arg", "-s", "-B", "a'b", "a")

	// --- the SIZE error paths -----------------------------------------
	add("block-invalid", "--block-size", "a", "a")
	add("block-long-suffix", "--block-size=1b", "a")
	add("block-zero", "-s", "-B0", "a")
	add("block-zero-octal", "-s", "-B00", "a")
	add("block-zero-hex", "-s", "-B0x0", "a")
	add("block-blank-unit", "-s", "-B K", "a")
	add("block-trailing-blank", "-s", "-B1K ", "a")
	add("block-suffix-1b", "-s", "-B1b", "a")
	add("block-suffix-1e", "-s", "-B1e", "a")
	add("block-suffix-1R", "-s", "-B1R", "a")
	add("block-suffix-1p", "-s", "-B1p", "a")
	add("block-suffix-1B", "-s", "-B1B", "a")
	add("block-suffix-1iB", "-s", "-B1iB", "a")
	add("block-bare-iB", "-s", "-BiB", "a")
	add("block-too-large-1Z", "-s", "-B1Z", "a")
	add("block-too-large-1Y", "-s", "-B1Y", "a")
	add("block-negative", "-s", "-B", "-1", "a")
	add("block-negative-glued", "-s", "-B-1", "a")
	add("block-huge", "-s", "-B", "99999999999999999999999", "a")
	add("block-1E", "-s", "-B1E", "a")
	add("block-1P", "-s", "-B1P", "a")

	// --- exclusion ----------------------------------------------------
	add("exclude-file", "--exclude=f2", "-a", "a")
	add("exclude-total", "-c", "--exclude=f2", "a")
	add("exclude-dir", "--exclude=sub", "-a", "a")
	add("exclude-full-path", "--exclude=a/sub", "-a", "a")
	add("exclude-star-path", "--exclude=*/sub", "-a", "a")
	add("exclude-operand", "--exclude=a", "a")
	add("exclude-empty-pattern", "--exclude=", "-a", "a")
	add("exclude-twice", "--exclude=f1", "--exclude=f2", "-a", "a")
	add("exclude-question", "--exclude=?1", "-a", "a")
	add("exclude-class", "--exclude=[fg]1", "-a", "a")
	add("exclude-star-crosses-slash", "--exclude=a*1", "-a", "a")
	add("exclude-suffix", "--exclude=g", "-a", "a")
	add("exclude-not-a-suffix", "--exclude=ub", "-a", "a")
	add("exclude-star-suffix", "--exclude=*g", "-a", "a")
	add("exclude-deep-full-path", "--exclude=a/sub/g", "-a", "a")
	add("exclude-leading-slash", "--exclude=/sub", "-a", "a")
	add("exclude-inner-suffix", "-a", "--exclude=sub/g", "a")
	add("exclude-negated-class", "--exclude=[!a-e]1", "-a", "a")
	add("exclude-escape", "--exclude=f\\1", "-a", "a")
	add("exclude-operand-slash", "--exclude=empty/", "empty/")
	add("exclude-operand-no-slash", "--exclude=empty", "empty/")
	add("exclude-operand-slash-all", "--exclude=a/", "-a", "a/")
	add("exclude-under-slash-operand", "--exclude=sub", "-a", "a/")
	add("exclude-time", "--exclude=old", "--time", "-s", "d")
	add("exclude-from", "-X", "y1", "-a", "a")
	add("exclude-from-twice", "-X", "y1", "-X", "y2", "-a", "a")
	add("exclude-from-and-exclude", "-X", "y1", "--exclude=f2", "-a", "a")
	add("exclude-from-two-lines", "-X", "y3", "-a", "a")
	add("exclude-from-cr", "-X", "x1", "-a", "a")
	add("exclude-from-blank-lines", "-X", "x2", "-a", "a")
	add("exclude-from-spaces", "-X", "x3", "-a", "a")
	add("exclude-from-nul", "-X", "x4", "-a", "a")
	add("exclude-from-no-newline", "-X", "x5", "-a", "a")
	add("exclude-from-leading-cr", "-X", "x6", "-a", "a")
	add("exclude-from-empty", "-X", "/dev/null", "-a", "a")
	add("exclude-from-missing", "-X", "/nonexistent", "a")
	add("exclude-from-quoted-missing", "-X", "/nonexistent dir/nope", "a")
	cases = append(cases, invocation{
		name: "exclude-from-stdin", args: []string{"-X", "-", "-a", "a"},
		dir: dir, stdin: "f1\n",
	})

	// --- --files0-from -------------------------------------------------
	add("files0", "--files0-from=f0")
	add("files0-separate-arg", "--files0-from", "f0")
	add("files0-no-trailing-nul", "--files0-from=f0b")
	add("files0-empty", "--files0-from=fempty")
	add("files0-empty-total", "-c", "--files0-from=fempty")
	add("files0-with-operand", "--files0-from=f0", "a")
	add("files0-with-quoted-operand", "--files0-from=f0", "a'b")
	add("files0-missing-file", "--files0-from=nosuchfile")
	add("files0-directory", "--files0-from=a")
	add("files0-dash-name-in-file", "--files0-from=fdash")
	add("files0-zero-length-name", "--files0-from=fnul")
	add("files0-missing-name", "--files0-from=fmiss", "-s", "-c")
	add("files0-trailing-slash", "--files0-from=fsl", "-s")
	add("files0-exclude", "--exclude=f1", "--files0-from=f0")
	add("files0-summarize", "-s", "--files0-from=f0")
	add("files0-depth0-summarize", "-d", "0", "-s", "--files0-from=f0")
	cases = append(cases, invocation{
		name: "files0-stdin", args: []string{"--files0-from=-"},
		dir: dir, stdin: "a\x00b\x00",
	})
	cases = append(cases, invocation{
		name: "files0-stdin-dash-name", args: []string{"--files0-from=-"},
		dir: dir, stdin: "a\x00-\x00b\x00",
	})
	cases = append(cases, invocation{
		name: "files0-stdin-zero-length", args: []string{"--files0-from=-"},
		dir: dir, stdin: "a\x00\x00b\x00",
	})
	// -X reads stdin first, so the name list gets nothing.
	cases = append(cases, invocation{
		name: "exclude-from-and-files0-both-stdin",
		args: []string{"-X", "-", "--files0-from=-"}, dir: dir, stdin: "f1\n",
	})

	// --- --time ---------------------------------------------------------
	add("time", "--time", "-s", "d")
	add("time-all", "--time", "-a", "d")
	add("time-separate-dirs", "--time", "-S", "d")
	add("time-depth1", "--time", "-d", "1", "d")
	add("time-total", "-c", "--time", "-s", "d")
	add("time-null", "-0", "--time", "-s", "d")
	add("time-total-separate", "--time", "-c", "-S", "a")
	add("time-atime", "--time=atime", "-s", "d")
	add("time-a", "--time=a", "-s", "d")
	add("time-ac", "--time=ac", "-s", "d")
	add("time-use", "--time=u", "-s", "d")
	add("time-access", "--time=access", "-s", "d")
	add("time-use-word", "--time=use", "-s", "d")
	add("time-ctime", "--time=c", "-s", "d")
	add("time-status", "--time=st", "-s", "d")
	add("time-status-word", "--time=status", "-s", "d")
	add("time-mtime-refused", "--time=mtime", "-s", "d")
	add("time-modif-refused", "--time=modif", "-s", "d")
	add("time-empty-value", "--time=", "-s", "d")
	add("time-upper-A", "--time=A", "-s", "d")
	add("time-quoted-value", "--time=a'b", "-s", "d")
	add("time-newline-value", "--time=x\ny", "-s", "d")
	add("time-optional-arg-not-consumed", "--time", "atime", "-s")

	// --- --time in a zone that is not UTC ---------------------------------
	//
	// The harness pins TZ=UTC (baseEnv), so every case above is exact
	// whatever this utility does with a zone — which is how `du --time`
	// shipped rendering UTC where GNU calls localtime_r (#9076). These
	// cases pass TZ per-case so the difference is visible: a fixed-offset
	// POSIX rule, a negative one, one with a DST rule that the pinned 2030
	// stamps fall either side of, a named zone that resolves under TZDIR,
	// and the spellings tzset(3) accepts for "no zone at all".
	env("tz-fixed-east", []string{"TZ=XXX-5"}, "--time", "-s", "d")
	env("tz-fixed-west", []string{"TZ=YYY8"}, "--time", "-s", "d")
	env("tz-half-hour", []string{"TZ=ZZZ-5:30"}, "--time", "-s", "d")
	env("tz-angle-name", []string{"TZ=<-03>3"}, "--time", "-s", "d")
	env("tz-dst-rule", []string{"TZ=EST5EDT,M3.2.0/2,M11.1.0/2"}, "--time", "-s", "d")
	env("tz-dst-rule-atime", []string{"TZ=EST5EDT,M3.2.0/2,M11.1.0/2"}, "--time=atime", "-s", "d")
	env("tz-named-zone", []string{"TZ=America/New_York"}, "--time", "-s", "d")
	env("tz-named-colon", []string{"TZ=:America/New_York"}, "--time", "-s", "d")
	env("tz-named-utc", []string{"TZ=UTC0"}, "--time", "-s", "d")
	env("tz-empty", []string{"TZ="}, "--time", "-s", "d")
	env("tz-bogus", []string{"TZ=Not/AZone"}, "--time", "-s", "d")
	// The two conversions that read the zone off the same lookup the
	// broken-down fields came from, rather than off a second one.
	env("tz-zone-name", []string{"TZ=EST5EDT,M3.2.0/2,M11.1.0/2"},
		"--time-style=+%Z|%z|%:z", "--time", "-s", "d")
	env("tz-full-iso", []string{"TZ=EST5EDT,M3.2.0/2,M11.1.0/2"},
		"--time-style=full-iso", "--time", "-s", "d")
	// %s is the epoch second, which a zone must NOT move.
	env("tz-epoch-unmoved", []string{"TZ=XXX-5"}, "--time-style=+%s", "--time", "-s", "d")

	// --- --time-style ---------------------------------------------------
	add("style-full-iso", "--time-style=full-iso", "--time", "-s", "d")
	add("style-iso", "--time-style=iso", "--time", "-s", "d")
	add("style-long-iso", "--time-style=long-iso", "--time", "-s", "d")
	add("style-prefix-ful", "--time-style=ful", "--time", "-s", "d")
	add("style-prefix-l", "--time-style=l", "--time", "-s", "d")
	add("style-loc-refused", "--time-style=loc", "--time", "-s", "d")
	add("style-iso-8601-refused", "--time-style=iso-8601", "--time", "-s", "d")
	add("style-lazy", "--time-style=bogus", "a")
	add("style-bogus", "--time", "--time-style=bogus", "a")
	add("style-empty", "--time-style=", "--time", "-s", "d")
	add("style-quoted-value", "--time-style=a'b", "--time", "-s", "d")
	add("style-format-date", "--time", "--time-style=+%Y-%m-%d", "-s", "d")
	add("style-format-empty", "--time", "--time-style=+", "-s", "d")
	add("style-format-zone", "--time-style=+%Z|%z", "--time", "-s", "d")
	add("style-format-epoch", "--time-style=+%s", "--time", "-s", "d")
	add("style-format-c", "--time-style=+%c", "--time", "-s", "d")
	add("style-format-xX", "--time-style=+%x!%X", "--time", "-s", "d")
	add("style-format-DFTRr", "--time-style=+%D!%F!%T!%R!%r", "--time", "-s", "d")
	add("style-format-names", "--time-style=+%a!%A!%b!%B!%h", "--time", "-s", "d")
	add("style-format-weeks", "--time-style=+%j!%u!%w!%U!%W!%V!%G!%g", "--time", "-s", "d")
	add("style-format-numbers", "--time-style=+%C!%y!%Y!%m!%d!%e!%H!%I!%k!%l!%M!%S", "--time", "-s", "d")
	add("style-format-pPqN", "--time-style=+%p!%P!%q!%N", "--time", "-s", "d")
	add("style-format-flags", "--time-style=+%-d/%_d/%03d/%^a/%#b", "--time", "-s", "d")
	add("style-format-width", "--time-style=+%10Y!%-j!%_j", "--time", "-s", "d")
	add("style-format-unknown", "--time-style=+%Q%v%%", "--time", "-s", "d")
	add("style-format-newline", "--time-style=+line1%nline2%tx", "--time", "-s", "d")
	add("style-format-trailing-percent", "--time-style=+x%", "--time", "-s", "d")

	// --- -0 --------------------------------------------------------------
	add("null-total", "-0", "-c", "-s", "a", "b")
	add("null-error", "-0", "-s", "nosuchfile", "a")
	add("null-long", "--null", "-c", "-s", "a", "b")

	// --- -x ----------------------------------------------------------------
	add("one-file-system-no-crossing", "-x", "-s", "a")
	add("one-file-system-all", "-x", "-a", "a")
	add("one-file-system-long", "--one-file-system", "-s", "a")
	// /sys/fs is sysfs with a tmpfs mounted at cgroup, so -x is what
	// decides whether that line appears — the only crossing a test can
	// reach without mounting something. Every entry it walks is zero
	// blocks and none of them come and go, which is what makes it stable;
	// on a host without /sys both sides report the same failure. The same
	// walk WITHOUT -x is deliberately absent: it descends into the cgroup
	// tree, where a directory appearing or vanishing between the two runs
	// is a diagnostic one side has and the other does not.
	add("one-file-system-crossing", "-x", "-d1", "/sys/fs")

	// --- the long option table -------------------------------------------
	add("ambiguous-t", "--t", "a")
	add("ambiguous-ti", "--ti", "a")
	add("ambiguous-ti-value", "--ti=atime", "a")
	add("ambiguous-s", "--s", "a")
	add("ambiguous-h", "--h", "a")
	add("ambiguous-n", "--n", "a")
	add("ambiguous-d", "--d", "a")
	add("ambiguous-b", "--b", "a")
	add("ambiguous-a", "--a", "a")
	add("ambiguous-e", "--e", "a")
	add("ambiguous-excl", "--excl", "a")
	add("unrecognized-p", "--p", "a")
	add("unrecognized-x", "--x", "a")
	add("unique-c", "--c", "a")
	add("unique-i", "--i", "a")
	add("unique-m", "--m", "a")
	add("unique-o", "--o", "a")
	add("help-with-value", "--help=x")
	add("version-with-value", "--version=x")
	add("flag-with-value", "--all=1", "a")
	add("digit-not-an-option", "-8", "a")
	add("digit-one-not-an-option", "-1", "a")
	add("long-spellings", "--all", "--total", "--separate-dirs", "a")
	add("long-count-links", "--count-links", "-a", "b")
	add("long-max-depth", "--max-depth=1", "-a", "a")
	add("long-deref", "--dereference", "-s", "slink")
	add("long-no-deref", "--no-dereference", "-s", "slink")
	add("long-deref-args", "--dereference-args", "-s", "slink")
	add("long-summarize", "--summarize", "a")
	add("missing-B-argument", "-B")
	add("missing-exclude-argument", "--exclude")
	add("missing-files0-argument", "--files0-from")
	add("missing-d-argument", "-d")
	add("missing-X-argument", "-X")
	add("dash-operand", "-")
	add("dashdash", "--", "a")
	add("dashdash-twice", "-s", "--", "--", "a")
	add("dashdash-option", "--", "-s")

	// --- validation order -------------------------------------------------
	add("all-and-summarize", "-a", "-s", "a")
	add("summarize-depth-then-all", "-s", "-d1", "-a", "a")
	add("conflict-beats-operand", "-a", "-s", "nosuch")
	add("block-fault-beats-conflict", "-a", "-s", "-B0", "a")
	add("block-fault-first", "-B0", "-a", "-s", "a")
	add("inodes-warning-after-conflict", "--inodes", "-b", "-a", "-s", "d")
	add("conflict-beats-files0", "-a", "-s", "--files0-from=f0", "a")
	add("files0-then-conflict", "--files0-from=f0", "-a", "-s", "a")
	add("depth-conflict-beats-files0", "-s", "-d1", "--files0-from=f0", "a")
	add("inodes-warning-before-extra-operand", "--inodes", "-b", "--files0-from=f0", "a")
	add("exclude-from-fault-beats-conflict", "-X", "/nonexistent", "-a", "-s", "a")
	add("time-value-beats-conflict", "--time=bogus", "-a", "-s", "a")
	add("conflict-beats-style", "-a", "-s", "--time-style=bogus", "--time", "a")
	add("conflict-beats-files0-open", "--files0-from=nosuch", "-a", "-s")
	add("style-beats-extra-operand", "--files0-from=f0", "--time", "--time-style=bogus", "a")
	add("depth-warning-then-style", "-s", "-d", "0", "--time", "--time-style=bogus", "a")
	add("inodes-warning-then-style", "--inodes", "-b", "--time", "--time-style=bogus", "-s", "a")
	add("exclude-does-not-conflict", "--exclude=nope", "-a", "-s", "a")

	// --- the environment ---------------------------------------------------
	env("env-block-size", []string{"BLOCK_SIZE=K"}, "-s", "a")
	env("env-du-block-size", []string{"DU_BLOCK_SIZE=1M"}, "-s", "a")
	env("env-blocksize", []string{"BLOCKSIZE=2048"}, "-s", "a")
	env("env-block-size-invalid", []string{"BLOCK_SIZE=bogus"}, "-s", "a")
	env("env-block-size-zero", []string{"BLOCK_SIZE=0"}, "-s", "a")
	env("env-block-size-human", []string{"BLOCK_SIZE=human-readable"}, "-s", "a")
	env("env-block-size-si", []string{"BLOCK_SIZE=si"}, "-s", "a")
	env("env-block-size-grouping", []string{"BLOCK_SIZE='1"}, "-s", "a")
	env("env-block-size-blank-unit", []string{"BLOCK_SIZE= K"}, "-s", "a")
	env("env-block-size-empty", []string{"BLOCK_SIZE="}, "-s", "a")
	env("env-du-block-size-empty", []string{"DU_BLOCK_SIZE="}, "-s", "a")
	env("env-ls-block-size-ignored", []string{"LS_BLOCK_SIZE=1M"}, "-s", "d")
	env("env-first-set-wins", []string{"DU_BLOCK_SIZE=bogus", "BLOCK_SIZE=K"}, "-s", "a")
	env("env-block-size-beats-blocksize", []string{"BLOCK_SIZE=1", "BLOCKSIZE=K"}, "-s", "a")
	env("env-option-beats-env", []string{"BLOCK_SIZE=K"}, "-B1K", "-s", "a")
	env("posixly-correct", []string{"POSIXLY_CORRECT=1"}, "-s", "a")
	env("posixly-correct-empty", []string{"POSIXLY_CORRECT="}, "-s", "a")
	env("posixly-correct-no-permute", []string{"POSIXLY_CORRECT=1"}, "a", "-s")
	env("posixly-correct-conflict", []string{"POSIXLY_CORRECT=1"}, "-s", "-a", "a")
	env("posixly-correct-and-block-size", []string{"POSIXLY_CORRECT=1", "BLOCK_SIZE=K"}, "-s", "a")
	add("permuted-operand-first", "a", "-s")
	env("env-time-style", []string{"TIME_STYLE=full-iso"}, "--time", "-s", "d")
	env("env-time-style-posix", []string{"TIME_STYLE=posix-full-iso"}, "--time", "-s", "d")
	env("env-time-style-posix-prefix", []string{"TIME_STYLE=posix-l"}, "--time", "-s", "d")
	env("env-time-style-locale", []string{"TIME_STYLE=locale"}, "--time", "-s", "d")
	env("env-time-style-posix-locale", []string{"TIME_STYLE=posix-locale"}, "--time", "-s", "d")
	env("env-time-style-empty", []string{"TIME_STYLE="}, "--time", "-s", "d")
	env("env-time-style-lazy", []string{"TIME_STYLE=bogus"}, "-s", "d")
	env("env-time-style-bogus", []string{"TIME_STYLE=bogus"}, "--time", "-s", "d")
	env("env-time-style-format", []string{"TIME_STYLE=+%Y"}, "--time", "-s", "d")
	env("env-time-style-loses-to-option", []string{"TIME_STYLE=iso"}, "--time", "--time-style=long-iso", "-s", "d")
	env("env-quoting-style-ignored", []string{"QUOTING_STYLE=c"}, "nosuch")

	// --- write failures ----------------------------------------------------
	cases = append(cases, invocation{
		name: "write-error-full", args: []string{"-s", "a"}, dir: dir, stdout: stdoutFull,
	})
	cases = append(cases, invocation{
		name: "write-error-full-large", args: []string{"-a", "deep", "hr", "a"}, dir: dir, stdout: stdoutFull,
	})
	cases = append(cases, invocation{
		name: "write-error-closed", args: []string{"-s", "a"}, dir: dir, stdout: stdoutClosed,
	})
	cases = append(cases, invocation{
		name: "write-error-closed-no-output", args: []string{"--exclude=a", "-s", "a"}, dir: dir, stdout: stdoutClosed,
	})
	return cases
}

func TestDu(t *testing.T) {
	requireParity(t, "du", duCases(t))
}

func TestDuHelp(t *testing.T) {
	requireHelp(t, "du", []string{"--help"}, 0)
	requireHelp(t, "du", []string{"a", "--help"}, 0)
}

func TestDuVersion(t *testing.T) {
	requireVersion(t, "du", []string{"--version"}, 0)
	requireVersion(t, "du", []string{"--vers"}, 0)
}
