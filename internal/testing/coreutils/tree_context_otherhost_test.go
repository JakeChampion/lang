//go:build !linux

package coreutils

import "syscall"

// entryContext: only Linux stores a context in `security.selinux`.
func entryContext(string) string { return "" }

func setEntryContext(string, string) error { return syscall.ENOTSUP }
