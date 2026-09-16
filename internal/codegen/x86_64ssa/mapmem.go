package x86_64ssa

// The runtime names core/map.fern bottoms out in. The Map itself is Fern
// (map_new_impl and the __map_*_impl functions), lifted like any other module
// function; a call site names the natural form (map_new, __method_Map_set)
// and ir.CodegenAlias resolves it at the label. What the Fern needs from the
// backend is an allocator pair, a byte fill, the per-process hash seed, and
// the drops for a handle and for a pointer-element value column.

// mapSeedSym is the .bss word core/map's string-hash seed is cached in.
const mapSeedSym = "__ssa_map_seed"

// emitAllocHelper writes __alloc(n) -> ptr: a raw 16-aligned block of n bytes
// with no header of its own; callers lay down rc / cap / len words themselves.
// The bump sequence every inline allocation uses, behind a label, with one
// slot of padding so the guard is called 16-aligned. n is a non-negative i32.
func emitAllocHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc"))
	w("\tmov edi, edi")
	w("\tsub rsp, 8")
	ssaBumpAlloc(w, "rax", "rdi")
	w("\tadd rsp, 8")
	w("\tret")
}

// emitFreeHelper writes __free(base, n): nothing. This heap is a bump cursor
// with no reclamation (see __fern_box_free), so a released block is left
// behind — a leak, not a miscompile, and the same trade every drop here makes.
func emitFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__free"))
	w("\tret")
}

// emitMemsetHelper writes __memset(dst, byte, n): n copies of the low byte of
// `byte` at dst. core/map fills a fresh control-byte array with its empty
// marker through it. rep stosb takes dst in rdi as it arrives. Leaf.
func emitMemsetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__memset"))
	w("\tmov eax, esi")
	w("\tmov ecx, edx")
	w("\tcld")
	w("\trep stosb")
	w("\tret")
}

// emitMapHashSeedHelper writes __fern_map_hash_seed() -> i32: core/map's
// per-process string-hash seed, mixed into its FNV basis so a set of key
// strings cannot be precomputed to collide (#6194). Drawn once through
// random_i32 and cached in mapSeedSym; the word doubles as the drawn flag, so
// the value is forced nonzero, which is also what keeps a map header from
// reading as unseeded.
func emitMapHashSeedHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_map_hash_seed"))
	w("\tmov eax, [rip + %s]", mapSeedSym)
	w("\ttest eax, eax")
	w("\tjnz .Lssa_mapseed_ret")
	w("\tsub rsp, 8")
	w("\tcall %s", fnLabel("random_i32"))
	w("\tadd rsp, 8")
	w("\tor eax, 1")
	w("\tmov [rip + %s], eax", mapSeedSym)
	w(".Lssa_mapseed_ret:")
	w("\tret")
}

// emitHelperBss writes the .bss words and buffers the referenced helpers
// read: the map seed, Reader.read_line's line buffer, and the over-release
// counter __fern_rc_dec bumps.
func emitHelperBss(w func(string, ...any), helpers []string) {
	if referencesHelper(helpers, "__fern_map_hash_seed") {
		w(".section .bss")
		w(".align 8")
		w("%s:", mapSeedSym)
		w("\t.quad 0")
	}
	if referencesHelper(helpers, "__method_Reader_read_line") {
		w(".section .bss")
		w(".align 8")
		w("%s:", readlineBufSym)
		w("\t.space %d", readlineBytes)
	}
	if referencesHelper(helpers, "__fern_rc_dec") || referencesHelper(helpers, "__fern_rc_underflow_count") {
		w(".section .bss")
		w(".align 8")
		w("%s:", rcUnderflowSym)
		w("\t.quad 0")
	}
}
