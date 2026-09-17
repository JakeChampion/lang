package x86_64ssa

import (
	"strconv"
	"strings"
)

// The FERN_LEAKCHECK census, the x86_64ssa sibling of arm64ssa's and of the
// flat backend's __fern_lc_report, printing the same one line on stderr:
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// Kept identical to the other two on purpose: the point of having it here is
// that the two SSA backends can be measured with ONE instrument, and a line
// that differed by a field would put them back to being incomparable.
//
// live_bytes is bumped + popped - freed. Bumped is the 16-rounded cursor less
// the arena base: a bump site rounds the cursor to 16 before adding its size,
// so that difference is exactly the sum of the rounded sizes __free counts
// with, and only a pop leaves the cursor alone. K is signed, so an over-free
// reads negative rather than wrapping.
const (
	lcAllocCountSym = "__ssa_lc_alloc_count"
	lcPopBytesSym   = "__ssa_lc_pop_bytes"
	lcFreeCountSym  = "__ssa_lc_free_count"
	lcFreeBytesSym  = "__ssa_lc_free_bytes"
	lcReportSym     = "__ssa_lc_report"

	lcMsgAllocs = "leakcheck: allocs="
	lcMsgFrees  = " frees="
	lcMsgLive   = " live_bytes="
)

// emitLcAdd adds one, or `addend`, to the counter at `sym`. It is a
// read-modify-write on memory, so it CLOBBERS FLAGS: every site either has
// dead flags or brackets itself with pushfq, as the heap guard does.
func emitLcAdd(w func(string, ...any), sym, addend string) {
	if addend == "" {
		w("\tadd qword ptr [rip + %s], 1", sym)
		return
	}
	w("\tadd qword ptr [rip + %s], %s", sym, addend)
}

// emitLcBss writes the census counters. Emitted whatever the module
// allocates, so a no-heap program still reports its (zero) line rather than
// nothing.
func emitLcBss(w func(string, ...any)) {
	w(".section .bss")
	w(".align 8")
	for _, sym := range []string{lcAllocCountSym, lcPopBytesSym, lcFreeCountSym, lcFreeBytesSym} {
		w("%s:", sym)
		w("\t.quad 0")
	}
}

// emitLcReport writes __ssa_lc_report(), called from the two paths a program
// leaves by: _start's epilogue and the exit() builtin, which bypasses it.
// Both park the exit status across the call. This clobbers rax, rcx, rdx,
// rsi, rdi and r8..r10, and needs no stack alignment — it reaches the kernel
// directly and touches no SSE register.
//
// `heap` is false for a module that never allocates: then there is no arena
// to read and every number is zero.
func emitLcReport(w func(string, ...any), heap bool) {
	msg := func(sym, text string) {
		w(".section .rodata")
		w("%s:", sym)
		bytes := make([]string, len(text))
		for i := 0; i < len(text); i++ {
			bytes[i] = strconv.Itoa(int(text[i]))
		}
		w("\t.byte %s", strings.Join(bytes, ", "))
		w(".text")
	}
	write := func(sym, text string) {
		w("\tlea rsi, [rip + %s]", sym)
		w("\tmov edx, %d", len(text))
		w("\tcall .Lssa_lc_write")
	}
	num := func(sym string) {
		w("\tmov rdi, [rip + %s]", sym)
		w("\tcall .Lssa_lc_wrnum")
	}
	w("")
	w("%s:", lcReportSym)
	write("__ssa_lc_msg_allocs", lcMsgAllocs)
	num(lcAllocCountSym)
	write("__ssa_lc_msg_frees", lcMsgFrees)
	num(lcFreeCountSym)
	write("__ssa_lc_msg_live", lcMsgLive)
	if heap {
		w("\tmov rax, [rip + %s]", heapPtrSym)
		w("\tadd rax, 15")
		w("\tand rax, -16")
		w("\tsub rax, [rip + %s]", heapBaseSym)
	} else {
		w("\txor eax, eax")
	}
	w("\tadd rax, [rip + %s]", lcPopBytesSym)
	w("\tsub rax, [rip + %s]", lcFreeBytesSym)
	w("\tmov rdi, rax")
	w("\tcall .Lssa_lc_wrnum")
	write("__ssa_lc_msg_nl", "\n")
	w("\tret")
	// .Lssa_lc_write(rsi = buf, rdx = len): one write(2) to stderr. Leaf, so
	// the return into the report survives on the stack.
	w(".Lssa_lc_write:")
	w("\tmov edi, 2") // stderr
	w("\tmov eax, 1") // write
	w("\tsyscall")
	w("\tret")
	// .Lssa_lc_wrnum(rdi = signed i64): decimal itoa, digits built backwards
	// from the end of a 32-byte stack buffer (an i64 is 19 digits plus a sign
	// at most), then one write(2) to stderr. Leaf.
	w(".Lssa_lc_wrnum:")
	w("\tsub rsp, 48")
	w("\tlea rcx, [rsp + 32]") // one past the last digit
	w("\tmov r8, rcx")         // the end, for the length
	w("\tmov rax, rdi")
	w("\txor r9d, r9d") // sign flag
	w("\ttest rax, rax")
	w("\tjns .Lssa_lc_wrnum_abs")
	w("\tneg rax")
	w("\tmov r9d, 1")
	w(".Lssa_lc_wrnum_abs:")
	w("\tmov r10, 10")
	w(".Lssa_lc_wrnum_loop:")
	w("\txor edx, edx")
	w("\tdiv r10")    // rdx:rax / 10 → rax quotient, rdx remainder
	w("\tadd dl, 48") // → ASCII
	w("\tsub rcx, 1")
	w("\tmov [rcx], dl")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_lc_wrnum_loop")
	w("\ttest r9d, r9d")
	w("\tjz .Lssa_lc_wrnum_emit")
	w("\tsub rcx, 1")
	w("\tmov byte ptr [rcx], 45") // '-'
	w(".Lssa_lc_wrnum_emit:")
	w("\tmov rsi, rcx")
	w("\tmov rdx, r8")
	w("\tsub rdx, rcx") // len
	w("\tmov edi, 2")   // stderr
	w("\tmov eax, 1")   // write
	w("\tsyscall")
	w("\tadd rsp, 48")
	w("\tret")
	msg("__ssa_lc_msg_allocs", lcMsgAllocs)
	msg("__ssa_lc_msg_frees", lcMsgFrees)
	msg("__ssa_lc_msg_live", lcMsgLive)
	msg("__ssa_lc_msg_nl", "\n")
}
