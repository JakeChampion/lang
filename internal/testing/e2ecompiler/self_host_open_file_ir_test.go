package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostOpenFileIRX86_64(t *testing.T) {
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

	target := filepath.Join(t.TempDir(), "openfile_out.txt")
	excl := filepath.Join(t.TempDir(), "openfile_excl.txt")
	prog := fmt.Sprintf(`function main(): i32 {
    match (open_writer("%s")) {
        Ok(w) => { w.write("hello "); w.close(); },
        Err(_) => { return 91; }
    }
    match (open_appender("%s")) {
        Ok(a) => { a.write("world\n"); a.close(); },
        Err(_) => { return 93; }
    }
    match (open_reader("%s")) {
        Ok(r) => { r.close(); },
        Err(_) => { return 95; }
    }
    match (open_exclusive("%s")) {
        Ok(x) => { x.write("EX"); x.close(); },
        Err(_) => { return 98; }
    }
    match (open_exclusive("%s")) {
        Ok(x2) => { x2.close(); return 99; },
        Err(ex) => {
            match (ex) {
                AlreadyExists(_) => {},
                _ => { return 100; }
            }
        }
    }
    match (open_reader("%s")) {
        Ok(r2) => { r2.close(); return 96; },
        Err(e) => {
            match (e) {
                NotFound(_) => {},
                _ => { return 97; }
            }
        }
    }
    return 42;
}`, target, target, target, excl, excl, filepath.Join(t.TempDir(), "no_such_file.txt"))

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the open-file program")
	}
	progBin := buildBin(t, gcc, dir, "open_file", string(asm))

	cmd := runX86_64Bin(runner, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("open-file program exit = %d, want 42 (open_writer/appender/reader/exclusive steps; #4372, #7758, #8776)", code)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back the written file: %v", err)
	}
	if string(got) != "hello world\n" {
		t.Errorf("file content = %q, want %q (open_writer create+truncate then open_appender append; #4372)", string(got), "hello world\n")
	}
}
