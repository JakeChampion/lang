package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `w.close()` / `r.close()` report a FAILING close, rather than answering None
// whatever the kernel said (#8569).
//
// The self-host's `__fern_reader_close` discarded the syscall's result — a
// deliberate choice at the time, on the grounds that the std/io drain loop
// never read the error arm. Every native backend maps the errno
// (`__fern_close_fd_box` on x86-64/arm64, `buildCloseBody` on wasm), so the two
// compilers disagreed the moment anything did read it: gnulib's close_stream
// contract is exactly "did the close fail", so `printf ... >&-` built by the
// self-host printed `write error` where native and GNU print `write error: Bad
// file descriptor`. The coreutils self-host gate (internal/coreutils) found it;
// this is the reduced case.
//
// Closing the same fd twice is the portable way to reach EBADF: the first close
// succeeds, the second has nothing to close. The exit code carries both answers
// — 1 for None, 7 for Some(Other) with glibc's text — so the whole outcome is
// one number, 17, and the interp is the oracle for it. A self-host that
// swallows the error returns 11.
const closeIoErrorSrc = `function code_of(o: Option[IoError]): i32 {
    match (o) {
        Some(e) => {
            match (e) {
                Other(c, msg) => { if (msg == "Bad file descriptor") { return 7; } return 5; },
                _ => { return 4; }
            }
        },
        None => { return 1; }
    }
}
function main(): i32 {
    var w: Writer = stdout();
    var first: i32 = code_of(w.close());
    var second: i32 = code_of(w.close());
    return first * 10 + second;
}`

// TestSelfHostCloseIoErrorIRX86_64 — the x86-64 IR path.
func TestSelfHostCloseIoErrorIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, closeIoErrorSrc)
	asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(closeIoErrorSrc), "-ir")
	progBin := buildBin(t, gcc, dir, "close_io_error", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	cmd.Stdout = nil
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("close reporting = %d, want %d (interp oracle; 11 = the failing close answered None)", got, want)
	}
}

// TestSelfHostCloseIoErrorIRArm64 — the arm64 sibling under qemu.
func TestSelfHostCloseIoErrorIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, closeIoErrorSrc)
	asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(closeIoErrorSrc), "-target", "arm64-linux", "-ir")
	progBin := buildBin(t, arm64gcc, dir, "close_io_error", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("close reporting = %d, want %d (interp oracle; 11 = the failing close answered None)", got, want)
	}
}

// TestSelfHostCloseIoErrorWasmIR — the wasm leg. The errno is WASI's `badf`
// rather than a Linux one, and `needs_io_error` has to fold the close in so the
// classifier and the strerror texts reach a module that only closes.
func TestSelfHostCloseIoErrorWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping close IoError wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, closeIoErrorSrc)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(closeIoErrorSrc))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "close_io_error.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	rcmd.Stdout = nil
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("close reporting = %d, want %d (interp oracle; 11 = the failing close answered None)", got, want)
	}
}
