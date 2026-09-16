package x86_64ssa

// The global string builder behind the strbuf_reset / strbuf_append /
// strbuf_take builtins, which the self-hosted compiler emits its output
// through. Its state is one .bss control block, strbufCtlSym: the buffer's
// data pointer at +0, the bytes written at +8 and the buffer's capacity at
// +16. The buffer is an __alloc that doubles when an append would overflow
// it, with the outgrown block freed, so the builder grows to whatever the
// program appends rather than trapping at a fixed size, as the flat
// backends' builders do. A take copies the bytes into a right-sized string
// and keeps the buffer for the next build.
const (
	strbufCtlSym = "__ssa_strbuf"
	strbufMinCap = 4096
)

// usesStrbuf reports whether the module references any strbuf builtin, so the
// control block is emitted only when needed.
func usesStrbuf(helpers []string) bool {
	return referencesHelper(helpers, "strbuf_reset") || referencesHelper(helpers, "strbuf_append") ||
		referencesHelper(helpers, "strbuf_take")
}

// emitStrbufBss writes the control block: data, len, cap, all zero.
func emitStrbufBss(w func(string, ...any)) {
	w(".section .bss")
	w(".align 8")
	w("%s:", strbufCtlSym)
	w("\t.quad 0")
	w("\t.quad 0")
	w("\t.quad 0")
}

// emitStrbufResetHelper writes strbuf_reset(): the builder starts over at the
// buffer it has. Unused return is 0. Leaf.
func emitStrbufResetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("strbuf_reset"))
	w("\tmov qword ptr [rip + %s + 8], 0", strbufCtlSym)
	w("\txor eax, eax")
	w("\tret")
}

// emitStrbufAppendHelper writes strbuf_append(s): copy the string's bytes
// (length at [s-4]) past the builder's tail and count them. When they would
// not fit, the buffer first moves to a block of at least twice its capacity,
// the appended length and strbufMinCap, whichever is largest, and the old
// block goes back to the freelist. rdi=s; unused return is 0.
func emitStrbufAppendHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("strbuf_append"))
	w("\tmov edx, %s", memRef("rdi", -4)) // n
	w("\tlea r8, [rip + %s]", strbufCtlSym)
	w("\tmov rax, [r8 + 8]") // len
	w("\tlea rcx, [rax + rdx]")
	w("\tcmp rcx, [r8 + 16]")
	w("\tja .Lssa_sba_grow")
	w(".Lssa_sba_copy:")
	w("\tmov rsi, rdi")      // src = s
	w("\tmov rdi, [r8]")     // data
	w("\tadd rdi, rax")      // + len
	w("\tmov [r8 + 8], rcx") // len += n
	w("\tcall %s", bcopySym)
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_sba_grow:")
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")     // three pushes past the return address: 16-aligned for the calls
	w("\tmov rbx, rdi") // s
	w("\tmov r12, rdx") // n
	w("\tmov r13, [r8 + 16]")
	w("\tadd r13, r13") // twice the capacity
	w("\tcmp r13, rcx")
	w("\tcmovb r13, rcx") // at least the grown length
	w("\tmov r9, %d", strbufMinCap)
	w("\tcmp r13, r9")
	w("\tcmovb r13, r9")
	ssaBumpAlloc(w, "r9", "r13") // the new buffer; r8 survives
	w("\tmov rdi, r9")
	w("\tmov rsi, [r8]")
	w("\tmov rdx, [r8 + 8]")
	w("\tcall %s", bcopySym) // the bytes so far
	w("\tmov rdi, [r8]")     // the outgrown block, if there was one
	w("\tmov rsi, [r8 + 16]")
	w("\tmov [r8], r9")
	w("\tmov [r8 + 16], r13")
	w("\ttest rsi, rsi")
	w("\tjz .Lssa_sba_grown")
	w("\tcall %s", fnLabel("__free"))
	w(".Lssa_sba_grown:")
	w("\tlea r8, [rip + %s]", strbufCtlSym)
	w("\tmov rdi, rbx")
	w("\tmov rdx, r12")
	w("\tmov rax, [r8 + 8]")
	w("\tlea rcx, [rax + rdx]")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_sba_copy")
}

// emitStrbufTakeHelper writes strbuf_take() -> string: a fresh single-word
// rc string (rc=1@base, len@base+4, data@base+8) holding the builder's
// bytes, which it then counts from zero again. Returns rax=data.
func emitStrbufTakeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("strbuf_take"))
	w("\tlea r8, [rip + %s]", strbufCtlSym)
	w("\tmov r9, [r8 + 8]")  // len
	w("\tlea r10, [r9 + 8]") // plus the header
	w("\tsub rsp, 8")        // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "rax", "r10")
	w("\tadd rsp, 8")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov [rax + 4], r9d")     // len
	w("\tlea r10, [rax + 8]")     // data
	w("\tmov rdi, r10")
	w("\tmov rsi, [r8]")
	w("\tmov rdx, r9")
	w("\tcall %s", bcopySym)
	w("\tmov qword ptr [r8 + 8], 0") // len = 0
	w("\tmov rax, r10")
	w("\tret")
}
