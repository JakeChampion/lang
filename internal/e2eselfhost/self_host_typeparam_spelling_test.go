package e2eselfhost

import "testing"

// typeParamSpellingCases name a user type exactly like a generic's type
// parameter (#9577). The self-host monomorphiser reads types as spellings, so
// `PMap[K, V]` taken at a user `struct K` produced a clone indistinguishable
// from its template: the instantiations never registered and the build
// stopped on calls to the bare templates. Every case's expected value is what
// the native compiler's build of the same program exits with.
var typeParamSpellingCases = []struct {
	name string
	src  string
	want int
}{
	// The issue's program: a struct `K` as the key of std/pmap's `PMap[K, V]`.
	{"struct-named-like-map-key-param", `import "std/pmap" as pmap;
import "std/option";
import "std/i32";
import "core/cmp";

@derive(cmp.Eq)
struct K { bucket: i32, id: i32 }
impl cmp.Hash for K { function hash(self: K): i32 { return self.bucket; } }
function main(): i32 {
    var m: pmap.PMap[K, i32] = pmap.pmap_new();
    var i: i32 = 0;
    while (i < 40) { m = m.insert(K { bucket: i % 5, id: i }, i); i = i + 1; }
    var n: i32 = m.get_or(K { bucket: 3, id: 13 }, -1) * 10 + m.get_or(K { bucket: 3, id: 14 }, -1);
    if (m.contains(K { bucket: 1, id: 6 })) { n = n + 1000; }
    return n % 113;
}
`, 112},
	// An enum `V` as the value of `PMap[K, V]`, read back through `get`'s
	// `Option[V]`.
	{"enum-named-like-map-value-param", `import "std/pmap" as pmap;
import "std/option";
import "std/i32";

enum V { Small(i32), Big(i32) }
function weight(v: V): i32 { match (v) { Small(n) => { return n; }, Big(n) => { return n * 100; } } return 0; }
function main(): i32 {
    var m: pmap.PMap[i32, V] = pmap.pmap_new();
    var i: i32 = 0;
    while (i < 30) {
        if (i % 3 == 0) { m = m.insert(i, Big(i)); } else { m = m.insert(i, Small(i)); }
        i = i + 1;
    }
    var n: i32 = 0;
    match (m.get(9)) { Some(v) => { n = n + weight(v); }, None => { n = n + 1; } }
    match (m.get(10)) { Some(v) => { n = n + weight(v); }, None => { n = n + 1; } }
    match (m.get(99)) { Some(v) => { n = n + weight(v); }, None => { n = n + 7; } }
    return (n + m.len()) % 113;
}
`, 43},
	// A struct `T` beside generics whose own parameter is `T`: an erased
	// function (`pick`), bounded ones that are cloned (`first_eq`, `count_eq`),
	// and a generic struct `Box[T]` taken at the struct.
	{"struct-named-like-fn-param", `import "core/cmp";

@derive(cmp.Eq)
struct T { a: i32, b: i32 }
struct Box[T] { v: T, n: i32 }
function (b: Box[T]) get(): T { return b.v; }
function wrap[T](x: T): Box[T] { return Box { v: x, n: 1 }; }
function pick[T](xs: T[], i: i32): T { return xs[i]; }
function first_eq[T: cmp.Eq](xs: T[], x: T): Option[T] {
    for y in xs { if (y.eq(x)) { return Some(y); } }
    return None;
}
function count_eq[T: cmp.Eq](xs: T[], x: T): i32 {
    var n: i32 = 0;
    for y in xs { if (y.eq(x)) { n = n + 1; } }
    return n;
}
function main(): i32 {
    var ts: T[] = [T { a: 1, b: 2 }, T { a: 3, b: 4 }, T { a: 1, b: 2 }];
    var t: T = pick(ts, 1);
    var bx: Box[T] = wrap(T { a: 5, b: 6 });
    var names: string[] = ["x", "yy", "zzz"];
    var f: i32 = 0;
    match (first_eq(ts, T { a: 3, b: 4 })) { Some(u) => { f = u.b; }, None => { f = 9; } }
    return (t.a * 10 + t.b + count_eq(ts, T { a: 1, b: 2 }) * 100 + pick(names, 2).len() + bx.get().a + f) % 113;
}
`, 20},
}

// TestSelfHostTypeParamSpelling compiles each case with the self-host CLI for
// every target, under both the default lowering and `FERN_SEM_IR=`.
func TestSelfHostTypeParamSpelling(t *testing.T) {
	cli := buildSelfHostCLI(t)
	lowerings := []struct {
		name string
		env  []string
	}{{"default", nil}, {"ast", []string{"FERN_SEM_IR="}}}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi", "arm64-linux"} {
		for _, lw := range lowerings {
			for _, tc := range typeParamSpellingCases {
				t.Run(target+"/"+lw.name+"/"+tc.name, func(t *testing.T) {
					if stderr, code := cli.exitOf(t, tc.src, target, lw.env...); code != tc.want {
						t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
					}
				})
			}
		}
	}
}
