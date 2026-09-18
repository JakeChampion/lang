package x86_64ssa

import (
	"fmt"
	"strings"
)

// Four helpers that each blocked a handful of corpus programs on their own:
// write, __fern_heap_bump_bytes, the two line readers and the two directory
// listings.

// readlineBufSym is the shared 4 KiB .bss line buffer the two line readers
// fill before they know the line's length; readlineBytes bounds one line.
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

// emitReadLineHelper returns the emitter for a line read -> Option[string]:
// one byte at a time into the .bss line buffer until '\n' (kept), the
// buffer's end, or EOF or an error, then a fresh right-sized rc string of what
// was read. None when the first read returns nothing.
//
// `fd` is the descriptor to read, or -1 to take it from a handle argument at
// [rdi + 8]: __method_Reader_read_line asks a Reader, the free read_line asks
// standard input. `sfx` keeps the local labels distinct so both can live in
// one object.
//
// rbx = buffer, r12 = bytes read then the string, r13 = fd.
func emitReadLineHelper(name, sfx string, fd int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		emitReadLineBody(w, name, sfx, fd)
	}
}

func emitReadLineBody(w func(string, ...any), name, sfx string, fd int) {
	w("")
	w("%s:", fnLabel(name))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned.
	if fd < 0 {
		w("\tmov r13d, dword ptr [rdi + 8]")
	} else {
		w("\tmov r13d, %d", fd)
	}
	w("\tlea rbx, [rip + %s]", readlineBufSym)
	w("\txor r12d, r12d")
	w(".Lssa_rrl_loop%s:", sfx)
	w("\tcmp r12, %d", readlineBytes)
	w("\tjae .Lssa_rrl_done%s", sfx)
	w("\tmov edi, r13d")
	w("\tlea rsi, [rbx + r12]")
	w("\tmov edx, 1")
	w("\txor eax, eax") // read
	w("\tsyscall")
	w("\tcmp rax, 1")
	w("\tjl .Lssa_rrl_done%s", sfx) // EOF or an error ends the line
	w("\tmovzx eax, byte ptr [rbx + r12]")
	w("\tadd r12, 1")
	w("\tcmp eax, 10") // '\n', kept
	w("\tjne .Lssa_rrl_loop%s", sfx)
	w(".Lssa_rrl_done%s:", sfx)
	w("\ttest r12, r12")
	w("\tjz .Lssa_rrl_none%s", sfx)
	w("\tlea rdx, [r12 + %d]", strBlockBytes) // header + bytes + NUL
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov dword ptr [rax + 4], r12d")
	w("\tadd rax, 8")
	emitBcopyCall(w, "rax", "rbx", "r12")
	w("\tmov byte ptr [rax + r12], 0")
	w("\tmov r12, rax")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_rrl_ret%s", sfx)
	w(".Lssa_rrl_none%s:", sfx)
	ssaOptionBox(w, 1, "")
	w(".Lssa_rrl_ret%s:", sfx)
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitReadDirLike writes a directory listing -> Result[string[], IoError]: the
// immediate children as base names, in getdents64 order as the flat backend
// lists them, with "." and ".." excluded when skipDots is set and kept when it
// is not. `lb` prefixes the local labels so both forms can live in one object.
//
// Two passes over a 4 KiB
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
func emitReadDirLike(w func(string, ...any), name, lb string, skipDots bool) {
	const (
		off   = "[rsp]"
		chunk = "[rsp + 8]"
		fill  = "[rsp + 16]"
		nptr  = "[rsp + 24]"
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
	// entryName leaves the entry's d_name pointer in r10 and, when the caller
	// excludes them, branches to skip for "." and "..".
	entryName := func(keep, skip string) {
		w("\tmov r10, %s", off)
		w("\tlea r10, [r14 + r10 + 19]") // d_name
		if !skipDots {
			return
		}
		w("\tcmp byte ptr [r10], 46") // '.'
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
	w("%s:", fnLabel(name))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	w("\tsub rsp, 48") // five pushes past the return address: 16-aligned, and so is this
	w("\tmov rbx, rdi")
	// The pathz labels take the same prefix, so the two listings do not share
	// a label when both are emitted.
	ssaPathz(w, strings.TrimPrefix(lb, ".Lssa_"))
	w("\tmov edi, -100") // AT_FDCWD
	w("\tmov rsi, r12")
	w("\tmov edx, 65536") // O_RDONLY|O_DIRECTORY
	w("\txor r10d, r10d")
	w("\tmov eax, 257") // openat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs %s_err", lb)
	w("\tmov r13d, eax")
	ssaBumpAlloc(w, "rax", "4096")
	w("\tmov r14, rax")
	w("\txor r15d, r15d")
	// Pass 1: count the kept entries.
	w("%s_g1:", lb)
	getdents(fmt.Sprintf("%s_c1", lb), fmt.Sprintf("%s_g1d", lb), fmt.Sprintf("%s_err_close", lb))
	entryName(fmt.Sprintf("%s_c1keep", lb), fmt.Sprintf("%s_c1skip", lb))
	w("\tadd r15, 1")
	w("%s_c1skip:", lb)
	advance(fmt.Sprintf("%s_c1", lb), fmt.Sprintf("%s_g1", lb))
	w("%s_g1d:", lb)
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
	w("%s_g2:", lb)
	getdents(fmt.Sprintf("%s_c2", lb), fmt.Sprintf("%s_g2d", lb), fmt.Sprintf("%s_err_close", lb))
	entryName(fmt.Sprintf("%s_c2keep", lb), fmt.Sprintf("%s_c2skip", lb))
	// The directory can gain entries between the passes; the container was
	// sized by the first, so the extras are dropped rather than written
	// past it.
	w("\tmov rcx, %s", fill)
	w("\tcmp rcx, r15")
	w("\tjae %s_c2skip", lb)
	w("\tmov %s, r10", nptr)
	w("\txor ecx, ecx")
	w("%s_len:", lb)
	w("\tcmp byte ptr [r10 + rcx], 0")
	w("\tje %s_lend", lb)
	w("\tadd rcx, 1")
	w("\tjmp %s_len", lb)
	w("%s_lend:", lb)
	w("\tmov %s, rcx", nlen)
	w("\tlea rdx, [rcx + %d]", strBlockBytes) // header + length + NUL
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov rcx, %s", nlen)
	w("\tmov dword ptr [rax + 4], ecx")
	w("\tadd rax, 8")
	w("\tmov rsi, %s", nptr)
	emitBcopyCall(w, "rax", "rsi", "rcx")
	w("\tmov rcx, %s", nlen)
	w("\tmov byte ptr [rax + rcx], 0")
	w("\tmov rcx, %s", fill)
	w("\tmov [r12 + rcx * 8], rax")
	w("\tadd rcx, 1")
	w("\tmov %s, rcx", fill)
	w("%s_c2skip:", lb)
	advance(fmt.Sprintf("%s_c2", lb), fmt.Sprintf("%s_g2", lb))
	w("%s_g2d:", lb)
	w("\tmov rcx, %s", fill)
	w("\tmov %s, ecx", memRef("r12", -4)) // len = filled: entries can also go away between the passes
	w("\tmov edi, r13d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp %s_ret", lb)
	w("%s_err_close:", lb)
	w("\tneg rax")
	w("\tmov r12d, eax") // errno, across the close
	w("\tmov edi, r13d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov eax, r12d")
	w("\tjmp %s_errno", lb)
	w("%s_err:", lb)
	w("\tneg rax")
	w("%s_errno:", lb)
	ssaIoErr(w)
	w("%s_ret:", lb)
	w("\tadd rsp, 48")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitReadDirHelper writes read_dir(path) -> Result[string[], IoError], and
// emitReadDirAllHelper the same listing with "." and ".." kept.
func emitReadDirHelper(w func(string, ...any)) {
	emitReadDirLike(w, "read_dir", ".Lssa_rd", true)
}

func emitReadDirAllHelper(w func(string, ...any)) {
	emitReadDirLike(w, "read_dir_all", ".Lssa_rdall", false)
}
