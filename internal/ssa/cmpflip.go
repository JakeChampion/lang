package ssa

// CmpFlip rewrites `not(cmp a b)` into the inverted comparison
// in place. Saves an Op (the OpNot itself becomes the
// inverted-cmp Op; the original cmp Op stays put for any
// other consumers, and DCE reclaims it if there are none).
//
// Mappings:
//
//	not(eq a b) → ne a b
//	not(ne a b) → eq a b
//	not(lt a b) → ge a b
//	not(le a b) → gt a b
//	not(gt a b) → le a b
//	not(ge a b) → lt a b
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
	}
	return 0, false
}
