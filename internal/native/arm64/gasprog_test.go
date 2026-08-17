package arm64_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// TestAssembleProgramNegativeLiteral checks that a negative `ldr Rt, =v`
// literal-pool load is reinterpreted as its two's-complement bit pattern
// — `=-1` must match `=0xffffffffffffffff` (x) / `=0xffffffff` (w).
func TestAssembleProgramNegativeLiteral(t *testing.T) {
	cases := [][2]string{
		{"\t.text\n\tldr x0, =-1\n\tret\n\t.ltorg\n", "\t.text\n\tldr x0, =0xffffffffffffffff\n\tret\n\t.ltorg\n"},
		{"\t.text\n\tldr w0, =-1\n\tret\n\t.ltorg\n", "\t.text\n\tldr w0, =0xffffffff\n\tret\n\t.ltorg\n"},
		{"\t.text\n\tldr x0, =-256\n\tret\n\t.ltorg\n", "\t.text\n\tldr x0, =0xffffffffffffff00\n\tret\n\t.ltorg\n"},
	}
	for _, c := range cases {
		gotT, _, err := arm64.AssembleProgram(c[0], 0x400078)
		if err != nil {
			t.Fatalf("AssembleProgram(%q): %v", c[0], err)
		}
		wantT, _, err := arm64.AssembleProgram(c[1], 0x400078)
		if err != nil {
			t.Fatalf("AssembleProgram(%q): %v", c[1], err)
		}
		if !bytes.Equal(gotT, wantT) {
			t.Errorf("%q text = % x, want (= %q) % x", c[0], gotT, c[1], wantT)
		}
	}
}

// TestAssembleProgramQuadSymbol checks that `.quad <symbol>` in .rodata
// is filled with the symbol's absolute virtual address (function-pointer
// / closure tables). Two .text functions at known indices plus a rodata
// label exercise both text- and rodata-symbol resolution.
func TestAssembleProgramQuadSymbol(t *testing.T) {
	const textVAddr = 0x400078
	src := "" +
		"\t.text\n" +
		"fn0:\n\tret\n" + // index 0 -> textVAddr
		"fn1:\n\tret\n" + // index 1 -> textVAddr+4
		"\t.section .rodata\n" +
		"\t.balign 8\n" +
		"tbl:\n\t.quad fn0\n\t.quad fn1\n\t.quad tbl\n"
	text, rodata, err := arm64.AssembleProgram(src, textVAddr)
	if err != nil {
		t.Fatal(err)
	}
	// .text is 2 instructions (8 bytes); .rodata starts 8-aligned right
	// after, so rodataVAddr = textVAddr + 8.
	rodataVAddr := uint64(textVAddr + len(text))
	rd := func(off int) uint64 {
		var v uint64
		for i := 0; i < 8; i++ {
			v |= uint64(rodata[off+i]) << (8 * i)
		}
		return v
	}
	if got, want := rd(0), uint64(textVAddr); got != want {
		t.Errorf(".quad fn0 = %#x, want %#x", got, want)
	}
	if got, want := rd(8), uint64(textVAddr+4); got != want {
		t.Errorf(".quad fn1 = %#x, want %#x", got, want)
	}
	if got, want := rd(16), rodataVAddr; got != want {
		t.Errorf(".quad tbl = %#x, want %#x", got, want)
	}
}

// TestAssembleProgramDouble checks that `.double` in .rodata emits the
// 8-byte little-endian IEEE-754 representation — the polynomial
// coefficient table for the f64 transcendental runtime helpers.
func TestAssembleProgramDouble(t *testing.T) {
	const textVAddr = 0x400078
	src := "" +
		"\t.text\n\tret\n" +
		"\t.section .rodata\n\t.balign 8\n" +
		"c0:\n\t.double 0.5\n" +
		"c1:\n\t.double -0.16666666666666666\n" +
		"c2:\n\t.double 1.4426950408889634\n"
	_, rodata, err := arm64.AssembleProgram(src, textVAddr)
	if err != nil {
		t.Fatal(err)
	}
	rd := func(off int) uint64 {
		var v uint64
		for i := 0; i < 8; i++ {
			v |= uint64(rodata[off+i]) << (8 * i)
		}
		return v
	}
	for i, want := range []float64{0.5, -0.16666666666666666, 1.4426950408889634} {
		if got := rd(i * 8); got != math.Float64bits(want) {
			t.Errorf(".double %v = %#x, want %#x", want, got, math.Float64bits(want))
		}
	}
}

// TestAssembleProgramQuadUndefinedSymbol surfaces a `.quad` of a missing
// symbol as an error rather than silently emitting zero.
func TestAssembleProgramQuadUndefinedSymbol(t *testing.T) {
	src := "\t.text\n\tret\n\t.section .rodata\n\t.quad nope\n"
	if _, _, err := arm64.AssembleProgram(src, 0x400078); err == nil {
		t.Fatal("expected an error for .quad of undefined symbol")
	}
}

// TestAssembleProgramStringWithSlashes checks that a `//` inside a
// .asciz string literal is NOT treated as a line comment (which would
// truncate the string into an unterminated literal). This is the shape
// a self-hosted lexer/compiler emits constantly — test inputs full of
// "// comment" snippets — so the comment stripper must be string-aware.
// Also exercises an escaped quote (\") so the literal isn't ended early.
func TestAssembleProgramStringWithSlashes(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tret\n" +
		"\t.section .rodata\n" +
		"msg:\n" +
		"\t.asciz \"a: i32 // c\\n\\\"q\\\"\" // trailing comment\n"
	_, rodata, err := arm64.AssembleProgram(src, 0x400078)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("a: i32 // c\n\"q\""), 0)
	if !bytes.Equal(rodata, want) {
		t.Fatalf("rodata = %q, want %q", rodata, want)
	}
}

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

// TestAssembleProgramBss checks that .bss is materialised as zero bytes
// AFTER all of .rodata — whatever order the sections appeared in — and
// that its symbols resolve (no undefined-symbol error from the adrp/
// :lo12: that reference them).
//
// The tail placement is the point (#6928): a PT_LOAD can drop trailing
// zeros from the file and have the loader supply them via p_memsz, but
// only if they ARE trailing. Emitting .rodata after a .bss block, as the
// code generator does, used to leave the zero run stranded mid-blob.
func TestAssembleProgramBss(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tadrp x0, g\n" +
		"\tadd x0, x0, :lo12:g\n" +
		"\tret\n" +
		"\t.section .bss\n" +
		"\t.align 3\n" +
		"g:\n\t.quad 0\n" + // 8 zero bytes
		"\t.space 16\n" + // 16 more zero bytes
		"\t.section .rodata\n" +
		"msg:\n\t.asciz \"hi\"\n" // 3 bytes, emitted AFTER the .bss block
	text, data, err := arm64.AssembleProgram(src, 0x400078)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 3*4 {
		t.Fatalf("text = %d bytes, want 12", len(text))
	}
	// .rodata is "hi\0" (3), padded to the 16-byte .bss base; .bss is
	// .quad 0 (8) + .space 16 = 24.
	if len(data) != 40 {
		t.Fatalf("data = %d bytes, want 40", len(data))
	}
	// Everything from the .bss base on must be zero, so a trailing-zero
	// trim reaches all of it.
	for i, b := range data[3:] {
		if b != 0 {
			t.Fatalf("data[%d] = %d, want 0 — .bss is not the tail of the blob", i+3, b)
		}
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
