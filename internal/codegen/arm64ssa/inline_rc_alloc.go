package arm64ssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
)

// The rc primitives and the constant-size allocation fast paths, rendered at
// their sites the way the x86-64 renderer renders them (rcinline.go,
// allocinline.go there): the guard chain of each helper body, and a freelist
// pop or push whose class the emitter knew at compile time. The helper bodies
// stay for the helpers that call them and for every site the inline form does
// not cover. Under the leak census (ast.LeakCheckEnabled) the allocation fast
// paths stay calls, since __alloc and __free are where the census counts.

// rcInline names the rc primitives rendered inline.
var rcInline = map[string]bool{
	"__fern_rc_is_unique": true,
	"__fern_rc_inc":       true,
	"__fern_rc_dec":       true,
}

// inlinedCall reports whether a call is one the block renderer writes inline,
// so the module need not carry the helper body for it.
func inlinedCall(in x86.Inst) bool {
	if in.Op != x86.Call {
		return false
	}
	return rcInline[in.Callee] || boxFreeInline(in)
}

// boxFreeInline reports whether a call is a __fern_box_free whose size the
// emitter knew, in the tier the inline push covers, outside the census.
func boxFreeInline(in x86.Inst) bool {
	if ast.LeakCheckEnabled || in.Op != x86.Call || in.Callee != "__fern_box_free" || !in.SrcImm || len(in.ArgLocs) != 2 {
		return false
	}
	_, ok := x86.SmallClassIndex(in.Imm + 8)
	return ok
}

// inlineRcLines renders an rc primitive inline, or reports false when the
// callee is something else. Each reproduces its helper exactly: is_unique is 0
// below the heap floor or under a static sentinel (top bit set, so never equal
// to 1), else rc == 1; inc and dec skip the floor and the sentinel, dec counts
// a release of an already-zero count as an over-release and leaves the count
// alone, and both hand the pointer back. s0 homes a slot-resident operand, s1
// the rc word, s2 the counter's address.
func inlineRcLines(in x86.Inst, fr frameLayout, numAlloc int, seed string) ([]string, bool) {
	if !rcInline[in.Callee] || in.Op != x86.Call || len(in.ArgLocs) != 1 {
		return nil, false
	}
	s0, s1, s2 := numAlloc, numAlloc+1, numAlloc+2
	var out []string
	ptr := in.ArgLocs[0].Reg
	if !in.ArgLocs[0].IsReg {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(s0), fr.slot(in.ArgLocs[0].Slot)))
		ptr = s0
	}
	lbl := func(suffix string) string { return fmt.Sprintf(".Lssa_rc_%s_%s", seed, suffix) }
	p, rc := xreg(ptr), wreg(s1)
	switch in.Callee {
	case "__fern_rc_is_unique":
		out = append(out,
			fmt.Sprintf("cmp %s, #0x10000", p),
			fmt.Sprintf("b.lo %s", lbl("no")),
			fmt.Sprintf("ldur %s, [%s, #-8]", rc, p),
			fmt.Sprintf("cmp %s, #1", rc),
			fmt.Sprintf("cset %s, eq", wreg(in.Dst)),
			fmt.Sprintf("b %s", lbl("done")),
			lbl("no")+":",
			fmt.Sprintf("mov %s, #0", wreg(in.Dst)),
			lbl("done")+":",
		)
		return out, true
	case "__fern_rc_inc":
		out = append(out,
			fmt.Sprintf("cmp %s, #0x10000", p),
			fmt.Sprintf("b.lo %s", lbl("skip")),
			fmt.Sprintf("ldur %s, [%s, #-8]", rc, p),
			fmt.Sprintf("tbnz %s, #31, %s", rc, lbl("skip")), // a static sentinel
			fmt.Sprintf("add %s, %s, #1", rc, rc),
			fmt.Sprintf("stur %s, [%s, #-8]", rc, p),
			lbl("skip")+":",
		)
	case "__fern_rc_dec":
		out = append(out,
			fmt.Sprintf("cmp %s, #0x10000", p),
			fmt.Sprintf("b.lo %s", lbl("skip")),
			fmt.Sprintf("ldur %s, [%s, #-8]", rc, p),
			fmt.Sprintf("tbnz %s, #31, %s", rc, lbl("skip")), // a static sentinel
			fmt.Sprintf("cbz %s, %s", rc, lbl("under")),      // an over-release
			fmt.Sprintf("sub %s, %s, #1", rc, rc),
			fmt.Sprintf("stur %s, [%s, #-8]", rc, p),
			fmt.Sprintf("b %s", lbl("skip")),
			lbl("under")+":",
			fmt.Sprintf("adrp %s, %s", xreg(s2), rcUnderflowSym),
			fmt.Sprintf("add %s, %s, #:lo12:%s", xreg(s2), xreg(s2), rcUnderflowSym),
			fmt.Sprintf("ldr %s, [%s]", rc, xreg(s2)),
			fmt.Sprintf("add %s, %s, #1", rc, rc),
			fmt.Sprintf("str %s, [%s]", rc, xreg(s2)),
			lbl("skip")+":",
		)
	}
	if in.Dst != ptr {
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), p))
	}
	return out, true
}

// inlineAllocLines renders a MemAlloc of a constant small-tier size as an
// inline pop with the trampoline as the slow path, or a constant-size
// __fern_box_free as an inline push; it reports false for anything else and
// under the census. x16 and x17 are the per-instruction scratch the
// trampoline sequence already uses; s0 homes a slot-resident box and s1 the
// old list head.
func inlineAllocLines(in x86.Inst, fr frameLayout, numAlloc int, seed string) ([]string, bool) {
	lbl := func(suffix string) string { return fmt.Sprintf(".Lssa_alloc_%s_%s", seed, suffix) }
	heads := func(base string) []string {
		return []string{
			fmt.Sprintf("adrp %s, %s", base, freelistSym),
			fmt.Sprintf("add %s, %s, #:lo12:%s", base, base, freelistSym),
		}
	}
	if in.Op == x86.MemAlloc && in.SrcImm && !ast.LeakCheckEnabled {
		idx, ok := x86.SmallClassIndex(in.Imm)
		if !ok {
			return nil, false
		}
		dst := xreg(in.Dst)
		out := heads("x16")
		out = append(out,
			fmt.Sprintf("ldr %s, [x16, #%d]", dst, 8*idx),
			fmt.Sprintf("cbz %s, %s", dst, lbl("bump")),
			fmt.Sprintf("ldr x17, [%s]", dst), // the block's successor
			fmt.Sprintf("str x17, [x16, #%d]", 8*idx),
			fmt.Sprintf("b %s", lbl("done")),
			lbl("bump")+":",
			fmt.Sprintf("mov x16, #%d", in.Imm),
		)
		out = append(out, allocPresLines()...)
		return append(out,
			fmt.Sprintf("mov %s, x16", dst),
			lbl("done")+":",
		), true
	}
	if !boxFreeInline(in) {
		return nil, false
	}
	idx, _ := x86.SmallClassIndex(in.Imm + 8) // the payload plus its rc header
	s0, s1 := numAlloc, numAlloc+1
	var out []string
	data := in.ArgLocs[0].Reg
	if !in.ArgLocs[0].IsReg {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(s0), fr.slot(in.ArgLocs[0].Slot)))
		data = s0
	}
	out = append(out,
		fmt.Sprintf("cmp %s, #0x10000", xreg(data)),
		fmt.Sprintf("b.lo %s", lbl("skip")),
		fmt.Sprintf("sub x16, %s, #8", xreg(data)), // the block base
	)
	out = append(out, heads("x17")...)
	out = append(out,
		fmt.Sprintf("ldr %s, [x17, #%d]", xreg(s1), 8*idx), // old head
		fmt.Sprintf("str %s, [x16]", xreg(s1)),             // base.next = old head
		fmt.Sprintf("str x16, [x17, #%d]", 8*idx),
		lbl("skip")+":",
	)
	if in.Dst != data {
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(data)))
	}
	return out, true
}
