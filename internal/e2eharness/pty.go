package e2eharness

import (
	"os"

	"github.com/jakechampion/lang/internal/tty"
)

// OpenPTY hands the e2e package a real pseudo-terminal, for the tests that
// need a child process to see a terminal on stdout. The per-OS work is
// internal/tty's; this is the harness alias the e2e tests reach it through.
func OpenPTY() (master, slave *os.File, err error) { return tty.OpenPTY() }

// SetPTYSize sets the terminal size the kernel reports for `f`, so a test that
// asks a child how wide its terminal is knows what the answer must be.
func SetPTYSize(f *os.File, rows, cols int) error {
	return tty.SetWindowSize(int(f.Fd()), rows, cols)
}
