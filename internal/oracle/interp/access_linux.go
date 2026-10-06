//go:build linux

package interp

import "syscall"

// atFdcwd / atEaccess are `AT_FDCWD` and `AT_EACCESS`. Neither is
// exported by `syscall` (they are lower-cased constants inside it), so
// the values are written out; both are architecture-independent on Linux.
const (
	atFdcwd   = -0x64
	atEaccess = 0x200
)

// accessEffective answers `access(path, mode)` against the EFFECTIVE
// user and group ids — `euidaccess(3)`, which is what a shell's
// `test -r` / `-w` / `-x` is specified to use. `syscall.Faccessat`
// routes to `faccessat2(2)` when the kernel has it and emulates
// AT_EACCESS from the mode bits otherwise.
func accessEffective(path string, mode int) error {
	return syscall.Faccessat(atFdcwd, path, uint32(mode), atEaccess)
}
