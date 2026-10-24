package e2e

import "testing"

func TestGenericArrayCombinators(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "map then index-sum",
			src: `function map_arr[T, U](xs: T[], f: (T) => U): U[] {
    let out: U[] = [];
    for x in xs { out = out.append(f(x)); }
    return out;
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ys: i32[] = map_arr(xs, (n: i32): i32 => { return n * 10; });
    return ys[0] + ys[1] + ys[2];
}`,
			want: 60,
		},
		{
			name: "fold sum",
			src: `function fold_arr[T, A](xs: T[], init: A, f: (A, T) => A): A {
    let acc: A = init;
    for x in xs { acc = f(acc, x); }
    return acc;
}
function main(): i32 {
    let xs: i32[] = [4, 5, 6];
    return fold_arr(xs, 0, (a: i32, n: i32): i32 => { return a + n; });
}`,
			want: 15,
		},
		{
			name: "fold seeded with an empty array",
			src: `function fold_arr[T, A](xs: T[], init: A, f: (A, T) => A): A {
    let acc: A = init;
    for x in xs { acc = f(acc, x); }
    return acc;
}
function main(): i32 {
    let xs: i32[] = [4, 5, 6];
    let ys: i32[] = fold_arr(xs, [], (a: i32[], n: i32): i32[] => { return a.append(n * 2); });
    return ys.len() * 10 + ys[2];
}`,
			want: 42,
		},
		{
			name: "empty array at an element-typed parameter",
			src: `function zip[T](a: T[], b: T[]): T[] {
    let out: T[] = a;
    for x in b { out = out.append(x); }
    return out;
}
function main(): i32 { return zip([], [1, 2, 3]).len() + zip([1, 2], []).len(); }`,
			want: 5,
		},
		{
			name: "empty array at a view parameter",
			src: `function head2[T](xs: [T], ys: T[]): i32 { return xs.len() + ys.len(); }
function main(): i32 { return head2([], [7, 8]); }`,
			want: 2,
		},
		{
			name: "filter then count via len",
			src: `function filter_arr[T](xs: T[], keep: (T) => boolean): T[] {
    let out: T[] = [];
    for x in xs {
        if (keep(x)) { out = out.append(x); }
    }
    return out;
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4, 5, 6];
    let evens: i32[] = filter_arr(xs, (n: i32): boolean => { return n % 2 == 0; });
    return evens.len() * 10 + evens[0] + evens[1] + evens[2];
}`,
			want: 42,
		},
		{
			name: "map+fold pipeline",
			src: `function map_arr[T, U](xs: T[], f: (T) => U): U[] {
    let out: U[] = [];
    for x in xs { out = out.append(f(x)); }
    return out;
}
function fold_arr[T, A](xs: T[], init: A, f: (A, T) => A): A {
    let acc: A = init;
    for x in xs { acc = f(acc, x); }
    return acc;
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4];
    let doubled: i32[] = map_arr(xs, (n: i32): i32 => { return n + n; });
    return fold_arr(doubled, 0, (a: i32, n: i32): i32 => { return a + n; });
}`,
			want: 20,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpByte(t, c.src); got != c.want {
				t.Errorf("interp: got exit %d, want %d", got, c.want)
			}
			if _, got := compileAndRunX86_64(t, c.src); got != c.want {
				t.Errorf("x86-64 native: got exit %d, want %d", got, c.want)
			}
			if got := compileAndRunWasmbinMain(t, c.src); got != c.want {
				t.Errorf("wasm: got exit %d, want %d", got, c.want)
			}
		})
	}
}

func TestStdArrayCombinators(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "map+filter+fold qualified",
			src: `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4, 5, 6];
    let doubled: i32[] = array.map(xs, (n: i32): i32 => { return n * 2; });
    let evens: i32[] = array.filter(doubled, (n: i32): boolean => { return n % 4 == 0; });
    return array.fold(evens, 0, (a: i32, n: i32): i32 => { return a + n; });
}`,
			want: 24,
		},
		{
			name: "any+all qualified",
			src: `import "std/array";
function main(): i32 {
    let xs: i32[] = [2, 4, 6, 8];
    let r: i32 = 0;
    if (array.all(xs, (n: i32): boolean => { return n % 2 == 0; })) { r = r + 10; }
    if (array.any(xs, (n: i32): boolean => { return n == 6; })) { r = r + 5; }
    if (array.any(xs, (n: i32): boolean => { return n == 7; })) { r = r + 1; }
    return r;
}`,
			want: 15,
		},
		{
			name: "find Option arms qualified",
			src: `import "std/array";
function main(): i32 {
    let xs: i32[] = [5, 10, 15, 20];
    let hit: i32 = 0;
    match (array.find(xs, (n: i32): boolean => { return n > 12; })) {
        Some(v) => { hit = v; },
        None => { hit = 0 - 1; }
    }
    let miss: i32 = 0;
    match (array.find(xs, (n: i32): boolean => { return n > 99; })) {
        Some(v) => { miss = v; },
        None => { miss = 1; }
    }
    return hit + miss; // 15 + 1
}`,
			want: 16,
		},
		{
			name: "enumerate tuple-in-array qualified",
			src: `import "std/array";
function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let pairs: (i32, i32)[] = array.enumerate(xs);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < pairs.len()) {
        let p: (i32, i32) = pairs[i];
        acc = acc + p.0 + p.1;
        i = i + 1;
    }
    return acc; // (0+10)+(1+20)+(2+30) = 63
}`,
			want: 63,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpByte(t, c.src); got != c.want {
				t.Errorf("interp: got exit %d, want %d", got, c.want)
			}
			if _, got := compileAndRunX86_64(t, c.src); got != c.want {
				t.Errorf("x86-64 native: got exit %d, want %d", got, c.want)
			}
			if got := compileAndRunWasmbinMain(t, c.src); got != c.want {
				t.Errorf("wasm: got exit %d, want %d", got, c.want)
			}
		})
	}
}
