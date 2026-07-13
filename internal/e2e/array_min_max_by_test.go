package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/array's comparator-extremum verbs `max_by[T](xs, cmp) -> Option[T]` and
// `min_by` — the element that is greatest / least under a three-way comparator
// (same `cmp(a, b) < 0 == a-before-b` convention as `sort_by`). The comparator
// companions to `max_by_i32_key` / `min_by_i32_key`: where the key form needs
// an i32 projection, these take an arbitrary ordering, so the extremum can be
// by string length, lexicographic order, or any struct rule without
// materialising a key. Empty -> None; ties keep the FIRST extremum. The closure
// over a generic `T[]` lowers on every native backend and the self-host IR path.
//
// Each inline case inlines minimal `max_by` / `min_by` bodies so the
// single-program self-host driver (which resolves no imports) can compile it,
// then encodes the result as a small exit code.
var minMaxByCases = []struct {
	name string
	main string
	want int
}{
	// lengths [2,1,4,3]; longest is index 2 (tag 3), shortest index 1 (tag 2);
	// 3*10 + 2 = 32.
	{"max-min", `function max_by[T](xs: T[], cmp: (T, T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (cmp(xs[i], best) > 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function min_by[T](xs: T[], cmp: (T, T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (cmp(xs[i], best) < 0) { best = xs[i]; } i = i + 1; } return Some(best); }
struct R { tag: i32, w: string }
function cmp_len(a: R, b: R): i32 { return a.w.len() - b.w.len(); }
function pick(o: Option[R]): i32 { match (o) { Some(r) => { return r.tag; }, None => { return 0 - 1; } } }
function main(): i32 { var rs: R[] = [R { tag: 1, w: "bb" }, R { tag: 2, w: "a" }, R { tag: 3, w: "dddd" }, R { tag: 4, w: "ccc" }]; return pick(max_by(rs, cmp_len)) * 10 + pick(min_by(rs, cmp_len)); }`, 32},
	// empty -> None for both; encode as 2 (max None) + 3 (min None) = 5.
	{"empty-none", `function max_by[T](xs: T[], cmp: (T, T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (cmp(xs[i], best) > 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function min_by[T](xs: T[], cmp: (T, T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (cmp(xs[i], best) < 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function cmp_i(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { var e: i32[] = []; var r = 0; match (max_by(e, cmp_i)) { Some(x) => {}, None => { r = r + 2; } } match (min_by(e, cmp_i)) { Some(x) => {}, None => { r = r + 3; } } return r; }`, 5},
	// ties keep the FIRST extremum: two elements share length 4; max returns the
	// earlier one (tag 10), not tag 11.
	{"ties-first", `function max_by[T](xs: T[], cmp: (T, T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (cmp(xs[i], best) > 0) { best = xs[i]; } i = i + 1; } return Some(best); }
struct R { tag: i32, w: string }
function cmp_len(a: R, b: R): i32 { return a.w.len() - b.w.len(); }
function pick(o: Option[R]): i32 { match (o) { Some(r) => { return r.tag; }, None => { return 0 - 1; } } }
function main(): i32 { var rs: R[] = [R { tag: 10, w: "wxyz" }, R { tag: 7, w: "q" }, R { tag: 11, w: "abcd" }]; return pick(max_by(rs, cmp_len)); }`, 10},
}

// TestNativeArrayMinMaxBy runs the inline programs on interp / x86-64 / wasm /
// arm64.
func TestNativeArrayMinMaxBy(t *testing.T) {
	for _, tc := range minMaxByCases {
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

// TestNativeArrayMinMaxByModule exercises the shipped `import "std/array"`
// bodies (free-function and method forms) over a struct array compared by a
// string field, incl. an inverted comparator and the empty case.
func TestNativeArrayMinMaxByModule(t *testing.T) {
	src := `import "std/array" as arr;
struct Rec { id: i32, name: string }
function by_name(a: Rec, b: Rec): i32 { return a.name.cmp(b.name); }
function main(): i32 {
    var r = 0;
    var rs: Rec[] = [Rec { id: 1, name: "bravo" }, Rec { id: 2, name: "alpha" }, Rec { id: 3, name: "delta" }, Rec { id: 4, name: "charlie" }];
    match (arr.max_by(rs, by_name)) { Some(x) => { if (x.id == 3) { r = r + 1; } }, None => {} }
    match (rs.min_by(by_name)) { Some(x) => { if (x.id == 2) { r = r + 2; } }, None => {} }
    match (rs.max_by(function (a: Rec, b: Rec): i32 { return b.id - a.id; })) { Some(x) => { if (x.id == 1) { r = r + 4; } }, None => {} }
    var e: Rec[] = [];
    match (arr.min_by(e, by_name)) { Some(x) => {}, None => { r = r + 8; } }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 15 {
		t.Errorf("min_max_by module interp = %d, want 15", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 15 {
		t.Errorf("min_max_by module x86-64 = %d, want 15", code)
	}
	if code := runWasm(t, src); code != 15 {
		t.Errorf("min_max_by module wasm = %d, want 15", code)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 15 {
		t.Errorf("min_max_by module arm64 = %d, want 15", code)
	}
}
