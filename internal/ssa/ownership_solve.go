// The interprocedural half of the ownership signature table.
//
// `ParamModes` answers what one function's body does to its own
// parameters with no knowledge of anyone else. That is the leaf. A
// parameter also demands a unit when it is HANDED to a callee that
// consumes it, and whether that callee consumes it is the same question
// one level down — so the answer is a fixpoint over the call graph, not
// a walk.
//
// Roc solves the equivalent in `src/lir/arc_solve.zig` as two phases:
// parameter modes to a fixpoint with returns pessimistically owned,
// then returns marked borrowed where every returned value is a borrow
// anchored on a borrowed parameter, then re-solved. Both phases are
// here: this file is A, `ownership_returns.go` is B, and SolveOwnership
// alternates them.
//
// # What the fixpoint is worth, measured
//
// Over `examples/self_host/fern.fern` — 6514 functions, 0 lift
// failures, 10272 pointer parameters — the local demand alone finds
// **203** consumed parameters. Propagating it across call sites finds
// **368**, in 4 rounds. So 45% of the answer is not visible in any one
// function's body, which is the whole argument for doing this
// interprocedurally rather than per function.
//
// # This is not the certifier, and the cost note on #7786 is about that
//
// #7786 records Roc's `arc_certify.zig` at 7053 lines and a scaling
// failure where join-point summaries grow like the Bell number of the
// refcounted locals a loop carries. That is the per-path unit accounting,
// which summarises SETS of locals at every join. This pass does not: its
// lattice is two points per parameter and it only ever moves up, so it
// terminates in at most one round per parameter and each round is linear
// in the ops. The expensive thing is still expensive; this is not it.
package ssa

import (
	"sort"

	"github.com/jakechampion/lang/internal/ir"
)

// ParamOwnership is what a function's ABI requires of an argument.
type ParamOwnership int

const (
	// Borrowed: the callee does not take a unit. The caller keeps its
	// reference and releases it itself. Fern's default, and what an
	// argument to an indirect call is under every ownership model —
	// `rc_analysis.go` reaches the same conclusion from the other
	// side, since no caller-side retain is emitted at a call whose
	// callee has no name.
	Borrowed ParamOwnership = iota
	// Consumed: the callee takes a unit, so a caller that wants to
	// keep using the value has to retain before the call.
	Consumed
)

func (o ParamOwnership) String() string {
	if o == Consumed {
		return "consumed"
	}
	return "borrowed"
}

// Signature is one function's ownership contract as the solver sees it.
type Signature struct {
	// Params is per SSA parameter, in order.
	Params []ParamOwnership

	// Pointer marks the parameters reference counting can apply to at
	// all. A scalar is reported Borrowed and means nothing by it.
	Pointer []bool

	// ReturnBorrowed is true when every value the function returns is
	// a borrow of its own parameters rather than a unit of its own —
	// an accessor handing back a field of its receiver, a threading
	// helper handing back the state it was given.
	//
	// ReturnBorrowedFrom names WHICH parameter positions, and that is
	// the part a plain flag would lose. `q := g(p)` where g returns
	// its borrowed parameter makes q another name for p, and a later
	// release of q is a release of p; only the positions let
	// `aliasesOf` follow the call. See ownership_returns.go.
	//
	// False is the safe default and the starting point: a return
	// nothing proves is a borrow stays owned, so a release of the
	// result is attributed to the call rather than to a parameter.
	ReturnBorrowed     bool
	ReturnBorrowedFrom []int

	// ReturnOwned is true when every value the function returns carries
	// a unit of its own, so a caller holds one and must release it.
	//
	// The positive half of the same question, and it needed
	// `classifyValue`'s "owned" to be split first: that answer was a
	// union of "allocates" and "not understood", and only the second
	// may block a proof. Merging them is why 4492 functions had no
	// verdict rather than the right one.
	//
	// ReturnOwned and ReturnBorrowed are mutually exclusive; false on
	// both is "nothing was proved", which is the safe default in both
	// directions and is what a caller must treat an unplaceable result
	// as.
	ReturnOwned bool
}

// Solution is the whole-program answer plus what it could not see.
type Solution struct {
	Sigs map[string]Signature

	// Rounds is how many fixpoint iterations it took to settle. One
	// means nothing propagated across a call at all.
	Rounds int

	// Calls is every call site in the solved set, and Opaque is how
	// many of them have no ownership answer: an indirect or dynamic
	// call, a name that is in neither the solved set nor rcsigs.go,
	// or a helper rcsigs.go records as moving counts in a shape one
	// operand effect cannot express. Each is treated as borrowing
	// every argument.
	//
	// It is reported rather than hidden because it is the number that
	// says how much of the program the answer actually covers, and
	// because treating an unknown callee as borrowing is the
	// ASSUMPTION, not a derivation: a callee that really consumes and
	// is counted here reads as Borrowed and is wrong in the unsafe
	// direction. Nothing acts on this pass yet, so today that is a
	// reporting error; it has to be closed before anything lowers
	// from it.
	Calls  int
	Opaque int

	// OpaqueCallees names the distinct callees behind Opaque, so the
	// gap can be read rather than guessed at.
	//
	// Over the self-host compiler it is 11831 of 663854 call sites
	// (1.78%) across 26 names, and they fall into three groups:
	//
	//   - 21 language builtins the lowering emits by name — the Map
	//     methods, `strbuf_*`, `args` / `env` / `read_file` and the
	//     rest of the platform surface. They have no Fern body and no
	//     entry in rcsigs.go, which is a runtime table for the
	//     helpers rather than for the builtins. A second table is the
	//     obvious way to close this, and it would close most of the
	//     1.78%.
	//   - 4 runtime helpers rcsigs.go records as unmodelled:
	//     `__alloc_reuse` and the `__fern_arr_push_grow` family.
	//   - indirect calls, which are opaque by construction and are the
	//     one group nothing can close.
	OpaqueCallees []string
}

// SolveOwnership takes the parameter modes of every function to a
// fixpoint over the call graph.
//
// The input is the lifted form of a whole program keyed by name, which
// is the program context the solve needs and which `LiftFromIR` — one
// function at a time — cannot supply on its own. Build it by lifting
// each `ir.Func` and keying by `Func.Name`; a function missing from the
// map is an opaque callee, counted as such.
//
// Run it on the UNOPTIMISED lift, for the reason at the top of
// ownership.go: `Optimize` synthesises ops with no IR origin, and an
// answer produced here has to map back.
func SolveOwnership(funcs map[string]*Func) Solution {
	names := make([]string, 0, len(funcs))
	for n := range funcs {
		names = append(names, n)
	}
	// Sorted rather than ranged: the fixpoint's RESULT does not depend
	// on visit order — the lattice is monotone — but Rounds does, and a
	// count that changes run to run is not a number anyone can hold a
	// regression test against.
	sort.Strings(names)

	sol := Solution{Sigs: make(map[string]Signature, len(funcs))}
	uses := make(map[string]*Uses, len(funcs))
	aliases := make(map[string][][]Value, len(funcs))
	for _, n := range names {
		f := funcs[n]
		u := BuildUses(f)
		uses[n] = u
		sig := Signature{
			Params:  make([]ParamOwnership, len(f.Params)),
			Pointer: make([]bool, len(f.Params)),
		}
		as := make([][]Value, len(f.Params))
		for i, p := range f.Params {
			if i < len(f.ParamAddrs) {
				sig.Pointer[i] = f.ParamAddrs[i]
			}
			as[i] = aliasesOf(f, u, p)
		}
		sol.Sigs[n] = sig
		aliases[n] = as
	}

	for {
		sol.Rounds++
		changed := false
		for _, n := range names {
			f, sig := funcs[n], sol.Sigs[n]
			for i := range f.Params {
				if !sig.Pointer[i] || sig.Params[i] == Consumed {
					continue
				}
				if demandsUnit(uses[n], aliases[n][i], sol.Sigs) {
					sig.Params[i] = Consumed
					changed = true
				}
			}
			sol.Sigs[n] = sig
		}
		// Phase B: settle which returns hand back a borrow. A return
		// that flips changes what the aliases mean — the result of a
		// call is now another name for what was passed in — so the
		// alias sets are rebuilt and phase A runs again. Both lattices
		// only move one way, so the pair terminates.
		if solveReturns(funcs, names, sol.Sigs) {
			for _, n := range names {
				f := funcs[n]
				for i, p := range f.Params {
					aliases[n][i] = aliasesOfWithReturns(f, uses[n], p, sol.Sigs)
				}
			}
			changed = true
		}
		if !changed {
			break
		}
	}

	// Coverage is counted once over every call site rather than during
	// the walk: a site is opaque whether or not a parameter happens to
	// reach it, and a per-parameter tally would count the same site
	// once per argument.
	seen := map[string]bool{}
	for _, n := range names {
		for _, b := range funcs[n].Blocks {
			for _, o := range b.Ops {
				callee, opaque, ok := calleeOwnership(o, sol.Sigs)
				if !ok {
					continue
				}
				sol.Calls++
				if opaque {
					sol.Opaque++
					if !seen[callee] {
						seen[callee] = true
						sol.OpaqueCallees = append(sol.OpaqueCallees, callee)
					}
				}
			}
		}
	}
	sort.Strings(sol.OpaqueCallees)
	return sol
}

// demandsUnit reports whether the body takes an ownership unit from the
// caller for the parameter whose value-and-aliases are vs.
//
// Two ways it can:
//
//   - it RELEASES the value without a matching retain. Not "releases":
//     over the self-host compiler 760 of the 921 borrowed parameters
//     that are released are also retained, and a body that retains a
//     borrowed parameter for a local use and releases it when done
//     demands nothing. Reading the release alone would call all 921
//     consumed and be wrong about 760.
//   - it PASSES the value to a callee that consumes that argument. That
//     is the interprocedural edge, and it is why this is a fixpoint:
//     the callee's answer is the same question one level down.
//
// Retain and release are counted over the whole body rather than per
// path. A retain on one arm and a release on another reads as balanced
// here and is not; separating them needs per-path accounting, which is
// the certifier (#7782 slice 3) rather than this.
//
// The value has to REACH the callee as an argument, directly or through
// a pass-through alias. A parameter stored into a struct that is then
// passed on is not followed: that is ownership travelling through
// memory, the same gap RCSite.LiveAfter documents, and closing it is a
// different analysis rather than a bigger version of this one.
//
// # Where it disagrees with the declaration, and why
//
// Over the self-host compiler, 8 parameters declared `own` come back
// Borrowed. None is a solver bug and the split is instructive:
//
//   - 4 are balanced retain/release pairs, and 1 is retained and never
//     released. `computeConsumedParams` in `internal/ir` promotes a
//     THREADED parameter callee-internally and lowerFunc emits one
//     entry retain to pay for the reassignment's overwrite release —
//     and its own doc says "the call ABI is unchanged (the caller still
//     passes the arg borrowed)". So the body really is balanced, and
//     Borrowed is the right answer to the question this pass asks.
//     `own` on such a parameter answers a different one.
//   - 3 touch their parameter only through an unmodelled helper —
//     `ssa__merge_names` threads its `own a: string[]` through
//     `__fern_arr_push_grow_move_ptr`, and the two arm64 GAS emitters
//     rebuild their struct parameter through `__alloc_reuse`. The
//     release is real and the table cannot see it, so it reads
//     Borrowed. This is what the unmodelled bucket costs, priced at
//     three parameters.
func demandsUnit(uses *Uses, vs []Value, sigs map[string]Signature) bool {
	retained, released := false, false
	for _, v := range vs {
		for _, u := range uses.Of(v) {
			o := u.Op
			if o == nil {
				continue
			}
			if ops := rcSig(o); len(ops) > 0 {
				for _, ro := range ops {
					if ro.Value.ID != v.ID {
						continue
					}
					switch ro.Arg.Effect {
					case ir.RcRetain:
						retained = true
					case ir.RcRelease, ir.RcMove:
						released = true
					}
				}
				continue
			}
			// A call to a function with a signature: the argument
			// position this value lands in decides. Everything else —
			// an indirect or dynamic call, an unknown name, a helper
			// whose count movement rcsigs.go cannot express — borrows,
			// and calleeOwnership counts it as the assumption it is.
			if o.Kind != OpCall {
				continue
			}
			if callee, known := sigs[ir.CodegenAlias(o.Str)]; known {
				if u.Index < len(callee.Params) && callee.Params[u.Index] == Consumed {
					return true
				}
			}
		}
	}
	return released && !retained
}

// calleeOwnership classifies one op as a call site and says whether its
// callee's effect on the arguments is known. ok is false for an op that
// is not a call at all.
//
// The listing name for an indirect or dynamic call is its kind: there
// is no callee name to give. For a helper rcsigs.go records as
// unmodelled the reason travels with the name, because "opaque" and
// "opaque, and here is exactly why" are different amounts of help.
func calleeOwnership(o *Op, sigs map[string]Signature) (callee string, opaque, ok bool) {
	switch o.Kind {
	case OpCallIndirect:
		// Fern emits no caller-side retain at a call that dispatches
		// on a value, which is what makes every parameter of an
		// address-taken function borrowed under any ownership model.
		// So borrowing is the right answer here, and it is still
		// counted: it is an ABI fact rather than something derived
		// from the callee.
		return "<indirect call>", true, true
	case OpCallDyn:
		return "<dyn dispatch>", true, true
	case OpCall:
	default:
		return "", false, false
	}
	if name := ir.CodegenAlias(o.Str); name != o.Str {
		if _, known := sigs[name]; known {
			return name, false, true
		}
	}
	if _, known := sigs[o.Str]; known {
		return o.Str, false, true
	}
	if _, isRc := ir.RcHelperSig(o.Str); isRc {
		return o.Str, false, true
	}
	if reason, unmodelled := ir.RcHelperUnmodelled(o.Str); unmodelled {
		return o.Str + " (" + reason + ")", true, true
	}
	if ir.RcHelperClassified(o.Str) {
		// Classified as moving no count on any operand.
		return o.Str, false, true
	}
	return o.Str, true, true
}
