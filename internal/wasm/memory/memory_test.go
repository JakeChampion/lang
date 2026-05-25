package memory_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/memory"
)

// TestLoadOpcodes asserts each load opcode + a 1-byte align +
// 1-byte offset round-trip to the expected 3 bytes.
func TestLoadOpcodes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"i32.load", memory.InstI32Load(nil, 2, 0), []byte{0x28, 0x02, 0x00}},
		{"i64.load", memory.InstI64Load(nil, 3, 0), []byte{0x29, 0x03, 0x00}},
		{"f32.load", memory.InstF32Load(nil, 2, 0), []byte{0x2a, 0x02, 0x00}},
		{"f64.load", memory.InstF64Load(nil, 3, 0), []byte{0x2b, 0x03, 0x00}},
		{"i32.load8_s", memory.InstI32Load8S(nil, 0, 0), []byte{0x2c, 0x00, 0x00}},
		{"i32.load8_u", memory.InstI32Load8U(nil, 0, 4), []byte{0x2d, 0x00, 0x04}},
		{"i32.load16_s", memory.InstI32Load16S(nil, 1, 0), []byte{0x2e, 0x01, 0x00}},
		{"i32.load16_u", memory.InstI32Load16U(nil, 1, 0), []byte{0x2f, 0x01, 0x00}},
		{"i64.load8_s", memory.InstI64Load8S(nil, 0, 0), []byte{0x30, 0x00, 0x00}},
		{"i64.load8_u", memory.InstI64Load8U(nil, 0, 0), []byte{0x31, 0x00, 0x00}},
		{"i64.load16_s", memory.InstI64Load16S(nil, 1, 0), []byte{0x32, 0x01, 0x00}},
		{"i64.load16_u", memory.InstI64Load16U(nil, 1, 0), []byte{0x33, 0x01, 0x00}},
		{"i64.load32_s", memory.InstI64Load32S(nil, 2, 0), []byte{0x34, 0x02, 0x00}},
		{"i64.load32_u", memory.InstI64Load32U(nil, 2, 0), []byte{0x35, 0x02, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, c.got, c.want)
		}
	}
}

func TestStoreOpcodes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"i32.store", memory.InstI32Store(nil, 2, 0), []byte{0x36, 0x02, 0x00}},
		{"i64.store", memory.InstI64Store(nil, 3, 0), []byte{0x37, 0x03, 0x00}},
		{"f32.store", memory.InstF32Store(nil, 2, 0), []byte{0x38, 0x02, 0x00}},
		{"f64.store", memory.InstF64Store(nil, 3, 0), []byte{0x39, 0x03, 0x00}},
		{"i32.store8", memory.InstI32Store8(nil, 0, 0), []byte{0x3a, 0x00, 0x00}},
		{"i32.store16", memory.InstI32Store16(nil, 1, 0), []byte{0x3b, 0x01, 0x00}},
		{"i64.store8", memory.InstI64Store8(nil, 0, 0), []byte{0x3c, 0x00, 0x00}},
		{"i64.store16", memory.InstI64Store16(nil, 1, 0), []byte{0x3d, 0x01, 0x00}},
		{"i64.store32", memory.InstI64Store32(nil, 2, 0), []byte{0x3e, 0x02, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % x, want % x", c.name, c.got, c.want)
		}
	}
}

func TestSizeGrow(t *testing.T) {
	if got := memory.InstMemorySize(nil); !bytes.Equal(got, []byte{0x3f, 0x00}) {
		t.Errorf("memory.size: got % x", got)
	}
	if got := memory.InstMemoryGrow(nil); !bytes.Equal(got, []byte{0x40, 0x00}) {
		t.Errorf("memory.grow: got % x", got)
	}
}

// TestBulkMemory pins the 0xFC-prefixed copy / fill encoders to the
// exact byte sequences the backend's memcpy / memset helpers (and
// the std/wasm Lang mirror) emit: copy carries two reserved memidx
// bytes, fill one.
func TestBulkMemory(t *testing.T) {
	if got := memory.InstMemoryCopy(nil); !bytes.Equal(got, []byte{0xFC, 0x0A, 0x00, 0x00}) {
		t.Errorf("memory.copy: got % x", got)
	}
	if got := memory.InstMemoryFill(nil); !bytes.Equal(got, []byte{0xFC, 0x0B, 0x00}) {
		t.Errorf("memory.fill: got % x", got)
	}
}

// TestMemargMultiByteOffset confirms that a larger offset goes
// through the uleb path correctly (e.g. 128 = 2 bytes).
func TestMemargMultiByteOffset(t *testing.T) {
	got := memory.InstI32Load(nil, 2, 128)
	want := []byte{0x28, 0x02, 0x80, 0x01}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}
