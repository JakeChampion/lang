package x86_64ssa

// The write side of the credential getters: setgroups, setgid and setuid,
// which is the order a caller has to make them in, because each drops the
// privilege the next one needs.
//
// An id outside 32 unsigned bits is refused HERE rather than truncated at the
// syscall boundary. The kernel reads the low 32 bits of the register for a
// uid_t argument, so setuid(2^32 + 1) would otherwise set uid 1, and GNU's
// "no id" spelling (uid_t) -1 has every high bit set as an i64 and would set
// uid 0xFFFFFFFF.
//
// None of the three has a path to blame, so each classifies its errno against
// a fresh empty string, the way the descriptor methods do.

// emitCredSetHelper returns the emitter for setuid or setgid -> Result[(),
// IoError]: one body over two syscall numbers.
func emitCredSetHelper(name, tag string, sysno int) func(func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx") // one push past the return address: 16-aligned for the calls
		w("\ttest rdi, rdi")
		w("\tjs .Lssa_%s_inval", tag)
		w("\tmov rax, 4294967295")
		w("\tcmp rdi, rax")
		w("\tja .Lssa_%s_inval", tag)
		w("\tmov edi, edi") // the kernel reads 32 bits of it
		w("\tmov eax, %d", sysno)
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", tag)
		ssaOptionBox(w, 0, "") // Ok
		w("\tjmp .Lssa_%s_ret", tag)
		w(".Lssa_%s_inval:", tag)
		w("\tmov eax, 22") // EINVAL, never reaching the kernel
		w("\tjmp .Lssa_%s_fail", tag)
		w(".Lssa_%s_err:", tag)
		w("\tneg rax")
		w(".Lssa_%s_fail:", tag)
		ssaFdIoErr(w, 1) // Err(IoError)
		w(".Lssa_%s_ret:", tag)
		w("\tpop rbx")
		w("\tret")
	}
}

// emitSetgroupsHelper writes setgroups(gids) -> Result[(), IoError]: the
// inverse of getgroups, which widens the kernel's 32-bit gids into 8-byte
// slots. This narrows them back.
//
// The packing goes into its OWN allocation rather than over the argument.
// Writing 4 bytes at i*4 while reading 8 at i*8 would be safe as far as
// overlap goes, but the array belongs to the caller and a builtin does not
// get to rewrite it.
//
// An empty list is setgroups(0, NULL), which is a real request — "in no
// supplementary groups" — and the one `chroot --groups=` with an empty value
// makes, so it reaches the kernel rather than answering Ok without asking.
//
// rbx = the count, r12 = the caller's data, r13 = the packed buffer,
// r14 = the cursor.
func emitSetgroupsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("setgroups"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	// Four pushes past the return address leave rsp 8 past 16-aligned.
	w("\tsub rsp, 8")
	w("\tmov r12, rdi")
	w("\tmov ebx, dword ptr [r12 - 4]") // len
	w("\ttest ebx, ebx")
	w("\tjz .Lssa_sgrp_empty")
	w("\tmov rdx, rbx")
	w("\tshl rdx, 2")
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov r13, rax")
	w("\txor r14d, r14d")
	w(".Lssa_sgrp_pack:")
	w("\tcmp r14, rbx")
	w("\tjge .Lssa_sgrp_call")
	w("\tmov rax, [r12 + r14*8]")
	w("\ttest rax, rax")
	w("\tjs .Lssa_sgrp_inval")
	w("\tmov rdx, 4294967295")
	w("\tcmp rax, rdx")
	w("\tja .Lssa_sgrp_inval")
	w("\tmov [r13 + r14*4], eax")
	w("\tadd r14, 1")
	w("\tjmp .Lssa_sgrp_pack")
	w(".Lssa_sgrp_empty:")
	w("\txor r13d, r13d") // NULL, and nothing to release
	w(".Lssa_sgrp_call:")
	w("\tmov edi, ebx")
	w("\tmov rsi, r13")
	w("\tmov eax, 116") // setgroups
	w("\tsyscall")
	// The buffer goes back to its size class before anything is boxed, so
	// the failure path releases it too. r14 is dead once the loop is done
	// and __free leaves it alone, which is where the syscall's answer waits;
	// neither rax nor rbx can hold it, since __free takes one and
	// ssaFdIoErr clobbers the other.
	w("\tmov r14, rax")
	w("\ttest r13, r13")
	w("\tjz .Lssa_sgrp_freed")
	ssaFreeGidBuf(w)
	w(".Lssa_sgrp_freed:")
	w("\tmov rax, r14")
	w("\ttest rax, rax")
	w("\tjs .Lssa_sgrp_err")
	ssaOptionBox(w, 0, "") // Ok
	w("\tjmp .Lssa_sgrp_ret")
	w(".Lssa_sgrp_inval:")
	// Reached only from inside the pack loop, so the buffer always exists.
	ssaFreeGidBuf(w)
	w("\tmov eax, 22") // EINVAL, never reaching the kernel
	w("\tjmp .Lssa_sgrp_fail")
	w(".Lssa_sgrp_err:")
	w("\tneg rax")
	w(".Lssa_sgrp_fail:")
	ssaFdIoErr(w, 1) // Err(IoError)
	w(".Lssa_sgrp_ret:")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// ssaFreeGidBuf returns the packed buffer in r13 to its size class. rbx holds
// the element count, and the block is four bytes an element — the same
// arithmetic the allocation used.
func ssaFreeGidBuf(w func(string, ...any)) {
	w("\tmov rdi, r13")
	w("\tmov esi, ebx")
	w("\tshl esi, 2")
	w("\tcall %s", fnLabel("__free"))
}
