package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const fsBuiltinsProgram = `function main(): i32 {
    let dir: string = "";
    match (temp_dir("fernfs")) {
        Ok(d) => { dir = d; },
        Err(_) => { return 1; }
    }
    let f: string = dir + "/hello.txt";
    match (write_file(f, "0123456789")) {
        Err(_) => { return 2; },
        Ok(_) => {}
    }
    match (stat(f)) {
        Ok(st) => {
            if (!st.is_file) { return 3; }
            if (st.is_dir) { return 4; }
            if (st.size != 10) { return 5; }
        },
        Err(_) => { return 6; }
    }
    match (read_dir(dir)) {
        Ok(entries) => {
            if (entries.len() != 1) { return 7; }
            if (entries[0] != "hello.txt") { return 8; }
        },
        Err(_) => { return 9; }
    }
    match (remove_dir_all(dir)) {
        Err(_) => { return 10; },
        Ok(_) => {}
    }
    match (stat(dir)) {
        Ok(_) => { return 11; },
        Err(_) => {}
    }
    return 42;
}`

const fsBuiltinsWant = "fs builtins program exited %d, want 42 (1=temp_dir,2=write,3=!is_file,4=is_dir,5=size,6=stat,7=count,8=name,9=read_dir,10=rm,11=still-exists)"

func TestSelfHostFsBuiltinsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(fsBuiltinsProgram))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "fsprog", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf(fsBuiltinsWant, code)
	}
}

func TestSelfHostFsBuiltinsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	fsAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(fsBuiltinsProgram), "-target", "arm64-linux")
	if len(fsAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the fs program")
	}
	fsBin := buildBin(t, arm64gcc, dir, "fsprog", string(fsAsm))

	cmd := runArm64Bin(qemu, fsBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf(fsBuiltinsWant, code)
	}
}
