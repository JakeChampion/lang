package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4372 (stream half): the streaming-I/O WRITE surface must lower on the self-host
// IR paths. The read half (stdin / Reader.read_chunk / Reader.close) already
// lowered; stdout() / stderr() and Writer.write had no lowering, so a program using
// them bailed to the legacy AST backend and failed to link. This drives a program
// that writes to stdout and stderr through their Writers (fds 1 / 2) and captures
// the stdout bytes — proving stdout()/stderr() (i32 fd constants), Writer.write
// (the __fern_writer_write runtime helper) and Writer.close (the shared close)
// lower on the IR path.
//
// The program uses NATIVE's signature throughout: `Writer.write(s)` answers
// `Option[IoError]` — None once every byte is out — not the byte count the
// self-host used to hand back (#7926). So the bytes that reached stdout are what
// this asserts; the count is not recoverable from either compiler's signature, and
// the native-shaped `match (w.write(s))` below is exactly the program that used to
// bail the module for want of an Option type to recover.
const writerStdoutProg = `function main(): i32 {
    var w: Writer = stdout();
    match (w.write("hello writer\n")) { Some(_) => { return 1; }, None => {} }
    match (w.close()) { Some(_) => { return 2; }, None => {} }
    var e: Writer = stderr();
    match (e.write("err line\n")) { Some(_) => { return 3; }, None => {} }
    match (e.close()) { Some(_) => { return 4; }, None => {} }
    return 0;
}`

// writerErrProg is the OTHER arm: a failing write answers Some(IoError) carrying
// a real variant — the same value native builds — rather than the bare -errno the
// self-host used to push. Writing to a Writer whose fd is already closed is EBADF
// (9), which __fern_io_error classifies as Other; a write has no path to report,
// so the Other payload is the empty string the helper hands the classifier. Exit 5
// therefore says the Option box, the variant box and its path string are all
// well-formed.
//
// Pinned to what the native COMPILER does — `fern` exits 5 on both register
// targets. `fern -interp` is NOT an oracle here: it models a Writer as a registry
// entry rather than a raw fd, so writing to a closed one is a hard interpreter
// error instead of an EBADF, a native-side interp/codegen divergence that predates
// this surface. Neither is the wasm target, whose preview2 component holds a
// resource handle and traps.
const writerErrProg = `function main(): i32 {
    match (open_writer("` + writerErrPath + `")) {
        Ok(w) => {
            match (w.close()) { Some(_) => { return 8; }, None => {} }
            match (w.write("x")) {
                Some(e) => {
                    match (e) {
                        Other(p, _) => { if (p.len() == 0) { return 5; } return 6; },
                        _ => { return 7; },
                    }
                },
                None => { return 1; },
            }
        },
        Err(_) => { return 9; },
    }
    return 0;
}`

// The closed-fd probe writes (and never reads) one file. An absolute path under
// /tmp works under qemu-user too, where filesystem syscalls pass through to the
// host.
const writerErrPath = "/tmp/fern-selfhost-writer-closed-probe.txt"

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
	// The write is a CALL to the Fern runtime helper (#7926), not the inline
	// write(2) syscall it used to be — the helper is what boxes the Option.
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
	out, _ := cmd.Output() // stdout only; stderr goes elsewhere
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

// TestSelfHostWriterIRArm64 is the ARM64 counterpart: the same two programs
// through asm_ir_run -target arm64-linux, assembled with the aarch64 cross-gcc and
// run under qemu-aarch64. Both backends lower Writer.write to the SAME Fern
// runtime helper now (#7926) — it replaced an inline write(2) on each — so both
// need pinning, and the arm64 branch carries the stack-ABI `__fn___` prefix.
//
// SKIPs cleanly when the aarch64 cross-toolchain / qemu-aarch64 aren't installed
// (see arm64Tooling); CI provides them.
func TestSelfHostWriterIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	compile := func(t *testing.T, name, src string) string {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src+"\n"), "-target", "arm64-linux", "-ir")
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
