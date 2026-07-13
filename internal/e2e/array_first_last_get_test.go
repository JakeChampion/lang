package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/array's foundational accessors `is_empty[T](xs)`, `first[T](xs)`,
// `last[T](xs)`, `get[T](xs, i)` — the safe, bounds-checked alternatives to
// `xs.len()==0` / `xs[0]` / `xs[len-1]` / `xs[i]`, returning Option[T] (None on
// empty / out-of-range; negative index is None, not from-the-end). Generic over
// T; structural, no closures.
//
// Each inline case inlines minimal bodies so the single-program self-host driver
// (which resolves no imports) can compile it, then encodes the result as a small
// exit code.
var firstLastGetCases = []struct {
	name string
	main string
	want int
}{
	// first=10, last=30, get(1)=20 -> 10 + 30 + 20 = 60.
	{"basic-i32", `function first[T](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } return Some(xs[0]); }
function last[T](xs: T[]): Option[T] { var n: i32 = xs.len(); if (n == 0) { return None; } return Some(xs[n - 1]); }
function get[T](xs: T[], i: i32): Option[T] { if (i < 0 || i >= xs.len()) { return None; } return Some(xs[i]); }
function uw(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0; } } }
function main(): i32 { var xs: i32[] = [10, 20, 30]; return uw(first(xs)) + uw(last(xs)) + uw(get(xs, 1)); }`, 60},
	// empty / out-of-range / negative all None -> unwrap-or-7 sums: 7+7+7+7 = 28.
	{"none-cases", `function first[T](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } return Some(xs[0]); }
function last[T](xs: T[]): Option[T] { var n: i32 = xs.len(); if (n == 0) { return None; } return Some(xs[n - 1]); }
function get[T](xs: T[], i: i32): Option[T] { if (i < 0 || i >= xs.len()) { return None; } return Some(xs[i]); }
function uw(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 7; } } }
function main(): i32 { var e: i32[] = []; var xs: i32[] = [1, 2, 3]; return uw(first(e)) + uw(last(e)) + uw(get(xs, 9)) + uw(get(xs, 0 - 1)); }`, 28},
	// is_empty on empty (1) and non-empty (0): encode 10*empty + nonempty -> 10.
	{"is-empty", `function is_empty[T](xs: T[]): boolean { return xs.len() == 0; }
function main(): i32 { var e: i32[] = []; var xs: i32[] = [1]; var a = 0; if (is_empty(e)) { a = a + 10; } if (is_empty(xs)) { a = a + 1; } return a; }`, 10},
}

// TestNativeArrayFirstLastGet runs the inline programs on interp / x86-64 / wasm
// / arm64.
func TestNativeArrayFirstLastGet(t *testing.T) {
	for _, tc := range firstLastGetCases {
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

// TestNativeArrayFirstLastGetModule exercises the shipped `import "std/array"`
// bodies (free + method forms) over i32 and string arrays.
func TestNativeArrayFirstLastGetModule(t *testing.T) {
	src := `import "std/array" as arr;
function uw(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0 - 1; } } }
function main(): i32 {
    var r = 0;
    var xs: i32[] = [10, 20, 30];
    if (!xs.is_empty()) { r = r + 1; }
    if (uw(xs.first()) == 10) { r = r + 2; }
    if (uw(xs.last()) == 30) { r = r + 4; }
    if (uw(xs.get(1)) == 20) { r = r + 8; }
    var e: i32[] = [];
    if (e.is_empty()) { r = r + 16; }
    match (arr.first(e)) { Some(v) => {}, None => { r = r + 32; } }
    var ss: string[] = ["a", "b"];
    match (ss.get(1)) { Some(v) => { if (v == "b") { r = r + 64; } }, None => {} }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 127 {
		t.Errorf("first_last_get module interp = %d, want 127", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 127 {
		t.Errorf("first_last_get module x86-64 = %d, want 127", code)
	}
	if code := runWasm(t, src); code != 127 {
		t.Errorf("first_last_get module wasm = %d, want 127", code)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 127 {
		t.Errorf("first_last_get module arm64 = %d, want 127", code)
	}
}
