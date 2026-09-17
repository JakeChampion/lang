package arm64ssa

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
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
	reg := func(r int) x86.Loc { return x86.Loc{IsReg: true, Reg: r} }
	var fr frameLayout
	shapes := []struct {
		name string
		in   x86.Inst
	}{
		{"box_free const small", x86.Inst{Op: x86.Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free const at the tier edge", x86.Inst{Op: x86.Call, Callee: "__fern_box_free", SrcImm: true, Imm: 2040,
			ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free const above the tier", x86.Inst{Op: x86.Call, Callee: "__fern_box_free", SrcImm: true, Imm: 4096,
			ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free size in a register", x86.Inst{Op: x86.Call, Callee: "__fern_box_free", Imm: 16,
			ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"box_free one argument", x86.Inst{Op: x86.Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []x86.Loc{reg(0)}, Dst: 0}},
		{"box_free slot-resident box", x86.Inst{Op: x86.Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []x86.Loc{{Slot: 2}, reg(1)}, Dst: 0}},
		{"box_free as a pair call", x86.Inst{Op: x86.CallPair, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
			ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"rc_inc", x86.Inst{Op: x86.Call, Callee: "__fern_rc_inc", ArgLocs: []x86.Loc{reg(0)}, Dst: 0}},
		{"rc_dec", x86.Inst{Op: x86.Call, Callee: "__fern_rc_dec", ArgLocs: []x86.Loc{reg(0)}, Dst: 0}},
		{"rc_is_unique", x86.Inst{Op: x86.Call, Callee: "__fern_rc_is_unique", ArgLocs: []x86.Loc{reg(0)}, Dst: 0}},
		{"rc_inc with two arguments", x86.Inst{Op: x86.Call, Callee: "__fern_rc_inc", ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"rc_inc with no arguments", x86.Inst{Op: x86.Call, Callee: "__fern_rc_inc", Dst: 0}},
		{"rc_inc as a pair call", x86.Inst{Op: x86.CallPair, Callee: "__fern_rc_inc", ArgLocs: []x86.Loc{reg(0)}, Dst: 0}},
		{"an ordinary helper", x86.Inst{Op: x86.Call, Callee: "__str_concat", ArgLocs: []x86.Loc{reg(0), reg(1)}, Dst: 0}},
		{"a module function", x86.Inst{Op: x86.Call, Callee: "main", Dst: 0}},
	}
	for _, census := range []bool{false, true} {
		for _, counting := range []bool{false, true} {
			t.Run(fmt.Sprintf("census=%v/counting=%v", census, counting), func(t *testing.T) {
				defer func(prev bool) { ast.LeakCheckEnabled = prev }(ast.LeakCheckEnabled)
				ast.LeakCheckEnabled = census
				for _, sh := range shapes {
					rendered := false
					if _, ok := inlineAllocLines(sh.in, fr, 0, "t", counting); ok {
						rendered = true
					} else if _, ok := inlineRcLines(sh.in, fr, 0, "t"); ok {
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
func TestNeitherFastPathIsInlinedUnderTheCensus(t *testing.T) {
	defer func(prev bool) { ast.LeakCheckEnabled = prev }(ast.LeakCheckEnabled)
	ast.LeakCheckEnabled = true
	free := x86.Inst{Op: x86.Call, Callee: "__fern_box_free", SrcImm: true, Imm: 16,
		ArgLocs: []x86.Loc{{IsReg: true, Reg: 0}, {IsReg: true, Reg: 1}}, Dst: 0}
	if boxFreeInline(free) {
		t.Error("boxFreeInline is true under the census: the inline push never reaches __free, so the census would miss the free")
	}
	if _, ok := inlineAllocLines(free, frameLayout{}, 0, "t", countsAllocs(nil)); ok {
		t.Error("the renderer inlines the push under the census")
	}
	alloc := x86.Inst{Op: x86.MemAlloc, SrcImm: true, Imm: 16, Dst: 0}
	if _, ok := inlineAllocLines(alloc, frameLayout{}, 0, "t", countsAllocs(nil)); ok {
		t.Error("the renderer inlines the pop under the census: it would hand out a block __alloc never counted")
	}
	if !countsAllocs(nil) {
		t.Error("countsAllocs is false under the census, so the pop gate never fires")
	}
}
