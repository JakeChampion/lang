//go:build !linux && !darwin

package tty

import (
	"errors"
	"os"
)

// OpenPTY has no implementation off Linux/Darwin. The e2e suite's compiled
// targets are Linux ELF, Mach-O and wasm and the harness shells out to qemu /
// wasmtime, so this file exists to keep the package building rather than to
// serve a supported host.
func OpenPTY() (master, slave *os.File, err error) {
	return nil, nil, errors.New("no pty implementation for this host")
}
