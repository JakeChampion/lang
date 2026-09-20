package ir

import (
	"fmt"
	"sort"
	"strings"
)

// Recognition of std/ndarray's products, `inner` and `outer`, as whole
// shapes (#9735, step 3). Like the pipeline recogniser this CHANGES NOTHING:
// every recognized site still runs the scalar loop in std/ndarray. What it
// gives the kernel half is the list of sites a kernel would replace and the
// element functions each one was handed, which is what decides whether a
// site is `dot_f64` in disguise or a fold over something a kernel cannot do.
//
// Recognition is by resolved identity, the boundary docs/ARRAY-ALGEBRA.md §6
// sets and docs/ARRAY-SHAPES.md §6 carries to the handle: a receiver method
// of std/ndarray's struct mangles to `__method_ndarray__NdArray_<verb>` with
// the module's reserved prefix inside the struct name, so a user's own
// `NdArray` with its own `inner` mangles differently and is not the
// algebra's.

// NdarrayShape is one recognized call to a std/ndarray product.
type NdarrayShape struct {
	Func string
	Line int
	Col  int
	// Verb is the product as written: inner or outer.
	Verb string
	// Callee is the mangled, monomorphised name the call targets.
	Callee string
	// Elements names the element functions the call was handed, in
	// argument order — `outer` takes one, `inner` takes `mul` then `add`.
	// An entry is empty when the lowering did not park that function in a
	// slot the recogniser could follow.
	Elements []string
	// Op is the index of the call in the function's op stream.
	Op int
}

// ndarrayShapeVerbs maps each product to how many element functions it takes.
var ndarrayShapeVerbs = map[string]int{
	"inner": 2,
	"outer": 1,
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

// RecognizeNdarrayShapes finds every call to a std/ndarray product in the
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
		want := ndarrayShapeVerbs[verb]
		elements := make([]string, want)
		// The element functions are the LAST `want` function values loaded
		// before the call; an earlier one belonged to some other operand.
		if len(loaded) > want {
			loaded = loaded[len(loaded)-want:]
		}
		copy(elements[want-len(loaded):], loaded)
		out = append(out, NdarrayShape{
			Func: fn.Name, Line: op.Pos.Line, Col: op.Pos.Col,
			Verb: verb, Callee: op.Str, Elements: elements, Op: i,
		})
		windowStart = i + 1
	}
	return out
}

// FormatNdarrayShapes renders the recognized products, or "" when there are
// none, so the array report can leave the section out rather than print an
// empty heading.
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
	var b strings.Builder
	b.WriteString("std/ndarray products, recognized by identity and lowered as the scalar loop:\n")
	for i, s := range shapes {
		elems := make([]string, len(s.Elements))
		for k, e := range s.Elements {
			if e == "" {
				e = "(element function not statically resolved)"
			}
			elems[k] = e
		}
		fmt.Fprintf(&b, "%-*s  %-6s %s\n", posW, posOf[i], s.Verb, strings.Join(elems, ", "))
	}
	return b.String()
}
