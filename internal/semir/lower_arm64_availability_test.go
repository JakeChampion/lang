package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestARM64TypedAvailabilityValues(t *testing.T) {
	armLauncher(t)
	for _, tc := range []struct {
		name     string
		typ      ast.Type
		payload  int64
		fallback int64
	}{
		{"some-false", ast.BoolType{}, 0, 1},
		{"some-zero", ast.NumberType{}, 0, 17},
		{"i8", ast.NumberType{Width: 8, Signed: true}, -7, 17},
		{"u8", ast.NumberType{Width: 8}, 249, 17},
		{"i16", ast.NumberType{Width: 16, Signed: true}, -300, 17},
		{"u16", ast.NumberType{Width: 16}, 60000, 17},
		{"i32", ast.NumberType{}, -70000, 17},
		{"u32", ast.NumberType{Width: 32}, 4000000000, 17},
		{"i64", ast.NumberType{Width: 64, Signed: true}, -(1 << 40), 17},
		{"u64", ast.NumberType{Width: 64}, 1 << 50, 17},
		{"u64-all-bits", ast.NumberType{Width: 64}, -1, 17},
	} {
		for _, optimize := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optimized-%v", tc.name, optimize), func(t *testing.T) {
				a := availabilityDiamond(tc.typ)
				out := lowerSemanticARM64(t, a.f)
				b := harnessBuilder(out)
				bad := b.constant(0)
				for present := int64(0); present <= 1; present++ {
					width, _ := armValueShape(tc.typ)
					result := b.call(out.Symbols["pilot"], width, false, b.constant(present), b.constant(tc.payload))
					want := tc.fallback
					if present != 0 {
						want = tc.payload
					}
					wrong := b.op(ssa.OpNe, 64, false, result, b.constant(want))
					bad = b.op(ssa.OpOr, 32, false, bad, wrong)
				}
				b.f.SetRet(b.b, bad)
				requireARM64Success(t, out, b.f.Name, optimize)
			})
		}
	}
}

func availabilityLoop() *Func {
	f := newFunc("pilot", ast.NumberType{})
	payload := f.addParam(f.result, ParamValue, ast.Position{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	absent := f.addState(entry, ssa.OpStateAbsent, f.result, ast.Position{})
	f.graph.SetBr(entry, header)
	present := f.addState(body, ssa.OpStatePresent, f.result, ast.Position{}, payload)
	f.graph.SetBr(body, header)
	state := f.addState(header, ssa.OpPhi, f.result, ast.Position{}, absent, present)
	has := f.addOp(header, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, state)
	f.graph.SetBrIf(header, has, exit, body)
	value := f.addOp(exit, ssa.OpStateGet, f.result, ast.Position{}, state)
	f.graph.SetRet(exit, value)
	return f
}

func TestAvailabilityLoopAndForwardReferences(t *testing.T) {
	f := availabilityLoop()
	// Lowering must not rely on producer blocks being visited before users.
	f.graph.Blocks[1], f.graph.Blocks[2] = f.graph.Blocks[2], f.graph.Blocks[1]
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}

func TestARM64TypedAvailabilityLoop(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		t.Run(fmt.Sprintf("optimized-%v", optimize), func(t *testing.T) {
			out := lowerSemanticARM64(t, availabilityLoop())
			b := harnessBuilder(out)
			result := b.call(out.Symbols["pilot"], 32, false, b.constant(41))
			b.f.SetRet(b.b, b.op(ssa.OpNe, 32, false, result, b.constant(41)))
			requireARM64Success(t, out, b.f.Name, optimize)
		})
	}
}

func TestARM64TypedAvailabilityPresenceOnly(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		t.Run(fmt.Sprintf("optimized-%v", optimize), func(t *testing.T) {
			out := lowerSemanticARM64(t, availabilityPresenceOnly().f)
			b := harnessBuilder(out)
			bad := b.constant(0)
			for present := int64(0); present <= 1; present++ {
				result := b.call(out.Symbols["pilot"], 32, false, b.constant(present), b.constant(0))
				bad = b.op(ssa.OpOr, 32, false, bad, b.op(ssa.OpNe, 32, false, result, b.constant(present)))
			}
			b.f.SetRet(b.b, bad)
			requireARM64Success(t, out, b.f.Name, optimize)
		})
	}
}
