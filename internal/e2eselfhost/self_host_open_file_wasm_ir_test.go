package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostOpenFileWasmIR covers the streaming file-I/O intrinsics
// open_writer / open_appender / open_reader (op_open_file) + Writer.write
// (op_writer_write) on the self-host WASM IR path (#4372 file half, #7758).
// wasm_ir emits $__fern_open_file (path_open under preopen fd 3, mapping the openat
// flags to WASI oflags/rights/fdflags, then boxing Ok(fd) / Err($__fern_build_io_error))
// and $__fern_writer_write (fd_write). Runs under wasmtime with `--dir=.::/` granting
// the run dir as fd 3, and verifies the host file.
//
// The programs use NATIVE's signature — `match (open_writer(p)) { Ok(w) => .., Err(e)
// => .. }` over Result[Writer, IoError] — which is the whole point of #7758: the
// self-host used to hand back a bare fd here and refuse the match the native
// compiler runs. Each source below exits identically under `bin/fern -interp`.
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
		src := `function main(): i32 { match (open_writer("ow.txt")) { Ok(w) => { w.write("hello world"); w.close(); }, Err(_) => { return 91; } } return 0; }`
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

	// open_reader + read_chunk: reads a seeded file, returns its length. "hello
	// world" → 11.
	t.Run("reader", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "or.txt"), []byte("hello world"), 0o644); err != nil {
			t.Fatalf("seed or.txt: %v", err)
		}
		src := `function main(): i32 { match (open_reader("or.txt")) { Ok(r) => { match (r.read_chunk(64)) { Some(s) => { r.close(); return s.len(); }, None => { r.close(); return 98; } } }, Err(_) => { return 95; } } return 99; }`
		if code := run(t, src); code != 11 {
			t.Fatalf("reader exit %d, want 11", code)
		}
	})

	// Error path: open_reader on a missing file → Err, and the payload is a real
	// IoError variant (NotFound), not a raw errno — the same value native produces.
	t.Run("open-error", func(t *testing.T) {
		src := `function main(): i32 { match (open_reader("does_not_exist_xyz.txt")) { Ok(r) => { r.close(); return 0; }, Err(e) => { match (e) { NotFound(_) => { return 42; }, _ => { return 43; } } } } return 1; }`
		if code := run(t, src); code != 42 {
			t.Fatalf("open-error exit %d, want 42 (Err(NotFound); #7758)", code)
		}
	})

	// r.read_line() reads the RECEIVER's fd, not stdin: a file Reader's lines are
	// the file's. "a\nbb\n" is 2 + 3 bytes with newlines included → 5 (#7758).
	t.Run("reader-read-line", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "rl.txt"), []byte("a\nbb\n"), 0o644); err != nil {
			t.Fatalf("seed rl.txt: %v", err)
		}
		src := `function main(): i32 { match (open_reader("rl.txt")) { Ok(r) => { var n: i32 = 0; while (true) { match (r.read_line()) { Some(l) => { n = n + l.len(); }, None => { r.close(); return n; } } } r.close(); return n; }, Err(_) => { return 90; } } return 91; }`
		if code := run(t, src); code != 5 {
			t.Fatalf("reader-read-line exit %d, want 5 (r.read_line reads the file, not stdin; #7758)", code)
		}
	})
}
