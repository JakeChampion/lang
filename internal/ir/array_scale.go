package ir

import (
	"os"

	"github.com/jakechampion/lang/internal/ast"
)

// The first kernel a recognized `std/array` shape actually lowers to (#9735,
// step 4). Everything before this recognized shapes and reported verdicts;
// this one rewrites.
//
//	xs.map((x: f64): f64 => x * 2.0)
//
// is the `scale_f64` shape written the natural way, and it ran the scalar
// loop while `xs.scale_f64(2.0)` — the same computation, spelled as the
// wrapper — ran the vectorised kernel. docs/ATLAS-PLATFORM-PLAN.md §3 calls
// that shape "one op, scalars in, buffer out, vectors confined inside", and
// names it among the first kernels to build. CLAUDE.md's rule is the reason
// it is fixed HERE rather than by telling callers to write the wrapper: the
// compiler is what was slow, so every caller gains rather than the one that
// was rewritten.
//
// # Why this is a swap and not a loop
//
// The replaced range builds a closure, parks it, loads it, calls `map`, and
// releases it. The kernel needs none of that: the whole range collapses to
// the factor and the runtime call. Taking the release with the build is what
// keeps the refcounts balanced — this pass runs after RC insertion, so it may
// not leave a built closure unreleased or a release with nothing to release.
//
// The factor comes from one of two places. A literal is read out of the
// element function's body at compile time and emitted as a constant. A
// CAPTURED factor — `var k: f64 = 2.5; xs.map((x: f64): f64 => x * k)`, the
// spelling a reader reaches for as soon as the factor has a name — is already
// on the stack: closure conversion pushes each captured value immediately
// before the build that packs it, so deleting the build leaves the value
// standing exactly where the kernel wants it and nothing is emitted at all.
//
// The receiver is untouched, and does not need to be: `map` borrows it and
// `__fern_scale_f64` borrows it (`copyingBuiltinArgs`), and both answer a
// fresh rc=1 buffer (`rcResultOwned`). The swap is refcount-neutral by
// construction rather than by re-analysis.
//
// # What it does not take
//
// A capture the kernel cannot reduce to one scalar factor: two of them, one
// the body does more than multiply by, or one the enclosing function assigns
// anywhere — which boxes the variable, so the closure captures a CELL whose
// push carries an rc.inc and whose contents are read through an indirection.
//
// It runs after `MapOwnedArrayInPlace`, so an `own` receiver has already
// become R7's in-place loop and this never sees it. That order is deliberate:
// R7 allocates NOTHING, and its zero-allocation behaviour is a contract
// `fip`/E068 checks, so a kernel that allocated a fresh buffer in its place
// would break a claim a program is allowed to make. A vectorised copy does
// not beat no copy at all.
func ScaleF64Maps(prog *Program) int {
	// An off switch, for the reason fusion and R7 have one: a miscompilation
	// suspected here is confirmed or ruled out in one run.
	if os.Getenv("FERN_NO_SCALE_KERNEL") == "1" {
		return 0
	}
	n := 0
	cx := newArrayContext(prog)
	// Indexed once: the element function is looked up by name per candidate,
	// and a linear scan there makes the pass quadratic in the program on
	// exactly the programs worth optimising.
	byName := make(map[string]*Func, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	for _, fn := range prog.Funcs {
		if cx.isStdlibBody(fn) {
			continue
		}
		for {
			p, ok := planScaleF64Map(byName, fn, cx)
			if !ok {
				break
			}
			emitScaleF64Map(fn, p)
			n++
		}
	}
	return n
}

// scaleF64Map is one recognized `map` over f64 by a scalar factor.
type scaleF64Map struct {
	first, last int     // op range to replace, inclusive
	k           float64 // the constant the element function multiplies by
	// captured says the factor is not a constant but the closure's single
	// captured value, which the ops before `first` already pushed. The
	// replacement then emits no constant and leaves that push standing.
	captured bool
}

func planScaleF64Map(byName map[string]*Func, fn *Func, cx arrayContext) (scaleF64Map, bool) {
	for _, c := range collectArrayCalls(fn, cx) {
		if p, ok := scaleF64Verdict(byName, fn, c); ok {
			return p, true
		}
	}
	return scaleF64Map{}, false
}

var f64Type = ast.FloatType{Width: 64}

// isF64 reads the width rather than comparing the type value: ast.FloatType
// carries the source Spelling it was written with, so an `==` against a
// hand-built f64 is false for every type the parser produced.
func isF64(t ast.Type) bool {
	ft, ok := t.(ast.FloatType)
	return ok && ft.Width == 64
}

// scaleStageIsF64 reports whether the call is `f64[] -> f64[]` through an
// `(f64) => f64` element function, read from the monomorphised callee's own
// signature rather than inferred from the call.
func scaleStageIsF64(fn *Func, c arrayCall) bool {
	args := fn.Ops[c.op].ArgTypes()
	if len(args) != 2 {
		return false
	}
	at, ok := args[0].(ast.ArrayType)
	if !ok || !isF64(at.Elem) {
		return false
	}
	ft, isFn := args[1].(*ast.FuncType)
	return isFn && len(ft.Params) == 1 && isF64(ft.Params[0]) && isF64(ft.Result)
}

func scaleF64Verdict(byName map[string]*Func, fn *Func, c arrayCall) (scaleF64Map, bool) {
	if c.verb != "map" {
		return scaleF64Map{}, false
	}
	// The stage neither changes the element type nor leaves f64: the kernel
	// reads an f64 buffer and writes one.
	if !scaleStageIsF64(fn, c) {
		return scaleF64Map{}, false
	}
	name, first, ok := scaleElement(fn, c)
	if !ok {
		return scaleF64Map{}, false
	}
	// The receiver has to be pushed BEFORE the range opens, because the
	// replacement leaves it where it is and pushes only the factor. Nothing
	// about the element function's position guarantees that: a closure bound
	// to a variable is built at its `var`, which is before the receiver is
	// evaluated, so a range opening there would delete the receiver push and
	// land the kernel on an empty stack.
	//
	// The operand stack says it exactly. Between the range opening and the
	// call, the only thing pushed may be the element function itself — one
	// value. Two means the receiver is in there too.
	// Where the replaced range opens. For a constant factor that is the
	// element function's build; for a captured one it is the push of the
	// captured value, which the replacement keeps.
	open, captured := first, false
	if op := fn.Ops[first]; op.Kind == OpMakeClosure && op.I32 != 0 {
		// More than one capture cannot be a single scalar factor, and a
		// capture that is not the factor would change per element while the
		// kernel takes one scalar.
		if !capturedF64Factor(byName, name, op) {
			return scaleF64Map{}, false
		}
		s, ok := capturedFactorPush(fn, first)
		if !ok {
			return scaleF64Map{}, false
		}
		open, captured = s, true
	}
	if !scalePushesOnlyTheElement(fn, open, c.op) {
		return scaleF64Map{}, false
	}
	if captured {
		return scaleF64Map{first: first, last: scaleRangeEnd(fn, c), captured: true}, true
	}
	k, ok := constantF64Factor(byName, name)
	if !ok {
		return scaleF64Map{}, false
	}
	return scaleF64Map{first: first, last: scaleRangeEnd(fn, c), k: k}, true
}

// capturedF64Factor reads `x * k` out of a closure body whose k is its single
// captured value, and reports whether the body is exactly that.
//
// Closure conversion appends a synthetic `__env` parameter, so the capture is
// read as `__env + offset` and loaded at the capture's own width. With one
// capture there is one field, at offset 0, and an `f.load` of width 64 over it
// is what makes the captured value an f64 rather than an i64 of the same size
// — the env block records only byte sizes.
//
// The element and the capture may be multiplied in either order: IEEE-754
// multiplication is commutative bit for bit, NaN and signed zero included.
func capturedF64Factor(byName map[string]*Func, name string, closure Op) bool {
	if closure.I32 != 1 {
		return false
	}
	fn := byName[name]
	if fn == nil || len(fn.Params) < 2 {
		return false
	}
	env := int32(len(fn.Params) - 1)
	body := make([]Op, 0, 8)
	for _, op := range fn.Ops {
		if op.Kind != OpLine {
			body = append(body, op)
		}
	}
	// The capture read, as four ops, and the element read as one. They differ
	// only in which comes first.
	capture := func(at int) bool {
		return at+3 < len(body) &&
			body[at].Kind == OpLoadLocal && body[at].I32 == env &&
			body[at+1].Kind == OpConstI32 && body[at+1].I32 == 0 &&
			body[at+2].Kind == OpAdd &&
			body[at+3].Kind == OpFLoad && body[at+3].Width == 64
	}
	element := func(at int) bool {
		return at < len(body) &&
			body[at].Kind == OpLoadLocal && body[at].I32 >= 0 && body[at].I32 < env
	}
	var tail int
	switch {
	case element(0) && capture(1):
		tail = 5
	case capture(0) && element(4):
		tail = 5
	default:
		return false
	}
	return len(body) == tail+2 &&
		body[tail].Kind == OpFMul && body[tail].Width == 64 &&
		body[tail+1].Kind == OpReturn
}

// capturedFactorPush names the op that pushes the single captured value.
// `ast.MakeClosure` evaluates captures in declaration order immediately before
// the op that consumes them, and a scalar capture is a plain variable read, so
// that is one `local.load`. Anything else declines rather than being decoded:
// an f64 needs no inc, so a push that is more than a load is not the shape
// this recognises.
func capturedFactorPush(fn *Func, closure int) (int, bool) {
	for i := closure - 1; i >= 0; i-- {
		if fn.Ops[i].Kind == OpLine {
			continue
		}
		if fn.Ops[i].Kind == OpLoadLocal {
			return i, true
		}
		return 0, false
	}
	return 0, false
}

// constantF64Factor reads `x * k` out of the element function's body and
// answers k. The body has to be exactly that: the element, a constant, one
// multiply, a return. Multiplication is commutative in IEEE-754 including
// around NaN and signed zero — `x * k` and `k * x` produce the same bits — so
// the operand order is not constrained.
//
// The single load is the element without having to say which slot it is. A
// lambda carries an environment parameter beside the element, so the slot
// numbering is not the source's; but the caller has already established that
// the closure captures nothing, so the environment holds nothing to read, and
// a body of one load, one f64 constant and one f64 multiply can only be
// multiplying the element.
func constantF64Factor(byName map[string]*Func, name string) (float64, bool) {
	fn := byName[name]
	if fn == nil || len(fn.Params) == 0 {
		return 0, false
	}
	var sawParam, sawMul, sawReturn bool
	var k float64
	var sawConst bool
	for _, op := range fn.Ops {
		switch op.Kind {
		case OpLine:
			continue
		case OpLoadLocal:
			if op.I32 < 0 || int(op.I32) >= len(fn.Params) || sawParam || sawMul {
				return 0, false
			}
			sawParam = true
		case OpConstF64:
			if sawConst || sawMul {
				return 0, false
			}
			k, sawConst = op.F64, true
		case OpFNeg:
			// A negative literal reaches the IR as the positive constant and
			// a negate — `x * -1.5` is `const.f64 1.5; f.neg`. Folding it
			// here is exact: IEEE-754 negation only flips the sign bit.
			// Required to follow the constant, so the negate cannot be the
			// one in `(-x) * 1.5`, which is a different body.
			if !sawConst || sawMul {
				return 0, false
			}
			k = -k
		case OpFMul:
			if sawMul || !sawParam || !sawConst {
				return 0, false
			}
			sawMul = true
		case OpReturn:
			if !sawMul {
				return 0, false
			}
			sawReturn = true
		default:
			return 0, false
		}
	}
	return k, sawReturn
}

// scalePushesOnlyTheElement reports whether the span from the range's opening
// to the call pushes exactly one value and never reads below where it
// started. That one value is the element function, which is what makes the
// range safe to delete: the receiver is already on the stack beneath it and
// the replacement puts the factor in the element function's place.
//
// A captured factor opens the span at its own push rather than at the closure
// build, and the sum is the same one value — the build consumes the capture
// and answers the closure. So the guard reads a capturing closure bound to a
// VARIABLE the same way it reads a non-capturing one: the receiver push falls
// inside the span, the sum is two, and the site declines. Without it that
// shape rewrites into `operand stack underflow`.
//
// It reuses flatten.go's opStackEffect, so an op the table does not model
// declines the site rather than being counted as nothing.
func scalePushesOnlyTheElement(fn *Func, first, call int) bool {
	depth := 0
	for k := first; k < call; k++ {
		pops, pushes, ok := ndarrayOpEffect(fn.Ops[k])
		if !ok {
			return false
		}
		if depth -= pops; depth < 0 {
			// Reads a value pushed before the range, so deleting the range
			// would take something the call still needs.
			return false
		}
		depth += pushes
	}
	return depth == 1
}

// scaleElement names the element function and says where the replaced range
// opens. Two shapes reach the call, and the pipeline recogniser only follows
// the first: a lambda is parked in a slot by its make_closure and loaded back
// as the argument, while a NAMED function is pushed straight from its
// const_func and never parked — `xs.map(half)`, which is the spelling a
// reader writes first.
func scaleElement(fn *Func, c arrayCall) (string, int, bool) {
	if c.fnSlot >= 0 && c.element != "" {
		for i := c.op - 1; i >= 0; i-- {
			op := fn.Ops[i]
			if op.Kind != OpMakeClosure && op.Kind != OpConstFunc {
				continue
			}
			if i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpStoreLocal && fn.Ops[i+1].I32 == c.fnSlot {
				return c.element, i, true
			}
		}
		return "", 0, false
	}
	// Unparked: the function is the operand pushed immediately before the
	// call, and a bare const_func captures nothing by construction.
	for i := c.op - 1; i >= 0; i-- {
		if fn.Ops[i].Kind == OpLine {
			continue
		}
		if fn.Ops[i].Kind == OpConstFunc {
			return fn.Ops[i].Str, i, true
		}
		return "", 0, false
	}
	return "", 0, false
}

// scaleRangeEnd extends the range through the element function's release, so
// the build and the release leave together. A named function has none, and
// the range then ends at the call.
func scaleRangeEnd(fn *Func, c arrayCall) int {
	for k := c.op + 1; k < len(fn.Ops); k++ {
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
			return c.op
		default:
			return c.op
		}
	}
	return c.op
}

// emitScaleF64Map replaces the range with the factor and the kernel call. The
// receiver is already on the stack: it was pushed before the range opened and
// the range never touched it. A captured factor is already on the stack too —
// the closure build consumed it, and deleting the build leaves it standing
// where the kernel wants it — so only a constant factor is emitted here.
func emitScaleF64Map(fn *Func, p scaleF64Map) {
	out := make([]Op, 0, 2)
	if !p.captured {
		out = append(out, Op{Kind: OpConstF64, F64: p.k})
	}
	out = append(out, Op{Kind: OpCallDirect, Runtime: true, Str: "__fern_scale_f64", Width: ResAddr, I32: 2,
		Ext: &OpExt{ArgTypes: []ast.Type{ast.ArrayType{Elem: f64Type}, f64Type}}})
	next := make([]Op, 0, len(fn.Ops))
	next = append(next, fn.Ops[:p.first]...)
	next = append(next, out...)
	next = append(next, fn.Ops[p.last+1:]...)
	fn.Ops = next
}
