package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestARM64TypedBindingSnapshots(t *testing.T) {
	armLauncher(t)
	for _, mode := range []ParamMode{ParamBorrow, ParamCounted} {
		for _, present := range []int64{0, 1} {
			for _, optimize := range []bool{false, true} {
				t.Run(fmt.Sprintf("mode-%d/present-%d/optimized-%t", mode, present, optimize), func(t *testing.T) {
					a := bindingSnapshotDiamond(ast.ArrayType{Elem: armU64}, mode)
					if err := promoteBindings(a.f); err != nil {
						t.Fatal(err)
					}
					out := lowerSemanticARM64(t, a.f)
					b := harnessBuilder(out)
					input := b.array(b.constant(2), 8)
					b.store(input, 0, b.constant(17), armU64)
					b.store(input, 8, b.constant(29), armU64)
					result := b.call(out.Symbols["pilot"], 64, true, b.constant(present), input)
					bad := b.op(ssa.OpNe, 32, false, b.load(result, -4, armU32), b.constant(2*present))
					if present != 0 {
						for i, want := range []int64{17, 29} {
							wrong := b.op(ssa.OpNe, 32, false, b.load(result, int64(i)*8, armU64), b.constant(want))
							bad = b.op(ssa.OpOr, 32, false, bad, wrong)
						}
					}
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

func TestARM64TypedBindingSnapshotSavedPayload(t *testing.T) {
	armLauncher(t)
	for _, tc := range []struct {
		name  string
		build func() *Func
	}{
		{"loop", bindingSnapshotLoop}, {"replacement", bindingSnapshotReplacement}, {"lifetime", bindingSnapshotLifetime},
	} {
		for _, rounds := range []int64{1, 64} {
			for _, optimize := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/rounds-%d/optimized-%t", tc.name, rounds, optimize), func(t *testing.T) {
					f := tc.build()
					if err := promoteBindings(f); err != nil {
						t.Fatal(err)
					}
					out := lowerSemanticARM64(t, f)
					b := harnessBuilder(out)
					input := b.array(b.constant(1), 8)
					b.store(input, 0, b.constant(41), armU64)
					result := b.call(out.Symbols["pilot"], 64, true, input)
					b.loop(b.constant(rounds), func(_ ssa.Value) {
						noise := b.array(b.constant(1), 8)
						b.store(noise, 0, b.constant(99), armU64)
						b.call("__fern_arr_dec", 64, true, noise, b.constant(8))
					})
					bad := b.op(ssa.OpNe, 32, false, b.load(result, 0, armU64), b.constant(41))
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
