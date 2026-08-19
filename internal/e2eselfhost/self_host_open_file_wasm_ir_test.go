package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostOpenFileWasmIR covers the streaming file-I/O intrinsics
// open_writer / open_appender / open_reader (op_open_file, returning a bare fd) +
// Writer.write (op_writer_write) on the self-host WASM IR path (#4372 file half).
// Before this they deferred to the AST emitter, which had no fs-open runtime. Now
// wasm_ir emits $__fern_open_file (path_open under preopen fd 3, mapping the openat
// flags to WASI oflags/rights/fdflags) and $__fern_writer_write (fd_write). Runs
// under wasmtime with `--dir=.::/` granting the run dir as fd 3, and verifies the
// host file. Uses the raw fd intrinsics (`var w: i32 = open_writer(...)`), matching
// the register-path TestSelfHostOpenFileIR — the single-program driver resolves no
// std/io, so open_writer is the intrinsic (fd/-errno), not the Result[Writer,…] API.
func TestSelfHostOpenFileWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host open_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	run := func(t *testing.T, src string) int {
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

	// open_writer + Writer.write + close: writes "hello world" to ow.txt, returns 0.
	t.Run("writer", func(t *testing.T) {
		src := `function main(): i32 { var w: i32 = open_writer("ow.txt"); if (w < 0) { return 91; } var nw: i32 = w.write("hello world"); w.close(); if (nw < 0) { return 92; } return 0; }`
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

	// open_appender (O_APPEND): seed "AB", append "CD" → "ABCD".
	t.Run("appender", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "oa.txt"), []byte("AB"), 0o644); err != nil {
			t.Fatalf("seed oa.txt: %v", err)
		}
		src := `function main(): i32 { var a: i32 = open_appender("oa.txt"); if (a < 0) { return 93; } var na: i32 = a.write("CD"); a.close(); if (na < 0) { return 94; } return 0; }`
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

	// open_reader + read_chunk: reads a seeded file, returns its length. "hello
	// world" → 11.
	t.Run("reader", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "or.txt"), []byte("hello world"), 0o644); err != nil {
			t.Fatalf("seed or.txt: %v", err)
		}
		src := `function main(): i32 { var r: i32 = open_reader("or.txt"); if (r < 0) { return 95; } match (r.read_chunk(64)) { Some(s) => { r.close(); return s.len(); }, None => { r.close(); return 98; } } }`
		if code := run(t, src); code != 11 {
			t.Fatalf("reader exit %d, want 11", code)
		}
	})

	// Error path: open_reader on a missing file → fd < 0 → return 42.
	t.Run("open-error", func(t *testing.T) {
		src := `function main(): i32 { var r: i32 = open_reader("does_not_exist_xyz.txt"); if (r < 0) { return 42; } r.close(); return 0; }`
		if code := run(t, src); code != 42 {
			t.Fatalf("open-error exit %d, want 42 (fd < 0)", code)
		}
	})
}
