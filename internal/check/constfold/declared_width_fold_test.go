package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// A const of a declared integer type folds in that type. A step whose exact
// value the type does not hold is E047, and so is a shift by the width of the
// type or more.
func TestConstFoldStaysInDeclaredType(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
		err  string
	}{
		{"i32-add-leaves", `const C: i32 = (2147483647 + 1) / 2;`, 0, "const C: 2147483647 + 1 does not fit in i32"},
		{"i32-shift-count", `const C: i32 = (1 << 33) & 255;`, 0, "const C: shift count 33 is not less than the 32 bits of i32"},
		{"i32-shift-leaves", `const C: i32 = 1 << 31;`, 0, "const C: 1 << 31 does not fit in i32"},
		{"u8-add-leaves", `const C: u8 = 255 + 1;`, 0, "const C: 255 + 1 does not fit in u8"},
		{"u32-subtract-leaves", `const C: u32 = 0 - 1;`, 0, "const C: 0 - 1 does not fit in u32"},
		{"i32-multiply-leaves", `const C: i32 = 100000 * 100000;`, 0, "const C: 100000 * 100000 does not fit in i32"},
		{"i64-shift-count", `const C: i64 = 1 << 64;`, 0, "const C: shift count 64 is not less than the 64 bits of i64"},
		{"i64-multiply-leaves", `const C: i64 = 4611686018427387904 * 2;`, 0, "const C: 4611686018427387904 * 2 does not fit in i64"},
		{"u64-add-leaves", `const C: u64 = 18446744073709551615 + 1;`, 0, "const C: 18446744073709551615 + 1 does not fit in u64"},
		{"i32-divide-leaves", `const C: i32 = (0 - 2147483647 - 1) / (0 - 1);`, 0, "const C: -2147483648 / -1 does not fit in i32"},
		{"i32-negate-leaves", `const C: i32 = -(0 - 2147483647 - 1);`, 0, "const C: -(-2147483648) does not fit in i32"},
		{"u8-step-after-a-bad-literal", `const C: u8 = 300 + 1;`, 0, "literal 300 does not fit in u8"},
		{"i32-shift-right-count", `const C: i32 = (0 - 8) >> 33;`, 0, "const C: shift count 33 is not less than the 32 bits of i32"},
		{"i32-lowest-literal", `const C: i32 = -2147483648;`, -2147483648, ""},
		{"i32-lowest", `const C: i32 = 0 - 2147483647 - 1;`, -2147483648, ""},
		{"u32-highest", `const C: u32 = 4294967295 - 1 + 1;`, 4294967295, ""},
		{"u64-highest", `const C: u64 = 18446744073709551614 + 1;`, -1, ""},
		{"u32-shift-right-is-logical", `const C: u32 = 4294967295 >> 1;`, 2147483647, ""},
		{"i32-shift-right-is-arithmetic", `const C: i32 = (0 - 8) >> 1;`, -4, ""},
		{"u8-not-stays-in-type", `const C: u8 = ~1;`, 254, ""},
		{"i32-ordinary", `const C: i32 = 6 * 7;`, 42, ""},
		{"i64-wide-value-survives", `const C: i64 = 3000000000 + 1;`, 3000000001, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src + " function main(): i32 { return C; }")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tc.err != "" {
				errs := foldErrors(t, tc.src+" function main(): i32 { return C; }")
				if len(errs) != 1 || errs[0].ErrCode != "E047" || errs[0].Msg != tc.err {
					t.Fatalf("Fold(%s) = %v, want one E047 with %q", tc.src, errs, tc.err)
				}
				return
			}
			if err := Fold(prog, nil); err != nil {
				t.Fatalf("Fold: %v", err)
			}
			got := returnLit(t, prog)
			lit, ok := got.(*ast.NumberLit)
			if !ok {
				t.Fatalf("const C folded to %T, want *ast.NumberLit", got)
			}
			if lit.Value != tc.want {
				t.Errorf("%s folded to %d, want %d", tc.src, lit.Value, tc.want)
			}
		})
	}
}

func TestUndeclaredConstStillFoldsInInt64(t *testing.T) {
	prog, err := parser.Parse(`const C = 3000000000 + 1; function main(): i32 { return C; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, nil); err != nil {
		t.Fatalf("Fold: %v", err)
	}
	got := returnLit(t, prog)
	lit, ok := got.(*ast.NumberLit)
	if !ok {
		t.Fatalf("const C folded to %T, want *ast.NumberLit", got)
	}
	if lit.Value != 3000000001 {
		t.Errorf("undeclared const folded to %d, want 3000000001", lit.Value)
	}
}

func TestConstInAParameterDefaultIsFolded(t *testing.T) {
	prog, err := parser.Parse(`const LIMIT: i32 = 128;
function listen(port: i32, backlog: i32 = LIMIT): i32 { return port + backlog; }
function main(): i32 { return listen(80); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, nil); err != nil {
		t.Fatalf("Fold: %v", err)
	}
	var def ast.Expr
	for _, fn := range prog.Funcs {
		if fn.Name == "listen" {
			def = fn.Params[1].Default
		}
	}
	if def == nil {
		t.Fatal("listen has no default on its second parameter")
	}
	lit, ok := def.(*ast.NumberLit)
	if !ok {
		t.Fatalf("default folded to %T, want *ast.NumberLit — an unfolded const is refused by E076 as a free name", def)
	}
	if lit.Value != 128 {
		t.Errorf("default folded to %d, want 128", lit.Value)
	}
}
