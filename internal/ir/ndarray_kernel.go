package ir

import (
	"fmt"
	"strings"
)

// What a kernel can do with each element function a recognized std/ndarray
// site was handed (#9735). Recognition says WHAT a site is
// (ndarray_shapes.go); this says whether a kernel could take it, and why not
// when it could not.
//
// The question is the one docs/ARRAY-SHAPES.md §6 already says the section
// exists to answer: "a site whose `mul` and `add` are multiplication and
// addition over `f64` is `dot_f64` in disguise, and one handed a closure
// that captures is not". Until now the report printed the two names and left
// the reader to go and look.
//
// The bar is docs/ATLAS-PLATFORM-PLAN.md §3's, and it is narrow on purpose: a
// kernel is ONE IR op whose whole vector lifetime stays inside its own
// emitted sequence, so a call to an element function is an op boundary and
// nothing survives it. A kernel can therefore only take an element function
// it can inline, which means a single primitive operation over the operands
// — not a body it would have to call.
//
// LIKE RECOGNITION, THIS CHANGES NOTHING. No kernel exists for any verb yet.
// A verdict of NdarrayElementPrimitive says the element function is not what
// stands in the way, not that the site lowers to a kernel.

// NdarrayElementRefusal is why a kernel could not inline an element
// function, or NdarrayElementPrimitive when it could. The set is CLOSED and
// each member has a stable tag, for the reason FusionRefusal's is: the
// refusals are the coverage checklist for widening the kernels, and a
// checklist made of free text cannot be tallied.
type NdarrayElementRefusal int

const (
	NdarrayElementPrimitive NdarrayElementRefusal = iota
	NdarrayElementUnresolved
	NdarrayElementNotInProgram
	NdarrayElementCaptures
	NdarrayElementCalls
	NdarrayElementNotOneOp
	NdarrayElementNotArithmetic
)

// AllNdarrayElementRefusals is every reason an element function could NOT be
// inlined, so a report prints a row per reason even at zero — a reason that
// vanished when it stopped firing is one nobody notices went missing.
// NdarrayElementPrimitive is absent: it is the absence of a refusal, and the
// report states it as a total instead.
var AllNdarrayElementRefusals = []NdarrayElementRefusal{
	NdarrayElementUnresolved,
	NdarrayElementNotInProgram,
	NdarrayElementCaptures,
	NdarrayElementCalls,
	NdarrayElementNotOneOp,
	NdarrayElementNotArithmetic,
}

// Tag is the stable one-word name a histogram counts under.
func (r NdarrayElementRefusal) Tag() string {
	switch r {
	case NdarrayElementPrimitive:
		return "primitive"
	case NdarrayElementUnresolved:
		return "element-fn-unresolved"
	case NdarrayElementNotInProgram:
		return "element-fn-not-in-program"
	case NdarrayElementCaptures:
		return "element-fn-captures"
	case NdarrayElementCalls:
		return "element-fn-calls"
	case NdarrayElementNotOneOp:
		return "element-fn-not-one-op"
	case NdarrayElementNotArithmetic:
		return "element-fn-not-arithmetic"
	}
	return "unknown"
}

// String is the reason clause, without a verdict in front of it.
func (r NdarrayElementRefusal) String() string {
	switch r {
	case NdarrayElementPrimitive:
		return "one primitive operation, which a kernel can inline"
	case NdarrayElementUnresolved:
		return "the element function is not statically resolved"
	case NdarrayElementNotInProgram:
		return "the element function is named but not in the program"
	case NdarrayElementCaptures:
		return "the element function closes over something, and a kernel has nowhere to put it"
	case NdarrayElementCalls:
		return "the body calls something, and a call is an op boundary a kernel's vectors do not cross"
	case NdarrayElementNotOneOp:
		return "the body is more than one operation, so inlining it is not what a kernel does"
	case NdarrayElementNotArithmetic:
		return "the body's one operation is not arithmetic a kernel emits"
	}
	return "no reason recorded"
}

// NdarrayKernelVerdict is what a kernel could do with one element function.
// Op is the primitive it is — `f64 mul`, `i64 add` — and is empty unless Why
// is NdarrayElementPrimitive.
type NdarrayKernelVerdict struct {
	Why NdarrayElementRefusal
	Op  string
}

func (v NdarrayKernelVerdict) String() string {
	if v.Why == NdarrayElementPrimitive {
		return v.Op
	}
	return v.Why.Tag()
}

// ndarrayPrimitiveOps is the arithmetic a kernel emits inline, mapped to how
// the report names it. Division and remainder are absent for the reason
// rotate.go's purity list leaves them out: both trap on a zero divisor, so a
// kernel that hoisted one would move a trap.
var ndarrayPrimitiveOps = map[OpKind]string{
	OpAdd: "add", OpSub: "sub", OpMul: "mul",
	OpAnd: "and", OpOr: "or", OpXor: "xor",
	OpShl: "shl", OpShrS: "shr",
	OpEq: "eq", OpNe: "ne", OpLtS: "lt", OpLeS: "le", OpGtS: "gt", OpGeS: "ge",
	OpFAdd: "add", OpFSub: "sub", OpFMul: "mul", OpFDiv: "div",
	OpFEq: "eq", OpFNe: "ne", OpFLt: "lt", OpFLe: "le", OpFGt: "gt", OpFGe: "ge",
}

// ndarrayFloatOps is which of those read float operands, which is how the
// report names the width: the op kind carries the family and Width the size.
var ndarrayFloatOps = map[OpKind]bool{
	OpFAdd: true, OpFSub: true, OpFMul: true, OpFDiv: true,
	OpFEq: true, OpFNe: true, OpFLt: true, OpFLe: true, OpFGt: true, OpFGe: true,
}

// NdarrayKernelVerdicts classifies each element function a recognized site
// was handed, in argument order.
func NdarrayKernelVerdicts(p *Program, s NdarrayShape) []NdarrayKernelVerdict {
	out := make([]NdarrayKernelVerdict, len(s.Elements))
	for i, e := range s.Elements {
		out[i] = ndarrayClassifyElement(p, e)
	}
	return out
}

func ndarrayClassifyElement(p *Program, e NdarrayElementFn) NdarrayKernelVerdict {
	if e.Name == "" {
		return NdarrayKernelVerdict{Why: NdarrayElementUnresolved}
	}
	if e.Captures > 0 {
		return NdarrayKernelVerdict{Why: NdarrayElementCaptures}
	}
	fn := ndarrayFuncNamed(p, e.Name)
	if fn == nil {
		return NdarrayKernelVerdict{Why: NdarrayElementNotInProgram}
	}
	return ndarrayClassifyBody(fn)
}

func ndarrayFuncNamed(p *Program, name string) *Func {
	for _, fn := range p.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

// ndarrayClassifyBody reads a body that a kernel could inline: operands
// pushed, one operation applied, the result returned. `x + y` and `x * 2`
// are both that shape; anything with a branch, a call or a second operation
// is not.
func ndarrayClassifyBody(fn *Func) NdarrayKernelVerdict {
	var applied *Op
	seenReturn := false
	for i := range fn.Ops {
		op := &fn.Ops[i]
		switch op.Kind {
		case OpCallDirect, OpCallDirectPair, OpCallIndirect:
			// ATLAS §3: a kernel's vector lifetime ends at an op
			// boundary, and a call is one. Named before the
			// one-operation rule because it is the more useful
			// answer — "this calls out" rather than "this is long".
			return NdarrayKernelVerdict{Why: NdarrayElementCalls}
		case OpLine:
			continue
		case OpConstI32, OpConstI64, OpConstF32, OpConstF64, OpLoadLocal:
			if applied != nil {
				// A push after the operation is a second value the
				// body does something with, so there is more here
				// than one operation over the operands.
				return NdarrayKernelVerdict{Why: NdarrayElementNotOneOp}
			}
			continue
		case OpReturn:
			seenReturn = true
			continue
		}
		if applied != nil || seenReturn {
			return NdarrayKernelVerdict{Why: NdarrayElementNotOneOp}
		}
		applied = op
	}
	if applied == nil {
		// Nothing is applied at all: the body returns an operand or a
		// constant. A kernel has no operation to emit for it.
		return NdarrayKernelVerdict{Why: NdarrayElementNotOneOp}
	}
	name, ok := ndarrayPrimitiveOps[applied.Kind]
	if !ok {
		return NdarrayKernelVerdict{Why: NdarrayElementNotArithmetic}
	}
	return NdarrayKernelVerdict{Why: NdarrayElementPrimitive, Op: ndarrayOperandType(*applied) + " " + name}
}

// ndarrayOperandType names the width the operation reads. Width zero means
// 32 the way it does everywhere else in the IR.
func ndarrayOperandType(op Op) string {
	var b strings.Builder
	if ndarrayFloatOps[op.Kind] {
		b.WriteString("f")
	} else {
		b.WriteString("i")
	}
	if op.Width == 64 {
		b.WriteString("64")
	} else {
		b.WriteString("32")
	}
	return b.String()
}

// FormatNdarrayElementHistogram tallies every element function of every
// recognized std/ndarray site by what a kernel could do with it, or "" when
// the program has no such site. A row per reason even at zero, for the
// reason the fusion histogram prints one.
func FormatNdarrayElementHistogram(p *Program) string {
	shapes := RecognizeNdarrayShapes(p)
	if len(shapes) == 0 {
		return ""
	}
	counts := map[NdarrayElementRefusal]int{}
	inlinable, total := 0, 0
	for _, s := range shapes {
		for _, v := range NdarrayKernelVerdicts(p, s) {
			counts[v.Why]++
			total++
			if v.Why == NdarrayElementPrimitive {
				inlinable++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "std/ndarray element functions (#9735): %d, %d a kernel could inline\n", total, inlinable)
	for _, r := range AllNdarrayElementRefusals {
		fmt.Fprintf(&b, "  %-26s %d\n", r.Tag(), counts[r])
	}
	return b.String()
}
