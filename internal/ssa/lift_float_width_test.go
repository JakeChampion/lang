package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The lift carries an f32 op's width onto the SSA op. It is the only source of
// that information downstream: the constant folders round to f32 when they see
// Width 32, and the backends emit their fcvt round trip on the same signal. The
// lift used to propagate only Width 64 — on the theory that "floats carry width
// in their kind" — which silently left every f32 computation on the SSA path
// running at f64 precision.
//
// Integer ops keep the opposite convention: 32 is the default and stays 0, the
// value maskFix reads. Asserting both directions here keeps a future "just
// propagate every width" change from breaking the integer side.
func TestLiftCarriesFloatWidth(t *testing.T) {
	cases := []struct {
		name    string
		irKind  ir.OpKind
		ssaKind OpKind
		width   int
		want    int8
	}{
		{"f32 mul keeps 32", ir.OpFMul, OpFMul, 32, 32},
		{"f32 add keeps 32", ir.OpFAdd, OpFAdd, 32, 32},
		{"f32 sub keeps 32", ir.OpFSub, OpFSub, 32, 32},
		{"f32 div keeps 32", ir.OpFDiv, OpFDiv, 32, 32},
		{"f64 mul keeps 64", ir.OpFMul, OpFMul, 64, 64},
		{"i32 mul stays 0", ir.OpMul, OpMul, 32, 0},
		{"i64 mul keeps 64", ir.OpMul, OpMul, 64, 64},
		// A float COMPARISON yields a bool, so there is nothing to round and
		// the width is deliberately not propagated.
		{"f32 compare stays 0", ir.OpFLt, OpFLt, 32, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &ir.Func{
				Name: "main",
				Ops: []ir.Op{
					{Kind: ir.OpConstF64, F64: 1.5},
					{Kind: ir.OpConstF64, F64: 2.5},
					{Kind: c.irKind, Width: c.width},
					{Kind: ir.OpReturn},
				},
			}
			f, err := LiftFromIR(fn)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			var found *Op
			for _, b := range f.Blocks {
				for _, op := range b.Ops {
					if op.Kind == c.ssaKind {
						found = op
					}
				}
			}
			if found == nil {
				t.Fatalf("no %v op in lifted function", c.ssaKind)
			}
			if found.Width != c.want {
				t.Errorf("%v Width = %d, want %d", c.ssaKind, found.Width, c.want)
			}
		})
	}
}

// An f32 occupies 4 bytes in memory and holds an f32 bit pattern there, while
// an SSA float value is an f64 bit pattern whatever its type. The lift is where
// those two conventions meet, so an f32 access has to become a narrow access
// plus the reinterpret that converts between them. Emitting the 8-byte float
// access for both widths made every f32 store flatten the three slots after it.
func TestLiftFloatMemoryWidthPicksTheAccess(t *testing.T) {
	storeKinds := func(width int) []OpKind {
		fn := &ir.Func{
			Name: "main",
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 64},
				{Kind: ir.OpConstF64, F64: 1.5},
				{Kind: ir.OpFStore, Width: width},
				{Kind: ir.OpConstI32, I32: 0},
				{Kind: ir.OpReturn},
			},
		}
		return liftedKinds(t, fn)
	}
	loadKinds := func(width int) []OpKind {
		fn := &ir.Func{
			Name: "main",
			Ops: []ir.Op{
				{Kind: ir.OpConstI32, I32: 64},
				{Kind: ir.OpFLoad, Width: width},
				{Kind: ir.OpDrop},
				{Kind: ir.OpConstI32, I32: 0},
				{Kind: ir.OpReturn},
			},
		}
		return liftedKinds(t, fn)
	}

	cases := []struct {
		name string
		got  []OpKind
		want OpKind
		deny OpKind
	}{
		{"f32 store is narrow", storeKinds(0), OpStore32, OpStoreF},
		{"f64 store is wide", storeKinds(64), OpStoreF, OpStore32},
		{"f32 load is narrow", loadKinds(0), OpLoad32U, OpLoadF},
		{"f64 load is wide", loadKinds(64), OpLoadF, OpLoad32U},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !containsKind(c.got, c.want) {
				t.Errorf("lifted to %v, want it to contain %v", c.got, c.want)
			}
			if containsKind(c.got, c.deny) {
				t.Errorf("lifted to %v, want no %v — that is the other width's access",
					c.got, c.deny)
			}
		})
	}

	// The narrow access is only correct paired with its reinterpret: without it
	// the 4 bytes in memory would be the low half of an f64 pattern.
	if got := storeKinds(0); !containsKind(got, OpReinterpretF32ToI32) {
		t.Errorf("f32 store lifted to %v, want the f32->i32 reinterpret before the store", got)
	}
	if got := loadKinds(0); !containsKind(got, OpReinterpretI32ToF32) {
		t.Errorf("f32 load lifted to %v, want the i32->f32 reinterpret after the load", got)
	}
}

// int -> float carries the DESTINATION float width, and the lift has to
// propagate it for the same reason it propagates the width of f32 arithmetic:
// the folders and the backends round to f32 only when they see 32. Without it
// `16777217 as f32` kept every bit instead of rounding to 16777216.
func TestLiftCarriesIntToFloatWidth(t *testing.T) {
	cases := []struct {
		name    string
		irKind  ir.OpKind
		ssaKind OpKind
		width   int
		want    int8
	}{
		{"i32 to f32 keeps 32", ir.OpFConvertI32, OpIToFS, 32, 32},
		{"i64 to f32 keeps 32", ir.OpFConvertI64, OpIToFS, 32, 32},
		{"i32 to f64 stays 0", ir.OpFConvertI32, OpIToFS, 64, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &ir.Func{
				Name: "main",
				Ops: []ir.Op{
					{Kind: ir.OpConstI32, I32: 16777217},
					{Kind: c.irKind, Width: c.width},
					{Kind: ir.OpDrop},
					{Kind: ir.OpConstI32, I32: 0},
					{Kind: ir.OpReturn},
				},
			}
			f, err := LiftFromIR(fn)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			var found *Op
			for _, b := range f.Blocks {
				for _, op := range b.Ops {
					if op.Kind == c.ssaKind {
						found = op
					}
				}
			}
			if found == nil {
				t.Fatalf("no %v in the lifted function", c.ssaKind)
			}
			if found.Width != c.want {
				t.Errorf("%v.Width = %d, want %d", c.ssaKind, found.Width, c.want)
			}
		})
	}
}

func liftedKinds(t *testing.T, fn *ir.Func) []OpKind {
	t.Helper()
	f, err := LiftFromIR(fn)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	var kinds []OpKind
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			kinds = append(kinds, op.Kind)
		}
	}
	return kinds
}

func containsKind(kinds []OpKind, want OpKind) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}
