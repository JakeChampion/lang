package interp

import "testing"

func TestInnerMulAddRejectsGeometryBeforeAllocation(t *testing.T) {
	for _, dims := range [][3]Number{{-1, 0, 0}, {0, -1, 0}, {0, 0, -1}, {1, 1, 1}, {2147483647, 0, 2}, {65536, 65536, 0}, {0, 65536, 65536}} {
		exit := 0
		i := &Interp{Exiter: func(code int) { exit = code }}
		if _, err := builtinInnerMulAddF64(i, []Value{newArray(0), newArray(0), dims[0], dims[1], dims[2], Float{Width: 64}}); err != nil {
			t.Fatal(err)
		}
		if exit != 134 {
			t.Fatalf("geometry %v exit = %d, want 134", dims, exit)
		}
	}
}

func TestInnerMulAddBuiltinArguments(t *testing.T) {
	floats := newArray(1)
	floats.E[0] = Float{V: 2, Width: 64}
	bad := newArray(1)
	bad.E[0] = Number(2)
	valid := []Value{floats, floats, Number(1), Number(1), Number(1), Float{Width: 64}}
	for _, tc := range []struct {
		name  string
		index int
		value Value
	}{
		{"left type", 0, Number(1)},
		{"right type", 1, Number(1)},
		{"rows type", 2, Float{V: 1}},
		{"extent type", 3, Float{V: 1}},
		{"columns type", 4, Float{V: 1}},
		{"init type", 5, Number(0)},
		{"left element", 0, bad},
		{"right element", 1, bad},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]Value(nil), valid...)
			args[tc.index] = tc.value
			if _, err := builtinInnerMulAddF64(&Interp{}, args); err == nil {
				t.Fatal("invalid arguments accepted")
			}
		})
	}
	if _, err := builtinInnerMulAddF64(&Interp{}, valid[:5]); err == nil {
		t.Fatal("invalid arity accepted")
	}
}
