package interp

import "syscall"

func disableCoreDumps() bool {
	const prSetDumpable = 4
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); e == 0 {
		return true
	}
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}) == nil
}
