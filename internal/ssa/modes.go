// Ownership-mode specialisation over the lifted form (#7792): at which
// call sites would a second copy of the callee, taking a parameter in
// the other ownership mode, remove reference-count traffic?
//
// This file answers the question and rewrites nothing, for the reason
// unique.go gives about #7787: the tracker carried three measurements
// of this population, none committed, and the third had to correct the
// second. A variant policy built on an unreproducible number would
// repeat that, so the census comes first.
//
// # What a variant can and cannot remove
//
// A call site hands a value to a parameter in one of four states, and
// only two of them have anything a variant could delete:
//
//   - the caller OWNS the value and it DIES at the call, but the
//     signature BORROWS the position. The caller keeps its unit alive
//     across the call and releases afterwards. If the callee retains
//     the parameter — stores it, threads it into a container — an
//     owned variant turns that retain plus the caller's release into a
//     transfer: a pair gone. If the callee only reads it, the same
//     variant merely moves the one release from the caller into the
//     callee, and nothing is saved. Every earlier measurement counted
//     both shapes as one number; CalleeRetains is what separates them.
//   - the caller still NEEDS the value after the call, but the
//     signature CONSUMES the position. The caller retains before the
//     call and the callee releases at its exit. A borrowed variant
//     deletes both. Witnessed by the retain rather than inferred from
//     liveness, because a value threaded through a loop phi is "live"
//     at every call in the loop whether or not this call cost anything.
//
// A dying value into a consumed position is a transfer already, and a
// live value into a borrowed position is a borrow already; both are
// what the signature was solved to make cheap.
package ssa

import (
	"sort"

	"github.com/jakechampion/lang/internal/ir"
)

// CallModeSite is one pointer argument at one direct call site whose
// callee has a solved ownership signature.
type CallModeSite struct {
	Caller string
	Callee string
	// Arg is the SSA argument position, which is the callee's SSA
	// parameter position: the lift preserves argument order, and a
	// two-word value is two arguments against two parameters.
	Arg int

	// Mode is what the callee's signature asks of this position.
	Mode ParamOwnership
	// Origin is how the caller came to hold the argument.
	Origin UnitOrigin
	// Dying is true when no use of the argument — nor of anything that
	// denotes the same object, phis included — is reachable after the
	// call. A value that feeds a loop-carried phi is live, because the
	// next iteration reads it.
	Dying bool
	// CalleeRetains is true when the callee's own body retains the
	// parameter, so an owned variant would have a retain to elide.
	CalleeRetains bool
	// CallerRetains is true when the argument is the result of a retain
	// the caller performed for this call — the witnessed form of "the
	// caller still needs the value", which no liveness walk over phis
	// can give: a loop-threaded value reads as live at every call in
	// the loop, but only a retain says the caller paid for it.
	CallerRetains bool

	// SrcOp is the source op index the call maps back to, and Mapped
	// whether it has one.
	SrcOp  int
	Mapped bool
}

// The classes a census histograms. Stable strings, not prose.
const (
	// ClassOwnedVariantPair: an owned variant would delete a retain in
	// the callee and a release in the caller.
	ClassOwnedVariantPair = "owned-variant:pair"
	// ClassOwnedVariantDeferred: an owned variant would move the one
	// release from the caller into the callee and delete nothing.
	ClassOwnedVariantDeferred = "owned-variant:deferred-release"
	// ClassBorrowedVariantPair: a borrowed variant would delete the
	// caller's pre-call retain and the callee's exit release.
	ClassBorrowedVariantPair = "borrowed-variant:pair"
	// ClassOptimal: the solved mode is already the cheap one for this
	// site.
	ClassOptimal = "optimal"
)

// Class names which of the four states the site is in.
func (s CallModeSite) Class() string {
	switch {
	case s.Mode == Borrowed && s.Dying && (s.Origin == UnitFresh || s.Origin == UnitTransferred):
		if s.CalleeRetains {
			return ClassOwnedVariantPair
		}
		return ClassOwnedVariantDeferred
	case s.Mode == Consumed && s.CallerRetains:
		return ClassBorrowedVariantPair
	}
	return ClassOptimal
}

// CallModeSites reports every pointer argument at every direct call
// whose callee is in the solved set. Indirect calls, runtime helpers and
// builtins have no signature to deviate from and are not sites.
//
// Callers are visited in name order so the output is stable.
func CallModeSites(funcs map[string]*Func, sol Solution) []CallModeSite {
	names := make([]string, 0, len(funcs))
	for n := range funcs {
		names = append(names, n)
	}
	sort.Strings(names)

	retains := make(map[string][]bool, len(funcs))
	retainsOf := func(callee string) []bool {
		if r, ok := retains[callee]; ok {
			return r
		}
		r := ParamRetained(funcs[callee], sol.Sigs)
		retains[callee] = r
		return r
	}

	var out []CallModeSite
	for _, n := range names {
		f := funcs[n]
		uses := BuildUses(f)
		reach := reachableBlocks(f)
		units := UnitsOf(f, sol.Sigs)
		defs := defMap(f)
		for _, b := range f.Blocks {
			for oi, o := range b.Ops {
				if o.Kind != OpCall {
					continue
				}
				callee := ir.CodegenAlias(o.Str)
				sig, known := sol.Sigs[callee]
				if !known || funcs[callee] == nil {
					continue
				}
				if _, isHelper := ir.RcHelperSig(o.Str); isHelper {
					// A generated drop is in the solved set because it
					// has a body, but consuming is what it is FOR: there
					// is no borrowed drop to specialise towards.
					continue
				}
				src, mapped := o.SourceOp()
				for i, a := range o.Args {
					if i >= len(sig.Params) || !sig.Pointer[i] || !a.IsValid() {
						continue
					}
					// From the root, not the argument: the argument is
					// often a retain's result, and the closure walks
					// forward from a value, so starting at the rename
					// would miss every use of the object's other names.
					carriers := unitCarriersOf(f, uses, units.Root(a), sol.Sigs)
					later := wantedUses(usesAfter(uses, reach, b, oi, carriers, o))
					r := retainsOf(callee)
					out = append(out, CallModeSite{
						Caller:        n,
						Callee:        callee,
						Arg:           i,
						Mode:          sig.Params[i],
						Origin:        units.Origin(a),
						Dying:         len(later) == 0,
						CalleeRetains: i < len(r) && r[i],
						CallerRetains: isRetainResult(defs[a.ID]),
						SrcOp:         src,
						Mapped:        mapped,
					})
				}
			}
		}
	}
	return out
}

// wantedUses drops the uses that are plain releases: a later
// `__fern_rc_dec` says the caller is done with the value, not that it
// still wants it, and the deferred release after a borrowed call is
// exactly what an owned variant would move. A consuming builtin —
// `__method_Array_push`, `__method_Map_set` — also carries a release
// effect, but it hands the unit to a container, and a value the caller
// goes on to store is wanted like any other. The runtime's releases
// return their operand (`__free` returns nothing); the builtins do not,
// which is what tells the two apart.
func wantedUses(uses []UseSite) []UseSite {
	var out []UseSite
	for _, u := range uses {
		if u.Op != nil && plainRelease(u.Op, u.Index) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// isRetainResult reports whether o is a retain helper handing back its
// operand, so its result is the retained object under a new name.
func isRetainResult(o *Op) bool {
	if o == nil || o.Kind != OpCall {
		return false
	}
	for _, ro := range rcSig(o) {
		if ro.Arg.Effect == ir.RcRetain && ro.Arg.ResultIsOperand {
			return true
		}
	}
	return false
}

func plainRelease(o *Op, argIndex int) bool {
	if o.Kind != OpCall {
		return false
	}
	sig, ok := ir.RcHelperSig(o.Str)
	if !ok {
		return false
	}
	a, ok := sig.Arg(argIndex)
	return ok && a.Effect == ir.RcRelease && (a.ResultIsOperand || o.Str == "__free")
}

// ParamRetained reports, per SSA parameter, whether f's body retains it:
// some value denoting the parameter — an alias, a pass-through, a phi
// it feeds — is the operand of a helper the runtime table records as
// adding a unit.
//
// A store of an alias is preceded by exactly such a retain in the
// lowering, so this is also "the callee keeps the parameter somewhere".
func ParamRetained(f *Func, sigs map[string]Signature) []bool {
	if f == nil {
		return nil
	}
	uses := BuildUses(f)
	out := make([]bool, len(f.Params))
	for i, p := range f.Params {
		if i < len(f.ParamAddrs) && !f.ParamAddrs[i] {
			continue
		}
		for _, v := range unitCarriersOf(f, uses, p, sigs) {
			for _, u := range uses.Of(v) {
				if u.Op == nil {
					continue
				}
				for _, ro := range rcSig(u.Op) {
					if ro.Value.ID == v.ID && ro.Arg.Effect == ir.RcRetain {
						out[i] = true
					}
				}
			}
		}
	}
	return out
}
