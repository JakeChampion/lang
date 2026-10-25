package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFipConstantArraysAfterFolding(t *testing.T) {
	for _, kw := range []string{"fip", "fbip"} {
		t.Run(kw, func(t *testing.T) {
			source := `const TABLE: i32[][] = [[3, 5], [7, 11]];
` + kw + ` function lookup(i: i32): i32 { return TABLE[1][i]; }
function main(): i32 { return lookup(1) - 11; }`
			prog, err := parser.Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("before constant fold: %v", err)
			}
			if err := constfold.Fold(prog, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("after constant fold: %v", err)
			}
		})
	}
}

func wantE053(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected E053 (fip allocation), got none", name)
	}
	if !strings.Contains(err.Error(), "`fip` function") {
		t.Errorf("%s: expected a fip-allocation error, got: %v", name, err)
	}
}

func wantNoErr(t *testing.T, name, src string) {
	t.Helper()
	if err := checkSource(t, src); err != nil {
		t.Errorf("%s: expected no error, got: %v", name, err)
	}
}

func TestFipAcceptsAllocationFree(t *testing.T) {
	wantNoErr(t, "inplace sort", `fip function sort_inplace(own arr: i32[]): i32[] {
    let n: i32 = arr.len();
    let k: i32 = 1;
    while (k < n) {
        let key: i32 = arr[k];
        let j: i32 = k - 1;
        while (j >= 0 && arr[j] > key) { arr = arr.with(j + 1, arr[j]); j = j - 1; }
        arr = arr.with(j + 1, key);
        k = k + 1;
    }
    return arr;
}
function main(): i32 { return 0; }`)

	wantNoErr(t, "with on own", `fip function set0(own a: i32[]): i32[] { return a.with(0, 9); }
function main(): i32 { return 0; }`)

	wantNoErr(t, "scalar", `fip function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fip calls fip", `fip function inc(x: i32): i32 { return x + 1; }
fip function twice(x: i32): i32 { return inc(inc(x)); }
function main(): i32 { return 0; }`)
}

func TestFipAdmitsNonAllocatingBuiltins(t *testing.T) {
	for _, call := range []string{
		`__memchr(s, 44, 0)`, `__memchr_bytes(set, 44, 0)`, `__rmemchr_bytes(set, 44, 0)`, `__mismatch_bytes(set, 0, set, 0, 1)`, `__mismatch_bytes_str(set, 0, s, 0, 1)`, `__rmemchr(s, 44, s.len())`, `__ascii_run(s, 0)`,
		`__count_byte(s, 44)`, `__count_byte_bytes(set, 44)`, `__crc32_cksum_array(0, set)`, `__crc32_ieee_array(0, set)`, `__sum_bytes_array(set)`, `__bsd_sum_bytes(set, 0)`, `__sum_bytes(s)`, `__scan_set(s, 0, set)`, `__scan_set_bytes(set, 0, set)`, `__count_runs_bytes(set, 0, set)`, `__bsd_sum(s, 0)`,
		`__count_runs(s, 0, set)`, `__crc32_cksum(0, s)`, `__str_hash(s, 0)`,
		`__clz32(x)`, `__ctz32(x)`, `__popcount32(x)`, `__clz64(y)`, `__ctz64(y)`, `__popcount64(y)`, `(__mulhi_u64(y, y) as i32)`,
		`__ptr_width()`, `(__heap_bump_bytes() as i32)`, `(__heap_alloc_count() as i32)`, `(monotonic_ns() as i32)`,
		`(__f64x2_lane(__f64x2_of(1.0, 2.0), 1) as i32)`, `(__f64x2_lane(__f64x2_splat(3.0), 0) as i32)`,
		`__f64x2_bitmask(__f64x2_lt(__f64x2_of(1.0, 2.0), __f64x2_splat(2.0)))`,
		`__f64x2_bitmask(__f64x2_ne(__f64x2_of(1.0, 2.0), __f64x2_splat(2.0)))`, `__f32x4_bitmask(v)`,
	} {
		for _, kw := range []string{"fip", "fbip"} {
			wantNoErr(t, kw+" "+call, kw+` function probe(s: string, set: u8[], x: u32, y: u64, v: f32x4): i32 { return `+call+`; }
function main(): i32 { return 0; }`)
		}
	}
	if len(fipNonAllocBuiltins) != 39 {
		t.Errorf("fipNonAllocBuiltins has %d entries and this test covers 39: add a row for the new one", len(fipNonAllocBuiltins))
	}
}

func TestFipRefusesAllocatingBuiltinAndShadow(t *testing.T) {
	wantE053(t, "allocating builtin", `fip function f(xs: f64[]): f64[] { return __scale_f64(xs, 2.0); }
function main(): i32 { return 0; }`)
	wantE053(t, "shadowed builtin", `function monotonic_ns(): i64 { let a: i32[] = [1]; return a.len() as i64; }
fip function f(): i32 { return monotonic_ns() as i32; }
function main(): i32 { return 0; }`)
}

func TestFipRejectsAllocation(t *testing.T) {
	wantE053(t, "array literal", `fip function f(): i32[] { return [1, 2, 3]; }
function main(): i32 { return 0; }`)

	wantE053(t, "string concat", `fip function f(a: string, b: string): string { return a + b; }
function main(): i32 { return 0; }`)

	wantE053(t, "calls non-fip", `function alloc(): i32[] { return [1]; }
fip function f(): i32 { let a: i32[] = alloc(); return a[0]; }
function main(): i32 { return 0; }`)

	wantE053(t, "with on non-own array", `fip function f(arr: i32[]): i32[] { return arr.with(0, 9); }
function main(): i32 { return 0; }`)
}

func TestFipAcceptsConstructorShape(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"struct rebuild from own", `struct P { x: i32, y: i32 }
fip function f(own p: P): P { return P { ...p, x: p.x + 1 }; }
function main(): i32 { return 0; }`},
		{"fresh struct literal", `struct P { x: i32, y: i32 }
fip function f(): P { return P { x: 1, y: 2 }; }
function main(): i32 { return 0; }`},
		{"tuple literal", `fip function f(): (i32, i32) { return (1, 2); }
function main(): i32 { return 0; }`},
		{"enum construction", `enum L { C(i32, L), N }
fip function f(t: L): L { return C(1, t); }
function main(): i32 { return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, tc.src); err != nil {
				t.Errorf("expected no E053 for a constructor shape, got: %v", err)
			}
		})
	}
}

func wantFbipE053(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected E053 (fbip shape violation), got none", name)
	}
	if !strings.Contains(err.Error(), "`fbip` function") {
		t.Errorf("%s: expected an fbip-shape error, got: %v", name, err)
	}
}

func TestFbipAcceptsConstructors(t *testing.T) {
	wantNoErr(t, "fbip struct literal", `struct P { x: i32, y: i32 }
fbip function f(a: i32): P { return P { x: a, y: a }; }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip tuple literal", `fbip function f(a: i32): (i32, i32) { return (a, a + 1); }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip enum construction", `enum L { C(i32, L), N }
fbip function f(own t: L): L { return C(1, t); }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip consuming match", `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`)

	wantNoErr(t, "graded fip(1) struct literal", `struct P { x: i32, y: i32 }
fip(1) function f(a: i32): P { return P { x: a, y: a }; }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip calls fip", `fip function inc(x: i32): i32 { return x + 1; }
fbip function f(x: i32): i32 { return inc(x); }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip calls fbip", `struct P { x: i32 }
fbip function g(a: i32): P { return P { x: a }; }
fbip function f(a: i32): P { return g(a); }
function main(): i32 { return 0; }`)
}

func TestFbipRejectsNonConstructorAllocation(t *testing.T) {
	wantFbipE053(t, "fbip array literal", `fbip function f(): i32[] { return [1, 2, 3]; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip string concat", `fbip function f(a: string, b: string): string { return a + b; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip string interpolation", `fbip function f(a: i32): string { return f"v={a}"; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip calls unmarked", `function alloc(): i32[] { return [1]; }
fbip function f(): i32 { let a: i32[] = alloc(); return a[0]; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip cow write", `struct H { v: i32 }
fbip function f(arr: i32[]): i32[] { return arr.with(0, 9); }
function main(): i32 { return 0; }`)

	wantE053(t, "fip calls fbip", `struct P { x: i32 }
fbip function g(a: i32): P { return P { x: a }; }
fip function f(a: i32): i32 { let p: P = g(a); return p.x; }
function main(): i32 { return 0; }`)

}

func TestFipWithReceiverRootWalk(t *testing.T) {
	wantNoErr(t, "with on an own struct's array field", `struct S { xs: i32[] }
fip function f(own s: S): i32[] { return s.xs.with(0, 1); }`)

	wantNoErr(t, "with on an own struct's nested array field", `struct Inner { xs: i32[] }
struct S { inner: Inner }
fip function f(own s: S): i32[] { return s.inner.xs.with(0, 1); }`)

	wantNoErr(t, "fbip struct update writing its own array field", `struct S { xs: i32[], n: i32 }
fbip function f(own s: S): S { return S { ...s, xs: s.xs.with(0, 1), n: s.n + 1 }; }`)

	wantE053(t, "with on a non-own struct's array field", `struct S { xs: i32[] }
fip function f(s: S): i32[] { return s.xs.with(0, 1); }`)

	wantNoErr(t, "chained with on an own array",
		`fip function f(own b: i32[]): i32[] { return b.with(0, 1).with(1, 2); }`)

	wantNoErr(t, "chained with on an own array in assignment position",
		`fip function f(own b: i32[]): i32[] { b = b.with(0, 1).with(1, 2); return b; }`)

	wantNoErr(t, "chained with on an own struct's array field", `struct S { xs: i32[] }
fip function f(own s: S): i32[] { return s.xs.with(0, 1).with(1, 2); }`)

	wantE053(t, "chained with whose outer link reads the receiver",
		`fip function f(own b: i32[]): i32[] { return b.with(0, 1).with(1, b[0]); }`)

	wantE053(t, "chained with on a borrowed array",
		`fip function f(b: i32[]): i32[] { return b.with(0, 1).with(1, 2); }`)

	wantE053(t, "chain rooted at a non-with call", `fip function pick(own a: i32[]): i32[] { return a; }
fip function f(own b: i32[]): i32[] { return pick(b).with(0, 1); }`)
}

func TestFipMapOnOwnReceiver(t *testing.T) {
	const decl = "function (xs: i64[]) map(f: (i64) => i64): i64[] { return xs; }\n"
	wantNoErr(t, "map on an own array", decl+
		`fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }`)
	wantNoErr(t, "fbip map on an own array", decl+
		`fbip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }`)
	wantE053(t, "map whose element function calls outside the claim", decl+
		`function g(x: i64): i64 { return x; }
fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => g(x)); }`)
	wantE053(t, "map on a borrowed array", decl+
		`fip function run(xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }`)
	wantE053(t, "chained map on an own array", decl+
		`fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x).map((x: i64): i64 => x); }`)
}

func TestFipAdmitsSubstitutedConstArrays(t *testing.T) {
	for _, claim := range []string{"fip", "fbip"} {
		t.Run(claim, func(t *testing.T) {
			prog, err := parser.Parse(`const STOPS: f64[] = [0.0, 0.16, 0.42, 1.0];
const FLOATS: f32[] = [0.1f32, 0.1f32 + 0.2f32];
const BYTES: u8[] = [1u8, 2u8];
` + claim + ` function pick(i: i32): f64 { return STOPS[i] + FLOATS[i % 2] as f64 + BYTES[i % 2] as f64; }
function main(): i32 { return 0; }`)
			if err != nil {
				t.Fatal(err)
			}
			if err := constfold.Fold(prog, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("static table read after substitution: %v", err)
			}
		})
	}
}
