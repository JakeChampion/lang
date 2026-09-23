package x86_64ssa

// The Reader and Writer methods that make one syscall on the descriptor the
// handle holds at [handle+8], beyond the read, write and close in handles.go:
// seek, truncate, write_some, fsync, fdatasync, syncfs, dup_onto and flags,
// plus the free sync(2) that names no descriptor at all.
//
// None of them has a path to report, so each classifies its errno against a
// fresh empty string — the contract __fern_io_error is written to, since the
// box it builds owns the string it is handed.

// ssaFdIoErr appends the failure tail for a helper with no path to name: with
// the positive errno in eax, build the IoError against a fresh empty string
// and leave it boxed under `tag`. rbx is scratch, and the site must call at a
// 16-aligned rsp.
func ssaFdIoErr(w func(string, ...any), tag int) {
	w("\tmov ebx, eax") // errno, across the allocation
	ssaEmptyString(w, "rsi")
	w("\tmov edi, ebx")
	w("\tcall %s", fnLabel("__fern_io_error"))
	w("\tmov rbx, rax")
	ssaOptionBox(w, tag, "rbx")
}

// emitFdCallHelper returns the emitter for a method that makes one syscall on
// the handle's descriptor and answers Option[IoError]: None on success,
// Some(IoError) on failure. fsync, fdatasync, syncfs, dup_onto and truncate
// are all this shape — Writer.close is the same contract with a different
// number.
//
// Arguments past the descriptor arrive in place and stay there: the fd load
// writes rdi and touches nothing else, so ftruncate's length reaches the
// kernel in rsi untouched. `prep` is for the one argument that does need
// adjusting before that load. `tag` prefixes the local labels so every method
// can live in one object.
func emitFdCallHelper(name, tag string, sysno int, prep func(w func(string, ...any))) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx") // one push past the return address: 16-aligned for the calls
		if prep != nil {
			prep(w)
		}
		w("\tmov edi, dword ptr [rdi + 8]") // fd
		w("\tmov eax, %d", sysno)
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", tag)
		ssaOptionBox(w, 1, "") // None
		w("\tjmp .Lssa_%s_ret", tag)
		w(".Lssa_%s_err:", tag)
		w("\tneg rax")
		ssaFdIoErr(w, 0) // Some(IoError)
		w(".Lssa_%s_ret:", tag)
		w("\tpop rbx")
		w("\tret")
	}
}

// prepDupOnto sets up dup3(own_fd, fd, 0) for emitFdCallHelper: the
// destination arrives in esi and is SIGN-extended rather than zero-extended,
// so a negative one stays negative and answers EBADF.
func prepDupOnto(w func(string, ...any)) {
	w("\tmovsxd rsi, esi")
	w("\txor edx, edx")
}

// emitSeekHelper returns the emitter for Reader.seek / Writer.seek ->
// Result[i64, IoError]: lseek(2) on the handle's fd, the new offset back. A
// pipe answers ESPIPE. The offset and whence arrive in rsi and rdx, which is
// where lseek wants them.
func emitSeekHelper(name, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx")
		w("\tmov edi, dword ptr [rdi + 8]") // fd
		w("\tmov eax, 8")                   // lseek
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", tag)
		w("\tmov rbx, rax")
		ssaOptionBox(w, 0, "rbx") // Ok(offset)
		w("\tjmp .Lssa_%s_ret", tag)
		w(".Lssa_%s_err:", tag)
		w("\tneg rax")
		ssaFdIoErr(w, 1) // Err(e)
		w(".Lssa_%s_ret:", tag)
		w("\tpop rbx")
		w("\tret")
	}
}

// emitReaderSpliceHelper writes __method_Reader_splice_to(reader, writer, max)
// -> Result[i64, IoError], the x86_64 codegen's __fern_reader_splice: a direct
// splice(2), and on EINVAL the pipe cached in __fern_splice_pipe after an
// empty-pipe probe of the writer. Every failure before bytes leave the reader
// is Unsupported; a drain failure is the writer's and discards the pipe.
func emitReaderSpliceHelper(w func(string, ...any)) {
	splice := func(in, out, n string, flags int) {
		w("\tmov edi, %s", in)
		w("\txor esi, esi")
		w("\tmov edx, %s", out)
		w("\txor r10d, r10d")
		w("\tmov r8, %s", n)
		w("\tmov r9d, %d", flags)
		w("\tmov eax, 275") // splice
		w("\tsyscall")
	}
	const pipeR, pipeW = "dword ptr [rip + __fern_splice_pipe]", "dword ptr [rip + __fern_splice_pipe + 4]"
	w("")
	w("%s:", fnLabel("__method_Reader_splice_to"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	w("\tmov r12d, dword ptr [rdi + 8]") // reader fd
	w("\tmov r13d, dword ptr [rsi + 8]") // writer fd
	w("\tmovsxd r14, edx")               // max
	splice("r12d", "r13d", "r14", 0)
	w("\tmov rbx, rax")
	w("\ttest rax, rax")
	w("\tjns .Lssa_rspl_grow")
	w("\tcmp rax, -22") // EINVAL: no pipe on either side
	w("\tjne .Lssa_rspl_unsup")
	w("\tcmp qword ptr [rip + __fern_splice_pipe], 0")
	w("\tjne .Lssa_rspl_have")
	w("\tlea rdi, [rip + __fern_splice_pipe]")
	w("\tmov esi, 0x80000") // O_CLOEXEC
	w("\tmov eax, 293")     // pipe2
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_rspl_unsup")
	w("\tmov edi, %s", pipeW)
	w("\tmov esi, 1031") // F_SETPIPE_SZ
	w("\tmov rdx, r14")
	w("\tmov eax, 72") // fcntl
	w("\tsyscall")
	w(".Lssa_rspl_have:")
	splice(pipeR, "r13d", "1", 2) // SPLICE_F_NONBLOCK
	w("\tcmp rax, -11")           // EAGAIN: the writer takes spliced bytes
	w("\tjne .Lssa_rspl_unsup")
	splice("r12d", pipeW, "r14", 0)
	w("\ttest rax, rax")
	w("\tjs .Lssa_rspl_unsup")
	w("\tmov rbx, rax") // moved
	w("\tmov r15, rax") // still in the pipe
	w(".Lssa_rspl_drain:")
	w("\ttest r15, r15")
	w("\tjz .Lssa_rspl_ok")
	splice(pipeR, "r13d", "r15", 0)
	w("\ttest rax, rax")
	w("\tjle .Lssa_rspl_derr")
	w("\tsub r15, rax")
	w("\tjmp .Lssa_rspl_drain")
	w(".Lssa_rspl_derr:")
	w("\tcmp rax, -4") // EINTR
	w("\tje .Lssa_rspl_drain")
	w("\tmov rbx, rax")
	w("\tmov edi, %s", pipeR)
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov edi, %s", pipeW)
	w("\tmov eax, 3")
	w("\tsyscall")
	w("\tmov qword ptr [rip + __fern_splice_pipe], 0")
	w("\tmov eax, 5") // a writer that takes nothing without saying why is EIO
	w("\tmov rcx, rbx")
	w("\tneg rcx")
	w("\ttest rbx, rbx")
	w("\tcmovnz eax, ecx")
	ssaFdIoErr(w, 1) // Err(e)
	w("\tjmp .Lssa_rspl_ret")
	// A direct splice had a pipe on one side; a writer that is one grows to
	// hold max, as GNU grows stdout's, or each call moves 64 KiB.
	w(".Lssa_rspl_grow:")
	w("\tlea ecx, [r13 + 1]") // the writer last checked, plus one
	w("\tcmp dword ptr [rip + __fern_splice_pipe + 8], ecx")
	w("\tje .Lssa_rspl_ok")
	w("\tmov dword ptr [rip + __fern_splice_pipe + 8], ecx")
	w("\tmov edi, r13d")
	w("\tmov esi, 1032") // F_GETPIPE_SZ
	w("\tmov eax, 72")
	w("\tsyscall")
	w("\tcmp rax, r14")
	w("\tjge .Lssa_rspl_ok")
	w("\ttest rax, rax")
	w("\tjs .Lssa_rspl_ok")
	w("\tmov edi, r13d")
	w("\tmov esi, 1031") // F_SETPIPE_SZ
	w("\tmov rdx, r14")
	w("\tmov eax, 72")
	w("\tsyscall")
	w("\tjmp .Lssa_rspl_ok")
	w(".Lssa_rspl_unsup:")
	ssaOptionBox(w, 5, "") // IoError::Unsupported
	w("\tmov rbx, rax")
	ssaOptionBox(w, 1, "rbx") // Err
	w("\tjmp .Lssa_rspl_ret")
	w(".Lssa_rspl_ok:")
	ssaOptionBox(w, 0, "rbx") // Ok(moved)
	w(".Lssa_rspl_ret:")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
	w(".section .bss")
	w(".align 8")
	w("__fern_splice_pipe: .skip 16")
	w(".text")
}

// emitFdFlagsHelper returns the emitter for Reader.flags / Writer.flags ->
// Result[i64, IoError]: fcntl(fd, F_GETFL) reduced to Fern's own three bits —
// 1 readable, 2 writable, 4 appending. The kernel's access mode is a VALUE in
// the low two bits (0 read-only, 1 write-only, 2 read-write), so the two bits
// come out of two comparisons against it rather than a mask.
func emitFdFlagsHelper(name, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx")
		w("\tmov edi, dword ptr [rdi + 8]") // fd
		w("\tmov esi, 3")                   // F_GETFL
		w("\txor edx, edx")
		w("\tmov eax, 72") // fcntl
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", tag)
		w("\tmov edx, eax")
		w("\tand edx, 3") // the access mode
		w("\txor ebx, ebx")
		w("\tcmp edx, 1") // write-only is the one mode that cannot read
		w("\tje .Lssa_%s_nord", tag)
		w("\tor ebx, 1")
		w(".Lssa_%s_nord:", tag)
		w("\ttest edx, edx") // read-only is the one mode that cannot write
		w("\tjz .Lssa_%s_nowr", tag)
		w("\tor ebx, 2")
		w(".Lssa_%s_nowr:", tag)
		w("\ttest eax, 1024") // O_APPEND
		w("\tjz .Lssa_%s_noap", tag)
		w("\tor ebx, 4")
		w(".Lssa_%s_noap:", tag)
		ssaOptionBox(w, 0, "rbx") // Ok(bits)
		w("\tjmp .Lssa_%s_ret", tag)
		w(".Lssa_%s_err:", tag)
		w("\tneg rax")
		ssaFdIoErr(w, 1) // Err(e)
		w(".Lssa_%s_ret:", tag)
		w("\tpop rbx")
		w("\tret")
	}
}

// emitWriterWriteSomeHelper writes __method_Writer_write_some(writer, s) ->
// Result[i64, IoError]: ONE write(2) and the count it returned. The loop in
// __method_Writer_write is what makes that count unobservable there — a
// failure has forgotten what landed before it — and the count is output for
// shred's failing-write offset and dd's record tally (#9231). Zero is a real
// answer rather than an error.
func emitWriterWriteSomeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Writer_write_some"))
	w("\tpush rbx")
	w("\tmov edx, %s", memRef("rsi", -4)) // byte length; the data is already in rsi
	w("\tmov edi, dword ptr [rdi + 8]")   // fd
	w("\tmov eax, 1")                     // write
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_wrws_err")
	w("\tmov rbx, rax")
	ssaOptionBox(w, 0, "rbx") // Ok(count)
	w("\tjmp .Lssa_wrws_ret")
	w(".Lssa_wrws_err:")
	w("\tneg rax")
	ssaFdIoErr(w, 1) // Err(e)
	w(".Lssa_wrws_ret:")
	w("\tpop rbx")
	w("\tret")
}

// emitSyncHelper writes sync() — sync(2) over every mounted filesystem. No
// arguments, no result and no failure, so it is a leaf with no frame at all.
func emitSyncHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("sync"))
	w("\tmov eax, 162") // sync
	w("\tsyscall")
	w("\tret")
}
