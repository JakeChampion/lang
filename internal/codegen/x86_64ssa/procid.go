package x86_64ssa

// The leaf helpers that ask the kernel about this process and get an integer
// back: the four credential calls, umask, cpu_count and sleep_ns. None
// allocates and none can fail in a way the language's signature has room for,
// so each is a syscall and a ret.

// emitIdHelper returns the emitter for one of getuid / geteuid / getgid /
// getegid. Each takes no argument, cannot fail, and answers the id in rax,
// which is already the return register.
func emitIdHelper(name string, sysno int) func(func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov eax, %d", sysno)
		w("\tsyscall")
		w("\tret")
	}
}

// emitUmaskHelper writes umask(mask) -> the previous mask. umask(2) cannot
// fail and returns the mask it replaced, so the syscall's own return value is
// the whole answer.
func emitUmaskHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("umask"))
	w("\tand edi, 4095")
	w("\tmov eax, 95") // umask
	w("\tsyscall")
	w("\tret")
}

// emitCPUCountHelper writes cpu_count() -> how many processing units the
// process may run on: sched_getaffinity(2) writes the mask and answers its
// size in bytes, rounded to whole longs, and the answer is that mask's
// population count. A refused call answers 0, the builtin's "cannot say".
//
// popcnt is in the x86-64-v3 baseline, so the count is one instruction per
// word with no cpuid check.
func emitCPUCountHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("cpu_count"))
	w("\tsub rsp, 128") // 1024 CPUs' worth of mask
	w("\txor edi, edi") // pid 0 = this thread
	w("\tmov esi, 128")
	w("\tmov rdx, rsp")
	w("\tmov eax, 204") // sched_getaffinity
	w("\tsyscall")
	w("\txor r9d, r9d")
	w("\ttest rax, rax")
	w("\tjle .Lssa_cpu_done")
	w("\tmov rcx, rax") // bytes the kernel wrote
	w("\txor edx, edx") // byte offset
	w(".Lssa_cpu_word:")
	w("\tcmp rdx, rcx")
	w("\tjae .Lssa_cpu_done")
	w("\tpopcnt r8, [rsp + rdx]")
	w("\tadd r9d, r8d")
	w("\tadd rdx, 8")
	w("\tjmp .Lssa_cpu_word")
	w(".Lssa_cpu_done:")
	w("\tmov eax, r9d")
	w("\tadd rsp, 128")
	w("\tret")
}

// emitSleepNsHelper writes sleep_ns(ns): nanosleep with the caller's
// nanoseconds carried through rather than rounded to a millisecond first
// (#8528) — the timespec split is by 1e9 and the remainder IS tv_nsec. A
// non-positive argument returns without a syscall; an interrupted sleep is not
// resumed (rem = NULL), as on the natives.
func emitSleepNsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("sleep_ns"))
	w("\ttest rdi, rdi")
	w("\tjle .Lssa_sleepns_done")
	w("\tsub rsp, 24") // struct timespec { i64 tv_sec; i64 tv_nsec }
	w("\tmov rax, rdi")
	w("\txor edx, edx")
	w("\tmov rcx, 1000000000")
	w("\tdiv rcx") // rax = tv_sec, rdx = tv_nsec
	w("\tmov [rsp], rax")
	w("\tmov [rsp + 8], rdx")
	w("\tmov rdi, rsp")
	w("\txor esi, esi") // rem = NULL
	w("\tmov eax, 35")  // nanosleep
	w("\tsyscall")
	w("\tadd rsp, 24")
	w(".Lssa_sleepns_done:")
	w("\tret")
}
