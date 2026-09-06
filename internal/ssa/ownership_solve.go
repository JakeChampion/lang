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
	blockIndex := make(map[string]map[*Block]int, len(funcs))
	aliases := make(map[string][][]Value, len(funcs))
	for _, n := range names {
		f := funcs[n]
		u := BuildUses(f)
		uses[n] = u
		idx := make(map[*Block]int, len(f.Blocks))
		for i, b := range f.Blocks {
			idx[b] = i
		}
		blockIndex[n] = idx
		sig := Signature{
			Params:  make([]ParamOwnership, len(f.Params)),
			Pointer: make([]bool, len(f.Params)),
		}
		as := make([][]Value, len(f.Params))
		for i, p := range f.Params {
			if i < len(f.ParamAddrs) {
				sig.Pointer[i] = f.ParamAddrs[i]
			}
			as[i] = unitCarriersOf(f, u, p, nil)
		}
		sol.Sigs[n] = sig
		aliases[n] = as
	}

	for {
		sol.Rounds++
		changed := false
		for _, n := range names {
			f, sig := funcs[n], sol.Sigs[n]
			var units Units
			placed := false
			for i := range f.Params {
				if !sig.Pointer[i] || sig.Params[i] == Consumed {
					continue
				}
				if !placed {
					units, placed = UnitsOf(f, sol.Sigs), true
				}
				if demandsUnit(f, blockIndex[n], uses[n], units, aliases[n][i], sol.Sigs) {
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
					aliases[n][i] = unitCarriersOf(f, uses[n], p, sol.Sigs)
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
// The question is asked PER PATH: along every path from entry to a
// return, the body's retains of the carriers are counted against what
// it releases, hands to a consuming callee, stores into memory, or
// captures into a closure. A path that gives away more than
// it retained ends holding less than it was handed, so the caller must
// have handed a unit over — the parameter is consumed. A body that
// retains a borrowed value for a local use and releases it when done is
// balanced on every path and demands nothing: over the self-host
// compiler 760 of the 921 borrowed parameters that are released are
// also retained, and reading the release alone would call all 921
// consumed.
//
// Counting over the whole body instead — "released and never retained"
// — reads a retain on one arm and a release on another as balanced, and
// under the owned-by-default model that is the common shape rather than
// a corner: `return a` on an owned parameter is a transfer inc beside
// the exit sweep's drop, and the arm that rebuilds instead just drops.
// The whole-body reading called such a parameter Borrowed, so every
// caller that had MOVED its argument in read as still holding it.
//
// The discharge set is the certifier's (`applyBlock`) less the return.
// A returned parameter carries no rc op of its own: the typed lowering
// pairs it with a transfer inc (borrowed) or an inc beside the exit
// sweep's drop (owned), and the untyped `usize` helpers under `core/map`
// hand back the raw word — `__map_get_or_impl` returns its fallback on
// the miss path and nothing on the hit path. Counting the return as a
// discharge read that fallback as consumed and then held on the hit
// path, in every fixture the runtime proves clean. The inc and drop
// beside a return are the evidence; the return is not.
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
func demandsUnit(f *Func, idx map[*Block]int, uses *Uses, units Units, vs []Value, sigs map[string]Signature) bool {
	carrier := make(map[int32]bool, len(vs))
	for _, v := range vs {
		carrier[v.ID] = true
	}
	// Per-block net movement, gathered from the carriers' use sites
	// rather than by scanning every op: an op that reads two carriers
	// is visited once per carrier, so its delta is split per operand.
	// Most parameters move no count anywhere, and a body with no
	// net-release block has no path below zero, so the dataflow runs
	// only when some block gives more away than it takes.
	delta := make([]int, len(f.Blocks))
	negative := false
	for _, v := range vs {
		for _, u := range uses.Of(v) {
			if u.Op == nil {
				continue
			}
			if bi, ok := idx[u.Block]; ok {
				delta[bi] += unitDelta(u.Op, u.Index, v, sigs)
				negative = negative || delta[bi] < 0
			}
		}
	}
	// A phi in the carrier set holds the parameter's unit only along
	// the edges a carrier feeds it. Along an edge that brings another
	// value instead — the reassigned accumulator, `acc = push(acc, x)`
	// merging with the untouched `acc` — the phi's later release spends
	// that value's unit, not the parameter's, so the path is credited
	// with it on the way in. A borrow brings nothing: a TRMC'd walk
	// loads the tail out of the cell it then releases, and that release
	// is the parameter's. Anything else is credited, an unplaced call
	// result included — the `__fern_arr_push_grow` family is unmodelled
	// and every threaded array accumulator flows through it, and
	// reading its result as unit-less called all of them consumed.
	credit := map[int][]int{}
	var rebuilt []Value
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Kind != OpPhi || !carrier[o.Result.ID] {
				continue
			}
			bi := idx[b]
			for i, a := range o.Args {
				if i >= len(b.Preds) || carrier[a.ID] {
					continue
				}
				if og := units.Origin(a); og == UnitBorrowed || og == UnitNone {
					continue
				}
				if credit[bi] == nil {
					credit[bi] = make([]int, len(b.Preds))
					rebuilt = append(rebuilt, o.Result)
				}
				credit[bi][i]++
			}
		}
	}
	// The credit pays for the release the phi's unit meets later. When
	// the phi is RETURNED instead — `d = d.with(i, v); return d`, one arm
	// keeping the parameter and the other continuing with a consuming
	// callee's fresh result — there is no release to pay for: the unit
	// leaves through the return, which the discharge set excludes, so
	// the consumption on the rebuilt arm would read as balanced and the
	// kept arm as a borrow of a value the caller no longer holds. The
	// return of such a carrier is the unit leaving.
	if len(rebuilt) > 0 {
		returned := map[int32]bool{}
		for _, r := range rebuilt {
			for _, a := range unitCarriersOf(f, uses, r, sigs) {
				returned[a.ID] = true
			}
		}
		for i, b := range f.Blocks {
			if b.Term.Kind == TermRet && b.Term.Value.IsValid() && returned[b.Term.Value.ID] {
				delta[i]--
				negative = negative || delta[i] < 0
			}
		}
	}
	if !negative {
		return false
	}

	// Forward dataflow to a fixpoint. A block's entry balance is the
	// minimum over its predecessors' exits — the path that gave the most
	// away is the one that decides — so the state only ever moves down,
	// and the clamp at -balanceCap makes a loop with a net release settle
	// rather than count forever.
	//
	// Worklist-driven, as in `certify.go`: only a block whose
	// predecessor changed can change, and a full sweep per pass made
	// this quadratic on the self-host parser's largest functions.
	const unreached = int(^uint(0) >> 1)
	in := make([]int, len(f.Blocks))
	out := make([]int, len(f.Blocks))
	queued := make([]bool, len(f.Blocks))
	for i := range in {
		in[i], out[i] = unreached, unreached
	}
	entry := idx[f.Entry]
	in[entry] = 0
	queued[entry] = true
	for pending := true; pending; {
		pending = false
		for i, b := range f.Blocks {
			if !queued[i] {
				continue
			}
			queued[i] = false
			if b != f.Entry {
				in[i] = unreached
				for j, pb := range b.Preds {
					pi, ok := idx[pb]
					if !ok || out[pi] == unreached {
						continue
					}
					v := out[pi]
					if c := credit[i]; c != nil {
						v += c[j]
					}
					if v < in[i] {
						in[i] = v
					}
				}
			}
			if in[i] == unreached {
				continue
			}
			v := clampBalance(in[i] + delta[i])
			if out[i] == v {
				continue
			}
			out[i] = v
			for _, sb := range b.Succs() {
				if si, ok := idx[sb]; ok {
					queued[si] = true
					pending = true
				}
			}
		}
	}
	for i, b := range f.Blocks {
		if (b.Term.Kind == TermRet || b.Term.Kind == TermRetPair) && out[i] != unreached && out[i] < 0 {
			return true
		}
	}
	return false
}

// balanceCap bounds the per-path balance in both directions. Below zero
// the sign is all that is read, and above it a retain can only be
// cancelled by as many releases, so anything past it is the same answer.
const balanceCap = 8

func clampBalance(n int) int {
	if n < -balanceCap {
		return -balanceCap
	}
	if n > balanceCap {
		return balanceCap
	}
	return n
}

// unitDelta is what one op does to the balance through the carrier `v`
// it reads at Args[i]: +1 for a retain, -1 for a release, move, hand-off
// to a consuming callee, store into memory, or closure capture.
func unitDelta(o *Op, i int, v Value, sigs map[string]Signature) int {
	if ops := rcSig(o); len(ops) > 0 {
		d := 0
		for _, ro := range ops {
			if ro.Value.ID != v.ID || ro.Arg.Index != i {
				continue
			}
			switch ro.Arg.Effect {
			case ir.RcRetain:
				d++
			case ir.RcRelease, ir.RcMove:
				d--
			}
		}
		return d
	}
	switch o.Kind {
	case OpCall:
		// Everything without a signature — an unknown name, a helper
		// whose count movement rcsigs.go cannot express — borrows, and
		// calleeOwnership counts it as the assumption it is.
		if callee, known := sigs[ir.CodegenAlias(o.Str)]; known &&
			i < len(callee.Params) && callee.Params[i] == Consumed {
			return -1
		}
	case OpStore, OpStoreF, OpStore32:
		// Ownership passes into the container; the value operand is the
		// last one.
		if i == len(o.Args)-1 {
			return -1
		}
	case OpMakeClosure, OpMakeEnv, OpBoxDyn:
		return -1
	}
	return 0
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
