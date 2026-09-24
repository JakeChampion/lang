package checker

import "testing"

// A local named like an enum shadows it for a qualified access, in the entry
// and in an imported module alike (#10159), and inside a lambda that captures
// it (#10211). std/string has a local `R` that
// calls `R.mul_pow10(...)`, so an entry union named R used to make the import
// fail with E036.
func TestEnumNameShadowedByLocal(t *testing.T) {
	cases := []struct{ name, src string }{
		{"entry union named like an import's local", `import "std/string";
struct A { n: i32 }
struct B { n: i32 }
type R = A | B;
function main(): i32 { var r: R = A { n: 1 }; return 0; }
`},
		{"method on a local", `enum R { X, Y }
function f(): i32 { var R: string = "ab"; return R.len(); }
function main(): i32 { var r: R = R.X; return f(); }
`},
		{"field of a local", `enum R { X, Y }
struct S { X: i32 }
function f(): i32 { var R: S = S { X: 7 }; return R.X; }
function main(): i32 { var r: R = R.Y; return f(); }
`},
		{"field of a captured local", `enum R { X, Y }
struct S { X: i32 }
function f(): i32 { var R: S = S { X: 7 }; var g = (): i32 => { return R.X; }; return g(); }
function main(): i32 { var r: R = R.Y; return f(); }
`},
		{"method on a captured local", `enum R { X, Y }
function f(): i32 { var R: string = "ab"; var g = (): i32 => { return R.len(); }; return g(); }
function main(): i32 { var r: R = R.X; return f(); }
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkModuleSource(t, c.src); err != nil {
				t.Fatalf("want clean, got: %v", err)
			}
		})
	}
}
