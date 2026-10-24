//go:build !linux && !darwin

package tty

import (
	"errors"
	"os"
)

func OpenPTY() (master, slave *os.File, err error) {
	return nil, nil, errors.New("no pty implementation for this host")
}
