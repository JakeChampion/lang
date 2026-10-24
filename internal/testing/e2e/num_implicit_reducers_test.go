package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

var numImplicitReducerCases = []struct {
	name string
	main string
	want int
}{
	{"sum-i32", `import "std/num";
function main(): i32 { let a: i32[] = [10, 20, 12]; return num.sum(a); }`, 42},
	{"product-u32", `import "std/num";
function main(): i32 { let a: u32[] = [2, 3, 7]; return (num.product(a) as i32); }`, 42},
	{"sum-i64", `import "std/num";
function main(): i32 { let b: i64[] = [40, 2]; return (num.sum(b) as i32); }`, 42},
	{"empty-identities", `import "std/num";
function main(): i32 { let e: i32[] = []; return num.sum(e) + num.product(e); }`, 1},
	{"product-i32", `import "std/num";
function main(): i32 { let a: i32[] = [1, 2, 3, 4]; return num.product(a); }`, 24},
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
