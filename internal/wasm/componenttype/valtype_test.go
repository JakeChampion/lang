package componenttype

import (
	"bytes"
	"testing"
)

func roundTripDef(t *testing.T, name string, in []byte) DefinedType {
	t.Helper()
	d, n, err := decodeDefinedType(in)
	if err != nil {
		t.Fatalf("%s: decode: %v", name, err)
	}
	if n != len(in) {
		t.Fatalf("%s: consumed %d of %d bytes", name, n, len(in))
	}
	got := d.encode(nil)
	if !bytes.Equal(got, in) {
		t.Fatalf("%s: re-encode\n got % x\nwant % x", name, got, in)
	}
	return d
}

func TestValtypeRoundTrip(t *testing.T) {
	cases := [][]byte{
		{primU8},
		{primString},
		{0x07},
		{0x80, 0x01},
	}
	for _, in := range cases {
		v, n, err := decodeValtype(in)
		if err != nil || n != len(in) {
			t.Fatalf("decodeValtype(% x) = (_, %d, %v)", in, n, err)
		}
		if got := v.encode(nil); !bytes.Equal(got, in) {
			t.Errorf("valtype re-encode % x -> % x", in, got)
		}
	}
}

func TestDefinedTypeRoundTrip(t *testing.T) {
	roundTripDef(t, "list", []byte{tagList, primU8})
	roundTripDef(t, "option", []byte{tagOption, primString})
	roundTripDef(t, "record", []byte{
		tagRecord, 0x02,
		0x07, 's', 'e', 'c', 'o', 'n', 'd', 's', primU64,
		0x0b, 'n', 'a', 'n', 'o', 's', 'e', 'c', 'o', 'n', 'd', 's', primU32,
	})
	roundTripDef(t, "enum", []byte{tagEnum, 0x02, 0x01, 'a', 0x01, 'b'})
	roundTripDef(t, "flags", []byte{
		tagFlags, 0x02, 0x04, 'r', 'e', 'a', 'd', 0x05, 'w', 'r', 'i', 't', 'e',
	})
	roundTripDef(t, "tuple", []byte{tagTuple, 0x02, 0x03, primString})
	roundTripDef(t, "own", []byte{tagOwn, 0x01})
	roundTripDef(t, "borrow", []byte{tagBorrow, 0x05})
	roundTripDef(t, "variant", []byte{
		tagVariant, 0x02,
		0x15, 'l', 'a', 's', 't', '-', 'o', 'p', 'e', 'r', 'a', 't', 'i', 'o', 'n', '-', 'f', 'a', 'i', 'l', 'e', 'd', 0x01, 0x02, 0x00,
		0x06, 'c', 'l', 'o', 's', 'e', 'd', 0x00, 0x00,
	})
	d := roundTripDef(t, "result-ok-err", []byte{tagResult, 0x01, 0x08, 0x01, 0x04})
	if !d.HasOk || !d.HasErr || d.Ok.Idx != 8 || d.Err.Idx != 4 {
		t.Errorf("result-ok-err decoded wrong: %+v", d)
	}
	roundTripDef(t, "result-prim-ok", []byte{tagResult, 0x01, primU64, 0x01, 0x04})
	d = roundTripDef(t, "result-err-only", []byte{tagResult, 0x00, 0x01, 0x04})
	if d.HasOk || !d.HasErr {
		t.Errorf("result-err-only: HasOk=%v HasErr=%v", d.HasOk, d.HasErr)
	}
	roundTripDef(t, "result-ok-only", []byte{tagResult, 0x01, 0x08, 0x00})
}

func TestFuncTypeRoundTrip(t *testing.T) {
	roundTrip := func(name string, in []byte) FuncType {
		f, n, err := decodeFuncType(in)
		if err != nil || n != len(in) {
			t.Fatalf("%s: decode = (_, %d, %v)", name, n, err)
		}
		if got := f.encode(nil); !bytes.Equal(got, in) {
			t.Fatalf("%s: re-encode\n got % x\nwant % x", name, got, in)
		}
		return f
	}
	f := roundTrip("two-params-unnamed-result", []byte{
		tagFunc, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x07,
		0x03, 'l', 'e', 'n', primU64,
		0x00, 0x09,
	})
	if len(f.Params) != 2 || f.NamedResults || f.Result.Idx != 9 {
		t.Errorf("decoded wrong: %+v", f)
	}
	f = roundTrip("no-params-empty-named-results", []byte{tagFunc, 0x00, 0x01, 0x00})
	if len(f.Params) != 0 || !f.NamedResults || len(f.Results) != 0 {
		t.Errorf("decoded wrong: %+v", f)
	}
}

func TestValtypeSignedLEB(t *testing.T) {
	idx65 := []byte{0xc1, 0x00}
	v, n, err := decodeValtype(idx65)
	if err != nil || n != 2 || v.IsPrim || v.Idx != 65 {
		t.Fatalf("decodeValtype(c1 00) = (%+v, %d, %v), want idx 65", v, n, err)
	}
	if got := v.encode(nil); !bytes.Equal(got, idx65) {
		t.Errorf("encode idx 65 = % x, want c1 00", got)
	}
	if got := (Valtype{Idx: 63}).encode(nil); !bytes.Equal(got, []byte{0x3f}) {
		t.Errorf("encode idx 63 = % x, want 3f", got)
	}
	if got := (Valtype{Idx: 64}).encode(nil); !bytes.Equal(got, []byte{0xc0, 0x00}) {
		t.Errorf("encode idx 64 = % x, want c0 00", got)
	}
	if got := (Valtype{IsPrim: true, Prim: primBool}).encode(nil); !bytes.Equal(got, []byte{0x7f}) {
		t.Errorf("encode bool = % x, want 7f", got)
	}
}
