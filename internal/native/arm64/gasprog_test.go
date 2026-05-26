package arm64_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// TestAssembleProgramRodata checks the data section is parsed into the
// expected bytes and that symbol references assemble without error.
func TestAssembleProgramRodata(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tadrp x0, msg\n" +
		"\tadd x0, x0, :lo12:msg\n" +
		"\tldr x1, [x0]\n" +
		"\tret\n" +
		"\t.section .rodata\n" +
		"\t.balign 8\n" +
		"msg:\n" +
		"\t.8byte 0x2a\n" +
		"\t.byte 1, 2, 3\n" +
		"\t.asciz \"hi\"\n"
	text, rodata, err := arm64.AssembleProgram(src, 0x400078)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 4*4 { // adrp, add, ldr, ret
		t.Fatalf("text = %d bytes, want 16", len(text))
	}
	want := []byte{
		0x2a, 0, 0, 0, 0, 0, 0, 0, // .8byte 0x2a
		1, 2, 3, // .byte 1,2,3
		'h', 'i', 0, // .asciz "hi"
	}
	if !bytes.Equal(rodata, want) {
		t.Fatalf("rodata = % x, want % x", rodata, want)
	}
}

// TestAssembleProgramLiteralPool checks that `ldr Xt, =value` places
// the value in a pool at .ltorg and resolves the load's PC-relative
// offset. textVAddr 0x400078 is 8-aligned, so with the ldr at index 0
// and ret at index 1 the 8-byte literal lands at index 2 (offset 8):
// imm19 = 8/4 = 2 → ldr encoding 0x58000040, then the value words.
func TestAssembleProgramLiteralPool(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tldr x0, =0x1122334455667788\n" +
		"\tret\n" +
		"\t.ltorg\n"
	text, _, err := arm64.AssembleProgram(src, 0x400078)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 16 {
		t.Fatalf("text = %d bytes, want 16", len(text))
	}
	u32 := func(off int) uint32 {
		return uint32(text[off]) | uint32(text[off+1])<<8 | uint32(text[off+2])<<16 | uint32(text[off+3])<<24
	}
	if got := u32(0); got != 0x58000040 {
		t.Errorf("ldr-literal = %#08x, want 0x58000040", got)
	}
	if lo, hi := u32(8), u32(12); lo != 0x55667788 || hi != 0x11223344 {
		t.Errorf("pool value = %#08x%08x, want 0x1122334455667788", hi, lo)
	}
}

// TestAssembleProgramUndefinedSymbol surfaces a reference to a missing
// symbol as an error.
func TestAssembleProgramUndefinedSymbol(t *testing.T) {
	src := "\t.text\n\tadrp x0, nope\n\tret\n"
	if _, _, err := arm64.AssembleProgram(src, 0x400078); err == nil {
		t.Fatal("expected an error for undefined symbol")
	}
}
