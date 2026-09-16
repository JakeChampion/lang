package arm64ssa

import (
	"strings"
	"testing"

	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// A constant shift count renders as the instruction's own immediate, masked
// to the width as a register count would be.
func TestShiftByImmediateRenders(t *testing.T) {
	cases := []struct {
		in   x86.Inst
		want string
	}{
		{x86.Inst{Op: x86.BinOp, K: ssa.OpShl, Dst: 1, Src: 1, Imm: 3, SrcImm: true, W: 64}, "lsl x1, x1, #3"},
		{x86.Inst{Op: x86.BinOp, K: ssa.OpShr, Dst: 1, Src: 1, Imm: 31, SrcImm: true, W: 32}, "asr w1, w1, #31"},
		{x86.Inst{Op: x86.BinOp, K: ssa.OpShrU, Dst: 2, Src: 2, Imm: 30, SrcImm: true, W: 32}, "lsr w2, w2, #30"},
		{x86.Inst{Op: x86.BinOp, K: ssa.OpShl, Dst: 1, Src: 1, Imm: 68, SrcImm: true, W: 64}, "lsl x1, x1, #4"},
	}
	for _, c := range cases {
		got := strings.Join(divShiftSeq(c.in, c.in.Dst, 16), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("render of %+v = %q, want it to contain %q", c.in, got, c.want)
		}
	}
}
