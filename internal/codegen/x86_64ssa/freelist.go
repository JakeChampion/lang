package x86_64ssa

import "github.com/jakechampion/lang/internal/ast"

// The size-class freelist, the same scheme the flat backend and arm64ssa use,
// so the three agree on every block: exact 16-byte classes up to 2048 bytes,
// 3-significant-bit capacities above, bump-only past 1 GiB. Every block that
// can be released comes out of __alloc, which rounds the request to its
// class, so a block's physical extent always covers the class __free later
// pushes it on. Compiled code and the helpers reach __alloc through
// __ssa_alloc_pres, a trampoline that preserves every other register, so an
// allocation can be spliced anywhere without knowing what is live around it:
// the size goes in r11 and the block base comes back in it. The flags are not
// kept: no site reads them across an allocation, and pushfq/popfq cost more
// than the pushes.
// r11 is never a home for a value — it is a per-instruction scratch at the
// largest file and outside the file below it.

const (
	freelistSym     = "__ssa_freelist_heads"
	allocPresSym    = "__ssa_alloc_pres"
	freelistClasses = 256
)

// emitFreelistClass writes the size-class computation __alloc and __free
// share: sizeReg holds the request and comes out as the bytes the class spans
// (the 16-rounded size, at least 16; in the large tier the 3-significant-bit
// capacity); idxReg comes out as the class index. A request past 1 GiB
// branches to noneLabel with sizeReg rounded. rcx and r8 are scratch, so
// neither operand may be one of them. Labels are namespaced by tag.
func emitFreelistClass(w func(string, ...any), tag, sizeReg, idxReg, noneLabel string) {
	lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
	w("\tadd %s, 15", sizeReg)
	w("\tand %s, -16", sizeReg)
	w("\tcmp %s, 16", sizeReg)
	w("\tjae %s", lbl("min"))
	w("\tmov %s, 16", sizeReg) // a zero-byte request still owns a block
	w("%s:", lbl("min"))
	w("\tcmp %s, 2048", sizeReg)
	w("\tja %s", lbl("large"))
	w("\tmov %s, %s", idxReg, sizeReg)
	w("\tshr %s, 4", idxReg)
	w("\tsub %s, 1", idxReg) // small class index 0..127
	w("\tjmp %s", lbl("classed"))
	w("%s:", lbl("large"))
	w("\tmov rcx, 1073741824") // 1 GiB
	w("\tcmp %s, rcx", sizeReg)
	w("\tja %s", noneLabel)
	// Round up to 3 significant bits: gran = 1 << (bsr(size) - 2).
	w("\tbsr rcx, %s", sizeReg)
	w("\tsub ecx, 2")
	w("\tmov r8d, 1")
	w("\tshl r8, cl") // gran
	w("\tlea %s, [%s + r8 - 1]", idxReg, sizeReg)
	w("\tneg r8")
	w("\tand %s, r8", idxReg)
	w("\tmov %s, %s", sizeReg, idxReg) // cap = roundup(size, gran)
	// class = (e2-11)*4 + mant + 124, e2 = bsr(cap), mant = cap >> (e2-2).
	w("\tbsr rcx, %s", sizeReg)
	w("\tsub ecx, 2")
	w("\tshr %s, cl", idxReg)
	w("\tsub ecx, 9")
	w("\tshl ecx, 2")
	w("\tadd %s, rcx", idxReg)
	w("\tadd %s, 124", idxReg)
	w("%s:", lbl("classed"))
}

// emitAllocHelper writes __alloc(n) -> ptr, the one allocator behind every
// block this backend hands out: a raw 16-aligned block of at least n bytes
// with no header of its own (callers lay down rc / cap / len words
// themselves). It pops the block's size class off the freelist when one is
// waiting and otherwise bumps the cursor by the class's rounded size. Popped
// memory is NOT zeroed, the same contract as the flat __fern_alloc. n is a
// non-negative i32.
func emitAllocHelper(w func(string, ...any)) { emitAllocHelperCensus(w, false) }

// emitAllocHelperCounting is the same allocator with the census tick a module
// that reads __heap_alloc_count() (#9596) needs: one increment at the entry,
// which is ahead of the freelist-pop return, so a recycled block is counted
// exactly like a bumped one — the census is of blocks handed out, not of
// memory bought. It is substituted for the plain body by emitRuntimeHelpers.
func emitAllocHelperCounting(w func(string, ...any)) { emitAllocHelperCensus(w, true) }

func emitAllocHelperCensus(w func(string, ...any), census bool) {
	w("")
	w("%s:", fnLabel("__alloc"))
	if census {
		w("\tadd qword ptr [rip + %s], 1", allocCountSym)
	}
	w("\tmov edi, edi")
	emitFreelistClass(w, "alloc", "rdi", "rsi", ".Lssa_alloc_bump")
	w("\tlea r8, [rip + %s]", freelistSym)
	w("\tmov rax, [r8 + rsi * 8]") // head
	w("\ttest rax, rax")
	w("\tjz .Lssa_alloc_bump")
	w("\tmov rcx, [rax]")          // head.next
	w("\tmov [r8 + rsi * 8], rcx") // heads[idx] = next
	if ast.LeakCheckEnabled {
		// The one allocation shape that leaves the cursor alone, so the guard
		// cannot see it. rdi still holds the class-rounded size __free
		// counted this block with. Flags are dead before the ret.
		emitLcAdd(w, lcAllocCountSym, "")
		emitLcAdd(w, lcPopBytesSym, "rdi")
	}
	w("\tret")
	w(".Lssa_alloc_bump:")
	w("\tsub rsp, 8") // entered 8 past alignment; the guard is called at 16
	ssaInlineBump(w, "rax", "rdi")
	w("\tadd rsp, 8")
	w("\tret")
}

// emitFreeHelper writes __free(base, n): push the n-byte block at base onto
// its size class's intrusive freelist (successor pointer in the block's first
// 8 bytes), for __alloc to hand out again. A block past 1 GiB was never
// classed and is dropped. rdi is left as it came, which __fern_box_free
// relies on. Leaf.
func emitFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__free"))
	w("\tmov esi, esi")
	emitFreelistClass(w, "free", "rsi", "rdx", ".Lssa_free_ret")
	if ast.LeakCheckEnabled {
		// After the class computation, so the census counts only what is
		// really pushed: a block above the largest class returns unreclaimed
		// and its bytes stay live for the rest of the process.
		emitLcAdd(w, lcFreeCountSym, "")
		emitLcAdd(w, lcFreeBytesSym, "rsi")
	}
	w("\tlea r8, [rip + %s]", freelistSym)
	w("\tmov rax, [r8 + rdx * 8]") // old head
	w("\tmov [rdi], rax")          // base.next = old head
	w("\tmov [r8 + rdx * 8], rdi") // heads[idx] = base
	w(".Lssa_free_ret:")
	w("\tret")
}

// emitAllocPres writes __ssa_alloc_pres: size in r11, block base back in r11,
// every other caller-saved register exactly as it was; the flags are
// clobbered. The callee-saved ones __alloc preserves by convention. Eight
// pushes past the return address leave rsp 8 past alignment, so one more
// word pads it to 16 for the call, given a site that calls at 16.
func emitAllocPres(w func(string, ...any)) {
	w("")
	w("%s:", allocPresSym)
	for _, r := range []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10"} {
		w("\tpush %s", r)
	}
	w("\tsub rsp, 8")
	w("\tmov rdi, r11")
	w("\tcall %s", fnLabel("__alloc"))
	w("\tmov r11, rax")
	w("\tadd rsp, 8")
	for _, r := range []string{"r10", "r9", "r8", "rdi", "rsi", "rdx", "rcx", "rax"} {
		w("\tpop %s", r)
	}
	w("\tret")
}

// allocPresLines is the inline splice of an allocation through the
// trampoline: `size` (a register or an immediate) in, the 16-aligned block
// base in dst out, nothing else touched but r11.
func allocPresLines(dst, size string) []string {
	var out []string
	if size != "r11" {
		out = append(out, "mov r11, "+size)
	}
	out = append(out, "call "+allocPresSym)
	if dst != "r11" {
		out = append(out, "mov "+dst+", r11")
	}
	return out
}

// ssaBumpAlloc allocates `size` bytes (an immediate or a register) through
// __ssa_alloc_pres and leaves the block's 16-aligned base in dst. Every
// register but r11 and dst survives; the flags do not. The site must call at
// a 16-aligned rsp.
func ssaBumpAlloc(w func(string, ...any), dst, size string) {
	for _, l := range allocPresLines(dst, size) {
		w("\t%s", l)
	}
}

// ssaInlineBump is the raw cursor bump __alloc itself takes and the one site
// that rewinds the cursor afterwards (Reader.read_chunk) has to take: dst =
// the 16-aligned cursor, the cursor moves past `size` bytes, and the guard
// checks the reservation. A block from here is never pushed on a freelist,
// so nothing depends on its class. __ssa_heap_guard preserves every register
// and the flags; r11 is scratch.
func ssaInlineBump(w func(string, ...any), dst, size string) {
	w("\tmov %s, [rip + %s]", dst, heapPtrSym)
	w("\tadd %s, 15", dst)
	w("\tand %s, -16", dst)
	w("\tmov r11, %s", dst)
	w("\tadd r11, %s", size)
	w("\tmov [rip + %s], r11", heapPtrSym)
	w("\t%s", heapGuardCall)
}

// emitFreelistBss writes the class heads, zero-initialised: an empty list is a
// null head.
func emitFreelistBss(w func(string, ...any)) {
	w(".section .bss")
	w(".align 8")
	w("%s:", freelistSym)
	w("\t.space %d", 8*freelistClasses)
}

// emitBoxFreeHelper writes __fern_box_free(data, size) -> data: return an
// rc-headed block (base = data-8, size+8 bytes) to the freelist. The IR
// pre-gates the call on rc == 1 and has already dropped the box's counted
// fields, so this is only the push. Null and low addresses are skipped.
func emitBoxFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_box_free"))
	w("\tmov rax, rdi")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_boxfree_ret")
	w("\tsub rsp, 8")
	w("\tsub rdi, 8") // base
	w("\tmov esi, esi")
	w("\tadd rsi, 8") // plus the rc header
	w("\tcall %s", fnLabel("__free"))
	w("\tadd rsp, 8")
	w("\tlea rax, [rdi + 8]")
	w(".Lssa_boxfree_ret:")
	w("\tret")
}

// emitMapDropHelper writes __fern_map_drop(m) -> m: the scope-exit drop for a
// Map local. A Map handle keeps its rc at [m-8] and its kv-buffer pointer at
// [m+0]. On the last reference (rc == 1) both allocations go back: the buffer
// (ast.MapHeaderBytes+8 + cap*(4 + entryStride + 1), cap at [buf+0],
// entryStride = 16 here, the +1/+8 being core/map's ctrl bytes and their
// mirror) and then the 16-byte handle cell at m-8; a shared handle is
// decremented in place. Entry keys and values are NOT walked: the IR emits a
// __map_drop_values call ahead of this one. rbx = m across the frees.
func emitMapDropHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_map_drop"))
	w("\tpush rbx")
	w("\tmov rbx, rdi")
	w("\tcmp rbx, 0x10000")
	w("\tjb .Lssa_mapdrop_ret")
	w("\tmov eax, %s", memRef("rbx", -8)) // rc
	w("\ttest eax, eax")
	w("\tjle .Lssa_mapdrop_ret") // a static sentinel or nothing to do
	w("\tcmp eax, 1")
	w("\tjne .Lssa_mapdrop_dec")
	w("\tmov rdi, [rbx]") // buf
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_mapdrop_handle")
	w("\tmov esi, dword ptr [rdi]") // cap
	w("\timul rsi, rsi, 21")        // 4 + entryStride(16) + 1 ctrl byte
	w("\tadd rsi, %d", ast.MapHeaderBytes+8)
	w("\tcall %s", fnLabel("__free"))
	w(".Lssa_mapdrop_handle:")
	w("\tlea rdi, [rbx - 8]") // handle base
	w("\tmov esi, 16")
	w("\tcall %s", fnLabel("__free"))
	w("\tjmp .Lssa_mapdrop_ret")
	w(".Lssa_mapdrop_dec:")
	w("\tsub eax, 1")
	w("\tmov %s, eax", memRef("rbx", -8))
	w(".Lssa_mapdrop_ret:")
	w("\tmov rax, rbx")
	w("\tpop rbx")
	w("\tret")
}
