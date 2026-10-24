package leb128_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/leb128"
)

func TestUlebU32SpecVectors(t *testing.T) {
	cases := []struct {
		v    uint32
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xff, 0x01}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{624485, []byte{0xe5, 0x8e, 0x26}},
		{0xffffffff, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
	}
	for _, c := range cases {
		got := leb128.UlebU32(nil, c.v)
		if !bytes.Equal(got, c.want) {
			t.Errorf("UlebU32(%d) = % x, want % x", c.v, got, c.want)
		}
		if leb128.UlebSizeU32(c.v) != len(c.want) {
			t.Errorf("UlebSizeU32(%d) = %d, want %d", c.v, leb128.UlebSizeU32(c.v), len(c.want))
		}
	}
}

func TestUlebU64SpecVectors(t *testing.T) {
	cases := []struct {
		v    uint64
		want []byte
	}{
		{0, []byte{0x00}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{0xffffffff, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{0xffffffffffffffff, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	}
	for _, c := range cases {
		got := leb128.UlebU64(nil, c.v)
		if !bytes.Equal(got, c.want) {
			t.Errorf("UlebU64(%d) = % x, want % x", c.v, got, c.want)
		}
		if leb128.UlebSizeU64(c.v) != len(c.want) {
			t.Errorf("UlebSizeU64(%d) = %d, want %d", c.v, leb128.UlebSizeU64(c.v), len(c.want))
		}
	}
}

func TestSlebI32SpecVectors(t *testing.T) {
	cases := []struct {
		v    int32
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{-1, []byte{0x7f}},
		{63, []byte{0x3f}},
		{64, []byte{0xc0, 0x00}},
		{-64, []byte{0x40}},
		{-65, []byte{0xbf, 0x7f}},
		{-12345, []byte{0xc7, 0x9f, 0x7f}},
		{2147483647, []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{-2147483648, []byte{0x80, 0x80, 0x80, 0x80, 0x78}},
	}
	for _, c := range cases {
		got := leb128.SlebI32(nil, c.v)
		if !bytes.Equal(got, c.want) {
			t.Errorf("SlebI32(%d) = % x, want % x", c.v, got, c.want)
		}
	}
}

func TestSlebI64SpecVectors(t *testing.T) {
	cases := []struct {
		v    int64
		want []byte
	}{
		{0, []byte{0x00}},
		{-1, []byte{0x7f}},
		{64, []byte{0xc0, 0x00}},
		{-64, []byte{0x40}},
		{9223372036854775807, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}},
		{-9223372036854775808, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}},
	}
	for _, c := range cases {
		got := leb128.SlebI64(nil, c.v)
		if !bytes.Equal(got, c.want) {
			t.Errorf("SlebI64(%d) = % x, want % x", c.v, got, c.want)
		}
	}
}

func TestAppendBehaviour(t *testing.T) {
	buf := []byte{0xaa, 0xbb}
	buf = leb128.UlebU32(buf, 128)
	want := []byte{0xaa, 0xbb, 0x80, 0x01}
	if !bytes.Equal(buf, want) {
		t.Fatalf("append: got % x, want % x", buf, want)
	}
}
