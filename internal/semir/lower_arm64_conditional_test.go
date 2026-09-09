package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestARM64TypedConditionalSharingAndProjection(t *testing.T) {
	armLauncher(t)
	for _, tc := range []struct {
		name  string
		build func() *Func
		loop  bool
	}{
		{"shared-phi", func() *Func { return conditionalSharedDiamond().f }, false},
		{"dead-phi", func() *Func {
			a := conditionalSharedDiamond()
			unused := a.join.Ops[len(a.join.Ops)-1]
			a.join.Ops = a.join.Ops[:len(a.join.Ops)-1]
			delete(a.f.values, unused.Result.ID)
			return a.f
		}, false},
		{"nested-projection", func() *Func { return conditionalNestedProjection().f }, false},
		{"loop", conditionalArrayLoop, true},
	} {
		for _, rounds := range []int64{1, 64} {
			for _, present := range []int64{0, 1} {
				if tc.loop && present == 0 {
					continue
				}
				for _, optimize := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/rounds-%d/present-%d/optimized-%t", tc.name, rounds, present, optimize), func(t *testing.T) {
						out := lowerSemanticARM64(t, tc.build())
						b := harnessBuilder(out)
						input := b.array(b.constant(2), 8)
						b.store(input, 0, b.constant(17), armU64)
						b.store(input, 8, b.constant(29), armU64)
						args := []ssa.Value{b.constant(present), input}
						if tc.loop {
							args = []ssa.Value{input}
						}
						result := b.call(out.Symbols["pilot"], 64, true, args...)
						b.loop(b.constant(rounds), func(_ ssa.Value) {
							noise := b.array(b.constant(2), 8)
							b.store(noise, 0, b.constant(91), armU64)
							b.store(noise, 8, b.constant(92), armU64)
							b.call("__fern_arr_dec", 64, true, noise, b.constant(8))
						})
						bad := b.op(ssa.OpNe, 32, false, b.load(result, -8, armU32), b.constant(1))
						if present != 0 {
							for i, want := range []int64{17, 29} {
								wrong := b.op(ssa.OpNe, 32, false, b.load(result, int64(i)*8, armU64), b.constant(want))
								bad = b.op(ssa.OpOr, 32, false, bad, wrong)
							}
						}
						b.call("__fern_arr_dec", 64, true, result, b.constant(8))
						b.f.SetRet(b.b, bad)
						requireARM64Success(t, out, b.f.Name, optimize)
					})
				}
			}
		}
	}
}

func TestARM64TypedConditionalAppendPreservesSnapshot(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		t.Run(fmt.Sprintf("optimized-%t", optimize), func(t *testing.T) {
			a := conditionalArrayDiamond(ParamBorrow)
			elem := a.f.addOp(a.good, ssa.OpConstInt, ast.NumberType{Width: 64, Signed: true}, ast.Position{})
			a.good.Ops[len(a.good.Ops)-1].Imm = 41
			updated := a.f.addOp(a.good, ssa.OpArrayAppend, a.f.result, ast.Position{}, a.get.Result, elem)
			a.f.graph.SetRet(a.good, updated)
			out := lowerSemanticARM64(t, a.f)
			b := harnessBuilder(out)
			old := b.array(b.constant(2), 8)
			b.store(old, 0, b.constant(17), armU64)
			b.store(old, 8, b.constant(29), armU64)
			result := b.call(out.Symbols["pilot"], 64, true, b.constant(1), old)
			bad := b.constant(0)
			for _, check := range []struct {
				value        ssa.Value
				offset, want int64
			}{
				{old, 0, 17}, {old, 8, 29}, {result, 0, 17}, {result, 8, 29}, {result, 16, 41},
			} {
				wrong := b.op(ssa.OpNe, 32, false, b.load(check.value, check.offset, armU64), b.constant(check.want))
				bad = b.op(ssa.OpOr, 32, false, bad, wrong)
			}
			b.call("__fern_arr_dec", 64, true, old, b.constant(8))
			b.call("__fern_arr_dec", 64, true, result, b.constant(8))
			b.f.SetRet(b.b, bad)
			requireARM64Success(t, out, b.f.Name, optimize)
		})
	}
}

func TestARM64TypedConditionalPayloadUnits(t *testing.T) {
	armLauncher(t)
	for _, mode := range []ParamMode{ParamBorrow, ParamCounted} {
		for _, present := range []int64{0, 1} {
			for _, optimize := range []bool{false, true} {
				t.Run(fmt.Sprintf("mode-%d/present-%d/optimized-%t", mode, present, optimize), func(t *testing.T) {
					a := conditionalArrayDiamond(mode)
					out := lowerSemanticARM64(t, a.f)
					b := harnessBuilder(out)
					input := b.array(b.constant(2), 8)
					b.store(input, 0, b.constant(17), armU64)
					b.store(input, 8, b.constant(29), armU64)
					result := b.call(out.Symbols["pilot"], 64, true, b.constant(present), input)
					bad := b.constant(0)
					if present != 0 {
						for i, want := range []int64{17, 29} {
							wrong := b.op(ssa.OpNe, 32, false, b.load(result, int64(i)*8, armU64), b.constant(want))
							bad = b.op(ssa.OpOr, 32, false, bad, wrong)
						}
					}
					wantRC := int64(1)
					if mode == ParamBorrow && present != 0 {
						wantRC = 2
					}
					wrongRC := b.op(ssa.OpNe, 32, false, b.load(result, -8, armU32), b.constant(wantRC))
					bad = b.op(ssa.OpOr, 32, false, bad, wrongRC)
					b.call("__fern_arr_dec", 64, true, result, b.constant(8))
					if mode == ParamBorrow {
						b.call("__fern_arr_dec", 64, true, input, b.constant(8))
					}
					b.f.SetRet(b.b, bad)
					requireARM64Success(t, out, b.f.Name, optimize)
				})
			}
		}
	}
}

func TestARM64TypedConditionalRepeatedReplacement(t *testing.T) {
	armLauncher(t)
	for _, rounds := range []int64{0, 1, 2, 3, 64} {
		for _, optimize := range []bool{false, true} {
			t.Run(fmt.Sprintf("rounds-%d/optimized-%t", rounds, optimize), func(t *testing.T) {
				out := lowerSemanticARM64(t, conditionalReplacementLoop())
				b := harnessBuilder(out)
				result := b.call(out.Symbols["pilot"], 64, true, b.constant(rounds))
				length := int64(1)
				if rounds == 0 || rounds == 2 {
					length = 0
				}
				bad := b.op(ssa.OpNe, 32, false, b.load(result, -4, armU32), b.constant(length))
				if length != 0 {
					wrong := b.op(ssa.OpNe, 32, false, b.load(result, 0, armU32), b.constant(rounds-1))
					bad = b.op(ssa.OpOr, 32, false, bad, wrong)
				}
				wrongRC := b.op(ssa.OpNe, 32, false, b.load(result, -8, armU32), b.constant(1))
				bad = b.op(ssa.OpOr, 32, false, bad, wrongRC)
				b.call("__fern_arr_dec", 64, true, result, b.constant(4))
				b.f.SetRet(b.b, bad)
				requireARM64Success(t, out, b.f.Name, optimize)
			})
		}
	}
}

func TestARM64TypedConditionalBorrowedString(t *testing.T) {
	armLauncher(t)
	for _, present := range []int64{0, 1} {
		for _, optimize := range []bool{false, true} {
			t.Run(fmt.Sprintf("present-%d/optimized-%t", present, optimize), func(t *testing.T) {
				a := availabilityDiamond(ast.StringType{})
				a.f.modes[1] = ParamBorrow
				a.bad.Ops[0].Kind, a.bad.Ops[0].Imm = ssa.OpConstString, 0
				out := lowerSemanticARM64(t, a.f)
				b := harnessBuilder(out)
				base := b.op(ssa.OpAlloc, 64, true, b.constant(12))
				b.store(base, 0, b.constant(1), armI32)
				b.store(base, 4, b.constant(3), armI32)
				str := b.offset(base, 8)
				for i, ch := range []byte{'a', 0, 'z', 0} {
					b.store(str, int64(i), b.constant(int64(ch)), ast.NumberType{Width: 8})
				}
				result := b.call(out.Symbols["pilot"], 64, true, b.constant(present), str)
				bad := b.op(ssa.OpNe, 32, false, b.load(str, -8, armU32), b.constant(1+present))
				if present != 0 {
					bad = b.op(ssa.OpOr, 32, false, bad, b.op(ssa.OpNe, 32, false, result, str))
				}
				b.call("__fern_str_dec", 64, true, result)
				wrong := b.op(ssa.OpNe, 32, false, b.load(str, -8, armU32), b.constant(1))
				bad = b.op(ssa.OpOr, 32, false, bad, wrong)
				// Match the existing string-unit harness: retain a final fixture
				// owner and free its known allocation explicitly. This does not
				// repair or conceal the runtime's final-string-free limitation.
				b.call("__fern_box_free", 64, true, str, b.constant(4))
				b.f.SetRet(b.b, bad)
				requireARM64Success(t, out, b.f.Name, optimize)
			})
		}
	}
}

func TestARM64TypedConditionalAbsenceIgnoresPayloadBits(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		t.Run(fmt.Sprintf("optimized-%t", optimize), func(t *testing.T) {
			f := newFunc("pilot", ast.BoolType{})
			entry := f.graph.NewBlock()
			none := f.addState(entry, ssa.OpStateAbsent, ast.ArrayType{Elem: armU64}, ast.Position{})
			has := f.addOp(entry, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, none)
			f.graph.SetRet(entry, has)
			out := lowerSemanticARM64(t, f)
			poisoned := 0
			for _, block := range out.Functions[out.Symbols["pilot"]].Blocks {
				for _, op := range block.Ops {
					if op.Kind == ssa.OpConstInt && op.Addr {
						op.Imm = 1 // Invalid pointer, deliberately not a null sentinel.
						poisoned++
					}
				}
			}
			if poisoned != 1 {
				t.Fatalf("expected one inactive payload lane, got %d", poisoned)
			}
			b := harnessBuilder(out)
			b.f.SetRet(b.b, b.call(out.Symbols["pilot"], 32, false))
			requireARM64Success(t, out, b.f.Name, optimize)
		})
	}
}
