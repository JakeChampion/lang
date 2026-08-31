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
// # What it found
//
// Over `examples/self_host/fern.fern`: 3873 functions return an
// address. **1494 are proved to return an OWNED unit and 127 a
// borrow**; the remaining 2252 get no verdict, and a load is the
// largest reason.
//
// The owned half was zero until `clsOwned` was split. That answer used
// to mean "allocates, OR reads out of memory, OR is not understood",
// and the three cannot share a verdict: only the last is entitled to
// block a proof, and merging them meant a function that plainly
// returned what it allocated could not be told from one the pass had
// never heard of. Splitting it also improved the BORROW half, 84 to
// 127, because a call to a helper whose result axis says immortal now
// classifies neutral instead of blocking.
//
// No parameter mode moved either time: consumed stays at 368. The
// anchor mechanism works — a caller that passes a parameter to a
// borrow-returning callee and releases the result does get its
// parameter marked consumed, which is pinned by a test — but the
// self-host compiler does not do that often enough for the count to
// shift.
package ssa

import "github.com/jakechampion/lang/internal/ir"

// classification is what one SSA value carries.
type classification int

const (
	// clsNeutral: no ownership unit at all — a constant, a scalar
	// computation. It neither is a borrow nor blocks one.
	clsNeutral classification = iota
	// clsBorrow: the value is a parameter, or reaches one through
	// aliasing. anchors names which.
	clsBorrow
	// clsFresh: the value provably carries a unit of its own — an
	// allocation, or a callee whose result the signature table or this
	// pass says is owned.
	//
	// Split out from clsUnknown so the pass can say something POSITIVE.
	// Both block the borrowed conclusion, which is all the pass needed
	// while its only verdict was "borrowed or nothing"; only this one
	// supports ReturnOwned, and merging them is why 4492 functions had
	// no answer rather than the right one.
	clsFresh
	// clsUnknown: anything this pass does not understand — a load, an
	// indirect call, a callee with no signature. It blocks BOTH
	// conclusions, which is what keeps the pass safe: an unrecognised
	// definition must never be read as a proof in either direction.
	clsUnknown
)

// solveReturns decides, for each function, what its return proves: a
// borrow of named parameters, a unit the caller must release, or
// nothing either way.
//
// Each verdict LATCHES. A round can only turn "unproven" into an
// answer — the extra information a later round carries is a callee
// signature that was unknown before — so iterating this against phase A
// terminates, and a function cannot oscillate between the two
// conclusions.
func solveReturns(funcs map[string]*Func, names []string, sigs map[string]Signature) bool {
	changed := false
	for _, n := range names {
		sig := sigs[n]
		if sig.ReturnBorrowed || sig.ReturnOwned {
			continue // settled on an earlier round
		}
		verdict, anchors := returnVerdict(funcs[n], sigs)
		switch verdict {
		case retBorrow:
			sig.ReturnBorrowed = true
			sig.ReturnBorrowedFrom = anchors
		case retOwned:
			sig.ReturnOwned = true
		default:
			continue
		}
		sigs[n] = sig
		changed = true
	}
	return changed
}

// retVerdict is what a function's returns collectively prove.
type retVerdict int

const (
	// retUnproven: nothing holds on every path.
	retUnproven retVerdict = iota
	// retBorrow: every returned value is a borrow of the anchored
	// parameters. Roc's phase B, and what this pass answered before it
	// could answer anything else.
	retBorrow
	// retOwned: every returned value carries a unit the caller must
	// release.
	retOwned
	// retNeutral: every returned value is a static sentinel or a
	// constant. There is no unit either way, which is a third real
	// answer rather than a failure to decide.
	retNeutral
)

// returnVerdict decides what f's returns prove, and — for a borrow —
// which parameters they are anchored on.
//
// A function with no address return, or with a pair return, is
// unproven. The pair is the Option / Result (tag, payload) convention
// and deciding which half carries the unit is a separate question from
// the one this pass answers.
func returnVerdict(f *Func, sigs map[string]Signature) (retVerdict, []int) {
	if f == nil || !f.ReturnAddr {
		return retUnproven, nil
	}
	defs := defMap(f)
	escaped := escapedRoots(f)
	seen := map[int32]bool{}
	anchorSet := map[int]bool{}

	merged, sawReturn := clsNeutral, false
	for _, b := range f.Blocks {
		switch b.Term.Kind {
		case TermRet:
			if !b.Term.Value.IsValid() {
				continue
			}
			sawReturn = true
			cls, anchors := classifyValue(f, defs, escaped, sigs, b.Term.Value, seen)
			switch {
			case cls == clsUnknown:
				return retUnproven, nil
			case cls == clsNeutral:
				// yields to whatever the other returns say
			case merged == clsNeutral:
				merged = cls
				for _, a := range anchors {
					anchorSet[a] = true
				}
			case merged != cls:
				// One return borrows and another allocates. Neither
				// conclusion holds on every path.
				return retUnproven, nil
			default:
				for _, a := range anchors {
					anchorSet[a] = true
				}
			}
		case TermRetPair:
			return retUnproven, nil
		}
	}
	if !sawReturn {
		return retUnproven, nil
	}
	switch merged {
	case clsBorrow:
		out := make([]int, 0, len(anchorSet))
		for a := range anchorSet {
			out = append(out, a)
		}
		sortInts(out)
		return retBorrow, out
	case clsFresh:
		return retOwned, nil
	}
	return retNeutral, nil
}

// classifyValue traces v back to what produced it.
//
// A value already on the path classifies neutral rather than blocking:
// the only way to revisit one is a phi cycle in a loop, where the
// value's own previous iteration says nothing new. Every OTHER incoming
// edge of that phi is still classified, so an owned value reaching the
// loop still blocks the borrowed conclusion.
func classifyValue(f *Func, defs map[int32]*Op, escaped map[int32]bool, sigs map[string]Signature, v Value, seen map[int32]bool) (classification, []int) {
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
		return clsUnknown, nil
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
		return mergeClassifications(f, defs, escaped, sigs, o.Args, seen)
	case OpPhi:
		return mergeClassifications(f, defs, escaped, sigs, o.Args, seen)
	case OpCall:
		// A helper that hands back the pointer it was given is the
		// same object under a new name.
		for _, ro := range rcSig(o) {
			if ro.Arg.ResultIsOperand {
				return classifyValue(f, defs, escaped, sigs, ro.Value, seen)
			}
		}
		callee, known := sigs[ir.CodegenAlias(o.Str)]
		if !known {
			// Not a function this program defines. The runtime's own
			// result axis answers for the helpers.
			if r, classified := ir.RcHelperResult(o.Str); classified {
				switch r {
				case ir.RcResultOwned, ir.RcResultRaw:
					return freshUnlessEscaped(escaped, v)
				case ir.RcResultImmortal, ir.RcResultNone:
					return clsNeutral, nil
				}
			}
			return clsUnknown, nil
		}
		if callee.ReturnOwned {
			return freshUnlessEscaped(escaped, v)
		}
		if !callee.ReturnBorrowed {
			return clsUnknown, nil
		}
		// The callee hands back a borrow of the arguments in these
		// positions, so the result is another name for whatever this
		// call passed there.
		args := make([]Value, 0, len(callee.ReturnBorrowedFrom))
		for _, j := range callee.ReturnBorrowedFrom {
			if j < 0 || j >= len(o.Args) {
				return clsUnknown, nil
			}
			args = append(args, o.Args[j])
		}
		return mergeClassifications(f, defs, escaped, sigs, args, seen)
	}
	if isAllocating(o.Kind) {
		return freshUnlessEscaped(escaped, v)
	}
	// Everything else — a load, an indirect call — is UNKNOWN, and
	// blocks both conclusions.
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
	//
	// It is not clsFresh either: a field read hands back a reference
	// the container owns, and claiming it owned would tell every caller
	// to release something it never acquired.
	return clsUnknown, nil
}

// freshUnlessEscaped is the fresh conclusion, withheld when the value
// has already handed its unit to memory.
//
// `__map_grow_keyed` is the shape: it allocates a buffer, stores it
// into the map with `__store_ptr`, and returns it as well. The store is
// what the map's ownership rests on — nothing retained the buffer — so
// the return hands back a BORROW of something the map now owns, and
// telling every caller it owns the result makes each of them account
// for a unit it never acquired.
//
// It is the mirror of the load case below, which already declines to
// call a field read owned for the same reason, and the same rule
// `certify.go` applies within a function: a store transfers.
//
// The answer is UNKNOWN rather than borrow. The value really is a
// borrow, but of the container it was stored into — reachable from a
// parameter, not identical to one — and `ReturnBorrowedFrom` can only
// name a parameter position. Refusing both conclusions is the honest
// answer and the fail-soft one.
func freshUnlessEscaped(escaped map[int32]bool, v Value) (classification, []int) {
	if escaped[v.ID] {
		return clsUnknown, nil
	}
	return clsFresh, nil
}

// escapedRoots collects the values whose unit is handed to memory
// somewhere in f — a store's value operand, or a capture written into a
// closure environment.
//
// Renames are deliberately NOT followed. Storing `__fern_rc_inc(p)`
// gives the container a unit of its own and leaves p's with the
// function, so only a store of the value ITSELF transfers.
func escapedRoots(f *Func) map[int32]bool {
	out := map[int32]bool{}
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			switch o.Kind {
			case OpMakeClosure, OpMakeEnv, OpBoxDyn:
				for _, a := range o.Args {
					out[a.ID] = true
				}
			case OpStore, OpStoreF, OpStore32, OpStore8, OpStore16:
				if n := len(o.Args); n > 0 {
					out[o.Args[n-1].ID] = true
				}
			case OpCall:
				// The raw stores the stdlib writes through. They move
				// no COUNT, which is why `rcsigs.go` calls them inert,
				// but the pointer they write is reachable from the
				// container afterwards exactly as OpStore's is.
				switch o.Str {
				case "__store_ptr", "__store_i64", "__store_i32":
					if len(o.Args) == 2 {
						out[o.Args[1].ID] = true
					}
				}
			}
		}
	}
	return out
}

// isAllocating reports the op kinds that produce a unit of their own.
//
// The same set `units.go` calls allocating, and deliberately the same
// list rather than a second reading of the question.
func isAllocating(k OpKind) bool {
	return unitAllocating(k)
}

// mergeClassifications combines the classifications of several values
// that all flow into one result — a phi's incomings, an offset's
// operands, the arguments a borrow-returning callee anchors on.
//
// Unknown dominates: one operand this pass cannot place makes the whole
// thing unplaceable. A borrow and a fresh value together are unknown
// too — the result is one or the other depending on the path, and
// neither conclusion holds on all of them. Neutral yields to anything,
// so a phi of `Some(box)` and the `None` sentinel stays fresh: a
// release of a static sentinel short-circuits on its rc word, so the
// caller may release the result whichever arm ran.
func mergeClassifications(f *Func, defs map[int32]*Op, escaped map[int32]bool, sigs map[string]Signature, vs []Value, seen map[int32]bool) (classification, []int) {
	cls, anchors := clsNeutral, []int(nil)
	for _, a := range vs {
		c, an := classifyValue(f, defs, escaped, sigs, a, seen)
		switch {
		case c == clsUnknown:
			return clsUnknown, nil
		case c == clsNeutral:
			// yields
		case cls == clsNeutral:
			cls = c
			anchors = append(anchors, an...)
		case cls != c:
			// A borrow on one path and a fresh value on another.
			return clsUnknown, nil
		case c == clsBorrow:
			anchors = append(anchors, an...)
		}
	}
	return cls, anchors
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
