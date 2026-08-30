// Where a pointer value's ownership unit comes from.
//
// `docs/rc-log/2026-08-30-certifier-oracle-first-results.md` ends by
// saying the static leak walk "wants rebuilding against the oracle from
// a correct unit-holder set, one named class at a time". This is that
// set. It answers one question per SSA value — does the DEFINITION of
// this value put an ownership unit in the function's hands — and it
// answers it by op KIND, the rc signature table and the solved callee
// signatures, never by `Op.Addr`.
//
// # Why not Op.Addr
//
// `Op.Addr` answers a register-width question: must the backend skip
// its sign-extension. It is deliberately over-approximate for that, and
// every one of its over-approximations is a false unit holder:
// `OpLoad` is marked unconditionally because an i64 and a pointer are
// indistinguishable there, a `usize` parameter is an address, `base +
// fieldOffset` is an address, and `OpConstString` / `OpEnumSentinel` /
// `OpConstVtable` are addresses that can never be freed. It is also set
// by `ResolveWidths`, a module-level pass the ownership route does not
// run, so on a bare lift it is false almost everywhere.
//
// The probe this replaces read `Op.Addr || Kind == OpAlloc` and marked
// every such value as holding a unit at its definition. Over the 318
// census-clean conformance fixtures that flagged 18,638 functions as
// leaking — 20.3% — and the breakdown named the classes: `alloc x212,
// enum_sentinel x106` in one `checked_abs`.
//
// # The classes are named, and each one is a decision
//
// Nothing here is a heuristic filter over an existing answer. Each
// origin below is a statement about what the emitted code does, with
// the evidence in its comment, and a value the pass cannot place gets
// `UnitUnknown` rather than a guess — the same fail-soft stance
// `verifyrc.go` takes, and countable for the same reason.
package ssa

import "github.com/jakechampion/lang/internal/ir"

// UnitOrigin says where the ownership unit a value holds came from, or
// that the value holds none.
type UnitOrigin uint8

const (
	// UnitNone: reference counting does not apply. A scalar, or an
	// address that is never counted — see unitStatic.
	UnitNone UnitOrigin = iota

	// UnitFresh: the definition allocates. Nobody else holds this unit.
	UnitFresh

	// UnitTransferred: a consumed parameter. The caller handed its unit
	// over at entry, so the function holds one before its first op.
	UnitTransferred

	// UnitBorrowed: an address the definition did NOT get a unit for.
	// A borrowed parameter, a pointer read out of memory, an interior
	// address, a call the signature table proves hands back a borrow.
	//
	// Borrowed is not the same as UnitNone: a borrow can be RETAINED,
	// and then the function does hold a unit on it. The walk needs the
	// distinction; the classification only says what the definition
	// did.
	UnitBorrowed

	// UnitMerged: a phi. Which unit it holds depends on the edge taken,
	// which is a per-path question rather than a property of the
	// definition, so the walk answers it and this pass does not.
	UnitMerged

	// UnitUnknown: an address from a call whose result nothing
	// classifies.
	//
	// This is the honest name for the gap #7786 left open.
	// `internal/ir/rcsigs.go` models what a callee does to its
	// ARGUMENTS and says in its own header that "whether the RESULT
	// carries a unit is not modelled"; `ssa.Signature` proves a return
	// is borrowed but its false is the safe default, not a proof of
	// ownership. So a call returning an address is genuinely unplaced,
	// and saying so is worth more than picking a side: the probe that
	// picked "owned" made its leak count 70% worse.
	UnitUnknown
)

func (o UnitOrigin) String() string {
	switch o {
	case UnitFresh:
		return "fresh"
	case UnitTransferred:
		return "transferred"
	case UnitBorrowed:
		return "borrowed"
	case UnitMerged:
		return "merged"
	case UnitUnknown:
		return "unknown"
	default:
		return "none"
	}
}

// Units is one function's unit-holder set.
type Units struct {
	origin map[int32]UnitOrigin

	// renamed maps a value to the value it is another name for. Only
	// the rc helpers' pass-through results are in here — an inc or a
	// dec hands back the pointer it was given, so code after the call
	// reads the RESULT and a walk that does not follow the rename
	// attributes the later release to nothing.
	//
	// It is exactly the closure `aliasesOf` follows, in the other
	// direction. A phi is deliberately NOT a rename: its incomings are
	// usually different objects.
	renamed map[int32]Value

	// unplaced counts the values that reached UnitUnknown, so a caller
	// can hold a coverage floor rather than discover the gap.
	unplaced int
}

// Origin reports where v's unit came from. A value with no definition
// in f — a constant folded away, a value from another function — is
// UnitNone.
func (u Units) Origin(v Value) UnitOrigin {
	return u.origin[u.Root(v).ID]
}

// Root resolves v through the rename chain to the value that actually
// denotes the object. `__fern_rc_inc(p)` returns p, so the result's
// root is p.
func (u Units) Root(v Value) Value {
	for i := 0; i < 64; i++ {
		next, ok := u.renamed[v.ID]
		if !ok || next.ID == v.ID {
			return v
		}
		v = next
	}
	return v
}

// Unplaced is how many values landed in UnitUnknown.
func (u Units) Unplaced() int { return u.unplaced }

// Carriers is how many distinct values reference counting can apply to
// at all — everything that is not UnitNone.
func (u Units) Carriers() int {
	n := 0
	for _, o := range u.origin {
		if o != UnitNone {
			n++
		}
	}
	return n
}

// unitStatic reports the op kinds whose result is an address that can
// never carry a unit.
//
// All three are `.rodata` (or the wasm data segment) with an immortal
// rc header — the 4-byte rc word at [ptr-8] is 0x80000000, and every rc
// helper on every backend short-circuits on that high bit, so an inc, a
// dec and an is_unique against one are no-ops. `__fern_rc_is_unique`
// answers 0. A vtable has no header at all and nothing ever passes one
// to a helper.
func unitStatic(k OpKind) bool {
	switch k {
	case OpConstString, OpEnumSentinel, OpConstVtable:
		return true
	}
	return false
}

// unitAllocating reports the op kinds that allocate a counted object.
//
// OpAlloc is here for the SSA codegen path, where the bump allocator
// writes rc=1 into an 8-byte header and hands back base+8. On the
// analysis path the flat IR's own OpAlloc lowers to `__fern_alloc`,
// a raw bump with no header, and the counted allocators arrive as
// OpCall instead — which is why an OpAlloc that nothing ever releases
// is not by itself evidence of a leak, and why the walk that reads
// this is fail-soft.
func unitAllocating(k OpKind) bool {
	switch k {
	case OpAlloc, OpMakeClosure, OpMakeEnv, OpBoxDyn:
		return true
	}
	return false
}

// addressShaped reports whether an op's result is pointer-shaped, from
// the op kind alone.
//
// Kind rather than `Op.Addr` on purpose: see this file's header. The
// answer is used only to decide whether a value is worth classifying,
// so an op kind that is sometimes a pointer and sometimes an i64 —
// OpLoad, a call — is included and lands in UnitBorrowed or
// UnitUnknown, never in UnitFresh.
func addressShaped(o *Op) bool {
	switch o.Kind {
	case OpAlloc, OpMakeClosure, OpMakeEnv, OpBoxDyn, OpConstVtable,
		OpConstString, OpEnumSentinel, OpLoad:
		return true
	case OpCall, OpCallIndirect, OpCallDyn:
		// The one place `Op.Addr` is the best answer available: a
		// provided callee's result classification rides on the op from
		// the IR, and a defined callee's comes from its own
		// `ReturnAddr`. Both need `ResolveWidths` to have run — see
		// LiftProgram, which runs it for exactly this reason.
		return o.Addr
	case OpAdd, OpSub, OpSelect, OpPhi:
		return true
	}
	return false
}

// UnitsOf classifies every value in f by where its ownership unit comes
// from.
//
// sigs is the solved whole-program answer from SolveOwnership, keyed by
// function name; pass nil to classify with no interprocedural knowledge
// — every parameter then reads borrowed and every call result unknown,
// which is sound and much less useful.
func UnitsOf(f *Func, sigs map[string]Signature) Units {
	u := Units{origin: map[int32]UnitOrigin{}, renamed: map[int32]Value{}}

	self := sigs[f.Name]
	for i, p := range f.Params {
		if i >= len(f.ParamAddrs) || !f.ParamAddrs[i] {
			continue
		}
		// The second word of a two-word value is a length, and the
		// lift already records that as ParamAddrs=false, so anything
		// reaching here is the data word.
		if i < len(self.Params) && self.Params[i] == Consumed {
			u.origin[p.ID] = UnitTransferred
			continue
		}
		u.origin[p.ID] = UnitBorrowed
	}

	defs := defMap(f)
	paramAddr := map[int32]bool{}
	for i, p := range f.Params {
		if i < len(f.ParamAddrs) && f.ParamAddrs[i] {
			paramAddr[p.ID] = true
		}
	}

	// Renames first, and in their own pass: `baseOf` asks about a
	// value's DEFINITION, which may sit in a block this loop has not
	// reached yet, so deciding renames while classifying would make the
	// answer depend on block order.
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if !o.Result.IsValid() {
				continue
			}
			// A pass-through rc helper's result is the operand under
			// another name.
			if renamesOperand(o) {
				u.renamed[o.Result.ID] = renameTarget(o)
				continue
			}
			// So is an interior address. `base + fieldOffset` is the
			// one object under an offset: the rc header sits below the
			// pointer the program passes around, so the value a
			// function returns or stores is routinely `alloc + N`
			// rather than the allocation itself, and a walk that does
			// not resolve that reports every such object as leaked
			// while its own transfer is standing in front of it.
			if base, ok := baseOf(o, defs, paramAddr); ok {
				u.renamed[o.Result.ID] = base
			}
		}
	}

	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if !o.Result.IsValid() {
				continue
			}
			if _, renamed := u.renamed[o.Result.ID]; renamed {
				continue
			}
			u.origin[o.Result.ID] = classifyDef(o, sigs)
			if u.origin[o.Result.ID] == UnitUnknown {
				u.unplaced++
			}
		}
	}
	return u
}

// baseOf reports the address an offset computation stands on.
//
// Written against the DEFINING op rather than `Op.Addr` so it holds on a
// bare lift, and biased to argument 0 because that is where the lift
// puts the base (`l.offset(addr, n)`); the scan exists for the case
// where constant folding reorders it.
func baseOf(o *Op, defs map[int32]*Op, paramAddr map[int32]bool) (Value, bool) {
	if o.Kind != OpAdd && o.Kind != OpSub {
		return Value{}, false
	}
	for _, a := range o.Args {
		if paramAddr[a.ID] {
			return a, true
		}
		if d, ok := defs[a.ID]; ok && addressShaped(d) {
			return a, true
		}
	}
	return Value{}, false
}

// renamesOperand reports whether o's result denotes the same object as
// one of its arguments, per the rc signature table's ResultIsOperand.
//
// False for RcMove, whose result is a DIFFERENT object whenever the
// receiver was shared, and false for `__free` and `__fern_rc_is_unique`,
// which return nothing and a boolean.
func renamesOperand(o *Op) bool {
	if o.Kind != OpCall {
		return false
	}
	sig, ok := ir.RcHelperSig(o.Str)
	if !ok {
		return false
	}
	for _, a := range sig.Args {
		if a.ResultIsOperand && a.Index >= 0 && a.Index < len(o.Args) {
			return true
		}
	}
	return false
}

func renameTarget(o *Op) Value {
	sig, _ := ir.RcHelperSig(o.Str)
	for _, a := range sig.Args {
		if a.ResultIsOperand && a.Index >= 0 && a.Index < len(o.Args) {
			return o.Args[a.Index]
		}
	}
	return o.Result
}

// unitFromResult maps the result axis of the ownership signature table
// onto this pass's origins.
//
// `RcResultRaw` and `RcResultOwned` both answer UnitFresh, and the
// distinction is not lost by that — it is simply not this pass's
// question. A raw block and an rc-headered one are equally the
// function's to dispose of; what separates them is what an emitted
// RELEASE would do (reclaim, versus read a neighbour's bytes), which is
// the verifier's question. Collapsing them here also keeps a call to
// `__alloc` classified the same as the `OpAlloc` it is an alias for.
//
// `RcResultImmortal` is UnitNone rather than UnitFresh: the block is
// fresh and pointer-shaped, but its rc word is the static sentinel, so
// no release can reclaim it and the caller holds nothing to leak.
func unitFromResult(r ir.RcResult) UnitOrigin {
	switch r {
	case ir.RcResultOwned, ir.RcResultRaw:
		return UnitFresh
	case ir.RcResultBorrow:
		return UnitBorrowed
	case ir.RcResultOperand:
		// Reached only if the argument axis had no ResultIsOperand to
		// rename through, which the two-axis agreement gate forbids.
		return UnitUnknown
	default:
		// RcResultNone, and RcResultImmortal.
		return UnitNone
	}
}

// classifyDef places one defining op.
func classifyDef(o *Op, sigs map[string]Signature) UnitOrigin {
	switch {
	case unitAllocating(o.Kind):
		return UnitFresh
	case unitStatic(o.Kind):
		return UnitNone
	}
	switch o.Kind {
	case OpLoad:
		// A pointer read out of memory. The container owns the unit;
		// the read is reachable-FROM it rather than identical to it,
		// which is the same reason `ownership_returns.go` refuses to
		// follow a load back to its base. Where the binding site does
		// want its own reference, lowering emits a retain, and the
		// retain is what puts a unit in this function's hands.
		//
		// OpLoad is also every 8-byte scalar read, which is why this
		// must not be UnitFresh.
		return UnitBorrowed
	case OpAdd, OpSub:
		// Pointer arithmetic on nothing recognisable as a base. An
		// interior address WITH a base is a rename and never reaches
		// here — see baseOf.
		return UnitBorrowed
	case OpSelect, OpPhi:
		// Both merge two values that may have different ownership.
		return UnitMerged
	case OpCallPair:
		// A pair return is (tag, payload) and one `Addr` bit covers
		// both results, so which half carries a unit is not readable
		// here. `ownership_returns.go` bails on TermRetPair for the
		// same reason. Left unmodelled rather than guessed.
		return UnitNone
	case OpCall, OpCallIndirect, OpCallDyn:
		if !addressShaped(o) {
			return UnitNone
		}
		if o.Kind == OpCall {
			if sig, ok := sigs[ir.CodegenAlias(o.Str)]; ok && sig.ReturnBorrowed {
				// Phase B proved every returned value is a borrow of
				// a parameter, so the caller got no unit.
				return UnitBorrowed
			}
			if r, known := ir.RcHelperResult(o.Str); known {
				return unitFromResult(r)
			}
		}
		return UnitUnknown
	}
	return UnitNone
}
