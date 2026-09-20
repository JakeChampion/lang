package ir

import (
	"os"

	"github.com/jakechampion/lang/internal/ast"
)

// Ownership-aware materialization for a same-shape `map` (#9733, phase 2 of
// #9727). Where fusion removes an intermediate because a consumer can absorb
// its producer, this handles the other case: the array result HAS to exist, so
// the only question is whose storage it is.
//
// `own xs` means the caller has relinquished its reference at the call
// boundary, so for a transform returning an array of the same shape the
// compiler — not the programmer — can establish that writing through the
// donor's buffer is observationally equivalent:
//
//	function twice(own xs: i64[]): i64[] { return xs.map(f); }
//
// Source semantics stay value-oriented; execution becomes the in-place loop.
//
// # The guard is not new
//
// Nothing here invents a uniqueness rule. `__fern_arr_cow_inplace` is the same
// dynamically-guarded helper `arr[i] = v` and `.with` already use: rc == 1
// returns the buffer unchanged, rc > 1 copies it and decrements the original.
// So a donor that turns out to be shared at run time still gets ordinary
// immutable semantics, which is #9727 §12's non-goal — reuse must never make
// mutation observable — discharged by construction rather than by analysis.
//
// The one improvement on the hand-written `.with` loop is WHERE the guard
// runs. `xs = xs.with(i, f(xs[i]))` re-asks per element; the array's identity
// cannot change inside the loop, so this asks once before it.
//
// # Why the epilogue is part of the rewrite
//
// An `own` parameter that is dead after the call is decremented by the
// lowering. Once the result IS the donor's buffer, that decrement would free
// what is being returned — so the replaced op range runs through it, and
// ownership transfers into the returned value exactly as it does from the
// hand-written loop this is modelled on.
func MapOwnedArrayInPlace(prog *Program, ptrW int) int {
	// An off switch, for the reason fusion has one: a miscompilation
	// suspected here is confirmed or ruled out in one run, and the baseline
	// this changes stays measurable.
	if os.Getenv("FERN_NO_ARRAY_INPLACE") == "1" {
		return 0
	}
	n := 0
	cx := newArrayContext(prog)
	for _, fn := range prog.Funcs {
		if cx.isStdlibBody(fn) {
			continue
		}
		for {
			p, ok := planInPlaceMap(fn, cx)
			if !ok {
				break
			}
			emitInPlaceMap(fn, p, ptrW)
			n++
		}
	}
	return n
}

// inPlaceMap is one recognized `own`-receiver same-shape map.
type inPlaceMap struct {
	first, last int   // op range to replace, inclusive
	recvSlot    int32 // the donated array
	fnSlot      int32 // the element function
	elemBytes   int32
	elemType    ast.Type
}

func planInPlaceMap(fn *Func, cx arrayContext) (inPlaceMap, bool) {
	for _, c := range collectArrayCalls(fn, cx) {
		if p, why := inPlaceVerdict(fn, c, cx); why == StorageReused {
			return p, true
		}
	}
	return inPlaceMap{}, false
}

// inPlaceVerdict decides one combinator call: the rewrite when every R7
// condition holds, or the first condition that does not. The report and the
// `fip` verifier read the same answer the pass acts on, so neither can claim
// a reuse the other did not perform.
func inPlaceVerdict(fn *Func, c arrayCall, cx arrayContext) (inPlaceMap, ArrayStorage) {
	if c.verb != "map" {
		return inPlaceMap{}, StorageNoInPlaceShape
	}
	// The licence comes first: a local is not enough, the donor has to be one
	// the caller has already let go of, and a parameter's `own` is where that
	// is recorded.
	if c.recv < 0 || !ownedParamSlot(fn, c.recv) {
		return inPlaceMap{}, StorageReceiverNotOwnedParam
	}
	// §1's purity boundary, for the same reason fusion has one: the element
	// function runs in a different order relative to the donor's storage than
	// it did, so it must not be able to observe that.
	if c.fnSlot < 0 || c.element == "" {
		return inPlaceMap{}, StorageElementFunctionUnresolved
	}
	if cx.effectful[c.element] {
		return inPlaceMap{}, StorageElementFunctionEffectful
	}
	// And it must capture nothing. An element function that closes over the
	// donor would read, mid-loop, elements this has already overwritten —
	// `xs.map(x => x + xs[0])` is the shape — where the original semantics
	// give every element the ORIGINAL `xs[0]`.
	//
	// The runtime guard happens to cover it: a capture holds a reference, so
	// the donor is not unique and `__fern_arr_cow_inplace` copies. But resting
	// a soundness property on a refcount is the wrong way round when a static
	// check says it outright, and the shape that reaches here today is
	// declined only because capturing changes the RC epilogue this pass
	// matches on — an accident, not a rule.
	if !elementFunctionCapturesNothing(fn, c) {
		return inPlaceMap{}, StorageElementFunctionCaptures
	}
	width, elemT, ok := elemWidthOf(fn.Ops[c.op])
	if !ok {
		return inPlaceMap{}, StorageElementWidthUnsupported
	}
	// Same SHAPE only. A `map` that changes the element type needs a buffer
	// of a different size, so the donor's is the wrong one — the case
	// docs/REUSE-CONTRACT.md calls an incompatible shape.
	if !sameElementTypeMap(fn, c, elemT) {
		return inPlaceMap{}, StorageShapeChange
	}
	first, ok := rangeStart(fn, c)
	if !ok {
		return inPlaceMap{}, StorageDonorNotReleasedHere
	}
	last, ok := inPlaceRangeEnd(fn, c)
	if !ok {
		return inPlaceMap{}, StorageDonorNotReleasedHere
	}
	return inPlaceMap{
		first: first, last: last, recvSlot: c.recv,
		fnSlot: c.fnSlot, elemBytes: width, elemType: elemT,
	}, StorageReused
}

// elementFunctionCapturesNothing reports whether the element function was
// built with no captures, so it cannot reach the donor at all.
func elementFunctionCapturesNothing(fn *Func, c arrayCall) bool {
	for i := c.op - 1; i >= 0; i-- {
		op := fn.Ops[i]
		if op.Kind == OpConstFunc && i+1 < len(fn.Ops) &&
			fn.Ops[i+1].Kind == OpStoreLocal && fn.Ops[i+1].I32 == c.fnSlot {
			return true
		}
		if op.Kind == OpMakeClosure && i+1 < len(fn.Ops) &&
			fn.Ops[i+1].Kind == OpStoreLocal && fn.Ops[i+1].I32 == c.fnSlot {
			return op.I32 == 0
		}
	}
	return false
}

// ownedParamSlot reports whether a slot is a parameter this function consumes.
// A local is not enough: the donor has to be one the caller has already let
// go of, and a parameter's `own` is where that is recorded.
func ownedParamSlot(fn *Func, slot int32) bool {
	if slot < 0 || int(slot) >= len(fn.Params) || int(slot) >= len(fn.ParamConsumed) {
		return false
	}
	return fn.ParamConsumed[slot]
}

// sameElementTypeMap reports whether the map's result element type matches its
// input's, read from the monomorphised callee's own signature rather than
// inferred from the call.
func sameElementTypeMap(fn *Func, c arrayCall, in ast.Type) bool {
	for _, t := range fn.Ops[c.op].ArgTypes() {
		ft, isFn := t.(*ast.FuncType)
		if !isFn || len(ft.Params) != 1 {
			continue
		}
		return ft.Params[0] == in && ft.Result == in
	}
	return false
}

// inPlaceRangeEnd extends the replaced range through the epilogue, and
// REQUIRES the donor's own decrement to be in it: without that decrement
// consumed, returning the donor's buffer would return something freed.
func inPlaceRangeEnd(fn *Func, c arrayCall) (int, bool) {
	end, sawRecvDec := c.op, false
	for k := c.op + 1; k < len(fn.Ops); k++ {
		op := fn.Ops[k]
		switch op.Kind {
		case OpLoadLocal, OpConstI32, OpDrop, OpLine:
			continue
		case OpCallDirect:
			if op.Str == "__drop_closure_value" {
				end = k + 1
				continue
			}
			if isReclaimCallee(op) && decsSlot(fn, k, c.recv) {
				end, sawRecvDec = k+1, true
				continue
			}
			return end, sawRecvDec
		default:
			return end, sawRecvDec
		}
	}
	return end, sawRecvDec
}

// decsSlot reports whether the reclaim call at k is releasing `slot`, by
// looking back for the load that pushed it.
func decsSlot(fn *Func, k int, slot int32) bool {
	for i := k - 1; i >= 0 && i > k-4; i-- {
		if fn.Ops[i].Kind == OpLoadLocal {
			return fn.Ops[i].I32 == slot
		}
	}
	return false
}

// emitInPlaceMap replaces the range with the guarded in-place loop.
func emitInPlaceMap(fn *Func, p inPlaceMap, ptrW int) {
	base := int32(len(fn.Params)) + int32(len(fn.Locals)) + int32(len(fn.ScratchTypes))
	buf, idx, length := base, base+1, base+2
	i32 := ast.NumberType{Width: 32, Signed: true}
	fn.ScratchTypes = append(fn.ScratchTypes, i32, i32, i32)

	var out []Op
	add := func(ops ...Op) { out = append(out, ops...) }

	// The element function, rebuilt in place exactly as the replaced range
	// built it.
	for k := p.first; k <= p.last; k++ {
		if op := fn.Ops[k]; op.Kind == OpMakeClosure || op.Kind == OpConstFunc {
			add(op)
			if k+1 <= p.last && fn.Ops[k+1].Kind == OpStoreLocal {
				add(fn.Ops[k+1])
			}
		}
	}

	// buf = xs, copied first if the donor turns out to be shared. Asked once:
	// the array's identity cannot change under the loop.
	add(Op{Kind: OpLoadLocal, I32: p.recvSlot}, Op{Kind: OpStoreLocal, I32: buf})
	add(Op{Kind: OpLoadLocal, I32: buf}, Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1})
	add(Op{Kind: OpIf, I32: BlockTypeVoid}, Op{Kind: OpElse})
	add(Op{Kind: OpLoadLocal, I32: buf}, Op{Kind: OpConstI32, I32: p.elemBytes})
	add(Op{Kind: OpCallDirect, Runtime: true, Str: "__fern_arr_cow_inplace", Width: ResAddr, I32: 2})
	add(Op{Kind: OpStoreLocal, I32: buf}, Op{Kind: OpEnd})

	// The length, read once off the buffer that survived the guard.
	add(Op{Kind: OpLoadLocal, I32: buf}, Op{Kind: OpConstI32, I32: 4}, Op{Kind: OpSub}, Op{Kind: OpLoad})
	add(Op{Kind: OpStoreLocal, I32: length})
	add(Op{Kind: OpConstI32}, Op{Kind: OpStoreLocal, I32: idx})

	add(Op{Kind: OpBlock}, Op{Kind: OpLoop})
	add(Op{Kind: OpLoadLocal, I32: idx}, Op{Kind: OpLoadLocal, I32: length})
	add(Op{Kind: OpLtS, Width: 32}, Op{Kind: OpNot}, Op{Kind: OpBrIf, I32: 1})

	// buf[i] = f(buf[i]). The address is computed twice rather than stashed,
	// because the element function runs between the read and the write and
	// the operand stack is not a safe place to keep an address across a call.
	add(Op{Kind: OpLoadLocal, I32: buf}, Op{Kind: OpLoadLocal, I32: idx})
	add(Op{Kind: OpCallDirect, Str: "__arr_idx_8_nc", Width: ResAddr, I32: 2})
	add(Op{Kind: OpLoad, Width: 64})
	add(Op{Kind: OpLoadLocal, I32: p.fnSlot}, Op{Kind: OpCallIndirect, I32: 1})
	add(Op{Kind: OpStoreLocal, I32: base + 3})

	add(Op{Kind: OpLoadLocal, I32: buf}, Op{Kind: OpLoadLocal, I32: idx})
	add(Op{Kind: OpCallDirect, Str: "__arr_idx_8_nc", Width: ResAddr, I32: 2})
	add(Op{Kind: OpLoadLocal, I32: base + 3}, Op{Kind: OpStore, Width: 64})

	add(Op{Kind: OpLoadLocal, I32: idx}, Op{Kind: OpConstI32, I32: 1}, Op{Kind: OpAdd, Width: 32})
	add(Op{Kind: OpStoreLocal, I32: idx})
	add(Op{Kind: OpBr}, Op{Kind: OpEnd}, Op{Kind: OpEnd})

	// The result is the donor's buffer, left where the map call's was. The
	// donor's decrement was inside the replaced range: ownership moves here.
	add(Op{Kind: OpLoadLocal, I32: buf})

	// The element function's release, re-emitted as the range carried it.
	for k := p.first; k <= p.last; k++ {
		if fn.Ops[k].Kind == OpCallDirect && fn.Ops[k].Str == "__drop_closure_value" {
			add(fn.Ops[k-1], fn.Ops[k], Op{Kind: OpDrop})
		}
	}

	fn.ScratchTypes = append(fn.ScratchTypes, p.elemType) // the element in flight
	next := make([]Op, 0, len(fn.Ops)+len(out))
	next = append(next, fn.Ops[:p.first]...)
	next = append(next, out...)
	next = append(next, fn.Ops[p.last+1:]...)
	fn.Ops = next
}
