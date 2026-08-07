package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// core/iter's ArrayIter (`iter.of`) makes the whole combinator library usable
// on a plain array — and, via a map's `.keys()` / `.values()` snapshot, on map
// keys and values. It is the stdlib's first PARAMETRIC impl of a GENERIC trait
// (`impl[T] Iterator[T] for ArrayIter[T]`), which fails two ways if unguarded:
// the checker's conformance compare splits a substituted `StructType("T")` from
// the hoisted method's `ParamType("T")` (identical when printed), and the
// monomorphiser never cloned a parametric-impl method reached ONLY through a
// trait bound (`it.next()` inside `sum[I: Iterator[i32]]`), since no direct
// concrete call site exists. Both are fixed; these pin the behaviour on the
// native backends (interp / x86-64 / wasm).
var arrayIterCases = []struct {
	name string
	main string
	want int
}{
	// sum over an array: iter.of(xs) yields 10,20,12 → 42.
	{"sum-array", `import "core/iter";
function main(): i32 { var xs: i32[] = [10, 20, 12]; return iter.sum(iter.of(xs)); }`, 42},
	// count over an array → 5.
	{"count-array", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 1, 1, 1, 1]; return iter.count(iter.of(xs)); }`, 5},
	// filter (closure) then count: evens of 1..6 → 3.
	{"filter-count", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5, 6]; var e: i32[] = iter.filter(iter.of(xs), function (n: i32): boolean { return n % 2 == 0; }); return iter.count(iter.of(e)); }`, 3},
	// map (closure) then sum: squares of 1..4 → 1+4+9+16 = 30.
	{"map-sum", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; var sq: i32[] = iter.map(iter.of(xs), function (n: i32): i32 { return n * n; }); return iter.sum(iter.of(sq)); }`, 30},
	// over a map's keys snapshot: sum of keys 10+20+12 → 42.
	{"sum-map-keys", `import "core/iter";
import "core/map";
function main(): i32 { var m: Map[i32, i32] = Map { 10: 1, 20: 2, 12: 3 }; return iter.sum(iter.of(m.keys())); }`, 42},
	// over a map's values snapshot: sum of values 1+2+3 → 6.
	{"sum-map-values", `import "core/iter";
import "core/map";
function main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 12 }; return iter.sum(iter.of(m.values())); }`, 42},
}

func TestArrayIterCombinators(t *testing.T) {
	for _, tc := range arrayIterCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// A standalone parametric impl of a generic trait (no stdlib): a single
// `impl[T] Iterator[T] for One[T]` is consumed by a fully-generic collector at
// TWO element types (i32 + string) from one impl block. This pins the checker
// conformance fix (generic trait arg = impl type param) AND the monomorphiser
// cloning a bound-only-reached parametric method per element type, independent
// of core/iter.
const parametricGenericTraitProg = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct One[T] { v: T, done: boolean }
impl[T] Iterator[T] for One[T] {
    function next(self: Self): Option[(T, Self)] {
        if (self.done) { return None; }
        return Some((self.v, One { v: self.v, done: true }));
    }
}
function count[T, I: Iterator[T]](it: I): i32 {
    var n = 0; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { n = n + 1; cur = t.1; }, None => { go = false; }, } }
    return n;
}
function main(): i32 { return count(One { v: 7, done: false }) + count(One { v: "x", done: false }); }`

func TestParametricImplOfGenericTrait(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(parametricGenericTraitProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 2 {
		t.Errorf("parametric-generic-trait interp = %d, want 2", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 2 {
		t.Errorf("parametric-generic-trait x86-64 = %d, want 2", code)
	}
	if code := runWasm(t, parametricGenericTraitProg); code != 2 {
		t.Errorf("parametric-generic-trait wasm = %d, want 2", code)
	}
}
