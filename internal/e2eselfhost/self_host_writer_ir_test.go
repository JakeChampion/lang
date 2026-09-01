package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4372 (stream half): the streaming-I/O WRITE surface must lower on the self-host
// x86-64 IR path. The read half (stdin / Reader.read_chunk / Reader.close) already
// lowered; stdout() / stderr() and Writer.write had no lowering, so a program using
// them bailed to the legacy AST backend and failed to link. This drives a program
// that writes to stdout and stderr through their Writers (fds 1 / 2), captures the
// stdout bytes, and checks the returned byte count — proving stdout()/stderr()
// (i32 fd constants), Writer.write (inline write(2)) and Writer.close (shared close)
// lower on the IR path. A Writer is REPRESENTED by its bare fd, but it is typed
// `Writer` — stdout() / stderr() return the same nominal type native gives them
// (#7758), so the annotations below are the ones native accepts.
//
// "hello writer\n" is 13 bytes, so main returns 13 (the write count) and stdout
// carries the line. Writer.write's RESULT still diverges from native here — the
// self-host gives the byte count where native gives Option[IoError] — which is
// why `n` is read from it; the constructors are what #7758 converged.
const writerStdoutProg = `function main(): i32 {
    var w: Writer = stdout();
    var n: i32 = w.write("hello writer\n");
    w.close();
    var e: Writer = stderr();
    e.write("err line\n");
    e.close();
    return n;
}`

func TestSelfHostWriterIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(writerStdoutProg+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the Writer program")
	}
	progBin := buildBin(t, gcc, dir, "writer_stdout", string(asm))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	out, _ := cmd.Output() // stdout only; stderr goes elsewhere
	code := cmd.ProcessState.ExitCode()

	if !strings.Contains(string(out), "hello writer") {
		t.Errorf("stdout = %q, want it to contain %q (self-host stdout()/Writer.write, #4372)", string(out), "hello writer")
	}
	if code != 13 {
		t.Errorf("main returned %d, want 13 (bytes written by w.write; #4372)", code)
	}
}
