package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The write-back family through the SELF-HOST IR path, on all three of its
// backends: `sync()` and the fsync / fdatasync / syncfs methods.
//
// `internal/e2eselfhost` is PRIMARY for a self-host lowering change
// (docs/TEST-GATES.md): the fixpoint is self-referential and cannot see a
// stable miscompile of a construct the compiler does not itself use, and
// nothing in the compiler flushes a file. #9124 landed a self-host op whose
// kind_tag every emitter carried and whose lowering arm did not exist, and
// every Go test passed.
//
// A call that lowered to nothing, or to the wrong syscall number, still links
// and still answers None — so the probe never accepts a bare `ok`. It pins
// each call to an answer only the RIGHT syscall gives: on a character device
// fsync and fdatasync are EINVAL where syncfs succeeds, and on a closed handle
// every one of them is EBADF. A helper that ignored its descriptor, or issued
// some other number, fails one of those.
func selfHostSyncSource(prefix string, wasm bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	// Neither WASI preview has a per-filesystem flush or a whole-machine one,
	// so the wasm leg asserts syncfs's refusal and never names `sync()`.
	tail := `
    match (open_reader("/dev/zero")) {
        Ok(f) => {
            match (f.fsync())     { None => { return 20; }, Some(_) => {} }
            match (f.fdatasync()) { None => { return 21; }, Some(_) => {} }
            match (f.syncfs())    { None => {}, Some(_) => { return 22; } }
            f.close();
        },
        Err(_) => { return 23; }
    }
    sync();`
	syncfsArm := `match (w.syncfs()) { None => {}, Some(_) => { return 12; } }`
	closed := `match (w.fsync())     { None => { return 14; }, Some(_) => {} }
            match (w.fdatasync()) { None => { return 15; }, Some(_) => {} }`
	if wasm {
		tail = ""
		syncfsArm = `match (w.syncfs()) { None => { return 12; }, Some(_) => {} }`
		// `close` drops the preview-2 descriptor resource, and a dropped
		// handle traps rather than answering EBADF.
		closed = ""
	}
	return `function main(): i32 {
    match (open_writer("` + p("flushed.txt") + `")) {
        Ok(w) => {
            match (w.write("self-host\n")) { None => {}, Some(_) => { return 10; } }
            match (w.fsync())     { None => {}, Some(_) => { return 11; } }
            ` + syncfsArm + `
            match (w.fdatasync()) { None => {}, Some(_) => { return 13; } }
            w.close();
            ` + closed + `
        },
        Err(_) => { return 16; }
    }` + tail + `
    return 0;
}
`
}

// selfHostSyncTree asserts what the probe left on disk through Go's own read
// rather than the compiler's, so a broken reader cannot make the program
// self-consistent.
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

// TestSelfHostSyncIR is the x86-64 leg: the self-host driver emits the asm,
// gcc links it, and the program runs against a real temporary directory.
func TestSelfHostSyncIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("sync test runs only natively (mutates host paths)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the character-device answers this pins were measured on Linux")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostSyncSource(work, false)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	// Each of the four has its own runtime symbol, so a lowering arm that was
	// never written shows up here rather than as a silent success.
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

// TestSelfHostSyncIRArm64 is the same probe through the arm64 IR backend under
// qemu: four different syscall numbers, and syncfs branches inline on Darwin,
// so none of it is taken on trust.
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
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

// TestSelfHostSyncWasmIR is the wasm leg. fsync and fdatasync are preview 1's
// fd_sync / fd_datasync; syncfs has no import to reach at all and must answer
// Unsupported, which is what the probe's inverted arm asserts.
func TestSelfHostSyncWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sync wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
