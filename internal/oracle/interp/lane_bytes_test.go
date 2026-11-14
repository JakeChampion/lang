package interp

import "testing"

func byteLanes(bs ...uint64) Lanes {
	out := Lanes{Width: 8, Int: true, Bits: make([]uint64, 16)}
	for k := range out.Bits {
		out.Bits[k] = bs[k%len(bs)]
	}
	return out
}

// The builtins of u8x16 cut each result to 8 bits, and the arithmetic that
// saturates stops at 0 and at 255.
func TestByteLaneBuiltins(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	a, b := byteLanes(200, 7, 0, 255), byteLanes(100, 250, 0, 1)
	for verb, want := range map[string]Lanes{
		"min":     byteLanes(100, 7, 0, 1),
		"max":     byteLanes(200, 250, 0, 255),
		"eq":      byteLanes(0, 0, 255, 0),
		"ne":      byteLanes(255, 255, 0, 255),
		"lt":      byteLanes(0, 255, 0, 0),
		"add_sat": byteLanes(255, 255, 0, 255),
		"sub_sat": byteLanes(100, 0, 0, 254),
		"avg":     byteLanes(150, 129, 0, 128),
		"and":     byteLanes(64, 2, 0, 1),
		"or":      byteLanes(236, 255, 0, 255),
		"xor":     byteLanes(172, 253, 0, 254),
	} {
		got, err := i.Builtins["__u8x16_"+verb].Fn(i, []Value{a, b})
		if err != nil || got.(Lanes).String() != want.String() {
			t.Errorf("%s = %v, %v, want %v", verb, got, err, want)
		}
	}
	for op, want := range map[string]Lanes{"+": byteLanes(44, 1, 0, 0), "-": byteLanes(100, 13, 0, 254)} {
		got, err := lanesBinary(op, a, b)
		if err != nil || got.(Lanes).String() != want.String() {
			t.Errorf("%s = %v, %v, want %v", op, got, err, want)
		}
	}
	if _, err := lanesBinary("*", a, b); err == nil {
		t.Error("* on integer lanes is an error")
	}
	mask, err := i.Builtins["__u8x16_bitmask"].Fn(i, []Value{byteLanes(128, 127, 255, 0)})
	if err != nil || mask != Number(0x5555) {
		t.Errorf("bitmask = %v, %v, want 0x5555", mask, err)
	}
	picked, err := i.Builtins["__u8x16_select"].Fn(i, []Value{a, b, byteLanes(255, 0, 15, 240)})
	if want := byteLanes(200, 250, 0, 241); err != nil || picked.(Lanes).String() != want.String() {
		t.Errorf("select = %v, %v, want %v", picked, err, want)
	}
	if _, err := i.Builtins["__u8x16_min"].Fn(i, []Value{a, Lanes{Width: 32, Bits: make([]uint64, 4)}}); err == nil {
		t.Error("a float lane value is not an argument of a builtin of u8x16")
	}
}

func intLanes(width int, signed bool, xs ...int64) Lanes {
	out := Lanes{Width: width, Int: true, Signed: signed, Bits: make([]uint64, 128/width)}
	for k := range out.Bits {
		out.Bits[k] = uint64(xs[k%len(xs)]) & out.ones()
	}
	return out
}

// A signed lane compares and saturates with its sign, and each result is
// cut to the width of a lane.
func TestIntegerLaneBuiltins(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	a, b := intLanes(16, true, 30000, -30000, -1, 7), intLanes(16, true, 10000, -10000, 1, -7)
	for verb, want := range map[string]Lanes{
		"min":     intLanes(16, true, 10000, -30000, -1, -7),
		"max":     intLanes(16, true, 30000, -10000, 1, 7),
		"lt":      intLanes(16, true, 0, -1, -1, 0),
		"add_sat": intLanes(16, true, 32767, -32768, 0, 0),
		"sub_sat": intLanes(16, true, 20000, -20000, -2, 14),
	} {
		got, err := i.Builtins["__i16x8_"+verb].Fn(i, []Value{a, b})
		if err != nil || got.(Lanes).String() != want.String() {
			t.Errorf("%s = %v, %v, want %v", verb, got, err, want)
		}
	}
	for op, want := range map[string]Lanes{"+": intLanes(16, true, -25536, 25536, 0, 0), "*": intLanes(16, true, -23808, -23808, -1, -49)} {
		got, err := lanesBinary(op, a, b)
		if err != nil || got.(Lanes).String() != want.String() {
			t.Errorf("%s = %v, %v, want %v", op, got, err, want)
		}
	}
	if got := lanesNeg(a).String(); got != intLanes(16, true, -30000, 30000, 1, -7).String() {
		t.Errorf("neg = %v", got)
	}
	for verb, want := range map[string]Lanes{
		"shl": intLanes(32, false, 0xfffffff0, 0x10),
		"shr": intLanes(32, false, 0x0fffffff, 0),
	} {
		// A count of 36 is a count of 4.
		got, err := i.Builtins["__u32x4_"+verb].Fn(i, []Value{intLanes(32, false, 0xffffffff, 1), Number(36)})
		if err != nil || got.(Lanes).String() != want.String() {
			t.Errorf("%s = %v, %v, want %v", verb, got, err, want)
		}
	}
	shifted, err := i.Builtins["__i8x16_shr"].Fn(i, []Value{intLanes(8, true, -128, 64), Number(3)})
	if want := intLanes(8, true, -16, 8); err != nil || shifted.(Lanes).String() != want.String() {
		t.Errorf("signed shr = %v, %v, want %v", shifted, err, want)
	}
	mask, err := i.Builtins["__i32x4_bitmask"].Fn(i, []Value{intLanes(32, true, -1, 1, -5, 0)})
	if err != nil || mask != Number(5) {
		t.Errorf("bitmask = %v, %v, want 5", mask, err)
	}
	if _, ok := i.Builtins["__i32x4_add_sat"]; ok {
		t.Error("lanes of 32 bits have no arithmetic that saturates")
	}
	if _, ok := i.Builtins["__i16x8_avg"]; ok {
		t.Error("a signed lane type has no average")
	}
	if _, err := i.Builtins["__i16x8_min"].Fn(i, []Value{a, intLanes(16, false, 1)}); err == nil {
		t.Error("a lane value of u16x8 is not an argument of a builtin of i16x8")
	}
}

// A narrow holds each lane to the limits of the narrow lane, and a swizzle
// gives zero for an index above 15.
func TestIntegerLaneShapes(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	call := func(name string, args ...Value) string {
		got, err := i.Builtins[name].Fn(i, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return got.(Lanes).String()
	}
	a := intLanes(16, true, -300, -1, 127, 128, 255, 300, 0, 1)
	if got, want := call("__i16x8_narrow", a, a), intLanes(8, true, -128, -1, 127, 127, 127, 127, 0, 1).String(); got != want {
		t.Errorf("narrow = %v, want %v", got, want)
	}
	if got, want := call("__i16x8_narrow_u", a, a), intLanes(8, false, 0, 0, 127, 128, 255, 255, 0, 1).String(); got != want {
		t.Errorf("narrow_u = %v, want %v", got, want)
	}
	u := intLanes(16, false, 0, 255, 256, 40000)
	if got, want := call("__u16x8_narrow", u, u), intLanes(8, false, 0, 255, 255, 255).String(); got != want {
		t.Errorf("unsigned narrow = %v, want %v", got, want)
	}
	s := intLanes(8, true, -1, 2, -3, 4, 5, 6, 7, 8, -9, 10, 11, 12, 13, 14, 15, -16)
	if got, want := call("__i8x16_widen_lo", s), intLanes(16, true, -1, 2, -3, 4, 5, 6, 7, 8).String(); got != want {
		t.Errorf("widen_lo = %v, want %v", got, want)
	}
	if got, want := call("__i8x16_widen_hi", s), intLanes(16, true, -9, 10, 11, 12, 13, 14, 15, -16).String(); got != want {
		t.Errorf("widen_hi = %v, want %v", got, want)
	}
	b := intLanes(8, false, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25)
	idx := intLanes(8, false, 15, 0, 16, 255, 1, 1, 128, 14, 2, 3, 4, 5, 6, 7, 8, 9)
	if got, want := call("__u8x16_swizzle", b, idx), intLanes(8, false, 25, 10, 0, 0, 11, 11, 0, 24, 12, 13, 14, 15, 16, 17, 18, 19).String(); got != want {
		t.Errorf("swizzle = %v, want %v", got, want)
	}
	for _, name := range []string{"__i32x4_widen_lo", "__u8x16_narrow", "__u16x8_narrow_u", "__i8x16_swizzle"} {
		if _, ok := i.Builtins[name]; ok {
			t.Errorf("%s is not a builtin", name)
		}
	}
}
