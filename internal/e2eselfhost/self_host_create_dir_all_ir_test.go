package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostCreateDirAllIR pins `create_dir_all(path)` lowering on the
// self-host x86-64 IR path (#6749). It is the one filesystem builtin that can
// BUILD a directory tree, so `fern -vendor` — and any program that writes into
// a layout it does not already have — is gated on it.
//
// The program creates a chain two levels deeper than anything that exists,
// writes into the leaf (the proof the WHOLE chain is there: a walk that created
// only the last component leaves a path write_file cannot reach), re-creates it
// to pin that an existing path is Ok rather than EEXIST, and finally asks for a
// chain under a regular file to pin that a real failure is still Err.
func TestSelfHostCreateDirAllIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-mkdirp-ir")) {
        Ok(d) => {
            match (create_dir_all(d + "/one/two/three")) { Err(_) => { return 1; }, Ok(_) => {}, }
            match (write_file(d + "/one/two/three/deep.txt", "deep")) { Err(_) => { return 2; }, Ok(_) => {}, }
            match (read_file(d + "/one/two/three/deep.txt")) {
                Ok(s) => { if (s != "deep") { return 3; } },
                Err(_) => { return 4; },
            }
            match (create_dir_all(d + "/one/two/three")) { Err(_) => { return 5; }, Ok(_) => {}, }
            match (write_file(d + "/blocker", "x")) { Err(_) => { return 6; }, Ok(_) => {}, }
            match (create_dir_all(d + "/blocker/inner")) { Ok(_) => { return 7; }, Err(_) => {}, }
            match (remove_dir_all(d)) { Err(_) => { return 8; }, Ok(_) => {}, }
            return 0;
        },
        Err(_) => { return 9; },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_create_dir_all") {
		t.Fatal("create_dir_all did not reach the IR runtime path (no __fern_create_dir_all in asm)")
	}
	progBin := buildBin(t, gcc, dir, "mkdirp_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("create_dir_all IR program exited %d, want 0", code)
	}
}
