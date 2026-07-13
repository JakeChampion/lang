package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/array's closure-aggregate verbs `count_where[T](xs, pred) -> i32` (tally
// of matching elements — the allocation-free companion to filter(…).len()) and
// `sum_by[T](xs, key) -> i32` (sum of an i32 projection over any element type —
// the projection sibling of the i32[]-only sum). Both are generic over the
// element type with a closure arg, the same shape as find / position that
// already lowers on every native backend and the self-host IR path. Empty → 0.
//
// Each inline case inlines minimal bodies so the single-program self-host
// driver (which resolves no imports) can compile it, then encodes the result as
// a small exit code.
var countWhereSumByCases = []struct {
	name string
	main string
	want int
}{
	// 3 evens in [1..6] (tag 3) then sum of string lengths a/bb/ccc = 6; 3*10+6 = 36.
	{"count-and-sum", `function count_where[T](xs: T[], pred: (T) => boolean): i32 { var c: i32 = 0; var i: i32 = 0; while (i < xs.len()) { if (pred(xs[i])) { c = c + 1; } i = i + 1; } return c; }
function sum_by[T](xs: T[], key: (T) => i32): i32 { var t: i32 = 0; var i: i32 = 0; while (i < xs.len()) { t = t + key(xs[i]); i = i + 1; } return t; }
function is_even(x: i32): boolean { return x % 2 == 0; }
function slen(s: string): i32 { return s.len(); }
function main(): i32 { var ns: i32[] = [1, 2, 3, 4, 5, 6]; var ws: string[] = ["a", "bb", "ccc"]; return count_where(ns, is_even) * 10 + sum_by(ws, slen); }`, 36},
	// empty → 0 for both; encode as 2 + 3 = 5.
	{"empty-zero", `function count_where[T](xs: T[], pred: (T) => boolean): i32 { var c: i32 = 0; var i: i32 = 0; while (i < xs.len()) { if (pred(xs[i])) { c = c + 1; } i = i + 1; } return c; }
function sum_by[T](xs: T[], key: (T) => i32): i32 { var t: i32 = 0; var i: i32 = 0; while (i < xs.len()) { t = t + key(xs[i]); i = i + 1; } return t; }
function is_even(x: i32): boolean { return x % 2 == 0; }
function idn(x: i32): i32 { return x; }
function main(): i32 { var e: i32[] = []; return count_where(e, is_even) + 2 + sum_by(e, idn) + 3; }`, 5},
	// none match → 0 count; sum over identity of [10,20,30] = 60.
	{"none-and-total", `function count_where[T](xs: T[], pred: (T) => boolean): i32 { var c: i32 = 0; var i: i32 = 0; while (i < xs.len()) { if (pred(xs[i])) { c = c + 1; } i = i + 1; } return c; }
function sum_by[T](xs: T[], key: (T) => i32): i32 { var t: i32 = 0; var i: i32 = 0; while (i < xs.len()) { t = t + key(xs[i]); i = i + 1; } return t; }
function is_neg(x: i32): boolean { return x < 0; }
function idn(x: i32): i32 { return x; }
function main(): i32 { var ns: i32[] = [10, 20, 30]; return count_where(ns, is_neg) * 100 + sum_by(ns, idn); }`, 60},
}

// TestNativeArrayCountWhereSumBy runs the inline programs on interp / x86-64 /
// wasm / arm64.
func TestNativeArrayCountWhereSumBy(t *testing.T) {
	for _, tc := range countWhereSumByCases {
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

// TestNativeArrayCountWhereSumByModule exercises the shipped `import
// "std/array"` bodies (free + method forms) over i32 and string arrays.
func TestNativeArrayCountWhereSumByModule(t *testing.T) {
	src := `import "std/array" as arr;
function is_even(x: i32): boolean { return x % 2 == 0; }
function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    var r = 0;
    var ns: i32[] = [1, 2, 3, 4, 5, 6];
    if (arr.count_where(ns, is_even) == 3) { r = r + 1; }
    if (ns.count_where(is_even) == 3) { r = r + 2; }
    var ws: string[] = ["a", "bb", "ccc"];
    if (arr.sum_by(ws, slen) == 6) { r = r + 4; }
    if (ws.sum_by(slen) == 6) { r = r + 8; }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 15 {
		t.Errorf("count_where/sum_by module interp = %d, want 15", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 15 {
		t.Errorf("count_where/sum_by module x86-64 = %d, want 15", code)
	}
	if code := runWasm(t, src); code != 15 {
		t.Errorf("count_where/sum_by module wasm = %d, want 15", code)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 15 {
		t.Errorf("count_where/sum_by module arm64 = %d, want 15", code)
	}
}
