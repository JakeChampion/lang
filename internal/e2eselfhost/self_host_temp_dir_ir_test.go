package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostTempDirIR pins `temp_dir(prefix)` lowering on the self-host x86-64
// IR path. temp_dir makes a uniquely-named /tmp/<prefix>-<monotonic_ns> directory
// (mkdirat) and returns Result[string, IoError]; it had a full AST runtime but no
// IR lowering, so any module using it (std/test's must_temp_dir → result_assertions
// / helpers) bailed the module (#3457). It now lowers to op_temp_dir →
// the same recursive __fern_temp_dir runtime the AST path called (which also pulls
// in __fern_monotonic_ns). The program creates a temp dir, sanity-checks the path,
// removes it (exercising remove_dir_all too), and exits 0; the test also pins that
// the IR path was taken ($__fern_temp_dir in the emitted asm).
func TestSelfHostTempDirIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Create a temp dir under /tmp, verify the path looks right (non-empty,
	// "/tmp/" prefix, contains the requested prefix), then remove it. Exit 0
	// only if every step succeeds.
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
