//go:build js || plan9

package interp

import "syscall"

// nofileSoft on a platform with no getrlimit(2). js/wasm is the one that
// matters: cmd/fern-wasm compiles this package for the playground, and its
// syscall package declares neither Rlimit nor RLIMIT_NOFILE.
//
// ENOSYS rather than "unlimited": a descriptor budget nobody measured is not
// the same answer as a kernel that says there is no ceiling, and `rlimit_nofile`
// is refused on the wasm worlds by internal/platforms anyway, so no program
// that type-checks for this target can reach it.
func nofileSoft() (uint64, error) { return 0, syscall.ENOSYS }
