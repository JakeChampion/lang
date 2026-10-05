package interp

import "testing"

func TestOuterMulRejectsOverflowBeforeAllocation(t *testing.T) {
	// Each input is small; their product exceeds the language's i32 length.
	a := newArray(46341)
	exit := 0
	i := &Interp{Exiter: func(code int) { exit = code }}
	if _, err := builtinOuterMulF64(i, []Value{a, a}); err != nil {
		t.Fatal(err)
	}
	if exit != 134 {
		t.Fatalf("overflow exit = %d, want 134", exit)
	}
}

func TestOuterMulBuiltinArguments(t *testing.T) {
	floats := newArray(1)
	floats.E[0] = Float{V: 2, Width: 64}
	bad := newArray(1)
	bad.E[0] = Number(2)
	for _, tc := range []struct {
		name string
		args []Value
	}{
		{"arity", []Value{floats}},
		{"left type", []Value{Number(1), floats}},
		{"right type", []Value{floats, Number(1)}},
		{"left element", []Value{bad, floats}},
		{"right element", []Value{floats, bad}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := builtinOuterMulF64(&Interp{}, tc.args); err == nil {
				t.Fatal("invalid arguments accepted")
			}
		})
	}
}
