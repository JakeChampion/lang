package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const statProbeMtime int64 = 1_600_000_000

func statProbeTree(t *testing.T) (dir, file, link, other string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
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

func statFieldsNativeSource(t *testing.T, file, link, other string, euid, egid int) string {
	t.Helper()
	bt, btn := hostBirth(t, file)
	dev, _ := hostDevs(t, file)
	_, nullRdev := hostDevs(t, "/dev/null")
	return fmt.Sprintf(`function st(p: string): FileStat {
    match (stat(p)) {
        Ok(s) => { return s; },
        Err(e) => { return FileStat { is_file: false, is_dir: false, size: 0 as i64,
            mode: 0 as u32, nlink: 0 as u32, uid: 0 as u32, gid: 0 as u32,
            dev: 0 as i64, rdev: 0 as i64, ino: 0 as i64, blksize: 0 as i64, blocks: 0 as i64,
            atime: 0 as i64, atime_nsec: 0 as i64, mtime: 0 as i64, mtime_nsec: 0 as i64,
            ctime: 0 as i64, ctime_nsec: 0 as i64, btime: 0 as i64, btime_nsec: 0 as i64 }; },
    }
    return FileStat { is_file: false, is_dir: false, size: 0 as i64,
        mode: 0 as u32, nlink: 0 as u32, uid: 0 as u32, gid: 0 as u32,
        dev: 0 as i64, rdev: 0 as i64, ino: 0 as i64, blksize: 0 as i64, blocks: 0 as i64,
        atime: 0 as i64, atime_nsec: 0 as i64, mtime: 0 as i64, mtime_nsec: 0 as i64,
        ctime: 0 as i64, ctime_nsec: 0 as i64, btime: 0 as i64, btime_nsec: 0 as i64 };
}
function main(): i32 {
    let f: FileStat = st(%[1]q);
    let l: FileStat = st(%[2]q);
    let o: FileStat = st(%[3]q);
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
    // The birth time, zero where the filesystem records none.
    if (f.btime != (%[7]d as i64)) { return 22; }
    if (f.btime_nsec != (%[8]d as i64)) { return 23; }
    // dev_t as the host encodes it, and a device node's rdev.
    if (f.dev != (%[9]d as i64)) { return 24; }
    if (st("/dev/null").rdev != (%[10]d as i64)) { return 25; }
    return 0;
}
`, file, link, other, statProbeMtime, euid, egid, bt, btn, dev, nullRdev)
}

func TestX86_64StatFields(t *testing.T) {
	_, file, link, other := statProbeTree(t)
	src := statFieldsNativeSource(t, file, link, other, os.Geteuid(), os.Getegid())
	code, out := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)\n%s", code, out)
	}
}

func TestArm64StatFields(t *testing.T) {
	_, file, link, other := statProbeTree(t)
	src := statFieldsNativeSource(t, file, link, other, os.Geteuid(), os.Getegid())
	out, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)\n%s", code, out)
	}
}

func TestNativeStatFields(t *testing.T) {
	fern := buildFernCLI(t)
	_, file, link, other := statProbeTree(t)
	srcPath := filepath.Join(t.TempDir(), "probe.fern")
	src := statFieldsNativeSource(t, file, link, other, os.Geteuid(), os.Getegid())
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			runner := ""
			if target == "x86-64-linux" {
				runner = x86QemuOrEmpty(t)
			} else {
				runner = arm64QemuOrEmpty(t)
			}
			bin := filepath.Join(t.TempDir(), "probe")
			if out, err := exec.Command(fern, "-target", target, "-o", bin, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd := exec.Command(bin)
			if runner != "" {
				cmd = exec.Command(runner, bin)
			}
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)\n%s", code, out)
			}
		})
	}
}

func TestInterpStatFields(t *testing.T) {
	_, file, link, other := statProbeTree(t)
	src := statFieldsNativeSource(t, file, link, other, os.Geteuid(), os.Getegid())
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the field (see statFieldsNativeSource)", code)
	}
}

func statFieldsWasmSource() string {
	return fmt.Sprintf(`function main(): i32 {
    match (stat("probe.txt")) {
        Ok(f) => {
            if (!f.is_file) { return 1; }
            if (f.size != (5 as i64)) { return 2; }
            if (f.mtime != (%[1]d as i64)) { return 3; }
            if (f.mtime_nsec < (0 as i64) || f.mtime_nsec > (999999999 as i64)) { return 4; }
            // The fields WASI does not report.
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

func TestWASMPreview1StatFields(t *testing.T) {
	mod := buildWasmCore(t, statFieldsWasmSource())
	if got := runPreview1Module(t, mod, statProbeWasmDir(t)); got != 0 {
		t.Errorf("main = %d, want 0 — the code names the field (see statFieldsWasmSource)", got)
	}
}

func TestWASMStatFields(t *testing.T) {
	out := runResultStdout(t, statFieldsWasmSource(), runOpts{workDir: statProbeWasmDir(t)})
	if got := parseMainResult(t, out); got != 0 {
		t.Errorf("main = %d, want 0 — the code names the field (see statFieldsWasmSource)\nstdout:\n%s", got, out)
	}
}

func statProbeWasmDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatalf("seed: %v", err)
	}
	when := time.Unix(statProbeMtime, 0)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return dir
}

func parseMainResult(t *testing.T, stdout string) int {
	t.Helper()
	lines := strings.Split(stdout, "\n")
	for k := len(lines) - 1; k >= 0; k-- {
		ln := strings.TrimSpace(lines[k])
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
