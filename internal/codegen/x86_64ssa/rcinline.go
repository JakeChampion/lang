package x86_64ssa

import "fmt"

// rcInline names the rc primitives rendered at their call sites rather than
// called: a guard chain of five or six instructions each, reached from the
// hot loop of anything built on a persistent collection (70 to 200 sites per
// such program, one per node touched). The call was never the cost; it is the
// caller-saves the allocator plants around a call whose callee it knows
// nothing about, and the call and return around six instructions. The flat
// backends inline all three.
//
// The helper bodies stay for the helpers that call them (__fern_closure_drop,
// the element-retaining array copies) and for arm64ssa, whose renderer still
// calls them; a module whose only readers are compiled code carries no body.
var rcInline = map[string]bool{
	"__fern_rc_is_unique": true,
	"__fern_rc_inc":       true,
	"__fern_rc_dec":       true,
}

// rcInlineCall reports whether a call is an rc primitive the renderer writes
// inline. inlinedCall and inlineRcLines both read it, so the shape conditions
// live in one place (#9618).
func rcInlineCall(in Inst) bool {
	return in.Op == Call && rcInline[in.Callee] && len(in.ArgLocs) == 1
}

// inlineRcLines renders an rc primitive inline, or reports false when the
// callee is something else. Each reproduces its helper exactly:
//
//   - is_unique: 0 below the heap floor or under a static sentinel (whose
//     top bit makes the rc word negative, so it never compares equal to 1),
//     else rc == 1.
//   - inc / dec: nothing below the floor or under a sentinel; a release of an
//     already-zero count is an over-release, counted and left alone rather
//     than wrapped into the sentinel. Both hand the pointer back.
//
// seed keeps the labels apart across sites. s0 homes a slot-resident operand
// and s1 the 0/1, so neither collides with a destination that aliases the
// operand.
func inlineRcLines(in Inst, numAlloc int, seed string) ([]string, bool) {
	if !rcInlineCall(in) {
		return nil, false
	}
	s0, s1 := numAlloc, numAlloc+1
	var out []string
	ptr := in.ArgLocs[0].Reg
	if !in.ArgLocs[0].IsReg {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(s0), slotMem(in.ArgLocs[0].Slot)))
		ptr = s0
	}
	lbl := func(suffix string) string { return fmt.Sprintf(".Lssa_rc_%s_%s", seed, suffix) }
	rc := memRef(reg(ptr), -8)
	switch in.Callee {
	case "__fern_rc_is_unique":
		out = append(out,
			fmt.Sprintf("xor %s, %s", reg32n(s1), reg32n(s1)),
			fmt.Sprintf("cmp %s, 0x10000", reg(ptr)),
			fmt.Sprintf("jb %s", lbl("done")),
			fmt.Sprintf("cmp dword ptr %s, 1", rc),
			fmt.Sprintf("sete %s", reg8n(s1)),
			lbl("done")+":",
			fmt.Sprintf("movzx %s, %s", reg32n(in.Dst), reg8n(s1)), // zero-extends: no width fixup
		)
		return out, true
	case "__fern_rc_inc":
		out = append(out,
			fmt.Sprintf("cmp %s, 0x10000", reg(ptr)),
			fmt.Sprintf("jb %s", lbl("skip")),
			fmt.Sprintf("cmp dword ptr %s, 0", rc),
			fmt.Sprintf("jl %s", lbl("skip")), // negative: a static sentinel
			fmt.Sprintf("add dword ptr %s, 1", rc),
			lbl("skip")+":",
		)
	case "__fern_rc_dec":
		out = append(out,
			fmt.Sprintf("cmp %s, 0x10000", reg(ptr)),
			fmt.Sprintf("jb %s", lbl("skip")),
			fmt.Sprintf("cmp dword ptr %s, 0", rc),
			fmt.Sprintf("jl %s", lbl("skip")),                          // negative: a static sentinel
			fmt.Sprintf("jne %s", lbl("sub")),                          // positive: the release
			fmt.Sprintf("add dword ptr [rip + %s], 1", rcUnderflowSym), // zero: an over-release
			fmt.Sprintf("jmp %s", lbl("skip")),
			lbl("sub")+":",
			fmt.Sprintf("sub dword ptr %s, 1", rc),
			lbl("skip")+":",
		)
	}
	if in.Dst != ptr {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(ptr)))
	}
	return out, true
}
