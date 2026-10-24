package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostIoErrorIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host io-error wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write ok file: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	const missing = "missing-fern-probe"
	const missingLen = 18

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-notfound-variant", `function main(): i32 { match (read_file("` + missing + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } } } return 0; }`, 2},
		{"read-notfound-payload-len", `function main(): i32 { match (read_file("` + missing + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, missingLen},
		{"read-ok-control", `function main(): i32 { match (read_file("ok.txt")) { Ok(s) => { return s.len(); }, Err(_) => { return 90; } } return 0; }`, 6},
		{"writefile-notfound", `function main(): i32 { match (write_file("nodir-fern/x.txt", "hi")) { Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, Ok(_) => { return 1; } } return 0; }`, 2},
		{"writefile-ok-control", `function main(): i32 { match (write_file("out-fern.txt", "hi")) { Err(_) => { return 90; }, Ok(_) => { return 1; } } return 0; }`, 1},
		{"stat-notfound-payload-len", `function main(): i32 { match (stat("` + missing + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, missingLen},
		{"removefile-notfound", `function main(): i32 { match (remove_file("` + missing + `")) { Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, Ok(_) => { return 1; } } return 0; }`, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed to compile %s: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_build_io_error")) {
				t.Fatalf("%s: WAT has no call $__fern_build_io_error (error path not routed through the IoError builder)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
			run.Dir = dir
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: wasm IR exit = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
