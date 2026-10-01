package arm64

// Extract into the ordinary owned-array layout. The builder keeps its
// capacity and the result remains independent of subsequent pushes/free.
func (g *generator) emitBufTakeBytesRuntime() {
	g.line(".global __fern_buf_take_bytes")
	g.typeDirective("__fern_buf_take_bytes")
	g.label("__fern_buf_take_bytes")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #-16]!")
	g.emit("stp x21, x22, [sp, #-16]!")
	g.emit("mov x19, x0")
	g.emit("ldr x20, [x19, #8]")
	// Every payload byte is copied below. Initialize ordinary array metadata
	// without zero-filling bytes that the copy immediately overwrites.
	// Keep __alloc_u8's empty sentinel and invalid-length handling.
	g.emit("cmp w20, #0")
	g.emit("b.le .Lbuftake_bytes_alloc_special")
	g.emit("add w0, w20, #16")
	g.emit("bl __fern_alloc")
	g.emit("add x0, x0, #16")
	g.emit("stur w20, [x0, #-12]")
	g.emit("mov w1, #1")
	g.emit("stur w1, [x0, #-8]")
	g.emitArrayLenStore("w20", "x0")
	g.emit("b .Lbuftake_bytes_alloc_done")
	g.label(".Lbuftake_bytes_alloc_special")
	g.emit("mov x0, x20")
	g.emit("bl __alloc_u8")
	g.label(".Lbuftake_bytes_alloc_done")
	g.emit("mov x21, x0")
	g.emit("cbz x20, .Lbuftake_bytes_done")
	g.emit("ldr x1, [x19]")
	g.emit("mov x2, x20")
	g.emit("bl __fern_memcpy")
	g.label(".Lbuftake_bytes_done")
	g.emit("str xzr, [x19, #8]")
	g.emit("mov x0, x21")
	g.emit("ldp x21, x22, [sp], #16")
	g.emit("ldp x19, x20, [sp], #16")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_buf_take_bytes")
}
