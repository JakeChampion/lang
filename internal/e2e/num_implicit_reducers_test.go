package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/num's implicit-identity reducers `sum` / `product` — `sum_with` /
// `product_with` without the explicit seed, recovering it from the Zero / One
// trait (`T.zero()` / `T.one()`). These are the first consumers of the Zero /
// One traits, and lower on every backend now that an associated-function call
// on a bound type parameter lowers on the self-host IR path (#3749). Pinned
// across element types + the empty-identity edges on interp / x86-64 / wasm.
var numImplicitReducerCases = []struct {
	name string
	main string
	want int
}{
	// sum over an i32 array, identity recovered from Zero → 10+20+12 = 42.
	{"sum-i32", `import "std/num";
function main(): i32 { var a: i32[] = [10, 20, 12]; return num.sum(a); }`, 42},
	// product over a u32 array, identity recovered from One → 2*3*7 = 42.
	{"product-u32", `import "std/num";
function main(): i32 { var a: u32[] = [2, 3, 7]; return (num.product(a) as i32); }`, 42},
	// i64 element width (exercises a non-i32 T inferred from the array) →
	// 40+2 = 42, kept in exit-code range.
	{"sum-i64", `import "std/num";
function main(): i32 { var b: i64[] = [40, 2]; return (num.sum(b) as i32); }`, 42},
	// empty array → additive identity 0 (sum) + multiplicative identity 1
	// (product) = 1.
	{"empty-identities", `import "std/num";
function main(): i32 { var e: i32[] = []; return num.sum(e) + num.product(e); }`, 1},
	// product over an i32 array → 1*2*3*4 = 24.
	{"product-i32", `import "std/num";
function main(): i32 { var a: i32[] = [1, 2, 3, 4]; return num.product(a); }`, 24},
}

func TestNumImplicitReducers(t *testing.T) {
	for _, tc := range numImplicitReducerCases {
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
