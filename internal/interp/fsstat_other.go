//go:build !linux && !darwin

package interp

import "syscall"

// fsStatFields on a platform this package has no `statfs(2)` projection for.
// ENOSYS rather than a zero-filled record: the caller gets an `Err` naming
// the absence, where zeros would claim a filesystem with no blocks and no
// name length — a measurement nobody took.
func fsStatFields(string) (rawFsStat, error) { return rawFsStat{}, syscall.ENOSYS }
