package imports_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/imports"
)

func TestImportDescFunc(t *testing.T) {
	got := imports.ImportDescFunc(2)
	if !bytes.Equal(got, []byte{0x02}) {
		t.Fatalf("got % x, want %x", got, 0x02)
	}
}

func TestImportDescGlobal(t *testing.T) {
	got := imports.ImportDescGlobal(encode.ValtypeI32, imports.MutConst)
	if !bytes.Equal(got, []byte{0x7f, 0x00}) {
		t.Fatalf("got % x, want 7f 00", got)
	}
	got = imports.ImportDescGlobal(encode.ValtypeI64, imports.MutVar)
	if !bytes.Equal(got, []byte{0x7e, 0x01}) {
		t.Fatalf("got % x, want 7e 01", got)
	}
}

func TestImportDescMemory(t *testing.T) {
	got := imports.ImportDescMemory(1, -1)
	if !bytes.Equal(got, []byte{0x00, 0x01}) {
		t.Errorf("no-max: got % x", got)
	}
	got = imports.ImportDescMemory(1, 16)
	if !bytes.Equal(got, []byte{0x01, 0x01, 0x10}) {
		t.Errorf("with-max: got % x", got)
	}
}

func TestImportDescTable(t *testing.T) {
	got := imports.ImportDescTable(imports.ReftypeFuncref, 0, -1)
	if !bytes.Equal(got, []byte{0x70, 0x00, 0x00}) {
		t.Fatalf("got % x", got)
	}
}

func TestEncodeImportSection(t *testing.T) {
	got := imports.EncodeImportSection(nil,
		[]string{"env"}, []string{"foo"},
		[]byte{imports.ImportFunc},
		[][]byte{imports.ImportDescFunc(0)})
	want := []byte{
		0x02, 0x0b,
		0x01,
		0x03, 'e', 'n', 'v',
		0x03, 'f', 'o', 'o',
		0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEncodeGlobalSection(t *testing.T) {
	initExpr := []byte{0x41, 0x07, 0x0b}
	got := imports.EncodeGlobalSection(nil,
		[]byte{encode.ValtypeI32}, []byte{imports.MutConst}, [][]byte{initExpr})
	want := []byte{
		0x06, 0x06,
		0x01,
		0x7f, 0x00,
		0x41, 0x07, 0x0b,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}
