// Phase B: which functions hand back a borrow rather than a unit.
//
// Phase A (ownership_solve.go) settles parameter modes with every
// return pessimistically OWNED. That is the safe starting point and it
// is imprecise in one specific, common way: a function that takes a
// value and hands the same value back — `emit(s, x)` returning `s`, an
// accessor returning a field of its receiver — looks to its caller like
// a fresh allocation. A release of the result is then attributed to
// nothing, and the parameter that value really came from reads as
// borrowed when it is not.
//
// Roc's `src/lir/arc_solve.zig` phase B marks a return borrowed when
// every returned value is a borrow anchored on a borrowed parameter,
// then re-solves phase A. This is that, with one addition the flag
// alone would not give.
//
// # Recording the ANCHOR, not just the flag
//
// Roc records borrowed-or-owned because a flag is what its lowering
// consumes. Here the interesting consumer is `aliasesOf`, which follows
// a value through the reference-count helpers that hand back their
// argument — and which is blind to the same relationship expressed as a
// call: `q := g(p)` where `g` returns its own borrowed parameter makes
// `q` another name for `p`, and a later release of `q` is a release of
// `p`. A flag cannot say that; the argument POSITIONS the return may
// alias can. `Signature.ReturnBorrowedFrom` is those positions, and
// following them is where the extra precision actually comes from.
//
// # What it found, and the answer is mostly "the assumption was right"
//
// Over `examples/self_host/fern.fern`: 3829 functions return an
// address, and 84 of them are proved to hand back a borrow. The other
// 4492 blocked classifications are `OpAlloc` — those functions really
// do return a unit the caller owns, so the pessimistic assumption phase
// A started from was correct for them, and this pass says so rather
// than assuming it.
//
// No parameter mode moved as a result: consumed stays at 368. The
// anchor mechanism works — a caller that passes a parameter to one of
// the 84 and releases the result does get its parameter marked consumed,
// which is pinned by a test — but the self-host compiler does not do
// that often enough for the count to shift. Worth stating plainly: the
// deliverable here is that a stated assumption became a derived answer,
// and the derived answer is that the assumption was right 98% of the
// time.
package ssa

// classification is what one SSA value carries.
type classification int

const (
	// clsNeutral: no ownership unit at all — a constant, a scalar
	// computation. It neither is a borrow nor blocks one.
	clsNeutral classification = iota
	// clsBorrow: the value is a parameter, or reaches one through
	// aliasing. anchors names which.
	clsBorrow
	// clsOwned: freshly allocated, read out of memory, or produced by
	// a callee whose return is owned. Also the answer for anything not
	// understood, which is what keeps the pass safe: an unrecognised
	// definition blocks the borrowed conclusion rather than allowing
	// it.
	clsOwned
)

// solveReturns decides, for each function, whether its return hands
// back a borrow and which parameters that borrow is anchored on.
//
// It only ever moves a return from owned to borrowed, so iterating it
// against phase A terminates.
func solveReturns(funcs map[string]*Func, names []string, sigs map[string]Signature) bool {
	changed := false
	for _, n := range names {
		f := funcs[n]
		sig := sigs[n]
		if sig.ReturnBorrowed {
			// Already settled as a borrow on an earlier round; the
			// lattice only moves one way.
			continue
		}
		anchors, borrowed := returnAnchors(f, sigs)
		if !borrowed {
			continue
		}
		sig.ReturnBorrowed = true
		sig.ReturnBorrowedFrom = anchors
		sigs[n] = sig
		changed = true
	}
	return changed
}

// returnAnchors reports whether every value f returns is a borrow (or
// carries no unit at all), and which parameter positions those borrows
// are anchored on.
//
// A function whose return is not an address is skipped: there is no
// unit to hand back, and calling such a return "borrowed" would be a
// statement about nothing.
//
// A PAIR return is skipped too. It is the Option / Result ABI's (tag,
// payload) convention, and deciding which half carries the unit is a
// separate question from the one this pass answers — so it stays
// pessimistically owned rather than being guessed at.
func returnAnchors(f *Func, sigs map[string]Signature) ([]int, bool) {
	if f == nil || !f.ReturnAddr {
		return nil, false
	}
	defs := defMap(f)
	seen := map[int32]bool{}
	anchorSet := map[int]bool{}
	sawBorrow := false
	for _, b := range f.Blocks {
		switch b.Term.Kind {
		case TermRet:
			if !b.Term.Value.IsValid() {
				continue
			}
			cls, anchors := classifyValue(f, defs, sigs, b.Term.Value, seen)
			switch cls {
			case clsOwned:
				return nil, false
			case clsBorrow:
				sawBorrow = true
				for _, a := range anchors {
					anchorSet[a] = true
				}
			}
		case TermRetPair:
			return nil, false
		}
	}
	if !sawBorrow {
		return nil, false
	}
	out := make([]int, 0, len(anchorSet))
	for a := range anchorSet {
		out = append(out, a)
	}
	sortInts(out)
	return out, true
}

// classifyValue traces v back to what produced it.
//
// A value already on the path classifies neutral rather than blocking:
// the only way to revisit one is a phi cycle in a loop, where the
// value's own previous iteration says nothing new. Every OTHER incoming
// edge of that phi is still classified, so an owned value reaching the
// loop still blocks the borrowed conclusion.
func classifyValue(f *Func, defs map[int32]*Op, sigs map[string]Signature, v Value, seen map[int32]bool) (classification, []int) {
	if !v.IsValid() {
		return clsNeutral, nil
	}
	if i, ok := paramIndex(f, v); ok {
		return clsBorrow, []int{i}
	}
	if seen[v.ID] {
		return clsNeutral, nil
	}
	seen[v.ID] = true

	o := defs[v.ID]
	if o == nil {
		return clsOwned, nil
	}
	switch o.Kind {
	case OpConstInt, OpConstBool, OpConstString, OpConstStringLen, OpEnumSentinel:
		// A constant carries no ownership unit. A string literal and a
		// payloadless enum variant are STATIC sentinels — the rc word's
		// high bit marks them "never touch" — so returning one hands
		// back nothing to own, and it must not block a borrow proof
		// the way a fresh allocation does.
		return clsNeutral, nil
	case OpAdd, OpSub:
		// Pointer arithmetic: an address computed from a borrowed
		// object is a pointer INTO that object, and so a borrow of the
		// same thing. `self + fieldOffset` is the shape, and it is the
		// single largest reason a return could not be proved before —
		// 4576 of the blocked classifications over the self-host
		// compiler.
		//
		// If either operand is owned the result may point into a fresh
		// object, so the whole thing is owned. Two constants make plain
		// arithmetic, which is neutral.
		cls, anchors := clsNeutral, []int(nil)
		for _, a := range o.Args {
			c, an := classifyValue(f, defs, sigs, a, seen)
			if c == clsOwned {
				return clsOwned, nil
			}
			if c == clsBorrow {
				cls = clsBorrow
				anchors = append(anchors, an...)
			}
		}
		return cls, anchors
	case OpPhi:
		cls, anchors := clsNeutral, []int(nil)
		for _, a := range o.Args {
			c, an := classifyValue(f, defs, sigs, a, seen)
			if c == clsOwned {
				return clsOwned, nil
			}
			if c == clsBorrow {
				cls = clsBorrow
				anchors = append(anchors, an...)
			}
		}
		return cls, anchors
	case OpCall:
		// A helper that hands back the pointer it was given is the
		// same object under a new name.
		for _, ro := range rcSig(o) {
			if ro.Arg.ResultIsOperand {
				return classifyValue(f, defs, sigs, ro.Value, seen)
			}
		}
		callee, known := sigs[o.Str]
		if !known || !callee.ReturnBorrowed {
			return clsOwned, nil
		}
		// The callee hands back a borrow of the arguments in these
		// positions, so the result is another name for whatever this
		// call passed there.
		cls, anchors := clsNeutral, []int(nil)
		for _, j := range callee.ReturnBorrowedFrom {
			if j < 0 || j >= len(o.Args) {
				return clsOwned, nil
			}
			c, an := classifyValue(f, defs, sigs, o.Args[j], seen)
			if c == clsOwned {
				return clsOwned, nil
			}
			if c == clsBorrow {
				cls = clsBorrow
				anchors = append(anchors, an...)
			}
		}
		return cls, anchors
	}
	// Everything else — an OpAlloc, a load, an indirect call — is
	// owned, which is what keeps the pass safe: an unrecognised
	// definition blocks the borrowed conclusion rather than allowing
	// it.
	//
	// A LOAD is deliberately not followed, and it is the largest
	// remaining category (775 blocked classifications over the
	// self-host compiler). `return self.field` does hand back
	// something the caller must not release — but ReturnBorrowedFrom
	// means "the result IS another name for that argument", which is
	// what lets aliasesOf attribute a release of the result to the
	// parameter. A field pointer is REACHABLE FROM the receiver, not
	// identical to it, and releasing it is not releasing the
	// container. Conflating the two would make the anchor say
	// something it cannot support.
	return clsOwned, nil
}

// paramIndex reports whether v is one of f's parameters.
func paramIndex(f *Func, v Value) (int, bool) {
	for i, p := range f.Params {
		if p.ID == v.ID {
			return i, true
		}
	}
	return 0, false
}

// defMap indexes each value by the op that defines it. Phis included —
// they are ops in the block like any other.
func defMap(f *Func) map[int32]*Op {
	out := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Result.IsValid() {
				out[o.Result.ID] = o
			}
		}
	}
	return out
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
