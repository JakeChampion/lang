package e2e

import "testing"

// TestStdArrayEqual pins the Eq-driven `equal` (structural array
// equality) and `index_of_last` verbs as they ship in std/array —
// called qualified through `import "std/array"`. Both are bound
// `[T: cmp.Eq]` and compare elements with `==`, so `i32` keeps the
// scalar compare and `string` dispatches byte equality, on interp +
// x86-64 + wasm. The self-host IR path is pinned in
// self_host_equal_ir_test.go.
func TestStdArrayEqual(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "equal i32 + length mismatch",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = [1, 2, 3];
    var c: i32[] = [1, 2, 4];
    var r: i32 = 0;
    if (array.equal(a, b)) { r = r + 1; }
    if (!array.equal(a, c)) { r = r + 2; }
    if (!array.equal(a, [1, 2])) { r = r + 4; }
    return r;
}`,
			want: 7,
		},
		{
			name: "equal string + empty",
			src: `import "std/array";
function main(): i32 {
    var ss: string[] = ["x", "y", "x"];
    var empty: i32[] = [];
    var empty2: i32[] = [];
    var r: i32 = 0;
    if (array.equal(ss, ["x", "y", "x"])) { r = r + 10; }
    if (!array.equal(ss, ["x", "y", "z"])) { r = r + 5; }
    if (array.equal(empty, empty2)) { r = r + 1; }
    return r;
}`,
			want: 16,
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
