package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

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

func TestSelfHostSetWindowSizeIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("set_window_size test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(selfHostSetWindowSizeSource))
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

func TestSelfHostSetWindowSizeArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("set_window_size test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(selfHostSetWindowSizeSource), "-target", "arm64-linux")
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
