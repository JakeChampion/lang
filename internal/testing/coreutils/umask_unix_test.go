//go:build unix

package coreutils

import (
	"os"
	"syscall"
	"testing"
)

// Pin the creation mask before any parallel cases start. Tree comparisons
// include modes, and the corpus is defined against the usual 022 mask.
func TestMain(m *testing.M) {
	previous := syscall.Umask(0o022)
	code := m.Run()
	syscall.Umask(previous)
	os.Exit(code)
}
