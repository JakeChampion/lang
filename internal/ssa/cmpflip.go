package ssa

// CmpFlip rewrites `not(cmp a b)` into the inverted comparison
// in place. Saves an Op (the OpNot itself becomes the
// inverted-cmp Op; the original cmp Op stays put for any
// other consumers, and DCE reclaims it if there are none).
//
// Mappings (signed + unsigned int):
//
//	not(eq a b)   → ne a b      not(ne a b)   → eq a b
//	not(lt a b)   → ge a b      not(ge a b)   → lt a b
//	not(le a b)   → gt a b      not(gt a b)   → le a b
//	not(ltU a b)  → geU a b     not(geU a b)  → ltU a b
//	not(leU a b)  → gtU a b     not(gtU a b)  → leU a b
//
// Float Eq/Ne are also flipped — by IEEE-754 the two are exact
// complements (both return false for NaN comparisons, but `feq`
// returns false on NaN and `fne` returns true, so they're truly
// inverse on every input):
//
//	not(feq a b)  → fne a b     not(fne a b)  → feq a b
//
// The ordered float predicates (FLt/FLe/FGt/FGe) are NOT
// flipped: NaN comparisons return false for all four, so
// `not(FLt NaN NaN) == true` but `FGe NaN NaN == false`. The
// IEEE-754 negations would need unordered predicates we don't
// have ops for.
//
// The rewrite preserves the original OpNot's Result Value, so
// downstream consumers see the inverted comparison transparently.
// Args are copied (not aliased) from the producing cmp so a later
// in-place mutation of the original Op can't break this one.
func CmpFlip(f *Func) {
	if f == nil {
		return
	}
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}

	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Kind != OpNot || len(op.Args) != 1 {
				continue
			}
			def, ok := defs[op.Args[0].ID]
			if !ok {
				continue
			}
			flipped, ok := flippedCmp(def.Kind)
			if !ok {
				continue
			}
			op.Kind = flipped
			op.Args = append([]Value(nil), def.Args...)
		}
	}
}

func flippedCmp(k OpKind) (OpKind, bool) {
	switch k {
	case OpEq:
		return OpNe, true
	case OpNe:
		return OpEq, true
	case OpLt:
		return OpGe, true
	case OpLe:
		return OpGt, true
	case OpGt:
		return OpLe, true
	case OpGe:
		return OpLt, true
	case OpLtU:
		return OpGeU, true
	case OpLeU:
		return OpGtU, true
	case OpGtU:
		return OpLeU, true
	case OpGeU:
		return OpLtU, true
	case OpFEq:
		return OpFNe, true
	case OpFNe:
		return OpFEq, true
	}
	return 0, false
}
