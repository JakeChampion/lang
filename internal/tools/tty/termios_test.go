package tty_test

import (
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

// The words Termios reports go back through SetTermios unchanged, and one
// flag cleared through them is cleared in the kernel. ECHO is 0x8 in lflag
// on both Linux and Darwin, so the probe is the same on each; what differs is
// the layout, which the length and VINTR's slot pin.
func TestTermiosRoundTrip(t *testing.T) {
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	fd := int(slave.Fd())

	words, err := tty.Termios(fd)
	if err != nil {
		t.Fatalf("Termios: %v", err)
	}
	n, vintr := 24, 5
	if runtime.GOOS == "darwin" {
		n, vintr = 26, 4+8
	}
	if len(words) != n {
		t.Fatalf("Termios answered %d words, want %d", len(words), n)
	}
	if words[vintr] != 3 {
		t.Errorf("VINTR = %d, want ^C on a fresh pty", words[vintr])
	}

	const echo = 8
	off := append([]int64(nil), words...)
	off[3] &^= echo
	if runtime.GOOS == "darwin" {
		off[n-2] = 300
	}
	if err := tty.SetTermios(fd, 1, off); err != nil {
		t.Fatalf("SetTermios: %v", err)
	}
	got, err := tty.Termios(fd)
	if err != nil {
		t.Fatalf("Termios after the set: %v", err)
	}
	for i := range off {
		if got[i] != off[i] {
			t.Errorf("word %d = %#x after the set, want %#x", i, got[i], off[i])
		}
	}
	if err := tty.SetTermios(fd, 0, words[:n-1]); err == nil {
		t.Error("SetTermios took a short array")
	}
	if err := tty.SetTermios(fd, 3, words); err == nil {
		t.Error("SetTermios took action 3")
	}
}
