package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The program makes a directory with a file, a link, a directory and, with
// bad, a file whose name is not UTF-8. Each exit status names the check that
// failed.
func readDirInoBytesSource(bad, kinds, handles bool) string {
	src := `function same(a: u8[], b: u8[]): boolean {
    if (a.len() != b.len()) { return false; }
    for i in 0..a.len() {
        if (a[i] != b[i]) { return false; }
    }
    return true;
}

function bad_name(): u8[] {
    return [98u8, 97u8, 100u8, 255u8, 254u8];
}

// The number of entries with a known name, plus 100 for the name that is not UTF-8.
function check(es: DirEntryBytes[]): i32 {
    let seen: i32 = 0;
    for e in es {
        let name: string = unsafe { string_from_bytes_unchecked(e.name) };
        let Ok(st) = lstat("d/" + name) else { return -20; };
        if (st.ino != e.ino) { return -21; }
        let want: u32 = 32768u32;
        if (name == "lnk") { want = 40960u32; }
        if (name == "sub") { want = 16384u32; }
        if (!KINDS) { want = 0u32; }
        if (e.kind != want) { return -22; }
        if (same(e.name, bad_name())) {
            seen = seen + 100;
        } else if (name == "a.txt" || name == "lnk" || name == "sub") {
            seen = seen + 1;
        } else {
            return -23;
        }
    }
    return seen;
}

function main(): i32 {
    match (create_dir_all("d/sub")) { Err(_) => { return 1; }, Ok(_) => {} }
    match (write_file("d/a.txt", "x")) { Err(_) => { return 2; }, Ok(_) => {} }
    match (create_symlink("a.txt", "d/lnk")) { Err(_) => { return 3; }, Ok(_) => {} }
    let want: i32 = 3;
    if (BAD) {
        match (write_file("d/" + unsafe { string_from_bytes_unchecked(bad_name()) }, "y")) { Err(_) => { return 4; }, Ok(_) => {} }
        want = 103;
        match (read_dir_ino("d")) { Err(InvalidUtf8(_)) => {}, _ => { return 5; } }
    }
    match (read_dir_ino_bytes("d")) {
        Err(_) => { return 6; },
        Ok(es) => {
            let got: i32 = check(es);
            if (got != want) { print(got.to_string()); return 7; }
        },
    }
    match (read_dir_ino_bytes("missing")) {
        Err(NotFound(p)) => { if (p != "missing") { return 8; } },
        _ => { return 9; },
    }
HANDLES    return 0;
}
`
	handleChecks := ""
	if handles {
		handleChecks = `    match (open_dir("d")) {
        Err(_) => { return 10; },
        Ok(d) => {
            for round in 0..2 {
                match (d.entries_ino_bytes()) {
                    Err(_) => { return 11; },
                    Ok(es) => { if (check(es) != want) { return 12; } },
                }
            }
            d.close();
        },
    }
`
	}
	for _, r := range [][2]string{{"HANDLES", handleChecks}, {"BAD", fmt.Sprint(bad)}, {"KINDS", fmt.Sprint(kinds)}} {
		src = strings.ReplaceAll(src, r[0], r[1])
	}
	return src
}

func writeReadDirInoBytesSource(t *testing.T, bad, kinds, handles bool) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(readDirInoBytesSource(bad, kinds, handles)), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSelfHostReadDirInoBytes(t *testing.T) {
	native := writeReadDirInoBytesSource(t, true, true, true)
	t.Run("oracle", func(t *testing.T) {
		runReadDirIno(t, exec.Command(buildLangBinForInterp(t), "-interp", native))
	})
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		_, _, stderr := runReadDirIno(t, runX86_64Bin(cli.runner, cli.x86Binary(t, native, "FERN_LEAKCHECK=1")))
		assertBalancedCensus(t, stderr)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		_, _, stderr := runReadDirIno(t, runArm64Bin(qemu, cli.arm64Binary(t, native, "FERN_LEAKCHECK=1")))
		assertBalancedCensus(t, stderr)
	})
	// A WASI host gives a name as a string, so a name that is not UTF-8 does
	// not reach the program.
	t.Run("wasm32-wasi-preview1", func(t *testing.T) {
		wat := cli.emit(t, writeReadDirInoBytesSource(t, false, true, false), "wasm32-wasi", "FERN_LEAKCHECK=1")
		_, _, stderr := runReadDirIno(t, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", wat))
		assertBalancedCensus(t, stderr)
	})
	t.Run("wasm32-wasi-preview2", func(t *testing.T) {
		comp := cli.wasmComponent(t, writeReadDirInoBytesSource(t, false, false, false), "FERN_LEAKCHECK=1")
		_, _, stderr := runReadDirIno(t, exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", comp))
		assertBalancedCensus(t, stderr)
	})
}

func TestSelfHostArm64DarwinReadDirInoBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	// APFS refuses a file name that is not UTF-8.
	src := writeReadDirInoBytesSource(t, false, true, true)
	bin := filepath.Join(t.TempDir(), "read_dir_ino_bytes")
	compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	compile.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	_, _, stderr := runReadDirIno(t, exec.Command(bin))
	assertBalancedCensus(t, stderr)
}
