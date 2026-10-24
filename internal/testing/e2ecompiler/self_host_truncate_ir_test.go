package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const selfHostTruncBig = 4294967297

func selfHostTruncateSource(prefix string) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	return fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "hello world\n")) { Ok(_) => {}, Err(_) => { return 1; } }

    // Shrink: the bytes past the new length are gone, the ones before it are
    // untouched.
    match (truncate(%[1]q, 5)) { Ok(_) => {}, Err(_) => { return 2; } }
    match (stat(%[1]q)) { Ok(f) => { if (f.size != (5 as i64)) { return 3; } }, Err(_) => { return 4; } }
    match (read_file(%[1]q)) { Ok(s) => { if (s != "hello") { return 5; } }, Err(_) => { return 6; } }

    // Grow: the extension is a hole that reads as zeros, and what was already
    // there keeps its bytes. This is the direction an fd-based builtin could
    // not express — every Fern open of an existing file for writing empties
    // it first.
    match (truncate(%[1]q, 20)) { Ok(_) => {}, Err(_) => { return 7; } }
    match (read_file_bytes(%[1]q)) {
        Ok(b) => {
            if (b.len() != 20) { return 8; }
            if (b[0] != 104) { return 9; }
            if (b[19] != 0) { return 10; }
        },
        Err(_) => { return 11; }
    }
    match (truncate(%[1]q, 5)) { Ok(_) => {}, Err(_) => { return 12; } }

    // It does not create, and a missing path names the kind rather than
    // answering a silent Ok.
    match (truncate(%[2]q, 3)) {
        Ok(_) => { return 13; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 14; } } }
    }
    match (stat(%[2]q)) { Ok(_) => { return 15; }, Err(_) => {} }

    // A negative length is the kernel's error rather than a clamp to zero,
    // which would resize the file to something the caller never named.
    match (truncate(%[1]q, 0 - 1)) { Ok(_) => { return 16; }, Err(_) => {} }
    match (stat(%[1]q)) { Ok(f) => { if (f.size != (5 as i64)) { return 17; } }, Err(_) => { return 18; } }

    // The length is 64 bits wide. Narrowed to 32 it would be 1 here.
    match (write_file(%[3]q, "x")) { Ok(_) => {}, Err(_) => { return 19; } }
    match (truncate(%[3]q, %[4]d)) { Ok(_) => {}, Err(_) => { return 20; } }
    match (stat(%[3]q)) { Ok(f) => { if (f.size != (%[4]d as i64)) { return 21; } }, Err(_) => { return 22; } }
    return 0;
}
`, p("t.txt"), p("missing.txt"), p("big.txt"), selfHostTruncBig)
}

func selfHostTruncateTree(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "t.txt"))
	if err != nil {
		t.Fatalf("stat t.txt: %v", err)
	}
	if fi.Size() != 5 {
		t.Errorf("t.txt size = %d, want 5", fi.Size())
	}
	got, err := os.ReadFile(filepath.Join(dir, "t.txt"))
	if err != nil {
		t.Fatalf("read t.txt: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("t.txt = %q, want %q — the shrink kept the wrong bytes", got, "hello")
	}
	big, err := os.Stat(filepath.Join(dir, "big.txt"))
	if err != nil {
		t.Fatalf("stat big.txt: %v", err)
	}
	if big.Size() != selfHostTruncBig {
		t.Errorf("big.txt size = %d, want %d — the length was narrowed on its way to the kernel",
			big.Size(), selfHostTruncBig)
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Errorf("missing.txt exists (lstat err = %v) — truncate created a file", err)
	}
}

func TestSelfHostTruncateIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("truncate test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(selfHostTruncateSource(work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fern_truncate")) {
		t.Fatal("truncate did not reach the IR runtime path (no __fern_truncate in the asm)")
	}
	progBin := buildBin(t, gcc, dir, "truncate_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("truncate program exited %d, want 0 — the code names the step (see selfHostTruncateSource)", code)
	}
	selfHostTruncateTree(t, work)
}

func TestSelfHostTruncateIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("truncate test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-target", "arm64-linux")
	cmd.Stdin = bytes.NewReader([]byte(selfHostTruncateSource(work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_truncate")) {
		t.Fatal("no `bl __fn___fern_truncate` in the emitted asm — it did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "truncate_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("truncate arm64 program exited %d, want 0 — the code names the step (see selfHostTruncateSource)", code)
	}
	selfHostTruncateTree(t, work)
}

func TestSelfHostTruncateWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host truncate wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(selfHostTruncateSource("")))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_truncate")) {
		t.Fatal("truncate did not reach the wasm IR runtime path (no call $__fern_truncate in WAT)")
	}
	work := t.TempDir()
	watFile := filepath.Join(work, "truncate_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = work
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("truncate wasm program exited %d, want 0 — the code names the step\n--- WAT ---\n%s", code, wat)
	}
	selfHostTruncateTree(t, work)
}
