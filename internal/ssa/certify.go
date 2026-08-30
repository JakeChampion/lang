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

	poisoned := map[int32]bool{}
	// A forward dataflow to a fixpoint. The lattice is four points and
	// only ever moves toward ownMaybe, so it settles; the round cap is a
	// backstop against a malformed CFG rather than an expected exit.
	for changed, round := true, 0; changed && round < 64; round++ {
		changed = false
		for bi, b := range f.Blocks {
			cur := blockEntryState(f, b, idx, out, entry, units)
			applyBlock(b, cur, units, sigs, poisoned)
			if !sameState(cur, out[bi]) {
				changed = true
				out[bi] = cur
			}
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

// applyBlock runs the block's ops over the state.
func applyBlock(b *Block, cur map[int32]ownState, units Units, sigs map[string]Signature, poisoned map[int32]bool) {
	for _, o := range b.Ops {
		if o.Kind == OpPhi {
			continue // resolved on entry, against the edges
		}
		if o.Result.IsValid() {
			switch units.origin[o.Result.ID] {
			case UnitFresh, UnitTransferred:
				cur[o.Result.ID] = ownHolds
			case UnitBorrowed, UnitNone:
				cur[o.Result.ID] = ownGone
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
				cur[units.Root(o.Args[n-1]).ID] = ownGone
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
