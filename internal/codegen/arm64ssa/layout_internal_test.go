package arm64ssa

import (
	"strings"
	"testing"

	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// prog builds a Program from terminators alone; the layout only reads those.
func prog(entry int, terms ...x86.Term) *x86.Program {
	p := &x86.Program{Entry: entry}
	for _, t := range terms {
		p.Blocks = append(p.Blocks, x86.MBlock{Term: t})
	}
	return p
}

// The abstract emitter numbers blocks in the lifter's creation order and
// appends critical-edge splits at the end, so index order leaves a branch in
// front of nearly every label. The walk follows the fallthrough chain instead.
func TestLayoutOrderFollowsTheFallthroughChain(t *testing.T) {
	// b0 -> b3 -> b1; b2 is the taken arm of b0 and is placed after the chain.
	p := prog(0,
		x86.Term{Kind: x86.TBrIf, True: 2, False: 3},
		x86.Term{Kind: x86.TRet},
		x86.Term{Kind: x86.TJmp, Target: 1},
		x86.Term{Kind: x86.TJmp, Target: 1},
	)
	got := layoutOrder(p)
	want := []int{0, 3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// Every block must be written exactly once whatever the CFG shape, or a label
// dangles (missing) or is defined twice (duplicate).
func TestLayoutOrderIsAPermutation(t *testing.T) {
	cases := map[string]*x86.Program{
		"self loop": prog(0,
			x86.Term{Kind: x86.TBrIf, True: 0, False: 1},
			x86.Term{Kind: x86.TRet},
		),
		"unreferenced block": prog(0,
			x86.Term{Kind: x86.TRet},
			x86.Term{Kind: x86.TJmp, Target: 0},
		),
		"entry is not block 0": prog(2,
			x86.Term{Kind: x86.TRet},
			x86.Term{Kind: x86.TJmp, Target: 0},
			x86.Term{Kind: x86.TBrIf, True: 1, False: 0},
		),
		"diamond into a loop": prog(0,
			x86.Term{Kind: x86.TBrIf, True: 1, False: 2},
			x86.Term{Kind: x86.TJmp, Target: 3},
			x86.Term{Kind: x86.TJmp, Target: 3},
			x86.Term{Kind: x86.TBrIf, True: 0, False: 4},
			x86.Term{Kind: x86.TRet},
		),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			got := layoutOrder(p)
			if len(got) != len(p.Blocks) {
				t.Fatalf("order %v covers %d of %d blocks", got, len(got), len(p.Blocks))
			}
			seen := make([]bool, len(p.Blocks))
			for _, b := range got {
				if b < 0 || b >= len(p.Blocks) {
					t.Fatalf("order %v has out-of-range block %d", got, b)
				}
				if seen[b] {
					t.Fatalf("order %v repeats block %d", got, b)
				}
				seen[b] = true
			}
			if got[0] != p.Entry {
				t.Fatalf("order %v starts at %d, want the entry %d", got, got[0], p.Entry)
			}
		})
	}
}

// The abstract two-address form copies an instruction's left operand into its
// destination first. Every AArch64 instruction the renderer emits for one is
// three-address (and cmp / fcmp have no destination at all), so the copy is
// dead and the instruction names the copy's source directly.
func TestDeadAccMovesFindsTheCopyInFrontOfATwoAddressOp(t *testing.T) {
	insts := []x86.Inst{
		{Op: x86.MovReg, Dst: 3, Src: 1},
		{Op: x86.SetCmp, Dst: 3, Src: 2},
		{Op: x86.MovReg, Dst: 5, Src: 4},
		{Op: x86.FCmp, Dst: 5, Src: 6},
	}
	skip, left := deadAccMoves(insts)
	if !skip[0] || !skip[2] {
		t.Errorf("skip = %v, want the copies at 0 and 2 dropped", skip)
	}
	if left[1] != 1 || left[3] != 4 {
		t.Errorf("left = %v, want the comparisons to read 1 and 4", left)
	}
	if left[0] != -1 || left[2] != -1 {
		t.Errorf("left = %v, want -1 on the copies themselves", left)
	}
}

// When the comparison's right operand IS the destination, the copy holds the
// value being compared against and dropping it would compare a register that
// was never written.
func TestDeadAccMovesKeepsACopyTheOperationStillReads(t *testing.T) {
	insts := []x86.Inst{
		{Op: x86.MovReg, Dst: 3, Src: 1},
		{Op: x86.SetCmp, Dst: 3, Src: 3},
	}
	skip, left := deadAccMoves(insts)
	if skip[0] {
		t.Errorf("skip = %v, want the copy kept", skip)
	}
	if left[1] != -1 {
		t.Errorf("left = %v, want the comparison to read its destination", left)
	}
}

// A copy in front of anything else stays put.
func TestDeadAccMovesLeavesCopiesBeforeNonAccumulatingOpsAlone(t *testing.T) {
	insts := []x86.Inst{
		{Op: x86.MovReg, Dst: 3, Src: 1},
		{Op: x86.MemLoad, Dst: 3, Src: 2},
		{Op: x86.MovReg, Dst: 7, Src: 1},
		{Op: x86.SetCmp, Dst: 3, Src: 2},
	}
	skip, _ := deadAccMoves(insts)
	for i, s := range skip {
		if s {
			t.Errorf("skip[%d] set; want every copy kept (skip = %v)", i, skip)
		}
	}
}

// The arithmetic ops are three-address on AArch64, so the copy in front of a
// BinOp is dead too — including when the left operand stays live afterwards,
// which no amount of destination coalescing could have fixed.
func TestDeadAccMovesFindsTheCopyInFrontOfArithmetic(t *testing.T) {
	insts := []x86.Inst{
		{Op: x86.MovReg, Dst: 3, Src: 1},
		{Op: x86.BinOp, Dst: 3, Src: 2},
		{Op: x86.MovReg, Dst: 6, Src: 4},
		{Op: x86.UnNeg, Dst: 6},
		{Op: x86.MovReg, Dst: 8, Src: 9},
		{Op: x86.UnOp, Dst: 8},
	}
	skip, left := deadAccMoves(insts)
	if !skip[0] || !skip[2] || !skip[4] {
		t.Errorf("skip = %v, want the copies at 0, 2 and 4 dropped", skip)
	}
	if left[1] != 1 || left[3] != 4 || left[5] != 9 {
		t.Errorf("left = %v, want left operands 1, 4 and 9", left)
	}
}

// A unary op reads no second operand, so a copy in front of one is dead even
// when the destination is also the copy's source.
func TestDeadAccMovesDoesNotNeedASecondOperandCheckForUnaryOps(t *testing.T) {
	insts := []x86.Inst{
		{Op: x86.MovReg, Dst: 3, Src: 1},
		{Op: x86.UnNeg, Dst: 3, Src: 3},
	}
	skip, left := deadAccMoves(insts)
	if !skip[0] || left[1] != 1 {
		t.Errorf("skip = %v left = %v, want the copy dropped and the negate reading 1", skip, left)
	}
}

func TestInvCondIsAnInvolution(t *testing.T) {
	for _, cc := range []string{"eq", "ne", "lt", "ge", "le", "gt", "lo", "hs", "ls", "hi"} {
		inv, ok := invCond(cc)
		if !ok {
			t.Fatalf("invCond(%q) not supported", cc)
		}
		back, ok := invCond(inv)
		if !ok || back != cc {
			t.Errorf("invCond(invCond(%q)) = %q, want %q", cc, back, cc)
		}
	}
}

// Every condition the renderer can put on a cset must also be invertible, or a
// fused branch whose taken arm falls through has no spelling.
func TestEveryConditionCodeHasAnInverse(t *testing.T) {
	kinds := []ssa.OpKind{
		ssa.OpEq, ssa.OpNe, ssa.OpLt, ssa.OpLe, ssa.OpGt, ssa.OpGe,
		ssa.OpLtU, ssa.OpLeU, ssa.OpGtU, ssa.OpGeU,
	}
	for _, k := range kinds {
		cc, ok := condCode(k)
		if !ok {
			t.Fatalf("condCode(%v) not supported", k)
		}
		if _, ok := invCond(cc); !ok {
			t.Errorf("condCode(%v) = %q has no inverse", k, cc)
		}
	}
}

// AArch64's loads already extend into the full register, so the model's i32
// sign-extension is either the load's own form (ldrsw) or a no-op — a separate
// sxtw after one is pure waste. Only the 8-byte load genuinely narrows.
func TestMemLoadSeqFoldsTheWidthMaskIntoTheLoad(t *testing.T) {
	cases := []struct {
		name string
		in   x86.Inst
		want []string
	}{
		{"i32 word load", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 4, W: 32},
			[]string{"ldrsw x1, [x2, #0]"}},
		{"i64 word load keeps its zero-extension", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 4, W: 64},
			[]string{"ldr w1, [x2, #0]"}},
		{"unsigned byte load", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 1, W: 32},
			[]string{"ldrb w1, [x2, #0]"}},
		{"signed byte load", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 1, Signed: true, W: 32},
			[]string{"ldrsb x1, [x2, #0]"}},
		{"unsigned halfword load", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 2, W: 32},
			[]string{"ldrh w1, [x2, #0]"}},
		{"signed halfword load", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 2, Signed: true, W: 32},
			[]string{"ldrsh x1, [x2, #0]"}},
		{"doubleword load still narrows", x86.Inst{Op: x86.MemLoad, Dst: 1, Src: 2, Bytes: 8, W: 32},
			[]string{"ldr x1, [x2, #0]", "sxtw x1, w1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := memLoadSeq(c.in)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The renderer only drops the comparison when the emitter's CondFuse says
// nothing else reads its 0/1, and when the block really does end with it.
func TestFuseBranchCmpNeedsBothTheAnnotationAndTheShape(t *testing.T) {
	cmp := x86.Inst{Op: x86.SetCmp, Dst: 3, Src: 2, K: ssa.OpLt}
	mov := x86.Inst{Op: x86.MovReg, Dst: 3, Src: 1}
	brIf := func(fuse bool) x86.Term {
		return x86.Term{Kind: x86.TBrIf, CondReg: 3, CondFuse: fuse}
	}

	t.Run("fuses and takes the left operand from the dropped copy", func(t *testing.T) {
		insts, cc, left, right := fuseBranchCmp(x86.MBlock{Insts: []x86.Inst{mov, cmp}, Term: brIf(true)})
		if cc != "lt" || left != 1 || right != 2 || len(insts) != 0 {
			t.Errorf("got insts=%v cc=%q left=%d right=%d, want the block emptied and lt on (1, 2)", insts, cc, left, right)
		}
	})

	t.Run("no annotation, no fusion", func(t *testing.T) {
		insts, cc, _, _ := fuseBranchCmp(x86.MBlock{Insts: []x86.Inst{mov, cmp}, Term: brIf(false)})
		if cc != "" || len(insts) != 2 {
			t.Errorf("got insts=%v cc=%q, want both instructions kept", insts, cc)
		}
	})

	t.Run("comparison is not the last instruction", func(t *testing.T) {
		trailing := []x86.Inst{cmp, {Op: x86.MovReg, Dst: 9, Src: 8}}
		insts, cc, _, _ := fuseBranchCmp(x86.MBlock{Insts: trailing, Term: brIf(true)})
		if cc != "" || len(insts) != 2 {
			t.Errorf("got insts=%v cc=%q, want both instructions kept", insts, cc)
		}
	})

	t.Run("comparison writes a different register than the branch reads", func(t *testing.T) {
		other := x86.Inst{Op: x86.SetCmp, Dst: 4, Src: 2, K: ssa.OpLt}
		insts, cc, _, _ := fuseBranchCmp(x86.MBlock{Insts: []x86.Inst{other}, Term: brIf(true)})
		if cc != "" || len(insts) != 1 {
			t.Errorf("got insts=%v cc=%q, want the comparison kept", insts, cc)
		}
	})

	t.Run("a return terminator never fuses", func(t *testing.T) {
		insts, cc, _, _ := fuseBranchCmp(x86.MBlock{Insts: []x86.Inst{cmp}, Term: x86.Term{Kind: x86.TRet}})
		if cc != "" || len(insts) != 1 {
			t.Errorf("got insts=%v cc=%q, want the comparison kept", insts, cc)
		}
	})
}
