package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

var arrayIterCases = []struct {
	name string
	main string
	want int
}{
	{"sum-array", `import "core/iter";
function main(): i32 { let xs: i32[] = [10, 20, 12]; return iter.sum(iter.of(xs)); }`, 42},
	{"len-array", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 1, 1, 1, 1]; return iter.of(xs).len(); }`, 5},
	{"filter-len", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4, 5, 6]; let e: i32[] = iter.of(xs).filter((n: i32): boolean => { return n % 2 == 0; }).collect(); return iter.of(e).len(); }`, 3},
	{"map-sum", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; let sq: i32[] = iter.of(xs).map((n: i32): i32 => { return n * n; }).collect(); return iter.sum(iter.of(sq)); }`, 30},
	{"sum-map-keys", `import "core/iter";
import "core/map";
function main(): i32 { let m: Map[i32, i32] = Map { 10: 1, 20: 2, 12: 3 }; return iter.sum(iter.of(m.keys())); }`, 42},
	{"sum-map-values", `import "core/iter";
import "core/map";
function main(): i32 { let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 12 }; return iter.sum(iter.of(m.values())); }`, 42},
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

const parametricGenericTraitProg = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct One[T] { v: T, done: boolean }
impl[T] Iterator[T] for One[T] {
    function next(self: Self): Option[(T, Self)] {
        if (self.done) { return None; }
        return Some((self.v, One { v: self.v, done: true }));
    }
}
function count[T, I: Iterator[T]](it: I): i32 {
    let n = 0; let cur = it; let go = true;
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
