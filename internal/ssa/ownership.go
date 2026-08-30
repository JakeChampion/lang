// Ownership queries over the lifted form.
//
// This is the first piece of `docs/SSA-CUTOVER-PLAN.md`'s analysis-only
// route: reason about reference counting where values have names,
// def-use edges and a CFG, then map the answers back to the op stream
// through `Op.SrcOp`. Nothing here emits or rewrites anything — it
// answers questions, and the callers decide what to do with them.
//
// # Why the questions are asked here and not in internal/ir
//
// The flat IR has no CFG to ask, so the same questions get answered by
// hand there: `rc_analysis.go` says of its own last-use test that it is
// "TEXTUAL, and a name declared OUTSIDE the loop is read again by the
// next iteration, so its textually-last occurrence is not its last
// dynamic use" — which is #7544, and which needed a bespoke walk over
// While / Loop / For / ForEach bodies to repair. Measured over the
// corpus, 69% of reference-count operations act on a value whose last
// use is in a DIFFERENT block, so that is the majority case rather than
// a corner of it.
//
// # Run this on the UNOPTIMISED lift
//
// `Optimize` synthesises ops with no IR origin, so provenance stops
// being total the moment it runs and an answer produced here could not
// be mapped back. Lift, ask, map back; optimise only on the codegen
// path, which is untouched by any of this.
package ssa

import "github.com/jakechampion/lang/internal/ir"

// rcSig answers what a call op does to a reference count, or reports
// that nothing is known about the callee.
//
// The table is `internal/ir/rcsigs.go` — the runtime half of #7786's
// ownership signature table — which names each helper's effect and
// which argument carries the counted pointer. Keeping it there rather
// than here is what lets `internal/ir`'s own verifier read the same
// record, and what makes a new runtime helper fail a completeness test
// until it is classified.
//
// The lift turns both spellings of a helper call — the flat IR's
// dedicated OpRcInc / OpRcDec / OpRcIsUnique and the self-host's plain
// calls — into an OpCall carrying the helper's name, so one table
// covers both compilers.
//
// Two things still move counts and still report nothing here, so a
// count derived from this reads LOW rather than wrong. The
// `__fern_arr_push_grow` family and `__alloc_reuse` move them in a
// shape one operand effect cannot express, and rcsigs.go says so
// entry by entry. User `Drop` finalizers carry whatever name
// `userDropFnName` resolved, which no rule can recognise — they are
// defined functions, and their ownership is the interprocedural
// fixpoint's answer rather than a table's.
func rcSig(o *Op) (ir.RcSig, Value, bool) {
	if o == nil || o.Kind != OpCall {
		return ir.RcSig{}, Value{}, false
	}
	sig, ok := ir.RcHelperSig(o.Str)
	if !ok || sig.Operand >= len(o.Args) {
		return ir.RcSig{}, Value{}, false
	}
	return sig, o.Args[sig.Operand], true
}

// RCSite is one reference-count operation and what the CFG says about
// the value it acts on.
type RCSite struct {
	Block  *Block
	Op     *Op
	Helper string

	// Effect is what the call does to the caller's unit on Operand.
	// A release and a retain read the same in the op stream — both are
	// a call on a pointer — and only the signature separates them.
	Effect  ir.RcEffect
	Operand Value

	// LiveAfter is true when some OTHER use of Operand is reachable from
	// this op — so the value is still wanted and this is not its last
	// use. A release here would be premature; a retain here is holding
	// the value for that later use.
	//
	// It is a question about SSA uses, and that is NOT the same question
	// as "is this reference count balanced". A retain can hand its count
	// to a data structure rather than to a later use, and then the value
	// is legitimately dead afterwards. `__map_own_key` in core/map.fern
	// is the worked example:
	//
	//	__fern_rc_inc(__load_ptr(boxed));   // result discarded
	//	return boxed;
	//
	// The retain bumps the string's heap buffer while the function
	// returns the CELL. Nothing uses the retained pointer again, and
	// nothing is wrong: ownership leaves through the return, because the
	// buffer is reachable from `boxed` through memory.
	//
	// That shape is the whole population, not a corner of it. Lifting
	// `examples/self_host/fern.fern` — 6514 functions, 0 lift failures —
	// gives 58137 retains of which 36879 are dead afterwards, and every
	// single one of the 36879 DISCARDS ITS RESULT. None hands the
	// pointer to a later use that liveness then failed to see; they all
	// pass ownership through memory. The proportion is a property of
	// the program rather than of the analysis: the conformance corpus
	// reports 51 dead out of 31930.
	//
	// So "dead afterwards" is a filter, not a verdict. Separating a
	// genuine unbalanced retain from this shape needs reachability
	// THROUGH MEMORY — whether the retained pointer is reachable from a
	// value that escapes — which this analysis does not have.
	//
	// The other direction reads the same way. Of the 149818 releases
	// the self-host reports as live afterwards, 133352 are
	// `__fern_box_free`, which is the last op of a generated drop before
	// it returns the pointer it just freed — the uniform result shape
	// every drop has. Also correct. Telling a premature release from a flat dec on a
	// value with other owners needs to know the count, which is the
	// callee ownership signature table (#7786).
	//
	// Both directions land in the same place: this pass classifies
	// STRUCTURE. It says where a value is still wanted, which is the
	// question the flat IR cannot ask without a bespoke walk. It does
	// not say whether the reference counting around it is right, and it
	// should not be read as if it did.
	LiveAfter bool

	// LaterUses are the uses that make LiveAfter true — every use of the
	// operand OR of one of its pass-through aliases that this op can
	// reach. Empty exactly when LiveAfter is false.
	//
	// It is here because the boolean alone invites a wrong follow-up
	// question. A caller wanting to know WHAT the later use is reaches
	// for uses.Of(Operand), gets nothing, and concludes the site has no
	// later use — because the uses are of the rc helper's RESULT, which
	// is the same object under another name. That mistake was made twice
	// within an hour of this analysis being written, both times by its
	// author. Handing back the sites the answer came from removes the
	// opportunity.
	LaterUses []UseSite

	// SrcOp is the source op index this site maps back to, and Mapped
	// says whether it has one. An unmapped site is one whose answer
	// could not be applied, which is why the provenance is total by
	// construction (see Op.SrcOp).
	SrcOp  int
	Mapped bool
}

// RCSites reports every reference-count operation in f, with the
// liveness of its operand at that point.
//
// Which argument the operand is comes from the callee's signature, not
// from a fixed position: the lift preserves argument order, and
// `RcSig.Operand` names the index.
func RCSites(f *Func) []RCSite {
	uses := BuildUses(f)
	reach := reachableBlocks(f)
	var out []RCSite
	for _, b := range f.Blocks {
		for oi, o := range b.Ops {
			sig, operand, ok := rcSig(o)
			if !ok {
				continue
			}
			src, mapped := o.SourceOp()
			later := usesAfter(uses, reach, b, oi, aliasesOf(f, uses, operand), o)
			out = append(out, RCSite{
				Block:     b,
				Op:        o,
				Helper:    o.Str,
				Effect:    sig.Effect,
				Operand:   operand,
				LaterUses: later,
				LiveAfter: len(later) > 0,
				SrcOp:     src,
				Mapped:    mapped,
			})
		}
	}
	return out
}

// aliasesOf returns v together with every value that is v under another
// name.
//
// Most reference-count helpers hand back the pointer they were given,
// so the lift gives their result a fresh SSA value that denotes the
// same object. Code after the call reads the RESULT, not the operand —
// so asking only about uses of the operand reports almost every retain
// as having no later use, which is an artifact of the representation
// rather than a fact about the program. Following the pass-through
// closure is what makes the question mean what it says.
//
// `RcSig.ResultIsOperand` is what says which helpers belong in the
// closure. `__fern_rc_is_unique` does not — it returns a boolean — and
// neither do the copy-on-write moves, whose result is a different
// object whenever the receiver was shared.
func aliasesOf(f *Func, uses *Uses, v Value) []Value {
	out := []Value{v}
	seen := map[int32]bool{v.ID: true}
	for i := 0; i < len(out); i++ {
		for _, u := range uses.Of(out[i]) {
			o := u.Op
			if o == nil || o.Result.ID == 0 {
				continue
			}
			sig, operand, ok := rcSig(o)
			if !ok || !sig.ResultIsOperand {
				continue
			}
			if operand.ID != out[i].ID || seen[o.Result.ID] {
				continue
			}
			seen[o.Result.ID] = true
			out = append(out, o.Result)
		}
	}
	return out
}

// usesAfter returns every use of vs (a value and its pass-through
// aliases) other than `self` that can be reached from position oi in
// block b.
//
// Two ways a use can be after this point: later in the same block, or
// anywhere in a block reachable from this one — INCLUDING b itself,
// which is what makes a loop work. A use textually earlier in a loop
// body is reached again on the next iteration, and that is exactly the
// case a textual scan gets wrong.
func usesAfter(uses *Uses, reach map[*Block]map[*Block]bool, b *Block, oi int, vs []Value, self *Op) []UseSite {
	var out []UseSite
	for _, v := range vs {
		for _, u := range uses.Of(v) {
			if u.Op == self {
				continue
			}
			if u.Block == b && !reach[b][b] {
				// Straight-line block: only a later position counts.
				if indexOfOp(b, u.Op) > oi {
					out = append(out, u)
				}
				continue
			}
			if reach[b][u.Block] {
				out = append(out, u)
			}
		}
	}
	return out
}

// indexOfOp returns the position of op in b, or -1 for a terminator
// operand use (Op is nil), which always counts as after the ops.
func indexOfOp(b *Block, op *Op) int {
	if op == nil {
		return len(b.Ops)
	}
	for i, o := range b.Ops {
		if o == op {
			return i
		}
	}
	return -1
}

// reachableBlocks builds, for every block, the set of blocks reachable
// from it along CFG edges. A block reaches ITSELF only through a cycle,
// which is the property the loop case above rests on.
func reachableBlocks(f *Func) map[*Block]map[*Block]bool {
	out := make(map[*Block]map[*Block]bool, len(f.Blocks))
	for _, b := range f.Blocks {
		seen := map[*Block]bool{}
		stack := append([]*Block(nil), b.Succs()...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == nil || seen[n] {
				continue
			}
			seen[n] = true
			stack = append(stack, n.Succs()...)
		}
		out[b] = seen
	}
	return out
}

// ParamMode is the LOCAL evidence about one parameter's ownership: what
// this function's own body does to it, with no knowledge of its callers
// or callees.
//
// It is the leaf of the interprocedural fixpoint #7786 needs, not the
// answer. Roc's equivalent (src/lir/arc_solve.zig) starts every
// parameter borrowed and flips it to owned when an occurrence demands a
// unit, then propagates through call sites to a fixpoint. This reports
// the demand; the propagation is not here.
type ParamMode struct {
	Index int
	Value Value

	// Pointer is true when the parameter is a heap address, so reference
	// counting can apply to it at all. A scalar parameter cannot be
	// released and is reported for completeness rather than interest.
	Pointer bool

	// Released is true when the body releases the parameter — a demand
	// for a unit, and the local evidence that it is CONSUMED rather than
	// borrowed. Retained is the mirror.
	//
	// Both are reported rather than collapsed into one verdict, and the
	// measurement says why. Over `examples/self_host/fern.fern` — 6514
	// lifted functions, 10272 pointer parameters — 921 are declared
	// borrowed and yet released, and 760 of those are ALSO retained.
	// They are balanced pairs: the body retains a borrowed parameter for
	// a local use and releases it when done, demanding no unit from the
	// caller. Reading Released alone would call all 921 consumed, and be
	// wrong about 760 of them.
	//
	// The rest of the split, same basis: 9289 borrowed parameters are
	// never released at all, 48 declared `own` are released, and 14
	// declared `own` are not. So the overwhelming majority of pointer
	// parameters are plain borrows, which is the shape a fixpoint should
	// start from.
	//
	// So the rule a fixpoint should start from is not "released" but
	// "released without a matching retain". Roc's phrasing is that a
	// parameter flips to owned when an occurrence DEMANDS A UNIT, and a
	// balanced pair demands nothing.
	//
	// The 161 that are released without a retain are the interesting
	// bucket. __query_pair in std/url.fern is the worked example: it
	// threads a Map parameter (`m = m.insert(...)`) and so takes the
	// reassignment's overwrite dec. Map is deliberately outside
	// computeConsumedParams' promotion — "consumedDropWired keeps Map /
	// slice / unwired shapes out (their deep drop is incomplete)" — so
	// it gets no compensating entry retain, which is the shape
	// rc_analysis.go's own comment calls an over-release. It is not one:
	// 50 rounds of query_parse over a five-pair query returns 0 from
	// __rc_underflow_count and leaves leakcheck at 950 allocs / 950
	// frees / 0 live bytes. Recorded because the reasoning says bug and
	// the measurement says no, and the measurement wins.
	Released bool
	Retained bool
}

// ParamModes reports the local ownership evidence for each of f's
// parameters, in parameter order.
//
// "Acts on" follows the pass-through closure: most reference-count
// helpers hand back the pointer they were given, so a release of an
// inc's result is a release of the parameter.
func ParamModes(f *Func) []ParamMode {
	out := make([]ParamMode, 0, len(f.Params))
	uses := BuildUses(f)
	for i, p := range f.Params {
		m := ParamMode{Index: i, Value: p}
		if i < len(f.ParamAddrs) {
			m.Pointer = f.ParamAddrs[i]
		}
		for _, v := range aliasesOf(f, uses, p) {
			for _, u := range uses.Of(v) {
				sig, operand, ok := rcSig(u.Op)
				if !ok || operand.ID != v.ID {
					continue
				}
				switch sig.Effect {
				case ir.RcRelease, ir.RcMove:
					m.Released = true
				case ir.RcRetain:
					m.Retained = true
				}
			}
		}
		out = append(out, m)
	}
	return out
}
