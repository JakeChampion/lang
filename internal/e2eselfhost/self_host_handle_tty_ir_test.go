package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/tty"
)

// selfHostHandleTtySource drives the four terminal questions asked of a
// HANDLE through the self-host's lowering (#9363). A handle IS its fd in this
// compiler, so each method lowers to the same op the free builtin does with
// the receiver as its first operand — which is `isatty`'s arrangement, and
// what this leg pins: that the receiver reaches the op as an operand and the
// method's own arguments follow it in order.
//
// The terminal is reached BY PATH, with fds 0-2 left as the driver's own, so
// nothing here could be answered by a standard descriptor instead.
const selfHostHandleTtySource = `function main(): i32 {
    var a: string[] = args();
    if (a.len() < 2) { return 9; }
    match (open_reader_with(a[1], 2)) {
        Err(_) => { return 10; },
        Ok(r) => {
            match (r.window_size()) {
                Err(_) => { return 11; },
                Ok(ws) => { if (ws.rows != (24 as i64) || ws.cols != (80 as i64)) { return 12; } }
            }
            match (r.set_window_size(40, 100)) { Err(_) => { return 13; }, Ok(_) => {} }
            match (r.window_size()) {
                Err(_) => { return 14; },
                Ok(ws) => { if (ws.rows != (40 as i64) || ws.cols != (100 as i64)) { return 15; } }
            }
            match (r.termios_get()) {
                Err(_) => { return 16; },
                Ok(t) => {
                    if (t.len() != 24) { return 17; }
                    if (t[5] != (3 as i64)) { return 18; }
                    var off: i64[] = t.with(3, t[3] & (0 - 1 - 8));
                    match (r.termios_set(1, off)) { Err(_) => { return 19; }, Ok(_) => {} }
                    match (r.termios_get()) {
                        Err(_) => { return 20; },
                        Ok(u) => { if (u[3] != (t[3] - (8 as i64))) { return 21; } }
                    }
                    return 0;
                }
            }
        }
    }
    return 25;
}
`

// handleTtyOnPath allocates a pseudo-terminal, plants 24x80 with a pixel pair,
// runs the program with the pty's NAME as its argument, and requires the
// resize to have landed with the pixel pair intact.
func handleTtyOnPath(t *testing.T, cmd *exec.Cmd) {
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
	cmd.Args = append(cmd.Args, slave.Name())
	// The child opens the pty by name, so no descriptor on it is passed.
	slave.Close()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostHandleTtySource)\n%s", code, out)
	}
	rows, cols, xpixel, ypixel, err := tty.WindowSizeFull(int(master.Fd()))
	if err != nil {
		t.Fatalf("read the size back: %v", err)
	}
	if rows != 40 || cols != 100 {
		t.Errorf("terminal is %dx%d, want 40x100", rows, cols)
	}
	if xpixel != 640 || ypixel != 480 {
		t.Errorf("pixel pair is %dx%d, want the planted 640x480", xpixel, ypixel)
	}
}

// TestSelfHostHandleTtyIR is the x86-64 IR leg.
func TestSelfHostHandleTtyIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("handle-tty test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(selfHostHandleTtySource), "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	for _, sym := range []string{
		"call __fn___fern_window_size",
		"call __fn___fern_set_window_size",
		"call __fn___fern_termios_get",
		"call __fn___fern_termios_set",
	} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Errorf("asm has no `%s`: the method did not reach the free op's runtime leaf", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "handle_tty_prog", string(asm))
	handleTtyOnPath(t, exec.Command(progBin))
}

// TestSelfHostHandleTtyArm64IR is the arm64 leg of the same programme, through
// asm_ir_run -target arm64-linux under qemu.
func TestSelfHostHandleTtyArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("handle-tty test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(selfHostHandleTtySource), "-target", "arm64-linux", "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	for _, sym := range []string{
		"bl __fn___fern_window_size",
		"bl __fn___fern_set_window_size",
		"bl __fn___fern_termios_get",
		"bl __fn___fern_termios_set",
	} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Errorf("asm has no `%s`: the method did not reach the free op's runtime leaf", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "handle_tty_prog_arm64", string(asm))
	handleTtyOnPath(t, runArm64Bin(qemu, bin))
}
