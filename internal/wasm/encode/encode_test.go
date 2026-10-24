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
	params := []byte{encode.ValtypeI32, encode.ValtypeI32}
	results := []byte{encode.ValtypeI32}
	got := encode.PutFuncType(nil, params, results)
	want := []byte{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	got = encode.PutFuncType(nil, nil, nil)
	want = []byte{0x60, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty: got % x, want % x", got, want)
	}
}

func TestEndToEndMinimalModule(t *testing.T) {
	out := encode.PutModuleHeader(nil)
	body := []byte{0x01}
	body = encode.PutFuncType(body, nil, nil)
	out = encode.PutSection(out, encode.SectionType, body)

	want := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04,
		0x01,
		0x60, 0x00, 0x00,
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("got % x, want % x", out, want)
	}
}
