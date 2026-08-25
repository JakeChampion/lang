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
// A candidate is a plain, concrete, non-closure function returning an enum
// whose body is `stmts…; match (p) { … }` for a BORROWED enum parameter `p`:
//
//   - the statements before the `match`, and those before an arm's tail, must
//     be rc-NEUTRAL — a scalar `var`, an assignment to a scalar non-parameter
//     local, and `if` nests of those. The
//     loop exits through its own `return`, which bypasses the rc exit sweep,
//     so a statement that would register a drop obligation declines the
//     transform rather than leaking it. A `return` never qualifies: it has to
//     reach the hole machinery, so it is classified as a tail or a guard
//     clause. The statements before the `match` are emitted INSIDE the loop —
//     the recursion re-enters them, so a local derived from a parameter would
//     otherwise freeze at its first-call value while the loop advances that
//     parameter underneath it.
//   - an arm may carry a `when` guard, and an unguarded `_` arm is admitted.
//     A failing guard falls through to the next arm as in ordinary match
//     lowering, so the UNGUARDED arms alone must still cover every variant:
//     falling off the last arm re-enters the loop with the scrutinee
//     unchanged, which the ordinary lowering's fall-through-to-join cannot do.
//   - an arm tail is a `return`, or an `if`/`else` whose branches are
//     themselves arm tails. Three leaf kinds: a variant constructor with one
//     self-call payload (the hole — in any payload position), a bare
//     self-call (`return self(t)`, the filter shape: advance with the hole
//     untouched), or a self-call-free value (the base case, which fills the
//     final hole and breaks).
//
// At least one constructor-wrapped self-call is required — that is what makes
// a function modulo-cons rather than plainly tail-recursive — and no payload
// other than the hole may contain a self-call (single hole → list-shaped, not
// tree-shaped). Anything else falls through to ordinary lowering unchanged.
// Gated on ast.TrmcEnabled so the differential gate can pin TRMC-on ==
// TRMC-off byte-identical.
//
// TRMC-CONSUMING (Slice 2) stays scoped to the narrow shape the cell recycling
// was proved against — see trmcShapeConsumeSafe.
package ir

import "github.com/jakechampion/lang/internal/ast"

const trmcRcHeaderBytes = 8

// trmcTailKind classifies the leaf of an arm body: what the arm's result is
// built from, and hence how the loop continues.
type trmcTailKind uint8

const (
	// trmcTailBase: `return <expr>` with no self-call — fill the final hole
	// and break out of the loop.
	trmcTailBase trmcTailKind = iota
	// trmcTailCons: `return Ctor(…, self(…), …)` — build a node whose hole
	// payload is left unwritten, link it, advance the hole, continue.
	trmcTailCons
	// trmcTailSelf: `return self(…)` — a plain tail call: advance the
	// parameters and continue with the hole untouched.
	trmcTailSelf
	// trmcTailBranch: `if (cond) { … } else { … }` whose branches are
	// themselves TRMC bodies.
	trmcTailBranch
)

// trmcTail is one classified arm tail.
type trmcTail struct {
	kind trmcTailKind

	// trmcTailCons
	ctorVarIdx int        // variant index in the RETURN enum (node built)
	ctorArgs   []ast.Expr // every ctor payload; ctorArgs[holeIdx] is the self-call
	holeIdx    int        // payload index left unwritten (the hole)
	buildTypes []ast.Type // payload types of the constructed variant

	// trmcTailCons / trmcTailSelf
	selfCall *ast.Call

	// trmcTailBase
	baseExpr ast.Expr

	// trmcTailBranch
	cond      ast.Expr
	then, els *trmcBody
}

// trmcStep is one statement of an arm body before its tail: either an
// rc-neutral statement lowered by ordinary statement lowering, or a guard
// clause `if (cond) { …return… }` whose branch is itself a TRMC body — its
// `return` has to reach the hole machinery, not the function's real return.
type trmcStep struct {
	stmt ast.Stmt  // nil for a guard clause
	cond ast.Expr  // guard-clause condition
	then *trmcBody // guard-clause branch
}

// trmcBody is a statement sequence ending in a tail. Every path out of it
// breaks or continues the loop; it never falls through.
type trmcBody struct {
	steps []trmcStep
	tail  *trmcTail
}

// trmcArm is one classified arm of a TRMC-eligible match.
type trmcArm struct {
	isWildcard   bool
	scrutVarIdx  int        // variant index in the SCRUTINEE enum (tag test)
	bindings     []string   // payload binding names (scrutinee variant)
	bindingTypes []ast.Type // resolved binding types
	guard        ast.Expr   // optional `when` guard; nil for unconditional arms
	body         *trmcBody
}

type trmcShape struct {
	matchedParam int        // param index of the match scrutinee
	prefix       []trmcStep // function-body statements before the `match`
	arms         []trmcArm
	// narrow marks the shape TRMC-consuming is scoped to: no `return` before
	// the loop, and every arm a plain unguarded variant arm whose body is a
	// single cons-or-base leaf. See trmcShapeConsumeSafe.
	narrow bool
}

// selfCallCount counts calls to the enclosing function within n (a self-call
// is a Call whose callee is the bare function name).
func (b *builder) selfCallCount(n ast.Node) int {
	c := 0
	ast.Walk(n, func(nd ast.Node) bool {
		if call, ok := nd.(*ast.Call); ok {
			if id, ok := call.Callee.(*ast.Ident); ok && id.Name == b.fn.Name && call.Method == nil {
				c++
			}
		}
		return true
	})
	return c
}

// stmtReturns reports whether s contains a `return` anywhere.
func stmtReturns(s ast.Stmt) bool {
	found := false
	ast.Walk(s, func(nd ast.Node) bool {
		if _, ok := nd.(*ast.Return); ok {
			found = true
		}
		return !found
	})
	return found
}

// trmcNeutralStmt reports whether s can be lowered inside a TRMC function by
// ordinary statement lowering without registering an rc obligation the loop's
// hand-rolled exit would never discharge. Only scalar state qualifies.
// A `return` never qualifies: it has to reach the hole machinery, so it is
// classified as a tail or a guard clause instead.
func (b *builder) trmcNeutralStmt(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Block:
		for _, st := range x.Stmts {
			if !b.trmcNeutralStmt(st) {
				return false
			}
		}
		return true
	case *ast.If:
		if !b.trmcNeutralStmt(x.Then) {
			return false
		}
		return x.Else == nil || b.trmcNeutralStmt(x.Else)
	case *ast.Var:
		return x.Type != nil && isDefinitelyScalar(x.Type)
	case *ast.ExprStmt:
		// `n = n + 1;` reaches lowering as an assignment EXPRESSION statement.
		// Nothing else qualifies: a discarded call's result would need the
		// release the loop's exit never runs.
		asn, isAssign := x.Expr.(*ast.Assign)
		if !isAssign {
			return false
		}
		id, isIdent := asn.Target.(*ast.Ident)
		if !isIdent {
			return false
		}
		// Rebinding a parameter would fight the loop's own advance.
		for _, p := range b.fn.Params {
			if p.Name == id.Name {
				return false
			}
		}
		return isDefinitelyScalar(b.declaredLocalType(id.Name))
	}
	return false
}

// declaredLocalType resolves a name against the checker's local table for this
// function. Deliberately narrower than exprStaticType, which also consults the
// in-progress b.locals / b.scratchType — findTrmcFuncs runs detectTrmc on a
// builder that has neither, so a verdict reading them could differ between the
// pre-pass and lowering, and the two must agree (the pre-pass verdict is what
// the ownership ladder publishes to call sites).
func (b *builder) declaredLocalType(name string) ast.Type {
	for _, v := range b.info.Locals[b.fn] {
		if v.Name == name {
			return v.Type
		}
	}
	for _, p := range b.fn.Params {
		if p.Name == name {
			return p.Type
		}
	}
	return nil
}

// classifyTrmcBody splits a statement sequence into rc-neutral steps, guard
// clauses, and the tail that ends it.
func (b *builder) classifyTrmcBody(stmts []ast.Stmt) *trmcBody {
	if len(stmts) == 0 {
		return nil
	}
	steps, ok := b.classifyTrmcSteps(stmts[:len(stmts)-1])
	if !ok {
		return nil
	}
	tail := b.classifyTrmcTail(stmts[len(stmts)-1])
	if tail == nil {
		return nil
	}
	return &trmcBody{steps: steps, tail: tail}
}

// classifyTrmcSteps classifies the statements that run before a tail. Each is
// either a guard clause — `if (cond) { …return… }`, whose branch is a nested
// body so its `return` reaches the hole machinery — or an rc-neutral statement
// lowered by ordinary statement lowering.
func (b *builder) classifyTrmcSteps(stmts []ast.Stmt) ([]trmcStep, bool) {
	var steps []trmcStep
	for _, st := range stmts {
		if ifs, ok := st.(*ast.If); ok && ifs.Else == nil && stmtReturns(ifs.Then) {
			then := b.classifyTrmcBodyStmt(ifs.Then)
			if then == nil {
				return nil, false
			}
			steps = append(steps, trmcStep{cond: ifs.Cond, then: then})
			continue
		}
		if b.selfCallCount(st) != 0 || !b.trmcNeutralStmt(st) {
			return nil, false
		}
		steps = append(steps, trmcStep{stmt: st})
	}
	return steps, true
}

// classifyTrmcBodyStmt classifies an `if` branch (a block, or a bare statement).
func (b *builder) classifyTrmcBodyStmt(s ast.Stmt) *trmcBody {
	if blk, ok := s.(*ast.Block); ok {
		return b.classifyTrmcBody(blk.Stmts)
	}
	return b.classifyTrmcBody([]ast.Stmt{s})
}

// classifyTrmcTail classifies the LAST statement of a TRMC body.
func (b *builder) classifyTrmcTail(s ast.Stmt) *trmcTail {
	switch x := s.(type) {
	case *ast.Return:
		if x.Value == nil {
			return nil
		}
		return b.classifyTrmcReturn(x.Value)
	case *ast.If:
		// Both branches must be present: a one-armed tail `if` falls out of
		// the arm block, which is the NEXT arm's tag test — the wrong
		// continuation entirely.
		if x.Else == nil {
			return nil
		}
		then := b.classifyTrmcBodyStmt(x.Then)
		els := b.classifyTrmcBodyStmt(x.Else)
		if then == nil || els == nil {
			return nil
		}
		return &trmcTail{kind: trmcTailBranch, cond: x.Cond, then: then, els: els}
	case *ast.Block:
		body := b.classifyTrmcBody(x.Stmts)
		if body == nil || len(body.steps) > 0 {
			return nil
		}
		return body.tail
	}
	return nil
}

// classifyTrmcReturn classifies a returned expression into a tail leaf.
func (b *builder) classifyTrmcReturn(v ast.Expr) *trmcTail {
	fn := b.fn
	switch b.selfCallCount(v) {
	case 0:
		return &trmcTail{kind: trmcTailBase, baseExpr: v}
	case 1:
		call, ok := v.(*ast.Call)
		if !ok {
			return nil
		}
		cid, ok := call.Callee.(*ast.Ident)
		if !ok {
			return nil
		}
		// `return self(…)`: a plain tail call. The hole is untouched — this
		// arm's result IS the next iteration's result.
		if b.isSelfCall(call) {
			return &trmcTail{kind: trmcTailSelf, selfCall: call}
		}
		// `return Ctor(…, self(…), …)`: the modulo-cons shape.
		retEnum, ok := fn.ReturnType.(ast.EnumType)
		if !ok {
			return nil
		}
		cenum, cVarIdx, cPayloadCount, ok := b.lookupVariantOn(cid.Name, cid.EnumName)
		if !ok || cenum != retEnum.Name || cPayloadCount != len(call.Args) || cPayloadCount == 0 {
			return nil
		}
		holeIdx, self := -1, (*ast.Call)(nil)
		for i, a := range call.Args {
			if b.selfCallCount(a) == 0 {
				continue
			}
			// The self-call must BE this payload, not be buried inside it.
			sc, isCall := a.(*ast.Call)
			if !isCall || !b.isSelfCall(sc) {
				return nil
			}
			holeIdx, self = i, sc
		}
		if holeIdx < 0 {
			return nil
		}
		// The payload types size the node and place the hole; without them
		// there is no layout to build against.
		bts := b.variantPayloadTypes(call, cenum, cVarIdx)
		if len(bts) != cPayloadCount {
			return nil
		}
		return &trmcTail{
			kind:       trmcTailCons,
			ctorVarIdx: cVarIdx,
			ctorArgs:   call.Args,
			holeIdx:    holeIdx,
			buildTypes: bts,
			selfCall:   self,
		}
	}
	return nil
}

// isSelfCall reports whether call is a direct, fully-applied recursive call
// whose own arguments hold no further self-call (`self(self(t))` is two holes).
func (b *builder) isSelfCall(call *ast.Call) bool {
	id, ok := call.Callee.(*ast.Ident)
	if !ok || id.Name != b.fn.Name || call.Method != nil || len(call.Args) != len(b.fn.Params) {
		return false
	}
	for _, a := range call.Args {
		if b.selfCallCount(a) != 0 {
			return false
		}
	}
	return true
}

// bodyIsLeaf reports whether a body is a single non-branching tail with no
// preceding steps — the narrow arm shape.
func bodyIsLeaf(body *trmcBody) bool {
	return body != nil && len(body.steps) == 0 && body.tail != nil && body.tail.kind != trmcTailBranch
}

// tailKindIn reports whether any leaf of the body has kind k.
func tailKindIn(body *trmcBody, k trmcTailKind) bool {
	if body == nil {
		return false
	}
	for _, s := range body.steps {
		if tailKindIn(s.then, k) {
			return true
		}
	}
	t := body.tail
	if t == nil {
		return false
	}
	if t.kind == trmcTailBranch {
		return tailKindIn(t.then, k) || tailKindIn(t.els, k)
	}
	return t.kind == k
}

// detectTrmc returns the eligible shape, or nil if the function isn't a TRMC
// candidate.
func (b *builder) detectTrmc() *trmcShape {
	fn := b.fn
	// Plain, concrete, non-closure function returning an enum.
	if fn.Body == nil || len(fn.TypeParams) > 0 || len(fn.Captures) > 0 || b.thisIsPair {
		return nil
	}
	if _, ok := fn.ReturnType.(ast.EnumType); !ok {
		return nil
	}
	// The body must END in a `match`; everything before it is rc-neutral setup.
	if len(fn.Body.Stmts) == 0 {
		return nil
	}
	m, ok := fn.Body.Stmts[len(fn.Body.Stmts)-1].(*ast.Match)
	if !ok || m.StructMatch != "" {
		return nil
	}
	prefix, ok := b.classifyTrmcSteps(fn.Body.Stmts[:len(fn.Body.Stmts)-1])
	if !ok {
		return nil
	}
	prefixReturns := false
	for _, st := range prefix {
		if st.stmt == nil {
			prefixReturns = true
		}
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
	scrutEnum, ok := fn.Params[mp].Type.(ast.EnumType)
	if !ok {
		return nil
	}

	sh := &trmcShape{matchedParam: mp, prefix: prefix, narrow: !prefixReturns}
	anyCons := false
	for _, arm := range m.Arms {
		// Plain variant arms and the bare `_` wildcard only — no literal /
		// range / tuple pattern, and no `@` binding (the TRMC arm binds
		// payloads by hand and has nowhere to put the whole-value name).
		if arm.Literal != nil || arm.RangeHi != nil || arm.TupleElems != nil || arm.AtBinding != "" {
			return nil
		}
		if !arm.IsWildcard && arm.VariantName == "" {
			return nil
		}
		if arm.Body == nil {
			return nil
		}
		body := b.classifyTrmcBody(arm.Body.Stmts)
		if body == nil {
			return nil
		}
		ta := trmcArm{isWildcard: arm.IsWildcard, guard: arm.Guard, body: body}
		if !arm.IsWildcard {
			// The checker's stamped resolution, like the two match lowerings.
			// TRMC is an optimisation, so an unresolved arm declines the
			// transform rather than failing the build.
			if arm.EnumName == "" {
				return nil
			}
			ta.scrutVarIdx = arm.VariantIndex
			ta.bindings = arm.Bindings
			ta.bindingTypes = arm.BindingTypes
		}
		if arm.IsWildcard || arm.Guard != nil || !bodyIsLeaf(body) || body.tail.kind == trmcTailSelf {
			sh.narrow = false
		}
		if tailKindIn(body, trmcTailCons) {
			anyCons = true
		}
		sh.arms = append(sh.arms, ta)
	}
	if !anyCons {
		return nil // plainly tail-recursive at best — not modulo-cons
	}
	if !b.trmcArmsTotal(sh, scrutEnum) {
		return nil
	}
	return sh
}

// trmcArmsTotal reports whether the UNGUARDED arms alone cover every variant of
// the scrutinee enum. Falling off the last arm re-enters the loop with the
// scrutinee unchanged, so a chain a guard can exhaust would hang; the checker's
// exhaustiveness only underwrites the ordinary lowering's fall-through to the
// match join, which TRMC does not have.
func (b *builder) trmcArmsTotal(sh *trmcShape, scrutEnum ast.EnumType) bool {
	covered := map[int]bool{}
	for _, arm := range sh.arms {
		if arm.guard != nil {
			continue
		}
		if arm.isWildcard {
			return true
		}
		covered[arm.scrutVarIdx] = true
	}
	ed, ok := b.info.Enums[scrutEnum.Name]
	if !ok {
		return false
	}
	for i := range ed.Variants {
		if !covered[i] {
			return false
		}
	}
	return true
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
func (b *builder) tryEmitTrmc() (bool, error) {
	if !ast.TrmcEnabled {
		return false, nil
	}
	sh := b.detectTrmc()
	if sh == nil {
		return false, nil
	}
	return true, b.emitTrmc(sh)
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

func (b *builder) emitTrmc(sh *trmcShape) error {
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

	// The statements before the `match` are part of the function body the
	// recursion RE-ENTERS, so they belong inside the loop: hoisting them out
	// would freeze a local derived from a parameter at its first-call value
	// while the loop advances that parameter underneath it.
	if err := b.emitTrmcSteps(sh.prefix, holeSlot, resultSlot, loopD, exitD); err != nil {
		return err
	}

	for _, arm := range sh.arms {
		b.openBlock(BlockTypeVoid)
		armD := b.depth
		if !arm.isWildcard {
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
		}
		// With the bindings in locals, a `when` guard decides whether the arm
		// really matches; on false fall through to the next arm.
		if arm.guard != nil {
			if err := b.expr(arm.guard); err != nil {
				return err
			}
			b.emit(Op{Kind: OpNot})
			b.brTo(armD, true)
		}
		if err := b.emitTrmcBody(arm.body, holeSlot, resultSlot, loopD, exitD); err != nil {
			return err
		}
		b.closeScope() // end arm block
	}
	b.closeScope() // end loop
	b.closeScope() // end exit block

	b.emit(Op{Kind: OpLoadLocal, I32: resultSlot})
	b.emit(Op{Kind: OpReturn})
	return nil
}

// emitTrmcBody lowers a classified body: its rc-neutral steps and guard
// clauses, then the tail that leaves or re-enters the loop.
func (b *builder) emitTrmcBody(body *trmcBody, holeSlot, resultSlot, loopD, exitD int32) error {
	if err := b.emitTrmcSteps(body.steps, holeSlot, resultSlot, loopD, exitD); err != nil {
		return err
	}
	return b.emitTrmcTail(body.tail, holeSlot, resultSlot, loopD, exitD)
}

// emitTrmcSteps lowers the statements before a tail: rc-neutral statements
// through ordinary lowering, guard clauses as a nested body under an `if`.
func (b *builder) emitTrmcSteps(steps []trmcStep, holeSlot, resultSlot, loopD, exitD int32) error {
	for _, s := range steps {
		if s.stmt != nil {
			if err := b.stmt(s.stmt); err != nil {
				return err
			}
			// The neutrality gate keeps rc-tracked locals out, so these tables
			// are empty here; splicing them anyway keeps the TRMC path honest
			// if the gate ever widens.
			for _, name := range b.rc.nestedDrops[s.stmt] {
				b.emitPreciseDrop(name)
			}
			continue
		}
		if err := b.expr(s.cond); err != nil {
			return err
		}
		b.openIf(BlockTypeVoid)
		if err := b.emitTrmcBody(s.then, holeSlot, resultSlot, loopD, exitD); err != nil {
			return err
		}
		b.closeScope()
	}
	return nil
}

func (b *builder) emitTrmcTail(tail *trmcTail, holeSlot, resultSlot, loopD, exitD int32) error {
	switch tail.kind {
	case trmcTailBase:
		valSlot := b.allocSlot()
		b.scratchType[valSlot] = b.fn.ReturnType
		if err := b.expr(tail.baseExpr); err != nil {
			return err
		}
		b.emit(Op{Kind: OpStoreLocal, I32: valSlot})
		b.emitHoleLink(valSlot, holeSlot, resultSlot)
		b.brTo(exitD, false) // break
	case trmcTailCons:
		if err := b.emitTrmcConsNode(tail, holeSlot, resultSlot); err != nil {
			return err
		}
		if err := b.emitTrmcAdvance(tail.selfCall); err != nil {
			return err
		}
		b.brTo(loopD, false) // continue
	case trmcTailSelf:
		// A plain tail call: the hole is already where this arm's result
		// belongs, so only the parameters advance.
		if err := b.emitTrmcAdvance(tail.selfCall); err != nil {
			return err
		}
		b.brTo(loopD, false) // continue
	case trmcTailBranch:
		if err := b.expr(tail.cond); err != nil {
			return err
		}
		b.openIf(BlockTypeVoid)
		if err := b.emitTrmcBody(tail.then, holeSlot, resultSlot, loopD, exitD); err != nil {
			return err
		}
		b.elseBranch()
		if err := b.emitTrmcBody(tail.els, holeSlot, resultSlot, loopD, exitD); err != nil {
			return err
		}
		b.closeScope()
	}
	return nil
}

// emitTrmcConsNode builds the node for a modulo-cons tail with its hole
// payload left unwritten, links it into the previous hole (or the result), and
// advances the hole to that unwritten field.
func (b *builder) emitTrmcConsNode(tail *trmcTail, holeSlot, resultSlot int32) error {
	payloadCount := len(tail.buildTypes)
	poffs, psize := payloadLayout(tail.buildTypes, payloadCount, b.ptrW)

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
	b.emit(Op{Kind: OpConstI32, I32: int32(tail.ctorVarIdx)})
	b.emit(Op{Kind: OpStore})
	// Every payload but the hole.
	for i := 0; i < payloadCount; i++ {
		if i == tail.holeIdx {
			continue
		}
		pt := tail.buildTypes[i]
		if err := b.expr(tail.ctorArgs[i]); err != nil {
			return err
		}
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
	// hole to the node's still-unwritten payload field.
	b.emitHoleLink(nodeDataSlot, holeSlot, resultSlot)
	b.emit(Op{Kind: OpLoadLocal, I32: nodeDataSlot})
	b.emit(Op{Kind: OpConstI32, I32: poffs[tail.holeIdx]})
	b.emit(Op{Kind: OpAdd})
	b.emit(Op{Kind: OpStoreLocal, I32: holeSlot})
	return nil
}

// emitTrmcAdvance rebinds the parameters to the recursive call's arguments so
// the next iteration runs on them. Evaluate every argument into a temp FIRST
// (they read the current params / bindings), then store the temps into the
// param slots — so a self-call that permutes or increments a scalar param
// can't clobber an input it still needs.
func (b *builder) emitTrmcAdvance(selfCall *ast.Call) error {
	tmp := make([]int32, len(b.fn.Params))
	for j := range b.fn.Params {
		if err := b.expr(selfCall.Args[j]); err != nil {
			return err
		}
		ts := b.allocSlot()
		b.scratchType[ts] = b.fn.Params[j].Type
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
	return nil
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
//
// The NARROW shape gate is the other half: the loop releases a cell only on the
// advance, so every path that leaves the loop early — a guard-false that lands
// in a later base arm, a branch tail whose base leaf fires mid-list, a `return`
// before the loop — walks away from the cells still ahead of it, which the
// caller has already retained for us. Those widened shapes still get TRMC; they
// just keep the borrow model.
func (b *builder) trmcShapeConsumeSafe(sh *trmcShape) bool {
	if sh == nil || !sh.narrow {
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
		if arm.body.tail.kind != trmcTailCons {
			continue
		}
		selfCall := arm.body.tail.selfCall
		// The self-call must advance the matched param to a bare binding ident.
		if mp >= len(selfCall.Args) {
			return false
		}
		adv, ok := selfCall.Args[mp].(*ast.Ident)
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
