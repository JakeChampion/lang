package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The bit-count ops on the real-asm path, assembled and run under
// qemu-aarch64. AArch64 has clz but no ctz (rbit first turns trailing zeros
// into leading ones) and no scalar popcount (cnt is Advanced SIMD and counts
// per byte, so the value shuttles through v0 and addv sums the eight counts).
//
// Every case is checked at both widths, including the zero operand the IR
// defines as the operand width, and values whose answer DIFFERS between the
// widths — clz(1) is 31 at 32 and 63 at 64 — so reading the wrong width is off
// by exactly 32 rather than broken on every operand. Counts stay under 256, so
// the exit code carries them whole.
func TestArmRunBitCount(t *testing.T) {
	cases := []struct {
		name  string
		kind  ssa.OpKind
		width int8
		imm   int64
		want  int
	}{
		{"popcount32", ssa.OpPopcount, 0, 255, 8},
		{"popcount32_zero", ssa.OpPopcount, 0, 0, 0},
		{"popcount64", ssa.OpPopcount, 64, 0x0F0F0F0F0F0F0F0F, 32},
		{"clz32_one", ssa.OpClz, 0, 1, 31},
		{"clz32_zero", ssa.OpClz, 0, 0, 32},
		{"clz64_one", ssa.OpClz, 64, 1, 63},
		{"clz64_zero", ssa.OpClz, 64, 0, 64},
		{"ctz32_eight", ssa.OpCtz, 0, 8, 3},
		{"ctz32_zero", ssa.OpCtz, 0, 0, 32},
		{"ctz64_1024", ssa.OpCtz, 64, 1024, 10},
		{"ctz64_zero", ssa.OpCtz, 64, 0, 64},
		// A 32-bit count must ignore the high half — the w-register forms are
		// what make that true, and the x-register ones would answer 64 and 32.
		{"popcount32_ignores_high", ssa.OpPopcount, 0, -1, 32},
		{"ctz32_ignores_high", ssa.OpCtz, 0, 1 << 32, 32},
	}
	for _, c := range cases {
		for _, numAlloc := range []int{1, 12} {
			f := ssa.NewFunc("main")
			e := f.NewBlock()
			imm := f.AddOp(e, ssa.OpConstInt)
			e.Ops[len(e.Ops)-1].Imm, e.Ops[len(e.Ops)-1].Width = c.imm, c.width
			v := f.AddOp(e, c.kind, imm)
			e.Ops[len(e.Ops)-1].Width = c.width
			f.SetRet(e, v)

			got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", numAlloc)
			if got != c.want {
				t.Errorf("%s (numAlloc=%d): exit = %d, want %d", c.name, numAlloc, got, c.want)
			}
		}
	}
}
