//go:build linux || darwin

package interp

import "syscall"

// The two signals no kernel lets a process catch, block or ignore.
//
// Taken from syscall so the number is the host's — SIGSTOP is 17 on
// Darwin and 19 on Linux — rather than one of the two guessed.
const (
	sigKill = int64(syscall.SIGKILL)
	sigStop = int64(syscall.SIGSTOP)
)
