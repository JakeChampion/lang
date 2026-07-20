package constfold

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// TestConstDeclaredWidthAccepted pins that a const's DECLARED numeric type
// settles its literal instead of being compared for exact equality against
// litType's default reading of that literal.
//
// litType reports NumberType{} (i32) for any integer literal and FloatType{}
// (f32) for any float one, and Fold used to reject the const unless
// ast.Equal(declared, that) held. So every non-i32 numeric const was rejected —
// not just the eye-catching `const B: i64 = 5000000000` ("declared type i64
// does not match initialiser type i32") but the entirely ordinary
// `const B: i64 = 5`, and `const H: f64 = 3.5` ("does not match initialiser
// type f32"). #5477.
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
			if err := Fold(prog); err != nil {
				t.Fatalf("Fold rejected a valid const: %v", err)
			}
		})
	}
}

// TestConstOutOfRangeRejected pins that relaxing the type comparison did not
// relax RANGE checking: a literal outside its declared type still fails, now
// with a range diagnostic rather than the old (accidental) type mismatch.
func TestConstOutOfRangeRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"i32-overflow", `const B: i32 = 5000000000; function main(): i32 { return 0; }`, "out of range for i32"},
		{"u32-negative", `const B: u32 = 0 - 1; function main(): i32 { return 0; }`, "out of range for u32"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := foldErr(t, tc.src); !strings.Contains(got, tc.want) {
				t.Errorf("error = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// TestConstWidthStampedOnSubstitution pins that the declared width reaches
// every substitution site. settleConstLit stamps Width on the const's literal,
// but cloneLit dropped it when copying the literal into each reference, so an
// i64 const still arrived at the checker as a width-0 (i32-default) literal and
// failed E047 "literal 5000000000 does not fit in i32" at its first use. #5477.
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

// TestConstSubstitutedInsideCompoundExprs pins the substituter walking every
// compound expression form. It had no case for casts, slices, tuple/map
// literals, enum payloads, f-strings or lambda bodies, so a const referenced
// inside any of them was left as a bare Ident and reached the checker as
// "E001: undefined identifier" for a const plainly in scope — `N as i32` being
// the common one. #5477.
func TestConstSubstitutedInsideCompoundExprs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"cast", `const N: i32 = 4; function main(): i32 { return N as i32; }`},
		{"slice-bound", `const N: i32 = 2; function main(): i32 { var a = [9, 8, 7]; var b = a[0:N]; return 0; }`},
		{"tuple-elem", `const N: i32 = 2; function main(): i32 { var t = (N, 5); return 0; }`},
		{"lambda-body", `const N: i32 = 3; function main(): i32 { var f = (x: i32) => x + N; return f(1); }`},
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
