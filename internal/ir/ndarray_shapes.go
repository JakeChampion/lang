package ir

import (
	"fmt"
	"sort"
	"strings"
)

// Recognition of std/ndarray's algebra — the eight operations that are
// handed a function, docs/ARRAY-SHAPES.md §7's closed list — as whole
// shapes (#9735). Like the pipeline recogniser this CHANGES NOTHING:
// every recognized site still runs the scalar loop in std/ndarray. What it
// gives the kernel half is the list of sites a kernel would replace, the
// element functions each one was handed, and the axis an axis-parameterized
// site walks, which is what decides whether a site is `dot_f64` in disguise
// or a fold over something a kernel cannot do.
//
// §2's structural operations are not here. They move no elements, so there
// is nothing for a kernel to replace.
//
// Recognition is by resolved identity, the boundary docs/ARRAY-ALGEBRA.md §6
// sets and docs/ARRAY-SHAPES.md §6 carries to the handle: a receiver method
// of std/ndarray's struct mangles to `__method_ndarray__NdArray_<verb>` with
// the module's reserved prefix inside the struct name, so a user's own
// `NdArray` with its own `inner` mangles differently and is not the
// algebra's.

// NdarrayAxisUnknown is the Axis of a site whose axis argument is not a
// literal the recogniser could read, and of every verb that takes none. No
// axis or cell rank is ever negative — a negative one aborts the site — so
// -1 cannot collide with an axis a program asked for.
const NdarrayAxisUnknown int32 = -1

// NdarrayShape is one recognized call to a std/ndarray operation.
type NdarrayShape struct {
	Func string
	Line int
	Col  int
	// Verb is the operation as written: map, zip_with, fold_all,
	// reduce_axis, scan_axis, outer, inner or map_rank.
	Verb string
	// Callee is the mangled, monomorphised name the call targets.
	Callee string
	// Elements names the element functions the call was handed, in
	// argument order — most take one, `inner` takes `mul` then `add`.
	// An entry is empty when the lowering did not park that function in a
	// slot the recogniser could follow.
	Elements []string
	// Axis is the leading integer argument of the axis-parameterized
	// verbs: the axis `reduce_axis` and `scan_axis` walk, the cell rank
	// `map_rank` maps over. NdarrayAxisUnknown when the argument is
	// computed rather than written, and for the verbs that take none.
	Axis int32
	// Op is the index of the call in the function's op stream.
	Op int
}

// ndarrayVerb is the shape of one operation: how many element functions it
// is handed, and what its leading integer argument is called when it has
// one.
type ndarrayVerb struct {
	elements int
	arg      string
}

// ndarrayShapeVerbs is docs/ARRAY-SHAPES.md §7's closed list.
var ndarrayShapeVerbs = map[string]ndarrayVerb{
	"map":         {elements: 1},
	"zip_with":    {elements: 1},
	"fold_all":    {elements: 1},
	"reduce_axis": {elements: 1, arg: "axis"},
	"scan_axis":   {elements: 1, arg: "axis"},
	"outer":       {elements: 1},
	"inner":       {elements: 2},
	"map_rank":    {elements: 1, arg: "rank"},
}

const ndarrayMethodPrefix = "__method_ndarray__NdArray_"

// ndarrayVerbOf returns the product a callee's name is, or "" when it is not
// one of std/ndarray's.
func ndarrayVerbOf(callee string) (string, bool) {
	if !strings.HasPrefix(callee, ndarrayMethodPrefix) {
		return "", false
	}
	base := callee[len(ndarrayMethodPrefix):]
	if i := strings.Index(base, "__"); i >= 0 {
		base = base[:i]
	}
	if _, ok := ndarrayShapeVerbs[base]; !ok {
		return "", false
	}
	return base, true
}

// isNdarrayBody reports whether fn is std/ndarray's own code, which the
// recogniser skips the way the pipeline recogniser skips std/array's.
func isNdarrayBody(fn *Func) bool {
	return strings.HasPrefix(fn.Name, "ndarray__") || strings.HasPrefix(fn.Name, ndarrayMethodPrefix)
}

// RecognizeNdarrayShapes finds every call to a std/ndarray operation in the
// program, in a deterministic order.
func RecognizeNdarrayShapes(p *Program) []NdarrayShape {
	var out []NdarrayShape
	for _, fn := range p.Funcs {
		if isNdarrayBody(fn) {
			continue
		}
		out = append(out, ndarrayShapesInFunc(fn)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Func != out[j].Func {
			return out[i].Func < out[j].Func
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out
}

// ndarrayShapesInFunc walks the op stream once. A function argument reaches
// the call one of two ways: a lambda is parked in a slot by its make_closure
// and loaded back just before the call, and a named function is pushed
// straight from its const_func. Both are read in the window since the
// previous call, in stack order, which is argument order.
func ndarrayShapesInFunc(fn *Func) []NdarrayShape {
	var out []NdarrayShape
	closureOf := map[int32]string{}
	windowStart := 0
	for i, op := range fn.Ops {
		if op.Kind == OpMakeClosure || op.Kind == OpConstFunc {
			if i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == OpStoreLocal {
				closureOf[fn.Ops[i+1].I32] = op.Str
			}
			continue
		}
		if op.Kind != OpCallDirect {
			continue
		}
		verb, ok := ndarrayVerbOf(op.Str)
		if !ok || op.Runtime {
			windowStart = i + 1
			continue
		}
		var loaded []string
		for k := windowStart; k < i; k++ {
			switch fn.Ops[k].Kind {
			case OpMakeClosure, OpConstFunc:
				if k+1 < i && fn.Ops[k+1].Kind == OpStoreLocal {
					continue
				}
				loaded = append(loaded, fn.Ops[k].Str)
			case OpLoadLocal:
				if name, isClosure := closureOf[fn.Ops[k].I32]; isClosure {
					loaded = append(loaded, name)
				}
			}
		}
		spec := ndarrayShapeVerbs[verb]
		want := spec.elements
		elements := make([]string, want)
		// The element functions are the LAST `want` function values loaded
		// before the call; an earlier one belonged to some other operand.
		if len(loaded) > want {
			loaded = loaded[len(loaded)-want:]
		}
		copy(elements[want-len(loaded):], loaded)
		axis := NdarrayAxisUnknown
		if spec.arg != "" {
			axis = ndarrayAxisArg(fn.Ops, windowStart, i)
		}
		out = append(out, NdarrayShape{
			Func: fn.Name, Line: op.Pos.Line, Col: op.Pos.Col,
			Verb: verb, Callee: op.Str, Elements: elements, Axis: axis, Op: i,
		})
		windowStart = i + 1
	}
	return out
}

// ndarrayAxisArg reads the leading integer argument of an axis-parameterized
// verb out of the window since the previous call.
//
// There is no operand-stack model here, so what it looks for is the run of
// operand pushes that reaches the call — the argument list. Anything that
// CONSUMES a value ends an expression and therefore ends the run, which is
// what keeps a literal belonging to some other operand out of it: the offset
// of a field receiver is followed by the `add` that applies it, and the `0`
// and `1` of a written `-1` are followed by the `sub` that combines them.
// The axis is the first integer literal in the run that is not immediately
// parked in a slot.
//
// A literal the run does not reach is NdarrayAxisUnknown, and so is an axis
// computed at run time. That is the safe direction: an axis the report does
// not know is a site the kernel half looks at, and a wrong one is a site it
// silently mis-plans.
func ndarrayAxisArg(ops []Op, start, call int) int32 {
	from := start
	for k := start; k < call; k++ {
		if !ndarrayPassesOperands(ops[k].Kind) {
			from = k + 1
		}
	}
	for k := from; k < call; k++ {
		if ops[k].Kind != OpConstI32 {
			continue
		}
		if k+1 < call && ops[k+1].Kind == OpStoreLocal {
			continue // a slot's initialiser, not this call's argument
		}
		return ops[k].I32
	}
	return NdarrayAxisUnknown
}

// ndarrayPassesOperands reports whether an op leaves the operands already
// pushed for this call alone: the pushes themselves, the store/load pair a
// closure is parked through, line markers, and the refcount ops and field
// loads that pass a value straight through.
func ndarrayPassesOperands(k OpKind) bool {
	switch k {
	case OpConstI32, OpConstI64, OpConstF32, OpConstF64, OpConstStr,
		OpConstFunc, OpMakeClosure, OpLoadLocal, OpStoreLocal,
		OpLoad, OpLine, OpRcInc, OpRcDec:
		return true
	}
	return false
}

// FormatNdarrayShapes renders the recognized operations, or "" when there
// are none, so the array report can leave the section out rather than print
// an empty heading.
func FormatNdarrayShapes(p *Program) string {
	shapes := RecognizeNdarrayShapes(p)
	if len(shapes) == 0 {
		return ""
	}
	posOf := make([]string, len(shapes))
	posW := 0
	for i, s := range shapes {
		posOf[i] = fmt.Sprintf("%s:%d:%d", s.Func, s.Line, s.Col)
		if len(posOf[i]) > posW {
			posW = len(posOf[i])
		}
	}
	verbOf := make([]string, len(shapes))
	verbW := 0
	for i, s := range shapes {
		verbOf[i] = s.Verb
		if w := ndarrayShapeVerbs[s.Verb].arg; w != "" {
			if s.Axis == NdarrayAxisUnknown {
				verbOf[i] += "(" + w + " ?)"
			} else {
				verbOf[i] += fmt.Sprintf("(%s %d)", w, s.Axis)
			}
		}
		if len(verbOf[i]) > verbW {
			verbW = len(verbOf[i])
		}
	}
	var b strings.Builder
	b.WriteString("std/ndarray operations, recognized by identity and lowered as the scalar loop:\n")
	for i, s := range shapes {
		elems := make([]string, len(s.Elements))
		for k, e := range s.Elements {
			if e == "" {
				e = "(element function not statically resolved)"
			}
			elems[k] = e
		}
		fmt.Fprintf(&b, "%-*s  %-*s  %s\n", posW, posOf[i], verbW, verbOf[i], strings.Join(elems, ", "))
	}
	return b.String()
}
