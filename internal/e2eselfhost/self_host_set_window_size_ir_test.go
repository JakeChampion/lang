package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/tty"
)

// selfHostSetWindowSizeSource drives `set_window_size(fd, rows, cols)` through
// the self-host's lowering: the op, its three-operand stack marshalling, and
// the Fern runtime leaf behind it, which is one TIOCGWINSZ — so the pixel pair
// nothing surrenders survives — and one TIOCSWINSZ back.
//
// Every failure returns its own exit code and nothing is printed, so the
// program can run with a terminal on fd 0 where a pipe would answer ENOTTY to
// everything.
//
// The last case is the one the three-operand marshalling gets wrong when the
// operands arrive reversed: 65536 rows has to reach the kernel as 0 rather
// than be refused, and reading it back proves which number landed in which
// field.
const selfHostSetWindowSizeSource = `function main(): i32 {
    match (set_window_size(0, 40, 100)) { Err(_) => { return 10; }, Ok(_) => {} }
    match (window_size(0)) {
        Err(_) => { return 11; },
        Ok(ws) => { if (ws.rows != (40 as i64) || ws.cols != (100 as i64)) { return 12; } }
    }
    match (set_window_size(1, 40, 100)) { Ok(_) => { return 13; }, Err(_) => {} }
    match (set_window_size(0, 65536, 7)) { Err(_) => { return 14; }, Ok(_) => {} }
    match (window_size(0)) {
        Err(_) => { return 15; },
        Ok(ws) => { if (ws.rows != (0 as i64) || ws.cols != (7 as i64)) { return 16; } }
    }
    return 0;
}
`

// setWindowSizeOnPty is runOnPty with the pixel pair planted first and checked
// after: the read-modify-write is the whole reason the helper makes two
// ioctls, and a one-ioctl version zeroes what it never read.
func setWindowSizeOnPty(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("openpty: %v", err)
	}
	defer master.Close()
	if err := tty.SetWindowSizeFull(int(slave.Fd()), 24, 80, 640, 480); err != nil {
		slave.Close()
		t.Fatalf("plant the size: %v", err)
	}
	cmd.Stdin = slave
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatalf("start: %v", err)
	}
	// The parent's copy goes as soon as the child holds it, or a read of the
	// master would never see the end of the output.
	slave.Close()
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := master.Read(buf); err != nil {
				close(done)
				return
			}
		}
	}()
	_ = cmd.Wait()
	<-done
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally (out=%q)", out.String())
	}
	_, _, xpixel, ypixel, err := tty.WindowSizeFull(int(master.Fd()))
	if err != nil {
		t.Fatalf("read the size back: %v", err)
	}
	if xpixel != 640 || ypixel != 480 {
		t.Errorf("pixel pair is %dx%d, want the planted 640x480 — a one-ioctl "+
			"set_window_size zeroes what it never read", xpixel, ypixel)
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

// TestSelfHostSetWindowSizeIR is the x86-64 IR leg: the op lowers to a call
// into the Fern-compiled __fern_set_window_size, with the three operands
// re-pushed so param[0] is lowest.
func TestSelfHostSetWindowSizeIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("set_window_size test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(selfHostSetWindowSizeSource), "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	if !bytes.Contains(asm, []byte("call __fn___fern_set_window_size")) {
		t.Error("asm has no `call __fn___fern_set_window_size`: the op did not reach the runtime leaf")
	}
	progBin := buildBin(t, gcc, dir, "swsz_prog", string(asm))
	out, code := setWindowSizeOnPty(t, exec.Command(progBin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostSetWindowSizeSource)\n%s", code, out)
	}
}

// TestSelfHostSetWindowSizeArm64IR is the arm64 leg of the same programme,
// through asm_ir_run -target arm64-linux under qemu.
func TestSelfHostSetWindowSizeArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("set_window_size test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(selfHostSetWindowSizeSource), "-target", "arm64-linux", "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_set_window_size")) {
		t.Error("asm has no `bl __fn___fern_set_window_size`: the op did not reach the runtime leaf")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "swsz_prog_arm64", string(asm))
	out, code := setWindowSizeOnPty(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostSetWindowSizeSource)\n%s", code, out)
	}
}
