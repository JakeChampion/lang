package ir

import "github.com/jakechampion/lang/internal/ast"

// Cross-branch reuse sharing (#4402 opt 3a): the arm-exclusivity test that
// lets ONE dead donor hand its reuse token to a construction in EVERY arm of
// a branch, instead of the first arm only. `if c { x = T{…} } else { y = T{…} }`
// with a single dead D used to reuse D's box on the then-path and bump-allocate
// on the else-path; both paths now take it, because only one of them runs.
//
// The token itself is materialised per claimant (emitReuseToken loads D's
// slot, is_unique-gates it, and ZEROES the slot), so sharing needs no new
// runtime machinery: whichever arm executes finds the box, and a arm that
// somehow ran second would find a null slot and allocate fresh.

// mutuallyExclusive reports whether nodes `a` and `b`, both nested under the
// statement `st`, sit in DIFFERENT arms of some `if` / `match` under it — so
// one pass through `st` can reach at most one of them.
//
// Only branch arms separate: two nodes in the same arm, in straight-line
// siblings, or in separate iterations of a loop can all run, and the test
// says false for those. It is a containment scan rather than a path walk
// because the ancestor chain of a construction is short and the answer is
// wanted for a handful of node pairs per function.
func mutuallyExclusive(st ast.Stmt, a, b ast.Node) bool {
	exclusive := false
	ast.Walk(st, func(n ast.Node) bool {
		if exclusive {
			return false
		}
		var arms []ast.Node
		switch x := n.(type) {
		case *ast.If:
			if x.Then != nil {
				arms = append(arms, x.Then)
			}
			if x.Else != nil {
				arms = append(arms, x.Else)
			}
		case *ast.Match:
			for _, arm := range x.Arms {
				if arm != nil && arm.Body != nil {
					arms = append(arms, arm.Body)
				}
			}
		default:
			return true
		}
		ai, bi := -1, -1
		for i, arm := range arms {
			if containsNode(arm, a) {
				ai = i
			}
			if containsNode(arm, b) {
				bi = i
			}
		}
		if ai >= 0 && bi >= 0 && ai != bi {
			exclusive = true
			return false
		}
		return true
	})
	return exclusive
}

// containsNode reports whether `target` occurs anywhere at or under `root`.
func containsNode(root ast.Node, target ast.Node) bool {
	found := false
	ast.Walk(root, func(n ast.Node) bool {
		if n == target {
			found = true
		}
		return !found
	})
	return found
}

// typeReachesUserDrop reports whether dropping a value of type `t` can run a
// user `core/mem.Drop` finalizer — on `t` itself or on anything it
// transitively holds. `seen` breaks recursive types.
//
// Reuse is what needs the transitive answer (reuseClassOf): it takes over a
// dying box, so the finalizers under it either do not run at all (the box's
// own) or run at a different point than they otherwise would (its fields').
func (b *builder) typeReachesUserDrop(t ast.Type, seen map[string]bool) bool {
	if tn, isNominal := ast.ReceiverTypeName(t); isNominal {
		if seen[tn] {
			return false
		}
		seen[tn] = true
		if _, hasDrop := userDropFnName(b.info, tn); hasDrop {
			return true
		}
	}
	switch tt := t.(type) {
	case ast.StructType:
		sd, ok := b.info.Structs[tt.Name]
		if !ok {
			return true // unknown shape — assume the worst
		}
		for _, f := range sd.Fields {
			if b.typeReachesUserDrop(f.Type, seen) {
				return true
			}
		}
	case ast.EnumType:
		ed, ok := b.info.Enums[tt.Name]
		if !ok {
			return true
		}
		if len(tt.Args) > 0 {
			ed = substituteEnumDecl(ed, tt.Args)
		}
		for _, v := range ed.Variants {
			for _, pt := range v.Payloads {
				if b.typeReachesUserDrop(pt, seen) {
					return true
				}
			}
		}
	case ast.TupleType:
		for _, et := range tt.Elems {
			if b.typeReachesUserDrop(et, seen) {
				return true
			}
		}
	case ast.ArrayType:
		return b.typeReachesUserDrop(tt.Elem, seen)
	case ast.SliceType:
		return b.typeReachesUserDrop(tt.Elem, seen)
	case ast.StreamType:
		return b.typeReachesUserDrop(tt.Elem, seen)
	}
	return false
}
