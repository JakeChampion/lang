package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

var iterGenericReducerCases = []struct {
	name string
	main string
	want int
}{
	{"sum-i32", `import "core/iter";
function main(): i32 { let a: i32[] = [10, 20, 12]; return iter.sum(iter.of(a)); }`, 42},
	{"product-i32", `import "core/iter";
function main(): i32 { let a: i32[] = [2, 3, 7]; return iter.product(iter.of(a)); }`, 42},
	{"sum-filter", `import "core/iter";
function main(): i32 { let a: i32[] = [1, 2, 3, 4, 5, 6]; let e: i32[] = iter.of(a).filter((n: i32): boolean => { return n % 2 == 0; }).collect(); return iter.sum(iter.of(e)); }`, 12},
	{"sum-i64", `import "core/iter";
function main(): i32 { let b: i64[] = [3000000000, 3000000000]; let s = iter.sum(iter.of(b)); if (s == 6000000000) { return 7; } return 0; }`, 7},
	{"f64", `import "core/iter";
function main(): i32 { let b: f64[] = [1.5, 2.5, 4.0]; if (iter.sum(iter.of(b)) == 8.0 && iter.product(iter.of(b)) == 15.0) { return 8; } return 0; }`, 8},
	{"empty-identities", `import "core/iter";
function main(): i32 { let e: i32[] = []; return iter.sum(iter.of(e)) + iter.product(iter.of(e)); }`, 1},
	{"min-max", `import "core/iter";
function main(): i32 {
  let s: string[] = ["pear", "apple", "zoo"];
  let w: i64[] = [5000000000, 2, 7];
  let e: i64[] = [];
  let r = 0;
  match (iter.min(iter.of(s))) { Some(v) => { if (v == "apple") { r = r + 1; } }, None => {} }
  match (iter.max(iter.of(s))) { Some(v) => { if (v == "zoo") { r = r + 2; } }, None => {} }
  match (iter.max(iter.of(w))) { Some(v) => { if (v == 5000000000) { r = r + 4; } }, None => {} }
  match (iter.min(iter.of(e))) { Some(v) => {}, None => { r = r + 8; } }
  return r;
}`, 15},
	{"eq-reducers", `import "core/iter";
function main(): i32 {
  let s: string[] = ["a", "b", "a"];
  let w: i64[] = [3, 9, 2];
  let r = 0;
  match (iter.index_of(iter.of(s), "b")) { Some(p) => { if (p == 1) { r = r + 1; } }, None => {} }
  if (iter.count(iter.of(s), "a") == 2) { r = r + 2; }
  if (iter.contains(iter.of(w), 2)) { r = r + 4; }
  if (!iter.contains(iter.of(w), 4)) { r = r + 8; }
  return r;
}`, 15},
}

func TestIterGenericReducers(t *testing.T) {
	for _, tc := range iterGenericReducerCases {
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
