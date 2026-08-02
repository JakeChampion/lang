package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostReaderWasmIR closes the streaming-reader wasm IR gap: the Reader
// intrinsics `r.read_chunk(n)` (-> Option[string]) and `r.close()` (-> the
// always-None Option[IoError]) now lower on the wasm IR path. A Reader is just
// its fd (stdin() = fd 0), so read_chunk reads stdin via preview1 fd_read into a
// fresh string block and boxes Some(chunk) / None-at-EOF, and close calls
// fd_close and boxes None — the same Option[string] / Option[IoError] ABIs the
// register backends' __fern_reader_read_chunk / __fern_reader_close produce, and
// the same string+Option layout env_func builds. Backs the std/io read_all_stdin
// loop on wasm. (open_reader/open_writer never lower on the IR path — they bail
// to the AST backend — so on wasm IR a Reader only ever wraps stdin.)
//
// Each case (a) pins the IR route (the WAT calls $__fern_reader_read_chunk /
// $__fern_reader_close) and (b) actually reads stdin under wasmtime, asserting
// the program's stdout and exit code. Distinct from the register-backend reader
// tests, which read a file via open_reader on the AST path.
func TestSelfHostReaderWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host reader wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name, src, stdin, wantOut string
		wantExit                  int
	}{
		// Two sized reads then close: first 5 bytes ("hello"), then the rest
		// (" world"). Proves read_chunk returns the requested-bounded chunk and
		// close returns None (no error).
		{"two_chunks_then_close", `function main(): i32 {
    var r: Reader = stdin();
    match (r.read_chunk(5)) { Some(s) => { write(s); write(":"); }, None => { return 4; } }
    match (r.read_chunk(20)) { Some(s) => { write(s); }, None => { return 5; } }
    match (r.close()) { Some(_) => { return 6; }, None => {} }
    return 0;
}`, "hello world", "hello: world", 0},
		// Drain to EOF: loop read_chunk until None, summing bytes. Exit code =
		// total bytes (7), proving None fires exactly at end-of-stream.
		{"drain_to_eof", `function main(): i32 {
    var r: Reader = stdin();
    var total: i32 = 0;
    var go: boolean = true;
    while (go) {
        match (r.read_chunk(3)) { Some(s) => { total = total + s.len(); }, None => { go = false; } }
    }
    return total;
}`, "abcdefg", "", 7},
		// Empty stdin: the very first read_chunk is None (nread == 0), so the
		// program takes the None arm immediately.
		{"empty_stdin_none", `function main(): i32 {
    var r: Reader = stdin();
    match (r.read_chunk(16)) { Some(_) => { return 1; }, None => { return 42; } }
    return 0;
}`, "", "", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_reader_read_chunk")) {
				t.Fatalf("%s: read_chunk did not reach the wasm IR runtime path (no call $__fern_reader_read_chunk)", tc.name)
			}
			// close() only appears in cases that call it.
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			run.Stdin = bytes.NewReader([]byte(tc.stdin))
			out, _ := run.Output()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: exit %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.wantExit, wat)
			}
			if got := string(out); got != tc.wantOut {
				t.Errorf("%s: stdout %q, want %q", tc.name, got, tc.wantOut)
			}
		})
	}
}
