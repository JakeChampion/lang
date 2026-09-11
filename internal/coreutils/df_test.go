package coreutils

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func init() {
	registerCorpus("df", dfCases)
}

// df's answer is the machine's LIVE state, and almost none of it is
// comparable between two processes. The GNU leg and the Fern leg run
// seconds apart over one machine; anything that moved in between is a
// diff that says nothing about either implementation. What this corpus
// can hold, and why, is the whole design of the file.
//
// NOT here, deliberately:
//
//   - `df` and `df -a` with no operands, and every `-t` / `-x` naming a
//     type that real volumes use. Their answer is "whatever happens to
//     be mounted", and the free space of a mounted volume moves under
//     any writer on the box — this container's own `/` moved by several
//     megabytes between consecutive probe runs. A case like that fails
//     for reasons that have nothing to do with df. The reasoning is
//     `scripts/coreutils-bench.d/_utmp.sh`'s, applied to space rather
//     than to logins.
//   - The `Used`, `Available`, `Use%`, `IUsed`, `IFree` and `IUse%`
//     columns of any filesystem that can be written to, for the same
//     reason. A mask by position would hide exactly the digits the
//     column exists for AND the width they give it, so `--output=`
//     selecting the stable columns is used instead: it leaves the
//     alignment, the header and the value under byte comparison and
//     drops only the fields that move.
//
// What IS here:
//
//   - Operands that land on a filesystem reporting NO BLOCKS AT ALL —
//     procfs, sysfs, devpts. Those counts are zero by construction
//     rather than by luck, so a complete row of theirs is stable: every
//     layout, every header, the `-` that a zero denominator prints in
//     `Use%`, and the whole `--output` field set are byte-exact against
//     GNU there.
//   - The SIZE of the filesystem the harness's temp directory is on,
//     which is fixed for the life of a mount and is the only real
//     number df prints that does not move. That is what exercises the
//     block-size division and the human-readable rounding against a
//     number with digits in it. (An allocate-on-demand filesystem whose
//     total grows — btrfs — could still move it. Every CI runner and
//     this container are ext4.)
//   - Every option parse, every diagnostic, and every operand that
//     fails to resolve: those touch no filesystem state at all.
//
// Four things are outside what any corpus here can reach, and are
// checked by hand against the reference on a machine with the mounts
// arranged for them rather than pretended at with a case:
//
//   - `--sync`. Its cases prove the option is accepted and changes no
//     output; that nothing was flushed is invisible here and is a real
//     divergence (#9102).
//   - Which mount of a device `df` keeps when two claim one, and which
//     it marks `-` under `-a`. Arranging that needs two mounts of one
//     filesystem, or a mount over another, which needs privileges this
//     suite does not assume.
//   - The pseudo-filesystem type list, past the members that report no
//     blocks and are covered by the blocks rule either way. `devtmpfs`
//     and `squashfs` are the two that report a size and are still
//     dropped, and neither can be mounted from a test.
//   - `-l`'s idea of remote. A source with a host before a colon is
//     confirmed against a tmpfs mounted from `host:/export`; the
//     `//server/share` half needs an SMB type to go with it.

// dfTree makes the file and directory the size cases are asked about,
// under the harness's own temp directory, and hands back its path.
func dfTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(dir, "slink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	// A link out of the temp directory's filesystem entirely: the mount an
	// operand lands on is decided after the name is resolved, and nothing
	// inside one filesystem can tell that apart from deciding it before.
	if err := os.Symlink("/proc", filepath.Join(dir, "procl")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func dfCases(t *testing.T) []invocation {
	t.Helper()
	dir := dfTree(t)
	file := filepath.Join(dir, "f")
	sub := filepath.Join(dir, "sub")

	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}
	env := func(name string, vars []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, env: vars})
	}
	// size names the stable columns of a real filesystem: everything
	// except the four that move.
	const stable = "--output=source,fstype,size,file,target"

	// --- the layouts, over a filesystem whose every count is zero -----------
	add("proc", "/proc")
	add("proc-human", "-h", "/proc")
	add("proc-si", "-H", "/proc")
	add("proc-si-long", "--si", "/proc")
	add("proc-kilo", "-k", "/proc")
	add("proc-inodes", "-i", "/proc")
	add("proc-print-type", "-T", "/proc")
	add("proc-portability", "-P", "/proc")
	add("proc-portability-type", "-P", "-T", "/proc")
	add("proc-portability-human", "-P", "-h", "/proc")
	add("proc-portability-si", "-P", "-H", "/proc")
	add("proc-inodes-type", "-i", "-T", "/proc")
	add("proc-inodes-portability", "-i", "-P", "/proc")
	add("proc-inodes-portability-type", "-i", "-P", "-T", "/proc")
	add("proc-inodes-human", "-h", "-i", "/proc")
	add("proc-total", "--total", "/proc")
	add("proc-total-type", "--total", "-T", "/proc", "/sys")
	add("proc-total-portability", "-P", "--total", "/proc")
	add("proc-all", "-a", "/proc")
	add("proc-local", "-l", "/proc")
	add("proc-verbose", "-v", "/proc")
	add("proc-verbose-twice", "-v", "-v", "/proc")
	add("sys", "/sys")
	add("devpts", "/dev/pts")
	add("proc-file", "/proc/self/status")
	add("proc-dir", "/proc/1")
	add("proc-trailing-slash", "/proc/")
	add("proc-many-slashes", "/proc///")
	add("two-operands", "/proc", "/sys")
	add("repeated-operand", "/proc", "/proc")
	add("repeated-operand-mixed", "/proc", "/sys", "/proc")
	add("dash-dash-operand", "--", "/proc")

	// --- --output ------------------------------------------------------------
	add("output-all", "--output", "/proc")
	add("output-all-two", "--output", "/proc", "/sys")
	add("output-source", "--output=source", "/proc")
	add("output-target", "--output=target", "/proc")
	add("output-fstype", "--output=fstype", "/proc")
	add("output-file", "--output=file", "/proc")
	add("output-pcent", "--output=pcent", "/proc")
	add("output-ipcent", "--output=ipcent", "/proc")
	add("output-itotal", "--output=itotal", "/proc")
	add("output-size", "--output=size", "/proc")
	add("output-order-reversed", "--output=target,source", "/proc")
	add("output-every-numeric", "--output=itotal,iused,iavail,ipcent,size,used,avail,pcent", "/proc")
	add("output-file-and-target", "--output=file,target", "/proc", "/sys")
	add("output-min-widths", "--output=used,size", "/proc")
	add("output-min-widths-human", "-h", "--output=used,size", "/proc")
	add("output-avail-header", "--output=avail,size", "/proc")
	add("output-human-size", "-h", "--output=size", "/proc")
	add("output-total-label-in-source", "--total", "--output=source,file", "/proc")
	add("output-total-label-in-target", "--total", "--output=size,target", "/proc")
	add("output-total-no-label", "--total", "--output=fstype,size", "/proc")
	add("output-total-file", "--total", "--output=file,size", "/proc")
	add("output-accumulates", "--output=size", "--output=used", "/proc")
	add("output-bare-then-list", "--output", "--output=size", "/proc")
	add("output-list-then-bare", "--output=size", "--output", "/proc")
	add("output-empty-field-list", "--output=", "/proc")
	add("output-unknown-field", "--output=nope", "/proc")
	add("output-field-case", "--output=Size", "/proc")
	add("output-duplicate-field", "--output=size,size", "/proc")
	add("output-duplicate-across-options", "--output=size", "--output=size", "/proc")
	add("output-trailing-comma", "--output=size,", "/proc")
	add("output-leading-comma", "--output=,size", "/proc")
	add("output-and-inodes", "-i", "--output=size", "/proc")
	add("output-and-inodes-after", "--output=size", "-i", "/proc")
	add("output-and-portability", "-P", "--output=size", "/proc")
	add("output-and-print-type", "-T", "--output=size", "/proc")
	add("output-conflict-i-beats-t", "-T", "-i", "--output=size", "/proc")
	add("output-conflict-p-beats-t", "-P", "-T", "--output=size", "/proc")
	add("output-conflict-first-seen", "-P", "--output=size", "-i", "/proc")
	add("output-conflict-i-first", "-i", "--output=size", "-P", "/proc")
	add("output-conflict-i-outranks-p", "-P", "-i", "--output=size", "/proc")
	add("output-conflict-i-outranks-p-and-t", "-T", "-P", "-i", "--output=size", "/proc")

	// --- the block-size grammar ---------------------------------------------
	for _, spec := range []string{
		"1", "512", "1K", "2K", "K", "k", "M", "G", "T", "P", "KiB", "MiB", "1MiB",
		"KB", "MB", "1KB", "2KB", "1MB", "1024", "1048576", "3000", "010", "0x10",
		" 1K", "+1K", "'1K", "human-readable", "si", "1024KiB",
	} {
		add("block-size-"+spec, "-B", spec, "/proc")
		add("block-size-long-"+spec, "--block-size="+spec, "/proc")
	}
	for _, spec := range []string{"0", "x", "bad", "", " ", " K", "-1K", "b", "B", "1b", "1B", "Ki", "1KK", "R", "Q", "p", "e", "z", "y", "Z", "Y", "99999999999999999999"} {
		add("block-size-bad-"+spec, "-B", spec, "/proc")
		add("block-size-long-bad-"+spec, "--block-size="+spec, "/proc")
	}
	// The header is the block size written back the way -B would have to
	// be typed for it, and which of the two bases that is depends on
	// which divides the number further — with a tie going to the powers
	// of 1000, which is what makes 102400 `100K` and 256000 `256kB`.
	// These are the sizes where the choice, the tie and the dropped `.0`
	// each decide the answer.
	for _, spec := range []string{
		"1023", "1025", "1500", "102400", "103424", "204800", "256000", "262144",
		"307200", "512000", "524288", "999424", "1022976", "1024000", "1047552",
		"1048575", "1048576", "1048577", "1049600", "2048000", "2097152",
		"512000000", "524288000", "1024000000", "1048576000", "2048000000",
		"2097152000", "2147483648", "1073741824000", "1048576000000",
		"1000000000000", "1099511627776", "123456789", "987654321",
	} {
		add("block-size-header-"+spec, "-B", spec, "--output=size", "/proc")
	}
	add("block-size-inodes-suffix", "-B", "KiB", "-i", "/proc")
	add("block-size-inodes-no-suffix", "-B", "M", "-i", "/proc")
	// A bare -B names a unit per COLUMN, off that column's own divisor:
	// the block columns divide by the block size and the inode columns by
	// one, so the two can carry different units in one row.
	add("block-size-suffix-per-column", "-B", "KiB", "--output=size,itotal", "/proc")
	add("block-size-suffix-per-column-k", "-B", "K", "--output=size,itotal", "/proc")
	add("block-size-suffix-per-column-mb", "-B", "MB", "--output=size,iused", "/proc")
	add("block-size-suffix-inode-only", "-B", "KiB", "--output=iavail", "/proc")
	add("block-size-suffix-inode-only-plain", "-B", "M", "--output=iavail", "/proc")
	add("block-size-portability", "-P", "-B", "M", "/proc")
	add("block-size-portability-plain", "-P", "-B", "1MB", "/proc")
	add("unit-last-wins-k-after-b", "-B", "M", "-k", "/proc")
	add("unit-last-wins-b-after-k", "-k", "-B", "M", "/proc")
	add("unit-last-wins-h-after-k", "-k", "-h", "/proc")
	add("unit-last-wins-b-after-h", "-h", "-B", "M", "/proc")
	add("unit-last-wins-si-after-h", "-h", "--si", "/proc")
	add("unit-last-wins-h-after-si", "--si", "-h", "/proc")

	// --- the environment -----------------------------------------------------
	env("env-df-block-size", []string{"DF_BLOCK_SIZE=M"}, "/proc")
	env("env-block-size", []string{"BLOCK_SIZE=M"}, "/proc")
	env("env-blocksize", []string{"BLOCKSIZE=M"}, "/proc")
	env("env-df-beats-block-size", []string{"DF_BLOCK_SIZE=M", "BLOCK_SIZE=K"}, "/proc")
	env("env-block-size-beats-blocksize", []string{"BLOCK_SIZE=K", "BLOCKSIZE=M"}, "/proc")
	env("env-first-set-wins-even-when-bad", []string{"DF_BLOCK_SIZE=bogus", "BLOCK_SIZE=K"}, "/proc")
	env("env-bad-ignored", []string{"DF_BLOCK_SIZE=bogus"}, "/proc")
	env("env-zero-ignored", []string{"DF_BLOCK_SIZE=0"}, "/proc")
	env("env-empty", []string{"DF_BLOCK_SIZE="}, "/proc")
	env("env-human", []string{"BLOCK_SIZE=human-readable"}, "/proc")
	env("env-si", []string{"BLOCKSIZE=si"}, "/proc")
	env("env-du-block-size-ignored", []string{"DU_BLOCK_SIZE=M"}, "/proc")
	env("env-option-beats-env", []string{"BLOCK_SIZE=K"}, "-B", "M", "/proc")
	env("posixly-correct", []string{"POSIXLY_CORRECT=1"}, "/proc")
	env("posixly-correct-empty", []string{"POSIXLY_CORRECT="}, "/proc")
	env("posixly-correct-portability", []string{"POSIXLY_CORRECT=1"}, "-P", "/proc")
	env("posixly-correct-loses-to-env", []string{"POSIXLY_CORRECT=1", "BLOCK_SIZE=M"}, "/proc")
	env("portability-ignores-env", []string{"DF_BLOCK_SIZE=M"}, "-P", "/proc")
	env("posixly-correct-no-permute", []string{"POSIXLY_CORRECT=1"}, "/proc", "-T")

	// --- -t / -x / -l --------------------------------------------------------
	// Only types whose every mount reports no blocks: a `-t ext4` would
	// be asking about whatever volumes this machine happens to carry.
	add("type-proc-filtered-out", "-t", "proc")
	add("type-proc-all", "-a", "-t", "proc")
	add("type-two", "-a", "-t", "proc", "-t", "sysfs")
	add("type-repeated", "-a", "-t", "proc", "-t", "proc")
	add("type-with-operand", "-t", "proc", "/proc")
	add("type-unknown-with-operand", "-t", "nosuchfs", "/proc")
	add("type-empty", "-t", "", "/proc")
	add("type-case-sensitive", "-t", "PROC", "/proc")
	add("type-operand-partly-filtered", "-t", "proc", "/proc", "/sys")
	add("exclude-with-operand", "-x", "proc", "/proc")
	add("exclude-unknown", "-x", "nosuchfs", "/proc")
	add("exclude-empty", "-x", "", "/proc")
	add("exclude-all-cgroups", "-a", "-t", "proc", "-x", "sysfs")
	add("type-and-exclude-conflict", "-t", "proc", "-x", "proc", "/proc")
	add("type-and-exclude-conflict-no-operand", "--output=source,target", "-t", "proc", "-x", "proc")
	add("type-and-exclude-conflict-second", "-t", "sysfs", "-t", "proc", "-x", "proc", "/proc")
	add("local-proc", "-l", "-t", "proc", "/proc")
	add("local-all", "-a", "-l", "-t", "proc")
	// Types that are not on the pseudo-filesystem list and still report no
	// blocks at all: the default listing drops them for the blocks, and
	// every one of them is zero on every machine, so both legs agree
	// whichever of these a kernel happens to mount.
	var zeroArgs []string
	for _, k := range []string{"cgroup", "cgroup2", "tracefs", "debugfs", "mqueue",
		"bpf", "pstore", "securityfs", "hugetlbfs", "ramfs", "binfmt_misc",
		"fusectl", "configfs", "selinuxfs"} {
		zeroArgs = append(zeroArgs, "-t", k)
	}
	add("zero-block-types-filtered-out", zeroArgs...)
	add("zero-block-types-all", append([]string{"-a"}, zeroArgs...)...)

	// --- --sync --------------------------------------------------------------
	// Accepted, and changes nothing that can be seen here: see the note
	// at the top of this file.
	add("sync", "--sync", "/proc")
	add("no-sync", "--no-sync", "/proc")
	add("no-sync-then-sync", "--no-sync", "--sync", "/proc")
	add("sync-then-no-sync", "--sync", "--no-sync", "/proc")

	// --- the getopt surface --------------------------------------------------
	add("bad-short", "-z")
	add("bad-long", "--foo=bar")
	add("bad-long-prefix", "--te")
	add("ambiguous-t", "--t")
	add("ambiguous-s", "--s")
	add("ambiguous-h", "--h")
	add("ambiguous-p", "--p")
	add("ambiguous-with-value", "--t=x")
	add("abbrev-total", "--to", "/proc")
	add("abbrev-type", "--ty", "proc", "/proc")
	add("abbrev-all", "--a", "/proc")
	add("abbrev-inodes", "--i", "/proc")
	add("abbrev-local", "--l", "/proc")
	add("abbrev-no-sync", "--n", "/proc")
	add("abbrev-output", "--o", "/proc")
	add("block-size-needs-argument", "--block-size")
	add("block-size-needs-argument-short", "-B")
	add("block-size-needs-argument-abbrev", "--b")
	add("type-needs-argument", "--type")
	add("type-needs-argument-short", "-t")
	add("exclude-needs-argument", "--exclude-type")
	add("total-refuses-argument", "--total=x")
	add("all-refuses-argument", "--all=x")
	add("help-refuses-argument", "--help=x")
	add("version-refuses-argument", "--version=x")
	add("cluster", "-aTP", "/proc")
	add("cluster-with-value", "-TBM", "/proc")
	add("cluster-with-split-value", "-TB", "M", "/proc")
	add("operand-before-option", "/proc", "-T")

	// --- operands that do not resolve ---------------------------------------
	add("missing", "/nosuchpath")
	add("missing-twice", "/nosuchpath", "/alsonosuch")
	add("missing-then-good", "/nosuchpath", "/proc")
	add("good-then-missing", "/proc", "/nosuchpath")
	add("missing-with-type-filter", "-t", "proc", "/nosuchpath")
	add("empty-operand", "")
	add("dash-operand", "-")
	add("dot-dot-of-root", stable, "/..")
	add("missing-quoted", "/no such/path")
	add("missing-non-utf8", "/no\xffpath")
	add("missing-newline", "/no\npath")
	add("missing-apostrophe", "/no'path")
	add("dangling-symlink", filepath.Join(dir, "dangling"))
	add("missing-under-tmp", filepath.Join(dir, "nosuch"))

	// --- real numbers, in the columns that cannot move ----------------------
	add("tempdir", stable, dir)
	add("tempfile", stable, file)
	add("tempsub", stable, sub)
	add("temp-symlink", stable, filepath.Join(dir, "slink"))
	add("symlink-to-another-filesystem", stable, filepath.Join(dir, "procl"))
	add("symlink-to-another-filesystem-file", "--output=file,fstype,target", filepath.Join(dir, "procl"))
	add("temp-relative-name-in-file-column", "--output=file,size", file)
	add("temp-size", "--output=size", dir)
	add("temp-size-human", "-h", "--output=size", dir)
	add("temp-size-si", "-H", "--output=size", dir)
	add("temp-size-bytes", "-B", "1", "--output=size", dir)
	add("temp-size-512", "-B", "512", "--output=size", dir)
	add("temp-size-2k", "-B", "2K", "--output=size", dir)
	add("temp-size-3m", "-B", "3M", "--output=size", dir)
	add("temp-size-mb", "-B", "1MB", "--output=size", dir)
	add("temp-size-bare-m", "-B", "M", "--output=size", dir)
	add("temp-size-kib", "-B", "KiB", "--output=size", dir)
	add("temp-size-posix", "-B", "512", "--output=size", dir)
	add("temp-size-total", "--total", "--output=source,size,target", dir)
	add("temp-size-two-operands", "--output=size,target", dir, "/proc")
	add("temp-size-with-proc", "--output=source,fstype,size,target", "/proc", dir, "/sys")

	// --- write failures ------------------------------------------------------
	cases = append(cases, invocation{
		name: "write-error-full", args: []string{"/proc"}, stdout: stdoutFull,
	})
	cases = append(cases, invocation{
		name: "write-error-closed", args: []string{"/proc"}, stdout: stdoutClosed,
	})
	cases = append(cases, invocation{
		name: "write-error-closed-usage", args: []string{"-z"}, stdout: stdoutClosed,
	})
	return cases
}

func TestDf(t *testing.T) {
	requireParity(t, "df", dfCases(t))
}

// TestDfAvailableIsNotFree holds the one thing about df's numbers that the
// oracle corpus structurally cannot: that `Available` is the count an
// unprivileged caller may take (`f_bavail`) and not every free block
// (`f_bfree`). The two differ by the superuser reserve, and a filesystem
// that keeps one is a filesystem being written to — so the columns that
// would show it move between the GNU run and the Fern run and cannot be
// diffed. It is checked here the way mktemp's unprovable properties are:
// beside the corpus, against the machine rather than against GNU.
//
// The check needs no clock-tight read. Both numbers come from ONE statfs
// inside df, so the identity holds exactly however much drifted since this
// test's own statfs:
//
//	used + available <= size
//
// Under the swap, used becomes blocks - bavail and available becomes
// bfree, and their sum exceeds blocks by the whole reserve.
//
// The reserve has to be real for that to bite, so the filesystem is chosen
// by asking the kernel which of the paths the suite can reach keeps one; a
// machine where none does fails rather than passing vacuously.
func TestDfAvailableIsNotFree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("df reads /proc/self/mountinfo; %s has no such table", runtime.GOOS)
	}
	dir := t.TempDir()
	var path string
	var gap uint64
	for _, cand := range []string{dir, "/", "/usr", "/var", "/home"} {
		var st syscall.Statfs_t
		if err := syscall.Statfs(cand, &st); err != nil {
			continue
		}
		if st.Bfree > st.Bavail && st.Bfree-st.Bavail > gap {
			path, gap = cand, st.Bfree-st.Bavail
		}
	}
	if path == "" {
		t.Fatal("no filesystem this suite can reach keeps a superuser reserve, so the f_bavail / f_bfree distinction cannot be exercised; run the suite where one does")
	}

	inv := invocation{name: "avail", args: []string{"-B", "1", "--output=size,used,avail", path}}
	got := inv.run(t, fernBin(t, "df"), "df")
	if got.exit != 0 || len(got.stderr) != 0 {
		t.Fatalf("df %v: %s, stderr %q", inv.args, got.how(), got.stderr)
	}
	lines := strings.Split(strings.TrimRight(string(got.stdout), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a header and one row, got %q", got.stdout)
	}
	fields := strings.Fields(lines[1])
	if len(fields) != 3 {
		t.Fatalf("want three columns, got %q", lines[1])
	}
	nums := make([]uint64, 3)
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			t.Fatalf("column %d of %q is not a count: %v", i, lines[1], err)
		}
		nums[i] = v
	}
	size, used, avail := nums[0], nums[1], nums[2]
	if used+avail > size {
		t.Errorf("df says used + available (%d + %d) is past the size of %s (%d): Available is f_bfree where it has to be f_bavail, and the %d-block reserve is being offered to callers who cannot have it",
			used, avail, path, size, gap)
	}
}

func TestDfHelp(t *testing.T) {
	requireHelp(t, "df", []string{"--help"}, 0)
	requireHelp(t, "df", []string{"/proc", "--help"}, 0)
	requireHelp(t, "df", []string{"--hel"}, 0)
}

func TestDfVersion(t *testing.T) {
	requireVersion(t, "df", []string{"--version"}, 0)
	requireVersion(t, "df", []string{"--vers"}, 0)
}
