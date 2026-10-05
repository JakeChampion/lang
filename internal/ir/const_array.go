package ir

import (
	"encoding/binary"
	"math"
	"strconv"

	"github.com/jakechampion/lang/internal/ast"
)

// constArrayOp is the OpConstArray a `const` array literal lowers to: its
// elements laid out exactly as the ArrayLit lowering's stores would write
// them. False when an element is not a scalar literal, when the element
// type's store does not fill its stride, or when the function hands array
// storage to the raw floor, which writes through it without a uniqueness
// test.
func (b *builder) constArrayOp(n *ast.ArrayLit) (Op, bool) {
	if !n.Const || n.ElemType == nil || ast.IsPointerType(n.ElemType) {
		return Op{}, false
	}
	stride := ast.ElemSizeBytesFor(n.ElemType, b.ptrW)
	store := arrayElemStoreOpFor(n.ElemType, b.ptrW)
	data := make([]byte, 0, len(n.Elems)*stride)
	for _, el := range n.Elems {
		v, ok := b.scalarLitOp(el)
		if !ok {
			return Op{}, false
		}
		at := len(data)
		data, ok = appendStored(data, store, v, b.ptrW)
		if !ok || len(data)-at != stride {
			return Op{}, false
		}
	}
	if b.reachesRawFloor() {
		return Op{}, false
	}
	return Op{Kind: OpConstArray, I32: int32(len(n.Elems)), Str: string(data)}, true
}

// appendStored appends the bytes `store` writes for the constant v, or
// reports false for a pairing the backends would not store as one plain
// little-endian value.
func appendStored(data []byte, store, v Op, ptrW int) ([]byte, bool) {
	width := store.Width
	if store.Kind == OpStore && width == WidthPtr {
		width = 8 * ptrW
	}
	switch {
	case store.Kind == OpStoreI8 && v.Kind == OpConstI32:
		return append(data, byte(v.I32)), true
	case store.Kind == OpStore && (width == 0 || width == 32) && v.Kind == OpConstI32:
		return binary.LittleEndian.AppendUint32(data, uint32(v.I32)), true
	case store.Kind == OpStore && width == 64 && v.Kind == OpConstI64:
		return binary.LittleEndian.AppendUint64(data, uint64(v.I64)), true
	case store.Kind == OpFStore && width == 0 && v.Kind == OpConstF32:
		return binary.LittleEndian.AppendUint32(data, math.Float32bits(v.F32)), true
	case store.Kind == OpFStore && width == 64 && v.Kind == OpConstF64:
		return binary.LittleEndian.AppendUint64(data, math.Float64bits(v.F64)), true
	}
	return data, false
}

// reachesRawFloor reports whether the function being lowered gives array
// storage to the raw floor: casts an array to its address, or calls a
// builtin that writes into an array argument. Such a function keeps building
// its constants fresh, as the self-host's raw_reached does for the literals
// it traces there. A body with no declaration to read is assumed to.
func (b *builder) reachesRawFloor() bool {
	if b.fn == nil {
		return true
	}
	if b.rawFloorFn != b.fn {
		b.rawFloorFn = b.fn
		b.rawFloor = fnReachesRawFloor(b.fn)
	}
	return b.rawFloor
}

// rawArrayWriters are the builtins that write into an array argument.
var rawArrayWriters = map[string]bool{
	"__arr_set_len": true,
	"tcp_recv_into": true,
	"reactor_wait":  true,
}

func fnReachesRawFloor(fn *ast.FuncDecl) bool {
	found := false
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok && x.Method == nil && rawArrayWriters[id.Name] {
				found = true
			}
		case *ast.CastExpr:
			switch x.InnerType.(type) {
			case ast.ArrayType, ast.SliceType:
				found = true
			}
		}
		return !found
	})
	return found
}

// ConstArrayKey identifies an OpConstArray's content: two ops with the same
// key can share one static array.
func ConstArrayKey(op Op) string {
	return strconv.Itoa(int(op.I32)) + ":" + op.Str
}
