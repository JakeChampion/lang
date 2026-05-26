package arm64_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// Reference encodings from llvm-mc:
//
//	$ printf 'movz x8,#93\nmovz x0,#42\nmovz x0,#0\nsvc #0\nret\n' \
//	    | llvm-mc -triple=aarch64 --show-encoding
//
// llvm-mc prints little-endian byte tuples; the uint32 values below
// are those bytes read back little-endian (e.g. [0xa8,0x0b,0x80,0xd2]
// → 0xd2800ba8).
func TestEncodings(t *testing.T) {
	cases := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"movz x8, #93", arm64.MOVZ(8, 93, 0), 0xd2800ba8},
		{"movz x0, #42", arm64.MOVZ(0, 42, 0), 0xd2800540},
		{"movz x0, #0", arm64.MOVZ(0, 0, 0), 0xd2800000},
		{"movz x1, #1, lsl #16", arm64.MOVZ(1, 1, 16), 0xd2a00021},
		{"movk x0, #1, lsl #16", arm64.MOVK(0, 1, 16), 0xf2a00020},
		{"movk x3, #0xabcd", arm64.MOVK(3, 0xabcd, 0), 0xf29579a3},
		{"movn x0, #0", arm64.MOVN(0, 0, 0), 0x92800000},
		{"movn x5, #7, lsl #32", arm64.MOVN(5, 7, 32), 0x92c000e5},
		{"add x0, x1, #1", arm64.ADDimm(0, 1, 1, false), 0x91000420},
		{"add x0, x1, #4095", arm64.ADDimm(0, 1, 4095, false), 0x913ffc20},
		{"add x2, x3, #1, lsl #12", arm64.ADDimm(2, 3, 1, true), 0x91400462},
		{"sub x0, x1, #1", arm64.SUBimm(0, 1, 1, false), 0xd1000420},
		{"add x0, x1, x2", arm64.ADDreg(0, 1, 2), 0x8b020020},
		{"sub x4, x5, x6", arm64.SUBreg(4, 5, 6), 0xcb0600a4},
		{"mov x0, x1", arm64.MOVreg(0, 1), 0xaa0103e0},
		{"svc #0", arm64.SVC(0), 0xd4000001},
		{"ret (x30)", arm64.RET(30), 0xd65f03c0},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %#08x, want %#08x", c.name, c.got, c.want)
		}
	}
}

// TestPutLittleEndian confirms Put lays the word down low-byte-first.
func TestPutLittleEndian(t *testing.T) {
	got := arm64.Put(nil, 0xd2800ba8)
	want := []byte{0xa8, 0x0b, 0x80, 0xd2}
	if len(got) != 4 {
		t.Fatalf("got %d bytes, want 4", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %#02x, want %#02x (full % x)", i, got[i], want[i], got)
		}
	}
}
