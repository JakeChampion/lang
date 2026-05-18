package numeric_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// TestOpcodeBytes asserts every encoder pushes exactly its
// documented opcode. Each entry is { byte-emitter, expected
// opcode }. With 98 ops a switch-style test is too noisy; the
// table-driven shape keeps it compact while still per-row.
func TestOpcodeBytes(t *testing.T) {
	cases := []struct {
		name   string
		fn     func([]byte) []byte
		opcode byte
	}{
		// i32 unary + test
		{"i32.clz", numeric.InstI32Clz, 0x67},
		{"i32.ctz", numeric.InstI32Ctz, 0x68},
		{"i32.popcnt", numeric.InstI32Popcnt, 0x69},
		{"i32.eqz", numeric.InstI32Eqz, 0x45},
		// i32 cmp
		{"i32.eq", numeric.InstI32Eq, 0x46},
		{"i32.ne", numeric.InstI32Ne, 0x47},
		{"i32.lt_s", numeric.InstI32LtS, 0x48},
		{"i32.lt_u", numeric.InstI32LtU, 0x49},
		{"i32.gt_s", numeric.InstI32GtS, 0x4a},
		{"i32.gt_u", numeric.InstI32GtU, 0x4b},
		{"i32.le_s", numeric.InstI32LeS, 0x4c},
		{"i32.le_u", numeric.InstI32LeU, 0x4d},
		{"i32.ge_s", numeric.InstI32GeS, 0x4e},
		{"i32.ge_u", numeric.InstI32GeU, 0x4f},
		// i32 binary
		{"i32.add", numeric.InstI32Add, 0x6a},
		{"i32.sub", numeric.InstI32Sub, 0x6b},
		{"i32.mul", numeric.InstI32Mul, 0x6c},
		{"i32.div_s", numeric.InstI32DivS, 0x6d},
		{"i32.div_u", numeric.InstI32DivU, 0x6e},
		{"i32.rem_s", numeric.InstI32RemS, 0x6f},
		{"i32.rem_u", numeric.InstI32RemU, 0x70},
		{"i32.and", numeric.InstI32And, 0x71},
		{"i32.or", numeric.InstI32Or, 0x72},
		{"i32.xor", numeric.InstI32Xor, 0x73},
		{"i32.shl", numeric.InstI32Shl, 0x74},
		{"i32.shr_s", numeric.InstI32ShrS, 0x75},
		{"i32.shr_u", numeric.InstI32ShrU, 0x76},
		{"i32.rotl", numeric.InstI32Rotl, 0x77},
		{"i32.rotr", numeric.InstI32Rotr, 0x78},
		// i64 unary + test
		{"i64.clz", numeric.InstI64Clz, 0x79},
		{"i64.ctz", numeric.InstI64Ctz, 0x7a},
		{"i64.popcnt", numeric.InstI64Popcnt, 0x7b},
		{"i64.eqz", numeric.InstI64Eqz, 0x50},
		// i64 cmp
		{"i64.eq", numeric.InstI64Eq, 0x51},
		{"i64.ne", numeric.InstI64Ne, 0x52},
		{"i64.lt_s", numeric.InstI64LtS, 0x53},
		{"i64.lt_u", numeric.InstI64LtU, 0x54},
		{"i64.gt_s", numeric.InstI64GtS, 0x55},
		{"i64.gt_u", numeric.InstI64GtU, 0x56},
		{"i64.le_s", numeric.InstI64LeS, 0x57},
		{"i64.le_u", numeric.InstI64LeU, 0x58},
		{"i64.ge_s", numeric.InstI64GeS, 0x59},
		{"i64.ge_u", numeric.InstI64GeU, 0x5a},
		// i64 binary
		{"i64.add", numeric.InstI64Add, 0x7c},
		{"i64.sub", numeric.InstI64Sub, 0x7d},
		{"i64.mul", numeric.InstI64Mul, 0x7e},
		{"i64.div_s", numeric.InstI64DivS, 0x7f},
		{"i64.div_u", numeric.InstI64DivU, 0x80},
		{"i64.rem_s", numeric.InstI64RemS, 0x81},
		{"i64.rem_u", numeric.InstI64RemU, 0x82},
		{"i64.and", numeric.InstI64And, 0x83},
		{"i64.or", numeric.InstI64Or, 0x84},
		{"i64.xor", numeric.InstI64Xor, 0x85},
		{"i64.shl", numeric.InstI64Shl, 0x86},
		{"i64.shr_s", numeric.InstI64ShrS, 0x87},
		{"i64.shr_u", numeric.InstI64ShrU, 0x88},
		{"i64.rotl", numeric.InstI64Rotl, 0x89},
		{"i64.rotr", numeric.InstI64Rotr, 0x8a},
		// f32 cmp
		{"f32.eq", numeric.InstF32Eq, 0x5b},
		{"f32.ne", numeric.InstF32Ne, 0x5c},
		{"f32.lt", numeric.InstF32Lt, 0x5d},
		{"f32.gt", numeric.InstF32Gt, 0x5e},
		{"f32.le", numeric.InstF32Le, 0x5f},
		{"f32.ge", numeric.InstF32Ge, 0x60},
		// f32 unary + binary
		{"f32.abs", numeric.InstF32Abs, 0x8b},
		{"f32.neg", numeric.InstF32Neg, 0x8c},
		{"f32.ceil", numeric.InstF32Ceil, 0x8d},
		{"f32.floor", numeric.InstF32Floor, 0x8e},
		{"f32.trunc", numeric.InstF32Trunc, 0x8f},
		{"f32.nearest", numeric.InstF32Nearest, 0x90},
		{"f32.sqrt", numeric.InstF32Sqrt, 0x91},
		{"f32.add", numeric.InstF32Add, 0x92},
		{"f32.sub", numeric.InstF32Sub, 0x93},
		{"f32.mul", numeric.InstF32Mul, 0x94},
		{"f32.div", numeric.InstF32Div, 0x95},
		{"f32.min", numeric.InstF32Min, 0x96},
		{"f32.max", numeric.InstF32Max, 0x97},
		{"f32.copysign", numeric.InstF32Copysign, 0x98},
		// f64 cmp
		{"f64.eq", numeric.InstF64Eq, 0x61},
		{"f64.ne", numeric.InstF64Ne, 0x62},
		{"f64.lt", numeric.InstF64Lt, 0x63},
		{"f64.gt", numeric.InstF64Gt, 0x64},
		{"f64.le", numeric.InstF64Le, 0x65},
		{"f64.ge", numeric.InstF64Ge, 0x66},
		// f64 unary + binary
		{"f64.abs", numeric.InstF64Abs, 0x99},
		{"f64.neg", numeric.InstF64Neg, 0x9a},
		{"f64.ceil", numeric.InstF64Ceil, 0x9b},
		{"f64.floor", numeric.InstF64Floor, 0x9c},
		{"f64.trunc", numeric.InstF64Trunc, 0x9d},
		{"f64.nearest", numeric.InstF64Nearest, 0x9e},
		{"f64.sqrt", numeric.InstF64Sqrt, 0x9f},
		{"f64.add", numeric.InstF64Add, 0xa0},
		{"f64.sub", numeric.InstF64Sub, 0xa1},
		{"f64.mul", numeric.InstF64Mul, 0xa2},
		{"f64.div", numeric.InstF64Div, 0xa3},
		{"f64.min", numeric.InstF64Min, 0xa4},
		{"f64.max", numeric.InstF64Max, 0xa5},
		{"f64.copysign", numeric.InstF64Copysign, 0xa6},
	}
	for _, c := range cases {
		got := c.fn(nil)
		if !bytes.Equal(got, []byte{c.opcode}) {
			t.Errorf("%s: got % x, want %#x", c.name, got, c.opcode)
		}
	}
}

// TestAppendBehaviour confirms encoders extend an existing
// buffer rather than replacing it — what callers rely on when
// chaining ops.
func TestAppendBehaviour(t *testing.T) {
	buf := []byte{0xaa}
	buf = numeric.InstI32Add(buf)
	buf = numeric.InstI32Mul(buf)
	if !bytes.Equal(buf, []byte{0xaa, 0x6a, 0x6c}) {
		t.Fatalf("got % x", buf)
	}
}
