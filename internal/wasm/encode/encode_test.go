package encode_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"
)

func TestPutModuleHeader(t *testing.T) {
	got := encode.PutModuleHeader(nil)
	want := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestPutU32LE(t *testing.T) {
	cases := []struct {
		v    uint32
		want []byte
	}{
		{0, []byte{0, 0, 0, 0}},
		{1, []byte{1, 0, 0, 0}},
		{0xdeadbeef, []byte{0xef, 0xbe, 0xad, 0xde}},
	}
	for _, c := range cases {
		got := encode.PutU32LE(nil, c.v)
		if !bytes.Equal(got, c.want) {
			t.Errorf("PutU32LE(%#x) = % x, want % x", c.v, got, c.want)
		}
	}
}

func TestPutName(t *testing.T) {
	got := encode.PutName(nil, "main")
	want := []byte{0x04, 'm', 'a', 'i', 'n'}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	// Empty string still emits the zero-length prefix.
	got = encode.PutName(nil, "")
	want = []byte{0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty: got % x, want % x", got, want)
	}
}

func TestPutSection(t *testing.T) {
	body := []byte{0xaa, 0xbb, 0xcc}
	got := encode.PutSection(nil, encode.SectionCustom, body)
	want := []byte{0x00, 0x03, 0xaa, 0xbb, 0xcc}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestPutFuncType(t *testing.T) {
	// (i32, i32) -> i32 — the canonical add(): byte sequence is
	// 0x60 + 0x02 + i32 + i32 + 0x01 + i32.
	params := []byte{encode.ValtypeI32, encode.ValtypeI32}
	results := []byte{encode.ValtypeI32}
	got := encode.PutFuncType(nil, params, results)
	want := []byte{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	// Empty params, empty results: `() -> ()`.
	got = encode.PutFuncType(nil, nil, nil)
	want = []byte{0x60, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty: got % x, want % x", got, want)
	}
}

// TestEndToEndMinimalModule wires PutModuleHeader + a hand-rolled
// type section through to a known-good byte sequence — the
// minimal module that compiles via wasm-tools.
func TestEndToEndMinimalModule(t *testing.T) {
	// Manually build the module bytes for `(module)` + a type
	// section with one functype `() -> ()`. This is the same
	// shape `wasm-tools parse` produces for the trivial program.
	out := encode.PutModuleHeader(nil)
	// Type section body: vec(functype) of length 1, then `() -> ()`.
	body := []byte{0x01} // count = 1
	body = encode.PutFuncType(body, nil, nil)
	out = encode.PutSection(out, encode.SectionType, body)

	want := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // header
		0x01, 0x04, // section id 1 (type), size 4
		0x01,             // count
		0x60, 0x00, 0x00, // functype () -> ()
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("got % x, want % x", out, want)
	}
}
