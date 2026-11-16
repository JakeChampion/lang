//go:build !linux

package coreutils

import "time"

// The syscall package gives no call that sets the times of a link itself on
// this host, so the times of a link stay as made.
func pinLinkAtime(string, time.Time, time.Time) error { return nil }
