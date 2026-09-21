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
			axis = ndarrayAxisArg(fn, windowStart, i)
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
// verb: the axis `reduce_axis` and `scan_axis` walk, the cell rank `map_rank`
// maps over.
//
// It simulates the operand stack over the window since the previous call,
// which is what it takes to name the argument rather than guess at it. The
// first integer literal in the window is NOT the axis in three shapes that
// all occur: a field receiver pushes its offset first (`bx.a.reduce_axis`), a
// written negative pushes the two halves a `sub` combines, and — the one that
// matters most — a COMPUTED axis leaves the init literal as the first one
// (`a.reduce_axis(k, 0, add)` over an i32 array reads the init's 0 as axis 0,
// which is the wrong-axis case this must never produce).
//
// The simulation reuses flatten.go's opStackEffect, so an op it does not
// model makes this bail rather than miscount, and the argument count on the
// call op then has to match the simulated depth. What is left is the call's
// operand list, in order: the receiver, then the arguments. The axis is the
// second, and it is a literal only when a bare OpConstI32 produced it.
func ndarrayAxisArg(fn *Func, start, call int) int32 {
	// Each entry is the index of the op that pushed that operand; a value
	// already on the stack when the window opened is not one of ours and
	// is recorded as -1.
	stack := []int{}
	for k := start; k < call; k++ {
		pops, pushes, ok := ndarrayOpEffect(fn.Ops[k])
		if !ok {
			return NdarrayAxisUnknown
		}
		for ; pops > 0; pops-- {
			if len(stack) == 0 {
				break // consumed a value from before the window
			}
			stack = stack[:len(stack)-1]
		}
		for ; pushes > 0; pushes-- {
			stack = append(stack, k)
		}
	}
	argc := int(fn.Ops[call].I32)
	if argc < 2 || len(stack) < argc {
		return NdarrayAxisUnknown
	}
	axis := fn.Ops[stack[len(stack)-argc+1]]
	if axis.Kind != OpConstI32 {
		return NdarrayAxisUnknown
	}
	return axis.I32
}

// ndarrayOpEffect is opStackEffect with the two refcount ops filled in. Both
// are pass-through — flatten.go says so on the arm that declines them, and
// declines them only to keep its own decisions byte-identical.
func ndarrayOpEffect(op Op) (pops, pushes int, ok bool) {
	switch op.Kind {
	case OpRcInc, OpRcDec:
		return 1, 1, true
	}
	return opStackEffect(op, nil)
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
