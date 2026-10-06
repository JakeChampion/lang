//go:build !linux && !darwin

package interp

import "syscall"

// getProcPriority on a platform with no scheduler knob Fern reaches. Nothing
// competes for the CPU by nice value there, so the only value that
// describes it is the default.
func getProcPriority() int { return 0 }

// setProcPriority on the same platform: there is nothing to set, and
// reporting success would claim a change that did not happen.
func setProcPriority(int) error { return syscall.ENOSYS }
