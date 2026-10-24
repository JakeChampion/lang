//go:build unix

package coreutils

import (
	"os"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	previous := syscall.Umask(0o022)
	code := m.Run()
	syscall.Umask(previous)
	os.Exit(code)
}
