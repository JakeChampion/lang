package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The native read_dir runtime drains the directory into a heap buffer that
// started life at a fixed 1 MiB and stopped when it was full, so a listing
// past about 32k entries was silently cut short — `du -a` on a 40000-file
// directory printed 32767 lines. The buffer grows now. The program seeds
// 4096 names of 253 bytes each: more than 1 MiB of records on both the
// Linux getdents64 and the Darwin getdirentries64 layouts, so a drain that
// stops at the first capacity lists fewer than it wrote. Exit codes name
// the failing step.
const readDirLargeSource = `function main(): i32 {
    match (create_dir_all("d")) { Err(_) => { return 1; }, Ok(_) => {} }
    let al: string[] = ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p"];
    let pad: string = "";
    while (pad.len() < 250) { pad = pad + "x"; }
    let a: i32 = 0;
    while (a < 16) {
        let b: i32 = 0;
        while (b < 16) {
            let c: i32 = 0;
            while (c < 16) {
                match (write_file("d/" + pad + al[a] + al[b] + al[c], "")) { Err(_) => { return 2; }, Ok(_) => {} }
                c = c + 1;
            }
            b = b + 1;
        }
        a = a + 1;
    }
    let n: i32 = 0;
    match (read_dir("d")) { Ok(ns) => { n = ns.len(); }, Err(_) => { return 3; } }
    if (n < 4096) { return 4; }
    if (n > 4096) { return 5; }
    let m: i32 = 0;
    match (read_dir_all("d")) { Ok(ns) => { m = ns.len(); }, Err(_) => { return 6; } }
    if (m != 4098) { return 7; }
    return 0;
}
`

// readDirLargeNative compiles the program with the Go compiler for target
// and returns the binary path, in a fresh directory the run can seed.
func readDirLargeNative(t *testing.T, target string) (bin, dir string) {
	t.Helper()
	fern := buildFernCLI(t)
	dir = t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(readDirLargeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "prog")
	if out, err := exec.Command(fern, "-target", target, "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("native %s build failed: %v\n%s", target, err, out)
	}
	return bin, dir
}

func checkReadDirLarge(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	out, code := runWithPipes(t, cmd)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (4 = read_dir listed fewer than it wrote; see readDirLargeSource)\n%s", code, out)
	}
}

func TestArm64NativeReadDirLarge(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	bin, dir := readDirLargeNative(t, "arm64-linux")
	cmd := runArm64Bin(qemu, bin)
	cmd.Dir = dir
	checkReadDirLarge(t, cmd)
}

func TestX86_64NativeReadDirLarge(t *testing.T) {
	runner := x86NativeRunner(t)
	bin, dir := readDirLargeNative(t, "x86-64-linux")
	cmd := benchX86Cmd(runner, bin)
	cmd.Dir = dir
	checkReadDirLarge(t, cmd)
}

func TestArm64DarwinNativeReadDirLarge(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("runs the binary, so only on Apple Silicon")
	}
	bin, dir := readDirLargeNative(t, "arm64-darwin")
	cmd := exec.Command(bin)
	cmd.Dir = dir
	checkReadDirLarge(t, cmd)
}
