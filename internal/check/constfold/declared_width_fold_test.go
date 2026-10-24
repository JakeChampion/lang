package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestConstFoldsAtDeclaredWidth(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{"i32-add-overflows-then-halves", `const C: i32 = (2147483647 + 1) / 2;`, -1073741824},
		{"i32-shift-count-masks-to-31", `const C: i32 = (1 << 33) & 255;`, 2},
		{"u8-add-wraps-at-8-bits", `const C: u8 = (255 + 1) / 2;`, 0},
		{"u32-subtract-wraps", `const C: u32 = 0 - 1;`, 4294967295},
		{"i32-multiply-wraps", `const C: i32 = 100000 * 100000;`, 1410065408},
		{"i64-shift-count-masks-to-63", `const C: i64 = 1 << 64;`, 1},
		{"u32-divide-is-unsigned", `const C: u32 = (0 - 2) / 2;`, 2147483647},
		{"u32-shift-right-is-logical", `const C: u32 = (0 - 1) >> 1;`, 2147483647},
		{"i32-shift-right-is-arithmetic", `const C: i32 = (0 - 8) >> 33;`, -4},
		{"i32-ordinary", `const C: i32 = 6 * 7;`, 42},
		{"i64-wide-value-survives", `const C: i64 = 3000000000 + 1;`, 3000000001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src + " function main(): i32 { return C; }")
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
			if lit.Value != tc.want {
				t.Errorf("%s folded to %d, want %d — the value the same expression produces at runtime", tc.src, lit.Value, tc.want)
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
