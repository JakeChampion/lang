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
