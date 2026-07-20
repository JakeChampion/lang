package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/array's `none[T](xs, pred)` — true iff `pred` holds for NO element, the
// readable complement of `any`; short-circuits on the first match, vacuously
// true for an empty array. Closure-based over a generic T[], the same shape as
// any / all / find that lowers on every native backend and the self-host IR
// path.
//
// Each inline case inlines a minimal `none` body so the single-program self-host
// driver (which resolves no imports) can compile it, then encodes the result as
// a small exit code.
var noneCases = []struct {
	name string
	main string
	want int
}{
	// no negatives -> none(is_neg) true (tag 1); has evens -> none(is_even) false;
	// 1*10 + 0 = 10.
	{"basic", `function none[T](xs: T[], pred: (T) => boolean): boolean { for x in xs { if (pred(x)) { return false; } } return true; }
function is_neg(x: i32): boolean { return x < 0; }
function is_even(x: i32): boolean { return x % 2 == 0; }
function b2i(b: boolean): i32 { if (b) { return 1; } return 0; }
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; return b2i(none(xs, is_neg)) * 10 + b2i(none(xs, is_even)); }`, 10},
	// empty -> vacuously true (1); all-match -> false (0); 1*10+0 = 10.
	{"empty-and-all-match", `function none[T](xs: T[], pred: (T) => boolean): boolean { for x in xs { if (pred(x)) { return false; } } return true; }
function is_neg(x: i32): boolean { return x < 0; }
function b2i(b: boolean): i32 { if (b) { return 1; } return 0; }
function main(): i32 { var e: i32[] = []; var negs: i32[] = [0 - 1, 0 - 2]; return b2i(none(e, is_neg)) * 10 + b2i(none(negs, is_neg)); }`, 10},
}

// TestNativeArrayNone runs the inline programs on interp / x86-64 / wasm / arm64.
func TestNativeArrayNone(t *testing.T) {
	for _, tc := range noneCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeArrayNoneModule exercises the shipped `import "std/array"` bodies
// (free + method forms) over i32 and string arrays.
func TestNativeArrayNoneModule(t *testing.T) {
	src := `import "std/array" as arr;
function is_neg(x: i32): boolean { return x < 0; }
function is_even(x: i32): boolean { return x % 2 == 0; }
function is_empty_str(s: string): boolean { return s.len() == 0; }
function main(): i32 {
    var r = 0;
    var xs: i32[] = [1, 2, 3, 4];
    if (xs.none(is_neg)) { r = r + 1; }        // no negatives
    if (!xs.none(is_even)) { r = r + 2; }      // has evens
    if (arr.none(xs, is_neg)) { r = r + 4; }   // free fn
    var e: i32[] = [];
    if (e.none(is_neg)) { r = r + 8; }         // empty -> true
    var ss: string[] = ["a", "b", "c"];
    if (ss.none(is_empty_str)) { r = r + 16; } // no empty strings
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 31 {
		t.Errorf("none module interp = %d, want 31", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 31 {
		t.Errorf("none module x86-64 = %d, want 31", code)
	}
	if code := runWasm(t, src); code != 31 {
		t.Errorf("none module wasm = %d, want 31", code)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 31 {
		t.Errorf("none module arm64 = %d, want 31", code)
	}
}
