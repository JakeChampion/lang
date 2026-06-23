package e2e

import "testing"

// TestStdArrayPrefix pins the Eq-driven `starts_with` / `ends_with`
// array prefix/suffix checks as they ship in std/array — qualified
// through `import "std/array"`. Bound `[T: cmp.Eq]`, they compare
// elements with `==` (scalar for i32, str_eq for string), so they work
// on interp + x86-64 + wasm. The self-host IR path is pinned in
// self_host_prefix_ir_test.go.
func TestStdArrayPrefix(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "starts_with i32 + bounds + empty",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3, 4, 5];
    var e: i32[] = [];
    var r: i32 = 0;
    if (array.starts_with(a, [1, 2, 3])) { r = r + 1; }
    if (!array.starts_with(a, [1, 3])) { r = r + 2; }
    if (array.starts_with(a, e)) { r = r + 4; }
    if (!array.starts_with([1, 2], [1, 2, 3])) { r = r + 8; }
    return r;
}`,
			want: 15,
		},
		{
			name: "ends_with i32 + bounds",
			src: `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3, 4, 5];
    var e: i32[] = [];
    var r: i32 = 0;
    if (array.ends_with(a, [4, 5])) { r = r + 1; }
    if (!array.ends_with(a, [3, 5])) { r = r + 2; }
    if (array.ends_with(a, e)) { r = r + 4; }
    if (!array.ends_with([4, 5], [3, 4, 5])) { r = r + 8; }
    return r;
}`,
			want: 15,
		},
		{
			name: "string element prefix/suffix",
			src: `import "std/array";
function main(): i32 {
    var ss: string[] = ["a", "b", "c", "d"];
    var r: i32 = 0;
    if (array.starts_with(ss, ["a", "b"])) { r = r + 10; }
    if (array.ends_with(ss, ["c", "d"])) { r = r + 5; }
    if (!array.starts_with(ss, ["a", "c"])) { r = r + 1; }
    return r;
}`,
			want: 16,
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
