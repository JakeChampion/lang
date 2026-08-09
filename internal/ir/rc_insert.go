package ir

// Perceus RC insertion — the Op-emitting half of reference counting,
// carved out of the AST→IR lowering builder (issue #4393, slice 3 of the
// RC-pass extraction; see docs/RC-NATIVE-PASS-EXTRACTION.md).
//
// Where rc_analysis.go DECIDES (the per-function rcPlan), this file EMITS:
// the function-exit dec sweep, precise drops, owned-temp stack drops,
// alias incs, reinit-overwrite drops, reuse-site old-field drops — plus
// the drop-specialisation subsystem: dropFnNameFor's routing of each
// rc-tracked shape to a generated `__drop_*` function and the gen*DropFn
// bodies the LowerWith worklist materialises. Everything here is still
// CALLED from lowering sites in ir.go (in-build insertion); converting
// the structural-boundary pieces to true post-lowering IR→IR insertion
// is the follow-up slice.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// insertConsumedParamEntryIncs splices the consumed-threaded param entry
// retain-incs into the LOWERED op stream, at the prologue boundary `at`
// (right after the rc-slot / defer-flag zero-init) — the first RC insertion
// converted from in-build emission to a true post-lowering []Op insertion
// (#4393 slice 4). A promoted consumed param (see computeConsumedParams) is
// reclaimed callee-side — its reassignment-overwrites and the exit sweep
// deep-drop it — but the caller passes it BORROWED (no caller-side retain
// inc). One retain inc at entry gives the callee an owned reference: the
// first overwrite-dec (or the exit-sweep dec on a path that never
// reassigns) balances it, while a still-shared value stays rc>1 and is only
// flat-dec'd (never freed out from under the caller). Gated on freeEligible
// — if the escape analysis re-tainted the param (it flows into a retain
// sink) it is not deep-dropped, so no entry-inc is owed.
//
// ARRAY params are the exception: an entry retain would cost them the
// in-place append, so they express the same ownership as an explicit bit
// (isConsumedArrayParam / emitConsumedArrayOverwriteDec) instead.
//
// `pos` is the builder's curPos at the boundary (the zero Position at
// function entry) so the spliced ops carry exactly the source positions the
// in-build emission stamped. The splice allocates no slots and shifts only
// op indices, which nothing records absolutely (control flow is
// depth-relative), so the emitted stream is byte-identical to the old
// in-build path.
func (b *builder) insertConsumedParamEntryIncs(at int, pos ast.Position) {
	if !ast.RcFreeEnabled {
		return
	}
	var incs []Op
	for _, p := range b.fn.Params {
		// The entry inc must fire for EVERY consumed param, not just the
		// freeEligible ones: the reassignment-overwrite dec is emitted
		// unconditionally (the Assign catch-all), so a consumed param that
		// borrow-taint kept out of freeEligible would otherwise dec the
		// caller's borrowed box without ever having retained it — the
		// count-steal that let a caller-side is_unique-gated drop (e.g. a
		// destructure temp's deep drop) free a still-referenced box (the
		// self-host `(Stmt, Par)` cursor loops crashed exactly this way).
		// A consumed-but-not-eligible param is NOT swept at exit (the
		// sweep stays freeEligible-gated), so its final value carries one
		// extra count out — a bounded safe leak, matching the existing
		// leak-over-UAF stance elsewhere in this file.
		if !b.rc.consumedParams[p.Name] {
			continue
		}
		// ARRAY params buy the same balance with a hidden ownership flag
		// instead of a retain, because the retain would cost them the
		// in-place append. See isConsumedArrayParam.
		if b.isConsumedArrayParam(p.Name) {
			continue
		}
		slot, ok := b.locals[p.Name]
		if !ok {
			continue
		}
		incs = append(incs,
			Op{Kind: OpLoadLocal, I32: slot, Pos: pos},
			Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1, Pos: pos},
			Op{Kind: OpDrop, Pos: pos})
	}
	if len(incs) == 0 {
		return
	}
	ops := b.out.Ops
	b.out.Ops = append(ops[:at:at], append(incs, ops[at:]...)...)
}

// freshOwnedRcTempType classifies an expression that, when evaluated,
// materialises a FRESH owned rc-tracked temporary — a brand-new box / heap
// string that aliases no borrowed value. It returns the value's static type
// and true for exactly the unambiguous fresh-ALLOCATING shapes that
// rhsTainted / computeFreeEligible treat as untainted-owned: array / struct
// / tuple literals, string concat (Binary.IsStringConcat), and string slice
// (SliceExpr whose result is a string — the runtime copies into a new owned
// buffer). These are exactly the RHS shapes that make a bound `var t = …`
// freeEligible, so DEC'ing such a temp is as safe as the already-shipped
// exit-sweep dec of that bound var.
//
// Two consuming sites use it to reclaim the unbounded statement-temporary
// leak (docs/RC-PERCEUS-PLAN.md):
//   - stage (a): a discarded bare-ExprStmt `a + b;` / `[x, y];` decs in place
//     (emitOwnedTempStackDrop) instead of OpDrop'ing the allocation.
//   - stage (b): an owned-temp passed as a borrowed arg to a non-retain-sink
//     call (`foo(a + b)`) is stashed and dec'd after the call.
//
// Ident / field / index reads are borrowed VIEWS (the owner upstream or the
// exit sweep accounts for them) and are never matched — dec'ing one would
// over-release. Calls are excluded — a method call can alias its receiver
// (`arr.push(x)` returns the receiver buffer), so dec'ing its result would
// double-free. MakeClosure is excluded for now (a bare closure temp is
// effectively nonexistent, and the per-closure capture-drop thunk is keyed
// by local name) — those keep their prior plain handling.
// appendCopyTempType classifies a call ARGUMENT that is an `.append` whose
// lowering takes the #4827 forced-copy path (appendForcesCopy: bare-ident
// operand, reused after, non-self-reassign, outside #4849's in-place
// exemptions). The general Call exclusion in freshOwnedRcTempType is
// because a push can return its receiver's buffer in place — but a
// FORCED-COPY push provably returns a fresh buffer (the operand's rc is
// bumped across the grow precisely so the helper's copy path runs).
// Handed to a borrowing call, that fresh buffer is otherwise never freed —
// the #4357 "consumed by a borrowing call" leak class, one whole array
// copy per call (TestX86_64AppendCopyLeakBound pinned 5000 iterations of
// `take(path.append(i))` leaking unboundedly). #4849's exemptions removed
// the self-host compile's own hot shapes; this recognizer reclaims the
// remaining arg-position copies.
//
// Restricted to SCALAR-element arrays (the `path.append(k)` i32[] shape):
// the grow's copy path memcpys elements WITHOUT retains, so a pointer-
// element copy's elements alias the original's — and emitOwnedSlotDrop
// routes pointer-element arrays through the DEEP per-element drops
// (__drop_arr_*, the string walk), which would over-release the
// original's elements. A scalar element falls through to the plain
// shallow __fern_arr_dec — exactly the sound free. Pointer-element
// forced copies keep their prior safe-leak.
func (b *builder) appendCopyTempType(e ast.Expr) (ast.Type, bool) {
	if !ast.RcFreeEnabled {
		return nil, false
	}
	c, ok := e.(*ast.Call)
	if !ok {
		return nil, false
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok || id.Name != "__method_Array_push" || len(c.Args) != 2 || len(c.TypeArgs) != 1 {
		return nil, false
	}
	if ast.IsPointerType(c.TypeArgs[0]) {
		return nil, false // pointer elements: shallow-free-unsafe, keep the leak
	}
	if !b.appendForcesCopy(c) {
		return nil, false // in-place fast path: result aliases the receiver
	}
	return ast.ArrayType{Elem: c.TypeArgs[0]}, true
}

func (b *builder) freshOwnedRcTempType(e ast.Expr) (ast.Type, bool) {
	if !ast.RcFreeEnabled {
		return nil, false
	}
	switch x := e.(type) {
	case *ast.ArrayLit, *ast.StructLit, *ast.TupleLit:
		if t := b.exprType(e); ast.IsPointerType(t) {
			return t, true
		}
	case *ast.Binary:
		if x.IsStringConcat {
			if t, ok := b.exprType(e).(ast.StringType); ok {
				return t, true
			}
		}
	case *ast.SliceExpr:
		if t, ok := b.exprType(e).(ast.StringType); ok {
			return t, true
		}
	}
	return nil, false
}

// ownedCallResultType classifies an expression that is a direct call to a
// USER function returning a pointer-shaped (rc-tracked) value — a fresh
// struct / array / string / enum the callee owns. Two consuming sites reclaim
// it (otherwise it's dropped on the floor and leaks every iteration):
//   - a discarded ExprStmt `mk(i);` (leaked 800 → 80000 in a loop) — dec'd
//     in place via the is_unique-gated emitOwnedTempStackDrop;
//   - a call ARG `take(mk(i))` / `outer(inner(i))` (leaked 800 → 80000 /
//     1600 → 160000) — stashed and dec'd after the enclosing scalar-
//     returning call via emitOwnedSlotDrop, alongside the literal-shape
//     temps freshOwnedRcTempType already handles.
//
// It returns the result's static type + true.
//
// Safety rests on the is_unique gate inside every emitOwnedTempStackDrop
// branch: the dec only FREES a uniquely-owned (rc==1) result; an aliased
// return (a function handing back a param / field) carries the return-
// transfer inc, so its rc is >= 2 and the gate merely decs it — never frees
// a value the caller's source still owns. This is exactly the shipped
// `var t = call(); /* t unused */` exit-sweep dec (computeFreeEligible marks
// such a t eligible), so it inherits that proven safety.
//
// Excluded — the callees that hand back an UNCOUNTED rc==1 alias the
// is_unique gate cannot distinguish from a fresh value:
//   - `__`-prefixed builtins / method lowerings: `arr.push(x)` /
//     `m.set(k, v)` return the receiver's buffer in place at rc==1 (no
//     inc), so dec'ing would free a live container.
//   - variant constructors (`Some(p)`): not in FuncSigs, and they store a
//     borrowed payload uncounted.
//   - pair-form callees: return a (tag, payload) pair, a different stack
//     shape.
//   - indirect (function-typed local) callees: unknown body / borrow shape.
func (b *builder) ownedCallResultType(e ast.Expr) (ast.Type, bool) {
	if !ast.RcFreeEnabled {
		return nil, false
	}
	call, ok := e.(*ast.Call)
	if !ok {
		return nil, false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return nil, false
	}
	if _, isLocal := b.locals[id.Name]; isLocal {
		return nil, false
	}
	if _, ok := b.info.FuncSigs[id.Name]; !ok {
		return nil, false // not a known function (excludes variant constructors)
	}
	// Only USER-DECLARED functions qualify (the oracle map keys every decl in
	// prog.Funcs, true or false). Source-level BUILTINS live in FuncSigs too
	// without a `__` prefix — e.g. `random_bytes(n)`, whose darwin helper
	// returns a buffer allocated WITHOUT an rc header, so the is_unique gate
	// would read a garbage header word and free through it (the
	// `random_bytes(32).len()` receiver crash). A builtin's allocation
	// contract is per-helper, not the user-fn return-transfer model this
	// reclaim's safety argument rests on.
	if _, isUserFn := b.returnsNoParamEscape[id.Name]; !isUserFn {
		return nil, false
	}
	if strings.HasPrefix(id.Name, "__") {
		// `__`-prefixed callees are method lowerings. The builtin ones
		// (`arr.push` / `m.set` / string / Reader / …) return the receiver's
		// buffer in place at rc==1 (an uncounted alias), so reclaiming would free
		// a live container. A USER method PROVEN to return a fresh value
		// (returnsNoParamEscape — e.g. a recursive `map`/`dup` over an enum) is
		// as safe to reclaim as a fresh free-function result; without this, a
		// method-call result used as an arg (`sum(xs.dup())`) leaks every call.
		if !b.returnsNoParamEscape[id.Name] {
			return nil, false
		}
	}
	if b.pairForm[id.Name] {
		return nil, false
	}
	t := b.exprType(e)
	if t == nil {
		return nil, false
	}
	// A MAP result is only reclaimable when the callee is PROVEN to return a
	// fresh map (the returnsNoParamEscape oracle — a cow-threaded builder
	// whose handle never aliases a param). The generic is_unique-gate
	// argument used for struct/array/enum results ("an aliased return is
	// rc>=2 via the return-transfer inc") is not relied on for maps, and
	// emitMapSlotDrop deep-frees the value column, so an unproven callee
	// returning a still-owned map (e.g. `return m` of a param) must keep its
	// prior safe-leak.
	if st, isStruct := t.(ast.StructType); isStruct && st.Name == "Map" {
		if !b.returnsNoParamEscape[id.Name] {
			return nil, false
		}
		return t, true
	}
	if !ast.IsPointerType(t) {
		return nil, false
	}
	return t, true
}

// reclaimableMatchScrutinee reports whether a match's scrutinee `tag` is a
// FRESH owned enum box that can be freed once the match completes, and returns
// its enum type. A match consumes its scrutinee — the arms read payload fields
// out of the box and then it's dead — but the box is never dec'd, so a
// `match (mk(i)) { … }` over a per-iteration-fresh `mk(i)` leaks one box every
// iteration (the value-consuming-position sibling of the shipped index-of-fresh
// / `.len()`-of-fresh reclamation; docs/RC-PERCEUS-PLAN.md).
//
// Eligibility mirrors that family's gate exactly:
//   - the scrutinee is a fresh owned call result (ownedCallResultType — a user
//     function returning a heap-boxed enum; pair-form / builtin / variant-
//     constructor callees are excluded there, so the value is always a real
//     box this lowering stores in ptrSlot and dispatches on via OpMatchTag);
//   - every NAMED arm binding is non-pointer, so no pointer payload is
//     extracted into a binding that would outlive (and alias) the freed box.
//     The drop here is DEEP (__drop_enum_<Name> releases payloads), so a
//     surviving binding dangles rather than merely leaking — this refusal is a
//     soundness requirement, not the conservatism the self-host's shallow-dec
//     sibling was widened out of. A `_` position is exempt: it extracts
//     nothing;
//   - for the expression form, the RESULT is non-pointer too (`resultType`;
//     pass nil for the statement form, which yields no value).
//
// emitEnumSlotDrop then frees the box under an is_unique gate, so an aliased
// scrutinee (rc>1 via a return-transfer inc) is only dec'd, never freed.
//
// bindingNames / bindingTypes are parallel per-arm slices: the statement and
// expression forms carry different arm types (*ast.MatchArm vs
// *ast.MatchExprArm).
func (b *builder) reclaimableMatchScrutinee(tag ast.Expr, bindingNames [][]string, bindingTypes [][]ast.Type, resultType ast.Type) (ast.EnumType, bool) {
	if !ast.RcFreeEnabled {
		return ast.EnumType{}, false
	}
	t, ok := b.ownedCallResultType(tag)
	if !ok {
		return ast.EnumType{}, false
	}
	et, ok := t.(ast.EnumType)
	if !ok {
		return ast.EnumType{}, false
	}
	if resultType != nil && ast.IsPointerType(resultType) {
		return ast.EnumType{}, false
	}
	for a, bts := range bindingTypes {
		for i, bt := range bts {
			if bt == nil || !ast.IsPointerType(bt) {
				continue
			}
			// `_` extracts nothing, so no binding outlives the free.
			if a < len(bindingNames) && i < len(bindingNames[a]) && bindingNames[a][i] == "_" {
				continue
			}
			return ast.EnumType{}, false
		}
	}
	return et, true
}

// reclaimablePairFormPayload reports whether a PAIR-FORM match's payload
// binding is a fresh owned heap value the arm can release once its body ends,
// and returns the binding's type.
//
// The pair-form return ABI hands back `(tag, payload)` in registers with no
// box, so reclaimableMatchScrutinee — which frees a box — has nothing to free
// and declines. A pair-form payload is nonetheless allowed to be POINTER-shaped
// (isPairFormPayloadShape admits array / slice / struct / tuple), and for those
// the register IS the only reference: the callee allocated it, the match binds
// it as a borrow, and nobody owns it. `match (mk()) { Some(v) => { … } }` over
// a per-iteration-fresh `mk()` therefore leaked the whole payload every
// iteration — the idiomatic lookup-then-read shape, growing without bound.
//
// Eligibility is deliberately tighter than the box path's:
//   - the callee is PROVEN to return a value that aliases no parameter
//     (returnsNoParamEscape true, not merely present). The box path can lean on
//     "an aliased return is rc>=2 via the return-transfer inc, and the free is
//     is_unique-gated"; with no box there is no such inc to lean on, so the
//     payload's freshness has to be proven outright;
//   - the binding is CONFINED to its arm (pairFormPayloadConfined), so the
//     pointer cannot outlive the release.
//
// The release itself is emitOwnedSlotDrop — the same type-directed deep drop
// the loop-var reinit path uses — so a struct payload routes through its
// is_unique-gated __drop_struct_<N> and an array through __fern_arr_dec.
func (b *builder) reclaimablePairFormPayload(tag ast.Expr, bt ast.Type, body ast.Stmt, name string) bool {
	if !ast.RcFreeEnabled || bt == nil || !ast.IsPointerType(bt) {
		return false
	}
	// Not ownedCallResultType: that gate rejects a pair-form callee outright
	// (b.pairForm), which is exactly the set this one is for. The checks it
	// shares are spelled out instead — a direct call to a user-declared,
	// non-builtin function proven to return no parameter's heap.
	call, ok := tag.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	if _, isLocal := b.locals[id.Name]; isLocal {
		return false
	}
	if strings.HasPrefix(id.Name, "__") {
		return false
	}
	if !b.returnsNoParamEscape[id.Name] {
		return false
	}
	return pairFormPayloadConfined(body, name)
}

// pairFormPayloadConfined reports whether every mention of `name` in `body` is
// a plain read THROUGH the value — the target of a field access or the base of
// an index — and so cannot let the pointer outlive the arm.
//
// It is a whitelist, not a blacklist: an occurrence the walk does not recognise
// as one of those two shapes counts as an escape. That deliberately declines
// shapes that are often fine (passing the binding to a function that only
// reads it, printing it) in exchange for not having to enumerate the sinks —
// a missed reclaim is the leak we already have, while a missed escape is a
// use-after-free. A shadowing declaration inside the arm errs the same safe
// way: its uses are attributed to the binding, so anything the shadow does
// with the name suppresses the release.
func pairFormPayloadConfined(body ast.Stmt, name string) bool {
	if body == nil {
		return false
	}
	excused := map[*ast.Ident]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FieldAccess:
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == name {
				excused[id] = true
			}
		case *ast.Index:
			if id, ok := x.Array.(*ast.Ident); ok && id.Name == name {
				excused[id] = true
			}
		}
		return true
	})
	confined := true
	ast.Walk(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name && !excused[id] {
			confined = false
		}
		return true
	})
	return confined
}

// reclaimableTryScrutinee reports whether a `?`'s source Option/Result box is
// a FRESH owned call result the TryOp lowering can free once the success
// payload is extracted — the value-consuming-position sibling of
// reclaimableMatchScrutinee (`mk()?` leaked one box per iteration on the
// success path; the failure path forwards the box (Result) or reads a static
// None sentinel (Option), so only the success edge leaks). Returns the enum
// type for emitTryBoxFree.
//
// Eligibility:
//   - the inner is a fresh owned call result (ownedCallResultType — a user
//     function returning a heap-boxed Option/Result; pair-form / builtin /
//     variant-constructor callees are excluded there, so the value is always
//     a real box the lowering stores in ptrSlot);
//   - the success payload (`n.Type`) is a NON-POINTER scalar or a STRING. A
//     scalar copy can't alias the freed box. A string payload is MOVED out:
//     the box's payload reference transfers to the extracted value —
//     construction-side alias-incs under EnumRcPayloads make that reference
//     counted, so rhsTainted's TryOp case credits the binding as owned and
//     the exit sweep balances it. Other pointer payloads (struct / array /
//     tuple / enum / Map) keep today's sound box+payload leak until their
//     ownership-transfer story is wired;
//   - the enum is EnumRcPayloads-eligible, so an aliased payload (`Ok(pre)`)
//     was inc'd at construction — the move hands the binding a counted
//     reference, never an uncounted borrow.
//
// The free itself (emitTryBoxFree) is is_unique-gated, so an aliased box (a
// callee returning its param, rc>=2 via the return-transfer inc) is only
// dec'd, never freed.
func (b *builder) reclaimableTryScrutinee(n *ast.TryOp) (ast.EnumType, bool) {
	if !ast.RcFreeEnabled {
		return ast.EnumType{}, false
	}
	t, ok := b.ownedCallResultType(n.Inner)
	if !ok {
		return ast.EnumType{}, false
	}
	et, ok := t.(ast.EnumType)
	if !ok {
		return ast.EnumType{}, false
	}
	if !b.enumRcPayloadsEligible(et.Name) {
		return ast.EnumType{}, false
	}
	if n.Type == nil {
		return ast.EnumType{}, false
	}
	if ast.IsPointerType(n.Type) {
		if _, isStr := n.Type.(ast.StringType); !isStr {
			return ast.EnumType{}, false
		}
	}
	return et, true
}

// emitOwnedTempStackDrop releases a FRESH owned rc temporary whose value is
// currently on top of the operand stack — the stage-(a) replacement for the
// plain OpDrop a discarded ExprStmt would otherwise emit (see
// freshOwnedRcTempType). The value aliases nothing borrowed and escapes
// nowhere, so a single dec is exactly balanced (rc==1 → free). It mirrors
// the per-type drop bodies the exit sweep / emitVarReinitDropOld use, but
// consumes the value in place rather than from a named slot — so the only
// shape needing a scratch slot is the plain-element tuple, whose inline
// is_unique + box_free reads the box pointer twice.
func (b *builder) emitOwnedTempStackDrop(t ast.Type) {
	switch ty := t.(type) {
	case ast.StringType:
		// Mirrors the exit-sweep / reinit string branch exactly (slice 5g is
		// done): two-word ABIs (wasm + arm64) free via __fern_str_dec, which
		// consumes the (data, len) pair on the stack and returns the data ptr
		// (dropped after); native single-word x86_64 via __fern_str_dec —
		// frees the buffer at rc==1, else defers to __fern_rc_dec. The
		// guards (inline-SSO / literal sentinel / below-heap / rc>1) keep
		// every source safe, and this drop only fires for a provably-fresh
		// owned temp (sole owner), so the free is exactly balanced.
		if ast.UseTwoWordStrings(b.ptrW) {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else if b.ptrW == 8 {
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.ArrayType:
		// An array-of-(struct / tuple / primitive-array) routes to the deep
		// per-element drop fn (1 arg); a plain / pointer-element array frees
		// its buffer via __fern_arr_dec(ptr, elemSize). Same dispatch the
		// exit sweep / reinit use (arrElemStructDropName), so element
		// reclamation matches the bound-var case.
		if dropName, ok := arrElemStructDropName(ty.Elem, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
			b.emit(Op{Kind: OpCallDirect, Str: dropName, I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else {
			b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(ty.Elem, b.ptrW))})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.StructType, ast.EnumType:
		// A discarded MAP result (`mk(i);` where mk is a proven-fresh map
		// builder — ownedCallResultType requires the returnsNoParamEscape
		// oracle for map results) reclaims through the same emitMapSlotDrop
		// the exit sweep / loop-reinit use: value column + string keys +
		// buf + handle, every helper self-guarding on the map's own rc==1.
		// Needs a scratch slot (the drop reads the handle several times),
		// like the plain-element tuple below. Without this the StructType
		// arm's dropFnNameFor declined Maps and the flat rc_dec leaked the
		// whole map every call (#4357's discarded-map shape).
		if st, isStruct := ty.(ast.StructType); isStruct && st.Name == "Map" {
			slot := b.allocSlot()
			b.locals[fmt.Sprintf("__tmpdrop_%d", slot)] = slot
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
			b.emitMapSlotDrop(slot, st)
			break
		}
		// A droppable struct / enum recurses through its generated __drop_*
		// fn (1 arg); types dropFnNameFor declines (non-uniform generics)
		// fall back to the flat one-level rc_dec — leak-but-never-UAF,
		// exactly as the slot-drop sibling.
		if name, ok := dropFnNameFor(ty, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else {
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
		}
	case ast.TupleType:
		// A needs-drop tuple has a generated __drop_tuple_<mangled> fn (1
		// arg). A plain-element tuple's inline is_unique + box_free reads the
		// box pointer twice, so stash it in a scratch slot and route through
		// the shared emitTupleSlotDrop (single-word box pointer → a normal
		// i32 scratch slot is exact).
		if name, ok := dropFnNameFor(ty, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
			b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
			b.emit(Op{Kind: OpDrop})
		} else {
			slot := b.allocSlot()
			b.locals[fmt.Sprintf("__tmpdrop_%d", slot)] = slot
			b.emit(Op{Kind: OpStoreLocal, I32: slot})
			b.emitTupleSlotDrop(slot, ty)
		}
	default:
		// Not an rc-tracked shape we reclaim here — keep the plain drop.
		b.emit(Op{Kind: OpDrop})
	}
}

func (b *builder) emitRcDecLocalsAtExit() {
	b.emitRcDecLocalsAtExitExcept("")
}

// emitRcDecLocalsAtExitExcept is emitRcDecLocalsAtExit but skips the
// dec for one named local. The Return lowering uses this for the
// move-on-return optimization: when a function returns a bare
// rc-tracked local, the return-transfer inc and that local's
// exit-sweep dec cancel (the inc exists only to survive the sweep), so
// emitting neither leaves the returned value at the same rc — fewer rc
// ops, identical result. `exclude == ""` decs every owned local.
func (b *builder) emitRcDecLocalsAtExitExcept(exclude string) {
	// Local aliases so existing call sites stay unchanged; the bodies were
	// promoted to *builder methods (decValueOnStack / dropStructField) so the
	// shared emitEnumSlotDrop can reuse them.
	decValueOnStack := b.decValueOnStack
	dropStructField := b.dropStructField
	_ = decValueOnStack
	emitDec := func(slot int32, t ast.Type, eligible bool, name string) {
		// `eligible` is the borrow-aware verdict for THIS local: true
		// only when it's a proven-OWNED array (computeFreeEligible).
		// Arrays of pointer-shaped rc-tracked elements route through
		// __fern_drop_arr_ptr (which walks + dec's the elements and,
		// flag-on, frees the buffer) ONLY when eligible; an ineligible
		// (borrowed-derived) array uses a plain dec — never freeing a
		// buffer a live borrow still holds. The helper carries the
		// null / low-address / sentinel guards.
		// `dyn Trait[]` locals (#4351): route through decValueOnStack so the
		// eligible exit-sweep release walks the elements via
		// __drop_arr_dyn_<set> (per-element __drop_dyn — concrete dtor +
		// cell free) before freeing the buffer, exactly like the loop-var
		// reinit path already did. arrElemIsRcTracked deliberately excludes
		// DynTraitType (dyn cells carry no rc header, so construction must
		// not inc them), so without this arm a FUNCTION-local dyn array fell
		// to the buffer-only dec below and leaked every element (cell +
		// concrete + transitively-owned strings) on each call. `eligible`
		// (computeFreeEligible) plus __drop_arr_dyn's own rc==1 gate keep an
		// aliased/escaping array a sound leak, never a double-free. NATIVES
		// ONLY (ptrW==8): wasm's inline two-word `dyn` elements double-drop
		// when an element was bound out (`for s in xs` + a call arg) — the
		// same hazard dropStructField's dyn arm documents — so wasm keeps
		// the buffer-only dec here (status-quo sound leak; its loop-reinit
		// element walk is unchanged).
		if at, ok := t.(ast.ArrayType); ok {
			_, elemIsDyn := at.Elem.(ast.DynTraitType)
			if arrElemIsRcTracked(at.Elem) || (elemIsDyn && b.ptrW == 8 && b.dynReclaim() && ast.RcFreeEnabled && eligible) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				decValueOnStack(t, eligible)
				return
			}
		}
		// Phase 3 step-4: a plain array (primitive elements — i32[],
		// u8[], …) frees its buffer at the last reference when the
		// freelist is on AND it's an owned local. __fern_arr_dec
		// carries the same guards as __fern_rc_dec. Ineligible /
		// flag-off arrays fall through to the plain box dec.
		// string[] on any two-word ABI (wasm + arm64-TwoWordOverride):
		// reclaim each element via the two-word walk in
		// __fern_drop_arr_str, then free the buffer. Gated eligible —
		// a borrowed string[] never frees its elements.
		if at, ok := t.(ast.ArrayType); ok && ast.RcFreeEnabled && eligible {
			if _, isStr := at.Elem.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_str", I32: 2})
				b.emit(Op{Kind: OpDrop})
				return
			}
			// Native single-word string[] (x86_64, !TwoWordOverride): each
			// element is a single pointer; __fern_drop_arr_ptr walks +
			// __fern_rc_dec's each one. arm64 / wasm two-word ABIs take
			// the __fern_drop_arr_str branch above.
			if _, isStr := at.Elem.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_drop_arr_str", I32: 2})
				b.emit(Op{Kind: OpDrop})
				return
			}
		}
		if at, ok := t.(ast.ArrayType); ok && ast.RcFreeEnabled && eligible {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, b.ptrW))})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// Two-word string reclamation (wasm). A string local occupies one
		// IR logical slot that OpLoadLocal fans into (data, len); the
		// catch-all rc_dec below would consume only one of the two values
		// (corrupting the stack) and dec a two-word value as a single
		// pointer, so strings MUST return here, never fall through. An
		// ELIGIBLE string (a fresh owned concat/slice result — rhsTainted
		// whitelists exactly those, both of which COPY into a new headered
		// buffer) frees via __fern_str_dec (inline no-op / rc==1 box_free /
		// else dec). Ineligible strings (aliases / views / literals,
		// tainted) are SKIPPED entirely — never touched, so a view string
		// can never be misread/freed.
		if _, isStr := t.(ast.StringType); isStr {
			// Two-word string ABIs (wasm + arm64-TwoWordOverride): __fern_str_dec
			// consumes (data, len), returns data; drop the returned ptr.
			// Native single-word (x86_64, !TwoWordOverride): __fern_rc_dec
			// consumes ptr, returns ptr (SSO inline-tag low-bit guard +
			// literal sentinel + low-address guard all safe).
			if ast.RcFreeEnabled && eligible && ast.UseTwoWordStrings(b.ptrW) {
				b.emit(Op{Kind: OpLoadLocal, I32: slot}) // pushes (data, len)
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
				b.emit(Op{Kind: OpDrop}) // drop the returned data ptr
			} else if ast.RcFreeEnabled && eligible && b.ptrW == 8 {
				b.emit(Op{Kind: OpLoadLocal, I32: slot}) // pushes single data ptr
				// Native single-word: free the owned buffer at rc==1 via
				// __fern_str_dec (else defer to __fern_rc_dec). Gated
				// `eligible` (proven-owned, alias-free), so freeing at exit
				// is balanced; inline / literal / sentinel short-circuit.
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
				b.emit(Op{Kind: OpDrop}) // drop the returned ptr
			}
			return
		}
		// `dyn Trait` reclamation (docs/DYN-TRAITS.md §4.4). The per-set
		// __drop_dyn_<set> helper reads the vtable's trailing drop slot and
		// dispatches the concrete destructor on `data` (freeing it + anything
		// it transitively owns, e.g. a String field). The concrete dtor
		// self-guards on rc==1 and the drop slot null-guards, so this is safe
		// whether the concrete is shared or unique; the static vtable word is
		// never dec'd. Borrowed `dyn` params never reach here (the param sweep
		// skips non-owned params), so a dispatched-only borrow is never
		// dropped — no double-free. Two representations:
		//   - wasm (ptrW==4, slice 4a): the slot is the inline two-word
		//     `[data, vtable]`; OpLoadLocal fans both words out (isTwoWordType)
		//     and the helper takes 2 args.
		//   - x86-64 (boxed, slice 4b): the slot is one word (the cell ptr);
		//     OpLoadLocal pushes it and the helper takes 1 arg, reloading
		//     data/vtable from the cell and freeing the cell.
		// arm64 leaks `dyn` (no helper, slice 4c) and never sets rcTracked,
		// so this arm is unreached there.
		if dt, ok := t.(ast.DynTraitType); ok && b.dynReclaim() {
			b.emit(Op{Kind: OpLoadLocal, I32: slot}) // wasm: [data, vtable]; native: cell ptr
			argc := int32(1)
			if b.ptrW == 4 {
				argc = 2
			}
			b.emit(Op{Kind: OpCallDirect, Str: dynDropFnName(dt.Traits), I32: argc})
			return
		}
		// Tuple reclamation: an OWNED tuple local drops its pointer-shaped
		// elements then returns its box to the freelist on the last
		// reference (rc==1), mirroring the struct box path. The box was
		// alloc'd as `tupleElemLayout size + 8` rc header, so
		// __fern_box_free frees base = data-8, size+8. Each element drop
		// is_unique-gates internally (dropStructField), so a shared
		// element only dec's; the per-element dec balances the dup the
		// projection sites (destructure / field read / return) emit when
		// they hand the element out. Ineligible (borrowed / escaped)
		// tuples and flag-off builds fall through to the plain box dec.
		if tt, ok := t.(ast.TupleType); ok && ast.RcFreeEnabled && eligible {
			offs, size := tupleElemLayout(tt.Elems, b.ptrW)
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			for i, et := range tt.Elems {
				if _, isStr := et.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
					// Two-word string element (wasm + arm64-TwoWordOverride):
					// load (data, len) and reclaim via __fern_str_dec. Unique
					// here (rc==1 guard), so the element is uniquely owned;
					// inline / literal strings no-op. Balances the projection
					// dup (__fern_str_inc) and the construction retain.
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					if offs[i] != 0 {
						b.emit(Op{Kind: OpConstI32, I32: offs[i]})
						b.emit(Op{Kind: OpAdd})
					}
					b.emit(Op{Kind: OpLoad, Width: WidthString})
					b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
					b.emit(Op{Kind: OpDrop})
					continue
				}
				// Native single-word string tuple element (x86_64,
				// !TwoWordOverride): load WidthPtr + __fern_rc_dec. SSO
				// inline-tag guard + literal sentinel keep all sources
				// safe. arm64 / wasm two-word ABIs take the WidthString
				// + __fern_str_dec branch above.
				if _, isStr := et.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					if offs[i] != 0 {
						b.emit(Op{Kind: OpConstI32, I32: offs[i]})
						b.emit(Op{Kind: OpAdd})
					}
					b.emit(Op{Kind: OpLoad, Width: WidthPtr})
					b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
					b.emit(Op{Kind: OpDrop})
					continue
				}
				if !arrElemIsRcTracked(et) {
					continue
				}
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				if offs[i] != 0 {
					b.emit(Op{Kind: OpConstI32, I32: offs[i]})
					b.emit(Op{Kind: OpAdd})
				}
				b.emit(Op{Kind: OpLoad, Width: WidthPtr})
				dropStructField(et)
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpConstI32, I32: size})
			b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpElse})
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			b.emit(Op{Kind: OpEnd})
			return
		}
		// Phase 3 map reclamation: an OWNED Map local returns its buf
		// + handle to the freelist at the last reference (rc==1) via
		// __fern_map_drop. First free the value column via
		// __map_drop_values (which self-guards on rc==1): it reads the
		// buf's packed valKind+stride (2 = plain-elem array → arr_dec,
		// 3 = rc-elem array → drop_arr_ptr) and frees each live value.
		// The retain-on-store (inc-on-set) + retain-on-read (inc-on-
		// get) balance keeps this release-balanced. Non-array V and
		// entry KEYS still leak — a later slice. Ineligible (borrowed-
		// derived) maps and flag-off builds fall through to the plain
		// box dec.
		if st, ok := t.(ast.StructType); ok && st.Name == "Map" && ast.RcFreeEnabled && eligible {
			b.emitMapSlotDrop(slot, st)
			return
		}
		// Cell reclamation: a Cell is a one-element array box (emitCellNew),
		// so reclaim it through the array machinery keyed on the
		// instantiation's element type — never the generic struct/box_free
		// branch below, whose data-8 base would mis-free the cell's 16-byte
		// header. A Cell[string] dec's its slot buffer; a Cell[scalar] frees
		// the box. Ineligible cells leak-safe via the plain dec.
		if st, ok := t.(ast.StructType); ok && st.Name == "Cell" {
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emitCellDropOnStack(cellElemOf(t), eligible)
			return
		}
		// Phase 3 step 3: a user struct with pointer-shaped
		// rc-tracked fields drops those fields on its LAST
		// reference before dec'ing the box — balancing the
		// per-field inc from Phase 1e-struct-ii. Gated on
		// __fern_rc_is_unique (rc == 1, guarded) so an aliased
		// struct (rc > 1) or a non-pointer slot only dec's the
		// box. Runtime handle types (Map / Reader / Writer /
		// MapIter) have no StructDecl in info.Structs, so sdOk is
		// false and they fall through to the plain box dec — their
		// own drop handlers land in a follow-up. Fields are dropped
		// at one level (decValueOnStack); nested struct/enum/closure
		// fields are flat-dec'd (deep recursion is a later slice).
		if st, ok := t.(ast.StructType); ok {
			sd, sdOk := b.info.Structs[st.Name]
			// Phase 3 struct-box reclamation: an OWNED user struct
			// returns its box to the freelist on the last reference
			// (rc==1) after dropping its rc-tracked fields. Gated on
			// eligible (computeFreeEligible) + flag-on; otherwise the
			// box only dec's (leaks) as before. The box was alloc'd as
			// `structFieldLayout size + 8` rc header, so __fern_box_free
			// frees base = data-8, size+8.
			if sdOk && ast.RcFreeEnabled && eligible {
				offs, size := structFieldLayout(sd.Fields, b.ptrW)
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
				b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
				// rc == 1: drop fields, then free the box. The struct is
				// uniquely owned here, so each field is too — a string
				// field's __fern_str_dec frees its buffer safely (inline /
				// literal-sentinel strings no-op). The field was retained
				// on construction (emitAliasInc) or moved in fresh, so the
				// dec balances. Direct string fields only; a string nested
				// in an array / tuple / enum field reclaims via that
				// container's own (future) string-aware drop.
				for _, f := range sd.Fields {
					if _, isStr := f.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(b.ptrW) {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if off := offs[f.Name]; off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(Op{Kind: OpLoad, Width: WidthString})
						b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1})
						b.emit(Op{Kind: OpDrop})
						continue
					}
					// Native single-word string field (x86_64, !TwoWordOverride):
					// the field is a single pointer at the field offset; load
					// it as WidthPtr and __fern_rc_dec (SSO inline-tag low-bit
					// guard + literal sentinel keep all sources safe). arm64
					// boxed strings excluded — same gating as the rest of the
					// native string-reclaim path.
					if _, isStr := f.Type.(ast.StringType); isStr && b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW) {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if off := offs[f.Name]; off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(Op{Kind: OpLoad, Width: WidthPtr})
						b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
						b.emit(Op{Kind: OpDrop})
						continue
					}
					if !arrElemIsRcTracked(f.Type) {
						continue
					}
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					if off := offs[f.Name]; off != 0 {
						b.emit(Op{Kind: OpConstI32, I32: off})
						b.emit(Op{Kind: OpAdd})
					}
					b.emit(Op{Kind: OpLoad, Width: WidthPtr})
					dropStructField(f.Type)
				}
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpConstI32, I32: size})
				b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpElse})
				// rc > 1: just dec the box.
				b.emit(Op{Kind: OpLoadLocal, I32: slot})
				b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
				b.emit(Op{Kind: OpEnd})
				return
			}
			if sdOk {
				offs, _ := structFieldLayout(sd.Fields, b.ptrW)
				var ptrFields []ast.Param
				for _, f := range sd.Fields {
					if arrElemIsRcTracked(f.Type) {
						ptrFields = append(ptrFields, f)
					}
				}
				if len(ptrFields) > 0 {
					b.emit(Op{Kind: OpLoadLocal, I32: slot})
					b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
					b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
					for _, f := range ptrFields {
						b.emit(Op{Kind: OpLoadLocal, I32: slot})
						if off := offs[f.Name]; off != 0 {
							b.emit(Op{Kind: OpConstI32, I32: off})
							b.emit(Op{Kind: OpAdd})
						}
						b.emit(Op{Kind: OpLoad, Width: WidthPtr})
						// Owned-but-NOT-free-eligible (escaped / tainted): the box
						// isn't freed here, and a nested struct field may still be
						// reachable through the escape, so it must NOT be deep-freed.
						// Flat one-level dec only; deep recursion fires solely in the
						// eligible branch above.
						decValueOnStack(f.Type, false)
					}
					b.emit(Op{Kind: OpEnd})
				}
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		// Phase 3 enum-box reclamation: an OWNED enum frees its box to
		// the freelist on the last reference (rc==1) after dropping its
		// payloads. The full tiered logic — uniform branchless path,
		// non-uniform / scalar variant-plan tag switch, and the generic
		// fallthrough flat-dec — lives in emitEnumSlotDrop so the loop-var
		// reinit / reassign drop (emitStructEnumSlotDrop) and the
		// fresh-match-scrutinee reclamation can free a scalar-payload box
		// the same way instead of leaking it.
		if et, ok := t.(ast.EnumType); ok {
			b.emitEnumSlotDrop(slot, et, eligible)
			return
		}
		// Closure reclamation: an OWNED FuncType local frees its env /
		// pair rc1 block at the last reference (rc==1). When the local
		// has a single known closure source with rc-tracked captures
		// (closureTarget), dispatch to that closure's
		// __closure_drop_<name> thunk, which ALSO frees the captured
		// pointer targets before freeing the env (Stage 3). Otherwise
		// the generic __fern_closure_drop frees just the env (Stage 2;
		// captures leak). Either way a single load+call keeps
		// ElideClosurePair's reader recognising the drop as benign.
		// Ineligible (borrowed / escaping) closures and flag-off
		// builds fall through to the plain dec.
		if _, isFunc := t.(*ast.FuncType); isFunc && ast.RcFreeEnabled && eligible {
			dropFn := "__fern_closure_drop"
			tgt := b.closureTarget[name]
			if tgt != "" && hasRcCapture(b.closureCaps[tgt], b.ptrW, b.dynRcSupported) {
				dropFn = "__closure_drop_" + tgt
			}
			b.emit(Op{Kind: OpLoadLocal, I32: slot})
			b.emit(Op{Kind: OpCallDirect, Str: dropFn, I32: 1})
			b.emit(Op{Kind: OpDrop})
			return
		}
		b.emit(Op{Kind: OpLoadLocal, I32: slot})
		b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
		b.emit(Op{Kind: OpDrop})
	}
	// Use b.locals[name] so we only dec slots that user code
	// actually writes to. Two scope-separated Var declarations
	// sharing a name (e.g. `var ns` declared 9 times across
	// different branches of vm.fern) all map to the SAME
	// physical slot via b.locals[name] — only the last entry
	// wins in the slot map, and every Var-decl Store reaches
	// that last slot. The "earlier" slot indices that
	// info.Locals[fn] tracks are never written by user code, so
	// dec'ing them at exit by index would read uninitialised
	// memory and trap.
	//
	// Dedup via a per-name set so we only dec each unique slot
	// once even if the same name appears multiple times in
	// info.Locals[fn].
	//
	// Phase 1e-struct-iii: dec sweep now also covers struct-
	// typed slots. The rc-tracked set matches the predicate
	// used by needsRcIncOnAlias / zeroRcTracked so inc and dec
	// agree on which slots get touched. The runtime guard
	// inside __fern_rc_dec (high-bit sentinel + low-address
	// short-circuit) keeps this safe for runtime-allocated
	// struct values (Reader/Writer/Map/MapIter) whose header
	// holds 0x80000000 instead of a real rc.
	rcTracked := func(t ast.Type) bool {
		// The shared slot set (rc_caps.go): array / struct / enum / closure /
		// tuple / string — see rcTrackedSlotType for the per-shape notes.
		if rcTrackedSlotType(t) {
			return true
		}
		// `dyn Trait` values own their erased concrete `data` object
		// (docs/DYN-TRAITS.md §4.4). On wasm (inline two-word) the slot's
		// `data` word, and on x86-64 (boxed one-word) the cell + its data,
		// are dropped through the per-set __drop_dyn_<set> helper at scope
		// exit (emitDec's DynTraitType arm). wasm (slice 4a) + x86-64
		// (slice 4b). arm64 still leaks `dyn` (no __drop_dyn helper, slice
		// 4c), so don't sweep it or the dec would call a missing fn.
		if _, isDyn := t.(ast.DynTraitType); isDyn && b.dynReclaim() {
			return true
		}
		return false
	}
	// Phase 2d-borrow: parameters are BORROWED, not owned. The
	// caller no longer inc's a tracked argument when passing it
	// (the matching arg-inc at the OpCallDirect site is gone), so
	// the callee must NOT dec its parameters at exit — doing so
	// would underflow the rc. A borrowed value's lifetime is
	// owned by the caller; the callee only reads/mutates through
	// the borrow. This is what lets a Map passed to a function be
	// mutated in place (the handle stays rc==1, so the Phase 2d
	// copy-on-write check mutates rather than copies), while a
	// genuine local alias (`var m2 = m1`) still inc's and so gets
	// a copy on write. Only OWNED locals are dec'd below.
	seen := map[string]bool{}
	if exclude != "" {
		// Move-on-return: the returned local is handed to the caller
		// without an inc, so it must NOT be dec'd here.
		seen[exclude] = true
	}
	// Move-on-alias: locals consumed by a single-use alias were never
	// inc'd (the transfer moved the reference to the alias target), so
	// they must NOT be dec'd here either.
	for name := range b.rc.movedLocals {
		seen[name] = true
	}
	// Borrowed-view aliases (#4402 opt 1) were never inc'd — their dec is
	// the other half of the cancelled pair.
	for name := range b.rc.borrowedAlias {
		seen[name] = true
	}
	// `.with` receivers consumed by __fern_arr_cow_inplace (#6013): no inc was
	// emitted before the call, so the helper took the receiver's reference over
	// — moved into the result on its rc==1 branch, dec'd by the helper itself on
	// the copy branch. Dec-ing here releases it a second time, which with the
	// freelist on frees the buffer the RESULT still points at. See
	// arraySetConsumed for why a reassign-to-self is deliberately not in the set.
	for name := range b.rc.arraySetConsumed {
		seen[name] = true
	}
	// Borrowed `dyn Trait` views (#4787): a dyn local bound from an element /
	// field / bare-ident read holds another value's cell pointer uncounted
	// (dyn cells carry no rc header). The sweep's DynTraitType arm drops
	// UNCONDITIONALLY (__drop_dyn_<set> frees the cell + runs the concrete
	// dtor), so sweeping a view double-frees against the owner's own drop —
	// e.g. `var x = xs[0]` swept alongside xs's __drop_arr_dyn walk. Skip
	// them; the owner releases the cell.
	for name := range b.rc.dynBorrowedViews {
		seen[name] = true
	}
	for _, v := range b.info.Locals[b.fn] {
		if !rcTracked(v.Type) {
			continue
		}
		if seen[v.Name] {
			continue
		}
		seen[v.Name] = true
		slot, ok := b.locals[v.Name]
		if !ok {
			continue
		}
		emitDec(slot, v.Type, b.rc.freeEligible[v.Name], v.Name)
	}
	// Owned (`own`) params are reclaimed by the callee at exit, like an owned
	// local — the borrow model sweeps only `var` locals, so they need an extra
	// pass. A moved own param (passed onward to another `own` param) is already
	// in `seen` (b.rc.movedLocals) and skipped, so the value is freed exactly once
	// at the end of the transfer chain; an own param that escaped is not
	// freeEligible (re-tainted) and is likewise skipped.
	for i, p := range b.fn.Params {
		if (!p.Own && !b.paramOwnedByDefault(p.Type, i) && !b.rc.consumedParams[p.Name]) || !rcTracked(p.Type) || seen[p.Name] {
			continue
		}
		// A consumed-threaded ARRAY param owns its slot only once a
		// reassignment has replaced the incoming borrow — it never took an
		// entry retain, so sweeping it unconditionally would release the
		// caller's reference on the paths that never reassigned. Its
		// ownership bit says which.
		//
		// That bit is a RUNTIME ownership proof, so this arm deliberately
		// does not consult the static freeEligible taint (#6036): the flag
		// is set only where emitConsumedArrayOverwriteDec has already
		// replaced the incoming borrow with a reference this frame owns.
		// Requiring freeEligible dropped exactly the exit half of that
		// protocol whenever the reassignment's RHS was a CALL whose result
		// may alias a param — `b = f(b, v); return b;` — leaving the
		// return-transfer inc unbalanced. One leaked reference per call
		// makes the accumulator's buffer permanently shared, so every
		// subsequent append copies it: correct, but O(n²) in bytes moved.
		flagSlot, hasFlag := b.locals[consumedArrayFlagName(p.Name)]
		flagGated := hasFlag && b.isConsumedArrayParam(p.Name)
		if !flagGated && !b.rc.freeEligible[p.Name] {
			continue
		}
		seen[p.Name] = true
		slot, ok := b.locals[p.Name]
		if !ok {
			continue
		}
		if flagGated {
			b.emit(Op{Kind: OpLoadLocal, I32: flagSlot})
			b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
			emitDec(slot, p.Type, true, p.Name)
			b.emit(Op{Kind: OpEnd})
			continue
		}
		emitDec(slot, p.Type, true, p.Name)
	}
	// Consuming-owned-match bindings (#4400) are counted owners: the per-arm
	// scrutinee release transferred (unique branch) or dup'd (shared branch)
	// their payload reference, so the sweep deep-drops them like owned locals.
	// They live in neither info.Locals nor Params, hence the extra pass. The
	// prologue pre-zeroed their slots, so a binding whose arm never ran decs a
	// NULL (inert under the helpers' guards). An escaped binding lost its
	// freeEligible bit (re-tainted) and is skipped — leak, never a UAF.
	if len(b.rc.consumingBindings) > 0 {
		names := make([]string, 0, len(b.rc.consumingBindings))
		for nm := range b.rc.consumingBindings {
			names = append(names, nm)
		}
		sort.Strings(names)
		for _, nm := range names {
			if seen[nm] || !b.rc.freeEligible[nm] {
				continue
			}
			seen[nm] = true
			slot, ok := b.locals[nm]
			if !ok {
				continue
			}
			emitDec(slot, b.rc.consumingBindings[nm], true, nm)
		}
	}
}

// emitPreciseDrop deep-drops the owned local `name` at its last use and
// zeroes the slot (see computePreciseDrops). Net-zero on the operand stack.
func (b *builder) emitPreciseDrop(name string) {
	slot, ok := b.locals[name]
	if !ok {
		return
	}
	t, ok := b.localDeclType(name)
	if !ok {
		return
	}
	b.emitOwnedSlotDrop(slot, t)
	b.emit(Op{Kind: OpConstI32, I32: 0})
	b.emit(Op{Kind: OpStoreLocal, I32: slot})
}

// emitAliasInc emits the retain (inc) for an alias of expr `e` whose
// value is already on the operand stack. For a wasm two-word string it
// uses __fern_str_inc (consumes + returns the (data, len) pair, so the
// value survives for the following store); everything else uses the
// single-word __fern_rc_inc. The callers all pre-gate on
// needsRcIncOnAlias(e), so this only fires for rc-tracked aliases.
//
// String inc is now UNCONDITIONAL (matching arrays / structs / etc.):
// every string is one of inline (the flag makes __fern_str_inc/dec a
// no-op), static literal (the 0x80000000 data-segment sentinel header
// short-circuits inc/dec), or headered heap (real rc) — there is no
// view form anymore (args()/env() copy into owned strings; see the
// args/env view-fix PR). So a borrowed read of a string out of a
// container (`var s = foo.field` / `arr[i]`) co-owns the buffer, which
// is required once a container drop dec's its string fields/elements:
// without the inc, dropping the container would free the buffer out
// from under the still-live alias (UAF). The earlier eligibility gate
// (inc only fresh-owned bare idents) existed solely to avoid touching
// view strings and is no longer needed.
func (b *builder) emitAliasInc(e ast.Expr) {
	if _, isStr := b.exprType(e).(ast.StringType); isStr && b.twoWordStrings() {
		// Two-word string ABI (wasm32 + arm64 TwoWordOverride): the
		// value occupies two stack words (data, len), so the retain
		// must go through __fern_str_inc, which tag-checks the inline
		// bit and inc's only the heap data pointer. The single-word
		// __fern_rc_inc fall-through below would pop just the top word
		// (the length) and dereference it as a pointer — a SIGSEGV on
		// literal strings, whose length is a small integer. Gating on
		// b.ptrW==4 (wasm-only) missed arm64 and crashed every aliased
		// string (e.g. generic id[T](x: string) returning its param).
		b.emit(Op{Kind: OpCallDirect, Str: "__fern_str_inc", I32: 1})
		return
	}
	b.emit(Op{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1})
}

// emitVarReinitDropOld releases the value currently in a var's slot
// before its (re-)initialisation store — Phase 5h (loop-body local
// drops). A `var row = …` declared inside a loop reuses one slot across
// iterations; without this the prior iteration's value is overwritten
// with no dec, so N-1 allocations leak — and the rc undercount keeps the
// freelist from reclaiming them, so a hot build-and-discard loop grows
// unbounded. A loop-body `var` is a re-DECLARATION (not a reassignment),
// so the assign hook's dec-on-overwrite never ran for it.
//
// Mirrors the reassignment dec-on-overwrite (the assign Ident case) for
// the SAME rc-tracked set and dec choice — owned arrays free via
// `__fern_arr_dec`, other single-box rc types (struct / enum / closure)
// flat `__fern_rc_dec`, owned strings on x86_64/wasm (arm64 deferred per
// slice 5g) — MINUS the self-mutation / map-COW branches, which cannot
// arise for a var-init RHS: a fresh binding can never reference its own
// prior slot value. Net-zero on the operand stack (load → dec → drop), so
// the new value already sitting underneath is left in place for the store.
//
// Safety gates:
//   - ast.RcFreeEnabled: the free-off baseline emits nothing here, so it
//     stays byte-identical to before this slice — the differential gate's
//     free-on == free-off comparison is the meaningful one.
//   - localNameUnique: the var's single slot is zero-init'd at entry
//     (Phase 1d-v), so the first-iteration dec is a NULL-guarded no-op.
//     Shadowed names (multiple distinct slots, one name-keyed zero) are
//     skipped — dec-ing an un-zeroed slot would read garbage.
//   - !movedLocals: a var whose reference was MOVED out (move-on-alias /
//     -construction / -destructure / -return — all top-level, last-use)
//     is excluded from the exit sweep; dec-ing its slot would over-
//     release. Moves are top-level only, so a loop-body var is never
//     marked; this guards the rare top-level re-declaration case.
func (b *builder) emitVarReinitDropOld(name string, idx int32) {
	if !ast.RcFreeEnabled {
		return
	}
	// freeEligible is the borrow-aware verdict the EXIT sweep uses: true
	// only for an OWNED local that genuinely holds its own reference.
	// Ineligible locals (borrowed params and borrowed-derived views) hold no
	// reference of their own, so the DEEP free must not fire for them.
	//
	// The distinction is narrow: ineligibility has several causes, and
	// only a CONSTRUCTION alias-inc (rc.ctorAliasInced) leaves the local
	// holding a counted reference it must give back. That case gets the flat
	// dec below; every other ineligible local is still skipped entirely.
	if !b.localNameUnique(name) || b.rc.movedLocals[name] {
		return
	}
	if !b.rc.freeEligible[name] {
		// Ineligible for the DEEP free — but a local that took a construction
		// alias-inc (ctorAliasInced) still holds a counted reference of its
		// own, and a loop-body re-declaration must give it back or the inc
		// happens once per iteration against a single exit-sweep dec: n-1
		// values leak, linear and unbounded (#5879 cause A).
		//
		// The release is the FLAT dec the exit sweep already emits for this
		// same local (emitDec's ineligible fall-through), never the deep
		// array/struct drop — the container shares the value and reclaims it
		// through its own drop, so freeing the payload here would be a
		// use-after-free. Every other ineligibility cause is skipped, since
		// only this one comes with a reference to release.
		if ast.RcFreeEnabled && b.rc.ctorAliasInced[name] {
			if t, ok := b.localDeclType(name); ok && rcTrackedForFlatDec(t) {
				b.emit(Op{Kind: OpLoadLocal, I32: idx})
				b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
				b.emit(Op{Kind: OpDrop})
			}
		}
		return
	}
	// A borrowed `dyn Trait` view (#4787 — e.g. a for-in loop var
	// re-declared per iteration from `iter[idx]`) owns no cell to release;
	// dropping the previous iteration's value would free the array's cell.
	// A dyn array that received a bare dyn LOCAL as a literal element
	// (dynAliasElemArrays) likewise skips the reinit drop — freeing the
	// prior iteration's walk would free the still-live source's cell.
	if b.rc.dynBorrowedViews[name] || b.rc.dynAliasElemArrays[name] {
		return
	}
	t, ok := b.localDeclType(name)
	if !ok {
		return
	}
	b.emitOwnedSlotDrop(idx, t)
}

// emitTupleSlotDrop releases the tuple value currently in local slot
// `idx` — the shared body for the loop-body re-declaration drop
// (emitVarReinitDropOld) and the reassignment dec-on-overwrite. It
// mirrors the exit sweep's inline TupleType branch (emitDec in
// emitRcDecLocalsAtExitExcept): a needs-drop tuple routes through the
// generated __drop_tuple_<mangled> fn (is_unique gate → per-element
// deep drop → box_free), registering the shape into b.genTupleDrops so
// the post-pass worklist emits that body; a plain-element tuple
// box_frees directly under the same is_unique gate. Net-zero on the
// operand stack, so a value sitting underneath (a reinit/reassign RHS)
// is left untouched. Callers gate on RcFreeEnabled + freeEligible +
// localNameUnique + !movedLocals before invoking.
func (b *builder) emitTupleSlotDrop(idx int32, tt ast.TupleType) {
	if name, ok := dropFnNameFor(tt, b.info, b.genEnumDrops, b.genTupleDrops, b.ptrW, b.dynRcSupported); ok {
		b.emit(Op{Kind: OpLoadLocal, I32: idx})
		b.emit(Op{Kind: OpCallDirect, Str: name, I32: 1})
		b.emit(Op{Kind: OpDrop})
		return
	}
	_, size := tupleElemLayout(tt.Elems, b.ptrW)
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpConstI32, I32: size})
	b.emit(Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpElse})
	b.emit(Op{Kind: OpLoadLocal, I32: idx})
	b.emit(Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
	b.emit(Op{Kind: OpDrop})
	b.emit(Op{Kind: OpEnd})
}

// emitReuseOldFieldDrops releases D's OLD pointer fields/elements from the
// reused box on the REUSE branch (gated reusedSlot), before C's stores
// overwrite them. D is dead at C, so every pointer slot is replaced — each old
// reference is deep-freeing-dropped (emitFieldDropOnStack). On the decline
// branch the box is fresh, so this is gated out. offsets/types are D's OWN
// layout (reuseSourceLayout). Scalar slots need no drop.
func (b *builder) emitReuseOldFieldDrops(reusedSlot, baseSlot int32, offsets []int32, types []ast.Type) {
	const rcHeaderBytes = 8
	hasPtr := false
	for _, t := range types {
		if arrElemIsRcTracked(t) {
			hasPtr = true
			break
		}
	}
	if !hasPtr {
		return
	}
	b.emit(Op{Kind: OpLoadLocal, I32: reusedSlot})
	b.emit(Op{Kind: OpIf, I32: BlockTypeVoid})
	for i, t := range types {
		if !arrElemIsRcTracked(t) {
			continue
		}
		b.emit(Op{Kind: OpLoadLocal, I32: baseSlot})
		b.emit(Op{Kind: OpConstI32, I32: rcHeaderBytes + offsets[i]})
		b.emit(Op{Kind: OpAdd})
		b.emit(Op{Kind: OpLoad, Width: WidthPtr})
		b.emitFieldDropOnStack(t)
	}
	b.emit(Op{Kind: OpEnd})
}

// hasRcCapture reports whether any capture is rc-tracked (i.e. was
// inc'd at MakeEnv and so needs dropping when the closure dies). A
// `dyn Trait` capture is move-only (no MakeEnv inc) but still needs the
// thunk to reclaim it, so it counts on the natives (dynRcSupported);
// docs/DYN-TRAITS.md §7.8 — closure-capture kind.
func hasRcCapture(caps []ast.Param, ptrW int, dynRcSupported bool) bool {
	for _, c := range caps {
		if arrElemIsRcTracked(c.Type) {
			return true
		}
		if _, isStr := c.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			return true
		}
		// Native single-word string capture (x86_64, !TwoWordOverride):
		// the env slot holds a single ptr that needs __fern_rc_dec'ing
		// on the closure's last reference.
		if _, isStr := c.Type.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			return true
		}
		// `dyn Trait` capture — NATIVES ONLY (boxed one-word cell ptr in
		// the env slot, reclaimed via __drop_dyn_<set> in the thunk). wasm
		// (ptrW==4, inline two-word) is excluded: it has no thunk reclaim
		// for `dyn` and keeps leaking the capture (correct-but-leaking).
		if _, isDyn := c.Type.(ast.DynTraitType); isDyn && dynRcSupported {
			return true
		}
	}
	return false
}

// genClosureDropThunk builds the per-closure __closure_drop_<name>
// function: at the closure's last reference (rc==1) it drops each
// rc-tracked capture — arrays free their buffer (arr_dec /
// drop_arr_ptr), struct/enum/closure captures flat-dec one level
// (consistent with decValueOnStack) — then frees the env block via
// the generic __fern_closure_drop. Returns nil for closures with no
// rc-tracked captures (the generic helper already handles those).
// The thunk's env is a plain param (slot 0), not a closure-pair
// local, so re-loading it freely doesn't perturb ElideClosurePair.
func genClosureDropThunk(name string, caps []ast.Param, ptrW int, info *checker.Info, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, dynRcSupported bool) *Func {
	// A no-rc-capture closure (scalar/i64/f64 captures only) still gets a
	// thunk when it's MakeClosure'd: the pair's drop-fn pointer needs a
	// callable target that frees the env block. Its body is just the
	// is_unique-gated (empty) capture sweep + the __fern_closure_drop(env)
	// tail, which frees the env block (a no-op for env==0). The thunk loop
	// gates generation on hasRcCapture || MakeClosure-target, so an elided
	// scalar closure that never forms a pair generates nothing.
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	off := int32(0)
	for _, c := range caps {
		// Same 8-byte alignment the env layout uses (closureconv +
		// the backend store loops) so the drop reads each capture at
		// the offset it was written to.
		off = ast.CaptureAlign(off, c.Type, ptrW)
		slot := irCaptureSlotSize(c.Type, ptrW)
		if _, isStr := c.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			// Two-word string capture (wasm + arm64-TwoWordOverride):
			// load (data, len) from [env+off] and reclaim via
			// __fern_str_dec (balances the __fern_str_inc at MakeEnv).
			// Inside the env's is_unique branch, so the capture is
			// this closure's owned reference; inline / literal
			// strings no-op.
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthString},
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			off += slot
			continue
		}
		// Native single-word string capture (x86_64, !TwoWordOverride):
		// load the single ptr from [env+off] and __fern_rc_dec it
		// (balances the __fern_rc_inc at MakeEnv via emitAliasInc).
		// Inside the env's is_unique branch, so the capture is uniquely
		// owned; SSO inline-tag low-bit guard + literal sentinel keep
		// all sources safe. arm64 / wasm two-word ABIs take the
		// WidthString + __fern_str_dec branch above.
		if _, isStr := c.Type.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthPtr},
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			off += slot
			continue
		}
		// `dyn Trait` capture — NATIVES ONLY (dynRcSupported, boxed one-word
		// cell ptr in the env slot). The capture was MOVED into the env (no
		// MakeEnv inc — needsRcIncOnAlias declines `dyn`), and
		// markConstructionMoves suppressed the source local's exit-sweep drop,
		// so this thunk is the SOLE owner that reclaims the cell. Load the cell
		// ptr from [env+off] and run __drop_dyn_<set> (argc 1, VOID return → NO
		// trailing OpDrop, mirroring appendChildDrop's dyn arm). wasm (inline
		// two-word) is excluded — it has no thunk reclaim for `dyn` and keeps
		// leaking the capture; see docs/DYN-TRAITS.md §7.8.
		if dt, isDyn := c.Type.(ast.DynTraitType); isDyn && dynRcSupported {
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthPtr},
				Op{Kind: OpCallDirect, Str: dynDropFnName(dt.Traits), I32: 1})
			off += slot
			continue
		}
		if arrElemIsRcTracked(c.Type) {
			// Load the capture pointer from [env+off]. The thunk only
			// runs when every rc-tracked capture was inc'd at MakeEnv
			// (emitDec's closureTarget gate), and inside the env's
			// is_unique branch, so the captures are this closure's
			// exclusively-owned references — safe to deep-free. The
			// per-value drop fns is_unique-gate again, so a shared
			// capture only dec's.
			ops = append(ops,
				Op{Kind: OpLoadLocal, I32: 0},
				Op{Kind: OpConstI32, I32: off},
				Op{Kind: OpAdd},
				Op{Kind: OpLoad, Width: WidthPtr})
			if at, isArr := c.Type.(ast.ArrayType); isArr {
				// dynRcSupported=false here: a `dyn Trait[]` CAPTURED by a
				// closure stays flagged-leaking on the natives this slice
				// (docs/DYN-TRAITS.md §7.8). The dedicated array-of-dyn drop
				// is wired for the value/field/element drop sites; routing it
				// from the closure capture sweep is a follow-up (the capture
				// inc/borrow accounting for a nested dyn isn't established).
				if drop, ok := arrElemStructDropName(at.Elem, info, reg, tupleReg, ptrW, false); ok {
					// Array of concrete structs: deep-drop each element
					// box + the buffer (Stage B loop).
					ops = append(ops, Op{Kind: OpCallDirect, Str: drop, I32: 1})
				} else {
					helper := "__fern_arr_dec"
					if arrElemIsRcTracked(at.Elem) {
						helper = "__fern_drop_arr_ptr"
					}
					ops = append(ops,
						Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, ptrW))},
						Op{Kind: OpCallDirect, Str: helper, I32: 2})
				}
			} else if isMapType(c.Type) {
				// Map capture: reclaim the value column + buf + handle
				// (both helpers self-guard on the map's rc==1 and return
				// the map ptr, which the trailing OpDrop discards).
				ops = append(ops,
					Op{Kind: OpCallDirect, Str: "__map_drop_values", I32: 1},
					Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1})
			} else if drop, ok := dropFnNameFor(c.Type, info, reg, tupleReg, ptrW, false); ok {
				// Concrete-struct (or boxed generic-enum) capture: free its
				// box + nested children.
				ops = append(ops, Op{Kind: OpCallDirect, Str: drop, I32: 1})
			} else {
				// enum / closure capture: flat one-level dec (a union's
				// variant type isn't statically known; nested closures
				// keep the env-only drop).
				ops = append(ops, Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1})
			}
			ops = append(ops, Op{Kind: OpDrop})
		}
		off += slot
	}
	ops = append(ops,
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpCallDirect, Str: "__fern_closure_drop", I32: 1},
		Op{Kind: OpReturn})
	return &Func{
		Name:       "__closure_drop_" + name,
		Params:     []ast.Param{{Name: "__cdenv", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// dropFnNameFor returns the generated recursive-drop function name for
// a NESTED value of type t, plus whether one exists. A CONCRETE user
// struct (rc-field-carrying OR childless) routes to __drop_struct_<Name>
// (genStructDropFn reads its exact fields). A CONCRETE (non-generic)
// enum with at least one statically-droppable payload routes to a
// tag-dispatched __drop_enum_<Name> — reading the runtime tag picks the
// exact per-variant payload type, so a union's differing variant types
// are handled correctly (no misread). A TUPLE with at least one rc-
// tracked element routes to __drop_tuple_<mangled> (genTupleDropFn
// generates a uniform deep-drop from the captured tuple shape) — the
// caller MUST supply a non-nil `tupleReg` so the worklist can recover
// the shape; absent a registry we fall back to the safe flat dec, the
// same way generic enums do. Map / runtime handle types, arrays,
// closures, and generic enum instantiations (Args != nil; their
// box-vs-pair-form shape needs the type args, handled inline for locals)
// return ("", false) so the caller falls back to a flat one-level dec.
func dropFnNameFor(t ast.Type, info *checker.Info, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, ptrW int, dynRcSupported bool) (string, bool) {
	switch v := t.(type) {
	case ast.StructType:
		if v.Name == "Map" {
			return "", false
		}
		// Cell is a one-element array box, not a record: a single
		// __drop_struct_Cell can't see the per-instantiation element type
		// (Cell[string] vs Cell[i32]) and box_free would mis-free its
		// 16-byte array header. Drops are handled inline at the call sites
		// (emitDec / decValueOnStack / emitStructEnumSlotDrop), which carry
		// the instantiation type and route through the array machinery.
		if v.Name == "Cell" {
			return "", false
		}
		if _, ok := info.Structs[v.Name]; !ok {
			return "", false
		}
		return "__drop_struct_" + v.Name, true
	case ast.EnumType:
		ed, ok := info.Enums[v.Name]
		if !ok {
			return "", false
		}
		if len(v.Args) > 0 {
			// Generic instantiation (Option[Item]). Substitute the type args
			// into the decl and route to a per-instantiation drop. The
			// substituted decl is stashed in reg under a mangled name the
			// worklist regenerates the body from; without a registry to record
			// into (direct unit calls) we can't be regenerated, so bail to the
			// safe flat path.
			//
			// This used to additionally require enumHasPointerPayload(sub),
			// on the grounds that a scalar instantiation is "pair-form, no
			// box". It is not: pair-form is a per-FUNCTION return ABI
			// (findPairFormFuncs, keyed by function name), and a scalar
			// instantiation is heap-boxed like any other. With the gate in
			// place a NESTED scalar Option leaked its box — measured at
			// 16 bytes per construction for `struct H { o: Option[i32] }` and
			// for `(Some(k), 1)`, linear and unbounded (#5917). The sibling
			// gate in emitEnumSlotDrop had the identical bug for a direct
			// local; both are gone.
			if reg == nil {
				return "", false
			}
			sub := substituteEnumDecl(ed, v.Args)
			mangled := mangleEnumInst(v)
			reg[mangled] = sub
			return "__drop_enum_" + mangled, true
		}
		if !enumNeedsDrop(ed) {
			return "", false
		}
		return "__drop_enum_" + v.Name, true
	case ast.TupleType:
		if tupleReg == nil {
			return "", false
		}
		mangled := mangleTupleInst(v)
		tupleReg[mangled] = v
		return "__drop_tuple_" + mangled, true
	case ast.DynTraitType:
		// A `dyn Trait` value owns its concrete `data` object (the erased
		// type), reclaimed through a per-trait-set drop helper that reads
		// the vtable's trailing drop slot and dispatches the concrete
		// destructor (docs/DYN-TRAITS.md §4.4). Per-SET (not per-trait) so
		// the drop-slot index (= the merged method count) is baked into
		// the helper; a single-trait set keys by the bare trait name.
		// wasm (ptrW==4, slice 4a), x86-64 (ptrW==8 + dynRcSupported,
		// slice 4b — boxed), and arm64 (ptrW==8 + dynRcSupported, slice
		// 4c — boxed, structural mirror) all have a __drop_dyn_<set>
		// helper (buildDynDropHelpers emits the right shape per ptrW). A
		// hypothetical ptrW==8 backend that lifts dispatch (DynSupported)
		// but NOT RC (!dynRcSupported) would still leak `dyn` — its vtable
		// emitter wouldn't append the drop slot and buildDynDropHelpers
		// would decline, so routing a `dyn` drop through a non-existent
		// __drop_dyn_ helper there would be a dangling call; the gate keeps
		// it correct-but-leaking. ptrW==0 (the collectVtables Drop probe)
		// only asks struct/enum, never reaching this arm, so its argument
		// is moot.
		if ptrW != 4 && !dynRcSupported {
			return "", false
		}
		return dynDropFnName(v.Traits), true
	}
	return "", false
}

// mangleEnumInst turns a generic enum instantiation type into a
// symbol-safe, injective name component for its `__drop_enum_<...>`
// drop function — `Option[Item]` → `Option_LB_Item_RB_`,
// `Result[Item, Err]` → `Result_LB_Item_C_Err_RB_`. Derived from the
// type's canonical String() with the non-identifier characters escaped
// to fixed tokens, so two distinct instantiations never collide and the
// same instantiation always mangles identically (routing ⇄ generation
// agreement, as the worklist requires). The escape tokens use only
// `[A-Za-z0-9_]`, keeping the name a valid wasm/asm symbol. The worklist
// resolves a `__drop_enum_<en>` name against info.Enums (concrete) before
// the generic registry, so a base name never shadows a real enum; the
// reverse — a hand-authored enum literally named to mimic an escaped
// instantiation (e.g. `Option_LB_Item_RB_`) — is a pathological clash we
// don't defend, as no realistic source produces it.
func mangleEnumInst(et ast.EnumType) string {
	return tupleEnumMangler.Replace(et.String())
}

// mangleTupleInst is mangleEnumInst's tuple sibling — same escape
// vocabulary, applied to a tuple's canonical String() so a tuple shape
// gets a stable, symbol-safe name component for its
// `__drop_tuple_<...>` recursive-drop function. `(string, i32)` →
// `__drop_tuple__LP_string_C_i32_RP_`. The mangled token uniquely
// determines the shape, so two structurally-equal tuples share one
// generated drop and two distinct shapes never collide.
func mangleTupleInst(tt ast.TupleType) string {
	return tupleEnumMangler.Replace(tt.String())
}

// appendMapDrop appends the map-reclamation chain for a map pointer
// already on the operand stack.
func appendMapDrop(ops []Op) []Op {
	return append(ops,
		Op{Kind: OpCallDirect, Str: "__map_drop_values", I32: 1},
		Op{Kind: OpCallDirect, Str: "__fern_map_drop", I32: 1},
		Op{Kind: OpDrop})
}

// substituteEnumDecl returns a copy of ed with each variant payload's
// ParamTypes bound to their concrete type args (Option[Item] →
// Some(Item)) — including ParamTypes NESTED in a composite payload
// (`T[]` at Wrap[string] → `string[]`, #2704 class 2): a composite
// payload slipped past enumVariantDropPlan's top-level-ParamType bail
// and classified from the still-generic shape, so `W(T[])` got a
// buffer-only __fern_arr_dec and leaked its elements' heap. Reproduces
// exactly the payload types emitEnumNew sized the box from
// (b.info.VariantCallPayloads) — the box layout for a composite payload
// is a single pointer regardless of T, so sharpening the type changes
// no sizing, only how deep the (is_unique-gated) drop recursion sees.
// Returns ed unchanged when not a type-arg-bearing instantiation; a
// ParamType left unbound (genuinely-still-generic context) still hits
// the plan's bail, preserving the safe-leak fallback.
func substituteEnumDecl(ed *ast.EnumDecl, args []ast.Type) *ast.EnumDecl {
	if len(ed.TypeParams) == 0 || len(args) != len(ed.TypeParams) {
		return ed
	}
	out := *ed
	out.Variants = make([]ast.EnumVariant, len(ed.Variants))
	for i, v := range ed.Variants {
		nv := v
		nv.Payloads = make([]ast.Type, len(v.Payloads))
		for j, pt := range v.Payloads {
			nv.Payloads[j] = substituteTypeParamsDeep(pt, ed.TypeParams, args)
		}
		out.Variants[i] = nv
	}
	return &out
}

// substituteTypeParamsDeep binds ParamTypes to their concrete args
// through the composite shapes a variant payload can carry (arrays,
// slices, tuples, generic struct/enum instantiations) — the recursive
// sibling of resolveTypeParam, which handles only a bare top-level
// ParamType (and stays that way: its pair-form/layout callers inspect a
// single scalar payload). Mirrors monomorph.substituteType's shape set;
// implemented here because ir must not import monomorph.
func substituteTypeParamsDeep(t ast.Type, params []string, args []ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ParamType:
		return resolveTypeParam(x, params, args)
	case ast.ArrayType:
		return ast.ArrayType{Elem: substituteTypeParamsDeep(x.Elem, params, args)}
	case ast.SliceType:
		return ast.SliceType{Elem: substituteTypeParamsDeep(x.Elem, params, args)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substituteTypeParamsDeep(x.Elems[i], params, args)
		}
		return out
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		nargs := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			nargs[i] = substituteTypeParamsDeep(x.Args[i], params, args)
		}
		return ast.StructType{Name: x.Name, Args: nargs}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		nargs := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			nargs[i] = substituteTypeParamsDeep(x.Args[i], params, args)
		}
		return ast.EnumType{Name: x.Name, Args: nargs}
	}
	return t
}

// arrElemStructDropName returns the __drop_arr_struct_<Elem> function
// name for an array whose element type is a CONCRETE struct, plus
// whether one applies. Transitive reclamation Stage B routes an
// eligible array-of-struct drop to this generated loop (deep-dropping
// each element box, then freeing the buffer) instead of the flat-
// element __fern_drop_arr_ptr. The element type of an array is
// statically exact (no union ambiguity), so it's safe to dispatch a
// type-specific per-element drop. Array-of-array / array-of-enum /
// array-of-closure return ("", false) and keep __fern_drop_arr_ptr.
func arrElemStructDropName(elem ast.Type, info *checker.Info, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, ptrW int, dynRcSupported bool) (string, bool) {
	// `dyn Trait[]` element (docs/DYN-TRAITS.md §7.8 — RC of `dyn` NESTED
	// in a container). Each element is a `dyn` value in the backend's
	// native shape: on the natives (boxed) a one-word cell ptr, on wasm
	// (inline) a two-word `[data, vtable]`. The dedicated
	// `__drop_arr_dyn_<set>` loop walks each element and runs the per-set
	// `__drop_dyn_<set>` destructor on it (which reads the vtable's
	// trailing drop slot, dispatches the erased concrete dtor, and on the
	// natives frees the 16-byte cell), then frees the outer buffer. Gated
	// on the backend's dyn-RC capability (ptrW==4 wasm slice 4a, or a
	// ptrW==8 native that opted into DynRcSupported): a backend that lifts
	// dispatch but not RC has no `__drop_dyn_<set>` helper, so it keeps the
	// flat buffer-only free (leak-but-never-UAF). genArrDynDropFn builds
	// the right-shaped loop per ptrW.
	if dt, ok := elem.(ast.DynTraitType); ok {
		if ptrW != 4 && !dynRcSupported {
			return "", false
		}
		// Embed the full per-element `__drop_dyn_<set>` symbol so the
		// worklist recovers it from the name alone (mirrors
		// `__drop_arr_of_<perElem>`); the '+'→"_x_" sanitisation in
		// dynDropFnName keeps the composite name a valid GAS label.
		return "__drop_arr_dyn_" + dynDropFnName(dt.Traits), true
	}
	if v, ok := elem.(ast.StructType); ok {
		if v.Name == "Map" {
			return "", false
		}
		if _, ok := info.Structs[v.Name]; !ok {
			return "", false
		}
		return "__drop_arr_struct_" + v.Name, true
	}
	// Closure-element array (`(() => R)[]`): each element is a pointer to a
	// closure PAIR `{fn_ptr, env_ptr, drop_fn, env_ptr}`. A flat
	// __fern_drop_arr_ptr rc_dec'd each element but never freed the pair
	// block OR its env block (the captures) — an array element typed
	// `(() => R)` can't name WHICH closure it holds, so it has no static
	// __closure_drop_<name> thunk to call. The representation change
	// (the pair carries a drop-fn POINTER at offset 2*ptrW) lets a single
	// generic __drop_arr_closure loop free each element's env generically:
	// it derefs the embedded drop-fn through the duplicated {drop_fn,
	// env_ptr} sub-pair (OpCallIndirect on pair+2*ptrW) on the element's
	// last reference, then frees the pair block. Static function-value
	// cells (OpConstFunc, rc sentinel) are skipped by the is_unique gate.
	if _, ok := elem.(*ast.FuncType); ok {
		return "__drop_arr_closure", true
	}
	// Enum-element sibling of the struct case: a `E[]` whose variants carry
	// rc-tracked payloads (e.g. `Value[]` — pervasive in the self-host
	// compiler) flat-rc_dec'd each element under __fern_drop_arr_ptr,
	// freeing the enum box but leaking its payloads. Route a CONCRETE
	// droppable enum to a __drop_arr_enum_<Name> loop whose per-element
	// call is __drop_enum_<Name> (tag-dispatched deep drop). Generic enum
	// instantiations (Option[…][]) need the genEnumDrops registry the
	// worklist threads but arrElemStructDropName doesn't carry — they keep
	// the flat path for now.
	if v, ok := elem.(ast.EnumType); ok && len(v.Args) == 0 {
		if ed, ok := info.Enums[v.Name]; ok && enumNeedsDrop(ed) {
			return "__drop_arr_enum_" + v.Name, true
		}
	}
	// Tuple-element sibling of the struct case: the per-element loop
	// recurses through __drop_tuple_<mangled>, which dec's the tuple's
	// rc-tracked / string elements (e.g. the string inside
	// `(string, i32)`) before returning the tuple box to the freelist.
	// Without this branch the array drop fell through to the flat
	// __fern_drop_arr_ptr (rc_dec per element only) — freed each tuple
	// box but never traversed it, leaking the strings inside. Caller
	// supplies the tuple registry so the per-shape drop can be
	// regenerated by the post-pass worklist; absent a registry (direct
	// unit calls) we bail to the safe flat path.
	if tt, ok := elem.(ast.TupleType); ok && tupleReg != nil {
		mangled := mangleTupleInst(tt)
		tupleReg[mangled] = tt
		return "__drop_arr_tuple_" + mangled, true
	}
	// Enum-element array (`E[]`): each element is a pointer to an enum box
	// whose rc-tracked payloads (string / array / struct / nested enum) must
	// reclaim, not just be flat-rc_dec'd by __fern_drop_arr_ptr (which frees
	// the box but leaks its payloads). Route through the generic
	// __drop_arr_of_<__drop_enum_E> loop: genArrOfArrDropFn calls the enum's
	// own deep drop (__drop_enum_<E>, is_unique-gated) per element, then frees
	// the outer buffer. dropFnNameFor declines scalar-only / non-heap enums
	// (Option[i32], payload-less) — no payload to reclaim, so those keep the
	// flat path. A generic instantiation registers its substituted decl into
	// `reg` so the worklist regenerates the __drop_enum_<mangled> body.
	if _, ok := elem.(ast.EnumType); ok {
		if perElem, ok := dropFnNameFor(elem, info, reg, tupleReg, ptrW, false); ok {
			return "__drop_arr_of_" + perElem, true
		}
		return "", false
	}
	// Array-of-array element (`i32[][]`'s outer drop): each element is
	// itself an array whose BUFFER must be freed, not just flat-rc_dec'd
	// (the __fern_drop_arr_ptr fallback frees the outer buffer but leaks
	// the inner ones). The per-element drop depends on the inner array's
	// element type:
	//   - PRIMITIVE inner (`i32[][]`): a plain __fern_arr_dec frees the
	//     inner buffer → stride-keyed __drop_arr_arr_<innerStride>.
	//   - STRING inner (`string[][]`): each inner buffer's string elements
	//     must reclaim too → __drop_arr_arr_str, whose loop calls
	//     __fern_drop_arr_str per element (walk + str_dec each (data,len) +
	//     free the inner buffer). Two-word ABIs (wasm + arm64-TwoWord) and
	//     native single-word both back __fern_drop_arr_str.
	// Deeper inner shapes route through a generated __drop_arr_of_<perElem>
	// loop whose per-element call is the INNER array's own deep drop —
	// arrElemStructDropName(inner.Elem), recursively. So a `P[][]` (inner
	// = P[], concrete struct) drops each inner P[] via __drop_arr_struct_P,
	// and a `i32[][][]` (inner = i32[][]) drops each inner i32[][] via
	// __drop_arr_arr_4 — then frees the outer buffer. The worklist
	// regenerates the per-element drop transitively (enqueueCalls picks it
	// up from the generated body). Inner element types arrElemStructDropName
	// declines (enum[]/closure[]) keep the flat __fern_drop_arr_ptr (inner
	// buffers leak — safe, a later slice).
	if inner, ok := elem.(ast.ArrayType); ok {
		if _, isStr := inner.Elem.(ast.StringType); isStr {
			return "__drop_arr_arr_str", true
		}
		if !arrElemIsRcTracked(inner.Elem) {
			return fmt.Sprintf("__drop_arr_arr_%d", ast.ElemSizeBytesFor(inner.Elem, ptrW)), true
		}
		// rc-tracked inner element (struct / array / tuple / enum): recurse to
		// the inner array's drop (also registers any tuple / generic-enum
		// shape it discovers).
		if perElem, ok := arrElemStructDropName(inner.Elem, info, reg, tupleReg, ptrW, dynRcSupported); ok {
			return "__drop_arr_of_" + perElem, true
		}
	}
	return "", false
}

// genArrStructDropFn builds __drop_arr_struct_<Elem>(ptr): on the
// array's last reference (rc==1, real heap) it walks every element and
// drops it through __drop_struct_<Elem> (which is_unique-gates per
// element, so a shared element box only dec's), then hands the buffer
// to __fern_arr_dec for the rc-dec / freelist return. Element structs
// are pointer-shaped, so the stride is ptrW and the length lives at
// [ptr-4]. Slots: 0=ptr (param), 1=i, 2=len (scratch).
// genArrElemDropFn is the shared skeleton for the "array of pointer-shaped
// elements, each reclaimed through a single 1-arg per-element drop callee"
// family (#4401 part 4). It builds `fnName(ptr)` which, on the array's last
// reference (rc==1), walks each element (a ptrW-stride pointer), drops it via
// `elemCallee(elem)`, then frees the buffer via __fern_arr_dec. The struct /
// enum / tuple array-drop generators are thin wrappers over this, differing
// only in (fnName, elemCallee, paramName) — byte-identical ops otherwise, so
// the goal-2 port mirrors one generator instead of three. Slots: 0=ptr, 1=i,
// 2=len.
func genArrElemDropFn(fnName, elemCallee, paramName string, ptrW int) *Func {
	stride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// len = mem[ptr-4]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break out of the block (depth 1).
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// elemCallee(mem[ptr + i*stride]); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: elemCallee, I32: 1},
		{Kind: OpDrop},
		// i = i + 1; continue.
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		// Dec / free the buffer itself (arr_dec re-checks rc==1).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         fnName,
		Params:       []ast.Param{{Name: paramName, Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

func genArrStructDropFn(elemName string, ptrW int) *Func {
	return genArrElemDropFn("__drop_arr_struct_"+elemName, "__drop_struct_"+elemName, "__as", ptrW)
}

// genArrEnumDropFn is genArrStructDropFn's enum sibling: __drop_arr_enum_<Name>(ptr)
// walks each element (a pointer-shaped enum box, stride ptrW) and drops it
// through the tag-dispatched __drop_enum_<Name> (which reclaims the box +
// its rc-tracked payloads, is_unique-gated per element), then frees the
// buffer. The worklist regenerates __drop_enum_<Name> from this body.
// Slots: 0=ptr, 1=i, 2=len.
func genArrEnumDropFn(elemName string, ptrW int) *Func {
	return genArrElemDropFn("__drop_arr_enum_"+elemName, "__drop_enum_"+elemName, "__ae", ptrW)
}

// genClosureValueDropFn builds __drop_closure_value(p) -> p: the release of
// ONE closure value whose static identity is unknown. A closure PAIR is laid
// out as {fn_ptr, env_ptr, drop_fn, env_ptr} (slots of ptrW bytes; env_ptr is
// duplicated at slot 3 so {drop_fn@2, env_ptr@3} forms a callable sub-pair).
// At the pair's last reference (is_unique — which also skips static OpConstFunc
// cells, whose rc word is the immortal sentinel) it frees the captures + env
// block by dispatching through the embedded drop-fn pointer, then frees the
// pair block itself via the generic __fern_closure_drop.
//
// A closure LOCAL does not need this: b.closureTarget names the single closure
// it can hold, so emitDec calls that closure's __closure_drop_<name> thunk
// directly. A closure reached through a CONTAINER cannot name which closure it
// holds — the field / element / payload type is just `(T) => R` — which is why
// the dispatch has to go through the pointer the pair carries.
//
// Returns its argument so it composes with the drop sites' stack discipline.
// Slot 0 = p (param).
func genClosureValueDropFn(ptrW int) *Func {
	return &Func{
		Name:       "__drop_closure_value",
		Params:     []ast.Param{{Name: "__cv", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        append(closureValueReleaseOps(0, ptrW), Op{Kind: OpLoadLocal, I32: 0}, Op{Kind: OpReturn}),
	}
}

// closureValueReleaseOps emits the release of the closure pair in local `slot`
// — the shared body of __drop_closure_value and __drop_arr_closure's
// per-element step. Net-zero on the operand stack.
func closureValueReleaseOps(slot int32, ptrW int) []Op {
	// Empty signature + the closure-call ABI's appended env_ptr makes the
	// dispatched wasm type (i32)->i32, matching __closure_drop_<name>'s
	// actual (env)->env shape; natives read env from sub-pair+ptrW.
	dropSig := &ast.FuncType{Result: ast.NumberType{}}
	return []Op{
		// if is_unique(p): drop_fn(env) via OpCallIndirect on (p + 2*ptrW).
		// The is_unique gate skips shared closures (rc>1, another holder
		// keeps the env live) and static function-value cells (sentinel rc,
		// only 2 slots — never read slot 2 on them). Inside, the drop-fn
		// slot is 0 for zero-capture closures (env==0, nothing to free) —
		// guard it so OpCallIndirect never dispatches through a null slot.
		{Kind: OpLoadLocal, I32: slot},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: slot},
		{Kind: OpConstI32, I32: 2 * int32(ptrW)},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpIf, I32: BlockTypeVoid}, // drop_fn != 0
		{Kind: OpLoadLocal, I32: slot},
		{Kind: OpConstI32, I32: 2 * int32(ptrW)},
		{Kind: OpAdd},
		{Kind: OpCallIndirect, I32: 0, Ext: &OpExt{Sig: dropSig}},
		{Kind: OpDrop},
		{Kind: OpEnd}, // if drop_fn != 0
		{Kind: OpEnd}, // if is_unique(p)
		// Free / dec the pair block itself (rc==1 -> box_free, else dec).
		{Kind: OpLoadLocal, I32: slot},
		{Kind: OpCallDirect, Str: "__fern_closure_drop", I32: 1},
		{Kind: OpDrop},
	}
}

// genArrClosureDropFn builds __drop_arr_closure(ptr): the array-of-closure
// sibling of genArrStructDropFn. Each element is a pointer to a closure PAIR
// laid out as {fn_ptr, env_ptr, drop_fn, env_ptr} (slots of ptrW bytes; the
// env_ptr is duplicated at slot 3 so {drop_fn@2, env_ptr@3} forms a callable
// sub-pair). On the array's last reference (rc==1) it walks each element and,
// for elements that are themselves uniquely held (is_unique gate — skips
// shared closures AND static OpConstFunc cells, whose rc word is the immortal
// sentinel), frees the captures + env block by dispatching through the
// embedded drop-fn pointer: OpCallIndirect on (pair + 2*ptrW) calls
// drop_fn(env_ptr) — i.e. the per-closure __closure_drop_<name> thunk, which
// deep-drops rc-tracked captures and frees the env. The pair block itself is
// then freed via the generic __fern_closure_drop (rc==1 → box_free). Finally
// the buffer is returned to the freelist via __fern_arr_dec. Slots: 0=ptr
// (param), 1=i, 2=len, 3=p (current element pair pointer).
func genArrClosureDropFn(ptrW int) *Func {
	stride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// len = mem[ptr-4]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break out of the block (depth 1).
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// p = mem[ptr + i*stride]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 3},
	}
	ops = append(ops, closureValueReleaseOps(3, ptrW)...)
	ops = append(ops, []Op{
		// i = i + 1; continue.
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		// Dec / free the buffer itself (arr_dec re-checks rc==1).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}...)
	return &Func{
		Name:         "__drop_arr_closure",
		Params:       []ast.Param{{Name: "__acl", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrTupleDropFn is genArrStructDropFn's tuple sibling — same
// loop shape, but the per-element call dispatches to a generated
// __drop_tuple_<mangled> helper (sized for THIS tuple's shape) so
// each element's rc-tracked / string members reclaim before the
// buffer is freed. Tuples have no source name; the mangled tuple
// shape carries the only key the worklist + per-element helper
// agree on. Slots: 0=ptr (param), 1=i, 2=len (scratch).
func genArrTupleDropFn(mangled string, ptrW int) *Func {
	return genArrElemDropFn("__drop_arr_tuple_"+mangled, "__drop_tuple_"+mangled, "__at", ptrW)
}

// genArrArrDropFn builds __drop_arr_arr_<innerStride>(ptr) — the
// array-of-array sibling of genArrStructDropFn. On the OUTER array's last
// reference (rc==1) it walks each element (a pointer to an INNER array
// buffer, so the outer stride is ptrW) and frees that inner buffer via
// __fern_arr_dec(elem, innerStride) — which is_unique-gates the inner
// array, so a shared inner buffer only dec's — then frees the outer
// buffer. Generated only for inner arrays of PRIMITIVE elements
// (arrElemStructDropName's array-of-array branch gates on that), so the
// inner __fern_arr_dec is the complete reclamation; inner arrays of rc /
// string elements keep the flat __fern_drop_arr_ptr (a later slice).
// Slots: 0=ptr (param), 1=i, 2=len (scratch).
func genArrArrDropFn(innerStride int32, ptrW int) *Func {
	outerStride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// len = mem[ptr-4]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __fern_arr_dec(mem[ptr + i*outerStride], innerStride); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpConstI32, I32: innerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		// i = i + 1; continue.
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		// Dec / free the outer buffer itself (arr_dec re-checks rc==1).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         fmt.Sprintf("__drop_arr_arr_%d", innerStride),
		Params:       []ast.Param{{Name: "__aa", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrArrStrDropFn builds __drop_arr_arr_str(ptr) — the array-of-string[]
// outer drop. On the outer array's last reference (rc==1) it walks each
// element (a pointer to an inner string[] buffer, outer stride ptrW) and
// reclaims that inner array via __fern_drop_arr_str(elem, stringStride) —
// which walks the inner buffer's (data,len) string elements, str_dec's
// each, and frees the inner buffer — then frees the outer buffer. The
// string element stride is ElemSizeBytesFor(string) (2*ptrW two-word /
// ptrW single-word). Each helper is_unique-gates internally, so a shared
// inner array or string only dec's. Slots: 0=ptr, 1=i, 2=len.
func genArrArrStrDropFn(ptrW int) *Func {
	outerStride := int32(ptrW)
	strStride := int32(ast.ElemSizeBytesFor(ast.StringType{}, ptrW))
	// Inner string[] reclamation helper, matching the exit sweep's string[]
	// routing: two-word ABIs (wasm + arm64-TwoWord) walk (data,len) pairs
	// via __fern_drop_arr_str; native single-word (x86_64) elements are
	// single pointers, so __fern_drop_arr_ptr (rc_dec each, SSO-safe) is the
	// available helper (__fern_drop_arr_str isn't emitted there).
	innerDrop := "__fern_drop_arr_str"
	if !ast.UseTwoWordStrings(ptrW) {
		innerDrop = "__fern_drop_arr_ptr"
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __fern_drop_arr_str(mem[ptr + i*outerStride], strStride); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpConstI32, I32: strStride},
		{Kind: OpCallDirect, Str: innerDrop, I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_arr_str",
		Params:       []ast.Param{{Name: "__aas", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrOfArrDropFn builds __drop_arr_of_<perElem>(ptr) — the generic
// "array of pointer-shaped, deeply-droppable element" outer drop. On the
// outer array's last reference (rc==1) it walks each element (a pointer,
// outer stride ptrW) and drops it through the 1-arg `perElemDrop` (each
// frees the element's storage + its rc-tracked contents and is_unique-gates
// internally), then frees the outer buffer. perElemDrop is any generated
// 1-arg → i32 deep drop: an INNER ARRAY's own drop for array-of-array
// (__drop_arr_struct_<E> / __drop_arr_arr_<n> / __drop_arr_arr_str /
// __drop_arr_of_<…>), OR the ENUM's own __drop_enum_<E> for array-of-enum
// (arrElemStructDropName's EnumType branch). The worklist regenerates
// perElemDrop transitively from this body. Slots: 0=ptr, 1=i, 2=len.
func genArrOfArrDropFn(perElemDrop string, ptrW int) *Func {
	outerStride := int32(ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// perElemDrop(mem[ptr + i*outerStride]); drop result.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: perElemDrop, I32: 1},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: outerStride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_of_" + perElemDrop,
		Params:       []ast.Param{{Name: "__ao", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genArrDynDropFn builds __drop_arr_dyn_<__drop_dyn_<set>>(ptr) — the
// outer drop for a `dyn Trait[]` array (docs/DYN-TRAITS.md §7.8). On the
// array's last reference (rc==1) it walks every element and runs the
// per-set `dynDrop` destructor on it, then frees the outer buffer.
// Representation-divergent in TWO ways the generic genArrOfArrDropFn
// can't express, hence a dedicated generator:
//   - element WIDTH: a `dyn` element is one word (the boxed cell ptr) on
//     the natives but TWO words ([data, vtable]) inline on wasm — so the
//     per-element stride is ptrW (native) vs 2*ptrW (wasm) and the load
//     fans out two words on wasm (WidthString) vs one (WidthPtr) native.
//   - the per-element drop RETURN: `__drop_dyn_<set>` returns VOID (it
//     dispatches the concrete dtor through the vtable and, on the
//     natives, frees the cell), so there is NO trailing OpDrop — unlike
//     genArrOfArrDropFn whose perElem returns the i32 box ptr.
//
// `dynDrop` is the full `__drop_dyn_<set>` symbol (embedded in this fn's
// name so the worklist recovers it). The concrete dtor self-guards on
// rc==1, so a shared element only dec's. Slots: 0=ptr, 1=i, 2=len.
func genArrDynDropFn(dynDrop string, ptrW int) *Func {
	stride := int32(ptrW)
	elemLoad := Op{Kind: OpLoad, Width: WidthPtr} // native: one-word cell ptr
	argc := int32(1)
	if ptrW == 4 {
		stride = int32(2 * ptrW)                        // wasm: two-word inline
		elemLoad = Op{Kind: OpLoad, Width: WidthString} // fans out [data, vtable]
		argc = 2
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpSub},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// dynDrop(mem[ptr + i*stride]) — void, no trailing OpDrop.
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpMul},
		{Kind: OpAdd},
		elemLoad,
		{Kind: OpCallDirect, Str: dynDrop, I32: argc},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 1},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: stride},
		{Kind: OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		{Kind: OpDrop},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_arr_dyn_" + dynDrop,
		Params:       []ast.Param{{Name: "__ad", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genDynPrimDropFn builds the `__drop_dynprim_<prim>` destructor a
// PRIMITIVE/STRING concrete's vtable drop slot points at (#4351 —
// docs/DYN-TRAITS.md §4.2.3 / §4.4). A primitive coerced to `dyn Trait` is
// heap-boxed into a headerless VALUE CELL (boxPrimitiveDynValue) whose
// pointer is the fat pointer's `data` word; before this helper the drop
// slot was the null sentinel, so every coercion leaked the cell (16 bytes
// per iteration in a churn loop). The helper frees exactly that cell —
// `__free(data, payloadSlotSize(prim))`, the same size the coercion
// allocated — matching the (ptr)->ptr dropSig every concrete dtor uses.
// A `string` concrete's cell holds the string value; the string BUFFER
// itself is deliberately NOT dec'd here: the coercion takes no retain, so
// an aliased source (`var s = ...; var d: dyn T = s;`) would be freed out
// from under `s`. Literal strings are static sentinels and heap strings
// leak their buffer — the safe-leak invariant, exactly like the pre-slice
// whole-cell behaviour. The cell is fresh at every coercion site and its
// pointer lives only in the `dyn` cell, so freeing it at the dyn drop is
// sole-owner sound. Null-guarded like __drop_dyn_<set>'s cell free.
func genDynPrimDropFn(prim string, ptrW int) *Func {
	ct := astTypeForConcreteName(prim)
	if ct == nil {
		return nil
	}
	size := payloadSlotSize(ct, ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI32, I32: size},
		{Kind: OpCallDirect, Str: "__free", I32: 2},
	}
	// The natives model every call as pushing a result (the register
	// backends push %rax/x0 unconditionally), so the meaningless __free
	// result must be dropped; wasm's __free is genuinely (i32, i32) → ()
	// and an OpDrop there would underflow the operand stack (invalid
	// module). Mirrors buildDynDropHelpers' native-only __free+drop.
	if ptrW != 4 {
		ops = append(ops, Op{Kind: OpDrop})
	}
	ops = append(ops,
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn},
	)
	return &Func{
		Name:       "__drop_dynprim_" + prim,
		Params:     []ast.Param{{Name: "__dp", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// mapValDropName returns the column-walk drop function name for a Map
// whose VALUE type has a generated recursive drop (concrete struct or
// enum), plus whether one applies. The name embeds the per-value drop fn
// (__drop_map_via_<perValueDrop>), so the worklist regenerates the loop —
// and the per-value drop it calls — from the name alone, no type lookup.
// The map's drop routes here instead of the generic __map_drop_values
// (which only reclaims array values). Mirrors mapValHasDrop's domain.
func mapValDropName(st ast.StructType, info *checker.Info, genEnumDrops map[string]*ast.EnumDecl, genTupleDrops map[string]ast.TupleType, ptrW int) (string, bool) {
	if st.Name != "Map" || len(st.Args) < 2 {
		return "", false
	}
	perVal, ok := mapValHasDrop(st.Args[1], info, genEnumDrops, genTupleDrops, ptrW)
	if !ok {
		return "", false
	}
	return "__drop_map_via_" + perVal, true
}

// genMapValDropFn builds __drop_map_via_<perValueDrop>(m): on the map's
// last reference (rc==1, guarded by __fern_rc_is_unique on the handle) it
// walks the value column and deep-drops each live value through
// perValueDrop (__drop_struct_<V> / __drop_enum_<V>, which is_unique-gate
// per value, so a value shared via an outstanding get/values borrow only
// dec's). The buf + handle are freed separately by the trailing
// __fern_map_drop the caller emits. Mirrors __map_drop_values' iteration:
// cap@buf+0, len@buf+4, entries at buf+16+cap*4, value at entry+ptrW with
// entryStride = 2*ptrW. Returns m so the caller's OpDrop pops a real
// value. Slots: 0=m (param), 1=buf, 2=len, 3=i, 4=entriesBase (scratch).
func genMapValDropFn(perValueDrop string, ptrW int) *Func {
	pw := int32(ptrW)
	entryStride := 2 * pw
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// buf = mem[m]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 1},
		// len = mem[buf+4]
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpAdd},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// entriesBase = buf + ast.MapHeaderBytes + cap*4   (cap = mem[buf])
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: ast.MapHeaderBytes},
		{Kind: OpAdd},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoad},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 4},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 3},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break (depth 1).
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// __drop_struct_<V>(mem[entriesBase + i*entryStride + ptrW]); drop.
		{Kind: OpLoadLocal, I32: 4},
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: entryStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpConstI32, I32: pw},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallDirect, Str: perValueDrop, I32: 1},
		{Kind: OpDrop},
		// i = i + 1; continue.
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: 1},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 3},
		{Kind: OpBr, I32: 0},
		{Kind: OpEnd}, // loop
		{Kind: OpEnd}, // block
		{Kind: OpEnd}, // if rc==1
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpReturn},
	}
	return &Func{
		Name:         "__drop_map_via_" + perValueDrop,
		Params:       []ast.Param{{Name: "__dm", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// genMapStrValDropFn / genMapStrKeyDropFn build the wasm string-column
// reclamation walks for a Map whose VALUE (resp. KEY) is a string: on the
// map's last reference (rc==1) the walk reclaims each string's heap buffer
// via __fern_str_dec. A string K/V is stored BOXED — the column holds an
// 8-byte cell pointer whose contents are the two-word (data, len) pair
// (boxIntoCell at set). So per entry we load the cell pointer at the
// column's byte offset (0 for keys, ptrW for values), and if non-null load
// (data, len) from it via the two-word WidthString load and __fern_str_dec
// the buffer (inline / literal strings no-op), then __fern_cell_free the
// now-dead 16-byte cell itself back to the freelist. The buf + handle are
// freed by the trailing __fern_map_drop the caller emits. Mirrors
// genMapValDropFn's iteration: cap@buf+0, len@buf+4, entries at
// buf+16+cap*4, entryStride = 2*ptrW.
// Slots: 0=m (param), 1=buf, 2=len, 3=i, 4=entriesBase, 5=cellPtr.
func genMapStrValDropFn(ptrW int) *Func {
	return genMapStrColDropFn("__drop_map_str_values", int32(ptrW), ptrW)
}

func genMapStrKeyDropFn(ptrW int) *Func {
	return genMapStrColDropFn("__drop_map_str_keys", 0, ptrW)
}

func genMapStrColDropFn(name string, colOff int32, ptrW int) *Func {
	pw := int32(ptrW)
	entryStride := 2 * pw
	// Inner block per entry differs by backend layout:
	//   two-word ABI (wasm ptrW=4 + arm64-TwoWordOverride ptrW=8):
	//     the kv slot stores a cell pointer; deref to load the
	//     (data, len) two-word string, __fern_str_dec it, then
	//     __fern_cell_free the now-dead 16-byte cell.
	//   native single-word (x86_64 ptrW=8, !TwoWordOverride): the
	//     kv slot stores the string data pointer directly (no
	//     boxing — the slot is already pointer-wide). One
	//     __fern_rc_dec per entry is the whole reclamation; the L2
	//     header at data-8 + rc-sentinel literals from prereqs 1+2
	//     make this safe across heap + literal sources.
	var inner []Op
	if ast.UseTwoWordStrings(ptrW) {
		inner = []Op{
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpLoad, Width: WidthString},
			{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
			{Kind: OpDrop},
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpCallDirect, Str: "__fern_cell_free", I32: 1},
			{Kind: OpDrop},
			{Kind: OpEnd},
		}
	} else {
		inner = []Op{
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpLoadLocal, I32: 5},
			{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
			{Kind: OpDrop},
			{Kind: OpEnd},
		}
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// buf = mem[m]
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 1},
		// len = mem[buf+4]
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpAdd},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 2},
		// entriesBase = buf + ast.MapHeaderBytes + cap*4   (cap = mem[buf])
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: ast.MapHeaderBytes},
		{Kind: OpAdd},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpLoad},
		{Kind: OpConstI32, I32: 4},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpStoreLocal, I32: 4},
		// i = 0
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: 3},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoop, I32: BlockTypeVoid},
		// if i >= len: break (depth 1).
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpLoadLocal, I32: 2},
		{Kind: OpGeS},
		{Kind: OpBrIf, I32: 1},
		// cellOrDataPtr = mem[entriesBase + i*entryStride + colOff]
		{Kind: OpLoadLocal, I32: 4},
		{Kind: OpLoadLocal, I32: 3},
		{Kind: OpConstI32, I32: entryStride},
		{Kind: OpMul},
		{Kind: OpAdd},
		{Kind: OpConstI32, I32: colOff},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpStoreLocal, I32: 5},
	}
	ops = append(ops, inner...)
	ops = append(ops,
		// i = i + 1; continue.
		Op{Kind: OpLoadLocal, I32: 3},
		Op{Kind: OpConstI32, I32: 1},
		Op{Kind: OpAdd},
		Op{Kind: OpStoreLocal, I32: 3},
		Op{Kind: OpBr, I32: 0},
		Op{Kind: OpEnd}, // loop
		Op{Kind: OpEnd}, // block
		Op{Kind: OpEnd}, // if rc==1
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn},
	)
	return &Func{
		Name:         name,
		Params:       []ast.Param{{Name: "__dm", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// pointer-shaped field (arrays, enums/unions, closures, Map, childless
// structs) takes a flat one-level __fern_rc_dec — matching the
// pre-transitive behaviour for those shapes (deep array-element,
// enum-payload, and map-key reclamation are later slices). Used by the
// generated __drop_struct_ bodies; the inline (builder) struct-field
// sweep delegates equivalently.
func appendChildDrop(ops []Op, t ast.Type, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, dynRcSupported bool) []Op {
	// `dyn Trait` child (enum payload / tuple element / struct field): the
	// per-set __drop_dyn_<set> destructor reads the vtable's trailing drop
	// slot, dispatches the concrete dtor on `data`, and frees the boxed cell
	// (docs/DYN-TRAITS.md §4.4 + §7.8). The caller loaded the dyn via
	// payloadLoadOpFor (ONE word boxed cell ptr on the natives); the call's
	// argc is 1 and — unlike the other branches — __drop_dyn_<set> returns
	// VOID, so there is NO trailing OpDrop.
	//
	// NATIVES ONLY (dynRcSupported, ptrW==8). wasm (ptrW==4, inline two-word
	// `dyn`) is deliberately EXCLUDED here: a `dyn` nested in an enum /
	// struct / tuple that is then matched-and-bound (`match (e) { V(s) => …
	// }`) double-drops on the inline representation (the bound `s` and the
	// container payload both reclaim the same `data` → "pointer not
	// aligned"). wasm keeps its prior correct-but-leaking behaviour for
	// these container kinds (the array-element kind reclaims via the
	// dedicated genArrDynDropFn path, unaffected). See §7.8.
	if dt, isDyn := t.(ast.DynTraitType); isDyn && dynRcSupported {
		return append(ops,
			Op{Kind: OpCallDirect, Str: dynDropFnName(dt.Traits), I32: 1})
	}
	// Two-word string value (wasm + arm64-TwoWordOverride): the
	// caller loaded (data, len) via a string-aware load
	// (payloadLoadOpFor), so reclaim via __fern_str_dec. Reached from
	// genEnumDropFn's payload drop (struct string fields are handled
	// inline in genStructDropFn before reaching here).
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
		return append(ops,
			Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
			Op{Kind: OpDrop})
	}
	// Single-word string value (native single-word, x86_64): the caller
	// loaded a ptr via payloadLoadOpFor; reclaim via __fern_rc_dec (SSO
	// inline-tag low-bit guard + literal sentinel keep all sources safe).
	// arm64 / wasm two-word ABIs take the WidthString + __fern_str_dec
	// branch above.
	if _, isStr := t.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
		return append(ops,
			Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
			Op{Kind: OpDrop})
	}
	if isMapType(t) {
		return appendMapDrop(ops)
	}
	// Closure child (struct field / enum payload / tuple element): free the
	// captures + env through the drop-fn pointer the pair carries, then the
	// pair block. The fall-through below is a bare __fern_rc_dec, which
	// zeroes the pair's count and stops — the pair block, the env block and
	// every rc-tracked capture were stranded, three blocks per instance for
	// the plainest `struct P { f: (T) => R }` (#6443). A container cannot
	// name WHICH closure the slot holds, so this is the same generic,
	// pointer-dispatched release __drop_arr_closure does per element.
	if _, isFunc := t.(*ast.FuncType); isFunc {
		return append(ops,
			Op{Kind: OpCallDirect, Str: "__drop_closure_value", I32: 1},
			Op{Kind: OpDrop})
	}
	if name, ok := dropFnNameFor(t, info, reg, tupleReg, ptrW, false); ok {
		return append(ops,
			Op{Kind: OpCallDirect, Str: name, I32: 1},
			Op{Kind: OpDrop})
	}
	if at, ok := t.(ast.ArrayType); ok {
		// Any array field frees its buffer (see dropStructField for the
		// rationale): array-of-struct deep-drops elements + buffer,
		// array-of-rc frees the outer buffer, plain arrays arr_dec.
		if name, ok := arrElemStructDropName(at.Elem, info, reg, tupleReg, ptrW, false); ok {
			return append(ops,
				Op{Kind: OpCallDirect, Str: name, I32: 1},
				Op{Kind: OpDrop})
		}
		helper := "__fern_arr_dec"
		if arrElemIsRcTracked(at.Elem) {
			helper = "__fern_drop_arr_ptr"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			helper = "__fern_drop_arr_str"
		} else if _, isStr := at.Elem.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			// string[] on native single-word: __fern_drop_arr_ptr walks +
			// __fern_rc_dec's each pointer element. Same routing as the
			// local-side gate above and the dropStructField gate.
			helper = "__fern_drop_arr_str"
		}
		return append(ops,
			Op{Kind: OpConstI32, I32: int32(ast.ElemSizeBytesFor(at.Elem, ptrW))},
			Op{Kind: OpCallDirect, Str: helper, I32: 2},
			Op{Kind: OpDrop})
	}
	return append(ops,
		Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop})
}

// genTupleDropFn builds the recursive __drop_tuple_<mangled> function
// — the struct-drop sibling for anonymous tuple shapes. At the box's
// last reference (rc==1) it dec's every rc-tracked / string element
// and returns the box to the freelist; otherwise it just dec's. The
// box was alloc'd as `tupleElemLayout size + 8` rc header, so
// __fern_box_free frees base = data-8 with that size. The body mirrors
// the inline tuple-LOCAL drop in emitDec (string elements split by
// wasm two-word vs native single-word ABI; rc-tracked elements recurse
// via appendChildDrop), so a nested tuple — `(string, i32)` as a
// struct field, an array element, an enum payload, or another tuple's
// element — reaches the same dec calls a top-level local does, fixing
// the leak the docs called out under "nested tuples … strings still
// leak."
//
// EVERY tuple shape is routed here, including one whose elements are all
// plain scalars: such a body emits no element drops, just the
// is_unique-gated box_free + dec. That is the point — the box still has to
// be freed, and the flat __fern_rc_dec fallback callers used to take for
// those shapes only decrements (freeing needs the size, which only this
// body has). The former tupleNeedsDrop gate suppressed exactly that free
// and leaked one box per construction (#5879).
func genTupleDropFn(mangled string, tt ast.TupleType, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, dynRcSupported bool) *Func {
	offs, size := tupleElemLayout(tt.Elems, ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	for i, et := range tt.Elems {
		if _, isStr := et.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			// Two-word string element (wasm + arm64-TwoWordOverride):
			// load (data, len) and reclaim via __fern_str_dec. Mirrors
			// the inline tuple-local path's two-word branch.
			ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
			if offs[i] != 0 {
				ops = append(ops, Op{Kind: OpConstI32, I32: offs[i]}, Op{Kind: OpAdd})
			}
			ops = append(ops,
				Op{Kind: OpLoad, Width: WidthString},
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		if _, isStr := et.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			// Native single-word string element (x86_64,
			// !TwoWordOverride): single ptr + __fern_rc_dec. SSO
			// inline-tag low-bit guard + literal sentinel keep all
			// sources safe. arm64 / wasm two-word ABIs take the
			// WidthString + __fern_str_dec branch above.
			ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
			if offs[i] != 0 {
				ops = append(ops, Op{Kind: OpConstI32, I32: offs[i]}, Op{Kind: OpAdd})
			}
			ops = append(ops,
				Op{Kind: OpLoad, Width: WidthPtr},
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		if !arrElemIsRcTracked(et) {
			continue
		}
		ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
		if offs[i] != 0 {
			ops = append(ops, Op{Kind: OpConstI32, I32: offs[i]}, Op{Kind: OpAdd})
		}
		ops = append(ops, Op{Kind: OpLoad, Width: WidthPtr})
		ops = appendChildDrop(ops, et, info, ptrW, reg, tupleReg, dynRcSupported)
	}
	ops = append(ops,
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpConstI32, I32: size},
		Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2},
		Op{Kind: OpDrop},
		Op{Kind: OpElse},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn})
	return &Func{
		Name:       "__drop_tuple_" + mangled,
		Params:     []ast.Param{{Name: "__dt", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// genStructDropFn builds the recursive __drop_struct_<Name> function:
// at the value's last reference (rc==1) it drops each rc-tracked field
// — recursing into nested struct fields via their own drop fns — then
// returns the box to the freelist; otherwise it just dec's. The box was
// alloc'd as `structFieldLayout size + 8` rc header, so __fern_box_free
// frees base = data-8, size+8 (structFieldLayout's size already
// accounts for the header). Works for a childless struct too: the
// field loop is empty, so it just is_unique-gates and frees the box.
func genStructDropFn(name string, sd *ast.StructDecl, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, dynRcSupported bool) *Func {
	offs, size := structFieldLayout(sd.Fields, ptrW)
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
	}
	for _, f := range sd.Fields {
		_, isTwoWordStr := f.Type.(ast.StringType)
		isTwoWordStr = isTwoWordStr && ast.UseTwoWordStrings(ptrW)
		_, isNativeStr := f.Type.(ast.StringType)
		isNativeStr = isNativeStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW)
		if !arrElemIsRcTracked(f.Type) && !isTwoWordStr && !isNativeStr {
			continue
		}
		ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
		if off := offs[f.Name]; off != 0 {
			ops = append(ops, Op{Kind: OpConstI32, I32: off}, Op{Kind: OpAdd})
		}
		if isTwoWordStr {
			// Two-word string field: load (data, len) and reclaim via
			// __fern_str_dec at the struct's last reference. Inline and
			// static-literal strings are no-ops (flag / sentinel); a
			// headered heap buffer frees at its own rc==1. The field was
			// retained on construction (emitAliasInc → __fern_str_inc),
			// so this dec balances. Direct string fields only — a string
			// nested in an array / tuple / enum field reclaims via that
			// container's own (future) string-aware drop.
			ops = append(ops, Op{Kind: OpLoad, Width: WidthString})
			ops = append(ops,
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		if isNativeStr {
			// Native single-word string field (x86_64, !TwoWordOverride):
			// load the single data pointer and reclaim via __fern_str_dec
			// — at the struct's last reference, and only when the field's
			// own rc hits 1, the heap buffer is freed (size at data-4);
			// inline-SSO / literal / sentinel / shared (rc>1) sources defer
			// to __fern_rc_dec. The field was retained on construction
			// (field-init emitAliasInc → __fern_rc_inc when the initialiser
			// aliases, or moved in when fresh-owned), so this free is
			// exactly balanced. Mirrors the two-word str_dec branch above.
			ops = append(ops, Op{Kind: OpLoad, Width: WidthPtr})
			ops = append(ops,
				Op{Kind: OpCallDirect, Str: "__fern_str_dec", I32: 1},
				Op{Kind: OpDrop})
			continue
		}
		ops = append(ops, Op{Kind: OpLoad, Width: WidthPtr})
		ops = appendChildDrop(ops, f.Type, info, ptrW, reg, tupleReg, dynRcSupported)
	}
	ops = append(ops,
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpConstI32, I32: size},
		Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2},
		Op{Kind: OpDrop},
		Op{Kind: OpElse},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn})
	return &Func{
		Name:       "__drop_struct_" + name,
		Params:     []ast.Param{{Name: "__ds", Type: ast.NumberType{}}},
		ReturnType: ast.NumberType{},
		Ops:        ops,
	}
}

// genEnumDropFn builds the tag-dispatched __drop_enum_<Name> function
// for a concrete enum: at the value's last reference (rc==1) it reads
// the tag, and in each variant arm — where the payload type is
// statically exact — deep-drops the variant's payloads (recursing via
// appendChildDrop) then frees the box with THAT variant's size;
// otherwise it dec's. Payloadless / sentinel values fail the is_unique
// gate and take the dec path. Mirrors the inline non-uniform enum drop
// (emitDec), but as a standalone fn so a nested enum field / payload /
// capture can route to it. Slots: 0=ptr (param), 1=tag (scratch).
func genEnumDropFn(name string, ed *ast.EnumDecl, info *checker.Info, ptrW int, reg map[string]*ast.EnumDecl, tupleReg map[string]ast.TupleType, dynRcSupported bool) *Func {
	plan, ok := enumVariantDropPlan(ed, ptrW, dynRcSupported)
	if !ok {
		return nil
	}
	ops := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpIf, I32: BlockTypeVoid},
		// tag = mem[ptr+0] → slot 1 (stashed so arms read it after a
		// variant's box_free has freed the box).
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoad},
		{Kind: OpStoreLocal, I32: 1},
	}
	for _, vd := range plan {
		ops = append(ops,
			Op{Kind: OpLoadLocal, I32: 1},
			Op{Kind: OpConstI32, I32: int32(vd.tag)},
			Op{Kind: OpEq},
			Op{Kind: OpIf, I32: BlockTypeVoid})
		for _, ld := range vd.loads {
			// A CLOSURE payload is a DOCUMENTED SAFE LEAK, the same shape as
			// the Map payload below. A variant's payloads are stored without
			// a retain unless the enum is EnumRcPayloads-eligible, and a
			// matched arm's binding takes the reference out of the box under
			// the move model — so deep-releasing one here frees an env the
			// binding is still calling through. `async.Future[T]`'s
			// `Pending(i32, (i32) => Future[T])` is exactly that: the
			// combinators match a Pending, call its `resume`, and build the
			// next Future from the result (SIGSEGV on both natives, wasm
			// out-of-bounds trap — the whole SimProperty corpus). The box
			// itself is still freed by __fern_box_free below; the pair and
			// its env leak, which is what they did before container-held
			// closures were released at all (#6443).
			if _, isFn := ld.typ.(*ast.FuncType); isFn {
				continue
			}
			if isMapType(ld.typ) {
				// Map-in-enum is a DOCUMENTED SAFE LEAK (see enumRcPayloadsEligible,
				// ~ir.go:9085): a Map-payload variant's box carries an un-inc'd map
				// (the enum is excluded from EnumRcPayloads), and __map_drop_values —
				// the value-column reclaimer this drop would call via appendChildDrop
				// — lives in core/map.fern, which a program can use the enum WITHOUT
				// importing (e.g. a `JsonValue[]` built from `JString` values: the
				// whole-enum drop glue still emits the JObject arm, but core/map was
				// never loaded, so the call was to an absent symbol — the wasm
				// "unknown callee __map_drop_values" build error, #4425). Skip the
				// map reclaim entirely: the map's buffer + values leak (safe — nothing
				// dangles), consistent with the enum's leak-mode exclusion. The box
				// itself is still freed by __fern_box_free below.
				continue
			}
			ops = append(ops, Op{Kind: OpLoadLocal, I32: 0})
			if ld.off != 0 {
				ops = append(ops, Op{Kind: OpConstI32, I32: ld.off}, Op{Kind: OpAdd})
			}
			ops = append(ops, payloadLoadOpFor(ld.typ, ptrW))
			ops = appendChildDrop(ops, ld.typ, info, ptrW, reg, tupleReg, dynRcSupported)
		}
		ops = append(ops,
			Op{Kind: OpLoadLocal, I32: 0},
			Op{Kind: OpConstI32, I32: vd.size},
			Op{Kind: OpCallDirect, Str: "__fern_box_free", I32: 2},
			Op{Kind: OpDrop},
			Op{Kind: OpEnd})
	}
	ops = append(ops,
		Op{Kind: OpElse},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpReturn})
	return &Func{
		Name:         "__drop_enum_" + name,
		Params:       []ast.Param{{Name: "__de", Type: ast.NumberType{}}},
		ScratchTypes: []ast.Type{ast.NumberType{}},
		ReturnType:   ast.NumberType{},
		Ops:          ops,
	}
}

// uniformEnumDropLoads reports the per-box droppable payload loads
// for an enum IFF every payload-carrying variant shares an
// identical droppable signature — same offsets, and the same
// array-drop-vs-flat-dec kind at each. In that case the loads can
// be emitted unconditionally inside the is_unique guard with no
// runtime tag switch, because every heap box of this enum (whatever
// its tag) holds droppable pointers at exactly those offsets.
//
// This is the union shape (`type V = A | B | ...`): each variant
// carries a single struct pointer at offset 4. Payloadless
// variants (sentinels — never heap boxes) don't constrain the
// signature and are skipped. Returns (nil, false) when no variant
// has a droppable payload, or when payload-carrying variants
// disagree — those enums fall back to the plain box dec (their
// payloads leak, which is safe under no-free). Generic ParamType
// payloads are not statically droppable, so generic enums return
// (nil, false) too.
func uniformEnumDropLoads(ed *ast.EnumDecl, ptrW int) ([]enumDropLoad, bool) {
	dropKind := func(t ast.Type) (int, bool) {
		if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) {
			return 1, true // recursive array drop
		}
		if arrElemIsRcTracked(t) {
			return 2, true // flat dec (struct / enum / closure)
		}
		if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			return 3, true // two-word string dec (__fern_str_dec)
		}
		if _, isStr := t.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			return 4, true // single-word native string dec (__fern_rc_dec)
		}
		return 0, false
	}
	var want []enumDropLoad
	var wantKey string
	have := false
	for _, v := range ed.Variants {
		if len(v.Payloads) == 0 {
			continue // payloadless ⇒ static sentinel, no box
		}
		offsets, _ := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
		var loads []enumDropLoad
		key := ""
		for i, pt := range v.Payloads {
			kind, ok := dropKind(pt)
			if !ok {
				continue
			}
			loads = append(loads, enumDropLoad{off: offsets[i], typ: pt})
			key += fmt.Sprintf("%d:%d;", offsets[i], kind)
		}
		if len(loads) == 0 {
			// A payload-carrying variant with NO droppable payload
			// (e.g. Some(i32), JBool(bool)) breaks uniformity: a box
			// of that variant has nothing to drop at the shared
			// offsets, so an unconditional dec would be wrong.
			return nil, false
		}
		if !have {
			want, wantKey, have = loads, key, true
			continue
		}
		if key != wantKey {
			return nil, false
		}
	}
	if !have {
		return nil, false
	}
	return want, true
}

// uniformEnumBoxSize reports the heap-box payload size shared by every
// payload-carrying variant of an enum, IFF they all agree. An enum box
// is alloc'd per-variant as `payloadLayout size + rcHeaderBytes`, so
// freeing it at drop needs a statically-known size — only possible
// when the variants don't disagree (e.g. a union of single-pointer
// variants all size to the same box). Returns (size, false) when
// variants disagree or none carry a payload; such enums keep leaking
// their box (safe under the rc==1 gate). Pairs with
// uniformEnumDropLoads: an enum frees its box only when BOTH agree.
func uniformEnumBoxSize(ed *ast.EnumDecl, ptrW int) (int32, bool) {
	var size int32
	have := false
	for _, v := range ed.Variants {
		if len(v.Payloads) == 0 {
			continue // payloadless ⇒ static sentinel, no heap box
		}
		_, sz := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
		if !have {
			size, have = sz, true
		} else if sz != size {
			return 0, false
		}
	}
	if !have {
		return 0, false
	}
	return size, true
}

// enumVariantDropPlan returns a per-variant drop plan for an enum whose
// payload-carrying variants DON'T share a uniform layout (so the
// uniform branchless path doesn't apply). emitDec emits a tag switch
// over these: each real box (rc==1) reads its tag, drops that variant's
// droppable payloads, and frees with that variant's exact box size.
// Payloadless variants are static sentinels (never rc==1 boxes), so
// they're skipped. Bails (false) if any variant carries a generic
// ParamType payload — its drop-kind / size isn't statically known, so
// the enum keeps leaking its box (safe). Mirrors uniformEnumDropLoads'
// dropKind classification.
func enumVariantDropPlan(ed *ast.EnumDecl, ptrW int, dynRcSupported bool) ([]variantDrop, bool) {
	dropKind := func(t ast.Type) (int, bool) {
		if _, isParam := t.(ast.ParamType); isParam {
			return 0, false
		}
		if at, ok := t.(ast.ArrayType); ok && arrElemIsRcTracked(at.Elem) {
			return 1, true // recursive array drop
		}
		if arrElemIsRcTracked(t) {
			return 2, true // flat dec (struct / enum / closure)
		}
		if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
			return 3, true // two-word string dec (__fern_str_dec)
		}
		if _, isStr := t.(ast.StringType); isStr && ptrW == 8 && !ast.UseTwoWordStrings(ptrW) {
			return 4, true // single-word native string dec (__fern_rc_dec)
		}
		// `dyn Trait` payload (docs/DYN-TRAITS.md §7.8): the per-set
		// __drop_dyn_<set> destructor reclaims the concrete + boxed cell.
		// NATIVES ONLY (dynRcSupported, ptrW==8) — wasm's inline two-word
		// `dyn` double-drops when the payload is matched-and-bound, so it
		// stays scalar-leaking here (correct-but-never-double-free); see
		// appendChildDrop's dyn arm + §7.8.
		if _, isDyn := t.(ast.DynTraitType); isDyn && dynRcSupported {
			return 5, true // dyn drop (__drop_dyn_<set>, void)
		}
		return 0, false // scalar — nothing to drop
	}
	var plan []variantDrop
	for i, v := range ed.Variants {
		if len(v.Payloads) == 0 {
			continue // payloadless ⇒ static sentinel, no heap box
		}
		offsets, size := payloadLayout(v.Payloads, len(v.Payloads), ptrW)
		var loads []enumDropLoad
		for j, pt := range v.Payloads {
			if _, isParam := pt.(ast.ParamType); isParam {
				return nil, false // generic payload — can't size/drop safely
			}
			if _, ok := dropKind(pt); !ok {
				continue
			}
			loads = append(loads, enumDropLoad{off: offsets[j], typ: pt})
		}
		plan = append(plan, variantDrop{tag: i, loads: loads, size: size})
	}
	if len(plan) == 0 {
		return nil, false
	}
	return plan, true
}

// rcTrackedForFlatDec reports whether a local of type t is released by a flat
// single-word __fern_rc_dec. Strings are excluded: their retain/release is
// two-word on wasm + arm64-TwoWordOverride (__fern_str_inc/_dec), so a flat dec
// would consume one of the two stack words and misread a length as a pointer —
// the same hazard emitAliasInc's string arm documents. dyn values carry no rc
// header. Used by the ctorAliasInced release in emitVarReinitDropOld.
func rcTrackedForFlatDec(t ast.Type) bool {
	switch t.(type) {
	case ast.StringType, ast.DynTraitType:
		return false
	}
	return arrElemIsRcTracked(t)
}

// ownArgNeedsRetain reports whether an argument in an explicit `own` parameter
// position must be retained before the call. It is true for an occurrence of
// one of THIS function's `own` params that move-on-call did not mark as the
// consuming transfer (b.rc.ownCallMoveArgs).
//
// The exit sweep decs an `own` param unless computeMovedLocals marked it moved,
// and that marking is whole-function and keyed on the param's textually-LAST
// occurrence. A function that transfers the param on one branch and returns it
// on another has its last occurrence on the RETURN, so the transfer goes
// unmarked: the callee consumes a reference the caller never gave up, and the
// sweep frees the same box a second time. Retaining here restores the balance —
// the callee's drop spends the extra reference and the sweep's dec spends this
// frame's own. Where the move IS marked, nothing changes (no inc, sweep
// skipped), so already-correct code keeps its exact rc traffic.
//
// Plain locals are excluded: E051 only admits one in an `own` position as a
// self-reassign `x = f(…, x, …)`, whose old binding is dropped by the callee
// and whose overwrite-dec callConsumesIdent already suppresses.
func (b *builder) ownArgNeedsRetain(a ast.Expr) bool {
	if !ast.RcFreeEnabled {
		return false
	}
	id, ok := a.(*ast.Ident)
	if !ok || b.rc.ownCallMoveArgs[a] {
		return false
	}
	for _, p := range b.fn.Params {
		if p.Name != id.Name {
			continue
		}
		// Only an explicitly-`own` param is swept unconditionally here; a
		// borrowed or owned-by-default one is covered by its own rules.
		return p.Own && rcTrackedSlotType(p.Type) && b.rc.freeEligible[p.Name]
	}
	return false
}
