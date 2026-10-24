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

const readDirInoSource = `import "std/i64";

function main(): i32 {
    match (create_dir_all("d/sub")) { Err(_) => { return 1; }, Ok(_) => {} }
    match (write_file("d/a.txt", "x")) { Err(_) => { return 2; }, Ok(_) => {} }
    match (write_file("d/b.txt", "y")) { Err(_) => { return 3; }, Ok(_) => {} }
    match (create_symlink("a.txt", "d/lnk")) { Err(_) => { return 11; }, Ok(_) => {} }
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
                        print(e.name + " " + e.ino.to_string() + " " + agrees + " kind=" + (e.kind as i64).to_string());
                    },
                    Err(_) => { return 8; },
                }
                i = i + 1;
            }
        },
    }
    let all: string[] = [];
    match (read_dir_all("d")) { Ok(ns) => { all = ns; }, Err(_) => { return 12; } }
    match (read_dir_ino_all("d")) {
        Err(_) => { return 13; },
        Ok(es) => {
            if (es.len() != all.len()) { return 14; }
            let i: i32 = 0;
            while (i < es.len()) {
                if (es[i].name != all[i]) { return 15; }
                if ((es[i].name == "." || es[i].name == "..") && es[i].kind != (16384 as u32) && es[i].kind != (0 as u32)) { return 16; }
                i = i + 1;
            }
        },
    }
    print("dots " + ((all.len() - names.len()) as i64).to_string());
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

func readDirInoWant(kinds bool) string {
	file, link, dir := "kind=32768", "kind=40960", "kind=16384"
	if !kinds {
		file, link, dir = "kind=0", "kind=0", "kind=0"
	}
	dots := "dots 2"
	if !kinds {
		dots = "dots 0"
	}
	return "a.txt N agrees " + file + "\nb.txt N agrees " + file + "\n" + dots + "\nlnk N agrees " + link + "\nmissing NotFound\nsub N agrees " + dir + "\n"
}

const (
	inoHost = iota
	inoPresent
	inoAbsent
)

func checkReadDirIno(t *testing.T, workDir, stdout string, mode int) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	sort.Strings(lines)
	var masked strings.Builder
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 4 {
			masked.WriteString(line + "\n")
			continue
		}
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
		masked.WriteString(f[0] + " N " + f[2] + " " + f[3] + "\n")
	}
	want := readDirInoWant(mode != inoAbsent)
	if got := masked.String(); got != want {
		t.Errorf("read_dir_ino listing, inode numbers masked:\n%s\nwant:\n%s\nraw:\n%s", got, want, stdout)
	}
}

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
