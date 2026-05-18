package inst_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
)

func TestConstants(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"i32 0", inst.InstI32Const(nil, 0), []byte{0x41, 0x00}},
		{"i32 42", inst.InstI32Const(nil, 42), []byte{0x41, 0x2a}},
		{"i32 -1", inst.InstI32Const(nil, -1), []byte{0x41, 0x7f}},
		{"i64 0", inst.InstI64Const(nil, 0), []byte{0x42, 0x00}},
		{"i64 -1", inst.InstI64Const(nil, -1), []byte{0x42, 0x7f}},
		// f32 1.0 bit pattern is 0x3f800000 (little-endian: 00 00 80 3f).
		{"f32 1.0", inst.InstF32Const(nil, 0x3f800000), []byte{0x43, 0x00, 0x00, 0x80, 0x3f}},
		// f64 2.0 bit pattern is 0x4000000000000000.
		{"f64 2.0", inst.InstF64Const(nil, 0x4000000000000000),
			[]byte{0x44, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x40}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, c.got, c.want)
		}
	}
}

func TestParametric(t *testing.T) {
	if got := inst.InstDrop(nil); !bytes.Equal(got, []byte{0x1a}) {
		t.Errorf("drop: got % x", got)
	}
	if got := inst.InstSelect(nil); !bytes.Equal(got, []byte{0x1b}) {
		t.Errorf("select: got % x", got)
	}
}

func TestVariableOps(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"local.get 0", inst.InstLocalGet(nil, 0), []byte{0x20, 0x00}},
		{"local.set 5", inst.InstLocalSet(nil, 5), []byte{0x21, 0x05}},
		{"local.tee 128", inst.InstLocalTee(nil, 128), []byte{0x22, 0x80, 0x01}},
		{"global.get 1", inst.InstGlobalGet(nil, 1), []byte{0x23, 0x01}},
		{"global.set 0", inst.InstGlobalSet(nil, 0), []byte{0x24, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, c.got, c.want)
		}
	}
}

func TestControlFlow(t *testing.T) {
	if got := inst.InstUnreachable(nil); !bytes.Equal(got, []byte{0x00}) {
		t.Errorf("unreachable: got % x", got)
	}
	if got := inst.InstNop(nil); !bytes.Equal(got, []byte{0x01}) {
		t.Errorf("nop: got % x", got)
	}
	if got := inst.InstBlockStart(nil, inst.BlocktypeEmpty); !bytes.Equal(got, []byte{0x02, 0x40}) {
		t.Errorf("block: got % x", got)
	}
	if got := inst.InstLoopStart(nil, encode.ValtypeI32); !bytes.Equal(got, []byte{0x03, 0x7f}) {
		t.Errorf("loop: got % x", got)
	}
	if got := inst.InstIfStart(nil, encode.ValtypeI32); !bytes.Equal(got, []byte{0x04, 0x7f}) {
		t.Errorf("if: got % x", got)
	}
	if got := inst.InstElse(nil); !bytes.Equal(got, []byte{0x05}) {
		t.Errorf("else: got % x", got)
	}
	if got := inst.InstEnd(nil); !bytes.Equal(got, []byte{0x0b}) {
		t.Errorf("end: got % x", got)
	}
	if got := inst.InstReturn(nil); !bytes.Equal(got, []byte{0x0f}) {
		t.Errorf("return: got % x", got)
	}
	if got := inst.InstBr(nil, 2); !bytes.Equal(got, []byte{0x0c, 0x02}) {
		t.Errorf("br: got % x", got)
	}
	if got := inst.InstBrIf(nil, 0); !bytes.Equal(got, []byte{0x0d, 0x00}) {
		t.Errorf("br_if: got % x", got)
	}
	if got := inst.InstCall(nil, 7); !bytes.Equal(got, []byte{0x10, 0x07}) {
		t.Errorf("call: got % x", got)
	}
	if got := inst.InstCallIndirect(nil, 3, 0); !bytes.Equal(got, []byte{0x11, 0x03, 0x00}) {
		t.Errorf("call_indirect: got % x", got)
	}
}

func TestLocalsHelpers(t *testing.T) {
	if got := inst.PutLocalsEmpty(nil); !bytes.Equal(got, []byte{0x00}) {
		t.Errorf("empty: got % x", got)
	}
	// 4 i32 locals: count=1, group_count=4, valtype=i32.
	got := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	want := []byte{0x01, 0x04, 0x7f}
	if !bytes.Equal(got, want) {
		t.Errorf("one_group: got % x, want % x", got, want)
	}
}

func TestPutFunctionBody(t *testing.T) {
	// Empty body except the end byte that this helper appends.
	got := inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), nil)
	// inner = locals_empty(0x00) + end(0x0b) = 2 bytes. size_prefix = 0x02.
	want := []byte{0x02, 0x00, 0x0b}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty body: got % x, want % x", got, want)
	}

	// Body = i32.const 42; result type implied by code section.
	body := inst.InstI32Const(nil, 42)
	got = inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
	// inner = 00 (locals) + 41 2a (i32.const 42) + 0b (end) = 4 bytes.
	want = []byte{0x04, 0x00, 0x41, 0x2a, 0x0b}
	if !bytes.Equal(got, want) {
		t.Fatalf("const body: got % x, want % x", got, want)
	}
}

func TestPutBlocktypeTypeidx(t *testing.T) {
	// sleb_i32(0) = single byte 0x00.
	if got := inst.PutBlocktypeTypeidx(nil, 0); !bytes.Equal(got, []byte{0x00}) {
		t.Errorf("idx 0: got % x", got)
	}
	if got := inst.PutBlocktypeTypeidx(nil, 64); !bytes.Equal(got, []byte{0xc0, 0x00}) {
		t.Errorf("idx 64: got % x", got)
	}
}
