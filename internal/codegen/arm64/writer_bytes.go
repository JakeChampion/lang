package arm64

import "github.com/jakechampion/lang/internal/ast"

// Byte views remain borrowed throughout each syscall and short-write retry.
func (g *generator) emitWriterBytesRuntime(all bool) {
	name := "__fern_writer_write_some_bytes"
	prefix := ".Lwwsomebytes"
	if all {
		name = "__fern_writer_write_bytes"
		prefix = ".Lwwbytes"
	}
	g.line(".global " + name)
	g.typeDirective(name)
	g.label(name)
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("ldr w19, [x0]")
	g.emit("ldr x20, [x1]")
	g.emit("ldr w22, [x1, #8]")
	g.emit("mov x21, #0")
	g.label(prefix + "_loop")
	g.emit("mov w0, w19")
	g.emit("add x1, x20, x21")
	g.emit("sub x2, x22, x21")
	g.syscall("write")
	g.emit("tbnz x0, #63, %s_error", prefix)
	if all {
		g.emit("add x21, x21, x0")
		g.emit("cmp x21, x22")
		g.emit("b.eq %s_done", prefix)
		g.emit("cbnz x0, %s_loop", prefix)
		g.emit("mov x0, #-5")
		g.emit("b %s_error", prefix)
	}
	g.label(prefix + "_done")
	if all {
		g.emitPayloadlessResultBox(16, 1)
	} else {
		g.emit("mov x21, x0")
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_rc1")
		g.emit("str wzr, [x0]")
		g.emit("str x21, [x0, #8]")
	}
	g.emit("b %s_return", prefix)
	g.label(prefix + "_error")
	g.emit("neg x19, x0")
	if ast.UseTwoWordStrings(8) {
		g.emit("mov x1, xzr")
		g.emit("movz x2, #0x8000, lsl #48")
	} else {
		g.adrpAdd("x1", ".LStr_ioerr_empty")
	}
	g.emit("mov x0, x19")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_rc1")
	if all {
		g.emit("str wzr, [x0]")
	} else {
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
	}
	g.emit("str x19, [x0, #8]")
	g.label(prefix + "_return")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective(name)
}
