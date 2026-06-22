package e2e

import "testing"

// TestStdArrayEqVerbs pins the Eq-driven generic array verbs
// (`contains` / `index_of` / `distinct`) as they ship in std/array —
// called qualified through `import "std/array"`. Unlike the structural
// verbs (reverse / take / drop / concat / zip), these compare elements
// with `==`, so the element type must implement `Eq`; `i32` and
// `string` satisfy that out of the box.
//
// This guards the generic-instantiation-through-import-mangling
// pipeline for a body whose `==` lowers to the primitive scalar /
// string comparison (no `core/cmp` dependency), across the
// interpreter, the x86-64 native backend, and wasm. The self-host IR
// path is pinned separately in self_host_eq_verbs_ir_test.go.
func TestStdArrayEqVerbs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "contains i32 + string",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [10, 20, 30, 20];
    var ss: string[] = ["x", "y", "z"];
    var r: i32 = 0;
    if (array.contains(a, 20)) { r = r + 1; }
    if (!array.contains(a, 99)) { r = r + 2; }
    if (array.contains(ss, "z")) { r = r + 4; }
    if (!array.contains(ss, "q")) { r = r + 8; }
    return r;
}`,
			want: 15,
		},
		{
			name: "index_of Option arms",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [5, 10, 15, 20];
    var hit: i32 = 0;
    match (array.index_of(a, 15)) {
        Some(v) => { hit = v; },
        None => { hit = 0 - 1; }
    }
    var miss: i32 = 0;
    match (array.index_of(a, 99)) {
        Some(v) => { miss = v; },
        None => { miss = 1; }
    }
    return hit * 10 + miss; // index 2 -> 20, +1 for the miss
}`,
			want: 21,
		},
		{
			name: "distinct preserves first-seen order",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [3, 1, 3, 2, 1, 3];
    var d: i32[] = array.distinct(a);
    // first-seen order: [3, 1, 2]
    return d.len() * 50 + d[0] * 10 + d[1] * 3 + d[2]; // 150 + 30 + 3 + 2
}`,
			want: 185,
		},
		{
			name: "distinct on string[]",
			src: `import "std/array";
function main(): i32 {
    var ss: string[] = ["a", "b", "a", "c", "b"];
    return array.distinct(ss).len(); // [a, b, c] -> 3
}`,
			want: 3,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpByte(t, c.src); got != c.want {
				t.Errorf("interp: got exit %d, want %d", got, c.want)
			}
			if _, got := compileAndRunX86Native(t, c.src); got != c.want {
				t.Errorf("x86-64 native: got exit %d, want %d", got, c.want)
			}
			if got := compileAndRunWasmbinMain(t, c.src); got != c.want {
				t.Errorf("wasm: got exit %d, want %d", got, c.want)
			}
		})
	}
}
