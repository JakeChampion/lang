package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

const selfHostHandleTtySource = `function main(): i32 {
    let a: string[] = args();
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
                    let off: i64[] = t.with(3, t[3] & (0 - 1 - 8));
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

func TestSelfHostHandleTtyIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("handle-tty test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(selfHostHandleTtySource))
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

func TestSelfHostHandleTtyArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("handle-tty test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(selfHostHandleTtySource), "-target", "arm64-linux")
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
