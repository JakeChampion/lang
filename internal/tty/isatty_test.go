package tty_test

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/tty"
)

// The whole reason IsTerminal asks the kernel for terminal ATTRIBUTES rather
// than for the file's mode is /dev/null: it is a character device, so the
// `fstat` + `S_ISCHR` test (Go's `os.ModeCharDevice`, which `cmd/fern` used to
// colourise its diagnostics by) calls it a terminal. It is not one, and
// `fern -check 2>/dev/null` should not paint escapes at it.
func TestIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device on this host, so it cannot "+
			"exercise the distinction this test exists for", os.DevNull)
	}
	if tty.IsTerminal(int(f.Fd())) {
		t.Errorf("IsTerminal(%s) = true; a character device is not a terminal",
			os.DevNull)
	}
}

// The positive half, against a real pty — otherwise "always false" would pass
// every other case in this file.
func TestIsTerminalAcceptsAPty(t *testing.T) {
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	for _, f := range []*os.File{master, slave} {
		if !tty.IsTerminal(int(f.Fd())) {
			t.Errorf("IsTerminal(%s) = false; both ends of a pty are terminals", f.Name())
		}
	}
}

// A pipe is the shape a redirected CLI actually has, and the one the colour
// gate turns on.
func TestIsTerminalRejectsAPipeAndAClosedFd(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if tty.IsTerminal(int(w.Fd())) {
		t.Error("IsTerminal(pipe) = true")
	}
	if tty.IsTerminal(-1) {
		t.Error("IsTerminal(-1) = true; a negative fd names nothing")
	}
}
