package checker

import (
	"strings"
	"testing"
)

func TestByteCellElementBoundary(t *testing.T) {
	for _, src := range []string{
		"let c: Cell[u8[]] = cell_new([0 as u8, 255 as u8]); c.set(c.get());",
		"let bytes: u8[] = []; let c = cell_new(bytes); c.set([255 as u8]);",
	} {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		ty      string
		refused bool
	}{
		{"string[]", false},
		{"u8[][]", false},
		{"(u8, u8)", false},
		{"[u8]", true},
		{"Cell[u8[]]", true},
	} {
		t.Run(tc.ty, func(t *testing.T) {
			err := checkSource(t, "function f(c: Cell["+tc.ty+"]): i32 { return 0; } function main(): i32 { return 0; }")
			refused := err != nil && strings.Contains(err.Error(), cellElemRefusal)
			if refused != tc.refused || (err != nil && !refused) {
				t.Fatalf("Cell[%s]: refused=%v, want %v (err %v)", tc.ty, refused, tc.refused, err)
			}
		})
	}
}

func TestScalarArrayCellElementBoundary(t *testing.T) {
	for _, ty := range []string{"u8", "i32", "u32", "i64", "u64", "usize", "f32", "f64", "float", "boolean"} {
		t.Run(ty, func(t *testing.T) {
			src := "function main(): i32 { let a: " + ty + "[] = []; let c: Cell[" + ty + "[]] = cell_new(a); let inferred = cell_new(a); c.set(inferred.get()); return c.get().len(); }"
			if err := checkSource(t, src); err != nil {
				t.Fatal(err)
			}
		})
	}
}
