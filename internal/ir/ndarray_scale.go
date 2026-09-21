package ir

import (
	"os"

	"github.com/jakechampion/lang/internal/ast"
)

// The first kernel an std/ndarray shape lowers to (#9735). Everything before
// this on the ndarray side recognized shapes and reported verdicts;
// docs/ARRAY-SHAPES.md §6 says so in as many words — "no kernel exists for
// any verb yet".
//
//	a.map((x: f64): f64 => x * k)
//
// over a handle whose layout is PACKED is `scale_f64` wearing a shape. Packed
// means `data` IS the reading order (§2), so the odometer walk in
// std/ndarray's `map` visits `data[0..n)` in order and the vectorised
// `__fern_scale_f64` computes the same buffer. The result keeps the receiver's
// shape, so the whole call is
//
//	from_flat(__fern_scale_f64(a.data, k), a.shape)
//
// and that is what this emits. Over a STRIDED receiver it is wrong — the walk
// is not contiguous — which is why the site is gated on the layout analysis
// rather than on the verb.
//
// # Why this needed the layout analysis
//
// `is_packed()` is a run-time predicate over the metadata, and until the
// layout pass there was no way to ask it before the program ran. §8's licence
// for an in-place elementwise kernel reads "consumed, unique, and
// `is_packed()`"; this takes the third conjunct for a kernel that allocates
// its own result, so it needs neither of the first two.
//
// # Refcounts
//
// `ndarray__from_flat__*` incs both arguments — it borrows them and the caller
// balances with its own drop, which is why std/ndarray's `map` can hand it a
// local buffer and let the frame release it. So the replacement holds one
// reference to the scaled buffer and releases it after the handle is built,
// and drops the receiver handle it consumed. Net: the new handle owns the
// scaled buffer and a count on the shape, the old buffer's count falls by one,
// and nothing leaks.
//
// A wrong gate fails LOUDLY rather than silently: `from_flat` aborts unless
// `count_of(shape) == data.len()`, which holds exactly when the receiver was
// packed.
func ScaleF64NdarrayMaps(prog *Program, ptrW int) int {
	// The off switch its std/array sibling has, for the same reason.
	if os.Getenv("FERN_NO_SCALE_KERNEL") == "1" {
		return 0
	}
	byName := make(map[string]*Func, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	// Settled once: the layout context walks every handle-returning function
	// to settle its summaries, and rebuilding it per candidate would make the
	// pass quadratic on exactly the programs worth optimising.
	cx := newNdarrayLayoutCtx(prog)
	n := 0
	for _, fn := range prog.Funcs {
		if isNdarrayBody(fn) {
			continue
		}
		for {
			p, ok := planNdarrayScale(byName, fn, cx)
			if !ok {
				break
			}
			emitNdarrayScale(fn, p, ptrW)
			n++
		}
	}
	return n
}

// ndarrayScaleMap is one recognized packed `map` by a scalar f64 factor.
type ndarrayScaleMap struct {
	first, last int     // op range to replace, inclusive
	k           float64 // the factor, when it is a constant
	captured    bool    // the factor is the closure's single capture, already pushed
	elem        string  // the monomorphised element type suffix, e.g. "f64"
}

func planNdarrayScale(byName map[string]*Func, fn *Func, cx *ndarrayLayoutCtx) (ndarrayScaleMap, bool) {
	recv, _ := ndarrayReceiverLayouts(fn, cx)
	for _, s := range ndarrayShapesInFunc(fn) {
		if p, ok := ndarrayScaleVerdict(byName, fn, s, recv); ok {
			return p, true
		}
	}
	return ndarrayScaleMap{}, false
}

// ndarrayScaleVerdict is the gate, in the order that declines cheapest first.
func ndarrayScaleVerdict(byName map[string]*Func, fn *Func, s NdarrayShape, recv map[int]NdarrayLayout) (ndarrayScaleMap, bool) {
	if s.Verb != "map" {
		return ndarrayScaleMap{}, false
	}
	// The receiver's storage has to BE the reading order, or the scalar walk
	// this replaces is not the buffer the kernel would read.
	if !recv[s.Op].ProvesPacked() {
		return ndarrayScaleMap{}, false
	}
	elem, ok := ndarrayScaleElemF64(fn, s)
	if !ok {
		return ndarrayScaleMap{}, false
	}
	// The replacement calls two functions by name. Both are std/ndarray's own
	// and both are reachable from any program that built such a handle, but
	// naming one the program does not define would emit a call to nothing.
	if byName["ndarray__from_flat__"+elem] == nil || byName[ndarrayDropGlue+elem] == nil {
		return ndarrayScaleMap{}, false
	}
	name := ""
	if len(s.Elements) == 1 {
		name = s.Elements[0].Name
	}
	if name == "" {
		return ndarrayScaleMap{}, false
	}
	first, ok := ndarrayScaleElementBuild(fn, s.Op)
	if !ok {
		return ndarrayScaleMap{}, false
	}
	open, captured := first, false
	if op := fn.Ops[first]; op.Kind == OpMakeClosure && op.I32 != 0 {
		if !capturedF64Factor(byName, name, op) {
			return ndarrayScaleMap{}, false
		}
		p, ok := capturedFactorPush(fn, first)
		if !ok {
			return ndarrayScaleMap{}, false
		}
		open, captured = p, true
	}
	// The same guard its std/array sibling needs, for the same reason: the
	// receiver must already be on the stack beneath the range, so the range
	// may push exactly the element function and nothing else.
	if !scalePushesOnlyTheElement(fn, open, s.Op) {
		return ndarrayScaleMap{}, false
	}
	last := ndarrayScaleRangeEnd(fn, s.Op)
	if captured {
		return ndarrayScaleMap{first: first, last: last, captured: true, elem: elem}, true
	}
	k, ok := constantF64Factor(byName, name)
	if !ok {
		return ndarrayScaleMap{}, false
	}
	return ndarrayScaleMap{first: first, last: last, k: k, elem: elem}, true
}

// ndarrayScaleElemF64 reports whether the site is f64 in and f64 out, read
// from the call's own argument types rather than inferred, and answers the
// monomorphised suffix the emitted `from_flat` needs.
func ndarrayScaleElemF64(fn *Func, s NdarrayShape) (string, bool) {
	args := fn.Ops[s.Op].ArgTypes()
	if len(args) != 2 {
		return "", false
	}
	ft, isFn := args[1].(*ast.FuncType)
	if !isFn || len(ft.Params) != 1 || !isF64(ft.Params[0]) || !isF64(ft.Result) {
		return "", false
	}
	return "f64", true
}

// ndarrayScaleElementBuild names the op that builds the element function.
//
// Two shapes reach the call. A lambda is PARKED — `make_closure ; store.local
// S ; load.local S ; call` — so the build is found by the slot the load names,
// not by walking back over the store, which is how the first version of this
// missed every site it was written for. A named function is pushed straight
// from its `const_func` and never parked.
func ndarrayScaleElementBuild(fn *Func, call int) (int, bool) {
	prev := -1
	for i := call - 1; i >= 0; i-- {
		if fn.Ops[i].Kind == OpLine {
			continue
		}
		prev = i
		break
	}
	if prev < 0 {
		return 0, false
	}
	switch fn.Ops[prev].Kind {
	case OpConstFunc:
		return prev, true
	case OpLoadLocal:
		slot := fn.Ops[prev].I32
		for i := prev - 1; i >= 0; i-- {
			op := fn.Ops[i]
			if op.Kind != OpMakeClosure && op.Kind != OpConstFunc {
				continue
			}
			if i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpStoreLocal && fn.Ops[i+1].I32 == slot {
				return i, true
			}
		}
	}
	return 0, false
}

// ndarrayScaleRangeEnd extends the range through the element function's
// release, so the build and the release leave together.
func ndarrayScaleRangeEnd(fn *Func, call int) int {
	for k := call + 1; k < len(fn.Ops); k++ {
		switch op := fn.Ops[k]; op.Kind {
		case OpLoadLocal, OpLine:
			continue
		case OpCallDirect:
			if op.Str == "__drop_closure_value" {
				if k+1 < len(fn.Ops) && fn.Ops[k+1].Kind == OpDrop {
					return k + 1
				}
				return k
			}
			return call
		default:
			return call
		}
	}
	return call
}

// ndarrayDropGlue is the prefix of the generated drop helper for a
// monomorphised handle.
const ndarrayDropGlue = "__drop_struct_ndarray__NdArray__"

// emitNdarrayScale replaces the range with the kernel and the handle it
// rebuilds. The receiver is already on the stack — it was pushed before the
// range opened and the range never touched it — and so is a captured factor,
// which the deleted closure build would have consumed.
func emitNdarrayScale(fn *Func, p ndarrayScaleMap, ptrW int) {
	base := int32(len(fn.Params)) + int32(len(fn.Locals)) + int32(len(fn.ScratchTypes))
	recv, factor, scaled := base, base+1, base+2
	// `ptr` holds a full pointer on the 64-bit targets, and is declared i32
	// anyway: ScratchTypes selects a slot's CLASS — integer or float — and
	// the backend sizes it from what is stored, which is why every other
	// pointer stash in this package declares the same (array_fusion.go's
	// boxBase, array_inplace.go's buf). Widening it to match the target is
	// not a correction: it would diverge from those for no gain and is
	// unverified on wasm32.
	ptr := ast.NumberType{Width: 32, Signed: true}
	fn.ScratchTypes = append(fn.ScratchTypes, ptr, f64Type, ptr)

	var out []Op
	add := func(ops ...Op) { out = append(out, ops...) }

	// Both operands come off the stack into slots: the kernel wants them in
	// the other order, and an address is not a safe thing to keep on the
	// operand stack across a call.
	if !p.captured {
		add(Op{Kind: OpConstF64, F64: p.k})
	}
	add(Op{Kind: OpStoreLocal, I32: factor})
	add(Op{Kind: OpStoreLocal, I32: recv})

	// scaled = __fern_scale_f64(a.data, k). `data` is the handle's first
	// field, so reading it needs no displacement.
	add(Op{Kind: OpLoadLocal, I32: recv}, Op{Kind: OpLoad, Width: WidthPtr})
	add(Op{Kind: OpLoadLocal, I32: factor})
	add(Op{Kind: OpCallDirect, Runtime: true, Str: "__fern_scale_f64", Width: ResAddr, I32: 2,
		Ext: &OpExt{ArgTypes: []ast.Type{ast.ArrayType{Elem: f64Type}, f64Type}}})
	add(Op{Kind: OpStoreLocal, I32: scaled})

	// from_flat(scaled, a.shape), whose result lands where map's did. `shape`
	// is the second field, one pointer along.
	add(Op{Kind: OpLoadLocal, I32: scaled})
	add(Op{Kind: OpLoadLocal, I32: recv}, Op{Kind: OpConstI32, I32: int32(ptrW)}, Op{Kind: OpAdd},
		Op{Kind: OpLoad, Width: WidthPtr})
	add(Op{Kind: OpCallDirect, Str: "ndarray__from_flat__" + p.elem, Width: ResAddr, I32: 2,
		Ext: &OpExt{ArgTypes: []ast.Type{
			ast.ArrayType{Elem: f64Type},
			ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}},
		}}})

	// Release this frame's reference to the scaled buffer: from_flat incremented
	// it, and the handle owns it from here.
	add(Op{Kind: OpLoadLocal, I32: scaled}, Op{Kind: OpConstI32, I32: 8})
	add(Op{Kind: OpCallDirect, Runtime: true, Str: "__fern_arr_dec", Width: ResAddr, I32: 2})
	add(Op{Kind: OpDrop})

	// Drop the handle `map` would have consumed.
	add(Op{Kind: OpLoadLocal, I32: recv})
	// Spelled as the lowering spells it: the drop helpers are emitted with no
	// result width, and a call that differs from the one the rest of the
	// program makes to the same function is a difference someone has to
	// explain later.
	add(Op{Kind: OpCallDirect, Str: ndarrayDropGlue + p.elem, I32: 1})
	add(Op{Kind: OpDrop})

	next := make([]Op, 0, len(fn.Ops)+len(out))
	next = append(next, fn.Ops[:p.first]...)
	next = append(next, out...)
	next = append(next, fn.Ops[p.last+1:]...)
	fn.Ops = next
}
