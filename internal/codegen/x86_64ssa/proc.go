package x86_64ssa

// Spawning and reaping: fork, the two waits, and the two execs.
//
// Strings and arrays are the same shape as everywhere else in this backend: a
// data pointer with the byte or element count at [p - 4] and the refcount at
// [p - 8]. execve wants NUL-terminated C strings and a NULL-terminated argv,
// so each exec builds both. Those copies are deliberately never freed: on
// success the image is replaced, and on failure the process is about to die.

// emitProcForkHelper writes proc_fork() -> i32: 0 in the child, the child's
// pid in the parent, -errno on failure — the kernel's own return shape, so
// there is nothing to normalise.
func emitProcForkHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("proc_fork"))
	w("\tmov eax, 57") // fork
	w("\tsyscall")
	w("\tret")
}

// emitProcWaitpidHelper returns the emitter for proc_waitpid(pid) and
// proc_waitpid_nohang(pid): a wait4, then the status-word decode the shell
// uses —
//
//	WIFEXITED  ((status & 0x7f) == 0) -> (status >> 8) & 0xff
//	else (signal death)               -> 128 + (status & 0x7f)
//
// so a bounds-trapped worker surfaces as its raw exit code, e.g. 134. A
// failing syscall returns -errno as-is.
//
// With WNOHANG, wait4 reports "no child has anything to report" as a return of
// 0, which collides with a clean exit once the status word is decoded, so that
// case becomes -1. No errno wait4 returns is 1, which is what makes the sign
// alone enough to tell "still running" from a failure.
func emitProcWaitpidHelper(name, tag string, nohang bool) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tsub rsp, 24") // the status word, and 16-alignment
		w("\tmovsxd rdi, edi")
		w("\tmov rsi, rsp") // &status
		if nohang {
			w("\tmov edx, 1") // WNOHANG
		} else {
			w("\txor edx, edx")
		}
		w("\txor r10d, r10d") // rusage = NULL
		w("\tmov eax, 61")    // wait4
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_done", tag) // -errno: return as-is
		if nohang {
			w("\tjnz .Lssa_%s_decode", tag)
			w("\tmov eax, -1") // nothing to report: the child is still running
			w("\tjmp .Lssa_%s_done", tag)
			w(".Lssa_%s_decode:", tag)
		}
		w("\tmov ecx, [rsp]")
		w("\tmov eax, ecx")
		w("\tand eax, 127") // termination signal (0 = exited)
		w("\tjnz .Lssa_%s_sig", tag)
		w("\tshr ecx, 8")
		w("\tmovzx eax, cl")
		w("\tjmp .Lssa_%s_done", tag)
		w(".Lssa_%s_sig:", tag)
		w("\tadd eax, 128")
		w(".Lssa_%s_done:", tag)
		w("\tadd rsp, 24")
		w("\tret")
	}
}

// ssaCStrCopy copies the Fern string whose data pointer is in `src` into a
// fresh NUL-terminated block and leaves the C string in rax. `n` takes the
// byte count; both it and `src` must be callee-saved, since the allocation is
// a call, and __alloc touches none of those. `n` must be one of r8-r15: its
// low half is written by the `d` suffix. r11 carries each byte, so the copy
// does not clobber the low half of the pointer it is filling.
func ssaCStrCopy(w func(string, ...any), tag, src, n string) {
	w("\tmov %sd, %s", n, memRef(src, -4))
	w("\tlea edi, [%s + 1]", n)
	w("\tcall %s", fnLabel("__alloc"))
	w("\txor ecx, ecx")
	w(".Lssa_%s_copy:", tag)
	w("\tcmp rcx, %s", n)
	w("\tjae .Lssa_%s_copy_done", tag)
	w("\tmov r11b, [%s + rcx]", src)
	w("\tmov [rax + rcx], r11b")
	w("\tinc rcx")
	w("\tjmp .Lssa_%s_copy", tag)
	w(".Lssa_%s_copy_done:", tag)
	w("\tmov byte ptr [rax + %s], 0", n)
}

// ssaCStrVector turns the string[] whose data pointer is in rbx into a
// NULL-terminated char** in r12, with the C string at the frame slot `head`
// written into slot 0 first when one is given — which is how proc_exec
// prepends the path, since execve does not supply argv[0] itself.
//
// rbx = the source array, r12 = the vector, r13 = its element count, rbp = the
// fill index, r15 = the element being copied, r14 = its length. The length is
// in r14 rather than rbp because ssaCStrCopy writes its low half by the `d`
// suffix, which only the numbered registers have.
func ssaCStrVector(w func(string, ...any), tag, head string) {
	base := 1
	if head == "" {
		base = 0
	}
	w("\tmov r13d, %s", memRef("rbx", -4)) // element count
	w("\tlea edi, [r13 + %d]", base+1)     // the head slot, if any, plus the NULL
	w("\tshl edi, 3")
	w("\tcall %s", fnLabel("__alloc"))
	w("\tmov r12, rax")
	if head != "" {
		// The head arrives from the frame, so it goes through a register:
		// there is no memory-to-memory move.
		w("\tmov r11, %s", head)
		w("\tmov [r12], r11")
	}
	w("\txor ebp, ebp")
	w(".Lssa_%s_elem:", tag)
	w("\tcmp rbp, r13")
	w("\tjae .Lssa_%s_elem_done", tag)
	w("\tmov r15, [rbx + rbp*8]")
	ssaCStrCopy(w, tag+"_e", "r15", "r14")
	w("\tmov rcx, rbp")
	if head != "" {
		w("\tinc rcx")
	}
	w("\tmov [r12 + rcx*8], rax")
	w("\tinc rbp")
	w("\tjmp .Lssa_%s_elem", tag)
	w(".Lssa_%s_elem_done:", tag)
	w("\tmov rcx, r13")
	if head != "" {
		w("\tinc rcx")
	}
	w("\tmov qword ptr [r12 + rcx*8], 0")
}

// emitProcExecHelper writes proc_exec(path, args) -> i32. It only ever returns
// on failure (-errno): a successful execve replaces the image. argv is
// [path, args...] because execve does not prepend argv[0], and the environment
// is the envp vector _start captured.
func emitProcExecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("proc_exec"))
	ssaProcFramePush(w)
	w("\tsub rsp, 16")
	w("\tmov rbx, rsi") // the args array, where ssaCStrVector wants it
	w("\tmov r15, rdi") // the path's bytes
	ssaCStrCopy(w, "pexec_p", "r15", "r14")
	// argv[0] goes on the frame: ssaCStrVector reuses every register below.
	w("\tmov [rsp], rax")
	ssaCStrVector(w, "pexec", "[rsp]")
	w("\tmov rdi, [rsp]") // path
	w("\tmov rsi, r12")   // argv
	w("\tmov rdx, [rip + %s]", envpSym)
	w("\tmov eax, 59") // execve
	w("\tsyscall")
	w("\tadd rsp, 16")
	ssaProcFramePop(w)
	w("\tret")
}

// emitProcExecAsHelper writes proc_exec_as(path, argv, envp) -> i32.
//
// Both vectors come from the caller: argv verbatim, argv[0] included, so it is
// NOT the path, and envp instead of the captured snapshot — which is why this
// helper does not pull in the envp capture the way proc_exec does. Everything
// else matches it.
func emitProcExecAsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("proc_exec_as"))
	ssaProcFramePush(w)
	w("\tsub rsp, 32")
	w("\tmov [rsp], rsi")     // the argv array
	w("\tmov [rsp + 8], rdx") // the envp array
	w("\tmov r15, rdi")
	ssaCStrCopy(w, "pexecas_p", "r15", "r14")
	w("\tmov [rsp + 16], rax") // the path's C string
	w("\tmov rbx, [rsp]")
	ssaCStrVector(w, "pexecas_argv", "")
	w("\tmov [rsp + 24], r12")
	w("\tmov rbx, [rsp + 8]")
	ssaCStrVector(w, "pexecas_envp", "")
	w("\tmov rdi, [rsp + 16]") // path
	w("\tmov rsi, [rsp + 24]") // argv
	w("\tmov rdx, r12")        // envp
	w("\tmov eax, 59")         // execve
	w("\tsyscall")
	w("\tadd rsp, 32")
	ssaProcFramePop(w)
	w("\tret")
}

// ssaProcFramePush and ssaProcFramePop bracket the exec helpers: six
// callee-saved registers, which leaves rsp 16-aligned past the return address
// and so ready for the allocation calls inside.
func ssaProcFramePush(w func(string, ...any)) {
	for _, r := range []string{"rbx", "r12", "r13", "r14", "r15", "rbp"} {
		w("\tpush %s", r)
	}
	w("\tsub rsp, 8")
}

func ssaProcFramePop(w func(string, ...any)) {
	w("\tadd rsp, 8")
	for _, r := range []string{"rbp", "r15", "r14", "r13", "r12", "rbx"} {
		w("\tpop %s", r)
	}
}
