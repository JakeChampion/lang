package interp

import (
	"fmt"
	"testing"
)

func TestArrayAppendValueSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Number
	}{
		{
			name: "two appends off one array",
			src: `function main(): i32 {
				let a: i32[] = [1, 2];
				let x: i32[] = a.append(3);
				let y: i32[] = a.append(4);
				return x[2] * 100 + y[2] * 10 + a.len();
			}`,
			want: 342,
		},
		{
			name: "two appends off a grown array",
			src: `function main(): i32 {
				let a: i32[] = [];
				let i: i32 = 0;
				while (i < 10) { a = a.append(i); i = i + 1; }
				let x: i32[] = a.append(100);
				let y: i32[] = a.append(200);
				return x[10] + y[10] + a.len();
			}`,
			want: 310,
		},
		{
			name: "chained append off an alias",
			src: `function main(): i32 {
				let a: i32[] = [1, 2];
				let x: i32[] = a.append(3);
				let z: i32[] = x.append(5);
				let y: i32[] = a.append(4);
				return z[3] * 100 + y[2] * 10 + z[2];
			}`,
			want: 543,
		},
		{
			name: "append after storing in a nested array",
			src: `function main(): i32 {
				let a: i32[] = [];
				let i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				let rows: i32[][] = [];
				rows = rows.append(a);
				a = a.append(99);
				return rows[0].len() * 10 + a.len();
			}`,
			want: 56,
		},
		{
			name: "append after storing in a struct field",
			src: `struct Box { items: i32[] }
			function main(): i32 {
				let a: i32[] = [];
				let i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				let b: Box = Box { items: a };
				a = a.append(99);
				return b.items.len() * 10 + a.len();
			}`,
			want: 56,
		},
		{
			name: "two appends off one array through a field",
			src: `struct Box { items: i32[] }
			function main(): i32 {
				let a: i32[] = [];
				let i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				let b: Box = Box { items: a };
				let x: i32[] = b.items.append(100);
				let y: i32[] = a.append(200);
				return x[5] + y[5] + b.items.len();
			}`,
			want: 305,
		},
		{
			name: "two appends off one array through a call",
			src: `function grow(p: i32[], v: i32): i32[] { return p.append(v); }
			function main(): i32 {
				let a: i32[] = [];
				let i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				let x: i32[] = grow(a, 100);
				let y: i32[] = grow(a, 200);
				return x[5] + y[5] + a.len();
			}`,
			want: 305,
		},
		{
			name: "param appended in argument position",
			src: `function walk(path: i32[], depth: i32): i32 {
				if (depth == 0) { return path.len(); }
				let a: i32 = walk(path.append(depth), depth - 1);
				let b: i32 = path.append(depth).len();
				return a * 100 + b;
			}
			function main(): i32 {
				let p: i32[] = [];
				return walk(p, 2);
			}`,
			want: 20201,
		},
		{
			name: "self-reassign append loop",
			src: `function main(): i32 {
				let a: i32[] = [];
				let i: i32 = 0;
				while (i < 64) { a = a.append(i); i = i + 1; }
				let sum: i32 = 0;
				let j: i32 = 0;
				while (j < a.len()) { sum = sum + a[j]; j = j + 1; }
				return sum;
			}`,
			want: 2016,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalProgramValue(t, tc.src); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestArrayAppendIsAmortised(t *testing.T) {
	build := func(n int) int {
		t.Helper()
		src := fmt.Sprintf(`function main(): i32 {
			let a: i32[] = [];
			let i: i32 = 0;
			while (i < %d) { a = a.append(i); i = i + 1; }
			return a.len();
		}`, n)
		v, ip := evalProgram(t, src)
		if v != Number(n) {
			t.Fatalf("built %v elements, want %d", v, n)
		}
		return ip.arrayGrowCopies
	}
	const small, large = 1000, 4000
	smallCopies, largeCopies := build(small), build(large)
	if smallCopies > 3*small {
		t.Errorf("%d appends copied %d elements, want at most %d", small, smallCopies, 3*small)
	}
	if largeCopies > 3*large {
		t.Errorf("%d appends copied %d elements, want at most %d", large, largeCopies, 3*large)
	}
	if ratio := float64(largeCopies) / float64(smallCopies); ratio > 8 {
		t.Errorf("copies grew %.1fx for a 4x larger array, want at most 8x (%d then %d)",
			ratio, smallCopies, largeCopies)
	}
}
