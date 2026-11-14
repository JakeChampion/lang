package interp

import (
	"fmt"
	"math"
	"strings"
)

// Lanes is a lane value. Int is true for integer lanes, whose Bits hold
// each lane as an unsigned number. Signed is true for integer lanes with a
// sign.
type Lanes struct {
	Width  int
	Int    bool
	Signed bool
	Bits   []uint64
}

func (l Lanes) String() string {
	var b strings.Builder
	b.WriteByte('[')
	for i := range l.Bits {
		if i > 0 {
			b.WriteString(", ")
		}
		if l.Int {
			fmt.Fprintf(&b, "%d", l.number(i))
		} else {
			b.WriteString(l.lane(i).String())
		}
	}
	b.WriteByte(']')
	return b.String()
}

func (l Lanes) count() int { return 128 / l.Width }

func (l Lanes) lane(i int) Float {
	if l.Width == 32 {
		return Float{V: float64(math.Float32frombits(uint32(l.Bits[i]))), Width: 32}
	}
	return Float{V: math.Float64frombits(l.Bits[i]), Width: 64}
}

func laneBits(width int, v float64) uint64 {
	if width == 32 {
		return uint64(math.Float32bits(float32(v)))
	}
	return math.Float64bits(v)
}

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

func mask(width int, hold bool) uint64 {
	if !hold {
		return 0
	}
	if width == 32 {
		return 0xffffffff
	}
	return ^uint64(0)
}

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

func lanesBinary(op string, a, b Lanes) (Value, error) {
	w := a.Width
	if a.Int {
		if op != "+" && op != "-" && (op != "*" || w == 8) {
			return nil, fmt.Errorf("interp: operator %q on integer lanes of %d bits", op, w)
		}
		return mapIntLanes(a, b, intLaneBinary[op]), nil
	}
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

func lanesNeg(a Lanes) Lanes {
	if a.Int {
		return mapIntLanes(a, a, func(_ Lanes, x, _ int64) int64 { return -x })
	}
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
	if i < 0 || i+lanes > len(a.Elems()) {
		return Array{}, 0, fmt.Errorf("%s: lanes %d to %d are out of bounds for length %d", name, i, i+lanes-1, len(a.Elems()))
	}
	return a, i, nil
}

func (i *Interp) registerLaneBuiltins() {
	i.registerIntegerLaneBuiltins()
	for _, w := range []int{64, 32} {
		p := fmt.Sprintf("__f%dx%d_", w, 128/w)
		lanes := 128 / w
		width := w
		for _, verb := range []string{"swap_halves", "reverse", "zip_lo", "zip_hi"} {
			i.Builtins[p+verb] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
				argc := 2
				if verb == "reverse" || verb == "swap_halves" {
					argc = 1
				}
				ls, err := laneArgs(p+verb, args, argc)
				if err != nil {
					return nil, err
				}
				out := Lanes{Width: width, Bits: make([]uint64, lanes)}
				for k := range out.Bits {
					src, at := k&1, k/2
					if verb == "reverse" {
						src, at = 0, lanes-1-k
					}
					if verb == "swap_halves" {
						src, at = 0, (k+lanes/2)%lanes
					}
					if verb == "zip_hi" {
						at += lanes / 2
					}
					out.Bits[k] = ls[src].Bits[at]
				}
				return out, nil
			}}
		}
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
		i.Builtins[p+"lane_bits"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("%slane_bits: expected two arguments", p)
			}
			l, ok := args[0].(Lanes)
			n, nok := args[1].(Number)
			if !ok || !nok || int(n) < 0 || int(n) >= lanes {
				return nil, fmt.Errorf("%slane_bits: invalid lane index", p)
			}
			return Number(int64(l.Bits[int(n)])), nil
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
			return mapLanes(ls, func(xs []float64) float64 {
				return round(width, round(width, xs[0]*xs[1])+xs[2])
			}), nil
		}}
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
				f, ok := a.Elems()[at+k].(Float)
				if !ok {
					return nil, fmt.Errorf("%sload: expected float elements, got %T", p, a.Elems()[at+k])
				}
				out.Bits[k] = laneBits(width, f.V)
			}
			return out, nil
		}}
		i.Builtins[p+"load_ptr"] = &Builtin{Fn: func(ip *Interp, args []Value) (Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("vector load requires an address")
			}
			address, ok := args[0].(Number)
			if !ok {
				return nil, fmt.Errorf("vector load requires an address")
			}
			bytes, err := ip.raw.span(p+"load_ptr", int64(address), 16)
			if err != nil {
				return nil, err
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k := 0; k < 16; k++ {
				out.Bits[k/(width/8)] |= uint64(bytes[k]) << ((k % (width / 8)) * 8)
			}
			return out, nil
		}}
		i.Builtins[p+"store"] = &Builtin{Fn: func(ip *Interp, args []Value) (Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("vector store requires an address and lanes")
			}
			address, ok := args[0].(Number)
			l, valid := args[1].(Lanes)
			if !ok || !valid || l.Width != width {
				return nil, fmt.Errorf("vector store requires matching lanes")
			}
			bytes, err := ip.raw.span(p+"store", int64(address), 16)
			if err != nil {
				return nil, err
			}
			for k := 0; k < 16; k++ {
				bytes[k] = byte(l.Bits[k/(width/8)] >> ((k % (width / 8)) * 8))
			}
			return Void{}, nil
		}}

		i.Builtins[p+"load_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			a, at, err := laneWindow(p+"load_bytes", args[0], args[1], 16)
			if err != nil {
				return nil, err
			}
			out := Lanes{Width: width, Bits: make([]uint64, lanes)}
			for k := 0; k < 16; k++ {
				n, ok := a.Elems()[at+k].(Number)
				if !ok {
					return nil, fmt.Errorf("packed lane load requires bytes")
				}
				out.Bits[k/(width/8)] |= uint64(uint8(n)) << ((k % (width / 8)) * 8)
			}
			return out, nil
		}}
		i.Builtins[p+"with_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			a, at, err := laneWindow(p+"with_bytes", args[0], args[1], 16)
			if err != nil {
				return nil, err
			}
			l, ok := args[2].(Lanes)
			if !ok || l.Width != width {
				return nil, fmt.Errorf("packed lane store requires matching lanes")
			}
			e := append([]Value(nil), a.Elems()...)
			for k := 0; k < 16; k++ {
				e[at+k] = Number(uint8(l.Bits[k/(width/8)] >> ((k % (width / 8)) * 8)))
			}
			return arrayOf(e...), nil
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
			e := make([]Value, len(a.Elems()))
			copy(e, a.Elems())
			for k := range l.Bits {
				e[at+k] = l.lane(k)
			}
			return arrayOf(e...), nil
		}}
	}
	i.Builtins["__f64x2_load_f32_ptr"] = &Builtin{Fn: func(ip *Interp, args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("widening load requires an address")
		}
		address, ok := args[0].(Number)
		if !ok {
			return nil, fmt.Errorf("widening load requires an address")
		}
		bytes, err := ip.raw.span("__f64x2_load_f32_ptr", int64(address), 8)
		if err != nil {
			return nil, err
		}
		out := Lanes{Width: 64, Bits: make([]uint64, 2)}
		for k := range out.Bits {
			bits := uint32(bytes[4*k]) | uint32(bytes[4*k+1])<<8 | uint32(bytes[4*k+2])<<16 | uint32(bytes[4*k+3])<<24
			out.Bits[k] = laneBits(64, float64(math.Float32frombits(bits)))
		}
		return out, nil
	}}
	i.Builtins["__f32x4_pack"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		ls, err := laneArgs("pack", args, 2)
		if err != nil {
			return nil, err
		}
		out := Lanes{Width: 32, Bits: make([]uint64, 4)}
		for k := 0; k < 4; k++ {
			out.Bits[k] = laneBits(32, ls[k/2].lane(k%2).V)
		}
		return out, nil
	}}
}

// number is the number in lane k of an integer lane value. A signed lane
// has its sign.
func (l Lanes) number(k int) int64 {
	if l.Signed && l.Bits[k]>>(l.Width-1) != 0 {
		return int64(l.Bits[k]) - 1<<l.Width
	}
	return int64(l.Bits[k])
}

func (l Lanes) ones() uint64 { return 1<<l.Width - 1 }

// like is an integer lane value of the type of l, with each lane zero.
func (l Lanes) like() Lanes {
	return Lanes{Width: l.Width, Int: true, Signed: l.Signed, Bits: make([]uint64, 128/l.Width)}
}

// saturated is x held to the range of a lane of l.
func (l Lanes) saturated(x int64) int64 {
	high, low := int64(l.ones()), int64(0)
	if l.Signed {
		high >>= 1
		low = -high - 1
	}
	return min(max(x, low), high)
}

// mapIntLanes is fn on each pair of lanes of two integer lane values, with
// the result cut to the width of a lane.
func mapIntLanes(a, b Lanes, fn func(l Lanes, x, y int64) int64) Lanes {
	out := a.like()
	for k := range out.Bits {
		out.Bits[k] = uint64(fn(a, a.number(k), b.number(k))) & a.ones()
	}
	return out
}

func laneMask(hold bool) int64 {
	if hold {
		return -1
	}
	return 0
}

// intLaneBinary has the operators and the builtins of the integer lane
// types that take two lane values.
var intLaneBinary = map[string]func(l Lanes, x, y int64) int64{
	"+":       func(_ Lanes, x, y int64) int64 { return x + y },
	"-":       func(_ Lanes, x, y int64) int64 { return x - y },
	"*":       func(_ Lanes, x, y int64) int64 { return x * y },
	"min":     func(_ Lanes, x, y int64) int64 { return min(x, y) },
	"max":     func(_ Lanes, x, y int64) int64 { return max(x, y) },
	"eq":      func(_ Lanes, x, y int64) int64 { return laneMask(x == y) },
	"ne":      func(_ Lanes, x, y int64) int64 { return laneMask(x != y) },
	"lt":      func(_ Lanes, x, y int64) int64 { return laneMask(x < y) },
	"add_sat": func(l Lanes, x, y int64) int64 { return l.saturated(x + y) },
	"sub_sat": func(l Lanes, x, y int64) int64 { return l.saturated(x - y) },
	"avg":     func(_ Lanes, x, y int64) int64 { return (x + y + 1) >> 1 },
	"and":     func(_ Lanes, x, y int64) int64 { return x & y },
	"or":      func(_ Lanes, x, y int64) int64 { return x | y },
	"xor":     func(_ Lanes, x, y int64) int64 { return x ^ y },
}

// intLaneVerbs are the verbs of the builtins of the integer lane type of
// `like`.
func intLaneVerbs(like Lanes) []string {
	verbs := []string{"splat", "of", "lane", "bitmask", "min", "max", "eq", "ne", "lt", "and", "or", "xor", "select", "load", "with", "shl", "shr"}
	if like.Width <= 16 {
		verbs = append(verbs, "add_sat", "sub_sat")
		if !like.Signed {
			verbs = append(verbs, "avg")
		}
	}
	if like.Signed {
		verbs = append(verbs, "abs")
	}
	return verbs
}

func intLaneArgs(name string, like Lanes, args []Value, n int) ([]Lanes, error) {
	ls, err := laneArgs(name, args, n)
	if err != nil {
		return nil, err
	}
	for _, l := range ls {
		if !l.Int || l.Width != like.Width || l.Signed != like.Signed {
			return nil, fmt.Errorf("%s: expected lane values of its type", name)
		}
	}
	return ls, nil
}

func intLanesOf(name string, like Lanes, args []Value) (Value, error) {
	out := like.like()
	for k := range out.Bits {
		// splat has one argument for each lane.
		n, ok := args[k%len(args)].(Number)
		if !ok {
			return nil, fmt.Errorf("%s: expected integers, got %T", name, args[k%len(args)])
		}
		out.Bits[k] = uint64(n) & out.ones()
	}
	return out, nil
}

func (i *Interp) registerIntegerLaneBuiltins() {
	for _, width := range []int{8, 16, 32} {
		for _, signed := range []bool{false, true} {
			i.registerIntegerLanes(Lanes{Width: width, Int: true, Signed: signed})
		}
	}
}

func (i *Interp) registerIntegerLanes(like Lanes) {
	sign := "u"
	if like.Signed {
		sign = "i"
	}
	p := fmt.Sprintf("__%s%dx%d_", sign, like.Width, 128/like.Width)
	count, size := 128/like.Width, like.Width/8
	builtins := map[string]func(args []Value) (Value, error){}
	builtins["splat"] = func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%ssplat: expected one integer", p)
		}
		return intLanesOf(p+"splat", like, args)
	}
	builtins["of"] = func(args []Value) (Value, error) {
		if len(args) != count {
			return nil, fmt.Errorf("%sof: expected %d integers, got %d", p, count, len(args))
		}
		return intLanesOf(p+"of", like, args)
	}
	builtins["lane"] = func(args []Value) (Value, error) {
		ls, err := intLaneArgs(p+"lane", like, args[:1], 1)
		n, ok := args[1].(Number)
		if err != nil || !ok || n < 0 || int(n) >= count {
			return nil, fmt.Errorf("%slane: expected a lane value and an index below %d", p, count)
		}
		return Number(ls[0].number(int(n))), nil
	}
	builtins["bitmask"] = func(args []Value) (Value, error) {
		ls, err := intLaneArgs(p+"bitmask", like, args, 1)
		if err != nil {
			return nil, err
		}
		var mask Number
		for k, bits := range ls[0].Bits {
			mask |= Number(bits>>(like.Width-1)) << k
		}
		return mask, nil
	}
	builtins["select"] = func(args []Value) (Value, error) {
		ls, err := intLaneArgs(p+"select", like, args, 3)
		if err != nil {
			return nil, err
		}
		m := ls[2]
		kept := mapIntLanes(ls[0], m, func(_ Lanes, x, y int64) int64 { return x & y })
		rest := mapIntLanes(ls[1], m, func(_ Lanes, x, y int64) int64 { return x &^ y })
		return mapIntLanes(kept, rest, func(_ Lanes, x, y int64) int64 { return x | y }), nil
	}
	builtins["abs"] = func(args []Value) (Value, error) {
		ls, err := intLaneArgs(p+"abs", like, args, 1)
		if err != nil {
			return nil, err
		}
		return mapIntLanes(ls[0], ls[0], func(_ Lanes, x, _ int64) int64 { return max(x, -x) }), nil
	}
	for _, verb := range []string{"shl", "shr"} {
		name, left := p+verb, verb == "shl"
		builtins[verb] = func(args []Value) (Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("%s: expected 2 args, got %d", name, len(args))
			}
			ls, err := intLaneArgs(name, like, args[:1], 1)
			n, ok := args[1].(Number)
			if err != nil || !ok {
				return nil, fmt.Errorf("%s: expected a lane value and a count", name)
			}
			// The count is modulo the width of a lane.
			by := uint(n) & uint(like.Width-1)
			return mapIntLanes(ls[0], ls[0], func(_ Lanes, x, _ int64) int64 {
				if left {
					return x << by
				}
				return x >> by
			}), nil
		}
	}
	builtins["load"] = func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("%sload: expected 2 args, got %d", p, len(args))
		}
		a, at, err := laneWindow(p+"load", args[0], args[1], 16)
		if err != nil {
			return nil, err
		}
		out := like.like()
		for k, b := range a.Elems()[at : at+16] {
			n, ok := b.(Number)
			if !ok {
				return nil, fmt.Errorf("%sload: expected bytes, got %T", p, b)
			}
			out.Bits[k/size] |= uint64(uint8(n)) << (8 * (k % size))
		}
		return out, nil
	}
	builtins["with"] = func(args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("%swith: expected 3 args, got %d", p, len(args))
		}
		a, at, err := laneWindow(p+"with", args[0], args[1], 16)
		if err != nil {
			return nil, err
		}
		ls, err := intLaneArgs(p+"with", like, args[2:], 1)
		if err != nil {
			return nil, err
		}
		e := append([]Value(nil), a.Elems()...)
		for k := range 16 {
			e[at+k] = Number(uint8(ls[0].Bits[k/size] >> (8 * (k % size))))
		}
		return arrayOf(e...), nil
	}
	for _, verb := range intLaneVerbs(like) {
		name := p + verb
		fn := builtins[verb]
		if fn == nil {
			binary := intLaneBinary[verb]
			fn = func(args []Value) (Value, error) {
				ls, err := intLaneArgs(name, like, args, 2)
				if err != nil {
					return nil, err
				}
				return mapIntLanes(ls[0], ls[1], binary), nil
			}
		}
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) { return fn(args) }}
	}
}

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
