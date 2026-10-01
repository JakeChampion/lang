package arm64

func (g *generator) emitStringBytesRuntime() {
	g.line(".global __fern_string_bytes_copy")
	g.typeDirective("__fern_string_bytes_copy")
	g.label("__fern_string_bytes_copy")
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emitStrDataPtr2W("x2", "x0", "x1", 32)
	g.emitStrLen2W("w3", "x1")
	g.emit("str x2, [x29, #48]")
	g.emit("str x3, [x29, #56]")
	g.emit("mov w0, w3")
	g.emit("bl __alloc_u8")
	g.emit("str x0, [x29, #64]")
	g.emit("ldr x1, [x29, #48]")
	g.emit("ldr x2, [x29, #56]")
	g.emit("bl __fern_memcpy")
	g.emit("ldr x0, [x29, #64]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__fern_string_bytes_copy")
}
