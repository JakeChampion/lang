package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostReadErrorWasmIR runs the wasm stdin readers with a directory as
// stdin, so every fd_read fails (EISDIR) and must read as end of input. The
// helpers take the byte count fd_read writes to scratch address 8; on an error
// nothing is written there, and a count left by an earlier call (the `write`
// each program starts with) was taken for bytes read: read_line returned
// Some, read_all_stdin a one-byte string.
func TestSelfHostReadErrorWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm read-error e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name, src string
		wantExit  int
	}{
		{"read_line_is_none", `function main(): i32 {
    write("x");
    match (read_line()) { Some(_) => { return 1; }, None => { return 0; } }
    return 9;
}`, 0},
		{"read_all_stdin_is_empty", `function main(): i32 {
    write("x");
    return read_all_stdin().len();
}`, 0},
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
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			stdin, err := os.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			run := exec.Command("wasmtime", "run", watFile)
			run.Stdin = stdin
			out, _ := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: exit %d, want %d\n%s", tc.name, code, tc.wantExit, out)
			}
		})
	}
}
