package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The mtime every probe below pins. A fixed instant rather than "whatever the
// filesystem said" is the point: a backend that reported the WRONG timestamp
// field — Darwin's st_birthtimespec sits two slots past st_mtimespec, and
// preview 1 reports nanoseconds where stat(2) reports seconds — would still
// produce a plausible-looking number if the assertion were only "non-zero".
const statProbeMtime int64 = 1_600_000_000

// statProbeTree writes the file every probe reads: 0640, a pinned mtime, five
// bytes of content, and a hard link beside it so `dev` + `ino` (a shell's
// `-ef`) and `nlink` have something to say.
func statProbeTree(t *testing.T) (dir, file, link, other string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	// WriteFile applies the process umask to the mode, so set it explicitly.
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	other = filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatalf("write other: %v", err)
	}
	link = filepath.Join(dir, "hardlink.txt")
	if err := os.Link(file, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	when := time.Unix(statProbeMtime, 0)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return dir, file, link, other
}

// statFieldsNativeSource asserts every FileStat field a kernel fills. Each
// failure returns its own exit code so the number names the field.
func statFieldsNativeSource(file, link, other string, euid, egid int) string {
	return fmt.Sprintf(`function st(p: string): FileStat {
    match (stat(p)) {
        Ok(s) => { return s; },
        Err(e) => { return FileStat { is_file: false, is_dir: false, size: 0 as i64,
            mode: 0 as u32, nlink: 0 as u32, uid: 0 as u32, gid: 0 as u32,
            dev: 0 as i64, rdev: 0 as i64, ino: 0 as i64, blksize: 0 as i64, blocks: 0 as i64,
            atime: 0 as i64, atime_nsec: 0 as i64, mtime: 0 as i64, mtime_nsec: 0 as i64,
            ctime: 0 as i64, ctime_nsec: 0 as i64 }; },
    }
    return FileStat { is_file: false, is_dir: false, size: 0 as i64,
        mode: 0 as u32, nlink: 0 as u32, uid: 0 as u32, gid: 0 as u32,
        dev: 0 as i64, rdev: 0 as i64, ino: 0 as i64, blksize: 0 as i64, blocks: 0 as i64,
        atime: 0 as i64, atime_nsec: 0 as i64, mtime: 0 as i64, mtime_nsec: 0 as i64,
        ctime: 0 as i64, ctime_nsec: 0 as i64 };
}
function main(): i32 {
    var f: FileStat = st(%[1]q);
    var l: FileStat = st(%[2]q);
    var o: FileStat = st(%[3]q);
    // chmod 0640 — the permission bits and the S_IFMT type bits, which is
    // what makes mode answer the kind predicates a shell test needs.
    if ((f.mode & (511 as u32)) != (416 as u32)) { return 1; }
    if ((f.mode & (61440 as u32)) != (32768 as u32)) { return 2; }
    if (!f.is_file) { return 3; }
    if (f.is_dir) { return 4; }
    if (f.size != (5 as i64)) { return 5; }
    // touch -d: seconds since the epoch, with the sub-second remainder
    // reported separately rather than folded into the same number.
    if (f.mtime != (%[4]d as i64)) { return 6; }
    if (f.mtime_nsec < (0 as i64) || f.mtime_nsec > (999999999 as i64)) { return 7; }
    if (f.atime != (%[4]d as i64)) { return 8; }
    if (f.ctime <= (0 as i64)) { return 9; }
    // The hard link: same inode on the same device, which is exactly what
    // a shell test -ef asks, and a link count of two.
    if (f.nlink != (2 as u32)) { return 10; }
    if (l.nlink != (2 as u32)) { return 11; }
    if (f.ino != l.ino) { return 12; }
    if (f.dev != l.dev) { return 13; }
    // A different file on the same filesystem: same device, different inode.
    if (o.dev != f.dev) { return 14; }
    if (o.ino == f.ino) { return 15; }
    if (o.nlink != (1 as u32)) { return 16; }
    // The file was created by this process, so it carries its ids.
    if (f.uid != (%[5]d as u32)) { return 17; }
    if (f.gid != (%[6]d as u32)) { return 18; }
    // A regular file has no device node, and its block accounting is real.
    if (f.rdev != (0 as i64)) { return 19; }
    if (f.blksize <= (0 as i64)) { return 20; }
    if (f.blocks < (0 as i64)) { return 21; }
    return 0;
}
`, file, link, other, statProbeMtime, euid, egid)
}

// TestX86_64StatFields / TestArm64StatFields read a known file through the
// full `stat(2)` record: the mode a chmod set, the mtime a chtimes pinned, and
// the (dev, ino) pair a hard link shares.
//
// Every backend fills this struct by hand — the two arm64 emitters and x86-64
// with stores at literal offsets, wasmbin with i32/i64.store — off one offset
// table in `internal/ir`. What that table cannot check is the OTHER side of
// each copy: which kernel field feeds which slot, which is per-target and, on
// Darwin, shares almost no offset with Linux. A wrong source offset is silent —
// it reads a real number out of the neighbouring field — so the assertions are
// on values a test can arrange rather than on "not zero".
func TestX86_64StatFields(t *testing.T) {
	_, file, link, other := statProbeTree(t)
	src := statFieldsNativeSource(file, link, other, os.Geteuid(), os.Getegid())
	code, out := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)\n%s", code, out)
	}
}

func TestArm64StatFields(t *testing.T) {
	_, file, link, other := statProbeTree(t)
	src := statFieldsNativeSource(file, link, other, os.Geteuid(), os.Getegid())
	out, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)\n%s", code, out)
	}
}

// The interpreter answers `stat` from Go's os.FileInfo plus a per-GOOS
// projection of syscall.Stat_t (Linux's Atim vs Darwin's Atimespec), so it is a
// fourth implementation of the same record and gets the same probe. It is also
// the one a migrated in-language test suite runs under.
func TestInterpStatFields(t *testing.T) {
	_, file, link, other := statProbeTree(t)
	src := statFieldsNativeSource(file, link, other, os.Geteuid(), os.Getegid())
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)", code)
	}
}

// statFieldsWasmSource is the preview-1 half of the same probe, and it is a
// different assertion because WASI answers a different question.
//
// `filestat` has dev, ino, nlink, size and the three timestamps; it has no
// mode, uid, gid, rdev, blksize or blocks at all. Those read ZERO, which is
// the checker's documented contract for `stat` — so a zero here is the
// assertion, not an absence of one. A backend that left the tail of the struct
// uninitialised would fail this exactly as a wrong value would.
func statFieldsWasmSource() string {
	return fmt.Sprintf(`function main(): i32 {
    match (stat("probe.txt")) {
        Ok(f) => {
            if (!f.is_file) { return 1; }
            if (f.size != (5 as i64)) { return 2; }
            if (f.mtime != (%[1]d as i64)) { return 3; }
            if (f.mtime_nsec < (0 as i64) || f.mtime_nsec > (999999999 as i64)) { return 4; }
            // The fields preview 1 does not report.
            if (f.mode != (0 as u32)) { return 5; }
            if (f.uid != (0 as u32)) { return 6; }
            if (f.gid != (0 as u32)) { return 7; }
            if (f.rdev != (0 as i64)) { return 8; }
            if (f.blksize != (0 as i64)) { return 9; }
            if (f.blocks != (0 as i64)) { return 10; }
            // nlink IS reported, and a plain file has exactly one link.
            if (f.nlink != (1 as u32)) { return 11; }
            return 0;
        },
        Err(_) => { return 20; },
    }
    return 21;
}
`, statProbeMtime)
}

func TestWASMStatFields(t *testing.T) {
	stdout, stderr := runWasmStatProbe(t, statFieldsWasmSource())
	if got := parseMainResult(t, stdout); got != 0 {
		t.Errorf("main = %d, want 0 — the code names the field (see statFieldsWasmSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
}

// runWasmStatProbe seeds the probe file and stamps its mtime before the
// component runs; runWasmInDir's own seeding cannot, since it writes the files
// itself and nothing there sets a time.
//
// main's return reaches us on STDOUT, not as the exit status: the harness
// builds with PrintMainResult, so a component that returned 7 still exits 0.
func runWasmStatProbe(t *testing.T, src string) (stdout, stderr string) {
	t.Helper()
	p := buildComponent(t, src)
	dir := t.TempDir()
	file := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatalf("seed: %v", err)
	}
	when := time.Unix(statProbeMtime, 0)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	s, e, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, s, e)
	}
	return s, e
}

// parseMainResult reads the integer PrintMainResult wrote to stdout.
func parseMainResult(t *testing.T, stdout string) int {
	t.Helper()
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			return n
		}
	}
	t.Fatalf("no main result on stdout:\n%s", stdout)
	return 0
}
