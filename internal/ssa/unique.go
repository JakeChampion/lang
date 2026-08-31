// Uniqueness inference over the lifted form (#7787): where is a
// runtime `__fern_rc_is_unique` guard provably going to answer 1?
//
// This file answers the question and nothing else — no IR is rewritten.
// #7787's own history is why: four instrument errors are recorded on
// that issue, and the tree carries two mutually incompatible headroom
// figures for the same population ("0 conservatively elidable" in
// docs/rc-log/2026-08-30-ownership-signature-table.md, "352 of 2041"
// quoted on the tracker with no committed derivation). A transform
// built on an unreproducible number would repeat the mistake, so the
// measurement comes first, as a gate with its denominators logged.
//
// # What the guard actually checks
//
// The runtime guard is four legs, not one: non-null, above the
// low-address guard page, not a static-sentinel header, and rc == 1.
// "Sole-owned" in the SSA sense discharges only the last leg, so the
// proof here accepts nothing weaker than a producer documented to hand
// back a LIVE rc=1 header (`ir.RcResultOwned`): that origin discharges
// all four at once. A consumed parameter is precisely where "the caller
// handed over a unit" and "the count is 1" diverge, and `OpAlloc` on
// the analysis path is the raw bump allocator with no header at all —
// both are refused, by class.
package ssa

import "github.com/jakechampion/lang/internal/ir"

// GuardSite is one `__fern_rc_is_unique` call together with the
// verdict of the sole-ownership proof at it.
type GuardSite struct {
	Site   RCSite
	Proven bool

	// Reason names the refusal class when Proven is false, so a census
	// can histogram what the proof is short of rather than report one
	// opaque count. Stable strings, not prose.
	Reason string
}

// SoleOwnedGuards reports every `__fern_rc_is_unique` site in f with a
// proof verdict: Proven means the guard must answer 1 at runtime, on
// every path, so the check and its shared arm are dead code.
//
// The proof, per site, over the value's whole rename family (the root
// plus every interior address and rc pass-through that denotes the
// same object):
//
//  1. the root's unit origin is Fresh, and its defining op is a call
//     the result axis classifies RcResultOwned — born rc=1, live
//     header, real heap;
//  2. the guard's block is not in a CFG cycle, so nothing between the
//     birth and the guard can run twice (the same condition
//     ir.PruneZeroSlotGuards holds, for the same reason);
//  3. no family member feeds a phi — after a join the object answers
//     to the phi's name, and which unit the phi holds is a per-path
//     question this proof does not attempt;
//  4. every use of every family member other than the guard itself is
//     strictly AFTER the guard (the guard dominates it). Between the
//     birth and the guard there is then no retain, no store, no call
//     and no escape of any kind — nothing that could move the count
//     off 1 or hand out a borrow.
//
// Condition 4 is deliberately much stronger than "no retain before the
// guard": a store or an unknown callee could add an owner the retain
// scan cannot see, and refusing every pre-guard use closes the whole
// class at once. What that strictness costs is exactly what the census
// histograms.
func SoleOwnedGuards(f *Func, u Units, sigs map[string]Signature) []GuardSite {
	var out []GuardSite
	uses := BuildUses(f)
	reach := reachableBlocks(f)
	dom := BuildDomTree(f)
	defs := defSites(f)

	for _, site := range RCSites(f) {
		if site.Helper != "__fern_rc_is_unique" {
			continue
		}
		proven, reason := soleOwnedAt(f, site, u, uses, reach, dom, defs, sigs)
		out = append(out, GuardSite{Site: site, Proven: proven, Reason: reason})
	}
	return out
}

// defSite locates a value's defining op.
type defSite struct {
	block *Block
	op    *Op
}

func defSites(f *Func) map[int32]defSite {
	out := map[int32]defSite{}
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Result.IsValid() {
				out[o.Result.ID] = defSite{block: b, op: o}
			}
		}
	}
	return out
}

func soleOwnedAt(f *Func, site RCSite, u Units, uses *Uses,
	reach map[*Block]map[*Block]bool, dom *DomTree, defs map[int32]defSite,
	sigs map[string]Signature) (bool, string) {
	if !site.Mapped {
		// An answer that cannot be applied is not worth proving.
		return false, "unmapped"
	}
	root := u.Root(site.Operand)
	if o := u.Origin(root); o != UnitFresh {
		return false, "origin:" + o.String()
	}
	d, ok := defs[root.ID]
	if !ok {
		return false, "no-def"
	}

	// Three producer classes, only the first of which discharges all
	// four legs of the runtime guard. A solver-proven ReturnOwned
	// callee is refused but counted apart: `ReturnOwned` proves the
	// caller holds a unit, not that the count is 1 or that the value
	// can never be null — widening onto it needs its own argument, and
	// the census needs to know what that argument would buy first.
	rc1 := false
	if d.op.Kind == OpCall {
		if r, known := ir.RcHelperResult(d.op.Str); known && r == ir.RcResultOwned {
			rc1 = true
		}
	}
	if !rc1 {
		if d.op.Kind == OpCall {
			if sig, known := sigs[ir.CodegenAlias(d.op.Str)]; known && sig.ReturnOwned {
				if rest, _ := pathConditionsHold(f, site, u, uses, reach, dom, root); rest {
					return false, "owned-callee-otherwise-proven"
				}
				return false, "owned-callee-producer"
			}
		}
		// OpAlloc is the raw bump allocator on this path — fresh, but
		// headerless, so the guard's sentinel leg is not discharged.
		return false, "fresh-not-rc1-producer"
	}

	if hold, reason := pathConditionsHold(f, site, u, uses, reach, dom, root); !hold {
		return false, reason
	}
	return true, ""
}

// pathConditionsHold checks conditions 2-4: no cycle at the guard, no
// phi feed, every other use of the rename family strictly after the
// guard.
func pathConditionsHold(f *Func, site RCSite, u Units, uses *Uses,
	reach map[*Block]map[*Block]bool, dom *DomTree, root Value) (bool, string) {
	if reach[site.Block][site.Block] {
		return false, "in-loop"
	}
	guardIdx := indexOfOp(site.Block, site.Op)
	for _, v := range renameFamily(f, u, root) {
		for _, use := range uses.Of(v) {
			if use.Op == site.Op {
				continue
			}
			if use.Op != nil && use.Op.Kind == OpPhi {
				return false, "feeds-phi"
			}
			if !guardDominatesUse(dom, site.Block, guardIdx, use) {
				return false, "use-before-guard"
			}
		}
	}
	return true, ""
}

// renameFamily returns root plus every value that resolves to it
// through Units' rename chain — rc pass-through results and interior
// addresses. Broader than aliasesOf, which follows the helpers only.
func renameFamily(f *Func, u Units, root Value) []Value {
	out := []Value{root}
	for _, p := range f.Params {
		if p.ID != root.ID && u.Root(p).ID == root.ID {
			out = append(out, p)
		}
	}
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Result.IsValid() && o.Result.ID != root.ID && u.Root(o.Result).ID == root.ID {
				out = append(out, o.Result)
			}
		}
	}
	return out
}

// guardDominatesUse reports whether the use can only execute after the
// guard: same block at a later position (a terminator counts as after
// every op), or a block the guard's block dominates.
func guardDominatesUse(dom *DomTree, gb *Block, guardIdx int, use UseSite) bool {
	if use.Block == gb {
		return indexOfOp(gb, use.Op) > guardIdx
	}
	return dom.Dominates(gb, use.Block)
}
