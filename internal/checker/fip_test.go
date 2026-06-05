package checker

import (
	"strings"
	"testing"
)

// `fip function` is a Koka-style fully-in-place CHECKED guarantee: the checker
// (E053) verifies the body performs no heap allocation, as a sound conservative
// subset. In-place index writes to an `own` array parameter are allowed (the
// copy-on-write unique branch); allocating literals / string ops / non-fip
// calls are rejected. These pin both directions.

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
	// In-place insertion sort over an `own` array: index writes to the unique
	// `arr` are in-place, `len` is whitelisted, everything else is scalar.
	wantNoErr(t, "inplace sort", `fip function sort_inplace(own arr: i32[]): i32[] {
    var n: i32 = arr.len();
    var k: i32 = 1;
    while (k < n) {
        var key: i32 = arr[k];
        var j: i32 = k - 1;
        while (j >= 0 && arr[j] > key) { arr[j + 1] = arr[j]; j = j - 1; }
        arr[j + 1] = key;
        k = k + 1;
    }
    return arr;
}
function main(): i32 { return 0; }`)

	// Pure scalar arithmetic — trivially fip.
	wantNoErr(t, "scalar", `fip function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return 0; }`)

	// A fip function calling another fip function.
	wantNoErr(t, "fip calls fip", `fip function inc(x: i32): i32 { return x + 1; }
fip function twice(x: i32): i32 { return inc(inc(x)); }
function main(): i32 { return 0; }`)
}

func TestFipRejectsAllocation(t *testing.T) {
	wantE053(t, "array literal", `fip function f(): i32[] { return [1, 2, 3]; }
function main(): i32 { return 0; }`)

	wantE053(t, "struct literal", `struct P { x: i32, y: i32 }
fip function f(): P { return P { x: 1, y: 2 }; }
function main(): i32 { return 0; }`)

	wantE053(t, "tuple literal", `fip function f(): (i32, i32) { return (1, 2); }
function main(): i32 { return 0; }`)

	wantE053(t, "enum construction", `enum L { C(i32, L), N }
fip function f(t: L): L { return C(1, t); }
function main(): i32 { return 0; }`)

	wantE053(t, "string concat", `fip function f(a: string, b: string): string { return a + b; }
function main(): i32 { return 0; }`)

	wantE053(t, "calls non-fip", `function alloc(): i32[] { return [1]; }
fip function f(): i32 { var a: i32[] = alloc(); return a[0]; }
function main(): i32 { return 0; }`)

	// Index write to a NON-`own` (shared/borrowed) array copies-on-write.
	wantE053(t, "write to non-own array", `fip function f(arr: i32[]): i32[] { arr[0] = 9; return arr; }
function main(): i32 { return 0; }`)
}
