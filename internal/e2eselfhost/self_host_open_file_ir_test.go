package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4372 (file half) / #7758: open_reader / open_writer / open_appender must lower
// on the self-host x86-64 IR path, with NATIVE's signature — Result[Reader, IoError]
// / Result[Writer, IoError], matched rather than sign-tested. All three lower to one
// op_open_file carrying the openat flags (O_RDONLY=0 / O_WRONLY|O_CREAT|O_TRUNC=577 /
// O_WRONLY|O_CREAT|O_APPEND=1089), backed by the __fern_open_res runtime
// (NUL-terminate the path, openat, then Ok(fd) / Err(io_error)). The Ok payload is
// the bare fd a Reader/Writer is represented by, so the bound name dispatches the
// resource intrinsics directly.
//
// This program creates a file with open_writer, appends with open_appender, re-opens
// with open_reader, and finally opens a path that does not exist and matches the
// IoError variant — proving all three openat flag paths, Writer.write, close, and
// BOTH Result arms (including the Err payload being a real matchable IoError, not a
// raw errno) work end-to-end through the IR backend. Byte-identical to what the
// native compiler runs: `bin/fern -interp` on this source also exits 42.
func TestSelfHostOpenFileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host open-file test runs host-native only (real filesystem)")
	}
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	target := filepath.Join(t.TempDir(), "openfile_out.txt")
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
}`, target, target, target, filepath.Join(t.TempDir(), "no_such_file.txt"))

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the open-file program")
	}
	progBin := buildBin(t, gcc, dir, "open_file", string(asm))

	cmd := exec.Command(progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("open-file program exit = %d, want 42 (open_writer/appender/reader steps; #4372, #7758)", code)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back the written file: %v", err)
	}
	if string(got) != "hello world\n" {
		t.Errorf("file content = %q, want %q (open_writer create+truncate then open_appender append; #4372)", string(got), "hello world\n")
	}
}
