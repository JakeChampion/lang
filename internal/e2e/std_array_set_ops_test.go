package e2e

import "testing"

// TestStdArraySetOps pins the Eq-driven set-algebra verbs
// (`count` / `union` / `intersection` / `difference`) as they ship in
// std/array — called qualified through `import "std/array"`. They share
// the `[T: cmp.Eq]` bound (and per-element-type monomorphisation) with
// `contains` / `distinct`, so `i32` keeps the scalar `==` while `string`
// dispatches byte equality, on interp + x86-64 + wasm. The self-host IR
// path is pinned separately in self_host_set_ops_ir_test.go.
func TestStdArraySetOps(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "count i32 + string",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 2, 3, 2];
    var ss: string[] = ["x", "y", "x"];
    return array.count(a, 2) * 10 + array.count(ss, "x") + array.count(a, 9);
}`,
			want: 32, // 3 twos -> 30, 2 x -> +2, no 9 -> +0
		},
		{
			name: "union dedups across both",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 2, 3, 4];
    var b: i32[] = [3, 4, 4, 5];
    var u: i32[] = array.union(a, b);
    // {1,2,3,4,5}; len*10 + first + last
    return u.len() * 10 + u[0] + u[4]; // 50 + 1 + 5
}`,
			want: 56,
		},
		{
			name: "intersection in a-order",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [4, 3, 2, 1];
    var b: i32[] = [1, 3, 5];
    var x: i32[] = array.intersection(a, b);
    // {3,1} in a-order: [3,1]; len*10 + x[0]*2 + x[1]
    return x.len() * 10 + x[0] * 2 + x[1]; // 20 + 6 + 1
}`,
			want: 27,
		},
		{
			name: "difference a minus b + strings",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3, 4];
    var b: i32[] = [2, 4];
    var d: i32[] = array.difference(a, b); // {1,3}
    var ss: string[] = ["a", "b", "c", "b"];
    var tt: string[] = ["b"];
    var sd: string[] = array.difference(ss, tt); // {a,c}
    return d.len() * 10 + d[0] + d[1] + sd.len(); // 20 + 1 + 3 + 2
}`,
			want: 26,
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
