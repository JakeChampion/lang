package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// std/array's `dedup[T: cmp.Eq](xs)` — collapse each run of CONSECUTIVE equal
// elements to one (`[1,1,2,2,1]` -> `[1,2,1]`), the single-pass complement of
// `distinct` (which removes ALL duplicates). Same T: cmp.Eq bound /
// per-element-type monomorphisation as distinct/contains, so the `==` is the
// type's structural equality; lowers on every native backend and the self-host
// IR path for the scalar element types.
//
// Each inline case inlines minimal bodies so the single-program self-host driver
// (which resolves no imports) can compile it, then encodes the result as a small
// exit code. (The generic `[T: cmp.Eq]` is spelled here as a concrete i32 body,
// since the single-file probe has no core/cmp to resolve the bound.)
var dedupCases = []struct {
	name string
	main string
	want int
}{
	// [1,1,2,2,1] -> [1,2,1]; encode r[0]*100 + r[1]*10 + r[2] = 121 (in byte
	// range, and requires len >= 3 or the r[2] index traps).
	{"runs", `function dedup(xs: i32[]): i32[] { var out: i32[] = []; var i: i32 = 0; while (i < xs.len()) { if (i == 0 || !(xs[i] == xs[i - 1])) { out = out.append(xs[i]); } i = i + 1; } return out; }
function main(): i32 { var r = dedup([1, 1, 2, 2, 1]); return r[0] * 100 + r[1] * 10 + r[2]; }`, 121},
	// all-equal collapses to one: [7,7,7,7] -> [7]; len 1, elem 7 -> 17.
	{"all-equal", `function dedup(xs: i32[]): i32[] { var out: i32[] = []; var i: i32 = 0; while (i < xs.len()) { if (i == 0 || !(xs[i] == xs[i - 1])) { out = out.append(xs[i]); } i = i + 1; } return out; }
function main(): i32 { var r = dedup([7, 7, 7, 7]); return r.len() * 10 + r[0]; }`, 17},
	// sorted with runs behaves like distinct: [1,1,2,3,3,3,4] -> [1,2,3,4]; len 4.
	{"sorted-like-distinct", `function dedup(xs: i32[]): i32[] { var out: i32[] = []; var i: i32 = 0; while (i < xs.len()) { if (i == 0 || !(xs[i] == xs[i - 1])) { out = out.append(xs[i]); } i = i + 1; } return out; }
function main(): i32 { var r = dedup([1, 1, 2, 3, 3, 3, 4]); return r.len(); }`, 4},
	// empty -> empty (len 0); no-dup input unchanged.
	{"empty-and-nodup", `function dedup(xs: i32[]): i32[] { var out: i32[] = []; var i: i32 = 0; while (i < xs.len()) { if (i == 0 || !(xs[i] == xs[i - 1])) { out = out.append(xs[i]); } i = i + 1; } return out; }
function main(): i32 { var e: i32[] = []; if (dedup(e).len() != 0) { return 0; } return dedup([1, 2, 3]).len(); }`, 3},
}

// TestNativeArrayDedup runs the inline programs on interp / x86-64 / wasm /
// arm64.
func TestNativeArrayDedup(t *testing.T) {
	for _, tc := range dedupCases {
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

// TestNativeArrayDedupModule exercises the shipped `import "std/array"`
// dedup[T: cmp.Eq] (free + method forms) over i32 and string arrays, incl. the
// consecutive-vs-all distinction and the empty case.
func TestNativeArrayDedupModule(t *testing.T) {
	src := `import "std/array" as arr;
function same(a: i32[], b: i32[]): boolean { if (a.len() != b.len()) { return false; } var i = 0; while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; } return true; }
function main(): i32 {
    var r = 0;
    if (same(arr.dedup([1, 1, 2, 2, 1]), [1, 2, 1])) { r = r + 1; }
    if (same([1, 1, 1].dedup(), [1])) { r = r + 2; }
    if (same([1, 2, 3].dedup(), [1, 2, 3])) { r = r + 4; }
    var e: i32[] = [];
    if (e.dedup().len() == 0) { r = r + 8; }
    var ss: string[] = ["a", "a", "b", "a"];
    var sr = ss.dedup();
    if (sr.len() == 3 && sr[0] == "a" && sr[1] == "b" && sr[2] == "a") { r = r + 16; }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 31 {
		t.Errorf("dedup module interp = %d, want 31", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 31 {
		t.Errorf("dedup module x86-64 = %d, want 31", code)
	}
	if code := runWasm(t, src); code != 31 {
		t.Errorf("dedup module wasm = %d, want 31", code)
	}
	if _, code := runFixtureArm64(t, p, ""); code != 31 {
		t.Errorf("dedup module arm64 = %d, want 31", code)
	}
}
