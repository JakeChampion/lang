package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostOpenFileWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host open_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	run := func(t *testing.T, src string) int {
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
		if !bytes.Contains(wat, []byte("$__fern_open_file")) {
			t.Fatalf("no $__fern_open_file — did not lower through the wasm IR path:\n%s", wat)
		}
		watFile := filepath.Join(dir, "of.wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		rc := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
		rc.Dir = dir
		_ = rc.Run()
		if rc.ProcessState == nil || !rc.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally:\n%s", wat)
		}
		return rc.ProcessState.ExitCode()
	}

	t.Run("writer", func(t *testing.T) {
		src := `function main(): i32 { match (open_writer("ow.txt")) { Ok(w) => { match (w.write("hello world")) { Err(_) => { return 92; }, Ok(_) => {} } w.close(); }, Err(_) => { return 91; } } return 0; }`
		if code := run(t, src); code != 0 {
			t.Fatalf("writer exit %d, want 0", code)
		}
		got, err := os.ReadFile(filepath.Join(dir, "ow.txt"))
		if err != nil {
			t.Fatalf("read ow.txt: %v", err)
		}
		if string(got) != "hello world" {
			t.Errorf("writer wrote %q, want %q", got, "hello world")
		}
	})

	t.Run("appender", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "oa.txt"), []byte("AB"), 0o644); err != nil {
			t.Fatalf("seed oa.txt: %v", err)
		}
		src := `function main(): i32 { match (open_appender("oa.txt")) { Ok(a) => { a.write("CD"); a.close(); }, Err(_) => { return 93; } } return 0; }`
		if code := run(t, src); code != 0 {
			t.Fatalf("appender exit %d, want 0", code)
		}
		got, err := os.ReadFile(filepath.Join(dir, "oa.txt"))
		if err != nil {
			t.Fatalf("read oa.txt: %v", err)
		}
		if string(got) != "ABCD" {
			t.Errorf("appender produced %q, want %q", got, "ABCD")
		}
	})

	t.Run("exclusive", func(t *testing.T) {
		_ = os.Remove(filepath.Join(dir, "ox.txt"))
		src := `function main(): i32 { match (open_exclusive("ox.txt")) { Ok(w) => { w.write("EX"); w.close(); }, Err(_) => { return 91; } } match (open_exclusive("ox.txt")) { Ok(w2) => { w2.close(); return 92; }, Err(e) => { match (e) { AlreadyExists(_) => {}, _ => { return 93; } } } } return 0; }`
		if code := run(t, src); code != 0 {
			t.Fatalf("exclusive exit %d, want 0 (92 = the second open succeeded, 93 = EEXIST was not AlreadyExists)", code)
		}
		got, err := os.ReadFile(filepath.Join(dir, "ox.txt"))
		if err != nil {
			t.Fatalf("read ox.txt: %v", err)
		}
		if string(got) != "EX" {
			t.Errorf("exclusive produced %q, want %q — the refused open truncated it", got, "EX")
		}
	})

	t.Run("reader", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "or.txt"), []byte("hello world"), 0o644); err != nil {
			t.Fatalf("seed or.txt: %v", err)
		}
		src := `function main(): i32 { match (open_reader("or.txt")) { Ok(r) => { match (r.read_chunk(64)) { Ok(s) => { r.close(); return s.len(); }, Err(e) => { r.close(); return 98; } } }, Err(_) => { return 95; } } return 99; }`
		if code := run(t, src); code != 11 {
			t.Fatalf("reader exit %d, want 11", code)
		}
	})

	t.Run("open-error", func(t *testing.T) {
		src := `function main(): i32 { match (open_reader("does_not_exist_xyz.txt")) { Ok(r) => { r.close(); return 0; }, Err(e) => { match (e) { NotFound(_) => { return 42; }, _ => { return 43; } } } } return 1; }`
		if code := run(t, src); code != 42 {
			t.Fatalf("open-error exit %d, want 42 (Err(NotFound); #7758)", code)
		}
	})

	t.Run("reader-read-chunks", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "rl.txt"), []byte("a\nbb\n"), 0o644); err != nil {
			t.Fatalf("seed rl.txt: %v", err)
		}
		src := `function main(): i32 { match (open_reader("rl.txt")) { Ok(r) => { let n: i32 = 0; while (true) { match (r.read_chunk_bytes(2)) { Ok(b) => { if (b.len() == 0) { r.close(); return n; } n = n + b.len(); }, Err(_) => { r.close(); return 92; } } } r.close(); return n; }, Err(_) => { return 90; } } return 91; }`
		if code := run(t, src); code != 5 {
			t.Fatalf("reader-read-chunks exit %d, want 5 (r.read_chunk_bytes reads the file, not stdin; #7758)", code)
		}
	})
}
