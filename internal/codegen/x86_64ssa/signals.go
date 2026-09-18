package x86_64ssa

// Signals, scheduling priority and process groups: the calls that take
// integers and name no file. Their failures — ESRCH, EPERM, EINVAL, EACCES —
// are none of the errnos __fern_io_error names a variant for, so each becomes
// Other against an empty path, which is honest: the primitive never saw the
// text its caller parsed the pid out of.

// ssaScalarOpHelper returns the emitter for a syscall over scalars that
// answers Result[(), IoError]. `args` fills the argument registers; the frame
// is one push, which is what the error path's two calls need.
func ssaScalarOpHelper(name, tag string, sysno int, args func(w func(string, ...any))) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx") // one push past the return address: 16-aligned for the calls
		args(w)
		w("\tmov eax, %d", sysno)
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", tag)
		ssaOptionBox(w, 0, "") // Ok(())
		w("\tjmp .Lssa_%s_ret", tag)
		w(".Lssa_%s_err:", tag)
		w("\tneg rax")
		ssaFdIoErr(w, 1) // Err(e)
		w(".Lssa_%s_ret:", tag)
		w("\tpop rbx")
		w("\tret")
	}
}

// emitSignalSendHelper writes signal_send(pid, sig) -> Result[(), IoError]:
// kill(2) with both arguments as written, so the negative pids reach the
// kernel with the "every process in the group" meaning the sender means by
// them.
func emitSignalSendHelper(w func(string, ...any)) {
	ssaScalarOpHelper("signal_send", "sigsend", 62, func(w func(string, ...any)) {
		w("\tmovsxd rdi, edi")
		w("\tmovsxd rsi, esi")
	})(w)
}

// emitSetProcessGroupHelper writes set_process_group(pid, pgid) ->
// Result[(), IoError]: setpgid(2) with both arguments as written, so the
// syscall's zero conventions reach the kernel — pid 0 names the caller and
// pgid 0 names the pid's own value.
func emitSetProcessGroupHelper(w func(string, ...any)) {
	ssaScalarOpHelper("set_process_group", "setpgid", 109, func(w func(string, ...any)) {
		w("\tmovsxd rdi, edi")
		w("\tmovsxd rsi, esi")
	})(w)
}

// emitSetPriorityHelper writes set_priority(nice) -> Result[(), IoError]:
// setpriority(PRIO_PROCESS, 0, nice). No bias on this side — only the READ is
// biased.
func emitSetPriorityHelper(w func(string, ...any)) {
	ssaScalarOpHelper("set_priority", "setprio", 141, func(w func(string, ...any)) {
		w("\tmovsxd rdx, edi") // nice
		w("\txor edi, edi")    // PRIO_PROCESS
		w("\txor esi, esi")    // this process
	})(w)
}

// emitPriorityHelper writes priority() -> this process's nice value:
// getpriority(PRIO_PROCESS, 0).
//
// Linux returns the value BIASED by 20 so a success is never negative — nice
// 19 arrives as 1, nice -20 as 40 — and undoing that is the caller's job on
// every libc. It cannot fail for this process, so there is no errno to
// classify.
//
// The number is 140 here and 141 on arm64: getpriority and setpriority are
// SWAPPED between the two ABIs, so neither backend's pair can be copied to
// the other.
func emitPriorityHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("priority"))
	w("\txor edi, edi") // PRIO_PROCESS
	w("\txor esi, esi") // this process
	w("\tmov eax, 140") // getpriority
	w("\tsyscall")
	w("\tmov ecx, 20")
	w("\tsub ecx, eax")
	w("\tmov eax, ecx")
	w("\tret")
}

// emitSignalDispositionHelper returns the emitter for signal_ignore and
// signal_default: rt_sigaction with a `struct sigaction` whose handler is
// SIG_IGN (1) or SIG_DFL (0) and whose every other word is zero. No restorer
// is needed because neither disposition ever returns to one.
func emitSignalDispositionHelper(name string, handler int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tsub rsp, 40") // 32 bytes of sigaction, rounded to keep rsp 16-aligned
		w("\tmov qword ptr [rsp], %d", handler)
		w("\tmov qword ptr [rsp + 8], 0")
		w("\tmov qword ptr [rsp + 16], 0")
		w("\tmov qword ptr [rsp + 24], 0")
		w("\tmov rsi, rsp")
		w("\txor edx, edx")
		w("\tmov r10d, 8") // sizeof(kernel sigset_t)
		w("\tmov eax, 13") // rt_sigaction
		w("\tsyscall")
		w("\tadd rsp, 40")
		w("\tret")
	}
}

// emitSignalDispositionReadHelper writes signal_disposition(sig) -> i32:
// rt_sigaction with a NULL `act`, which reads without writing, then maps the
// handler word onto 0 default, 1 ignore, 2 a handler is installed.
func emitSignalDispositionReadHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("signal_disposition"))
	w("\tsub rsp, 40")
	w("\tmov qword ptr [rsp], 0")
	w("\tmov qword ptr [rsp + 8], 0")
	w("\tmov qword ptr [rsp + 16], 0")
	w("\tmov qword ptr [rsp + 24], 0")
	w("\txor esi, esi") // act = NULL: read without writing
	w("\tmov rdx, rsp") // oldact
	w("\tmov r10d, 8")
	w("\tmov eax, 13") // rt_sigaction
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_sigdisp_ret") // -errno passthrough
	w("\tmov rcx, [rsp]")       // sa_handler
	w("\tmov eax, 2")           // a handler is installed
	w("\tcmp rcx, 1")           // SIG_IGN
	w("\tjne .Lssa_sigdisp_notign")
	w("\tmov eax, 1")
	w("\tjmp .Lssa_sigdisp_ret")
	w(".Lssa_sigdisp_notign:")
	w("\ttest rcx, rcx") // SIG_DFL
	w("\tjnz .Lssa_sigdisp_ret")
	w("\txor eax, eax")
	w(".Lssa_sigdisp_ret:")
	w("\tadd rsp, 40")
	w("\tret")
}

// emitSignalMaskHelper writes signal_mask(how, mask) -> i64:
// rt_sigprocmask(2), answering the mask that was blocked BEFORE the call so
// one call both reads and writes.
//
// `how` is Fern's numbering, which is Linux's (0 block, 1 unblock, 2 replace),
// so it reaches the kernel untouched. The 24 bytes of stack hold `set` and
// `oset`; `oset` is zeroed first so the full i64 read back is the kernel's
// answer and not leftover stack.
func emitSignalMaskHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("signal_mask"))
	w("\tsub rsp, 24")
	w("\tmov [rsp], rsi")             // set = mask
	w("\tmov qword ptr [rsp + 8], 0") // oset = 0
	w("\tmov rsi, rsp")
	w("\tlea rdx, [rsp + 8]")
	w("\tmov r10d, 8") // sizeof(kernel sigset_t)
	w("\tmov eax, 14") // rt_sigprocmask
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_sigmask_ret") // -errno passthrough
	w("\tmov rax, [rsp + 8]")
	w(".Lssa_sigmask_ret:")
	w("\tadd rsp, 24")
	w("\tret")
}
