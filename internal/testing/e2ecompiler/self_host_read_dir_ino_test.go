package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// readDirInoSource builds a directory of two files and a subdirectory, lists
// it with read_dir_ino, and prints one line per entry: the name, the inode
// number read_dir_ino reported, and whether lstat of the same path reports
// that number too. read_dir_ino must hand back read_dir's names in read_dir's
// order, so any difference is an exit code rather than a line. The last line
// is the missing directory's error.
const readDirInoSource = `import "std/i64";

function main(): i32 {
    match (create_dir_all("d/sub")) { Err(_) => { return 1; }, Ok(_) => {} }
    match (write_file("d/a.txt", "x")) { Err(_) => { return 2; }, Ok(_) => {} }
    match (write_file("d/b.txt", "y")) { Err(_) => { return 3; }, Ok(_) => {} }
    let names: string[] = [];
    match (read_dir("d")) { Ok(ns) => { names = ns; }, Err(_) => { return 4; } }
    match (read_dir_ino("d")) {
        Err(_) => { return 5; },
        Ok(es) => {
            if (es.len() != names.len()) { return 6; }
            let i: i32 = 0;
            while (i < es.len()) {
                let e: DirEntry = es[i];
                if (e.name != names[i]) { return 7; }
                match (lstat("d/" + e.name)) {
                    Ok(st) => {
                        let agrees: string = "differs";
                        if (st.ino == e.ino) { agrees = "agrees"; }
                        print(e.name + " " + e.ino.to_string() + " " + agrees);
                    },
                    Err(_) => { return 8; },
                }
                i = i + 1;
            }
        },
    }
    match (read_dir_ino("missing")) {
        Ok(_) => { return 9; },
        Err(e) => {
            match (e) {
                NotFound(_) => { print("missing NotFound"); },
                _ => { return 10; },
            }
        },
    }
    return 0;
}
`

// readDirInoWant is what every leg prints once the inode numbers are masked.
const readDirInoWant = "a.txt N agrees\nb.txt N agrees\nmissing NotFound\nsub N agrees\n"

// readDirInoSource's inode contract per leg. A kernel's d_ino is the inode
// lstat reports, so the native legs and the interpreter are checked against
// the host's own os.Lstat. Preview 1's d_ino is the host's identifier for the
// file, which wasmtime derives from the device and inode rather than passing
// the inode through, so there it is checked against the program's own lstat
// (the agrees column) and for being present. Preview 2's directory entry has
// no inode, so every ino is 0 there — the documented "not supplied" answer.
const (
	inoHost = iota
	inoPresent
	inoAbsent
)

// checkReadDirIno masks the inode numbers in stdout after checking them
// against `mode`, and compares what is left with readDirInoWant.
func checkReadDirIno(t *testing.T, workDir, stdout string, mode int) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	sort.Strings(lines)
	var masked strings.Builder
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 3 {
			masked.WriteString(line + "\n")
			continue
		}
		// An i64 holding the platform's unsigned number: wasmtime's
		// identifier uses all 64 bits, so it can print negative.
		signed, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			t.Fatalf("line %q: ino %q: %v", line, f[1], err)
		}
		ino := uint64(signed)
		switch mode {
		case inoHost:
			fi, err := os.Lstat(filepath.Join(workDir, "d", f[0]))
			if err != nil {
				t.Fatal(err)
			}
			if want := fi.Sys().(*syscall.Stat_t).Ino; ino != uint64(want) {
				t.Errorf("%s: read_dir_ino ino = %d, os.Lstat ino = %d", f[0], ino, want)
			}
		case inoPresent:
			if ino == 0 {
				t.Errorf("%s: read_dir_ino ino = 0, want the host's identifier", f[0])
			}
		case inoAbsent:
			if ino != 0 {
				t.Errorf("%s: read_dir_ino ino = %d, want 0 where the platform supplies none", f[0], ino)
			}
		}
		masked.WriteString(f[0] + " N " + f[2] + "\n")
	}
	if got := masked.String(); got != readDirInoWant {
		t.Errorf("read_dir_ino listing, inode numbers masked:\n%s\nwant:\n%s\nraw:\n%s", got, readDirInoWant, stdout)
	}
}

// runReadDirIno runs cmd in a fresh directory and returns that directory,
// stdout and stderr, failing on a non-zero exit (the code names the step in
// readDirInoSource).
func runReadDirIno(t *testing.T, cmd *exec.Cmd) (string, string, string) {
	t.Helper()
	if cmd.Dir == "" {
		cmd.Dir = t.TempDir()
	}
	var ob, eb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &ob, &eb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally\n%s", cmd.Path, eb.String())
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (see readDirInoSource)\nstdout:\n%s\nstderr:\n%s", code, ob.String(), eb.String())
	}
	return cmd.Dir, ob.String(), eb.String()
}

func writeReadDirInoSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(readDirInoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestSelfHostReadDirIno runs readDirInoSource under the interpreter and
// through the self-host compiler on x86-64, arm64 and wasm (a core module
// over preview 1 and a component over preview 2), each compiled leg with the
// leak census on: the entries, their names and the error all reach the
// program as boxes the runtime built, and every one must be given back.
func TestSelfHostReadDirIno(t *testing.T) {
	src := writeReadDirInoSource(t)
	t.Run("interp", func(t *testing.T) {
		dir, out, _ := runReadDirIno(t, exec.Command(buildLangBinForInterp(t), "-interp", src))
		checkReadDirIno(t, dir, out, inoHost)
	})
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		dir, out, stderr := runReadDirIno(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1")))
		checkReadDirIno(t, dir, out, inoHost)
		assertBalancedCensus(t, stderr)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		dir, out, stderr := runReadDirIno(t, runArm64Bin(qemu, cli.arm64Binary(t, src, "FERN_LEAKCHECK=1")))
		checkReadDirIno(t, dir, out, inoHost)
		assertBalancedCensus(t, stderr)
	})
	t.Run("wasm32-wasi-preview1", func(t *testing.T) {
		wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1")
		if b, err := os.ReadFile(wat); err != nil || !bytes.Contains(b, []byte("call $__fern_read_dir_ino")) {
			t.Fatalf("read_dir_ino did not reach $__fern_read_dir_ino (read: %v)", err)
		}
		dir, out, stderr := runReadDirIno(t, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", wat))
		checkReadDirIno(t, dir, out, inoPresent)
		assertBalancedCensus(t, stderr)
	})
	t.Run("wasm32-wasi-preview2", func(t *testing.T) {
		comp := cli.wasmComponent(t, src, "FERN_LEAKCHECK=1")
		dir, out, stderr := runReadDirIno(t, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", comp))
		checkReadDirIno(t, dir, out, inoAbsent)
		assertBalancedCensus(t, stderr)
	})
}

// TestSelfHostArm64DarwinReadDirIno is the getdirentries64 leg: XNU's record
// puts the name two bytes later than Linux's, and d_ino at the same place.
func TestSelfHostArm64DarwinReadDirIno(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src := writeReadDirInoSource(t)
	bin := filepath.Join(t.TempDir(), "read_dir_ino")
	compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	compile.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	work, out, stderr := runReadDirIno(t, exec.Command(bin))
	checkReadDirIno(t, work, out, inoHost)
	assertBalancedCensus(t, stderr)
}
