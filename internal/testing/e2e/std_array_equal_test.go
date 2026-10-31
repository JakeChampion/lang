package e2e

import "testing"

func TestStdArrayEqual(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "== on i32 arrays + length mismatch",
			src: `import "core/cmp";
function main(): i32 {
    let a: i32[] = [1, 2, 3];
    let b: i32[] = [1, 2, 3];
    let c: i32[] = [1, 2, 4];
    let r: i32 = 0;
    if (a == b) { r = r + 1; }
    if (a != c) { r = r + 2; }
    if (a != [1, 2]) { r = r + 4; }
    return r;
}`,
			want: 7,
		},
		{
			name: "== on string arrays + empty",
			src: `import "core/cmp";
function main(): i32 {
    let ss: string[] = ["x", "y", "x"];
    let empty: i32[] = [];
    let empty2: i32[] = [];
    let r: i32 = 0;
    if (ss == ["x", "y", "x"]) { r = r + 10; }
    if (ss != ["x", "y", "z"]) { r = r + 5; }
    if (empty == empty2) { r = r + 1; }
    return r;
}`,
			want: 16,
		},
		{
			name: "index_of_last last vs first",
			src: `import "std/array";
function uw(o: Option[i32], d: i32): i32 { match (o) { Some(v) => { return v; }, None => { return d; } } return d; }
function main(): i32 {
    let a: i32[] = [5, 1, 5, 2, 5];
    let ss: string[] = ["a", "b", "a"];
    let r: i32 = 0;
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
			t.Run("x86-64 native", func(t *testing.T) {
				if _, got := compileAndRunX86_64(t, c.src); got != c.want {
					t.Errorf("x86-64 native: got exit %d, want %d", got, c.want)
				}
			})
			t.Run("wasm", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, c.src); got != c.want {
					t.Errorf("wasm: got exit %d, want %d", got, c.want)
				}
			})
		})
	}
}
