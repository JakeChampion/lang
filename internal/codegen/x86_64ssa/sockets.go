package x86_64ssa

// The socket family behind std/async and the edge-handler examples: raw fds
// in, raw fds or -errno out, as the flat backend's __fern_tcp_* routines and
// arm64ssa's helpers report them. Linux x86-64: socket 41, connect 42,
// accept 43, bind 49, listen 50, poll 7.

// emitConstHelper returns the emitter for a helper that answers a constant
// whatever its arguments: the wasm pollable surface on native, where a
// socket's readiness token IS its fd, a deadline has no pollable (-1, which
// poll ignores) and dropping one is nothing.
func emitConstHelper(name string, value int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov eax, %d", value)
		w("\tret")
	}
}

// emitPollHelper writes poll(fds, timeout_ms) -> i32: the index of the first
// fd in the i32[] that is readable, or -1 on a timeout or none. A transient
// pollfd[] (8 bytes each: fd, events, revents) is bump-allocated, every entry
// asks for POLLIN, and poll(2) takes the millisecond timeout directly (a
// negative one blocks). rbx = fds, r12 = pollfd[], r13 = nfds, r14 =
// timeout.
func emitPollHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("poll"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tsub rsp, 8") // four pushes and one slot keep rsp 16-aligned
	w("\tmov rbx, rdi")
	w("\tmov r14d, esi")
	w("\tmov r13d, %s", memRef("rbx", -4)) // nfds
	w("\txor r12d, r12d")
	w("\ttest r13d, r13d")
	w("\tjle .Lssa_poll_none")
	w("\tlea rdi, [r13 * 8]")
	w("\tcall %s", fnLabel("__alloc"))
	w("\tmov r12, rax")
	w("\txor ecx, ecx")
	w(".Lssa_poll_fill:")
	w("\tcmp ecx, r13d")
	w("\tjge .Lssa_poll_filled")
	w("\tmov eax, [rbx + rcx * 4]")
	w("\tmov [r12 + rcx * 8], eax")             // .fd
	w("\tmov dword ptr [r12 + rcx * 8 + 4], 1") // .events = POLLIN, .revents = 0
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_poll_fill")
	w(".Lssa_poll_filled:")
	w("\tmov rdi, r12")
	w("\tmov esi, r13d")
	w("\tmovsxd rdx, r14d")
	w("\tmov eax, 7") // poll
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjle .Lssa_poll_none")
	w("\txor ecx, ecx")
	w(".Lssa_poll_scan:")
	w("\tcmp ecx, r13d")
	w("\tjge .Lssa_poll_none")
	w("\ttest word ptr [r12 + rcx * 8 + 6], 1") // revents & POLLIN
	w("\tjnz .Lssa_poll_found")
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_poll_scan")
	w(".Lssa_poll_found:")
	w("\tmov eax, ecx")
	w("\tjmp .Lssa_poll_ret")
	w(".Lssa_poll_none:")
	w("\tmov eax, -1")
	w(".Lssa_poll_ret:")
	w("\tmov r14d, eax")
	w("\ttest r12, r12")
	w("\tjz .Lssa_poll_restore")
	w("\tmov rdi, r12")
	w("\tlea rsi, [r13 * 8]")
	w("\tcall %s", fnLabel("__free"))
	w(".Lssa_poll_restore:")
	w("\tmov eax, r14d")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
