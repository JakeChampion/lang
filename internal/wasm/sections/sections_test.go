package sections_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

func TestEncodeCustomSection(t *testing.T) {
	got := sections.EncodeCustomSection(nil, "foo", []byte{0xaa, 0xbb})
	want := []byte{0x00, 0x06, 0x03, 'f', 'o', 'o', 0xaa, 0xbb}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	got = sections.EncodeCustomSection(nil, "x", nil)
	want = []byte{0x00, 0x02, 0x01, 'x'}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty: got % x, want % x", got, want)
	}
}

func TestEncodeTypeSection(t *testing.T) {
	params := [][]byte{nil, {encode.ValtypeI32, encode.ValtypeI32}}
	results := [][]byte{{encode.ValtypeI32}, {encode.ValtypeI32}}
	got := sections.EncodeTypeSection(nil, params, results)
	want := []byte{
		0x01, 0x0b,
		0x02,
		0x60, 0x00, 0x01, 0x7f,
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeFunctionSection(t *testing.T) {
	got := sections.EncodeFunctionSection(nil, []uint32{0, 1, 0})
	want := []byte{
		0x03, 0x04,
		0x03, 0x00, 0x01, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeMemorySection(t *testing.T) {
	got := sections.EncodeMemorySection(nil, 1, -1)
	want := []byte{0x05, 0x03, 0x01, 0x00, 0x01}
	if !bytes.Equal(got, want) {
		t.Fatalf("no-max: got % x, want % x", got, want)
	}
	got = sections.EncodeMemorySection(nil, 1, 16)
	want = []byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x10}
	if !bytes.Equal(got, want) {
		t.Fatalf("with-max: got % x, want % x", got, want)
	}
}

func TestEncodeTableSection(t *testing.T) {
	got := sections.EncodeTableSection(nil, 2, -1)
	want := []byte{0x04, 0x04, 0x01, 0x70, 0x00, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("no-max: got % x, want % x", got, want)
	}
	got = sections.EncodeTableSection(nil, 2, 10)
	want = []byte{0x04, 0x05, 0x01, 0x70, 0x01, 0x02, 0x0a}
	if !bytes.Equal(got, want) {
		t.Fatalf("with-max: got % x, want % x", got, want)
	}
}

func TestEncodeElementSection(t *testing.T) {
	got := sections.EncodeElementSection(nil,
		[]int32{0}, [][]uint32{{0, 1}})
	want := []byte{
		0x09, 0x08,
		0x01,
		0x00,
		0x41, 0x00, 0x0b,
		0x02, 0x00, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeGlobalSection(t *testing.T) {
	initExpr := []byte{0x41, 0x2a, 0x0b}
	got := sections.EncodeGlobalSection(nil,
		[]byte{encode.ValtypeI32}, []byte{0x00}, [][]byte{initExpr})
	want := []byte{
		0x06, 0x06,
		0x01,
		0x7f, 0x00,
		0x41, 0x2a, 0x0b,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeExportSection(t *testing.T) {
	got := sections.EncodeExportSection(nil,
		[]string{"main"}, []byte{sections.ExportFunc}, []uint32{0})
	want := []byte{
		0x07, 0x08,
		0x01,
		0x04, 'm', 'a', 'i', 'n',
		0x00, 0x00,
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
	bodies := [][]byte{
		{0x02, 0x00, 0x0b},
		{0x04, 0x00, 0x41, 0x2a, 0x0b},
	}
	got := sections.EncodeCodeSection(nil, bodies)
	want := []byte{
		0x0a, 0x09,
		0x02,
		0x02, 0x00, 0x0b,
		0x04, 0x00, 0x41, 0x2a, 0x0b,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeDataSection(t *testing.T) {
	got := sections.EncodeDataSection(nil,
		[]int32{0}, [][]byte{{0x01, 0x02, 0x03}})
	want := []byte{
		0x0b, 0x09,
		0x01,
		0x00,
		0x41, 0x00, 0x0b,
		0x03, 0x01, 0x02, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}
