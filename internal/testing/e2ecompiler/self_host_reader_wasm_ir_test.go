package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostReaderWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host reader wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name, src, stdin, wantOut string
		wantExit                  int
	}{
		{"two_chunks_then_close", `function main(): i32 {
    let r: Reader = stdin();
    match (r.read_chunk(5)) { Ok(s) => { write(s); write(":"); }, Err(e) => { return 4; } }
    match (r.read_chunk(20)) { Ok(s) => { write(s); }, Err(e) => { return 5; } }
    match (r.close()) { Err(_) => { return 6; }, Ok(_) => {} }
    return 0;
}`, "hello world", "hello: world", 0},
		{"drain_to_eof", `function main(): i32 {
    let r: Reader = stdin();
    let total: i32 = 0;
    let go: boolean = true;
    while (go) {
        match (r.read_chunk(3)) { Ok(s) => { if (s.len() == 0) { go = false; } total = total + s.len(); }, Err(e) => { go = false; } }
    }
    return total;
}`, "abcdefg", "", 7},
		{"empty_stdin_eof", `function main(): i32 {
    let r: Reader = stdin();
    match (r.read_chunk(16)) { Ok(s) => { if (s.len() > 0) { return 1; } return 42; }, Err(e) => { return 2; } }
    return 0;
}`, "", "", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_reader_read_chunk")) {
				t.Fatalf("%s: read_chunk did not reach the wasm IR runtime path (no call $__fern_reader_read_chunk)", tc.name)
			}
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
