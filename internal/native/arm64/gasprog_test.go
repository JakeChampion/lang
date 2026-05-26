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

// TestAssembleProgramUndefinedSymbol surfaces a reference to a missing
// symbol as an error.
func TestAssembleProgramUndefinedSymbol(t *testing.T) {
	src := "\t.text\n\tadrp x0, nope\n\tret\n"
	if _, _, err := arm64.AssembleProgram(src, 0x400078); err == nil {
		t.Fatal("expected an error for undefined symbol")
	}
}
