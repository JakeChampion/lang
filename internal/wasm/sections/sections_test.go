package sections_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

func TestEncodeCustomSection(t *testing.T) {
	// id 0 + uleb(6) + uleb(3) + "foo" + 0xAA 0xBB.
	got := sections.EncodeCustomSection(nil, "foo", []byte{0xaa, 0xbb})
	want := []byte{0x00, 0x06, 0x03, 'f', 'o', 'o', 0xaa, 0xbb}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	// Empty payload — body is just the name.
	got = sections.EncodeCustomSection(nil, "x", nil)
	want = []byte{0x00, 0x02, 0x01, 'x'}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty: got % x, want % x", got, want)
	}
}

func TestEncodeTypeSection(t *testing.T) {
	// Two types: () -> i32 and (i32, i32) -> i32.
	params := [][]byte{nil, {encode.ValtypeI32, encode.ValtypeI32}}
	results := [][]byte{{encode.ValtypeI32}, {encode.ValtypeI32}}
	got := sections.EncodeTypeSection(nil, params, results)
	want := []byte{
		0x01, 0x0b, // id 1 (type), size 11
		0x02,                   // count
		0x60, 0x00, 0x01, 0x7f, // () -> i32
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f, // (i32,i32) -> i32
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeFunctionSection(t *testing.T) {
	got := sections.EncodeFunctionSection(nil, []uint32{0, 1, 0})
	want := []byte{
		0x03, 0x04, // id 3 (function), size 4
		0x03, 0x00, 0x01, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeMemorySection(t *testing.T) {
	// No max: flag 0 + min.
	got := sections.EncodeMemorySection(nil, 1, -1)
	want := []byte{0x05, 0x03, 0x01, 0x00, 0x01}
	if !bytes.Equal(got, want) {
		t.Fatalf("no-max: got % x, want % x", got, want)
	}
	// With max: flag 1 + min + max.
	got = sections.EncodeMemorySection(nil, 1, 16)
	want = []byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x10}
	if !bytes.Equal(got, want) {
		t.Fatalf("with-max: got % x, want % x", got, want)
	}
}

func TestEncodeTableSection(t *testing.T) {
	// No max: flag 0 + min slots.
	got := sections.EncodeTableSection(nil, 2, -1)
	// id 4, size 4, count 1, reftype 0x70 (funcref), flag 0, min 2.
	want := []byte{0x04, 0x04, 0x01, 0x70, 0x00, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("no-max: got % x, want % x", got, want)
	}
	// With max: flag 1 + min + max.
	got = sections.EncodeTableSection(nil, 2, 10)
	want = []byte{0x04, 0x05, 0x01, 0x70, 0x01, 0x02, 0x0a}
	if !bytes.Equal(got, want) {
		t.Fatalf("with-max: got % x, want % x", got, want)
	}
}

func TestEncodeElementSection(t *testing.T) {
	// One active segment, table 0, offset 0, two func indices.
	got := sections.EncodeElementSection(nil,
		[]int32{0}, [][]uint32{{0, 1}})
	want := []byte{
		0x09, 0x08, // id 9 (element), size 8
		0x01,             // segment count
		0x00,             // flag: active, table 0
		0x41, 0x00, 0x0b, // i32.const 0; end
		0x02, 0x00, 0x01, // vec(funcidx): 2, funcidx 0, funcidx 1
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeGlobalSection(t *testing.T) {
	// Single i32 const = 42: init expr `i32.const 42 ; end`.
	initExpr := []byte{0x41, 0x2a, 0x0b}
	got := sections.EncodeGlobalSection(nil,
		[]byte{encode.ValtypeI32}, []byte{0x00}, [][]byte{initExpr})
	want := []byte{
		0x06, 0x06, // id 6 (global), size 6
		0x01,       // count
		0x7f, 0x00, // i32, mut const
		0x41, 0x2a, 0x0b, // i32.const 42; end
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeExportSection(t *testing.T) {
	got := sections.EncodeExportSection(nil,
		[]string{"main"}, []byte{sections.ExportFunc}, []uint32{0})
	want := []byte{
		0x07, 0x08, // id 7 (export), size 8
		0x01,                     // count
		0x04, 'm', 'a', 'i', 'n', // name
		0x00, 0x00, // kind func, idx 0
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeStartSection(t *testing.T) {
	got := sections.EncodeStartSection(nil, 5)
	want := []byte{0x08, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeCodeSection(t *testing.T) {
	// Two prebuilt bodies (already size-prefixed).
	bodies := [][]byte{
		{0x02, 0x00, 0x0b},             // size 2, no locals, end
		{0x04, 0x00, 0x41, 0x2a, 0x0b}, // size 4, no locals, i32.const 42, end
	}
	got := sections.EncodeCodeSection(nil, bodies)
	want := []byte{
		0x0a, 0x09, // id 10 (code), size 9
		0x02, // count
		0x02, 0x00, 0x0b,
		0x04, 0x00, 0x41, 0x2a, 0x0b,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeDataSection(t *testing.T) {
	// One segment at offset 0 with bytes [1, 2, 3].
	got := sections.EncodeDataSection(nil,
		[]int32{0}, [][]byte{{0x01, 0x02, 0x03}})
	want := []byte{
		0x0b, 0x09, // id 11 (data), size 9
		0x01,             // count
		0x00,             // memidx
		0x41, 0x00, 0x0b, // i32.const 0; end
		0x03, 0x01, 0x02, 0x03, // length + bytes
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}
