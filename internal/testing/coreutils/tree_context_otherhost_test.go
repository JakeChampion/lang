//go:build !linux

package coreutils

import "syscall"

func entryContext(string) string { return "" }

func setEntryContext(string, string) error { return syscall.ENOTSUP }
