package e2ecompiler

import "testing"

// typeParamSpellingCases name a user type exactly like a generic's type
// parameter (#9577). The self-host monomorphiser reads types as spellings, so
// `PMap[K, V]` taken at a user `struct K` must still produce a clone distinct
// from its template, or the instantiations never register and the build stops
// on calls to the bare templates. Every case's expected value comes from an
// independent build of the same program, never from the self-host run.
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
    let m: pmap.PMap[K, i32] = pmap.pmap_new();
    let i: i32 = 0;
    while (i < 40) { m = m.insert(K { bucket: i % 5, id: i }, i); i = i + 1; }
    let n: i32 = m.get_or(K { bucket: 3, id: 13 }, -1) * 10 + m.get_or(K { bucket: 3, id: 14 }, -1);
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
    let m: pmap.PMap[i32, V] = pmap.pmap_new();
    let i: i32 = 0;
    while (i < 30) {
        if (i % 3 == 0) { m = m.insert(i, Big(i)); } else { m = m.insert(i, Small(i)); }
        i = i + 1;
    }
    let n: i32 = 0;
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
    let n: i32 = 0;
    for y in xs { if (y.eq(x)) { n = n + 1; } }
    return n;
}
function main(): i32 {
    let ts: T[] = [T { a: 1, b: 2 }, T { a: 3, b: 4 }, T { a: 1, b: 2 }];
    let t: T = pick(ts, 1);
    let bx: Box[T] = wrap(T { a: 5, b: 6 });
    let names: string[] = ["x", "yy", "zzz"];
    let f: i32 = 0;
    match (first_eq(ts, T { a: 3, b: 4 })) { Some(u) => { f = u.b; }, None => { f = 9; } }
    return (t.a * 10 + t.b + count_eq(ts, T { a: 1, b: 2 }) * 100 + pick(names, 2).len() + bx.get().a + f) % 113;
}
`, 20},
	// A value named like a type variable is a value: the respelling of `T` in
	// `T.default()` must not reach a parameter, a local or a bounded clone's
	// parameter of the same name.
	{"param-named-like-type-param", `function pick[T](T: T[]): i32 { return T.len(); }
function main(): i32 { return pick([4, 5, 6]) + pick(["a"]) * 10; }
`, 13},
	{"local-named-like-type-param", `function first[T](xs: T[]): T { let T: T = xs[0]; return T; }
function count[T](xs: T[]): i32 { let T: i32 = xs.len(); return T; }
function main(): i32 { return first([7, 8]) + count(["a", "b", "c"]) * 10; }
`, 37},
	{"bounded-param-named-like-type-param", `import "core/cmp";
function pick3[T: cmp.Eq](T: T): T { return T; }
function main(): i32 { return pick3(40) + pick3("abc").len(); }
`, 43},
	// `f[T](x)` parses as an index until the type argument is recognised, so
	// its bracket is respelled like any type variable — but an index that is a
	// value of the same name stays a value.
	{"type-param-in-call-bracket-and-value-index", `function at[T](xs: T[], T: i32): T { return xs[T]; }
function id[T](a: T): T { return a; }
function pass[T](a: T): T { return id[T](a); }
function main(): i32 { return at([10, 20, 30], 2) + pass(5); }
`, 35},
	// A MODULE-level name is not a type variable either, in the index and
	// the field-access positions alike: a const read as an index, an enum
	// qualifying a variant, a struct qualifying an associated call (#10427).
	{"module-const-shadows-type-param-index", `const T: i32 = 2;
function at[T](xs: T[]): T { return xs[T]; }
function main(): i32 { return at([5, 6, 7, 8]); }
`, 7},
	{"module-enum-shadows-type-param-qualifier", `enum T { Red, Blue }
function pick[T](n: T): i32 {
    match (T.Red) { Red => { return 1; }, Blue => { return 2; } }
    return 0;
}
function main(): i32 { return pick(1); }
`, 1},
	{"module-struct-shadows-type-param-assoc-call", `struct T { x: i32 }
impl T { function get(): i32 { return 1; } }
function pick[T](n: T): i32 { return T.get(); }
function main(): i32 { return pick(1); }
`, 1},
	// Out of the value's scope, `T.default()` names the type variable again.
	{"type-param-object-after-shadow-scope", `import "core/cmp";
function mk[T: cmp.Default](x: T): T {
    if (true) { let T: i32 = 3; }
    for T in [1, 2] { }
    return T.default();
}
function main(): i32 { return mk(7) + mk("zz").len() + 4; }
`, 4},
}

// TestSelfHostTypeParamSpelling compiles each case with the self-host CLI for
// every target.
func TestSelfHostTypeParamSpelling(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi", "arm64-linux"} {
		for _, tc := range typeParamSpellingCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
