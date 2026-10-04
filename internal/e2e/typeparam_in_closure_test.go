package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// A lambda or nested function inside a generic function sees the encloser's
// type parameters and their bounds: `T.make()` and a bounded method on a
// captured `T` resolve there as in the body (#10404).
var typeParamInClosureCases = []struct {
	name, main string
	want       int
}{
	{"lambda", typeParamInClosureDecls + `
function call0(g: () => i32): i32 { return g(); }
function f[T: Mk](y: T, x: i32): i32 {
    return call0((): i32 => { return T.make().get() + y.get(); }) + x;
}
function main(): i32 { return f[W](W { v: 2 }, 1); }`, 44},
	{"nested_function", typeParamInClosureDecls + `
function f[T: Mk](y: T, x: i32): i32 {
    function inner(): i32 { return T.make().get() + y.get(); }
    return inner() + x;
}
function main(): i32 { return f[W](W { v: 2 }, 1); }`, 44},
	{"lambda_in_lambda", typeParamInClosureDecls + `
function call0(g: () => i32): i32 { return g(); }
function f[T: Mk](y: T): i32 {
    return call0((): i32 => { return call0((): i32 => { return T.make().get() - y.get(); }); });
}
function main(): i32 { return f[W](W { v: 2 }); }`, 39},
}

const typeParamInClosureDecls = `trait Mk { function make(): Self; function get(self: Self): i32; }
struct W { v: i32 }
impl Mk for W { function make(): W { return W { v: 41 }; } function get(self: W): i32 { return self.v; } }`

func TestTypeParamInClosure(t *testing.T) {
	for _, tc := range typeParamInClosureCases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("interp = %d, want %d", code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("x86-64 = %d, want %d", code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("wasm = %d, want %d", code, tc.want)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}
