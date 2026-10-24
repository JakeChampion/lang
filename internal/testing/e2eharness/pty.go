package e2eharness

import (
	"os"

	"github.com/jakechampion/lang/internal/tools/tty"
)

func OpenPTY() (master, slave *os.File, err error) { return tty.OpenPTY() }

func SetPTYSize(f *os.File, rows, cols int) error {
	return tty.SetWindowSize(int(f.Fd()), rows, cols)
}
