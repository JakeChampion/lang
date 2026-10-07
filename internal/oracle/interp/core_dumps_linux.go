package interp

import "syscall"

// disableCoreDumps clears the dumpable flag, which a piped core_pattern
// obeys where RLIMIT_CORE does not, and falls back to the limit.
func disableCoreDumps() bool {
	const prSetDumpable = 4
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); e == 0 {
		return true
	}
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}) == nil
}
