// The leak half of the certifier, rebuilt on a named unit-holder set.
//
// `docs/rc-log/2026-08-30-certifier-transfer-taxonomy.md` and its
// successor record a probe that walked the lifted form with one state
// per pointer value and reported 63,070 leaks over the self-host
// compiler, then seven refinements that between them removed 0.8% of
// them. The conclusion was that the walk "is wrong in many small ways
// rather than one large one, and filtering does not converge on it. It
// wants rebuilding against the oracle from a correct unit-holder set."
//
// The correct unit-holder set is `units.go`. This is the walk on top of
// it, and the two differences from the probe are structural rather than
// another refinement:
//
//   - A value's state at its definition comes from `UnitsOf`, so a
//     static sentinel, a field read, an interior address and a borrowed
//     parameter never enter the walk holding anything. The probe marked
//     every one of them as holding a unit.
//   - Ownership is tracked per RENAME ROOT, not per value, so an inc's
//     result and the pointer it was handed are one thing and a later
//     release lands on the right object.
//
// # Fail-soft, and countable
//
// Where the walk cannot say, it says so. A disagreement between
// predecessors about whether a unit is still held is `ownMaybe` and is
// never reported; an opaque callee poisons its pointer arguments the
// same way; a function whose shape is outside the model is skipped and
// counted. That is the stance `verifyrc.go` takes and it is what lets a
// corpus gate hold a floor under coverage rather than under a total.
//
// The consequence is that a clean run is NOT a proof there are no
// leaks. It is a proof that nothing the walk can see is one, over the
// part of the program it reports having seen.
package ssa

import "github.com/jakechampion/lang/internal/ir"

// ownState is what the walk believes about one rename root.
type ownState uint8

const (
	ownAbsent ownState = iota // no definition reached this point
	ownHolds                  // the function holds a unit
	ownGone                   // the unit was released or handed on
	ownMaybe                  // predecessors disagree, or an opaque call touched it
)

// Leak is one value the walk says still holds an ownership unit where
// the function returns.
type Leak struct {
	Func   string
	Value  Value
	Origin UnitOrigin

	// Kind is the op that defined the value. The localiser the oracle
	// note asked for: a function name alone does not say which shape
	// produced a finding, and "one level down, the op kinds" is what
	// separated `alloc` from `enum_sentinel` in the first breakdown.
	Kind OpKind

	// Block is the returning block the value was still held at.
	Block *Block

	// SrcOp is the source op index the value's definition maps back to,
	// and Mapped says whether it has one.
	SrcOp  int
	Mapped bool
}

// CertifyReport is what one function's walk saw, alongside what it did
// not.
type CertifyReport struct {
	Leaks []Leak

	// Modelled is false when the function's shape is outside the walk
	// and nothing about it was checked. Skipped names why.
	Modelled bool
	Skipped  string

	// Unplaced is how many of the function's values `UnitsOf` could not
	// classify — a call result with no ownership answer. Reported so a
	// caller can hold the coverage floor rather than read a low leak
	// count as a clean bill.
	Unplaced int

	// Passes is how many sweeps the dataflow needed to settle.
	//
	// Reported because it used to be capped at 64, which silently
	// truncated the answer on exactly the functions least able to
	// afford it: over the self-host compiler three functions need more
	// than that and one needs 206, and the cap was hiding 41 findings.
	// A caller that wants to know the walk ran to completion can see
	// that it did.
	Passes int

	// Poisoned is how many roots an opaque callee or a disagreeing join
	// forced to ownMaybe. The other half of the coverage figure: these
	// are values the walk saw and then stopped being able to answer
	// for.
	Poisoned int
}

// Certify walks f and reports the values it can prove are still held
// where the function returns.
//
// sigs is the solved whole-program answer from SolveOwnership. Without
// it every parameter reads borrowed and every call result unknown, so
// the walk is sound and reports almost nothing.
func Certify(f *Func, sigs map[string]Signature) CertifyReport {
	rep := CertifyReport{Modelled: true}
	if f == nil || f.Entry == nil {
		rep.Modelled, rep.Skipped = false, "no entry block"
		return rep
	}
	units := UnitsOf(f, sigs)
	rep.Unplaced = units.Unplaced()

	idx := make(map[*Block]int, len(f.Blocks))
	for i, b := range f.Blocks {
		idx[b] = i
	}
	defs := defMap(f)

	entry := map[int32]ownState{}
	for _, p := range f.Params {
		if units.Origin(p) == UnitTransferred {
			entry[p.ID] = ownHolds
		}
	}

	out := make([]map[int32]ownState, len(f.Blocks))
	for i := range out {
		out[i] = map[int32]ownState{}
	}

	// Which roots leave each block by flowing into a successor's phi.
	feeds := phiFeeds(f, idx, units)

	poisoned := map[int32]bool{}

	// A forward dataflow to a fixpoint, driven by a worklist. The
	// lattice is four points and only ever moves toward ownMaybe, so it
	// settles.
	//
	// The worklist is not a micro-optimisation. Re-scanning every block
	// each round makes the cost (rounds x blocks x state), and the state
	// is per-value, so one large function dominates a whole program:
	// over the self-host compiler `parser__parse_stmt_at` alone took
	// 1m41s of a 4m48s total. Only a block whose predecessors changed
	// can change, so only those are requeued.
	// Blocks are in reverse post-order from the lift, so a round-robin
	// sweep in index order converges in few passes. The queued set is
	// what stops an unchanged block being re-walked: cost here is
	// (passes x queued blocks x state), and the state is per-value, so
	// on one large function the difference is minutes.
	//
	// Ordered rather than FIFO deliberately. A FIFO worklist over a
	// loop-heavy CFG re-processes blocks far more often than an RPO
	// sweep — measured at 8m51s against 4m48s over the self-host
	// compiler — because it keeps revisiting a loop body before its
	// header has settled.
	//
	// There is NO round cap. The lattice only moves toward ownMaybe so
	// this terminates on its own, and a cap would silently truncate the
	// answer on exactly the largest functions.
	queued := make([]bool, len(f.Blocks))
	for i := range queued {
		queued[i] = true
	}
	for {
		rep.Passes++
		any := false
		for bi, b := range f.Blocks {
			if !queued[bi] {
				continue
			}
			queued[bi] = false
			cur := blockEntryState(f, b, idx, out, entry, units)
			applyBlock(b, cur, units, sigs, poisoned, feeds[bi])
			if sameState(cur, out[bi]) {
				continue
			}
			out[bi] = cur
			any = true
			for _, sb := range b.Succs() {
				if si, ok := idx[sb]; ok {
					queued[si] = true
				}
			}
		}
		if !any {
			break
		}
	}
	rep.Poisoned = len(poisoned)

	for bi, b := range f.Blocks {
		if b.Term.Kind != TermRet && b.Term.Kind != TermRetPair {
			continue
		}
		st := out[bi]
		returned := map[int32]bool{}
		if b.Term.Value.IsValid() {
			returned[units.Root(b.Term.Value).ID] = true
		}
		if b.Term.Value2.IsValid() {
			returned[units.Root(b.Term.Value2).ID] = true
		}
		for id, s := range st {
			if s != ownHolds || returned[id] || poisoned[id] {
				continue
			}
			o := units.origin[id]
			// Only a unit this function is known to have acquired can
			// be leaked by it. A merged or unknown origin is exactly
			// the case the walk is not entitled to an opinion on.
			if o != UnitFresh && o != UnitTransferred {
				continue
			}
			src, mapped, kind := 0, false, OpInvalid
			if d, ok := defs[id]; ok {
				src, mapped = d.SourceOp()
				kind = d.Kind
			}
			rep.Leaks = append(rep.Leaks, Leak{
				Func: f.Name, Value: Value{ID: id}, Origin: o, Kind: kind,
				Block: b, SrcOp: src, Mapped: mapped,
			})
		}
	}
	return rep
}

// blockEntryState merges the predecessors' exit states, then resolves
// the block's phis against the edge each incoming value arrived on.
//
// The phi half is the path-sensitivity the flat walk could not have.
// `docs/rc-log/2026-08-30-join-width.md` measured the set a join has to
// relate — the values its predecessors DISAGREE about — at p50 = 0 and
// p99 = 10, which is what makes doing this per value affordable.
func blockEntryState(f *Func, b *Block, idx map[*Block]int, out []map[int32]ownState,
	entry map[int32]ownState, units Units) map[int32]ownState {

	cur := map[int32]ownState{}
	if b == f.Entry {
		for id, s := range entry {
			cur[id] = s
		}
	}
	for _, pb := range b.Preds {
		for id, s := range out[idx[pb]] {
			cur[id] = meetOwn(cur[id], s)
		}
	}
	for _, o := range b.Ops {
		if o.Kind != OpPhi || !o.Result.IsValid() {
			continue
		}
		if units.origin[o.Result.ID] != UnitMerged {
			continue
		}
		st := ownAbsent
		for i, a := range o.Args {
			if i >= len(b.Preds) {
				break
			}
			st = meetOwn(st, out[idx[b.Preds[i]]][units.Root(a).ID])
		}
		cur[o.Result.ID] = st
	}
	return cur
}

// meetOwn combines two claims about one root. Absent yields to anything;
// two claims that disagree become ownMaybe and stay there.
func meetOwn(a, b ownState) ownState {
	switch {
	case a == b:
		return a
	case a == ownAbsent:
		return b
	case b == ownAbsent:
		return a
	default:
		return ownMaybe
	}
}

// phiFeeds reports, per block, the roots that flow out of it into a
// successor's phi.
//
// A value that feeds a phi hands its unit to the phi: everything after
// the join names the PHI's result, so that is where a later release or
// return lands, and a walk keyed on the incoming value never sees its
// own disposal. `docs/rc-log/2026-08-30-ownership-signature-table.md`
// records the same shape from the other side — "`aliasesOf` does not
// cross phis, and that is the real limitation" — and says the answer is
// per-path accounting rather than a wider alias closure. This is that
// accounting: the transfer is attributed to the EDGE, so an incoming
// value only loses its unit on the path that actually reaches the join.
//
// It is the same rule as the store and the closure capture. A value can
// still be leaked on a path that never reaches the phi, and this makes
// the walk blind to that — under-reporting, which is the direction it
// already fails in.
func phiFeeds(f *Func, idx map[*Block]int, units Units) [][]int32 {
	out := make([][]int32, len(f.Blocks))
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Kind != OpPhi || !o.Result.IsValid() {
				continue
			}
			for i, a := range o.Args {
				if i >= len(b.Preds) {
					break
				}
				root := units.Root(a).ID
				if root == units.Root(o.Result).ID {
					continue
				}
				pi := idx[b.Preds[i]]
				out[pi] = append(out[pi], root)
			}
		}
	}
	return out
}

// applyBlock runs the block's ops over the state.
func applyBlock(b *Block, cur map[int32]ownState, units Units, sigs map[string]Signature, poisoned map[int32]bool, feeds []int32) {
	for _, o := range b.Ops {
		if o.Kind == OpPhi {
			continue // resolved on entry, against the edges
		}
		if o.Result.IsValid() {
			switch units.origin[o.Result.ID] {
			case UnitFresh, UnitTransferred:
				cur[o.Result.ID] = ownHolds
			case UnitBorrowed:
				// A borrow can still become a holder — a retain on one
				// puts a unit in this function's hands — so it has to
				// carry an explicit "holds nothing" rather than being
				// absent: absent MEETS as the other side's claim, and a
				// value retained on one path and not another would then
				// merge to holding rather than to maybe.
				cur[o.Result.ID] = ownGone
			case UnitNone:
				// Reference counting cannot apply to it and nothing can
				// promote it — only a borrow is ever retained into
				// holding, and only fresh or transferred is ever
				// reported. Keeping it out of the state is what makes
				// this walk affordable: it is the bulk of every
				// function's values, and the dataflow cost is a
				// per-round map copy proportional to the state's size.
			case UnitUnknown:
				// A call whose result nobody classifies. Not a leak
				// candidate and not evidence of anything.
				cur[o.Result.ID] = ownMaybe
				poisoned[o.Result.ID] = true
			}
		}
		switch o.Kind {
		case OpMakeClosure, OpMakeEnv, OpBoxDyn:
			// Every argument is a capture, written into the block this
			// op allocates. The unit moves with it: the env block's
			// drop is what releases the capture afterwards, so the
			// local that built it does not hold one any more.
			for _, a := range o.Args {
				cur[units.Root(a).ID] = ownGone
			}
		case OpStore, OpStoreF, OpStore32:
			// Ownership passes into the container. The store's value
			// operand is the last one; a fresh box written into a
			// struct that outlives the block is not this function's to
			// release any more.
			if n := len(o.Args); n > 0 {
				if root := units.Root(o.Args[n-1]).ID; units.origin[root] != UnitNone {
					cur[root] = ownGone
				}
			}
		case OpCall:
			applyCall(o, cur, units, sigs, poisoned)
		case OpCallIndirect, OpCallDyn:
			poisonArgs(o, cur, units, poisoned)
		}
	}
	// The unit leaves through the return.
	//
	// A pair return discharges BOTH halves. `ownership_returns.go`
	// refuses this shape because it has to PROVE the returned value is
	// a borrow, and one `Addr` bit across two results cannot support
	// that proof. This walk carries the opposite obligation — it must
	// not over-report — and discharging a scalar tag's root is a no-op,
	// so taking both is sound here and skipping the function is not:
	// the Option/Result ABI is over half the corpus, and skipping it
	// cost more coverage than every other unmodelled shape together.
	if b.Term.Kind == TermRet || b.Term.Kind == TermRetPair {
		if b.Term.Value.IsValid() {
			cur[units.Root(b.Term.Value).ID] = ownGone
		}
		if b.Term.Value2.IsValid() {
			cur[units.Root(b.Term.Value2).ID] = ownGone
		}
	}
	// And the unit handed to a successor's phi leaves with it.
	for _, root := range feeds {
		if cur[root] == ownHolds {
			cur[root] = ownGone
		}
	}
}

func applyCall(o *Op, cur map[int32]ownState, units Units, sigs map[string]Signature, poisoned map[int32]bool) {
	if sig, ok := ir.RcHelperSig(o.Str); ok {
		for _, a := range sig.Args {
			if a.Index < 0 || a.Index >= len(o.Args) {
				continue
			}
			root := units.Root(o.Args[a.Index]).ID
			switch a.Effect {
			case ir.RcRelease, ir.RcMove:
				cur[root] = ownGone
			case ir.RcRetain:
				// A retain on a borrow puts a unit in this function's
				// hands that nothing else will release for it.
				if units.origin[root] == UnitBorrowed && cur[root] != ownMaybe {
					cur[root] = ownHolds
				}
			}
		}
		return
	}
	if _, unmodelled := ir.RcHelperUnmodelled(o.Str); unmodelled {
		poisonArgs(o, cur, units, poisoned)
		return
	}
	callee, known := sigs[ir.CodegenAlias(o.Str)]
	if !known {
		poisonArgs(o, cur, units, poisoned)
		return
	}
	for i, a := range o.Args {
		if i >= len(callee.Params) {
			break
		}
		if callee.Params[i] == Consumed {
			cur[units.Root(a).ID] = ownGone
		}
	}
}

// poisonArgs marks every pointer argument of an unanswerable call as
// ownMaybe. An indirect call, a dyn dispatch and the four helpers
// `rcsigs.go` records as moving counts in a shape one operand effect
// cannot express all reach here: what they did to their arguments is
// not knowable, so nothing downstream may be reported.
func poisonArgs(o *Op, cur map[int32]ownState, units Units, poisoned map[int32]bool) {
	for _, a := range o.Args {
		root := units.Root(a).ID
		if units.origin[root] == UnitNone {
			continue
		}
		cur[root] = ownMaybe
		poisoned[root] = true
	}
}

func sameState(a, b map[int32]ownState) bool {
	if len(a) != len(b) {
		return false
	}
	for id, s := range a {
		if b[id] != s {
			return false
		}
	}
	return true
}
