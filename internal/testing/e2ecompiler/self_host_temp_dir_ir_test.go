package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostTempDirIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-tempdir-ir")) {
        Ok(d) => {
            if (d.len() < 6) { return 1; }
            if (d[0] != 47) { return 2; }
            match (remove_dir_all(d)) {
                Ok(_) => { return 0; },
                Err(_) => { return 3; },
            }
        },
        Err(_) => { return 4; },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_temp_dir") {
		t.Fatal("temp_dir did not reach the IR runtime path (no __fern_temp_dir in asm)")
	}
	progBin := buildBin(t, gcc, dir, "tempdir_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("temp_dir IR program exited %d, want 0 (create + sanity-check + remove)", code)
	}
}
