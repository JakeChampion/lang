package interp

import (
	"fmt"
	"testing"
)

// withAliasingCases are the shapes `arr.with(i, v)` has to keep apart: in
// each one a second value covers the receiver's buffer, so a write in
// place that skipped the owner count would be visible through it. Every
// expected value is the one the compiled backends produce (probed with
// `fern -target x86-64-linux --run`), so this is the oracle's contract
// and not just the interpreter agreeing with itself. #7287.
var withAliasingCases = []struct {
	name string
	src  string
	want Number
}{
	{
		name: "alias through a second local",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var b: i32[] = a;
			a = a.with(0, 9);
			return b[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		// The receiver stays live after the call, so the result has to
		// be a copy even though only one binding holds the buffer.
		name: "receiver still live after the call",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var b: i32[] = a.with(0, 9);
			return a[0] * 10 + b[0];
		}`,
		want: 19,
	},
	{
		name: "alias through an array element",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var rows: i32[][] = [a];
			a = a.with(0, 9);
			return rows[0][0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "one array twice in a container",
		src: `function main(): i32 {
			var inner: i32[] = [1, 2, 3];
			var rows: i32[][] = [inner, inner];
			inner = inner.with(0, 9);
			return rows[0][0] * 100 + rows[1][0] * 10 + inner[0];
		}`,
		want: 119,
	},
	{
		name: "alias through a struct field",
		src: `struct Box { items: i32[] }
		function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var b: Box = Box { items: a };
			a = a.with(0, 9);
			return b.items[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		// The map's in-place insert is the store the assignment cannot
		// account for: it hands back the same *Map, so the assignment's
		// retain and release cancel.
		name: "alias through a map value",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var m: Map[i32, i32[]] = map_new(8);
			m = m.insert(1, a);
			a = a.with(0, 9);
			var got: i32[] = m.get_or(1, []);
			return got[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		name: "alias through an enum payload",
		src: `enum Holder { Wrap(i32[]), Empty }
		function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var h: Holder = Holder.Wrap(a);
			a = a.with(0, 9);
			var seen: i32 = 0;
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
			var a: i32[] = [1, 2, 3];
			var t: (i32[], i32) = (a, 5);
			a = a.with(0, 9);
			return t.0[0] * 10 + a[0];
		}`,
		want: 19,
	},
	{
		// The parameter is a second owner while the caller's binding is
		// still live, so a write through it must not reach the caller.
		name: "write through a parameter",
		src: `function f(p: i32[]): i32 {
			p = p.with(0, 9);
			return p[0];
		}
		function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var r: i32 = f(a);
			return a[0] * 10 + r;
		}`,
		want: 19,
	},
	{
		name: "write through a closure parameter",
		src: `function main(): i32 {
			var f: (i32[]) => i32 = function(p: i32[]): i32 { p = p.with(0, 9); return p[0]; };
			var a: i32[] = [1, 2, 3];
			var r: i32 = f(a);
			return a[0] * 10 + r;
		}`,
		want: 19,
	},
	{
		name: "captured by a closure",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var g: () => i32 = function(): i32 { return a[0]; };
			var b: i32[] = a;
			a = a.with(0, 9);
			return b[0] * 10 + g();
		}`,
		want: 19,
	},
	{
		name: "alias of a returned array",
		src: `function make3(): i32[] {
			var a: i32[] = [1, 2, 3];
			return a;
		}
		function main(): i32 {
			var x: i32[] = make3();
			var y: i32[] = x;
			x = x.with(0, 9);
			return y[0] * 10 + x[0];
		}`,
		want: 19,
	},
	{
		name: "two withs off one array",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var x: i32[] = a.with(0, 7);
			var y: i32[] = a.with(0, 8);
			return x[0] * 100 + y[0] * 10 + a[0];
		}`,
		want: 781,
	},
	{
		// An append still covers the receiver's slots, so a later write
		// through the shorter view must not reach the longer one.
		name: "with after an append off the same buffer",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var b: i32[] = a.append(4);
			a = a.with(0, 9);
			return b[0] * 100 + b[3] * 10 + a[0];
		}`,
		want: 149,
	},
	{
		// A borrowed view covers the owner's buffer, so the owner's
		// write must not reach it.
		name: "slice view of the receiver",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3, 4];
			var s: [i32] = a[1:3];
			a = a.with(1, 9);
			return s[0] * 10 + a[1];
		}`,
		want: 29,
	},
	{
		// The alias dies with the block, so the write after it is
		// unshared again.
		name: "alias then scope exit",
		src: `function main(): i32 {
			var a: i32[] = [1, 2, 3];
			if (true) {
				var b: i32[] = a;
				a = a.with(0, 7);
				if (b[0] != 1) { return 90; }
			}
			a = a.with(1, 8);
			return a[0] * 10 + a[1];
		}`,
		want: 78,
	},
	{
		// An argument already evaluated is a live reference while a
		// later argument runs statements that reassign the binding.
		name: "argument evaluated before a block that reassigns it",
		src: `function h(p: i32[], q: i32): i32 { return p[0] * 10 + q; }
		function main(): i32 {
			var a: i32[] = [1, 2, 3];
			var r: i32 = h(a, { a = a.with(0, 9); 1 });
			return r * 10 + a[0];
		}`,
		want: 119,
	},
	{
		name: "self-reassign loop reads back what it wrote",
		src: `function main(): i32 {
			var a: i32[] = [];
			var i: i32 = 0;
			while (i < 32) { a = a.append(0); i = i + 1; }
			var j: i32 = 0;
			while (j < 32) { a = a.with(j, j * 2); j = j + 1; }
			var sum: i32 = 0;
			var k: i32 = 0;
			while (k < 32) { sum = sum + a[k]; k = k + 1; }
			return sum;
		}`,
		want: 992,
	},
	{
		// An in-place write hands the Map-rc paths the array carries from
		// the element it replaces to the one it stores. Without that the
		// stored map looks unshared and its next insert mutates the copy
		// the array holds.
		name: "map stored by an in-place element write",
		src: `function main(): i32 {
			var a: Map[i32, i32][] = [map_new(8), map_new(8)];
			var m: Map[i32, i32] = map_new(8);
			m = m.insert(1, 5);
			a = a.with(0, m);
			m = m.insert(1, 9);
			return a[0].get_or(1, -1) * 10 + m.get_or(1, -1);
		}`,
		want: 59,
	},
	{
		// The caller's binding is dead at the call, so the write inside
		// the callee lands in the caller's buffer — invisibly.
		name: "reassign through a callee",
		src: `function setz(w: i32[], i: i32, v: i32): i32[] { w = w.with(i, v); return w; }
		function main(): i32 {
			var a: i32[] = [1, 2, 3];
			a = setz(a, 0, 9);
			return a[0] * 10 + a[2];
		}`,
		want: 93,
	},
}

// The oracle must give the same answer whether or not the in-place write
// fired, so every case runs under all three FERN_INTERP_ARRAY_COW modes:
// the counted default, `copy` (never in place — the cross-check
// baseline), and `verify` (in place, with a live-scope scan that refuses
// the write on an under-count).
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

// Writing every element of an n-element array costs O(n) element copies,
// not O(n²) (#7287). Counting copies rather than timing keeps the
// assertion deterministic on a shared runner: the quadratic shape copies
// n² elements, which is far outside every bound below.
func TestArrayWithIsLinear(t *testing.T) {
	fill := func(n int) int {
		t.Helper()
		src := fmt.Sprintf(`function main(): i32 {
			var a: i32[] = [];
			var i: i32 = 0;
			while (i < %d) { a = a.append(0); i = i + 1; }
			var j: i32 = 0;
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

// `copy` mode is only a useful cross-check if it really disables the
// in-place write, and the default mode is only useful if it really takes
// it. Assert both, so neither can quietly become the other.
func TestArrayWithModesDiffer(t *testing.T) {
	src := `function main(): i32 {
		var a: i32[] = [];
		var i: i32 = 0;
		while (i < 64) { a = a.append(0); i = i + 1; }
		var j: i32 = 0;
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
