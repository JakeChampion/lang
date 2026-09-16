package x86_64ssa

// Four helpers that each blocked a handful of corpus programs on their own:
// write, __fern_heap_bump_bytes, __method_Reader_read_line and read_dir.

// readlineBufSym is the shared 4 KiB .bss line buffer Reader.read_line fills
// before it knows the line's length; readlineBytes bounds one line.
const (
	readlineBufSym = "__ssa_readline_buf"
	readlineBytes  = 4096
)

// emitWriteHelper writes write(s): print's no-newline sibling, one write(2)
// of the string's bytes to stdout. Not a short-write loop, as print is not:
// this leg exists to compare with the flat backend, which has none either.
// Leaf; the unused return is 0.
func emitWriteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("write"))
	w("\tmov edx, %s", memRef("rdi", -4)) // len
	w("\tmov rsi, rdi")                   // buf
	w("\tmov edi, 1")                     // stdout
	w("\tmov eax, 1")                     // write
	w("\tsyscall")
	w("\txor eax, eax")
	w("\tret")
}

// emitHeapBumpBytesHelper writes __fern_heap_bump_bytes() -> i64: the bump
// high-water mark, cursor minus base. Zero before _start seeds the
// reservation, which only a program that never allocates can observe. Leaf.
func emitHeapBumpBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_heap_bump_bytes"))
	w("\tmov rax, [rip + %s]", heapPtrSym)
	w("\ttest rax, rax")
	w("\tjz .Lssa_bumpb_ret")
	w("\tsub rax, [rip + %s]", heapBaseSym)
	w(".Lssa_bumpb_ret:")
	w("\tret")
}

// emitReaderReadLineHelper writes __method_Reader_read_line(reader) ->
// Option[string]: one byte at a time from the handle's fd (at [reader + 8])
// into the .bss line buffer until '\n' (kept), the buffer's end, or EOF or an
// error, then a fresh right-sized rc string of what was read. None when the
// first read returns nothing. rbx = buffer, r12 = bytes read then the string,
// r13 = fd.
func emitReaderReadLineHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Reader_read_line"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned.
	w("\tmov r13d, dword ptr [rdi + 8]") // fd
	w("\tlea rbx, [rip + %s]", readlineBufSym)
	w("\txor r12d, r12d")
	w(".Lssa_rrl_loop:")
	w("\tcmp r12, %d", readlineBytes)
	w("\tjae .Lssa_rrl_done")
	w("\tmov edi, r13d")
	w("\tlea rsi, [rbx + r12]")
	w("\tmov edx, 1")
	w("\txor eax, eax") // read
	w("\tsyscall")
	w("\tcmp rax, 1")
	w("\tjl .Lssa_rrl_done") // EOF or an error ends the line
	w("\tmovzx eax, byte ptr [rbx + r12]")
	w("\tadd r12, 1")
	w("\tcmp eax, 10") // '\n', kept
	w("\tjne .Lssa_rrl_loop")
	w(".Lssa_rrl_done:")
	w("\ttest r12, r12")
	w("\tjz .Lssa_rrl_none")
	w("\tlea rdx, [r12 + 9]") // header + bytes + NUL
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov dword ptr [rax + 4], r12d")
	w("\tadd rax, 8")
	emitBcopyCall(w, "rax", "rbx", "r12")
	w("\tmov byte ptr [rax + r12], 0")
	w("\tmov r12, rax")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_rrl_ret")
	w(".Lssa_rrl_none:")
	ssaOptionBox(w, 1, "")
	w(".Lssa_rrl_ret:")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitReadDirHelper writes read_dir(path) -> Result[string[], IoError]: the
// immediate children of a directory as base names, "." and ".." excluded, in
// getdents64 order as the flat backend lists them. Two passes over a 4 KiB
// scratch block: the first counts the kept entries to size the container, an
// lseek rewinds, and the second allocates a string per name. Each pass drains
// getdents64 until it returns 0, so a directory of any size is listed. The
// second pass sees the directory as it is then: an entry that appeared since
// the first is dropped, and the length is the count the second pass reached.
//
// rbx = path, r12 = pathz then the container's data, r13 = fd, r14 = the
// scratch block, r15 = count; the frame carries the chunk offset, the chunk
// length, the fill index, and the current name's pointer and length across
// the string allocation, whose guard call may clobber the rest.
func emitReadDirHelper(w func(string, ...any)) {
	const (
		off   = "[rsp]"
		chunk = "[rsp + 8]"
		fill  = "[rsp + 16]"
		name  = "[rsp + 24]"
		nlen  = "[rsp + 32]"
	)
	getdents := func(next, end, fail string) {
		w("\tmov edi, r13d")
		w("\tmov rsi, r14")
		w("\tmov edx, 4096")
		w("\tmov eax, 217") // getdents64
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjz %s", end)
		w("\tjs %s", fail)
		w("\tmov %s, rax", chunk)
		w("\tmov qword ptr %s, 0", off)
		w("%s:", next)
	}
	// dotSkip branches to skip when the entry at r14 + [rsp] is "." or "..",
	// leaving its d_name pointer in r10.
	dotSkip := func(keep, skip string) {
		w("\tmov r10, %s", off)
		w("\tlea r10, [r14 + r10 + 19]") // d_name
		w("\tcmp byte ptr [r10], 46")    // '.'
		w("\tjne %s", keep)
		w("\tmovzx eax, byte ptr [r10 + 1]")
		w("\ttest eax, eax")
		w("\tjz %s", skip) // "."
		w("\tcmp eax, 46")
		w("\tjne %s", keep)
		w("\tcmp byte ptr [r10 + 2], 0")
		w("\tje %s", skip) // ".."
		w("%s:", keep)
	}
	advance := func(loop, again string) {
		w("\tmov rax, %s", off)
		w("\tmovzx ecx, word ptr [r14 + rax + 16]") // d_reclen
		w("\tadd rax, rcx")
		w("\tmov %s, rax", off)
		w("\tcmp rax, %s", chunk)
		w("\tjb %s", loop)
		w("\tjmp %s", again)
	}
	w("")
	w("%s:", fnLabel("read_dir"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	w("\tsub rsp, 48") // five pushes past the return address: 16-aligned, and so is this
	w("\tmov rbx, rdi")
	ssaPathz(w, "rd")
	w("\tmov edi, -100") // AT_FDCWD
	w("\tmov rsi, r12")
	w("\tmov edx, 65536") // O_RDONLY|O_DIRECTORY
	w("\txor r10d, r10d")
	w("\tmov eax, 257") // openat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_rd_err")
	w("\tmov r13d, eax")
	ssaBumpAlloc(w, "rax", "4096")
	w("\tmov r14, rax")
	w("\txor r15d, r15d")
	// Pass 1: count the kept entries.
	w(".Lssa_rd_g1:")
	getdents(".Lssa_rd_c1", ".Lssa_rd_g1d", ".Lssa_rd_err_close")
	dotSkip(".Lssa_rd_c1keep", ".Lssa_rd_c1skip")
	w("\tadd r15, 1")
	w(".Lssa_rd_c1skip:")
	advance(".Lssa_rd_c1", ".Lssa_rd_g1")
	w(".Lssa_rd_g1d:")
	w("\tmov edi, r13d")
	w("\txor esi, esi")
	w("\txor edx, edx")
	w("\tmov eax, 8") // lseek to 0
	w("\tsyscall")
	// The string[] container: 16-byte header, then count pointers.
	w("\tlea rdx, [r15 * 8 + 16]")
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tadd rax, 16")
	w("\tmov %s, r15d", memRef("rax", -12)) // cap
	w("\tmov dword ptr [rax - 8], 1")       // rc = 1
	w("\tmov %s, r15d", memRef("rax", -4))  // len
	w("\tmov r12, rax")
	w("\tmov qword ptr %s, 0", fill)
	// Pass 2: a fresh string per kept entry.
	w(".Lssa_rd_g2:")
	getdents(".Lssa_rd_c2", ".Lssa_rd_g2d", ".Lssa_rd_err_close")
	dotSkip(".Lssa_rd_c2keep", ".Lssa_rd_c2skip")
	// The directory can gain entries between the passes; the container was
	// sized by the first, so the extras are dropped rather than written
	// past it.
	w("\tmov rcx, %s", fill)
	w("\tcmp rcx, r15")
	w("\tjae .Lssa_rd_c2skip")
	w("\tmov %s, r10", name)
	w("\txor ecx, ecx")
	w(".Lssa_rd_len:")
	w("\tcmp byte ptr [r10 + rcx], 0")
	w("\tje .Lssa_rd_lend")
	w("\tadd rcx, 1")
	w("\tjmp .Lssa_rd_len")
	w(".Lssa_rd_lend:")
	w("\tmov %s, rcx", nlen)
	w("\tlea rdx, [rcx + 9]") // header + length + NUL
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov rcx, %s", nlen)
	w("\tmov dword ptr [rax + 4], ecx")
	w("\tadd rax, 8")
	w("\tmov rsi, %s", name)
	emitBcopyCall(w, "rax", "rsi", "rcx")
	w("\tmov rcx, %s", nlen)
	w("\tmov byte ptr [rax + rcx], 0")
	w("\tmov rcx, %s", fill)
	w("\tmov [r12 + rcx * 8], rax")
	w("\tadd rcx, 1")
	w("\tmov %s, rcx", fill)
	w(".Lssa_rd_c2skip:")
	advance(".Lssa_rd_c2", ".Lssa_rd_g2")
	w(".Lssa_rd_g2d:")
	w("\tmov rcx, %s", fill)
	w("\tmov %s, ecx", memRef("r12", -4)) // len = filled: entries can also go away between the passes
	w("\tmov edi, r13d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_rd_ret")
	w(".Lssa_rd_err_close:")
	w("\tneg rax")
	w("\tmov r12d, eax") // errno, across the close
	w("\tmov edi, r13d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov eax, r12d")
	w("\tjmp .Lssa_rd_errno")
	w(".Lssa_rd_err:")
	w("\tneg rax")
	w(".Lssa_rd_errno:")
	ssaIoErr(w)
	w(".Lssa_rd_ret:")
	w("\tadd rsp, 48")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
