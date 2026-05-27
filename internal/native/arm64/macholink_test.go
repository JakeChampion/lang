package arm64

import (
	"encoding/binary"
	"testing"
)

// TestLinkMachOPageReloc checks the @PAGE/@PAGEOFF relocation math: an
// `adrp Xd, sym@PAGE; add Xd, Xd, sym@PAGEOFF` pair must reconstruct the
// symbol's absolute address in the (separate) __DATA segment.
func TestLinkMachOPageReloc(t *testing.T) {
	src := ".text\n" +
		"_main:\n" +
		"\tadrp x0, val@PAGE\n" +
		"\tadd x0, x0, val@PAGEOFF\n" +
		"\tret\n" +
		".section __DATA,__data\n" +
		"\t.quad 0\n" + // 8 bytes of padding so val isn't at offset 0
		"val:\n" +
		"\t.quad 0\n"
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	const textVAddr = 0x100000400
	const dataVAddr = 0x100008000
	if got := a.MachOTextLen(); got != 3*4 {
		t.Fatalf("text len = %d, want 12", got)
	}
	text, _, err := a.LinkMachO(textVAddr, dataVAddr)
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	adrp := binary.LittleEndian.Uint32(text[0:])
	add := binary.LittleEndian.Uint32(text[4:])

	// Decode adrp: 21-bit signed page delta from immhi:immlo.
	immlo := (adrp >> 29) & 0x3
	immhi := (adrp >> 5) & 0x7ffff
	pageDelta := int32(immhi<<2 | immlo)
	if pageDelta&(1<<20) != 0 { // sign-extend 21 bits
		pageDelta |= ^int32(0) << 21
	}
	insnPage := int64(textVAddr) &^ 0xfff
	adrpResult := insnPage + int64(pageDelta)<<12

	imm12 := int64((add >> 10) & 0xfff)
	addr := adrpResult + imm12

	want := int64(dataVAddr) + 8 // val is at __DATA offset 8
	if addr != want {
		t.Errorf("reconstructed address = %#x, want %#x", addr, want)
	}
}
