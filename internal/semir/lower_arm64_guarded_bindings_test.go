package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func TestARM64TypedGuardedBindingReplacement(t *testing.T) {
	armLauncher(t)
	for _, mode := range []ParamMode{ParamBorrow, ParamCounted} {
		for _, present := range []int64{0, 1} {
			for _, saved := range []bool{false, true} {
				for _, rounds := range []int64{1, 64} {
					for _, optimize := range []bool{false, true} {
						t.Run(fmt.Sprintf("mode-%d/present-%d/saved-%t/rounds-%d/optimized-%t", mode, present, saved, rounds, optimize), func(t *testing.T) {
							a, _ := guardedBindingDiamond(mode, saved)
							if err := promoteBindings(a.f); err != nil {
								t.Fatal(err)
							}
							out := lowerSemanticARM64(t, a.f)
							b := harnessBuilder(out)
							input := b.array(b.constant(2), 8)
							b.store(input, 0, b.constant(17), armU64)
							b.store(input, 8, b.constant(29), armU64)
							result := b.call(out.Symbols["pilot"], 64, true, b.constant(present), input)
							if mode == ParamBorrow {
								b.call("__fern_arr_dec", 64, true, input, b.constant(8))
							}
							b.loop(b.constant(rounds), func(_ ssa.Value) {
								noise := b.array(b.constant(2), 8)
								b.store(noise, 0, b.constant(99), armU64)
								b.call("__fern_arr_dec", 64, true, noise, b.constant(8))
							})
							var values []int64
							if present != 0 {
								values = []int64{17, 29}
								if !saved {
									values = append(values, 41)
								}
							}
							bad := b.op(ssa.OpNe, 32, false, b.load(result, -4, armU32), b.constant(int64(len(values))))
							for i, want := range values {
								wrong := b.op(ssa.OpNe, 32, false, b.load(result, int64(i)*8, armU64), b.constant(want))
								bad = b.op(ssa.OpOr, 32, false, bad, wrong)
							}
							wrongRC := b.op(ssa.OpNe, 32, false, b.load(result, -8, armU32), b.constant(1))
							bad = b.op(ssa.OpOr, 32, false, bad, wrongRC)
							b.call("__fern_arr_dec", 64, true, result, b.constant(8))
							b.f.SetRet(b.b, bad)
							requireARM64Success(t, out, b.f.Name, optimize)
						})
					}
				}
			}
		}
	}
}

func TestARM64TypedGuardedBindingReplacementLoop(t *testing.T) {
	armLauncher(t)
	for _, rounds := range []int64{0, 1, 2, 3, 64} {
		for _, present := range []int64{0, 1} {
			for _, saved := range []bool{false, true} {
				for _, optimize := range []bool{false, true} {
					t.Run(fmt.Sprintf("rounds-%d/present-%d/saved-%t/optimized-%t", rounds, present, saved, optimize), func(t *testing.T) {
						f := guardedBindingReplacementLoop(saved)
						if err := promoteBindings(f); err != nil {
							t.Fatal(err)
						}
						out := lowerSemanticARM64(t, f)
						b := harnessBuilder(out)
						result := b.call(out.Symbols["pilot"], 64, true, b.constant(rounds), b.constant(present))
						b.loop(b.constant(rounds+1), func(_ ssa.Value) {
							noise := b.array(b.constant(1), 4)
							b.store(noise, 0, b.constant(99), armU32)
							b.call("__fern_arr_dec", 64, true, noise, b.constant(4))
						})
						length := present
						if present != 0 && !saved {
							length += rounds
						}
						bad := b.op(ssa.OpNe, 32, false, b.load(result, -4, armU32), b.constant(length))
						for i := int64(0); i < length; i++ {
							want := int64(0)
							if i > 0 {
								want = i - 1
							}
							wrong := b.op(ssa.OpNe, 32, false, b.load(result, i*4, armU32), b.constant(want))
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
	}
}
