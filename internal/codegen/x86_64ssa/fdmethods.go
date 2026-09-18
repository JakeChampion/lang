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
