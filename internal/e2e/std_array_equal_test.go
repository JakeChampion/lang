package e2e

import "testing"

// TestStdArrayEqual pins the Eq-driven array-equality surface after #5348
// consolidated the generic verbs into core/cmp:
//   - `cmp.eq_arrays[T: Eq]` — structural array equality, called qualified
//     through `import "core/cmp"` (the single home for the free function);
//   - `xs.equal(other)` — the std/array method form, which delegates to
//     `cmp.eq_arrays`, so an `import "std/array"` program still gets element-
//     wise equality without importing core/cmp directly;
//   - `array.index_of_last` — the reverse-scan Eq verb that stays in std/array.
// All are bound `[T: Eq]` and compare elements with `==`, so `i32` keeps the
// scalar compare and `string` dispatches byte equality, on interp + x86-64 +
// wasm.
func TestStdArrayEqual(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "cmp.eq_arrays i32 + length mismatch",
			src: `import "core/cmp" as cmp;
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = [1, 2, 3];
    var c: i32[] = [1, 2, 4];
    var r: i32 = 0;
    if (cmp.eq_arrays(a, b)) { r = r + 1; }
    if (!cmp.eq_arrays(a, c)) { r = r + 2; }
    if (!cmp.eq_arrays(a, [1, 2])) { r = r + 4; }
    return r;
}`,
			want: 7,
		},
		{
			name: "cmp.eq_arrays string + empty",
			src: `import "core/cmp" as cmp;
function main(): i32 {
    var ss: string[] = ["x", "y", "x"];
    var empty: i32[] = [];
    var empty2: i32[] = [];
    var r: i32 = 0;
    if (cmp.eq_arrays(ss, ["x", "y", "x"])) { r = r + 10; }
    if (!cmp.eq_arrays(ss, ["x", "y", "z"])) { r = r + 5; }
    if (cmp.eq_arrays(empty, empty2)) { r = r + 1; }
    return r;
}`,
			want: 16,
		},
		{
			name: "xs.equal method form via std/array",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = [1, 2, 3];
    var c: i32[] = [1, 2, 4];
    var r: i32 = 0;
    if (a.equal(b)) { r = r + 1; }
    if (!a.equal(c)) { r = r + 2; }
    if (!a.equal([1, 2])) { r = r + 4; }
    return r;
}`,
			want: 7,
		},
		{
			name: "index_of_last last vs first",
			src: `import "std/array";
function uw(o: Option[i32], d: i32): i32 { match (o) { Some(v) => { return v; }, None => { return d; } } return d; }
function main(): i32 {
    var a: i32[] = [5, 1, 5, 2, 5];
    var ss: string[] = ["a", "b", "a"];
    var r: i32 = 0;
    r = r + uw(array.index_of_last(a, 5), 0 - 1) * 10; // last 5 at index 4 -> 40
    r = r + uw(array.index_of_last(ss, "a"), 0 - 1);   // last "a" at index 2 -> +2
    if (uw(array.index_of_last(a, 9), 0 - 1) == 0 - 1) { r = r + 1; } // miss -> +1
    return r; // 43
}`,
			want: 43,
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
