package x86_64ssa

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// referencedRuntimeHelpers skips a call inlinedCall claims the renderer writes
// inline, which leaves that helper out of .text entirely. If the renderer then
// emits the call after all, it names a label nothing defines and the backend
// refuses its own output (#9618). The two decisions are made in different
// places, so pin them to each other here rather than to a list of shapes: a
// gate added to one and not the other fails this test, whichever gate it is.
//
// Only Call-shaped instructions are constrained. MemAlloc has no callee, so
// the scan never reasons about it and the renderer is free to decline.
func TestInlinedCallAgreesWithTheRenderer(t *testing.T) {
	reg := func(r int) Loc { return Loc{IsReg: true, Reg: r} }
	shapes := []struct {
		name string
		in   Inst
	}{
		{"box_free const small", Inst{Op: Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free const at the tier edge", Inst{Op: Call, Callee: "__fern_box_free", SrcImm: true, Imm: 2040,
			ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free const above the tier", Inst{Op: Call, Callee: "__fern_box_free", SrcImm: true, Imm: 4096,
			ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free size in a register", Inst{Op: Call, Callee: "__fern_box_free", Imm: 16,
			ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free one argument", Inst{Op: Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []Loc{reg(0)}, Dst: 0}},
		{"box_free slot-resident box", Inst{Op: Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []Loc{{Slot: 2}, reg(1)}, Dst: 0}},
		{"box_free as a pair call", Inst{Op: CallPair, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"rc_inc", Inst{Op: Call, Callee: "__fern_rc_inc", ArgLocs: []Loc{reg(0)}, Dst: 0}},
		{"rc_dec", Inst{Op: Call, Callee: "__fern_rc_dec", ArgLocs: []Loc{reg(0)}, Dst: 0}},
		{"rc_is_unique", Inst{Op: Call, Callee: "__fern_rc_is_unique", ArgLocs: []Loc{reg(0)}, Dst: 0}},
		{"rc_inc with two arguments", Inst{Op: Call, Callee: "__fern_rc_inc", ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"rc_inc with no arguments", Inst{Op: Call, Callee: "__fern_rc_inc", Dst: 0}},
		{"rc_inc as a pair call", Inst{Op: CallPair, Callee: "__fern_rc_inc", ArgLocs: []Loc{reg(0)}, Dst: 0}},
		{"an ordinary helper", Inst{Op: Call, Callee: "__str_concat", ArgLocs: []Loc{reg(0), reg(1)}, Dst: 0}},
		{"a module function", Inst{Op: Call, Callee: "main", Dst: 0}},
	}
	for _, census := range []bool{false, true} {
		for _, counting := range []bool{false, true} {
			t.Run(fmt.Sprintf("census=%v/counting=%v", census, counting), func(t *testing.T) {
				defer func(prev bool) { ast.LeakCheckEnabled = prev }(ast.LeakCheckEnabled)
				ast.LeakCheckEnabled = census
				for _, sh := range shapes {
					rendered := false
					if _, ok := inlineAllocLines(sh.in, 0, "t", counting); ok {
						rendered = true
					} else if _, ok := inlineRcLines(sh.in, 0, "t"); ok {
						rendered = true
					}
					if got := inlinedCall(sh.in); got != rendered {
						t.Errorf("%s: inlinedCall = %v but the renderer inlines = %v — "+
							"the scan and the renderer disagree, so this call either links to "+
							"a helper nothing emits or pays for one nothing calls", sh.name, got, rendered)
					}
				}
			})
		}
	}
}

// The census counts every allocation and every free inside __alloc and __free,
// so neither fast path may bypass them: an inline pop hands out a block the
// count never saw, and an inline push returns one the free count never saw.
// #9604 landed the gate for both in the renderer alone, which is what made the
// scan disagree; the rule belongs to the predicates, so assert it there.
func TestNeitherFastPathIsInlinedUnderTheCensus(t *testing.T) {
	defer func(prev bool) { ast.LeakCheckEnabled = prev }(ast.LeakCheckEnabled)
	ast.LeakCheckEnabled = true
	free := Inst{Op: Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
		ArgLocs: []Loc{{IsReg: true, Reg: 0}, {IsReg: true, Reg: 1}}, Dst: 0}
	if boxFreeInline(free) {
		t.Error("boxFreeInline is true under the census: the inline push never reaches __free, so the census would miss the free")
	}
	if _, ok := inlineAllocLines(free, 0, "t", countsAllocs(nil)); ok {
		t.Error("the renderer inlines the push under the census")
	}
	alloc := Inst{Op: MemAlloc, SrcImm: true, Imm: 16, Dst: 0}
	if _, ok := inlineAllocLines(alloc, 0, "t", countsAllocs(nil)); ok {
		t.Error("the renderer inlines the pop under the census: it would hand out a block __alloc never counted")
	}
	if !countsAllocs(nil) {
		t.Error("countsAllocs is false under the census, so the pop gate never fires")
	}
}
