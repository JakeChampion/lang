package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// bitCountFunc builds main() = <kind>(imm) at the given operand width.
func bitCountFunc(kind ssa.OpKind, width int8, imm int64) *ssa.Func {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	c := f.AddOp(e, ssa.OpConstInt)
	e.Ops[len(e.Ops)-1].Imm = imm
	e.Ops[len(e.Ops)-1].Width = width
	v := f.AddOp(e, kind, c)
	e.Ops[len(e.Ops)-1].Width = width
	f.SetRet(e, v)
	return f
}

// bitCountCases covers each op at both widths, including the zero operand the
// IR defines as the operand width (which is why the emitter selects
// lzcnt/tzcnt over the same-opcode bsr/bsf, undefined there), and a value whose
// answer DIFFERS between the widths — clz(1) is 31 at 32 and 63 at 64, so a
// backend reading the wrong width is off by exactly 32 rather than broken in a
// way any operand would show.
var bitCountCases = []struct {
	name  string
	kind  ssa.OpKind
	width int8
	imm   int64
	want  int64
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
	// A 32-bit count must ignore the high half: at width 32 only the low
	// 32 bits are the operand, so the set bits above bit 31 don't count and
	// the trailing-zero scan stops at 32 rather than finding bit 32.
	{"popcount32_ignores_high", ssa.OpPopcount, 0, -1, 32},
	{"ctz32_ignores_high", ssa.OpCtz, 0, 1 << 32, 32},
}

// The model interpreter agrees with ssa.Eval on every case.
func TestBitCountModelMatchesEval(t *testing.T) {
	for _, c := range bitCountCases {
		for _, numAlloc := range []int{1, 2, 4} {
			f := bitCountFunc(c.kind, c.width, c.imm)
			want, err := ssa.Eval(f)
			if err != nil {
				t.Fatalf("%s: Eval: %v", c.name, err)
			}
			if want != c.want {
				t.Errorf("%s: Eval = %d, want %d", c.name, want, c.want)
			}
			p, err := Emit(f, numAlloc)
			if err != nil {
				t.Fatalf("%s: Emit: %v", c.name, err)
			}
			got, err := Run(p, nil)
			if err != nil {
				t.Fatalf("%s: Run: %v", c.name, err)
			}
			if got != want {
				t.Errorf("%s (numAlloc=%d): Run = %d, Eval = %d", c.name, numAlloc, got, want)
			}
		}
	}
}

// And the real lzcnt / tzcnt / popcnt instructions agree, assembled and run
// natively. Every count here is under 256, so the exit code carries it whole.
func TestBitCountAsmRun(t *testing.T) {
	for _, c := range bitCountCases {
		for _, numAlloc := range []int{1, 4} {
			got := assembleRun(t, bitCountFunc(c.kind, c.width, c.imm), numAlloc)
			if int64(got) != c.want {
				t.Errorf("%s (numAlloc=%d): exit = %d, want %d", c.name, numAlloc, got, c.want)
			}
		}
	}
}
