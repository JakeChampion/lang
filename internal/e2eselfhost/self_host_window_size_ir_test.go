package e2eselfhost

import (
	"bytes"
	"io"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/tty"
)

// `window_size` through the SELF-HOST IR path, on the two backends that
// provide it. No wasm world has a terminal to measure, so there is no wasm leg
// here: `tty` is granted by no wasi profile and the refusal is pinned by the
// capability mirrors.
//
// Nothing here can be proved by compiling. A helper that lowered to nothing,
// read the wrong two bytes of `struct winsize`, or answered a plausible 80x24
// still links and still returns Ok — so the probe runs against a REAL pty
// whose size the harness set to a pair no default produces.
//
// `internal/e2eselfhost` is PRIMARY for a self-host lowering change
// (docs/TEST-GATES.md): the fixpoint is self-referential and nothing in the
// compiler asks how wide a terminal is.

const (
	shWsRows = 13
	shWsCols = 57
)

// selfHostWindowSizeSource reports what it found through the exit code: 0 for
// the size the harness set, and a distinct code per way of being wrong.
const selfHostWindowSizeSource = `function main(): i32 {
    match (window_size(1)) {
        Ok(ws) => {
            if (ws.rows != (13 as i64)) { return 21; }
            if (ws.cols != (57 as i64)) { return 22; }
            return 0;
        },
        Err(e) => { return 23; }
    }
}
`

// selfHostWindowSizeNoTtySource asks the same question of a redirected
// descriptor, where the answer is ENOTTY and has to stay one.
const selfHostWindowSizeNoTtySource = `function main(): i32 {
    match (window_size(1)) {
        Ok(ws) => { return 24; },
        Err(e) => { return 0; }
    }
}
`

// runSelfHostOnPTY runs the command with stdout wired to a pty of the size
// above and returns its exit code.
func runSelfHostOnPTY(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	if err := tty.SetWindowSize(int(slave.Fd()), shWsRows, shWsCols); err != nil {
		t.Fatalf("set pty size: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, master)
		close(done)
	}()
	cmd.Stdout = slave
	_ = cmd.Run()
	slave.Close()
	<-done
	if cmd.ProcessState == nil {
		t.Fatalf("the program did not run to completion on a pty")
	}
	return cmd.ProcessState.ExitCode()
}

// TestSelfHostWindowSizeIR is the x86-64 leg.
func TestSelfHostWindowSizeIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("window_size test runs only natively (it needs a host pty on the child's stdout)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	build := func(src, name string) string {
		cmd := exec.Command(driverBin, "-ir")
		cmd.Stdin = bytes.NewReader([]byte(src))
		asm, err := cmd.Output()
		if err != nil || len(asm) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		if !bytes.Contains(asm, []byte("__fern_window_size")) {
			t.Fatal("window_size did not reach the IR runtime path (no __fern_window_size in the asm)")
		}
		return buildBin(t, gcc, dir, name, string(asm))
	}

	if code := runSelfHostOnPTY(t, exec.Command(build(selfHostWindowSizeSource, "ws_tty"))); code != 0 {
		t.Fatalf("on a %dx%d pty: exit = %d, want 0 (21 = wrong rows, 22 = wrong cols, 23 = Err)",
			shWsRows, shWsCols, code)
	}
	run := exec.Command(build(selfHostWindowSizeNoTtySource, "ws_pipe"))
	run.Stdout = io.Discard
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("redirected: exit = %d, want 0 (24 = answered with a size for a pipe)", code)
	}
}

// TestSelfHostWindowSizeIRArm64 is the same probe through the arm64 IR
// backend under qemu: a second hand-written call sequence, and the request
// number comes from the dual table.
func TestSelfHostWindowSizeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("window_size test runs only natively (it needs a host pty on the child's stdout)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	build := func(src, name string) string {
		cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
		cmd.Stdin = bytes.NewReader([]byte(src))
		asm, err := cmd.Output()
		if err != nil || len(asm) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		if !bytes.Contains(asm, []byte("bl __fn___fern_window_size")) {
			t.Fatal("no `bl __fn___fern_window_size` in the emitted asm — it did not lower through the arm64 IR path")
		}
		return buildBinArm64(t, arm64gcc, dir, name, string(asm))
	}

	if code := runSelfHostOnPTY(t, runArm64Bin(qemu, build(selfHostWindowSizeSource, "ws_tty"))); code != 0 {
		t.Fatalf("on a %dx%d pty: exit = %d, want 0 (21 = wrong rows, 22 = wrong cols, 23 = Err)",
			shWsRows, shWsCols, code)
	}
	run := runArm64Bin(qemu, build(selfHostWindowSizeNoTtySource, "ws_pipe"))
	run.Stdout = io.Discard
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("redirected: exit = %d, want 0 (24 = answered with a size for a pipe)", code)
	}
}
