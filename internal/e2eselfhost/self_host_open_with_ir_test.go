package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// open_reader_with / open_writer_with on the self-host IR path, with the
// flags word carried as an operand and translated by __fern_open_with per
// target: the create bit creates without truncating, its absence is
// NotFound, and the non-blocking bit is what lets a FIFO with no peer be
// opened — the reader's open returns where it would wait, the writer's is
// ENXIO where it would wait. Every failure returns its own exit code.
func selfHostOpenWithSource(dir string) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	return fmt.Sprintf(`function main(): i32 {
    match (open_writer_with(%[1]q, 1)) { Ok(w) => { w.write("abc"); w.close(); }, Err(_) => { return 1; } }
    match (open_writer_with(%[1]q, 1)) { Ok(w) => { w.write("Z"); w.close(); }, Err(_) => { return 2; } }
    match (read_file(%[1]q)) { Ok(s) => { if (s != "Zbc") { return 3; } }, Err(_) => { return 4; } }
    match (open_writer_with(%[2]q, 0)) { Ok(w) => { w.close(); return 5; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 6; } } } }
    match (open_reader_with(%[2]q, 2)) { Ok(r) => { r.close(); return 7; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 8; } } } }
    match (open_reader_with(%[1]q, 0)) {
        Ok(r) => {
            match (r.read_chunk(3)) { Ok(s) => { if (s != "Zbc") { return 9; } }, Err(_) => { return 10; } }
            r.close();
        },
        Err(_) => { return 11; }
    }
    match (mknod(%[3]q, 4534, 0, 0)) { Ok(_) => {}, Err(_) => { return 12; } }
    match (open_reader_with(%[3]q, 2)) { Ok(r) => { r.close(); }, Err(_) => { return 13; } }
    match (open_writer_with(%[3]q, 2)) { Ok(w) => { w.close(); return 14; }, Err(e) => { match (e) { NotFound(_) => { return 15; }, _ => {} } } }
    return 0;
}
`, p("w.txt"), p("missing.txt"), p("fifo"))
}

// The wasm leg has no FIFO to open; it is the file half under the preopen.
const selfHostOpenWithWasmSource = `function main(): i32 {
    match (open_writer_with("w.txt", 1)) { Ok(w) => { w.write("abc"); w.close(); }, Err(_) => { return 1; } }
    match (open_writer_with("w.txt", 1)) { Ok(w) => { w.write("Z"); w.close(); }, Err(_) => { return 2; } }
    match (read_file("w.txt")) { Ok(s) => { if (s != "Zbc") { return 3; } }, Err(_) => { return 4; } }
    match (open_writer_with("missing.txt", 0)) { Ok(w) => { w.close(); return 5; }, Err(e) => { match (e) { NotFound(_) => {}, _ => { return 6; } } } }
    match (open_reader_with("w.txt", 2)) {
        Ok(r) => {
            match (r.read_chunk(3)) { Ok(s) => { if (s != "Zbc") { return 7; } }, Err(_) => { return 8; } }
            r.close();
        },
        Err(_) => { return 9; }
    }
    return 0;
}
`

func selfHostOpenWithTree(t *testing.T, dir string, fifo bool) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "w.txt"))
	if err != nil {
		t.Fatalf("read w.txt: %v", err)
	}
	if string(got) != "Zbc" {
		t.Errorf("w.txt = %q, want %q — the second open truncated or appended", got, "Zbc")
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Errorf("missing.txt exists (lstat err = %v) — an open without the create bit created it", err)
	}
	if fifo {
		fi, err := os.Lstat(filepath.Join(dir, "fifo"))
		if err != nil {
			t.Fatalf("lstat fifo: %v", err)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("fifo is not a named pipe: %v", fi.Mode())
		}
	}
}

func TestSelfHostOpenWithIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("open_with test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostOpenWithSource(work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fern_open_with")) {
		t.Fatal("open_*_with did not reach the IR runtime path (absent from the asm)")
	}
	progBin := buildBin(t, gcc, dir, "open_with_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("open_with program exited %d, want 0 — the code names the step (see selfHostOpenWithSource)", code)
	}
	selfHostOpenWithTree(t, work, true)
}

func TestSelfHostOpenWithIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("open_with test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostOpenWithSource(work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_open_with")) {
		t.Fatal("no `bl __fn___fern_open_with` in the emitted asm — it did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "open_with_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("open_with arm64 program exited %d, want 0 — the code names the step (see selfHostOpenWithSource)", code)
	}
	selfHostOpenWithTree(t, work, true)
}

func TestSelfHostOpenWithWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host open_with wasm IR e2e")
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
	cmd.Stdin = bytes.NewReader([]byte(selfHostOpenWithWasmSource))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_open_with")) {
		t.Fatal("open_*_with did not reach the wasm IR runtime path (no call $__fern_open_with in WAT)")
	}
	work := t.TempDir()
	watFile := filepath.Join(work, "open_with_prog.wat")
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
		t.Fatalf("open_with wasm program exited %d, want 0 — the code names the step\n--- WAT ---\n%s", code, wat)
	}
	selfHostOpenWithTree(t, work, false)
}
