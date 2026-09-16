package x86_64ssa

// The kernel-CSPRNG helpers, both through getrandom(2), syscall 318.

// emitRandomBytesHelper writes random_bytes(n) -> u8[]: a fresh zero-filled
// u8[] of n bytes from __alloc_u8, filled by getrandom in a loop. The loop is
// the point: past one page getrandom may write fewer bytes and return early
// when a signal arrives, or -EINTR having written none, and one call would
// leave the tail as the allocator left it — zeros where the caller asked for
// randomness (#9221). Any other error ends the loop; the signature has
// nowhere to report one. rbx = data, r12 = cursor, r13 = bytes still wanted.
func emitRandomBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("random_bytes"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned.
	w("\tmov r13d, edi")
	w("\tcall %s", fnLabel("__alloc_u8"))
	w("\tmov rbx, rax")
	w("\tmov r12, rax")
	w(".Lssa_randbytes_loop:")
	w("\ttest r13, r13")
	w("\tjz .Lssa_randbytes_done")
	w("\tmov rdi, r12")
	w("\tmov rsi, r13")
	w("\txor edx, edx")
	w("\tmov eax, 318") // getrandom
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjg .Lssa_randbytes_wrote")
	w("\tcmp rax, -4") // -EINTR: nothing written, try again
	w("\tje .Lssa_randbytes_loop")
	w("\tjmp .Lssa_randbytes_done")
	w(".Lssa_randbytes_wrote:")
	w("\tadd r12, rax")
	w("\tsub r13, rax")
	w("\tjmp .Lssa_randbytes_loop")
	w(".Lssa_randbytes_done:")
	w("\tmov rax, rbx")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitRandomI32Helper writes random_i32() -> i32: four bytes from getrandom
// into the frame, read back as the result. Leaf.
func emitRandomI32Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("random_i32"))
	w("\tsub rsp, 24")
	w("\tmov rdi, rsp")
	w("\tmov esi, 4")
	w("\txor edx, edx")
	w("\tmov eax, 318") // getrandom
	w("\tsyscall")
	w("\tmovsxd rax, dword ptr [rsp]")
	w("\tadd rsp, 24")
	w("\tret")
}
