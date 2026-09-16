package ssa

// UseSite identifies one read of a Value somewhere in a Func.
// Exactly one of Op or `Block` (with a nil Op) is "the use" —
// Op != nil indicates an Op-arg use at `Op.Args[Index]`;
// Op == nil indicates a terminator-operand use at the slot
// `Block.Term` decides (`Cond` for BrIf, `Value` for Ret).
type UseSite struct {
	Block *Block
	Op    *Op // nil ⇒ this is a terminator-operand use
	Index int // for Op uses, position in Op.Args
}

// Uses indexes every def→use edge in a Func. Build once with
// BuildUses; query with Of(v) / Count(v). Mutating the Func
// after building invalidates the index — rebuild after every
// transformation pass.
type Uses struct {
	of idTable[[]UseSite]
}

// BuildUses walks `f` and records every Value use in an index
// keyed by Value.ID. Cost is O(N + total-uses); negligible at
// SSA-function scale.
func BuildUses(f *Func) *Uses {
	u := &Uses{of: newIDTable[[]UseSite](f)}
	if f == nil {
		return u
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			for i, arg := range op.Args {
				if !arg.IsValid() {
					continue
				}
				u.of.set(arg, append(u.of.get(arg), UseSite{
					Block: b,
					Op:    op,
					Index: i,
				}))
			}
		}
		switch b.Term.Kind {
		case TermBrIf:
			if b.Term.Cond.IsValid() {
				u.of.set(b.Term.Cond, append(u.of.get(b.Term.Cond), UseSite{Block: b}))
			}
		case TermRet:
			if b.Term.Value.IsValid() {
				u.of.set(b.Term.Value, append(u.of.get(b.Term.Value), UseSite{Block: b}))
			}
		case TermRetPair:
			if b.Term.Value.IsValid() {
				u.of.set(b.Term.Value, append(u.of.get(b.Term.Value), UseSite{Block: b}))
			}
			if b.Term.Value2.IsValid() {
				u.of.set(b.Term.Value2, append(u.of.get(b.Term.Value2), UseSite{Block: b}))
			}
		}
	}
	return u
}

// Of returns every site that reads `v`. Callers must NOT
// mutate the returned slice — it aliases the internal index.
// Returns nil if `v` has no uses (callers can range over a
// nil slice safely).
func (u *Uses) Of(v Value) []UseSite {
	if u == nil {
		return nil
	}
	return u.of.get(v)
}

// Count returns how many sites read `v`. Cheaper than
// len(Of(v)) only marginally, but reads better at call sites
// that just want a yes/no on "is this Value used at all".
func (u *Uses) Count(v Value) int {
	if u == nil {
		return 0
	}
	return len(u.of.get(v))
}

// HasUses reports whether `v` is read anywhere.
func (u *Uses) HasUses(v Value) bool { return u.Count(v) > 0 }
