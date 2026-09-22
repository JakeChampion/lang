package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A file-backed mmap makes all six operands observable, especially the last:
// offset zero maps different bytes. Anonymous mmap would ignore that operand.
// The negative-fd call also pins the signed errno result on Darwin and Linux.
// This is the internal runtime floor, not a new public builtin (#9853, #4451).
func syscall6Source(t *testing.T, dir, target string) string {
	t.Helper()
	const page = 65536 // aligned on both 4 KiB and 16 KiB page hosts
	data := make([]byte, page*2)
	data[page], data[len(data)-1] = 91, 37
	path := filepath.Join(dir, "mapped.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	open, atcwd, mmap, munmap, close := 257, -100, 9, 11, 3
	if target == "arm64-linux" {
		open, mmap, munmap, close = 56, 222, 215, 57
	} else if target == "arm64-darwin" {
		open, atcwd, mmap, munmap, close = 463, -2, 197, 73, 6
	}
	return fmt.Sprintf(`function main(): i32 {
    var path: string = "%s\0";
    var fd: i32 = __syscall4(%d, %d, __raw_data(path), 0, 0);
    if (fd < 0) { return 1; }
    var p: i32 = __syscall6(%d, 0, 65536, 1, 2, fd, 65536);
    var first: i32 = __raw_load8(p, 0);
    var last: i32 = __raw_load8(p, 65535);
    var unmapped: i32 = __syscall3(%d, p, 65536, 0);
    var closed: i32 = __syscall3(%d, fd, 0, 0);
    if (first != 91 || last != 37) { return 2; }
    if (unmapped != 0 || closed != 0) { return 3; }
    if (__syscall6(%d, 0, 65536, 1, 2, 0 - 1, 65536) != 0 - 9) { return 4; }
    return 0;
}
`, path, open, atcwd, mmap, munmap, close, mmap)
}

func TestSelfHostSyscall6IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	src := syscall6Source(t, dir, "x86-64-linux")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(src))
	bin := buildBin(t, gcc, dir, "syscall6", string(asm))
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("six-argument syscall: %v\n%s", err, out)
	}
}

func TestSelfHostSyscall6IRArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	src := syscall6Source(t, dir, "arm64-linux")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(src), "-target", "arm64-linux")
	bin := buildBinArm64(t, armgcc, dir, "syscall6", string(asm))
	if out, err := runArm64Bin(qemu, bin).CombinedOutput(); err != nil {
		t.Fatalf("six-argument syscall: %v\n%s", err, out)
	}
}

func TestSelfHostArm64DarwinSyscall6(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBinArm64Darwin(t, dir, "asm_ir_run.fern", "driver")
	cmd := exec.Command(driver, "-target", "arm64-darwin")
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	cmd.Stdin = strings.NewReader(syscall6Source(t, dir, "arm64-darwin"))
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("emit six-argument Darwin syscall: %v", err)
	}
	path := filepath.Join(dir, "syscall6.s")
	if err := os.WriteFile(path, asm, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "syscall6")
	if out, err := exec.Command("clang", "-nostdlib", "-Wl,-e,_main", "-lSystem", path, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link Darwin syscall probe: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("six-argument Darwin syscall: %v\n%s", err, out)
	}
}
