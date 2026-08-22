package interp

import (
	"fmt"
	"testing"
)

// Array append grows the receiver's backing buffer in place when the
// slot it extends into is unclaimed (#6395). Each case here holds a
// second array value over that same buffer, so a growth that skipped
// the claim check would be observable through it — arrays are values,
// and `x = a.append(v)` must leave `a`, and every other view of its
// buffer, alone.
func TestArrayAppendValueSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Number
	}{
		{
			// Two appends off one array: the second must not see, or
			// overwrite, the element the first one added.
			name: "two appends off one array",
			src: `function main(): i32 {
				var a: i32[] = [1, 2];
				var x: i32[] = a.append(3);
				var y: i32[] = a.append(4);
				return x[2] * 100 + y[2] * 10 + a.len();
			}`,
			want: 342,
		},
		{
			// Same, on a buffer carrying spare capacity from an earlier
			// growth rather than one sized exactly to its elements.
			name: "two appends off a grown array",
			src: `function main(): i32 {
				var a: i32[] = [];
				var i: i32 = 0;
				while (i < 10) { a = a.append(i); i = i + 1; }
				var x: i32[] = a.append(100);
				var y: i32[] = a.append(200);
				return x[10] + y[10] + a.len();
			}`,
			want: 310,
		},
		{
			// A chain off one array: z extends x, y still starts from a.
			name: "chained append off an alias",
			src: `function main(): i32 {
				var a: i32[] = [1, 2];
				var x: i32[] = a.append(3);
				var z: i32[] = x.append(5);
				var y: i32[] = a.append(4);
				return z[3] * 100 + y[2] * 10 + z[2];
			}`,
			want: 543,
		},
		{
			// An array stored in a container must not grow when the
			// binding it came from does. Built by a loop so the buffer
			// has spare capacity at the point the two views split.
			name: "append after storing in a nested array",
			src: `function main(): i32 {
				var a: i32[] = [];
				var i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				var rows: i32[][] = [];
				rows = rows.append(a);
				a = a.append(99);
				return rows[0].len() * 10 + a.len();
			}`,
			want: 56,
		},
		{
			// Same through a struct field.
			name: "append after storing in a struct field",
			src: `struct Box { items: i32[] }
			function main(): i32 {
				var a: i32[] = [];
				var i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				var b: Box = Box { items: a };
				a = a.append(99);
				return b.items.len() * 10 + a.len();
			}`,
			want: 56,
		},
		{
			// The two views that split the buffer reached it by
			// different routes — one through a struct field, one
			// through the binding.
			name: "two appends off one array through a field",
			src: `struct Box { items: i32[] }
			function main(): i32 {
				var a: i32[] = [];
				var i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				var b: Box = Box { items: a };
				var x: i32[] = b.items.append(100);
				var y: i32[] = a.append(200);
				return x[5] + y[5] + b.items.len();
			}`,
			want: 305,
		},
		{
			// Both appends happen inside a callee, on the same array
			// passed twice.
			name: "two appends off one array through a call",
			src: `function grow(p: i32[], v: i32): i32[] { return p.append(v); }
			function main(): i32 {
				var a: i32[] = [];
				var i: i32 = 0;
				while (i < 5) { a = a.append(i); i = i + 1; }
				var x: i32[] = grow(a, 100);
				var y: i32[] = grow(a, 200);
				return x[5] + y[5] + a.len();
			}`,
			want: 305,
		},
		{
			// #4827's shape: an append on a parameter in argument
			// position, with the parameter read again afterwards.
			name: "param appended in argument position",
			src: `function walk(path: i32[], depth: i32): i32 {
				if (depth == 0) { return path.len(); }
				var a: i32 = walk(path.append(depth), depth - 1);
				var b: i32 = path.append(depth).len();
				return a * 100 + b;
			}
			function main(): i32 {
				var p: i32[] = [];
				return walk(p, 2);
			}`,
			want: 20201,
		},
		{
			// The self-reassign loop, which is the shape growth targets:
			// the sum must not pick up a stale or duplicated element.
			name: "self-reassign append loop",
			src: `function main(): i32 {
				var a: i32[] = [];
				var i: i32 = 0;
				while (i < 64) { a = a.append(i); i = i + 1; }
				var sum: i32 = 0;
				var j: i32 = 0;
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

// Building an n-element array costs O(n) element copies, not O(n²)
// (#6395). Counting copies rather than timing keeps the assertion
// deterministic on a shared runner: the quadratic shape copies n²/2
// elements, which is far outside every bound below.
func TestArrayAppendIsAmortised(t *testing.T) {
	build := func(n int) int {
		t.Helper()
		src := fmt.Sprintf(`function main(): i32 {
			var a: i32[] = [];
			var i: i32 = 0;
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
	// Linear growth quadruples with the input; the quadratic shape
	// would go up sixteenfold.
	if ratio := float64(largeCopies) / float64(smallCopies); ratio > 8 {
		t.Errorf("copies grew %.1fx for a 4x larger array, want at most 8x (%d then %d)",
			ratio, smallCopies, largeCopies)
	}
}
