package arm64ssa

func emitWriterBytesHelper(w func(string, ...any))     { emitWriterBytes(w, true) }
func emitWriterSomeBytesHelper(w func(string, ...any)) { emitWriterBytes(w, false) }

func emitWriterBytes(w func(string, ...any), all bool) {
	name := "__method_Writer_write_some_bytes"
	prefix := ".Lssa_wwsomebytes"
	if all {
		name = "__method_Writer_write_bytes"
		prefix = ".Lssa_wwbytes"
	}
	w("%s:", fnLabel(name))
	w("\tstp x29, x30, [sp, #-48]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tldr w19, [x0, #8]")
	w("\tldr x20, [x1]")
	w("\tldr w22, [x1, #8]")
	w("\tmov x21, #0")
	w("%s_loop:", prefix)
	w("\tmov w0, w19")
	w("\tadd x1, x20, x21")
	w("\tsub x2, x22, x21")
	w("\tmov x8, #64")
	w("\tsvc #0")
	w("\ttbnz x0, #63, %s_error", prefix)
	if all {
		w("\tadd x21, x21, x0")
		w("\tcmp x21, x22")
		w("\tb.eq %s_done", prefix)
		w("\tcbnz x0, %s_loop", prefix)
		w("\tmov x0, #-5")
		w("\tb %s_error", prefix)
	}
	w("%s_done:", prefix)
	if all {
		emitOptionBox(w, 1, "")
	} else {
		w("\tmov x21, x0")
		emitOptionBox(w, 0, "x21")
	}
	w("\tb %s_return", prefix)
	w("%s_error:", prefix)
	w("\tneg x19, x0")
	emitEmptyString(w, "x1")
	w("\tmov x0, x19")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0")
	if all {
		emitOptionBox(w, 0, "x19")
	} else {
		emitOptionBox(w, 1, "x19")
	}
	w("%s_return:", prefix)
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #48")
	w("\tret")
}
