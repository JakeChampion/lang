package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func selfHostSyncSource(prefix string, wasm bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	tail := `
    match (open_reader("/dev/zero")) {
        Ok(f) => {
            match (f.fsync())     { Ok(_) => { return 20; }, Err(_) => {} }
            match (f.fdatasync()) { Ok(_) => { return 21; }, Err(_) => {} }
            match (f.syncfs())    { Ok(_) => {}, Err(_) => { return 22; } }
            f.close();
        },
        Err(_) => { return 23; }
    }
    sync();`
	syncfsArm := `match (w.syncfs()) { Ok(_) => {}, Err(_) => { return 12; } }`
	closed := `match (w.fsync())     { Ok(_) => { return 14; }, Err(_) => {} }
            match (w.fdatasync()) { Ok(_) => { return 15; }, Err(_) => {} }`
	if wasm {
		tail = ""
		syncfsArm = `match (w.syncfs()) { Ok(_) => { return 12; }, Err(_) => {} }`
		closed = ""
	}
	return `function main(): i32 {
    match (open_writer("` + p("flushed.txt") + `")) {
        Ok(w) => {
            match (w.write("self-host\n")) { Ok(_) => {}, Err(_) => { return 10; } }
            match (w.fsync())     { Ok(_) => {}, Err(_) => { return 11; } }
            ` + syncfsArm + `
            match (w.fdatasync()) { Ok(_) => {}, Err(_) => { return 13; } }
            w.close();
            ` + closed + `
        },
        Err(_) => { return 16; }
    }` + tail + `
    return 0;
}
`
}

func selfHostSyncTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "flushed.txt"))
	if err != nil {
		t.Fatalf("read flushed.txt: %v", err)
	}
	if string(got) != "self-host\n" {
		t.Errorf("flushed.txt = %q, want %q", got, "self-host\n")
	}
}

func TestSelfHostSyncIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("sync test runs only natively (mutates host paths)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the character-device answers this pins were measured on Linux")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(selfHostSyncSource(work, false)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	for _, sym := range []string{"__fern_fd_fsync", "__fern_fd_fdatasync", "__fern_fd_syncfs", "__fern_sync"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("%s did not reach the IR runtime path (no %s in the asm)", sym, sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "sync_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("sync program exited %d, want 0 — the code names the step (see selfHostSyncSource)", code)
	}
	selfHostSyncTree(t, work)
}

func TestSelfHostSyncIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("sync test runs only natively (mutates host paths)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the character-device answers this pins were measured on Linux")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-target", "arm64-linux")
	cmd.Stdin = bytes.NewReader([]byte(selfHostSyncSource(work, false)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	for _, sym := range []string{"bl __fn___fern_fd_fsync", "bl __fn___fern_fd_fdatasync", "bl __fn___fern_fd_syncfs", "bl __fn___fern_sync"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("no `%s` in the emitted asm — it did not lower through the arm64 IR path", sym)
		}
	}
	progBin := buildBinArm64(t, arm64gcc, dir, "sync_prog", string(asm))
	run := runArm64Bin(qemu, progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("sync program exited %d, want 0 — the code names the step", code)
	}
	selfHostSyncTree(t, work)
}

func TestSelfHostSyncWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sync wasm IR e2e")
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
	cmd.Stdin = bytes.NewReader([]byte(selfHostSyncSource("", true)))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	for _, sym := range []string{"call $__fern_fd_fsync", "call $__fern_fd_fdatasync", "call $__fern_fd_syncfs"} {
		if !bytes.Contains(wat, []byte(sym)) {
			t.Fatalf("%q did not reach the wasm IR runtime path", sym)
		}
	}
	work := t.TempDir()
	watFile := filepath.Join(work, "sync_prog.wat")
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
		t.Fatalf("sync wasm program exited %d, want 0 — the code names the step\n--- WAT ---\n%s", code, wat)
	}
	selfHostSyncTree(t, work)
}
