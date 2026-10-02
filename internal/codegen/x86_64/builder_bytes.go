package x86_64

// Arrays have aligned payload pointers, so the existing range-copy kernel
// takes its contiguous-memory branch without constructing a string value.
func (g *generator) emitBufPushBytesRangeRuntime() {
	g.line(".globl __fern_buf_push_bytes_range")
	g.line(".type __fern_buf_push_bytes_range, @function")
	g.label("__fern_buf_push_bytes_range")
	g.emit("xor eax, eax")
	g.emit("test edx, edx")
	g.emit("cmovs edx, eax")
	g.emit("cmp ecx, dword ptr [rsi - 4]")
	g.emit("cmovg ecx, dword ptr [rsi - 4]")
	g.emit("movsxd rdx, edx")
	g.emit("movsxd rcx, ecx")
	g.emit("jmp __fern_buf_push_range")
	g.line(".size __fern_buf_push_bytes_range, .-__fern_buf_push_bytes_range")
}

// Extract into the ordinary owned-array layout. The builder keeps its
// capacity and the result remains independent of subsequent pushes/free.
func (g *generator) emitBufTakeBytesRuntime() {
	g.line(".globl __fern_buf_take_bytes")
	g.line(".type __fern_buf_take_bytes, @function")
	g.label("__fern_buf_take_bytes")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("mov rbx, rdi")
	g.emit("mov r12, qword ptr [rbx + 8]")
	// The following copy initializes the entire payload. Keep the ordinary
	// cap/RC/length header and delegate empty or invalid lengths to __alloc_u8.
	g.emit("test r12d, r12d")
	g.emit("jle .Lbuftake_bytes_alloc_special")
	g.emit("lea edi, [r12 + 16]")
	g.emit("call __fern_alloc")
	g.emit("add rax, 16")
	g.emit("mov dword ptr [rax - 12], r12d")
	g.emit("mov dword ptr [rax - 8], 1")
	g.emitArrayLenStore("r12d", "rax")
	g.emit("jmp .Lbuftake_bytes_alloc_done")
	g.label(".Lbuftake_bytes_alloc_special")
	g.emit("mov rdi, r12")
	g.emit("call __alloc_u8")
	g.label(".Lbuftake_bytes_alloc_done")
	g.emit("mov r13, rax")
	g.emit("test r12, r12")
	g.emit("jz .Lbuftake_bytes_done")
	g.emit("mov rdi, r13")
	g.emit("mov rsi, qword ptr [rbx]")
	g.emit("mov rdx, r12")
	g.emit("call __fern_memcpy")
	g.label(".Lbuftake_bytes_done")
	g.emit("mov qword ptr [rbx + 8], 0")
	g.emit("mov rax, r13")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_buf_take_bytes, .-__fern_buf_take_bytes")
}
