package x86_64ssa

// A slice is an owned 16-byte view header, not its backing array: the full
// data pointer lives at +0 and the i32 length at +8. These offsets are the IR
// contract shared with arm64ssa. This backend's strings are single-word data
// pointers with a byte length at -4, so as_bytes needs no representation copy.
func emitStringAsBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_string_as_bytes"))
	w("\tmov esi, dword ptr [rdi - 4]")
	w("\tjmp %s", fnLabel("__slice_make"))
}

// __slice_make(data, len) allocates only the view header. Backing-data ownership
// remains with the caller. Its rc=1 and payload-size=16 fields make ordinary
// closure/box drops valid. Like the other x86 SSA helpers, allocation uses the
// existing guarded bump heap; this patch does not add reclamation.
func emitSliceMakeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__slice_make"))
	// Align the stack for the guard call inside ssaBumpAlloc. The guard
	// preserves rax and does not touch the incoming rdi/rsi on success.
	w("\tsub rsp, 8")
	ssaBumpAlloc(w, "rax", "24")
	w("\tadd rsp, 8")
	w("\tmov dword ptr [rax], 1")
	w("\tmov dword ptr [rax + 4], 16")
	w("\tmov qword ptr [rax + 8], rdi")
	w("\tmov dword ptr [rax + 16], esi")
	w("\tadd rax, 8")
	w("\tret")
}

// __slice_idx_*(view, index) returns a checked element address. The unsigned
// i32 comparison rejects negative indices as well as index >= length. Reload
// the narrowed index into a different register before the 64-bit address
// calculation, so dirty upper argument bits cannot escape the checked range.
func emitSliceIdxHelper(name string, shift int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		ok := ".Lssa_" + fnLabel(name) + "_ok"
		w("")
		w("%s:", fnLabel(name))
		w("\tmov edx, dword ptr [rdi + 8]")
		w("\tcmp esi, edx")
		w("\tjb %s", ok)
		w("\tmov edi, 134")
		w("\tmov eax, 231") // Linux exit_group
		w("\tsyscall")
		w("%s:", ok)
		w("\tmov edx, esi")
		w("\tmov rax, qword ptr [rdi]")
		w("\tlea rax, [rax + rdx*%d]", 1<<shift)
		w("\tret")
	}
}

// __slice_range(lo, hi, length) validates construction and returns hi-lo.
// Normalise the i32 arguments before unsigned comparisons, matching the
// checked-range contract even when a caller leaves nonzero upper bits.
func emitSliceRangeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__slice_range"))
	w("\tmovsxd rdi, edi")
	w("\tmovsxd rsi, esi")
	w("\tmovsxd rdx, edx")
	w("\tcmp rsi, rdx")
	w("\tja .Lssa_slice_range_trap")
	w("\tcmp rdi, rsi")
	w("\tja .Lssa_slice_range_trap")
	w("\tmov eax, esi")
	w("\tsub eax, edi")
	w("\tret")
	w(".Lssa_slice_range_trap:")
	w("\tmov edi, 134")
	w("\tmov eax, 231")
	w("\tsyscall")
}
