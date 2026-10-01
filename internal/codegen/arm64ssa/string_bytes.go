package arm64ssa

// SSA strings are addressable, with their byte length at data - 4.
func emitStringBytesCopyHelper(w func(string, ...any)) {
	w("%s:", fnLabel("__fern_string_bytes_copy"))
	w("\tstp x29, x30, [sp, #-48]!")
	w("\tmov x29, sp")
	w("\tstr x0, [sp, #16]")
	w("\tldur w0, [x0, #-4]")
	w("\tstr x0, [sp, #24]")
	w("\tbl %s", fnLabel("__alloc_u8"))
	w("\tstr x0, [sp, #32]")
	w("\tldr x1, [sp, #16]")
	w("\tldr x2, [sp, #24]")
	emitBcopyCall(w, "x0", "x1", "x2")
	w("\tldr x0, [sp, #32]")
	w("\tldp x29, x30, [sp], #48")
	w("\tret")
}
