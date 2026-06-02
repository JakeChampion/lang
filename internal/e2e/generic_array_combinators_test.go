package e2e

import "testing"

// Generic array combinators — `map` / `filter` / `fold` written as
// user-level generic functions over `T[]` — exercise the single
// highest-leverage stdlib shape (docs/STDLIB-ROADMAP.md item #1).
//
// They were blocked by a monomorph substitution-walker gap: a
// generic body that calls an Array method (`push`) on a `T[]`
// receiver stamps the method call's TypeArgs to `[T]`, and the
// clone-substitution walker failed to rewrite those to the concrete
// instantiation because `substituteExpr` had no `*ast.Assign` case
// (so `out = out.push(x)` was never walked). The re-check then
// compared the abstract `T[]` parameter against the concrete element
// type and rejected with "expected T[], got i32[]".
//
// These cases pin the runtime result across the interpreter and the
// x86-64 native backend (wasm too, when wasmtime is on PATH) so the
// fix can't silently regress on any target.
func TestGenericArrayCombinators(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "map then index-sum",
			src: `function map_arr[T, U](xs: T[], f: (T) => U): U[] {
    var out: U[] = [];
    for x in xs { out = out.push(f(x)); }
    return out;
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ys: i32[] = map_arr(xs, function (n: i32): i32 { return n * 10; });
    return ys[0] + ys[1] + ys[2];
}`,
			want: 60,
		},
		{
			name: "fold sum",
			src: `function fold_arr[T, A](xs: T[], init: A, f: (A, T) => A): A {
    var acc: A = init;
    for x in xs { acc = f(acc, x); }
    return acc;
}
function main(): i32 {
    var xs: i32[] = [4, 5, 6];
    return fold_arr(xs, 0, function (a: i32, n: i32): i32 { return a + n; });
}`,
			want: 15,
		},
		{
			name: "filter then count via len",
			src: `function filter_arr[T](xs: T[], keep: (T) => boolean): T[] {
    var out: T[] = [];
    for x in xs {
        if (keep(x)) { out = out.push(x); }
    }
    return out;
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5, 6];
    var evens: i32[] = filter_arr(xs, function (n: i32): boolean { return n % 2 == 0; });
    return evens.len() * 10 + evens[0] + evens[1] + evens[2];
}`,
			want: 42, // 3 evens (2,4,6): 3*10 + 2 + 4 + 6 (kept < 256 for the exit-code path)
		},
		{
			name: "map+fold pipeline",
			src: `function map_arr[T, U](xs: T[], f: (T) => U): U[] {
    var out: U[] = [];
    for x in xs { out = out.push(f(x)); }
    return out;
}
function fold_arr[T, A](xs: T[], init: A, f: (A, T) => A): A {
    var acc: A = init;
    for x in xs { acc = f(acc, x); }
    return acc;
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4];
    var doubled: i32[] = map_arr(xs, function (n: i32): i32 { return n + n; });
    return fold_arr(doubled, 0, function (a: i32, n: i32): i32 { return a + n; });
}`,
			want: 20, // (1+2+3+4)*2
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
