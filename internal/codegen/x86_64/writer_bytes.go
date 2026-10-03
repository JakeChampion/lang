package x86_64

func (g *generator) emitWriterBytesRuntime(all bool) {
	name := "__fern_writer_write_some_bytes"
	prefix := ".Lwwsomebytes"
	if all {
		name = "__fern_writer_write_bytes"
		prefix = ".Lwwbytes"
	}
	g.line(".globl " + name)
	g.line(".type " + name + ", @function")
	g.label(name)
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("mov ebx, [rdi]")
	g.emit("mov r12, [rsi]")
	g.emit("mov r13d, [rsi + 8]")
	g.emit("xor r14d, r14d")
	g.label(prefix + "_loop")
	g.emit("mov edi, ebx")
	g.emit("lea rsi, [r12 + r14]")
	g.emit("mov rdx, r13")
	g.emit("sub rdx, r14")
	g.emitSyscall(1)
	g.emit("test rax, rax")
	g.emit("js " + prefix + "_error")
	if all {
		g.emit("add r14, rax")
		g.emit("cmp r14, r13")
		g.emit("je " + prefix + "_done")
		g.emit("test rax, rax")
		g.emit("jnz " + prefix + "_loop")
		g.emit("mov rax, -5")
		g.emit("jmp " + prefix + "_error")
	}
	g.label(prefix + "_done")
	g.emit("mov rbx, rax")
	if all {
		g.emitPayloadlessResultBox(1)
	} else {
		g.emit("mov edi, 16")
		g.emit("call __fern_alloc_rc1")
		g.emit("mov dword ptr [rax], 0")
		g.emit("mov [rax + 8], rbx")
	}
	g.emit("jmp " + prefix + "_return")
	g.label(prefix + "_error")
	g.emit("neg rax")
	g.emit("mov edi, eax")
	g.emit("lea rsi, [rip + .LStr_ioerr_empty]")
	g.emit("call __fern_io_error")
	g.emit("mov rbx, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_rc1")
	if all {
		g.emit("mov dword ptr [rax], 0")
	} else {
		g.emit("mov dword ptr [rax], 1")
	}
	g.emit("mov [rax + 8], rbx")
	g.label(prefix + "_return")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size " + name + ", .-" + name)
}
