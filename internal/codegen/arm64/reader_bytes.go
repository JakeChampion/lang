package arm64

import "github.com/jakechampion/lang/internal/ast"

// Read directly into an owned byte array. Capacity records the allocation;
// length records the short read, so later mutation and reclamation stay sound.
func (g *generator) emitReaderBytesRuntime() {
	g.line(".global __fern_reader_read_chunk_bytes")
	g.typeDirective("__fern_reader_read_chunk_bytes")
	g.label("__fern_reader_read_chunk_bytes")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("ldr w19, [x0]")
	g.emit("mov w20, w1")
	g.emit("tbnz w20, #31, .Lrrbytes_invalid")
	g.emit("mov w0, w20")
	g.emit("bl __alloc_u8")
	g.emit("mov x21, x0")
	g.emit("mov w0, w19")
	g.emit("mov x1, x21")
	g.emit("mov w2, w20")
	g.syscall("read")
	g.emit("tbnz x0, #63, .Lrrbytes_error")
	g.emit("mov w22, w0")
	// __alloc_u8(0) returns an immutable empty sentinel.
	g.emit("cbz w20, .Lrrbytes_ok")
	g.emitArrayLenStore("w22", "x21")
	g.label(".Lrrbytes_ok")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_rc1")
	g.emit("str wzr, [x0]")
	g.emit("str x21, [x0, #8]")
	g.emit("b .Lrrbytes_return")
	g.label(".Lrrbytes_error")
	g.emit("neg x19, x0")
	g.emit("cbz w20, .Lrrbytes_box_error")
	g.emit("sub x0, x21, #16")
	g.emit("add x1, x20, #16")
	g.emit("bl __fern_free")
	g.emit("b .Lrrbytes_box_error")
	g.label(".Lrrbytes_invalid")
	g.emit("mov x19, #22") // EINVAL
	g.label(".Lrrbytes_box_error")
	g.emit("mov x0, x19")
	if ast.UseTwoWordStrings(8) {
		g.emit("mov x1, xzr")
		g.emit("movz x2, #0x8000, lsl #48")
	} else {
		g.adrpAdd("x1", ".LStr_ioerr_empty")
	}
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("str x19, [x0, #8]")
	g.label(".Lrrbytes_return")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_reader_read_chunk_bytes")
}
