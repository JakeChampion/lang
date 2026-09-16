package x86_64ssa

import "fmt"

// The allocation fast paths. __alloc and __free are reached through
// __ssa_alloc_pres, a trampoline that saves nine registers and the flags
// around every allocation, and that is the whole cost of a program that
// builds and drops small boxes: examples/bench/enum_match.fern ran 3.3x the
// flat build's time on 1.34x its instructions, with no page faults and fewer
// mispredicted branches. A size known at compile time names its class at
// compile time too, so a MemAlloc of a constant pops its class's list inline
// and takes the trampoline only when the list is empty, and a
// __fern_box_free of a constant size pushes inline. Both read the same class
// function __alloc and __free compute at run time (emitFreelistClass), which
// TestInlineAllocationAgreesWithTheHelpersOnEveryClass pins.

// smallClassIndex is the freelist index of an n-byte request in the exact
// 16-byte tier, or false above it: the large tier's 3-significant-bit
// rounding stays with the helpers.
func smallClassIndex(n int64) (int, bool) {
	if n < 0 || n > 2048 {
		return 0, false
	}
	c := (n + 15) &^ 15
	if c < 16 {
		c = 16
	}
	return int(c/16 - 1), true
}

// boxFreeInline reports whether a call is a __fern_box_free whose size the
// emitter knew, in the tier the inline push covers.
func boxFreeInline(in Inst) bool {
	if in.Op != Call || in.Callee != "__fern_box_free" || !in.SrcImm || len(in.ArgLocs) != 2 {
		return false
	}
	_, ok := smallClassIndex(in.Imm + 8)
	return ok
}

// inlineAllocLines renders a MemAlloc of a constant small-tier size as an
// inline pop, with the trampoline as the slow path, or a __fern_box_free of a
// constant size as an inline push; it reports false for anything else. r11
// is the per-instruction scratch the trampoline already uses; s0 homes a
// slot-resident box and s1 the old list head.
func inlineAllocLines(in Inst, numAlloc int, seed string) ([]string, bool) {
	lbl := func(suffix string) string { return fmt.Sprintf(".Lssa_alloc_%s_%s", seed, suffix) }
	if in.Op == MemAlloc && in.SrcImm {
		idx, ok := smallClassIndex(in.Imm)
		if !ok {
			return nil, false
		}
		head := fmt.Sprintf("[rip + %s + %d]", freelistSym, 8*idx)
		dst := reg(in.Dst)
		return []string{
			fmt.Sprintf("mov %s, %s", dst, head),
			fmt.Sprintf("test %s, %s", dst, dst),
			fmt.Sprintf("jz %s", lbl("bump")),
			fmt.Sprintf("mov r11, [%s]", dst), // the block's successor
			fmt.Sprintf("mov %s, r11", head),
			fmt.Sprintf("jmp %s", lbl("done")),
			lbl("bump") + ":",
			fmt.Sprintf("mov r11, %d", in.Imm),
			"call " + allocPresSym,
			fmt.Sprintf("mov %s, r11", dst),
			lbl("done") + ":",
		}, true
	}
	if !boxFreeInline(in) {
		return nil, false
	}
	idx, _ := smallClassIndex(in.Imm + 8) // the payload plus its rc header
	head := fmt.Sprintf("[rip + %s + %d]", freelistSym, 8*idx)
	s0, s1 := numAlloc, numAlloc+1
	var out []string
	data := in.ArgLocs[0].Reg
	if !in.ArgLocs[0].IsReg {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(s0), slotMem(in.ArgLocs[0].Slot)))
		data = s0
	}
	out = append(out,
		fmt.Sprintf("cmp %s, 0x10000", reg(data)),
		fmt.Sprintf("jb %s", lbl("skip")),
		fmt.Sprintf("lea r11, [%s - 8]", reg(data)), // the block base
		fmt.Sprintf("mov %s, %s", reg(s1), head),
		fmt.Sprintf("mov [r11], %s", reg(s1)), // base.next = old head
		fmt.Sprintf("mov %s, r11", head),
		lbl("skip")+":",
	)
	if in.Dst != data {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(data)))
	}
	return out, true
}
