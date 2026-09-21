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
// releases it. The kernel needs none of that: the constant is read out of the
// element function's body at compile time, so the whole range collapses to
// the constant and the runtime call. Taking the release with the build is
// what keeps the refcounts balanced — this pass runs after RC insertion, so
// it may not leave a built closure unreleased or a release with nothing to
// release.
//
// The receiver is untouched, and does not need to be: `map` borrows it and
// `__fern_scale_f64` borrows it (`copyingBuiltinArgs`), and both answer a
// fresh rc=1 buffer (`rcResultOwned`). The swap is refcount-neutral by
// construction rather than by re-analysis.
//
// # What it does not take
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

// scaleF64Map is one recognized `map` over f64 by a constant.
type scaleF64Map struct {
	first, last int     // op range to replace, inclusive
	k           float64 // the constant the element function multiplies by
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
	// A capture could make the factor differ per element, and the kernel
	// takes one scalar. The runtime guard R7 leans on does not exist here —
	// there is nothing to guard — so this is a static requirement.
	if op := fn.Ops[first]; op.Kind == OpMakeClosure && op.I32 != 0 {
		return scaleF64Map{}, false
	}
	k, ok := constantF64Factor(byName, name)
	if !ok {
		return scaleF64Map{}, false
	}
	return scaleF64Map{first: first, last: scaleRangeEnd(fn, c), k: k}, true
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

// emitScaleF64Map replaces the range with the constant and the kernel call.
// The receiver is already on the stack: it was pushed before the range opened
// and the range never touched it.
func emitScaleF64Map(fn *Func, p scaleF64Map) {
	out := []Op{
		{Kind: OpConstF64, F64: p.k},
		{Kind: OpCallDirect, Runtime: true, Str: "__fern_scale_f64", Width: ResAddr, I32: 2,
			Ext: &OpExt{ArgTypes: []ast.Type{ast.ArrayType{Elem: f64Type}, f64Type}}},
	}
	next := make([]Op, 0, len(fn.Ops))
	next = append(next, fn.Ops[:p.first]...)
	next = append(next, out...)
	next = append(next, fn.Ops[p.last+1:]...)
	fn.Ops = next
}
