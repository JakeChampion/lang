package x86_64ssa

// The range-copy kernel consumes a data pointer, shared by packed byte arrays.
func emitBufPushBytesRangeHelper(w func(string, ...any)) {
	w("%s:", fnLabel("buf_push_bytes_range"))
	w("\txor eax, eax")
	w("\ttest edx, edx")
	w("\tcmovs edx, eax")
	w("\tcmp ecx, [rsi - 4]")
	w("\tcmovg ecx, [rsi - 4]")
	w("\tjmp %s", fnLabel("buf_push_range"))
}

// Copy builder bytes into an independently owned array and reset its length.
func emitBufTakeBytesHelper(w func(string, ...any)) {
	w("%s:", fnLabel("buf_take_bytes"))
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tmov rbx, rdi")
	w("\tmov r12, qword ptr [rbx + 8]")
	// Copy initializes all payload bytes; retain the normal array header and
	// __alloc_u8's empty/invalid-length behavior without redundant zero fill.
	w("\ttest r12d, r12d")
	w("\tjle .Lbuftake_bytes_alloc_special")
	w("\tlea edi, [r12 + 16]")
	w("\tcall %s", fnLabel("__alloc"))
	w("\tadd rax, 16")
	w("\tmov dword ptr [rax - 12], r12d")
	w("\tmov dword ptr [rax - 8], 1")
	w("\tmov dword ptr [rax - 4], r12d")
	w("\tjmp .Lbuftake_bytes_alloc_done")
	w(".Lbuftake_bytes_alloc_special:")
	w("\tmov rdi, r12")
	w("\tcall %s", fnLabel("__alloc_u8"))
	w(".Lbuftake_bytes_alloc_done:")
	w("\tmov r13, rax")
	w("\ttest r12, r12")
	w("\tjz .Lbuftake_bytes_done")
	w("\tmov rdi, r13")
	w("\tmov rsi, qword ptr [rbx]")
	w("\tmov rdx, r12")
	w("\tcall %s", fnLabel("__memcpy"))
	w(".Lbuftake_bytes_done:")
	w("\tmov qword ptr [rbx + 8], 0")
	w("\tmov rax, r13")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tpop rbp")
	w("\tret")
}
