package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A void payload takes a pointer-width slot (#8809). Both native runtimes
// build `Result[void, IoError]`'s Ok as a 16-byte box with the unit at +8,
// and the IR frees a box at the size its layout says: at a 4-byte unit slot
// the Ok box was 8 and the Err box 16, so the enum had no uniform size and
// every void-result helper call returned a 32-byte block to the 16-byte
// class. The layout now says what the runtimes emit.
func TestVoidPayloadSlotIsPointerWidth(t *testing.T) {
	result := &ast.EnumDecl{Name: "Result", Variants: []ast.EnumVariant{
		{Name: "Ok", Payloads: []ast.Type{ast.VoidType{}}},
		{Name: "Err", Payloads: []ast.Type{ast.EnumType{Name: "IoError"}}},
	}}
	for _, tc := range []struct {
		ptrW         int
		offset, size int32
	}{
		{ptrW: 8, offset: 8, size: 16},
		{ptrW: 4, offset: 4, size: 8},
	} {
		offs, size := payloadLayout([]ast.Type{ast.VoidType{}}, 1, tc.ptrW)
		if offs[0] != tc.offset || size != tc.size {
			t.Errorf("ptrW=%d: void payload at +%d in a %d-byte box, want +%d in %d", tc.ptrW, offs[0], size, tc.offset, tc.size)
		}
		okSize, ok := enumVariantBoxSize(result, "Ok", tc.ptrW)
		errSize, _ := enumVariantBoxSize(result, "Err", tc.ptrW)
		if !ok || okSize != errSize || okSize != tc.size {
			t.Errorf("ptrW=%d: Ok box %d, Err box %d, want both %d", tc.ptrW, okSize, errSize, tc.size)
		}
		if uniform, ok := uniformEnumBoxSize(result, tc.ptrW); !ok || uniform != tc.size {
			t.Errorf("ptrW=%d: Result[void, IoError] has no uniform box size (%d, %v), want %d", tc.ptrW, uniform, ok, tc.size)
		}
	}
}
