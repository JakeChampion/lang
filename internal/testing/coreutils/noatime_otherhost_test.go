//go:build !linux

package coreutils

// noAtime is 0 where the system has no O_NOATIME.
const noAtime = 0
