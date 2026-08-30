// The verifier's operand-stack model, exposed per op.
//
// `verifyStack` already computes how many operand-stack entries every
// op leaves, under either string ABI, because that is the whole point
// of it. Nothing could read that answer per op, and one consumer badly
// needs to.
//
// `internal/ssa`'s lift maintains a second model of the same stack, and
// #7803 is what happens when the two disagree: a two-word string is one
// entry to the lift and two to the verifier, so the lift's stack runs
// short and fails at the first op that notices — which is almost never
// the op that diverged. Two attempts at fixing it by watching an
// aggregate coverage number did not converge, because the number cannot
// separate "this fix exposed an older divergence" from "this fix caused
// one".
//
// A per-op height turns that into a localisable question: run both
// models over the same function and report the FIRST index where they
// differ. That is the same two-independent-models discipline
// `verifyprovided.go` applies to helper signatures, pointed at the
// stack instead.
package ir

// StackHeights returns the operand-stack height after each op of every
// function in p, keyed by function name, plus the functions the model
// had to abandon and why.
//
// A skipped function is absent from the heights map rather than present
// with wrong numbers: the verifier bails the moment something is
// unmodelled, and a consumer comparing against a bailed function would
// be comparing against nothing.
func StackHeights(p *Program) (heights map[string][]StackAt, skipped map[string]string) {
	known := map[string]*Func{}
	for _, f := range p.Funcs {
		known[f.Name] = f
	}
	externs := map[string]*ExternFunc{}
	for _, e := range p.Externs {
		externs[e.Name] = e
	}
	heights = make(map[string][]StackAt, len(p.Funcs))
	skipped = map[string]string{}
	for _, f := range p.Funcs {
		h, bail := stackHeights(f, known, externs, p.PtrW)
		if bail != "" {
			skipped[f.Name] = bail
			continue
		}
		heights[f.Name] = h
	}
	return heights, skipped
}

// stackHeights walks one function with the verifier's own stack model,
// recording the height after each op.
//
// It deliberately shares `stackChecker.step` rather than reimplementing
// the arithmetic: a second copy of the slot rules would agree with the
// lift or the verifier by accident rather than by construction, which
// is exactly the failure this is built to expose.
func stackHeights(f *Func, known map[string]*Func, externs map[string]*ExternFunc, ptrW int) ([]StackAt, string) {
	// The ABI comes off the FUNC, not from ast.UseTwoWordStrings: that
	// consults a global the lowering sets and restores, so an arm64
	// program inspected afterwards answers one-word and the verifier
	// silently models the wrong ABI. Func.TwoWordStr is what the
	// lowering actually used.
	s := &stackChecker{f: f, known: known, externs: externs, ptrW: ptrW, twoWordStr: f.TwoWordStr}
	if erased(f.ReturnType) {
		return nil, "result type is an unresolved type parameter"
	}
	retSlots := s.typeSlots(f.ReturnType)
	s.frames = []ctrlFrame{{kind: OpInvalid, at: -1, height: 0, labelSlots: retSlots, endSlots: retSlots}}

	out := make([]StackAt, 0, len(f.Ops))
	for i, op := range f.Ops {
		s.step(i, op)
		if s.bail != "" {
			return nil, s.bail
		}
		out = append(out, StackAt{Height: len(s.stack), Reachable: !s.top().unreachable})
	}
	return out, ""
}

// StackAt is the operand-stack state after one op.
//
// Height is ABSOLUTE — the whole operand stack, not the current control
// frame's slice of it. `stackChecker.height()` is frame-relative
// because that is what its own checks want; a consumer comparing
// against another model needs the absolute number, or every `if` reads
// as a divergence.
//
// Reachable matters as much as Height: after a `return` or a `br` the
// stack is abandoned, and the two models abandon it differently — the
// verifier goes polymorphic, the lift drops the block. Comparing
// heights there compares two kinds of nothing, which is what made the
// first version of this diagnostic report 144011 false divergences at
// `return` on the ABI where the models are supposed to agree
// everywhere.
type StackAt struct {
	Height    int
	Reachable bool
}
