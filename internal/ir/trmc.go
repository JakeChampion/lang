// Tail-recursion-modulo-cons (TRMC) as an IR transform.
//
// A function of the canonical shape
//
//	function map(xs: List): List {
//	    match (xs) {
//	        Cons(h, t) => { return Cons(g(h), map(t)); },
//	        Nil        => { return Nil; },
//	    }
//	}
//
// is NOT tail-recursive — the `Cons(..)` constructor wraps the recursive
// `map(t)` result — so ordinary lowering grows the call stack O(n) and
// overflows on long lists. TRMC rewrites it into a hole-passing loop: each
// `Cons` node is allocated with its tail field left UNWRITTEN (a "hole"); the
// previous hole is filled with the new node's data pointer, and the hole
// advances to the new node's tail field. The base arm fills the final hole
// with its value. O(1) stack, single pass, no reversal.
//
//	hole = 0; result = 0            // hole == 0 means "write into result"
//	loop {
//	    match (xs) {
//	        Cons(h, t) => {
//	            node = alloc Cons; node.head = g(h); node.tail = HOLE
//	            if hole == 0 { result = node } else { *hole = node }
//	            hole = &node.tail; xs = t; continue
//	        }
//	        Nil => { if hole == 0 { result = Nil } else { *hole = Nil }; break }
//	    }
//	}
//	return result
//
// Slice 1 is deliberately conservative — it fires only on the shape above:
// a single `match` body whose scrutinee is a BORROWED enum parameter, every
// arm a single `return`, each recursive arm a variant constructor whose LAST
// payload is the one self-call (single hole → list-shaped, not tree-shaped),
// and no other payload containing a self-call. Anything else falls through to
// ordinary lowering unchanged. Gated on ast.TrmcEnabled so the differential
// gate can pin TRMC-on == TRMC-off byte-identical.
package ir

import "github.com/jakechampion/lang/internal/ast"

const trmcRcHeaderBytes = 8

// trmcArm is one classified arm of a TRMC-eligible match.
type trmcArm struct {
	scrutVarIdx  int        // variant index in the SCRUTINEE enum (tag test)
	bindings     []string   // payload binding names (scrutinee variant)
	bindingTypes []ast.Type // resolved binding types

	recursive bool
	// recursive arm:
	ctorVarIdx int        // variant index in the RETURN enum (node built)
	headArgs   []ast.Expr // ctor payloads except the last (the hole)
	buildTypes []ast.Type // payload types of the constructed variant
	selfCall   *ast.Call  // the recursive call (last ctor payload)
	// base arm:
	baseExpr ast.Expr // the returned value (no self-call)
}

type trmcShape struct {
	matchedParam int // param index of the match scrutinee
	arms         []trmcArm
}

// selfCallCount counts calls to the enclosing function within e (a self-call
// is a Call whose callee is the bare function name).
func (b *builder) selfCallCount(e ast.Expr) int {
	n := 0
	ast.Walk(e, func(nd ast.Node) bool {
		if c, ok := nd.(*ast.Call); ok {
			if id, ok := c.Callee.(*ast.Ident); ok && id.Name == b.fn.Name && c.Method == nil {
				n++
			}
		}
		return true
	})
	return n
}

// detectTrmc returns the eligible shape, or nil if the function isn't a
// slice-1 TRMC candidate.
func (b *builder) detectTrmc() *trmcShape {
	fn := b.fn
	// Plain, concrete, non-closure function returning an enum.
	if fn.Body == nil || len(fn.TypeParams) > 0 || len(fn.Captures) > 0 || b.thisIsPair {
		return nil
	}
	retEnum, ok := fn.ReturnType.(ast.EnumType)
	if !ok {
		return nil
	}
	// Body must be exactly one `match`.
	if len(fn.Body.Stmts) != 1 {
		return nil
	}
	m, ok := fn.Body.Stmts[0].(*ast.Match)
	if !ok {
		return nil
	}
	// Scrutinee must be a bare BORROWED enum parameter.
	scrutID, ok := m.Tag.(*ast.Ident)
	if !ok {
		return nil
	}
	mp := -1
	for i, p := range fn.Params {
		if p.Name == scrutID.Name {
			mp = i
			break
		}
	}
	if mp < 0 || fn.Params[mp].Own {
		return nil
	}
	if _, ok := fn.Params[mp].Type.(ast.EnumType); !ok {
		return nil
	}

	sh := &trmcShape{matchedParam: mp}
	anyRecursive := false
	for _, arm := range m.Arms {
		// Only plain variant arms (no wildcard / literal / guard).
		if arm.IsWildcard || arm.Literal != nil || arm.Guard != nil || arm.VariantName == "" {
			return nil
		}
		if arm.Body == nil || len(arm.Body.Stmts) != 1 {
			return nil
		}
		ret, ok := arm.Body.Stmts[0].(*ast.Return)
		if !ok || ret.Value == nil {
			return nil
		}
		// The checker's stamped resolution, like the two match lowerings.
		// TRMC is an optimisation, so an unresolved arm declines the
		// transform rather than failing the build.
		if arm.EnumName == "" {
			return nil
		}
		scrutVarIdx := arm.VariantIndex
		ta := trmcArm{
			scrutVarIdx:  scrutVarIdx,
			bindings:     arm.Bindings,
			bindingTypes: arm.BindingTypes,
		}
		switch n := b.selfCallCount(ret.Value); n {
		case 0:
			ta.baseExpr = ret.Value
		case 1:
			// Recursive arm: a constructor whose LAST payload is the self-call.
			ctor, ok := ret.Value.(*ast.Call)
			if !ok {
				return nil
			}
			cid, ok := ctor.Callee.(*ast.Ident)
			if !ok {
				return nil
			}
			cenum, cVarIdx, cPayloadCount, ok := b.lookupVariantOn(cid.Name, cid.EnumName)
			if !ok || cenum != retEnum.Name || cPayloadCount != len(ctor.Args) || cPayloadCount == 0 {
				return nil
			}
			last := ctor.Args[len(ctor.Args)-1]
			self, ok := last.(*ast.Call)
			if !ok {
				return nil
			}
			sid, ok := self.Callee.(*ast.Ident)
			if !ok || sid.Name != fn.Name || self.Method != nil || len(self.Args) != len(fn.Params) {
				return nil
			}
			// The self-call must be EXACTLY the last payload; no other payload
			// may contain a self-call (single hole).
			for _, ha := range ctor.Args[:len(ctor.Args)-1] {
				if b.selfCallCount(ha) != 0 {
					return nil
				}
			}
			ta.recursive = true
			ta.ctorVarIdx = cVarIdx
			ta.headArgs = ctor.Args[:len(ctor.Args)-1]
			ta.buildTypes = b.variantPayloadTypes(ctor, cenum, cVarIdx)
			ta.selfCall = self
			anyRecursive = true
		default:
			return nil // >1 self-call → tree-shaped, not single-hole
		}
		sh.arms = append(sh.arms, ta)
	}
	if !anyRecursive {
		return nil
	}
	return sh
}

// variantPayloadTypes resolves the constructed variant's payload types,
// preferring the checker's substituted types for this exact call.
func (b *builder) variantPayloadTypes(call *ast.Call, enumName string, varIdx int) []ast.Type {
	if call != nil {
		if pts, ok := b.info.VariantCallPayloads[call]; ok {
			return pts
		}
	}
	if ed, ok := b.info.Enums[enumName]; ok && varIdx < len(ed.Variants) {
		return ed.Variants[varIdx].Payloads
	}
	return nil
}

// tryEmitTrmc detects and, if eligible, emits the TRMC hole-passing loop in
// place of normal body lowering. Returns whether it handled the function.
func (b *builder) tryEmitTrmc() bool {
	if !ast.TrmcEnabled {
		return false
	}
	sh := b.detectTrmc()
	if sh == nil {
		return false
	}
	b.emitTrmc(sh)
	return true
}

// emitHoleLink emits: if hole == 0 { result = [valSlot] } else { *hole = [valSlot] }.
func (b *builder) emitHoleLink(valSlot, holeSlot, resultSlot int32) {
	b.emit(Op{Kind: OpLoadLocal, I32: holeSlot})
	b.emit(Op{Kind: OpNot}) // hole == 0
	b.openIf(BlockTypeVoid)
	b.emit(Op{Kind: OpLoadLocal, I32: valSlot})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})
	b.elseBranch()
	b.emit(Op{Kind: OpLoadLocal, I32: holeSlot})
	b.emit(Op{Kind: OpLoadLocal, I32: valSlot})
	b.emit(Op{Kind: OpStore, Width: WidthPtr})
	b.closeScope()
}

func (b *builder) emitTrmc(sh *trmcShape) {
	mp := int32(sh.matchedParam)
	holeSlot := b.allocSlot()
	resultSlot := b.allocSlot()
	b.locals["__trmc_hole"] = holeSlot
	b.locals["__trmc_result"] = resultSlot
	// hole = 0; result = 0.
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: holeSlot})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: resultSlot})

	// TRMC-consuming (Slice 2): a consume-safe TRMC function frees its scrutinee
	// cell as the loop advances. trmcScrutSlot tracks the current cell (the
	// rebound param slot), stillFreeing stays 1 until the first SHARED cell.
	if b.trmcConsumeSafe[b.fn.Name] {
		if et, ok := b.fn.Params[sh.matchedParam].Type.(ast.EnumType); ok {
			b.trmcConsuming = true
			b.trmcScrutEnum = et
			b.trmcScrutSlot = mp
			b.trmcStillSlot = b.allocSlot()
			b.emit(Op{Kind: OpConstI32, I32: 1})
			b.emit(Op{Kind: OpStoreLocal, I32: b.trmcStillSlot})
		}
	}

	b.openBlock(BlockTypeVoid) // exit block — `break` target
	exitD := b.depth
	b.openLoop(BlockTypeVoid) // loop — `continue` target
	loopD := b.depth

	for _, arm := range sh.arms {
		b.openBlock(BlockTypeVoid)
		armD := b.depth
		// Skip this arm when the scrutinee tag doesn't match.
		b.emit(Op{Kind: OpLoadLocal, I32: mp})
		b.emit(Op{Kind: OpMatchTag})
		b.emit(Op{Kind: OpConstI32, I32: int32(arm.scrutVarIdx)})
		b.emit(Op{Kind: OpEq})
		b.emit(Op{Kind: OpNot})
		b.brTo(armD, true) // if tag != variant, exit arm block → next arm

		// Bind the scrutinee variant's payloads into fresh slots.
		boffs, _ := payloadLayout(arm.bindingTypes, len(arm.bindings), b.ptrW)
		for i, bname := range arm.bindings {
			if bname == "_" {
				continue
			}
			slot := b.allocSlot()
			b.locals[bname] = slot
			var bt ast.Type
			if i < len(arm.bindingTypes) {
				bt = arm.bindingTypes[i]
			}
			if bt != nil {
				b.scratchType[slot] = bt
			}
			b.emit(Op{Kind: OpLoadLocal, I32: mp})
			b.emit(Op{Kind: OpConstI32, I32: boffs[i]})
			b.emit(Op{Kind: OpAdd})
			b.emit(payloadLoadOpFor(bt, b.ptrW))
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
		}

		if arm.recursive {
			b.emitTrmcRecArm(arm, holeSlot, resultSlot)
			b.brTo(loopD, false) // continue
		} else {
			valSlot := b.allocSlot()
			b.scratchType[valSlot] = b.fn.ReturnType
			_ = b.expr(arm.baseExpr)
			b.emit(Op{Kind: OpStoreLocal, I32: valSlot})
			b.emitHoleLink(valSlot, holeSlot, resultSlot)
			b.brTo(exitD, false) // break
		}
		b.closeScope() // end arm block
	}
	b.closeScope() // end loop
	b.closeScope() // end exit block

	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpReturn})
}

// emitTrmcRecArm emits a recursive arm: build the node with its tail left as a
// hole, link it, advance the hole, then rebind the parameters to the recursive
// call's arguments and fall through to the caller's `continue` branch.
func (b *builder) emitTrmcRecArm(arm trmcArm, holeSlot, resultSlot int32) {
	payloadCount := len(arm.buildTypes)
	poffs, psize := payloadLayout(arm.buildTypes, payloadCount, b.ptrW)

	// alloc node + rc header.
	baseSlot := b.allocSlot()
	b.emit(Op{Kind: OpConstI32, I32: psize + trmcRcHeaderBytes})
	b.emit(Op{Kind: OpAlloc})
	b.emit(Op{Kind: OpStoreLocal, I32: baseSlot})
	// rc = 1.
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: 1})
	b.emit(Op{Kind: OpStore})
	// tag.
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: trmcRcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpConstI32, I32: int32(arm.ctorVarIdx)})
	b.emit(Op{Kind: OpStore})
	// head payloads (all but the last — that one is the hole).
	for i := 0; i < payloadCount-1; i++ {
		var pt ast.Type
		if i < len(arm.buildTypes) {
			pt = arm.buildTypes[i]
		}
		_ = b.expr(arm.headArgs[i])
		vslot := b.allocSlot()
		if pt != nil {
			b.scratchType[vslot] = pt
		}
		b.emit(Op{Kind: OpStoreLocal, I32: vslot})
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: trmcRcHeaderBytes + poffs[i]})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoadLocal, I32: vslot})
		b.emit(payloadStoreOpFor(pt, b.ptrW))
	}
	// nodeData = base + rc header.
	nodeDataSlot := b.allocSlot()
	b.scratchType[nodeDataSlot] = b.fn.ReturnType
	b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
	b.emit(Op{Kind: OpConstI32, I32: trmcRcHeaderBytes})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: nodeDataSlot})
	// Link the node into the previous hole (or the result), then advance the
	// hole to the node's still-unwritten tail field.
	b.emitHoleLink(nodeDataSlot, holeSlot, resultSlot)
	b.emit(Op{Kind: OpLoadLocal, I32: nodeDataSlot})
	b.emit(Op{Kind: OpConstI32, I32: poffs[payloadCount-1]})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: holeSlot})

	// Rebind parameters to the recursive call's arguments. Evaluate every
	// argument into a temp FIRST (they read the current params / bindings),
	// then store the temps into the param slots — so a self-call that permutes
	// or increments a scalar param can't clobber an input it still needs.
	tmp := make([]int32, len(b.fn.Params))
	for j := range b.fn.Params {
		_ = b.expr(arm.selfCall.Args[j])
		ts := b.allocSlot()
		if j < len(b.fn.Params) {
			b.scratchType[ts] = b.fn.Params[j].Type
		}
		b.emit(Op{Kind: OpStoreLocal, I32: ts})
		tmp[j] = ts
	}
	// TRMC-consuming: free the just-walked scrutinee cell now — after every read
	// of it (bindings, head payloads, the self-call arg temps) and BEFORE the
	// param store overwrites trmcScrutSlot with the tail. The freed cell recycles
	// via the freelist into the next iteration's node alloc (bounded heap).
	b.emitTrmcConsumeScrut()
	for j := range b.fn.Params {
		b.emit(Op{Kind: OpLoadLocal, I32: tmp[j]})
		b.emit(Op{Kind: OpStoreLocal, I32: int32(j)})
	}
}

// trmcShapeConsumeSafe reports whether a TRMC function can CONSUME its scrutinee
// in the loop (Slice 2 TRMC-consuming). Soundness turns entirely on the SHAPE OF
// THE SCRUTINEE BOX that the loop shallow-frees — not the node it builds. A
// shallow free discards the box and decs NONE of its payloads, so it is correct
// only when, for every recursive arm:
//
//   - the scrutinee parameter is owned-by-default-eligible (uniform box,
//     string/array/Map-free), AND
//   - the self-call advances the matched param to a bare binding ident — the
//     "tail" cell whose reference we STEAL (move forward as the next
//     scrutinee), which must itself be the same enum (a same-class cell), AND
//   - every OTHER scrutinee binding is definitely-scalar, so dropping the box
//     without dec'ing those payloads leaks nothing.
//
// That is exactly the FBIP list walk (`Cons(i32, List)` → recurse on the tail);
// trees (two pointer payloads) and pointer-headed cells are excluded because a
// non-tail pointer payload would be lost by the shallow free.
func (b *builder) trmcShapeConsumeSafe(sh *trmcShape) bool {
	if sh == nil {
		return false
	}
	mp := sh.matchedParam
	if mp < 0 || mp >= len(b.fn.Params) {
		return false
	}
	scrutEnum, ok := b.fn.Params[mp].Type.(ast.EnumType)
	if !ok || !b.isOwnedByDefaultType(scrutEnum) {
		return false
	}
	for _, arm := range sh.arms {
		if !arm.recursive {
			continue
		}
		// The self-call must advance the matched param to a bare binding ident.
		if mp >= len(arm.selfCall.Args) {
			return false
		}
		adv, ok := arm.selfCall.Args[mp].(*ast.Ident)
		if !ok {
			return false
		}
		tailIdx := -1
		for i, bn := range arm.bindings {
			if bn == adv.Name {
				tailIdx = i
				break
			}
		}
		if tailIdx < 0 {
			return false // advance arg isn't one of this arm's own bindings
		}
		for i, bt := range arm.bindingTypes {
			if i == tailIdx {
				// The stolen tail must be a same-class cell of the scrutinee enum.
				if et, ok := bt.(ast.EnumType); !ok || et.Name != scrutEnum.Name {
					return false
				}
				continue
			}
			if !isDefinitelyScalar(bt) {
				return false
			}
		}
	}
	return true
}

// emitTrmcConsumeScrut frees the current scrutinee cell as the loop advances,
// under owned-by-default. Perceus consuming traversal: while still-freeing,
// shallow-free a uniquely-owned cell (recycled by the freelist into the next
// node's alloc — in-place reuse); at the first SHARED cell, dec it once (balances
// the caller's retain inc) and stop freeing — the rest of the list belongs to
// the sharer and must not be touched. Net: a uniquely-owned input is fully
// reclaimed; an aliased input loses exactly the one retained reference.
func (b *builder) emitTrmcConsumeScrut() {
	if !b.trmcConsuming {
		return
	}
	ed, ok := b.info.Enums[b.trmcScrutEnum.Name]
	if !ok {
		return
	}
	if len(b.trmcScrutEnum.Args) > 0 {
		ed = substituteEnumDecl(ed, b.trmcScrutEnum.Args)
	}
	size, ok := uniformEnumBoxSize(ed, b.ptrW)
	if !ok {
		return
	}
	// if stillFreeing {
	b.emit(Op{Kind: OpLoadLocal, I32: b.trmcStillSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	//   if is_unique(scrut) { box_free(scrut, size) }
	b.emit(Op{Kind: OpLoadLocal, I32: b.trmcScrutSlot})
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: b.trmcScrutSlot})
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpCallDirect, Runtime: true, Str: "__fern_box_free", Width: ResAddr, I32: 2})
	b.emit(Op{Kind: OpDrop})
	//   else { dec(scrut); stillFreeing = 0 }
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: b.trmcScrutSlot})
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: b.trmcStillSlot})
	b.emit(Op{Kind: OpEnd})
	b.emit(Op{Kind: OpEnd})
}
