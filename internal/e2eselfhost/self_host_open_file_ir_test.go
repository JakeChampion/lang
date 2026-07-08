package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4372 (file half): open_reader / open_writer / open_appender must lower on the
// self-host x86-64 IR path. They complete the streaming Reader/Writer surface (the
// stream half — stdout()/stderr()/Writer.write — landed already). A Reader/Writer
// is a bare fd, so all three lower to one op_open_file carrying the openat flags
// (O_RDONLY=0 / O_WRONLY|O_CREAT|O_TRUNC=577 / O_WRONLY|O_CREAT|O_APPEND=1089),
// backed by the __fern_open_fd runtime (NUL-terminate the path, openat, return the
// fd). This program creates a file with open_writer, appends with open_appender,
// re-opens with open_reader, and the test reads the file back — proving all three
// openat flag paths + Writer.write + close work end-to-end through the IR backend.
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
    var w: i32 = open_writer("%s");
    if (w < 0) { return 91; }
    var nw: i32 = w.write("hello ");
    w.close();
    if (nw < 0) { return 92; }
    var a: i32 = open_appender("%s");
    if (a < 0) { return 93; }
    var na: i32 = a.write("world\n");
    a.close();
    if (na < 0) { return 94; }
    var r: i32 = open_reader("%s");
    if (r < 0) { return 95; }
    r.close();
    return 42;
}`, target, target, target)

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the open-file program")
	}
	progBin := buildBin(t, gcc, dir, "open_file", string(asm))

	cmd := exec.Command(progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("open-file program exit = %d, want 42 (open_writer/appender/reader steps; #4372)", code)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back the written file: %v", err)
	}
	if string(got) != "hello world\n" {
		t.Errorf("file content = %q, want %q (open_writer create+truncate then open_appender append; #4372)", string(got), "hello world\n")
	}
}
