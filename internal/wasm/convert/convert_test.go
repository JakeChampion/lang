package convert_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/convert"
)

func TestOpcodeBytes(t *testing.T) {
	cases := []struct {
		name   string
		fn     func([]byte) []byte
		opcode byte
	}{
		{"i32.wrap_i64", convert.InstI32WrapI64, 0xa7},
		{"i64.extend_i32_s", convert.InstI64ExtendI32S, 0xac},
		{"i64.extend_i32_u", convert.InstI64ExtendI32U, 0xad},
		{"i32.trunc_f32_s", convert.InstI32TruncF32S, 0xa8},
		{"i32.trunc_f32_u", convert.InstI32TruncF32U, 0xa9},
		{"i32.trunc_f64_s", convert.InstI32TruncF64S, 0xaa},
		{"i32.trunc_f64_u", convert.InstI32TruncF64U, 0xab},
		{"i64.trunc_f32_s", convert.InstI64TruncF32S, 0xae},
		{"i64.trunc_f32_u", convert.InstI64TruncF32U, 0xaf},
		{"i64.trunc_f64_s", convert.InstI64TruncF64S, 0xb0},
		{"i64.trunc_f64_u", convert.InstI64TruncF64U, 0xb1},
		{"f32.convert_i32_s", convert.InstF32ConvertI32S, 0xb2},
		{"f32.convert_i32_u", convert.InstF32ConvertI32U, 0xb3},
		{"f32.convert_i64_s", convert.InstF32ConvertI64S, 0xb4},
		{"f32.convert_i64_u", convert.InstF32ConvertI64U, 0xb5},
		{"f64.convert_i32_s", convert.InstF64ConvertI32S, 0xb7},
		{"f64.convert_i32_u", convert.InstF64ConvertI32U, 0xb8},
		{"f64.convert_i64_s", convert.InstF64ConvertI64S, 0xb9},
		{"f64.convert_i64_u", convert.InstF64ConvertI64U, 0xba},
		{"f32.demote_f64", convert.InstF32DemoteF64, 0xb6},
		{"f64.promote_f32", convert.InstF64PromoteF32, 0xbb},
		{"i32.reinterpret_f32", convert.InstI32ReinterpretF32, 0xbc},
		{"i64.reinterpret_f64", convert.InstI64ReinterpretF64, 0xbd},
		{"f32.reinterpret_i32", convert.InstF32ReinterpretI32, 0xbe},
		{"f64.reinterpret_i64", convert.InstF64ReinterpretI64, 0xbf},
		{"i32.extend8_s", convert.InstI32Extend8S, 0xc0},
		{"i32.extend16_s", convert.InstI32Extend16S, 0xc1},
		{"i64.extend8_s", convert.InstI64Extend8S, 0xc2},
		{"i64.extend16_s", convert.InstI64Extend16S, 0xc3},
		{"i64.extend32_s", convert.InstI64Extend32S, 0xc4},
	}
	for _, c := range cases {
		got := c.fn(nil)
		if !bytes.Equal(got, []byte{c.opcode}) {
			t.Errorf("%s: got % x, want %#x", c.name, got, c.opcode)
		}
	}
}
