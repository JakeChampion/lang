package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/oracle/ir"
)

// A read of a `const` array is one static array, not a fresh box per
// evaluation (#11471). The elements are laid out as the heap literal's
// stores would write them.
func TestConstArrayLowersToStaticArray(t *testing.T) {
	cases := []struct {
		name, decl, elem string
		count            int32
		bytes            string
	}{
		{"u8", "const SET: u8[] = [1u8, 0u8, 255u8];", "u8", 3, "\x01\x00\xff"},
		{"i32", "const SET: i32[] = [10, -2];", "i32", 2, "\x0a\x00\x00\x00\xfe\xff\xff\xff"},
		{"i64", "const SET: i64[] = [1i64, -1i64];", "i64", 2, "\x01\x00\x00\x00\x00\x00\x00\x00\xff\xff\xff\xff\xff\xff\xff\xff"},
		{"f64", "const SET: f64[] = [1.5];", "f64", 1, "\x00\x00\x00\x00\x00\x00\xf8\x3f"},
		{"empty", "const SET: u8[] = [];", "u8", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.decl + "\nfunction f(i: i32): " + tc.elem + "[] { return SET; }\n"
			ops := lowerFuncOps(t, src, "f")
			at := firstIndex(ops, func(op ir.Op) bool { return op.Kind == ir.OpConstArray })
			if at < 0 {
				t.Fatalf("no const.arr in f:\n%v", ops)
			}
			if got := ops[at]; got.I32 != tc.count || got.Str != tc.bytes {
				t.Errorf("const.arr len=%d % x, want len=%d % x", got.I32, got.Str, tc.count, tc.bytes)
			}
			if firstIndex(ops, func(op ir.Op) bool { return op.Kind == ir.OpAlloc }) >= 0 {
				t.Errorf("f still allocates the constant:\n%v", ops)
			}
		})
	}
}

// Only a constant's storage is shared: a literal written in the body is a
// fresh array its owner may grow in place, and a constant of pointer-shaped
// elements is not a block of plain bytes.
func TestConstArrayIsOnlyAConstantOfScalars(t *testing.T) {
	cases := []struct{ name, src string }{
		{"literal", "function f(): u8[] { return [1u8, 2u8]; }\n"},
		{"strings", "const SET: string[] = [\"a\", \"b\"];\nfunction f(): string[] { return SET; }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops := lowerFuncOps(t, tc.src, "f")
			if firstIndex(ops, func(op ir.Op) bool { return op.Kind == ir.OpConstArray }) >= 0 {
				t.Errorf("lowered to a static array:\n%v", ops)
			}
		})
	}
}

// A function that hands array storage to the raw floor builds its constants
// fresh: the floor writes without a uniqueness test, and a static array is
// shared by every evaluation.
func TestConstArrayStaysFreshWhereTheRawFloorWrites(t *testing.T) {
	cases := []struct{ name, body string }{
		{"set_len", "let a: u8[] = SET; __arr_set_len(a, 1); return a;"},
		{"address", "let a: u8[] = SET; let p: usize = a as usize; return a;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "const SET: u8[] = [1u8, 2u8];\nfunction f(): u8[] { " + tc.body + " }\n"
			ops := lowerFuncOps(t, src, "f")
			if firstIndex(ops, func(op ir.Op) bool { return op.Kind == ir.OpConstArray }) >= 0 {
				t.Errorf("lowered to a static array the raw floor then writes:\n%v", ops)
			}
		})
	}
}
