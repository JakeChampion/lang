package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/tty"
)

// selfHostTermiosSource drives `termios_get(fd)` and `termios_set(fd, when,
// words)` through the self-host's lowering: the `termios_get` and
// `termios_set` ops and the Fern runtime leaves behind them, which is one
// TCGETS into a 24-element word array and the TCSETS family back out of one.
//
// Every failure returns its own exit code; nothing is printed, so the test
// needs no stdout comparison and the program can run with a terminal on fd 0
// where a pipe would answer ENOTTY to everything.
//
// The assertions are chosen so a wrong request number or a mispacked struct
// cannot pass: the length is the target's, VINTR on a fresh pseudo-terminal is
// ^C, clearing ECHO changes lflag by exactly that bit and the value comes
// back, restoring the original returns it, and both a short array and an
// out-of-range action are refused.
const selfHostTermiosSource = `function main(): i32 {
    match (termios_get(0)) {
        Err(_) => { return 10; },
        Ok(t) => {
            if (t.len() != 24) { return 11; }
            if (t[5] != (3 as i64)) { return 12; }
            var off: i64[] = t.with(3, t[3] & (0 - 1 - 8));
            match (termios_set(0, 1, off)) { Err(_) => { return 13; }, Ok(_) => {} }
            match (termios_get(0)) {
                Err(_) => { return 14; },
                Ok(u) => { if (u[3] != (t[3] - (8 as i64))) { return 15; } }
            }
            match (termios_set(0, 1, t)) { Err(_) => { return 16; }, Ok(_) => {} }
            match (termios_get(0)) {
                Err(_) => { return 17; },
                Ok(v) => { if (v[3] != t[3]) { return 18; } }
            }
            match (termios_set(0, 1, [1 as i64])) { Ok(_) => { return 19; }, Err(_) => {} }
            match (termios_set(0, 9, t)) { Ok(_) => { return 20; }, Err(_) => {} }
            return 0;
        }
    }
}
`

// runOnPty runs cmd with a fresh pseudo-terminal as its stdin, which is the
// only way the probe above reaches anything: every one of its calls answers
// ENOTTY on a pipe. The master is drained so a child that wrote more than a
// terminal holds could not deadlock — this one writes nothing, and the drain
// costs nothing.
func runOnPty(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("openpty: %v", err)
	}
	defer master.Close()
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
	master.Close()
	<-done
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally (out=%q)", out.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

// TestSelfHostTermiosIR is the x86-64 IR leg: the two ops lower to calls into
// the Fern-compiled __fern_termios_get / __fern_termios_set, which assemble
// the flag words a byte at a time — tcflag_t is unsigned where __load_i32 is
// signed, so a word with its top bit set would arrive negative.
func TestSelfHostTermiosIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("termios test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(selfHostTermiosSource), "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	for _, sym := range []string{"call __fn___fern_termios_get", "call __fn___fern_termios_set"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Errorf("asm has no `%s`: the op did not reach the runtime leaf", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "termios_prog", string(asm))
	out, code := runOnPty(t, exec.Command(progBin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostTermiosSource)\n%s", code, out)
	}
}

// TestSelfHostTermiosArm64IR is the arm64 leg of the same programme, through
// asm_ir_run -target arm64-linux under qemu.
func TestSelfHostTermiosArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("termios test runs only natively (drives a host terminal)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(selfHostTermiosSource), "-target", "arm64-linux", "-ir")
	if len(asm) == 0 {
		t.Fatal("driver emitted no asm")
	}
	for _, sym := range []string{"bl __fn___fern_termios_get", "bl __fn___fern_termios_set"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Errorf("asm has no `%s`: the op did not reach the runtime leaf", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "termios_prog_arm64", string(asm))
	out, code := runOnPty(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the case (see selfHostTermiosSource)\n%s", code, out)
	}
}
