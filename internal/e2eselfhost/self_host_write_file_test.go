package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWriteFileX86_64 exercises the self-hosted x86-64
// emitter's `write_file` runtime builtin (std/test plan: the OS-syscall
// batch). It can't be cross-checked against a fixed value, so it
// round-trips: write a file, read it back, and compare — exercising
// both `__fern_write_file` (new) and `__fern_read_file` end-to-end
// through the self-host compiler.
func TestSelfHostWriteFileX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	path := filepath.Join(t.TempDir(), "wf_roundtrip.txt")
	prog := `function main(): i32 {
    var p: string = "` + path + `";
    match (write_file(p, "hello-selfhost")) {
        Err(_) => { return 1; },
        Ok(_) => {}
    }
    match (read_file(p)) {
        Ok(c) => {
            if (c == "hello-selfhost") { return 7; }
            return 2;
        },
        Err(_) => { return 3; }
    }
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "wf_roundtrip", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("write_file round-trip exited %d, want 7 (1=write err, 2=mismatch, 3=read err)", code)
	}
}
