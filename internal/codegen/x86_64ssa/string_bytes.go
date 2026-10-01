package x86_64ssa

// SSA strings are addressable, with their byte length at data - 4.
func emitStringBytesCopyHelper(w func(string, ...any)) {
	w("%s:", fnLabel("__fern_string_bytes_copy"))
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	w("\tsub rsp, 16")
	w("\tmov [rbp - 8], rdi")
	w("\tmov edi, dword ptr [rdi - 4]")
	w("\tmov [rbp - 16], rdi")
	w("\tcall %s", fnLabel("__alloc_u8"))
	w("\tmov rsi, [rbp - 8]")
	w("\tmov rcx, [rbp - 16]")
	emitBcopyCall(w, "rax", "rsi", "rcx")
	w("\tadd rsp, 16")
	w("\tpop rbp")
	w("\tret")
}
