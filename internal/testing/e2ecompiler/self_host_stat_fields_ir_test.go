package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const selfHostProbeMtime int64 = 1_600_000_000

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

func TestSelfHostStatFieldsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stat fields test runs only natively (stats host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	file, link := selfHostStatProbeTree(t, dir)
	src := selfHostStatFieldsSource(file, link, os.Geteuid(), os.Getegid())

	cmd := exec.Command(driverBin)
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

func TestSelfHostAccessAndIdsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("access test runs only natively (reads host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

	cmd := exec.Command(driverBin)
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

func TestSelfHostStatFieldsWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stat-fields wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	if err := os.WriteFile(filepath.Join(dir, "fields_target.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatalf("write target: %v", err)
	}
	when := time.Unix(selfHostProbeMtime, 0)
	if err := os.Chtimes(filepath.Join(dir, "fields_target.txt"), when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

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
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
