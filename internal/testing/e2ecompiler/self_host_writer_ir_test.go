package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const writerStdoutProg = `function main(): i32 {
    let w: Writer = stdout();
    match (w.write("hello writer\n")) { Err(_) => { return 1; }, Ok(_) => {} }
    match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
    let e: Writer = stderr();
    match (e.write("err line\n")) { Err(_) => { return 3; }, Ok(_) => {} }
    match (e.close()) { Err(_) => { return 4; }, Ok(_) => {} }
    return 0;
}`

const writerErrProg = `function main(): i32 {
    match (open_writer("` + writerErrPath + `")) {
        Ok(w) => {
            match (w.close()) { Err(_) => { return 8; }, Ok(_) => {} }
            match (w.write("x")) {
                Err(e) => {
                    match (e) {
                        Other(p, _, _) => { if (p.len() == 0) { return 5; } return 6; },
                        _ => { return 7; },
                    }
                },
                Ok(_) => { return 1; },
            }
        },
        Err(_) => { return 9; },
    }
    return 0;
}`

const writerErrPath = "/tmp/fern-selfhost-writer-closed-probe.txt"

func TestSelfHostWriterIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(writerStdoutProg+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the Writer program")
	}
	if !bytes.Contains(asm, []byte("call __fn___fern_writer_write")) {
		t.Error("writer asm has no `call __fn___fern_writer_write` (#7926)")
	}
	progBin := buildBin(t, gcc, dir, "writer_stdout", string(asm))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	out, _ := cmd.Output()
	code := cmd.ProcessState.ExitCode()

	if !strings.Contains(string(out), "hello writer") {
		t.Errorf("stdout = %q, want it to contain %q (self-host stdout()/Writer.write, #4372)", string(out), "hello writer")
	}
	if code != 0 {
		t.Errorf("main returned %d, want 0 (every write and close answered None; #7926)", code)
	}

	errAsm := runCapture(t, gcc, runner, driverBin, []byte(writerErrProg+"\n"))
	if len(errAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the failing-write program")
	}
	errBin := buildBin(t, gcc, dir, "writer_closed", string(errAsm))
	var ecmd *exec.Cmd
	if len(runner) == 0 {
		ecmd = exec.Command(errBin)
	} else {
		ecmd = exec.Command(runner[0], append(runner[1:], errBin)...)
	}
	_ = ecmd.Run()
	if ecmd.ProcessState == nil || !ecmd.ProcessState.Exited() {
		t.Fatal("failing-write binary did not exit normally (segfault?)")
	}
	if code := ecmd.ProcessState.ExitCode(); code != 5 {
		t.Errorf("failing write exited %d, want 5 (Some(Other(\"\", _)); #7926)", code)
	}
}

func TestSelfHostWriterIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	compile := func(t *testing.T, name, src string) string {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src+"\n"), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("self-host arm64 compiler emitted 0 bytes for %s", name)
		}
		if !bytes.Contains(asm, []byte("bl __fn___fern_writer_write")) {
			t.Fatalf("%s asm has no `bl __fn___fern_writer_write` (#7926)", name)
		}
		return buildBinArm64(t, arm64gcc, dir, name, string(asm))
	}

	t.Run("stdout", func(t *testing.T) {
		bin := compile(t, "writer_stdout_arm64", writerStdoutProg)
		out, err := runArm64Bin(qemu, bin).Output()
		if err != nil {
			t.Fatalf("run writer program: %v", err)
		}
		if !strings.Contains(string(out), "hello writer") {
			t.Errorf("stdout = %q, want it to contain %q", string(out), "hello writer")
		}
	})

	t.Run("closed-fd", func(t *testing.T) {
		bin := compile(t, "writer_closed_arm64", writerErrProg)
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatal("failing-write binary did not exit normally (segfault?)")
		}
		if code := cmd.ProcessState.ExitCode(); code != 5 {
			t.Errorf("failing write exited %d, want 5 (Some(Other(\"\", _)); #7926)", code)
		}
	})
}
