package x86_64ssa

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
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tmov ebx, [rdi + 8]")
	w("\tmov r12, rsi")
	w("\tmov r13d, [rsi - 4]")
	w("\txor r14d, r14d")
	w("%s_loop:", prefix)
	w("\tmov edi, ebx")
	w("\tlea rsi, [r12 + r14]")
	w("\tmov rdx, r13")
	w("\tsub rdx, r14")
	w("\tmov eax, 1")
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs %s_error", prefix)
	if all {
		w("\tadd r14, rax")
		w("\tcmp r14, r13")
		w("\tje %s_done", prefix)
		w("\ttest rax, rax")
		w("\tjnz %s_loop", prefix)
		w("\tmov rax, -5")
		w("\tjmp %s_error", prefix)
	}
	w("%s_done:", prefix)
	w("\tmov rbx, rax")
	if all {
		ssaOptionBox(w, 1, "")
	} else {
		ssaOptionBox(w, 0, "rbx")
	}
	w("\tjmp %s_return", prefix)
	w("%s_error:", prefix)
	w("\tneg rax")
	if all {
		ssaFdIoErr(w, 0)
	} else {
		ssaFdIoErr(w, 1)
	}
	w("%s_return:", prefix)
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tpop rbp")
	w("\tret")
}
