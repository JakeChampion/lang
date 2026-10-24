package module_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/module"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

func TestEmptyModule(t *testing.T) {
	got := module.Build(module.New())
	want := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestMinimalFunctionModule(t *testing.T) {
	m := module.New()
	m.TypeParams = [][]byte{nil}
	m.TypeResults = [][]byte{{encode.ValtypeI32}}
	m.FunctionTypeidxs = []uint32{0}
	m.ExportNames = []string{"main"}
	m.ExportKinds = []byte{sections.ExportFunc}
	m.ExportIdxs = []uint32{0}

	body := inst.InstI32Const(nil, 42)
	m.CodeBodies = [][]byte{inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)}

	got := module.Build(m)
	want := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x08, 0x01, 0x04, 'm', 'a', 'i', 'n', 0x00, 0x00,
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x\nwant % x", got, want)
	}
}

func TestSectionOrdering(t *testing.T) {
	m := module.New()
	m.TypeParams = [][]byte{nil}
	m.TypeResults = [][]byte{{encode.ValtypeI32}}
	m.FunctionTypeidxs = []uint32{0}
	m.MemoryPresent = true
	m.MemoryMin = 1
	m.MemoryMax = -1
	m.ExportNames = []string{"main"}
	m.ExportKinds = []byte{sections.ExportFunc}
	m.ExportIdxs = []uint32{0}
	m.CodeBodies = [][]byte{inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil),
		inst.InstI32Const(nil, 0))}
	m.DataOffsets = []int32{0}
	m.DataInits = [][]byte{{1}}

	got := module.Build(m)

	wantIDs := []byte{1, 3, 5, 7, 10, 11}
	pos := 8
	for i, wantID := range wantIDs {
		if pos >= len(got) {
			t.Fatalf("ran off end at section %d", i)
		}
		gotID := got[pos]
		if gotID != wantID {
			t.Fatalf("section %d: id = %d, want %d", i, gotID, wantID)
		}
		size := uint32(got[pos+1])
		hdr := 2
		if got[pos+1]&0x80 != 0 {
			t.Fatalf("section %d size byte 0x%02x needs multi-byte uleb decode", i, got[pos+1])
		}
		pos += hdr + int(size)
	}
}
