package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/array's structural rotation verbs `rotate_left[T](xs, n)` and
// `rotate_right[T](xs, n)` — cyclic shift by n (mod len), negative n rotates the
// other way, empty / n%len==0 → unchanged copy. Structural (no element
// comparison / closure), so any element type works and the module lowers on
// every native backend and the self-host IR path.
//
// Each inline case inlines minimal bodies so the single-program self-host driver
// (which resolves no imports) can compile it, then encodes the result as a small
// exit code.
var rotateCases = []struct {
	name string
	main string
	want int
}{
	// [1..5] rotate_left 2 -> [3,4,5,1,2]; sum of first two (3+4) tag = 7.
	{"left-basic", `function rotate_left[T](xs: T[], n: i32): T[] { var len: i32 = xs.len(); var out: T[] = []; if (len == 0) { return out; } var sh: i32 = ((n % len) + len) % len; var i: i32 = 0; while (i < len) { out = out.append(xs[(i + sh) % len]); i = i + 1; } return out; }
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; var r = rotate_left(xs, 2); return r[0] * 10 + r[1]; }`, 34},
	// rotate_right 2 -> [4,5,1,2,3]; r[0]*10+r[1] = 45.
	{"right-basic", `function rotate_left[T](xs: T[], n: i32): T[] { var len: i32 = xs.len(); var out: T[] = []; if (len == 0) { return out; } var sh: i32 = ((n % len) + len) % len; var i: i32 = 0; while (i < len) { out = out.append(xs[(i + sh) % len]); i = i + 1; } return out; }
function rotate_right[T](xs: T[], n: i32): T[] { return rotate_left(xs, 0 - n); }
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; var r = rotate_right(xs, 2); return r[0] * 10 + r[1]; }`, 45},
	// n > len wraps (7 % 5 == 2 -> same as left-basic): r[0]*10+r[1] = 34.
	{"wrap-and-negative", `function rotate_left[T](xs: T[], n: i32): T[] { var len: i32 = xs.len(); var out: T[] = []; if (len == 0) { return out; } var sh: i32 = ((n % len) + len) % len; var i: i32 = 0; while (i < len) { out = out.append(xs[(i + sh) % len]); i = i + 1; } return out; }
function same(a: i32[], b: i32[]): boolean { if (a.len() != b.len()) { return false; } var i = 0; while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; } return true; }
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; if (!same(rotate_left(xs, 7), rotate_left(xs, 2))) { return 0; } if (!same(rotate_left(xs, 0 - 1), rotate_left(xs, 4))) { return 0; } var r = rotate_left(xs, 7); return r[0] * 10 + r[1]; }`, 34},
}

// TestNativeArrayRotate runs the inline programs on interp / x86-64 / wasm /
// arm64.
func TestNativeArrayRotate(t *testing.T) {
	for _, tc := range rotateCases {
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

// TestNativeArrayRotateModule exercises the shipped `import "std/array"` bodies
// (free + method forms) over i32 and string arrays, incl. the wrap, zero-shift,
// negative, and empty cases.
func TestNativeArrayRotateModule(t *testing.T) {
	src := `import "std/array" as arr;
function same(a: i32[], b: i32[]): boolean { if (a.len() != b.len()) { return false; } var i = 0; while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; } return true; }
function main(): i32 {
    var r = 0;
    var xs: i32[] = [1, 2, 3, 4, 5];
    if (same(arr.rotate_left(xs, 2), [3, 4, 5, 1, 2])) { r = r + 1; }
    if (same(xs.rotate_right(2), [4, 5, 1, 2, 3])) { r = r + 2; }
    if (same(xs.rotate_left(7), xs.rotate_left(2))) { r = r + 4; }       // wrap
    if (same(xs.rotate_left(0), xs)) { r = r + 8; }                       // zero shift
    var e: i32[] = [];
    if (arr.rotate_left(e, 3).len() == 0) { r = r + 16; }               // empty
    var ss: string[] = ["a", "b", "c"];
    var sr = ss.rotate_left(1);
    if (sr[0] == "b" && sr[1] == "c" && sr[2] == "a") { r = r + 32; }   // strings
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 63 {
		t.Errorf("rotate module interp = %d, want 63", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 63 {
		t.Errorf("rotate module x86-64 = %d, want 63", code)
	}
	if code := runWasm(t, src); code != 63 {
		t.Errorf("rotate module wasm = %d, want 63", code)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 63 {
		t.Errorf("rotate module arm64 = %d, want 63", code)
	}
}
