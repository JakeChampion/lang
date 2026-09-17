package x86_64ssa

// Reader / Writer handles, and the process-level helpers that go with them.
//
// A handle is a 24-byte block whose value pointer is base+8: the slot at
// ptr+0 is the struct-type id (unused here) and the fd is an i32 at ptr+8.
// These offsets are the IR contract shared with arm64ssa, whose helpers are
// the semantic reference for everything in this file. What differs is the
// ISA: SysV puts arguments in rdi/rsi/rdx and the syscall number in rax, and
// the Linux x86-64 numbers are their own (read 0, write 1, close 3,
// openat 257, exit_group 231) rather than AArch64's.

import "strconv"

// heapRewindComment marks a cursor store that LOWERS the cursor: a site handing
// back space nothing owns, which needs no guard because the arena end moves no
// closer. It is the only legitimate unguarded cursor store, so
// TestEveryHeapBumpPublishesThroughTheGuard reads the marker rather than
// exempting the shape. The arm64ssa sibling carries the same contract.
const heapRewindComment = " // heap rewind"

// ssaHeapRewind publishes a cursor at or below the current one. `valueReg`
// holds the value to publish.
func ssaHeapRewind(w func(string, ...any), valueReg string) {
	w("\tmov [rip + %s], %s%s", heapPtrSym, valueReg, heapRewindComment)
}

// ssaHandleAlloc writes a handle and leaves its value pointer in rax. `fd` is
// a 32-bit source operand — an immediate, or a register the caller has kept
// live — stored AFTER the allocation. `immortal` selects the 0x80000000 rc
// sentinel the std handles carry, so a drop of stdin/stdout/stderr
// short-circuits instead of trying to free a handle the process never owned.
//
// fd must not name rax or r11: ssaBumpAlloc returns into the first and uses
// the second as its scratch, so either is gone by the time the fd is stored.
// The caller is also responsible for 16-byte stack alignment, since the
// allocation calls the heap guard.
func ssaHandleAlloc(w func(string, ...any), fd string, immortal bool) {
	ssaBumpAlloc(w, "rax", "24")
	if immortal {
		w("\tmov dword ptr [rax], 0x80000000")
	} else {
		w("\tmov dword ptr [rax], 1")
	}
	w("\tmov dword ptr [rax + 4], 16") // payload size
	w("\tmov qword ptr [rax + 8], 0")  // struct-type id slot @ ptr+0
	w("\tmov dword ptr [rax + 16], %s", fd)
	w("\tadd rax, 8")
}

// ssaOptionBox writes a 24-byte {tag, payload} box and leaves its value
// pointer in rax. Tag 1 with no payload is None; tag 0 with payloadReg is
// Some. The same block serves Result, where 0 is Ok and 1 is Err — the
// layout is identical and only the meaning of the tag changes.
func ssaOptionBox(w func(string, ...any), tag int, payloadReg string) {
	ssaBumpAlloc(w, "rax", "24")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tadd rax, 8")
	w("\tmov dword ptr [rax], %d", tag)
	if payloadReg == "" {
		w("\tmov qword ptr [rax + 8], 0")
	} else {
		w("\tmov [rax + 8], %s", payloadReg)
	}
}

// ssaEmptyString leaves a fresh empty single-word rc string in dst. The
// helpers here hand one to __fern_io_error when they have no path to report,
// which is what arm64ssa's emitEmptyString does for the same call.
func ssaEmptyString(w func(string, ...any), dst string) {
	ssaBumpAlloc(w, "rax", "9")
	w("\tmov dword ptr [rax], 1")     // rc = 1
	w("\tmov dword ptr [rax + 4], 0") // len = 0
	w("\tlea %s, [rax + 8]", dst)
	w("\tmov byte ptr [%s], 0", dst)
}

// emitStdHandleHelper writes stdin / stdout / stderr. The result is a bare
// handle over the fixed fd, not a Result: these cannot fail.
func emitStdHandleHelper(name string, fd int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tsub rsp, 8") // align for the guard call inside ssaBumpAlloc
		ssaHandleAlloc(w, strconv.Itoa(fd), true)
		w("\tadd rsp, 8")
		w("\tret")
	}
}

// emitExitHelper writes exit(status) — exit_group(2), which never returns.
// arm64ssa runs the leak census here, because exit() bypasses the _start
// epilogue that would otherwise report it; this backend does not implement
// -leakcheck at all (internal/ast's note on the flag), so there is nothing to
// run and no census seam to keep.
func emitExitHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("exit"))
	w("\tmov eax, 231") // exit_group; status already in edi
	w("\tsyscall")
}

// emitHandleCloseHelper writes __method_Reader_close / __method_Writer_close
// -> Option[IoError]: close(2) on the handle's fd, None on success.
func emitHandleCloseHelper(name, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		errL := ".Lssa_" + tag + "_err"
		retL := ".Lssa_" + tag + "_ret"
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx") // one push past the return address: 16-aligned for the calls
		w("\tmov edi, dword ptr [rdi + 8]")
		w("\tmov eax, 3") // close
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs %s", errL)
		ssaOptionBox(w, 1, "")
		w("\tjmp %s", retL)
		w("%s:", errL)
		w("\tneg rax")
		w("\tmov ebx, eax") // errno
		ssaEmptyString(w, "rsi")
		w("\tmov edi, ebx")
		w("\tcall %s", fnLabel("__fern_io_error"))
		w("\tmov rbx, rax") // IoError box
		ssaOptionBox(w, 0, "rbx")
		w("%s:", retL)
		w("\tpop rbx")
		w("\tret")
	}
}

// emitReaderReadChunkHelper writes __method_Reader_read_chunk(reader, n) ->
// Result[string, IoError]: one read(2) of up to n bytes into a fresh
// single-word rc string.
//
// The buffer is sized from the REQUEST and the helper only learns what it owns
// after the read, so it hands the rest back rather than stranding it: the
// unread tail of a short read, and the whole buffer at end of input or on an
// error, where the result carries no bytes at all. A pipe hands back at most
// 64 KiB, so without the trim a program looping over a 64 KiB request leaks a
// full block per round (#8698 on the arm64 side).
//
// rbx = the cursor before the bump (the rewind target), r12 = data pointer,
// r13 = fd then errno. All three must be callee-saved: the allocation calls
// the heap guard and the error path calls __fern_io_error.
func emitReaderReadChunkHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Reader_read_chunk"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned.
	w("\tmov r13d, dword ptr [rdi + 8]") // fd
	w("\tmov r12, rsi")                  // n
	w("\tmov rbx, [rip + %s]", heapPtrSym)
	// Header + n + trailing NUL, rounded to its size class: a raw bump, so
	// the rewind below can lower the cursor, but sized as __alloc would
	// size it, which is the extent __fern_str_append takes a string to own.
	w("\tlea rax, [r12 + 9]")
	emitFreelistClass(w, "rrc_req", "rax", "rdx", ".Lssa_rrc_req_none")
	w(".Lssa_rrc_req_none:")
	ssaInlineBump(w, "rcx", "rax")
	w("\tmov dword ptr [rcx], 1") // rc = 1
	w("\tadd rcx, 8")
	w("\tmov rax, r12") // n, before rcx moves into the arg register
	w("\tmov r12, rcx") // data pointer
	// read(fd, data, n)
	w("\tmov edi, r13d")
	w("\tmov rsi, r12")
	w("\tmov rdx, rax")
	w("\txor eax, eax") // read
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_rrc_err")
	w("\tje .Lssa_rrc_eof")
	w("\tmov dword ptr [r12 - 4], eax") // len = bytes read
	w("\tmov byte ptr [r12 + rax], 0")  // trailing NUL
	w("\tlea rsi, [rax + 9]")           // header + bytes + NUL, to the class it now fills
	emitFreelistClass(w, "rrc_got", "rsi", "rdx", ".Lssa_rrc_got_none")
	w(".Lssa_rrc_got_none:")
	w("\tlea rcx, [r12 - 8]")
	w("\tadd rcx, rsi") // base + that class's extent, never above the cursor
	ssaHeapRewind(w, "rcx")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_rrc_ret")
	w(".Lssa_rrc_eof:")
	ssaHeapRewind(w, "rbx") // nothing owns the buffer
	ssaEmptyString(w, "r12")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_rrc_ret")
	w(".Lssa_rrc_err:")
	ssaHeapRewind(w, "rbx") // nothing owns the buffer
	w("\tneg rax")
	w("\tmov r13d, eax") // errno
	// A read carries no path, so the errno is classified against an empty one,
	// exactly as a failed write is.
	ssaEmptyString(w, "rsi")
	w("\tmov edi, r13d")
	w("\tcall %s", fnLabel("__fern_io_error"))
	w("\tmov r12, rax")
	ssaOptionBox(w, 1, "r12")
	w(".Lssa_rrc_ret:")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitOpenHandleHelper writes open_reader / open_writer -> Result[handle,
// IoError]: NUL-terminate the path into a fresh heap buffer, openat it, wrap
// the fd in a handle, and return Ok(handle); on failure map -errno through
// __fern_io_error and return Err.
//
// Two things differ from the arm64 sibling beyond the mnemonics. fcntl is 72
// on x86-64 where AArch64 numbers it 25, and the fd has to survive in a
// CALLEE-saved register: arm64 keeps it in w9 across an inline allocation,
// but ssaBumpAlloc here calls the heap guard, which may clobber any
// caller-saved register.
//
// rbx = path, r12 = pathz / handle / IoError, r13 = fd.
func emitOpenHandleHelper(name, lbl string, flags, mode int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx")
		w("\tpush r12")
		w("\tpush r13")
		// Three pushes past the return address leave rsp 16-aligned.
		w("\tmov rbx, rdi") // path
		ssaPathz(w, lbl)
		// openat(AT_FDCWD, pathz, flags, mode)
		w("\tmov edi, -100")
		w("\tmov rsi, r12")
		w("\tmov edx, %d", flags)
		w("\tmov r10d, %d", mode)
		w("\tmov eax, 257")
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", lbl)
		// A descriptor below 3 is a standard stream the program was exec'd
		// without: move it up (fcntl F_DUPFD 3) and close the original, so
		// stdout() never aliases the file (#8823).
		w("\tcmp rax, 3")
		w("\tjae .Lssa_%s_hi", lbl)
		w("\tmov r13, rax") // the low descriptor
		w("\tmov edi, eax")
		w("\txor esi, esi") // F_DUPFD
		w("\tmov edx, 3")
		w("\tmov eax, 72") // fcntl
		w("\tsyscall")
		w("\tmov r12, rax") // the moved-up descriptor (or -errno)
		w("\tmov edi, r13d")
		w("\tmov eax, 3") // close the original
		w("\tsyscall")
		w("\tmov rax, r12")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", lbl)
		w(".Lssa_%s_hi:", lbl)
		w("\tmov r13d, eax") // fd, across the two allocations below
		ssaHandleAlloc(w, "r13d", true)
		w("\tmov r12, rax") // handle
		ssaOptionBox(w, 0, "r12")
		w("\tjmp .Lssa_%s_ret", lbl)
		w(".Lssa_%s_err:", lbl)
		w("\tneg rax")
		w("\tmov r13d, eax") // errno
		ssaRetainPathForIoErr(w)
		w("\tmov edi, r13d")
		w("\tmov rsi, rbx") // the path, as given
		w("\tcall %s", fnLabel("__fern_io_error"))
		w("\tmov r12, rax")
		ssaOptionBox(w, 1, "r12")
		w(".Lssa_%s_ret:", lbl)
		w("\tpop r13")
		w("\tpop r12")
		w("\tpop rbx")
		w("\tret")
	}
}

// emitWriterWriteHelper writes __method_Writer_write(writer, s) ->
// Option[IoError]: write(2) in a loop until every byte of the string is out,
// since a short write is not an error. None on success.
func emitWriterWriteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Writer_write"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tsub rsp, 8")                   // five slots past the return address: 16-aligned for the calls
	w("\tmov ebx, dword ptr [rdi + 8]") // fd
	w("\tmov r12, rsi")                 // data
	w("\tmov r13d, %s", memRef("rsi", -4))
	w("\txor r14d, r14d") // bytes written
	w(".Lssa_wrw_loop:")
	w("\tcmp r14d, r13d")
	w("\tjae .Lssa_wrw_done")
	w("\tmov edi, ebx")
	w("\tlea rsi, [r12 + r14]")
	w("\tmov edx, r13d")
	w("\tsub edx, r14d")
	w("\tmov eax, 1") // write
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_wrw_err")
	w("\tadd r14, rax")
	w("\tjmp .Lssa_wrw_loop")
	w(".Lssa_wrw_done:")
	ssaOptionBox(w, 1, "")
	w("\tjmp .Lssa_wrw_ret")
	w(".Lssa_wrw_err:")
	w("\tneg rax")
	w("\tmov ebx, eax") // errno
	ssaEmptyString(w, "rsi")
	w("\tmov edi, ebx")
	w("\tcall %s", fnLabel("__fern_io_error"))
	w("\tmov rbx, rax")
	ssaOptionBox(w, 0, "rbx")
	w(".Lssa_wrw_ret:")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
