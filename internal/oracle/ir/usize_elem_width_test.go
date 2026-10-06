package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// A usize element strides, stores and loads at the pointer width together;
// the stride alone moving to 8 would still drop the high word (#10753).
func TestUsizeElementWidthIsPointerWidth(t *testing.T) {
	usize := ast.NumberType{Width: ast.WidthPtr, Signed: false}
	for _, ptrW := range []int{4, 8} {
		if got := ast.ElemSizeBytesFor(usize, ptrW); got != ptrW {
			t.Errorf("ptrW=%d: ElemSizeBytesFor(usize) = %d, want %d", ptrW, got, ptrW)
		}
		if op := arrayElemStoreOpFor(usize, ptrW); op.Kind != OpStore || op.Width != WidthPtr {
			t.Errorf("ptrW=%d: arrayElemStoreOpFor(usize) = %v/%d, want OpStore/WidthPtr", ptrW, op.Kind, op.Width)
		}
		if kind, width := arraySetStoreOp(usize, ptrW); kind != OpStore || width != WidthPtr {
			t.Errorf("ptrW=%d: arraySetStoreOp(usize) = %v/%d, want OpStore/WidthPtr", ptrW, kind, width)
		}
		if op := payloadLoadOpFor(usize, ptrW); op.Kind != OpLoad || op.Width != WidthPtr {
			t.Errorf("ptrW=%d: payloadLoadOpFor(usize) = %v/%d, want OpLoad/WidthPtr", ptrW, op.Kind, op.Width)
		}
	}
}
