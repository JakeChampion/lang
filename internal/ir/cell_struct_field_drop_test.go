package ir

import (
	"testing"
)

// A `Cell[T]` held in a struct FIELD reclaims through the array machinery,
// like a cell local does — not through the generic child-drop, which dec'd
// the box and never released the slot.
//
// A cell is a one-element array box, so a `Cell[string]` field's buffer is
// only freed by the string-aware per-element walk; `__fern_rc_dec` on the
// box decrements and returns, stranding the buffer. The struct's drop is the
// only place that reference is ever released, so nothing else can make up
// for it. Checked at the op level: which helper the drop calls decides
// whether the buffer is freed, and the runtime difference is one unpaired
// allocation that no exit code shows.
func TestStructFieldCellDropsThroughArrayMachinery(t *testing.T) {
	const src = `struct Box { c: Cell[string] }
function build(): i32 {
    var b: Box = Box { c: cell_new("") };
    b.c.set(b.c.get() + "one;");
    return b.c.get().len();
}`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		drop := funcOpsOf(p, "__drop_struct_Box")
		if len(drop) == 0 {
			t.Fatalf("ptrW=%d: no __drop_struct_Box in the lowering:\n%s", ptrW, p)
		}
		found := false
		for _, op := range drop {
			if isNamedCallKind(op.Kind) && op.Str == "__fern_drop_arr_str" {
				found = true
			}
		}
		if !found {
			t.Errorf("ptrW=%d: __drop_struct_Box never calls __fern_drop_arr_str, so the cell's string slot is never released:\n%s",
				ptrW, p)
		}
	}
}

// A `Cell[i32]` field takes the same route with the scalar helper: the box
// itself is what has to be freed, and __fern_arr_dec is what frees a cell
// box (its 16-byte array-style header makes __fern_box_free's data-8 base
// wrong).
func TestStructFieldScalarCellDropsThroughArrayMachinery(t *testing.T) {
	const src = `struct Box { c: Cell[i32] }
function build(): i32 {
    var b: Box = Box { c: cell_new(0) };
    b.c.set(b.c.get() + 7);
    return b.c.get();
}`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		drop := funcOpsOf(p, "__drop_struct_Box")
		if len(drop) == 0 {
			t.Fatalf("ptrW=%d: no __drop_struct_Box in the lowering:\n%s", ptrW, p)
		}
		found := false
		for _, op := range drop {
			if isNamedCallKind(op.Kind) && op.Str == "__fern_arr_dec" {
				found = true
			}
		}
		if !found {
			t.Errorf("ptrW=%d: __drop_struct_Box never calls __fern_arr_dec, so the cell box is never freed:\n%s",
				ptrW, p)
		}
	}
}
