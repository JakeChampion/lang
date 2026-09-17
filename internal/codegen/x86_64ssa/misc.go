package x86_64ssa

// The last singletons: isatty and its handle forms, hostname, putchar,
// create_dir_all, and the over-release probe.

// rcUnderflowSym counts releases of an already-zero count, for
// __fern_rc_underflow_count to read back.
const rcUnderflowSym = "__ssa_rc_underflow"

// allocCountSym counts the blocks __alloc handed out, for
// __fern_heap_alloc_count to read back (#9596). Ticked only in a module that
// reads it: this backend does not implement the leak census, and a counter
// nothing reads would be a cost every program paid for nothing.
const allocCountSym = "__ssa_alloc_count"

// emitIsattyHelper writes isatty(fd) -> 0/1: one TCGETS ioctl, which only a
// terminal answers. struct termios is 60 bytes; the frame rounds up. Leaf.
func emitIsattyHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("isatty"))
	w("\tsub rsp, 72")
	w("\tmov esi, 21505") // TCGETS
	w("\tmov rdx, rsp")
	w("\tmov eax, 16") // ioctl
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tsete al")
	w("\tmovzx eax, al")
	w("\tadd rsp, 72")
	w("\tret")
}

// emitHandleIsattyHelper returns the emitter for r.isatty() / w.isatty(): the
// free isatty asked of the descriptor the handle holds at ptr+8.
func emitHandleIsattyHelper(name string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov edi, dword ptr [rdi + 8]")
		w("\tjmp %s", fnLabel("isatty"))
	}
}

// emitHostnameHelper writes hostname() -> string: the kernel's node name from
// uname(2), nodename at offset 65 of the 390-byte struct utsname, copied into
// a fresh rc string; a refused uname answers the empty string. rbx = source,
// r12 = length, both across the guard call.
func emitHostnameHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("hostname"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 392") // two pushes and this keep rsp 16-aligned
	w("\tmov rdi, rsp")
	w("\tmov eax, 63") // uname
	w("\tsyscall")
	w("\tlea rbx, [rsp + 65]") // nodename
	w("\txor r12d, r12d")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_hn_alloc")
	w(".Lssa_hn_len:")
	w("\tcmp byte ptr [rbx + r12], 0")
	w("\tje .Lssa_hn_alloc")
	w("\tadd r12, 1")
	w("\tjmp .Lssa_hn_len")
	w(".Lssa_hn_alloc:")
	w("\tlea rdx, [r12 + 9]")
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov dword ptr [rax + 4], r12d")
	w("\tadd rax, 8")
	emitBcopyCall(w, "rax", "rbx", "r12")
	w("\tmov byte ptr [rax + r12], 0")
	w("\tadd rsp, 392")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitPutcharHelper writes putchar(c): the low byte of c to stdout, from the
// frame so the kernel has an address to read. Leaf; the unused return is 0.
func emitPutcharHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("putchar"))
	w("\tsub rsp, 24")
	w("\tmov [rsp], dil")
	w("\tmov rsi, rsp")
	w("\tmov edx, 1")
	w("\tmov edi, 1") // stdout
	w("\tmov eax, 1") // write
	w("\tsyscall")
	w("\tadd rsp, 24")
	w("\txor eax, eax")
	w("\tret")
}

// emitCreateDirAllHelper writes create_dir_all(path) -> Result[(), IoError]:
// mkdirat with mode 0777 for every missing component, POSIX mkdir -p. The
// NUL-terminated copy is walked: at each '/' past the first byte that does
// not follow another, the separator becomes a NUL, mkdirat runs for the
// prefix, and the '/' goes back. Intermediate results are discarded — a
// parent that could not be created makes the leaf fail with the same errno —
// and EEXIST on the leaf is success. rbx = path, r12 = pathz, r13 = its
// length, r14 = cursor.
func emitCreateDirAllHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("create_dir_all"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tsub rsp, 8") // four pushes and one slot keep rsp 16-aligned
	w("\tmov rbx, rdi")
	ssaPathz(w, "cda")
	w("\tmov r14d, 1")
	w(".Lssa_cda_walk:")
	w("\tcmp r14, r13")
	w("\tjae .Lssa_cda_leaf")
	w("\tcmp byte ptr [r12 + r14], 47") // '/'
	w("\tjne .Lssa_cda_next")
	w("\tcmp byte ptr [r12 + r14 - 1], 47")
	w("\tje .Lssa_cda_next")
	w("\tmov byte ptr [r12 + r14], 0")
	w("\tmov edi, -100") // AT_FDCWD
	w("\tmov rsi, r12")
	w("\tmov edx, 511") // 0777
	w("\tmov eax, 258") // mkdirat
	w("\tsyscall")
	w("\tmov byte ptr [r12 + r14], 47")
	w(".Lssa_cda_next:")
	w("\tadd r14, 1")
	w("\tjmp .Lssa_cda_walk")
	w(".Lssa_cda_leaf:")
	w("\tmov edi, -100")
	w("\tmov rsi, r12")
	w("\tmov edx, 511")
	w("\tmov eax, 258") // mkdirat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjz .Lssa_cda_ok")
	w("\tcmp rax, -17") // -EEXIST is success
	w("\tjne .Lssa_cda_err")
	w(".Lssa_cda_ok:")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_cda_ret")
	w(".Lssa_cda_err:")
	w("\tneg rax")
	ssaIoErr(w)
	w(".Lssa_cda_ret:")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitRcUnderflowCountHelper writes __fern_rc_underflow_count() -> i32: the
// probe that reads back what __fern_rc_dec counted. Leaf.
func emitRcUnderflowCountHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_underflow_count"))
	w("\tmov eax, [rip + %s]", rcUnderflowSym)
	w("\tret")
}

// emitHeapAllocCountHelper writes __fern_heap_alloc_count() -> i64: the count
// of blocks __alloc has handed out (#9596), which is the half
// __fern_heap_bump_bytes cannot see — a freelist pop hands out a block
// without moving the cursor. Leaf.
func emitHeapAllocCountHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_heap_alloc_count"))
	w("\tmov rax, [rip + %s]", allocCountSym)
	w("\tret")
}
