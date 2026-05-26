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
