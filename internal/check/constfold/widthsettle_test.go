package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestConstDeclaredWidthAccepted(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"i64-big-literal", `const B: i64 = 5000000000; function main(): i32 { return 0; }`},
		{"i64-small-literal", `const B: i64 = 5; function main(): i32 { return 0; }`},
		{"u64", `const B: u64 = 18000000000; function main(): i32 { return 0; }`},
		{"u32-upper-half", `const B: u32 = 4000000000; function main(): i32 { return 0; }`},
		{"f64", `const H: f64 = 3.5; function main(): i32 { return 0; }`},
		{"f32", `const H: f32 = 1.5; function main(): i32 { return 0; }`},
		{"i32-unchanged", `const N: i32 = 41; function main(): i32 { return 0; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := Fold(prog, nil); err != nil {
				t.Fatalf("Fold rejected a valid const: %v", err)
			}
		})
	}
}

func TestConstWidthStampedOnSubstitution(t *testing.T) {
	prog := fold(t, `const B: i64 = 5000000000; function main(): i32 { return B; }`)
	lit, ok := returnLit(t, prog).(*ast.NumberLit)
	if !ok {
		t.Fatalf("returned expr is %T, want *ast.NumberLit", returnLit(t, prog))
	}
	if lit.Width != 64 {
		t.Errorf("substituted literal Width = %d, want 64", lit.Width)
	}
	if lit.Value != 5000000000 {
		t.Errorf("substituted literal Value = %d, want 5000000000", lit.Value)
	}
}

func TestConstSubstitutedInsideCompoundExprs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"cast", `const N: i32 = 4; function main(): i32 { return N as i32; }`},
		{"slice-bound", `const N: i32 = 2; function main(): i32 { let a = [9, 8, 7]; let b = a[0:N]; return 0; }`},
		{"tuple-elem", `const N: i32 = 2; function main(): i32 { let t = (N, 5); return 0; }`},
		{"lambda-body", `const N: i32 = 3; function main(): i32 { let f = (x: i32) => x + N; return f(1); }`},
		{"struct-spread", `const N: i32 = 3; struct S { a: i32, b: i32 } function mk(a: i32): S { return S { a: a, b: 0 }; } function main(): i32 { let s: S = S { ...mk(N), b: 1 }; return s.a; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := fold(t, tc.src)
			if n := countIdents(prog, "N"); n != 0 {
				t.Errorf("found %d unsubstituted `N` idents after Fold, want 0", n)
			}
		})
	}
}
