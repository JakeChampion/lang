package interp

import (
	"fmt"
	"math"
	"strings"
)

// Lanes is a value of a lane type: `f64x2` (Width 64) or `f32x4` (Width 32).
// Each lane is held as its bits, because a comparison gives a mask of all-ones
// lanes and `select` combines masks bit by bit. An f32 lane holds its bits in
// the low 32 of the word.
type Lanes struct {
	Width int
	Bits  []uint64
}

func (l Lanes) String() string {
	var b strings.Builder
	b.WriteByte('[')
	for i := range l.Bits {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(l.lane(i).String())
	}
	b.WriteByte(']')
	return b.String()
}

func (l Lanes) count() int { return 128 / l.Width }

// lane reads lane i as a float of the lane width.
func (l Lanes) lane(i int) Float {
	if l.Width == 32 {
		return Float{V: float64(math.Float32frombits(uint32(l.Bits[i]))), Width: 32}
	}
	return Float{V: math.Float64frombits(l.Bits[i]), Width: 64}
}

// laneBits is the bits of v at the lane width, rounding an f32 lane.
func laneBits(width int, v float64) uint64 {
	if width == 32 {
		return uint64(math.Float32bits(float32(v)))
	}
	return math.Float64bits(v)
}

// mapLanes applies fn to each lane's value and gives the lanes of the result.
// An f32 lane computes in single precision, as the vector unit does.
func mapLanes(ls []Lanes, fn func(xs []float64) float64) Lanes {
	w := ls[0].Width
	out := Lanes{Width: w, Bits: make([]uint64, ls[0].count())}
	xs := make([]float64, len(ls))
	for i := range out.Bits {
		for k, l := range ls {
			xs[k] = l.lane(i).V
		}
		out.Bits[i] = laneBits(w, fn(xs))
	}
	return out
}

// mask is the all-ones lane where `hold` and zero otherwise.
func mask(width int, hold bool) uint64 {
	if !hold {
		return 0
	}
	if width == 32 {
		return 0xffffffff
	}
	return ^uint64(0)
}

// round is v at the lane width: an f32 lane rounds to single precision.
func round(width int, v float64) float64 {
	if width == 32 {
		return float64(float32(v))
	}
	return v
}

func signBit(width int) uint64 {
	if width == 32 {
		return 1 << 31
	}
	return 1 << 63
}

// lanesBinary evaluates `+ - * /` over two values of one lane type.
func lanesBinary(op string, a, b Lanes) (Value, error) {
	w := a.Width
	var fn func(x, y float64) float64
	switch op {
	case "+":
		fn = func(x, y float64) float64 { return x + y }
	case "-":
		fn = func(x, y float64) float64 { return x - y }
	case "*":
		fn = func(x, y float64) float64 { return x * y }
	case "/":
		fn = func(x, y float64) float64 { return x / y }
	default:
		return nil, fmt.Errorf("interp: operator %q on lane values", op)
	}
	if w == 32 {
		return mapLanes([]Lanes{a, b}, func(xs []float64) float64 {
			return float64(float32(fn(float64(float32(xs[0])), float64(float32(xs[1])))))
		}), nil
	}
	return mapLanes([]Lanes{a, b}, func(xs []float64) float64 { return fn(xs[0], xs[1]) }), nil
}

// lanesNeg flips each lane's sign bit, as the vector negation does, NaN
// included.
func lanesNeg(a Lanes) Lanes {
	out := Lanes{Width: a.Width, Bits: make([]uint64, len(a.Bits))}
	for i, b := range a.Bits {
		out.Bits[i] = b ^ signBit(a.Width)
	}
	return out
}

func laneArgs(name string, args []Value, n int) ([]Lanes, error) {
	if len(args) != n {
		return nil, fmt.Errorf("%s: expected %d args, got %d", name, n, len(args))
	}
	out := make([]Lanes, n)
	for i, a := range args {
		l, ok := a.(Lanes)
		if !ok {
			return nil, fmt.Errorf("%s: expected lane args, got %T", name, a)
		}
		out[i] = l
	}
	return out, nil
}

// laneWindow is the index range a `load` or `with` at i covers, or an error
// when the array does not hold all of it.
func laneWindow(name string, arr Value, idx Value, lanes int) (Array, int, error) {
	a, ok := arr.(Array)
	if !ok {
		return Array{}, 0, fmt.Errorf("%s: expected an array, got %T", name, arr)
	}
	n, ok := idx.(Number)
	if !ok {
		return Array{}, 0, fmt.Errorf("%s: expected an index, got %T", name, idx)
	}
	i := int(n)
	if i < 0 || i+lanes > len(a.E) {
		return Array{}, 0, fmt.Errorf("%s: lanes %d to %d are out of bounds for length %d", name, i, i+lanes-1, len(a.E))
	}
	return a, i, nil
}

// registerLaneBuiltins installs the lane builtins of f64x2 and f32x4
// (docs/SIMD-GENERIC-KERNELS.md §6).
func (i *Interp) registerLaneBuiltins() {
	for _, w := range []int{64, 32} {
		p := fmt.Sprintf("__f%dx%d_", w, 128/w)
		lanes := 128 / w
		width := w
		i.Builtins[p+"splat"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			f, ok := args[0].(Float)
			if len(args) != 1 || !ok {
				return nil, fmt.Errorf("%ssplat: expected one float", p)
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k := range out.Bits {
				out.Bits[k] = laneBits(width, f.V)
			}
			return out, nil
		}}
		i.Builtins[p+"of"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if len(args) != lanes {
				return nil, fmt.Errorf("%sof: expected %d floats, got %d", p, lanes, len(args))
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k, a := range args {
				f, ok := a.(Float)
				if !ok {
					return nil, fmt.Errorf("%sof: expected floats, got %T", p, a)
				}
				out.Bits[k] = laneBits(width, f.V)
			}
			return out, nil
		}}
		i.Builtins[p+"lane"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			l, ok := args[0].(Lanes)
			n, nok := args[1].(Number)
			if len(args) != 2 || !ok || !nok || int(n) < 0 || int(n) >= lanes {
				return nil, fmt.Errorf("%slane: expected a lane value and an index below %d", p, lanes)
			}
			return l.lane(int(n)), nil
		}}
		i.Builtins[p+"bitmask"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			ls, err := laneArgs(p+"bitmask", args, 1)
			if err != nil {
				return nil, err
			}
			var mask Number
			for k, bits := range ls[0].Bits {
				mask |= Number((bits>>(width-1))&1) << k
			}
			return mask, nil
		}}
		unary := map[string]func(x float64) float64{
			"sqrt":  math.Sqrt,
			"floor": math.Floor,
			"round": math.Round,
		}
		for verb, fn := range unary {
			name, fn := p+verb, fn
			i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
				ls, err := laneArgs(name, args, 1)
				if err != nil {
					return nil, err
				}
				return mapLanes(ls, func(xs []float64) float64 { return fn(xs[0]) }), nil
			}}
		}
		i.Builtins[p+"abs"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			ls, err := laneArgs(p+"abs", args, 1)
			if err != nil {
				return nil, err
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k, b := range ls[0].Bits {
				out.Bits[k] = b &^ signBit(width)
			}
			return out, nil
		}}
		i.Builtins[p+"mul_add"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			ls, err := laneArgs(p+"mul_add", args, 3)
			if err != nil {
				return nil, err
			}
			// Unfused, as the scalar mul_add: the product rounds before the add.
			return mapLanes(ls, func(xs []float64) float64 {
				return round(width, round(width, xs[0]*xs[1])+xs[2])
			}), nil
		}}
		// min and max are `b < a ? b : a` and `a < b ? b : a`, the order the
		// IR's vmin and vmax define.
		i.Builtins[p+"min"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			ls, err := laneArgs(p+"min", args, 2)
			if err != nil {
				return nil, err
			}
			return pickLanes(ls[0], ls[1], func(a, b float64) bool { return b < a }), nil
		}}
		i.Builtins[p+"max"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			ls, err := laneArgs(p+"max", args, 2)
			if err != nil {
				return nil, err
			}
			return pickLanes(ls[0], ls[1], func(a, b float64) bool { return a < b }), nil
		}}
		cmp := map[string]func(a, b float64) bool{
			"lt": func(a, b float64) bool { return a < b },
			"ne": func(a, b float64) bool { return a != b },
		}
		for verb, fn := range cmp {
			name, fn := p+verb, fn
			i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
				ls, err := laneArgs(name, args, 2)
				if err != nil {
					return nil, err
				}
				out := Lanes{Width: width, Bits: make([]uint64, lanes)}
				for k := range out.Bits {
					out.Bits[k] = mask(width, fn(ls[0].lane(k).V, ls[1].lane(k).V))
				}
				return out, nil
			}}
		}
		i.Builtins[p+"select"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			ls, err := laneArgs(p+"select", args, 3)
			if err != nil {
				return nil, err
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k := range out.Bits {
				m := ls[2].Bits[k]
				out.Bits[k] = ls[0].Bits[k]&m | ls[1].Bits[k]&^m
			}
			return out, nil
		}}
		i.Builtins[p+"load"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("%sload: expected 2 args, got %d", p, len(args))
			}
			a, at, err := laneWindow(p+"load", args[0], args[1], lanes)
			if err != nil {
				return nil, err
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k := range out.Bits {
				f, ok := a.E[at+k].(Float)
				if !ok {
					return nil, fmt.Errorf("%sload: expected float elements, got %T", p, a.E[at+k])
				}
				out.Bits[k] = laneBits(width, f.V)
			}
			return out, nil
		}}
		i.Builtins[p+"with"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if len(args) != 3 {
				return nil, fmt.Errorf("%swith: expected 3 args, got %d", p, len(args))
			}
			a, at, err := laneWindow(p+"with", args[0], args[1], lanes)
			if err != nil {
				return nil, err
			}
			l, ok := args[2].(Lanes)
			if !ok {
				return nil, fmt.Errorf("%swith: expected a lane value, got %T", p, args[2])
			}
			e := make([]Value, len(a.E))
			copy(e, a.E)
			for k := range l.Bits {
				e[at+k] = l.lane(k)
			}
			return arrayOf(e...), nil
		}}
	}
}

// pickLanes takes b's lane where `takeB` holds for the lane pair and a's
// otherwise.
func pickLanes(a, b Lanes, takeB func(a, b float64) bool) Lanes {
	out := Lanes{Width: a.Width, Bits: make([]uint64, len(a.Bits))}
	for k := range out.Bits {
		if takeB(a.lane(k).V, b.lane(k).V) {
			out.Bits[k] = b.Bits[k]
		} else {
			out.Bits[k] = a.Bits[k]
		}
	}
	return out
}
