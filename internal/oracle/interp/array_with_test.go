package interp

import (
	"fmt"
	"testing"
)

var withAliasingCases = []struct {
	name string
	src  string
	want Number
}{
	{
		name: "alias through a second local",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let b: i32[] = a;
			a = a.with(0, 9);
			return b[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "receiver still live after the call",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let b: i32[] = a.with(0, 9);
			return a[0] * 10 + b[0];
		}`,
		want: 19,
	},
	{
		name: "alias through an array element",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let rows: i32[][] = [a];
			a = a.with(0, 9);
			return rows[0][0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "one array twice in a container",
		src: `function main(): i32 {
			let inner: i32[] = [1, 2, 3];
			let rows: i32[][] = [inner, inner];
			inner = inner.with(0, 9);
			return rows[0][0] * 100 + rows[1][0] * 10 + inner[0];
		}`,
		want: 119,
	},
	{
		name: "alias through a struct field",
		src: `struct Box { items: i32[] }
		function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let b: Box = Box { items: a };
			a = a.with(0, 9);
			return b.items[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "alias through a map value",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let m: Map[i32, i32[]] = map_new(8);
			m = m.insert(1, a);
			a = a.with(0, 9);
			let got: i32[] = m.get_or(1, []);
			return got[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "alias through an enum payload",
		src: `enum Holder { Wrap(i32[]), Empty }
		function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let h: Holder = Holder.Wrap(a);
			a = a.with(0, 9);
			let seen: i32 = 0;
			match (h) {
				Wrap(inner) => { seen = inner[0]; },
				Empty => { seen = -1; },
			}
			return seen * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "alias through a tuple element",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let t: (i32[], i32) = (a, 5);
			a = a.with(0, 9);
			return t.0[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "write through a parameter",
		src: `function f(p: i32[]): i32 {
			p = p.with(0, 9);
			return p[0];
		}
		function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let r: i32 = f(a);
			return a[0] * 10 + r;
		}`,
		want: 19,
	},
	{
		name: "write through a closure parameter",
		src: `function main(): i32 {
			let f: (i32[]) => i32 = (p: i32[]): i32 => { p = p.with(0, 9); return p[0]; };
			let a: i32[] = [1, 2, 3];
			let r: i32 = f(a);
			return a[0] * 10 + r;
		}`,
		want: 19,
	},
	{
		name: "captured by a closure",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let g: () => i32 = (): i32 => { return a[0]; };
			let b: i32[] = a;
			a = a.with(0, 9);
			return b[0] * 10 + g();
		}`,
		want: 19,
	},
	{
		name: "alias of a returned array",
		src: `function make3(): i32[] {
			let a: i32[] = [1, 2, 3];
			return a;
		}
		function main(): i32 {
			let x: i32[] = make3();
			let y: i32[] = x;
			x = x.with(0, 9);
			return y[0] * 10 + x[0];
		}`,
		want: 19,
	},
	{
		name: "two withs off one array",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let x: i32[] = a.with(0, 7);
			let y: i32[] = a.with(0, 8);
			return x[0] * 100 + y[0] * 10 + a[0];
		}`,
		want: 781,
	},
	{
		name: "with after an append off the same buffer",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let b: i32[] = a.append(4);
			a = a.with(0, 9);
			return b[0] * 100 + b[3] * 10 + a[0];
		}`,
		want: 149,
	},
	{
		name: "slice view of the receiver",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3, 4];
			let s: [i32] = a[1:3];
			a = a.with(1, 9);
			return s[0] * 10 + a[1];
		}`,
		want: 29,
	},
	{
		name: "alias then scope exit",
		src: `function main(): i32 {
			let a: i32[] = [1, 2, 3];
			if (true) {
				let b: i32[] = a;
				a = a.with(0, 7);
				if (b[0] != 1) { return 90; }
			}
			a = a.with(1, 8);
			return a[0] * 10 + a[1];
		}`,
		want: 78,
	},
	{
		name: "argument evaluated before a block that reassigns it",
		src: `function h(p: i32[], q: i32): i32 { return p[0] * 10 + q; }
		function main(): i32 {
			let a: i32[] = [1, 2, 3];
			let r: i32 = h(a, { a = a.with(0, 9); 1 });
			return r * 10 + a[0];
		}`,
		want: 119,
	},
	{
		name: "self-reassign loop reads back what it wrote",
		src: `function main(): i32 {
			let a: i32[] = [];
			let i: i32 = 0;
			while (i < 32) { a = a.append(0); i = i + 1; }
			let j: i32 = 0;
			while (j < 32) { a = a.with(j, j * 2); j = j + 1; }
			let sum: i32 = 0;
			let k: i32 = 0;
			while (k < 32) { sum = sum + a[k]; k = k + 1; }
			return sum;
		}`,
		want: 992,
	},
	{
		name: "map stored by an in-place element write",
		src: `function main(): i32 {
			let a: Map[i32, i32][] = [map_new(8), map_new(8)];
			let m: Map[i32, i32] = map_new(8);
			m = m.insert(1, 5);
			a = a.with(0, m);
			m = m.insert(1, 9);
			return a[0].get_or(1, -1) * 10 + m.get_or(1, -1);
		}`,
		want: 59,
	},
	{
		name: "reassign through a callee",
		src: `function setz(w: i32[], i: i32, v: i32): i32[] { w = w.with(i, v); return w; }
		function main(): i32 {
			let a: i32[] = [1, 2, 3];
			a = setz(a, 0, 9);
			return a[0] * 10 + a[2];
		}`,
		want: 93,
	},
}

func TestArrayWithValueSemantics(t *testing.T) {
	for _, mode := range []string{"", "copy", "verify"} {
		name := mode
		if name == "" {
			name = "counted"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("FERN_INTERP_ARRAY_COW", mode)
			for _, tc := range withAliasingCases {
				t.Run(tc.name, func(t *testing.T) {
					if got := evalProgramValue(t, tc.src); got != tc.want {
						t.Errorf("got %v, want %v", got, tc.want)
					}
				})
			}
		})
	}
}

func TestArrayWithIsLinear(t *testing.T) {
	fill := func(n int) int {
		t.Helper()
		src := fmt.Sprintf(`function main(): i32 {
			let a: i32[] = [];
			let i: i32 = 0;
			while (i < %d) { a = a.append(0); i = i + 1; }
			let j: i32 = 0;
			while (j < %d) { a = a.with(j, j); j = j + 1; }
			return a[%d - 1];
		}`, n, n, n)
		v, ip := evalProgram(t, src)
		if v != Number(n-1) {
			t.Fatalf("last element %v, want %d", v, n-1)
		}
		return ip.arraySetCopies
	}
	const small, large = 1000, 4000
	smallCopies, largeCopies := fill(small), fill(large)
	if smallCopies > small {
		t.Errorf("%d writes copied %d elements, want at most %d", small, smallCopies, small)
	}
	if largeCopies > large {
		t.Errorf("%d writes copied %d elements, want at most %d", large, largeCopies, large)
	}
}

func TestArrayWithModesDiffer(t *testing.T) {
	src := `function main(): i32 {
		let a: i32[] = [];
		let i: i32 = 0;
		while (i < 64) { a = a.append(0); i = i + 1; }
		let j: i32 = 0;
		while (j < 64) { a = a.with(j, j); j = j + 1; }
		return a[63];
	}`
	t.Run("counted writes in place", func(t *testing.T) {
		t.Setenv("FERN_INTERP_ARRAY_COW", "")
		if _, ip := evalProgram(t, src); ip.arraySetCopies != 0 {
			t.Errorf("copied %d elements, want 0", ip.arraySetCopies)
		}
	})
	t.Run("copy mode always copies", func(t *testing.T) {
		t.Setenv("FERN_INTERP_ARRAY_COW", "copy")
		if _, ip := evalProgram(t, src); ip.arraySetCopies != 64*64 {
			t.Errorf("copied %d elements, want %d", ip.arraySetCopies, 64*64)
		}
	})
}

func TestArrayWithChainOuterReadSeesTheOldValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want Number
	}{
		{"array", `function main(): i32 {
			let b: i32[] = [7, 8, 9, 10];
			b = b.with(0, 3).with(1, b[0]);
			return b[0] * 10 + b[1];
		}`, 37},
		{"map", `import "core/map";
		function main(): i32 {
			let m: Map[i32, i32] = map_new(4);
			m = m.insert(1, 5);
			m = m.insert(1, 3).insert(2, m.get_or(1, 0));
			return m.get_or(1, 0) * 10 + m.get_or(2, 0);
		}`, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evalProgramValue(t, tc.src)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
