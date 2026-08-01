package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// fsBuiltinsProgram is the shared end-to-end exercise for the fs
// builtins: make a temp dir, write a file, stat it, list the dir,
// remove the tree, and confirm it's gone. Returns 42 on full success.
const fsBuiltinsProgram = `function main(): i32 {
    var dir: string = "";
    match (temp_dir("fernfs")) {
        Ok(d) => { dir = d; },
        Err(_) => { return 1; }
    }
    var f: string = dir + "/hello.txt";
    match (write_file(f, "0123456789")) {
        Some(_) => { return 2; },
        None => {}
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
        Some(_) => { return 10; },
        None => {}
    }
    match (stat(dir)) {
        Ok(_) => { return 11; },
        Err(_) => {}
    }
    return 42;
}`

const fsBuiltinsWant = "fs builtins program exited %d, want 42 (1=temp_dir,2=write,3=!is_file,4=is_dir,5=size,6=stat,7=count,8=name,9=read_dir,10=rm,11=still-exists)"

// TestSelfHostFsBuiltinsX86_64 exercises the self-hosted x86-64
// emitter's filesystem builtins end-to-end: temp_dir, write_file, stat,
// read_dir, and remove_dir_all. The compiled program makes a temp dir,
// writes a file into it, stats the file (is_file / is_dir / size),
// lists the directory, removes the tree, and confirms the directory is
// gone — returning 42 only if every step matches. Any mismatch returns
// a distinct small code to localise the failure.
func TestSelfHostFsBuiltinsX86_64(t *testing.T) {
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

// TestSelfHostFsBuiltinsArm64 is the ARM64 counterpart: the same
// temp_dir / write_file / stat / read_dir / remove_dir_all exercise,
// compiled to aarch64 by the self-hosted ARM64 emitter and run under
// qemu-aarch64 (which passes the filesystem syscalls through to the
// host). CI-gated; skips cleanly without the cross toolchain.
func TestSelfHostFsBuiltinsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	fsAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(fsBuiltinsProgram), "-target", "arm64")
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
