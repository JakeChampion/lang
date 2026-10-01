package arm64ssa

// Packed array data can use the existing range-copy kernel after clamping.
func emitBufPushBytesRangeHelper(w func(string, ...any)) {
	w("%s:", fnLabel("buf_push_bytes_range"))
	w("\tcmp w2, #0")
	w("\tcsel w2, w2, wzr, ge")
	w("\tldur w9, [x1, #-4]")
	w("\tcmp w3, w9")
	w("\tcsel w3, w3, w9, le")
	w("\tb %s", fnLabel("buf_push_range"))
}

// Copy builder bytes into an independently owned array and reset its length.
func emitBufTakeBytesHelper(w func(string, ...any)) {
	w("%s:", fnLabel("buf_take_bytes"))
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #-16]!")
	w("\tstp x21, x22, [sp, #-16]!")
	w("\tmov x19, x0")
	w("\tldr x20, [x19, #8]")
	// Copy initializes all payload bytes; retain the normal array header and
	// __alloc_u8's empty/invalid-length behavior without redundant zero fill.
	w("\tcmp w20, #0")
	w("\tb.le .Lbuftake_bytes_alloc_special")
	w("\tadd w16, w20, #16")
	emitAllocPresCall(w)
	w("\tadd x0, x16, #16")
	w("\tstur w20, [x0, #-12]")
	w("\tmov w1, #1")
	w("\tstur w1, [x0, #-8]")
	w("\tstur w20, [x0, #-4]")
	w("\tb .Lbuftake_bytes_alloc_done")
	w(".Lbuftake_bytes_alloc_special:")
	w("\tmov x0, x20")
	w("\tbl %s", fnLabel("__alloc_u8"))
	w(".Lbuftake_bytes_alloc_done:")
	w("\tmov x21, x0")
	w("\tcbz x20, .Lbuftake_bytes_done")
	w("\tldr x1, [x19]")
	w("\tmov x2, x20")
	w("\tbl %s", fnLabel("__memcpy"))
	w(".Lbuftake_bytes_done:")
	w("\tstr xzr, [x19, #8]")
	w("\tmov x0, x21")
	w("\tldp x21, x22, [sp], #16")
	w("\tldp x19, x20, [sp], #16")
	w("\tldp x29, x30, [sp], #16")
	w("\tret")
}
