package x86_64ssa

// The runtime names core/map.fern bottoms out in. The Map itself is Fern
// (map_new_impl and the __map_*_impl functions), lifted like any other module
// function; a call site names the natural form (map_new, __method_Map_set)
// and ir.CodegenAlias resolves it at the label. What the Fern needs from the
// backend is an allocator pair, a byte fill, the per-process hash seed, and
// the drops for a handle and for a pointer-element value column.

// mapSeedSym is the .bss word core/map's string-hash seed is cached in.
const mapSeedSym = "__ssa_map_seed"

// emitMemsetHelper writes __memset(dst, byte, n): n copies of the low byte of
// `byte` at dst. core/map fills a fresh control-byte array with its empty
// marker through it. __ssa_bfill takes dst in rdi as it arrives.
func emitMemsetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__memset"))
	w("\tmov eax, esi")
	w("\tmov ecx, edx")
	w("\tsub rsp, 8")
	w("\tcall %s", bfillSym)
	w("\tadd rsp, 8")
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
// counter an rc_dec bumps (countsUnderflow: the module names it, inline or
// in a helper body).
func emitHelperBss(w func(string, ...any), helpers []string, countsUnderflow bool) {
	if referencesHelper(helpers, "__fern_map_hash_seed") {
		w(".section .bss")
		w(".align 8")
		w("%s:", mapSeedSym)
		w("\t.quad 0")
	}
	if usesStrbuf(helpers) {
		emitStrbufBss(w)
	}
	if referencesHelper(helpers, "__method_Reader_read_line") || referencesHelper(helpers, "read_line") {
		w(".section .bss")
		w(".align 8")
		w("%s:", readlineBufSym)
		w("\t.space %d", readlineBytes)
	}
	if countsUnderflow || referencesHelper(helpers, "__fern_rc_underflow_count") {
		w(".section .bss")
		w(".align 8")
		w("%s:", rcUnderflowSym)
		w("\t.quad 0")
	}
	if countsAllocs(helpers) {
		w(".section .bss")
		w(".align 8")
		w("%s:", allocCountSym)
		w("\t.quad 0")
	}
}
