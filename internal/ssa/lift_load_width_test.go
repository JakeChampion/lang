package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A full-word OpLoad must carry Width 64 onto the SSA op, not merely pick the
// 8-byte KIND. The backends' memLoadSeq / MemLoad render the load and then
// apply maskFix(dst, in.W), which sign-extends from bit 31 whenever the width
// is not 64 — so a lift that set the kind but left Width 0 emitted a correct
// `ldr x` followed by an `sxtw` that threw the top half away.
//
// That silently corrupted every i64[] element outside int32 range on
// -target arm64-ssa (the only CLI-reachable consumer of this lift): 2576980379
// read back as -1717986917, 4294967295 as -1, and 1234567890123 as 1912276171
// — its own low 32 bits. std/float's bignum limbs are exactly such values, so
// f64 .to_string() produced a wrong significand and then trapped out of bounds.
//
// This is the same hazard the OpFToIS / OpFToIU arm of the lift already
// documents; the memory arm just never got the matching propagation.
func TestLiftLoadCarriesWidth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		irWidth  int
		wantKind OpKind
		wantW    int8
	}{
		{"explicit 64", 64, OpLoad, 64},
		{"pointer width", ir.WidthPtr, OpLoad, 64},
		// A 4-byte load keeps Width 0: its value is stored sign-extended, so
		// the backend's maskFix is the correct re-normalisation.
		{"default i32 word", 0, OpLoad32U, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := LiftFromIR(&ir.Func{
				Name: "f",
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 4096},
					{Kind: ir.OpLoad, Width: tc.irWidth},
					{Kind: ir.OpReturn},
				},
			})
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			var load *Op
			for _, op := range out.Blocks[0].Ops {
				if op.Kind == OpLoad || op.Kind == OpLoad32U {
					load = op
				}
			}
			if load == nil {
				t.Fatalf("no load op in lifted output: %+v", out.Blocks[0].Ops)
			}
			if load.Kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", load.Kind, tc.wantKind)
			}
			if load.Width != tc.wantW {
				t.Errorf("Width = %d, want %d (a full-word load left at 0 gets sign-extended from bit 31 by the backend's maskFix)", load.Width, tc.wantW)
			}
		})
	}
}
