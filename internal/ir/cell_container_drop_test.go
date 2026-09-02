package ir

import "testing"

// A `Cell[T]` reached as a TUPLE ELEMENT or an ENUM PAYLOAD reclaims through
// the array machinery, like a cell local and like a plain array child beside
// it — not through a bare `__fern_rc_dec`, which decrements and returns.
//
// The exit sweep inlines its own drop for a tuple / enum local rather than
// calling the generated `__drop_tuple_*` (which is emitted, but only reaches
// the reinit drop of the slot's previous value). That inline arm hands each
// rc-tracked child to `dropStructField`, whose array case frees the buffer
// but whose cell case used to fall through to `decValueOnStack(t, false)` —
// gated on `mayFree`, so a plain dec. One unpaired allocation per cell, with
// no accumulation and no exit-code symptom.
//
// Asserted on `main` specifically: the helper has to appear in the sweep that
// actually runs. Both cases fail without the fix — the generated drop fn
// carries the helper either way, so a whole-program search would not.
func TestTupleAndEnumCellChildDropThroughArrayMachinery(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "tuple scalar cell",
			src: `function main(): i32 {
    var t: (i32, Cell[i32]) = (1, cell_new(0));
    return t.0 - 1;
}`,
			want: "__fern_arr_dec",
		},
		{
			name: "tuple string cell",
			src: `function main(): i32 {
    var t: (i32, Cell[string]) = (1, cell_new(""));
    return t.0 - 1;
}`,
			want: "__fern_drop_arr_str",
		},
		{
			name: "enum scalar cell payload",
			src: `enum H { Has(Cell[i32]), No }
function main(): i32 {
    var h: H = Has(cell_new(0));
    return 0;
}`,
			want: "__fern_arr_dec",
		},
	}
	for _, tc := range cases {
		for _, ptrW := range []int{4, 8} {
			p := lowerSourceWith(t, tc.src, ptrW)
			ops := funcOpsOf(p, "main")
			if len(ops) == 0 {
				t.Fatalf("%s ptrW=%d: no main in the lowering:\n%s", tc.name, ptrW, p)
			}
			found := false
			for _, op := range ops {
				if isNamedCallKind(op.Kind) && op.Str == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("%s ptrW=%d: main's exit sweep never calls %s, so the cell box is never freed:\n%s",
					tc.name, ptrW, tc.want, p)
			}
		}
	}
}
