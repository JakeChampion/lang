package ir

import "testing"

// A reused box's OLD `Cell` field is reclaimed through the array machinery,
// not through a bare `__fern_rc_dec`.
//
// When Perceus reuse hands a dead container's box to the next one, the reuse
// branch first releases the previous occupant's pointer fields
// (`emitReuseOldFieldDrops` / the struct- and enum-overwrite reuse paths, all
// three via `emitFieldDropOnStack`). That function delegated ARRAY fields to
// `dropStructField`'s ladder but left everything else to `dropFnNameFor`,
// which declines `Cell` — so a cell field fell to the flat dec, which
// decrements and returns. The box was stranded once per reuse site, which is
// why N containers in a function leaked N − 1: only the first allocates fresh.
//
// The assertion is positional rather than a count: the first reclaim call
// AFTER `__alloc_reuse` is the reinit drop of the old field, and it has to be
// the cell helper. A whole-function count would pass on the exit sweep alone.
func TestReusedBoxOldCellFieldDropThroughArrayMachinery(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "scalar cell field",
			src: `function main(): i32 {
    var a: (i32, Cell[i32]) = (7, cell_new(0));
    var r: i32 = a.1.get();
    var b: (i32, Cell[i32]) = (1, cell_new(2));
    return r + b.0 - 1;
}`,
			want: "__fern_arr_dec",
		},
		{
			name: "string cell field",
			src: `function main(): i32 {
    var a: (i32, Cell[string]) = (7, cell_new("p"));
    var r: i32 = a.0;
    var b: (i32, Cell[string]) = (1, cell_new("q"));
    return r + b.0 - 8;
}`,
			want: "__fern_drop_arr_str",
		},
	}
	reclaim := map[string]bool{
		"__fern_arr_dec":      true,
		"__fern_drop_arr_str": true,
		"__fern_rc_dec":       true,
	}
	for _, tc := range cases {
		for _, ptrW := range []int{4, 8} {
			p := lowerSourceWith(t, tc.src, ptrW)
			ops := funcOpsOf(p, "main")
			reuse := -1
			for i, op := range ops {
				if isNamedCallKind(op.Kind) && op.Str == "__alloc_reuse" {
					reuse = i
					break
				}
			}
			if reuse < 0 {
				t.Fatalf("%s ptrW=%d: no __alloc_reuse in main, so the shape no longer "+
					"exercises the reuse reinit drop this test is about:\n%s", tc.name, ptrW, p)
			}
			got := ""
			for _, op := range ops[reuse+1:] {
				if isNamedCallKind(op.Kind) && reclaim[op.Str] {
					got = op.Str
					break
				}
			}
			if got != tc.want {
				t.Errorf("%s ptrW=%d: first reclaim after __alloc_reuse is %q, want %q — "+
					"the old cell field is dec'd without being freed:\n%s",
					tc.name, ptrW, got, tc.want, p)
			}
		}
	}
}
