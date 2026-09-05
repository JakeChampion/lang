package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The mtime the probes below pin. A fixed instant is what makes a WRONG
// timestamp field visible: the self-host reads all three from one per-target
// offset table, and on Darwin st_birthtimespec sits between st_ctimespec and
// st_size, so an off-by-one row still produces a plausible number.
const selfHostProbeMtime int64 = 1_600_000_000

// selfHostStatProbeTree writes the file the probes read: 0640, five bytes, a
// pinned mtime, and a hard link beside it so (dev, ino) and nlink have
// something to say.
func selfHostStatProbeTree(t *testing.T, dir string) (file, link string) {
	t.Helper()
	file = filepath.Join(dir, "fields_target.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	link = filepath.Join(dir, "fields_link.txt")
	if err := os.Link(file, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	when := time.Unix(selfHostProbeMtime, 0)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return file, link
}

// selfHostStatFieldsSource asserts the fields the self-host reads out of the
// kernel's `struct stat`. Each failure returns its own exit code.
func selfHostStatFieldsSource(file, link string, euid, egid int) string {
	return fmt.Sprintf(`function main(): i32 {
    match (stat(%[1]q)) {
        Err(_) => { return 30; },
        Ok(f) => {
            match (stat(%[2]q)) {
                Err(_) => { return 31; },
                Ok(l) => {
                    if ((f.mode & (511 as u32)) != (416 as u32)) { return 1; }
                    if ((f.mode & (61440 as u32)) != (32768 as u32)) { return 2; }
                    if (f.size != (5 as i64)) { return 3; }
                    if (f.mtime != (%[3]d as i64)) { return 4; }
                    if (f.atime != (%[3]d as i64)) { return 5; }
                    if (f.mtime_nsec < (0 as i64)) { return 6; }
                    if (f.mtime_nsec > (999999999 as i64)) { return 7; }
                    if (f.ctime <= (0 as i64)) { return 8; }
                    if (f.nlink != (2 as u32)) { return 9; }
                    if (f.ino != l.ino) { return 10; }
                    if (f.dev != l.dev) { return 11; }
                    if (f.uid != (%[4]d as u32)) { return 12; }
                    if (f.gid != (%[5]d as u32)) { return 13; }
                    if (f.rdev != (0 as i64)) { return 14; }
                    if (f.blksize <= (0 as i64)) { return 15; }
                    if (f.blocks < (0 as i64)) { return 16; }
                    return 0;
                },
            }
        },
    }
}
`, file, link, selfHostProbeMtime, euid, egid)
}

// TestSelfHostStatFieldsIR is the self-host half of the same gate the native
// backends carry: a known file read through the whole `stat(2)` record.
//
// The self-host builds FileStat from one Fern body shared by all three targets
// (`asmcore.rt_src_stat`), with `statoff` / `statkind` carrying the entire
// per-target difference — offsets AND widths, since Darwin's st_mode is 16-bit
// where Linux's is 32. A wrong row there is silent: every offset is inside a
// buffer the kernel filled, so it reads a real number out of the wrong field.
// Only values a test can arrange — a chmod, a chtimes, a hard link — catch it.
func TestSelfHostStatFieldsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stat fields test runs only natively (stats host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	file, link := selfHostStatProbeTree(t, dir)
	src := selfHostStatFieldsSource(file, link, os.Geteuid(), os.Getegid())

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	progBin := buildBin(t, gcc, dir, "stat_fields_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("stat fields program exited %d, want 0 — the code names the field (see selfHostStatFieldsSource)", code)
	}
}

// TestSelfHostAccessAndIdsIR pins the three primitives that answer a shell
// test's permission and ownership questions on the self-host x86-64 IR path.
//
// `access` has to ask about the EFFECTIVE ids, so the emitted call carries
// AT_EACCESS — and the flag's value differs between Linux (0x200) and Darwin
// (0x10), which is the kind of constant that silently asks a different
// question. What this can prove without a set-uid binary is that the flags word
// is one the kernel accepts at all: without it the syscall answers EINVAL or
// ENOSYS and every case fails at once. The X_OK cases carry signal whatever uid
// the suite runs as, since the execute bit is checked even for root.
func TestSelfHostAccessAndIdsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("access test runs only natively (reads host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	readable := filepath.Join(dir, "readable.txt")
	if err := os.WriteFile(readable, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(readable, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	runnable := filepath.Join(dir, "runnable.sh")
	if err := os.WriteFile(runnable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(runnable, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	missing := filepath.Join(dir, "not-here.txt")

	src := fmt.Sprintf(`function ok(p: string, m: i32): boolean {
    match (access(p, m)) {
        Ok(_) => { return true; },
        Err(e) => { return false; },
    }
    return false;
}
function main(): i32 {
    if (!ok(%[1]q, 0)) { return 1; }
    if (ok(%[3]q, 0)) { return 2; }
    if (!ok(%[1]q, 4)) { return 3; }
    if (ok(%[1]q, 1)) { return 4; }
    if (!ok(%[2]q, 1)) { return 5; }
    if (!ok(%[2]q, 7)) { return 6; }
    if (geteuid() != %[4]d) { return 7; }
    if (getegid() != %[5]d) { return 8; }
    return 0;
}
`, readable, runnable, missing, os.Geteuid(), os.Getegid())

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fern_access")) {
		t.Fatal("access did not reach the IR runtime path (no __fern_access in asm)")
	}
	progBin := buildBin(t, gcc, dir, "access_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("access program exited %d, want 0 — the code names the case", code)
	}
}

// TestSelfHostStatFieldsWasmIR is the preview-1 half: the self-host's wasm
// emitter writes the FileStat slots by hand, and preview 1 answers only some of
// them. The fields it has no record for read ZERO, which is the checker's
// documented contract, so the zeros are the assertion — a body that left the
// tail of the struct as whatever the allocator handed back would fail this
// exactly as a wrong value would.
func TestSelfHostStatFieldsWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stat-fields wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	if err := os.WriteFile(filepath.Join(dir, "fields_target.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatalf("write target: %v", err)
	}
	when := time.Unix(selfHostProbeMtime, 0)
	if err := os.Chtimes(filepath.Join(dir, "fields_target.txt"), when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := fmt.Sprintf(`function main(): i32 {
    match (stat("fields_target.txt")) {
        Err(_) => { return 20; },
        Ok(f) => {
            if (f.size != (5 as i64)) { return 1; }
            if (f.mtime != (%[1]d as i64)) { return 2; }
            if (f.mtime_nsec < (0 as i64)) { return 3; }
            if (f.mtime_nsec > (999999999 as i64)) { return 4; }
            if (f.mode != (0 as u32)) { return 5; }
            if (f.uid != (0 as u32)) { return 6; }
            if (f.gid != (0 as u32)) { return 7; }
            if (f.rdev != (0 as i64)) { return 8; }
            if (f.blksize != (0 as i64)) { return 9; }
            if (f.blocks != (0 as i64)) { return 10; }
            if (f.nlink != (1 as u32)) { return 11; }
            return 0;
        },
    }
}
`, selfHostProbeMtime)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "stat_fields_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("stat fields wasm program exited %d, want 0 — the code names the field\n--- WAT ---\n%s", code, wat)
	}
}
