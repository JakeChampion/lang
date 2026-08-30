package ssa

import "github.com/jakechampion/lang/internal/ir"

// LiftProgram lifts a whole program to SSA for ANALYSIS.
//
// #7786 named the absence of this "one API blocker": `LiftFromIR` takes
// a bare `*ir.Func`, so nothing on the lift side could see call sites
// across a program, and every caller that needed the whole-program view
// — `SolveOwnership`, the ownership differential, the certifier — wrote
// the same loop. Three copies of it existed and they did not agree: one
// passed call shapes and two did not, which silently changes what a
// two-word call lifts to. The two ownership copies now call this; the
// third is `join_width_test.go`, kept deliberately — see below.
//
// Two things it does that a hand-written loop keeps getting wrong.
//
// It builds `ir.CallShapes` first. Without them the lift falls back to
// the IR argument count and assumes one result, which is only right for
// one-word arguments and single-result callees (#7803).
//
// It runs `ResolveWidths` on a 64-bit lowering, and NOT `Optimize`. The
// distinction matters and it is the reason this is a separate entry
// point from the codegen path: `Optimize` synthesises ops with no IR
// origin, so `Op.SrcOp` provenance stops being total and an answer
// produced here could not be mapped back to the op stream.
// `ResolveWidths` only stamps widths and address-ness onto existing
// ops, creates nothing, and is what makes `Op.Addr` mean anything at
// all — on a bare lift it is false almost everywhere, since only six
// lift sites set it and the rest comes from this pass. It is skipped at
// ptrW 4 for the reason its own doc gives: a wasm32 pointer IS an i32,
// so widening one would be wrong rather than merely unnecessary.
//
// Lift failures are returned rather than dropped: a silently skipped
// function is a hole in whatever the caller then measures.
//
// `join_width_test.go` keeps its own loop deliberately. Its measurement
// was taken on the bare lift, where `Op.Addr` is nearly empty, and
// moving it here would silently re-base a landed figure rather than
// re-measure it.
func LiftProgram(p *ir.Program) (map[string]*Func, []LiftFailure) {
	shapes := ir.NewCallShapes(p)
	funcs := make(map[string]*Func, len(p.Funcs))
	var failed []LiftFailure
	ptrW := 0
	for _, fn := range p.Funcs {
		if fn.PtrW > ptrW {
			ptrW = fn.PtrW
		}
		sf, err := LiftFromIRWith(fn, shapes)
		if err != nil {
			failed = append(failed, LiftFailure{Func: fn.Name, Err: err})
			continue
		}
		funcs[fn.Name] = sf
	}
	if ptrW == 8 {
		ResolveWidths(funcs)
	}
	return funcs, failed
}

// LiftFailure is one function the lift could not model, and why.
type LiftFailure struct {
	Func string
	Err  error
}
