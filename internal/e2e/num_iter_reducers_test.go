package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/num's iterator-form reducers `sum_iter` / `product_iter` — the Iterator
// counterparts of the array `sum_with` / `product_with`. They fold any
// `iter.Iterator[T]` directly (no intermediate array), dispatching arithmetic
// by method (`acc.add(x)`) over a `T: Add` / `T: Mul` bound with the identity
// passed explicitly (IR-safe: no `T.zero()` on a bound type param). `T` appears
// only inside the iterator bound and is recovered by bound-driven inference;
// the cases run over the new core/iter ArrayIter and through a filter pipeline,
// on interp / x86-64 / wasm.
var numIterReducerCases = []struct {
	name string
	main string
	want int
}{
	// sum over an i32 array via iter.of → 10+20+12 = 42.
	{"sum-iter-i32", `import "std/num";
import "core/iter";
function main(): i32 { var a: i32[] = [10, 20, 12]; return num.sum_iter(iter.of(a), 0); }`, 42},
	// product over an i32 array via iter.of → 2*3*7 = 42.
	{"product-iter-i32", `import "std/num";
import "core/iter";
function main(): i32 { var a: i32[] = [2, 3, 7]; return num.product_iter(iter.of(a), 1); }`, 42},
	// composed pipeline: sum of the evens of 1..6 (filter then sum_iter) → 2+4+6 = 12.
	{"sum-iter-filter", `import "std/num";
import "core/iter";
function main(): i32 { var a: i32[] = [1, 2, 3, 4, 5, 6]; var e: i32[] = iter.filter(iter.of(a), (n: i32): boolean => { return n % 2 == 0; }); return num.sum_iter(iter.of(e), 0); }`, 12},
	// i64 element type with a typed identity → 100+200+300 = 600, via sentinel
	// (a raw 600 would wrap mod 256 as a process exit code).
	{"sum-iter-i64", `import "std/num";
import "core/iter";
function main(): i32 { var b: i64[] = [100, 200, 300]; var z: i64 = 0; if (num.sum_iter(iter.of(b), z) == 600) { return 7; } return 0; }`, 7},
	// empty iterator → the identity, returned unchanged: 0 (sum) + 1 (product) = 1.
	{"empty-identities", `import "std/num";
import "core/iter";
function main(): i32 { var e: i32[] = []; return num.sum_iter(iter.of(e), 0) + num.product_iter(iter.of(e), 1); }`, 1},
}

func TestNumIterReducers(t *testing.T) {
	for _, tc := range numIterReducerCases {
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
