package x86_64ssa

// The whole-file and clock helpers: read_file, read_file_bytes, write_file,
// remove_file, temp_dir, monotonic_ns, now_unix_ms and sleep_ms. Each file
// helper NUL-terminates its path into a fresh heap block (Fern strings carry a
// length, not a terminator), makes its syscalls with the live values in
// callee-saved registers — ssaBumpAlloc's guard call may clobber any other —
// and reports a failure as Err(IoError) through __fern_io_error with the path
// as given. Result and Option boxes are ssaOptionBox's: tag 0 is Ok, tag 1 is
// Err, the payload at +8.

// ssaPathz appends the preamble every path-taking helper shares: with the path
// string in rbx, leave a NUL-terminated copy of it in r12 and its length in
// r13. Clobbers rax, rcx and r11, and needs rsp 16-aligned for the guard call.
// lbl keeps the copy loop's labels distinct per helper.
func ssaPathz(w func(string, ...any), lbl string) {
	ssaPathzInto(w, lbl, "rbx", "r12", "r13")
}

// ssaPathzInto is ssaPathz over a chosen register triple, for the two-path
// helpers, which need a second copy while the first is still live. src is the
// path string, dst takes the NUL-terminated copy and n its length; dst and n
// must be r8-r15, since their low halves are written with the `d` suffix.
func ssaPathzInto(w func(string, ...any), lbl, src, dst, n string) {
	w("\tmov %sd, %s", n, memRef(src, -4))
	w("\tmov %s, %s", dst, n)
	w("\tadd %s, 1", dst) // + NUL
	ssaBumpAlloc(w, "rax", dst)
	w("\tmov %s, rax", dst) // pathz
	w("\txor ecx, ecx")
	w(".Lssa_%s_cp:", lbl)
	w("\tcmp ecx, %sd", n)
	w("\tjae .Lssa_%s_cpd", lbl)
	w("\tmov al, [%s + rcx]", src)
	w("\tmov [%s + rcx], al", dst)
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_%s_cp", lbl)
	w(".Lssa_%s_cpd:", lbl)
	w("\tmov rax, %s", n)
	w("\tmov byte ptr [%s + rax], 0", dst)
}

// ssaRetainPathForIoErr retains the path in rbx before it is handed to
// __fern_io_error, with errno preserved across the call.
//
// The IoError box keeps that string and its drop releases it, so the box needs
// a reference of its own. The error paths with no path to name hand over a
// freshly allocated empty string (ssaEmptyString, rc 1) for exactly that
// reason: the helper's contract is that the caller passes an OWNED reference.
// rbx is the CALLER's string, so without this the box's drop frees a buffer
// the caller is still using (#9543).
//
// The 16-byte frame is alignment, not space: one 8-byte push would leave rsp
// misaligned at the call.
func ssaRetainPathForIoErr(w func(string, ...any)) {
	w("\tsub rsp, 16")
	w("\tmov [rsp], rax") // errno, across the retain
	w("\tmov rdi, rbx")
	w("\tcall %s", fnLabel("__fern_rc_inc"))
	w("\tmov rax, [rsp]")
	w("\tadd rsp, 16")
}

// ssaIoErr appends the shared failure tail: with the positive errno in eax and
// the path in rbx, build the IoError and leave Err(IoError) in rax. r12 is
// scratch.
func ssaIoErr(w func(string, ...any)) {
	ssaRetainPathForIoErr(w)
	w("\tmov edi, eax")
	w("\tmov rsi, rbx")
	w("\tcall %s", fnLabel("__fern_io_error"))
	w("\tmov r12, rax")
	ssaOptionBox(w, 1, "r12")
}

// emitWriteFileHelper writes write_file(path, content) -> Result[(), IoError]:
// openat with O_WRONLY|O_CREAT|O_TRUNC and mode 0644, write(2) in a loop until
// every byte is out (a short write is not an error), close. rbx = path,
// r12 = pathz then bytes written, r13 = fd, r14 = content.
func emitWriteFileHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("write_file"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tsub rsp, 8") // four pushes past the return address: one slot realigns
	w("\tmov rbx, rdi")
	w("\tmov r14, rsi")
	ssaPathz(w, "wf")
	w("\tmov edi, -100") // AT_FDCWD
	w("\tmov rsi, r12")
	w("\tmov edx, 577")  // O_WRONLY|O_CREAT|O_TRUNC
	w("\tmov r10d, 420") // 0644
	w("\tmov eax, 257")  // openat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_wf_err")
	w("\tmov r13d, eax")  // fd
	w("\txor r12d, r12d") // written
	w(".Lssa_wf_loop:")
	w("\tmov ecx, %s", memRef("r14", -4)) // len
	w("\tcmp r12d, ecx")
	w("\tjae .Lssa_wf_written")
	w("\tmov edi, r13d")
	w("\tlea rsi, [r14 + r12]")
	w("\tmov edx, ecx")
	w("\tsub edx, r12d")
	w("\tmov eax, 1") // write
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_wf_err_close")
	w("\tadd r12d, eax")
	w("\tjmp .Lssa_wf_loop")
	w(".Lssa_wf_written:")
	w("\tmov edi, r13d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_wf_ret")
	w(".Lssa_wf_err_close:")
	w("\tneg rax")
	w("\tmov r12d, eax") // errno, across the close
	w("\tmov edi, r13d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov eax, r12d")
	w("\tjmp .Lssa_wf_errno")
	w(".Lssa_wf_err:")
	w("\tneg rax")
	w(".Lssa_wf_errno:")
	ssaIoErr(w)
	w(".Lssa_wf_ret:")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitRemoveFileHelper writes remove_file(path) -> Result[(), IoError]:
// unlinkat(AT_FDCWD, path, 0). rbx = path, r12 = pathz, r13 = its length.
func emitRemoveFileHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("remove_file"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned.
	w("\tmov rbx, rdi")
	ssaPathz(w, "rmf")
	w("\tmov edi, -100") // AT_FDCWD
	w("\tmov rsi, r12")
	w("\txor edx, edx")
	w("\tmov eax, 263") // unlinkat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_rmf_err")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_rmf_ret")
	w(".Lssa_rmf_err:")
	w("\tneg rax")
	ssaIoErr(w)
	w(".Lssa_rmf_ret:")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitReadFileHelper returns the emitter for read_file(path) ->
// Result[string, IoError] and, with bytes set, read_file_bytes(path) ->
// Result[u8[], IoError]: openat read-only, fstat for a size hint, read to EOF
// into a container that grows when the hint runs short (#9065: a pseudo-file
// reports 0 or a page and generates its contents on the read), close. The text
// form validates at the boundary — invalid UTF-8 is Err(InvalidUtf8(path))
// through the synthetic EILSEQ (#5714) — and the raw form hands back the bytes
// as read. Asking for st_size + 1 is what tells a file that ended from a hint
// that was short without costing a regular file a second read.
//
// The frame is the 144-byte stat buffer; [rsp + 64] carries the grown
// capacity across the copy once st_size has been read out. rbx = path,
// r12 = pathz then data, r13 = fd, r14 = capacity, r15 = bytes read.
func emitReadFileHelper(name, lbl string, bytes bool) func(w func(string, ...any)) {
	// alloc leaves a fresh container of r14 bytes in rax: a single-word rc
	// string's data, or __alloc_u8's array data.
	alloc := func(w func(string, ...any)) {
		if bytes {
			w("\tmov edi, r14d")
			w("\tcall %s", fnLabel("__alloc_u8"))
		} else {
			w("\tlea rdx, [r14 + %d]", strBlockBytes) // header + capacity + NUL
			ssaBumpAlloc(w, "rax", "rdx")
			w("\tmov dword ptr [rax], 1") // rc = 1
			w("\tadd rax, 8")
		}
	}
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx")
		w("\tpush r12")
		w("\tpush r13")
		w("\tpush r14")
		w("\tpush r15")
		w("\tsub rsp, 144") // five pushes past the return address: 16-aligned, and the stat buffer keeps it so
		w("\tmov rbx, rdi")
		ssaPathz(w, lbl)
		w("\tmov edi, -100") // AT_FDCWD
		w("\tmov rsi, r12")
		w("\txor edx, edx") // O_RDONLY
		w("\txor r10d, r10d")
		w("\tmov eax, 257") // openat
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", lbl)
		w("\tmov r13d, eax") // fd
		w("\tmov edi, r13d")
		w("\tmov rsi, rsp")
		w("\tmov eax, 5") // fstat
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err_close", lbl)
		w("\tmov r14, [rsp + 48]") // st_size
		w("\tadd r14, 1")
		alloc(w)
		w("\tmov r12, rax")
		w("\txor r15d, r15d")
		w(".Lssa_%s_loop:", lbl)
		w("\tcmp r15, r14")
		w("\tjb .Lssa_%s_read", lbl)
		// Full and not at EOF: double the capacity, with a page floor so a
		// hint of 0 gets there in one step, and move the bytes over.
		w("\tlea rdx, [r14 + r14]")
		w("\tcmp rdx, 4096")
		w("\tjae .Lssa_%s_grow", lbl)
		w("\tmov edx, 4096")
		w(".Lssa_%s_grow:", lbl)
		w("\tmov [rsp + 64], rdx")
		w("\tmov r14, rdx")
		alloc(w)
		emitBcopyCall(w, "rax", "r12", "r15")
		w("\tmov r12, rax")
		w("\tmov r14, [rsp + 64]")
		w(".Lssa_%s_read:", lbl)
		w("\tmov edi, r13d")
		w("\tlea rsi, [r12 + r15]")
		w("\tmov rdx, r14")
		w("\tsub rdx, r15")
		w("\txor eax, eax") // read
		w("\tsyscall")
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err_close", lbl)
		w("\tjz .Lssa_%s_eof", lbl)
		w("\tadd r15, rax")
		w("\tjmp .Lssa_%s_loop", lbl)
		w(".Lssa_%s_eof:", lbl)
		if !bytes {
			w("\tmov byte ptr [r12 + r15], 0")
		}
		w("\tmov %s, r15d", memRef("r12", -4)) // len = bytes read
		w("\tmov edi, r13d")
		w("\tmov eax, 3") // close
		w("\tsyscall")
		if !bytes {
			w("\tmov rdi, r12")
			w("\tmov rsi, r15")
			w("\tcall %s", fnLabel("__fern_utf8_valid"))
			w("\ttest eax, eax")
			w("\tjnz .Lssa_%s_ok", lbl)
			w("\tmov eax, 84") // EILSEQ
			w("\tjmp .Lssa_%s_errno", lbl)
			w(".Lssa_%s_ok:", lbl)
		}
		ssaOptionBox(w, 0, "r12")
		w("\tjmp .Lssa_%s_ret", lbl)
		w(".Lssa_%s_err_close:", lbl)
		w("\tneg rax")
		w("\tmov r12d, eax") // errno, across the close
		w("\tmov edi, r13d")
		w("\tmov eax, 3") // close
		w("\tsyscall")
		w("\tmov eax, r12d")
		w("\tjmp .Lssa_%s_errno", lbl)
		w(".Lssa_%s_err:", lbl)
		w("\tneg rax")
		w(".Lssa_%s_errno:", lbl)
		ssaIoErr(w)
		w(".Lssa_%s_ret:", lbl)
		w("\tadd rsp, 144")
		w("\tpop r15")
		w("\tpop r14")
		w("\tpop r13")
		w("\tpop r12")
		w("\tpop rbx")
		w("\tret")
	}
}

// emitTempDirHelper writes temp_dir(prefix) -> Result[string, IoError]:
// mkdirat "/tmp/<prefix>-<monotonic ns>" with mode 0700 and return Ok(path),
// the shape the flat backend creates, so the two agree on what a temp
// directory is named. A '/' in the prefix is EINVAL before the syscall: the
// concatenation would otherwise place the directory wherever the caller's
// bytes point. The path is built in a scratch block of prefix length + 27
// ("/tmp/", '-', up to 20 digits, NUL) and copied into an exactly sized rc
// string. rbx = prefix, r12 = prefix length then the string, r13 = scratch,
// r14 = path length, r15 = ns; the frame holds the timespec.
func emitTempDirHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("temp_dir"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	w("\tsub rsp, 16") // five pushes past the return address: 16-aligned
	w("\tmov rbx, rdi")
	w("\tmov r12d, %s", memRef("rbx", -4))
	w("\txor ecx, ecx")
	w(".Lssa_td_sep:")
	w("\tcmp ecx, r12d")
	w("\tjae .Lssa_td_sepd")
	w("\tcmp byte ptr [rbx + rcx], 47") // '/'
	w("\tje .Lssa_td_einval")
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_td_sep")
	w(".Lssa_td_sepd:")
	w("\tmov edi, 1") // CLOCK_MONOTONIC
	w("\tmov rsi, rsp")
	w("\tmov eax, 228") // clock_gettime
	w("\tsyscall")
	w("\tmov r15, [rsp]")
	w("\timul r15, r15, 1000000000")
	w("\tadd r15, [rsp + 8]")
	w("\tlea rdx, [r12 + 27]")
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov r13, rax")
	w("\tmov byte ptr [r13], 47")      // '/'
	w("\tmov byte ptr [r13 + 1], 116") // 't'
	w("\tmov byte ptr [r13 + 2], 109") // 'm'
	w("\tmov byte ptr [r13 + 3], 112") // 'p'
	w("\tmov byte ptr [r13 + 4], 47")  // '/'
	w("\tmov r14d, 5")
	w("\txor ecx, ecx")
	w(".Lssa_td_pcp:")
	w("\tcmp ecx, r12d")
	w("\tjae .Lssa_td_pcpd")
	w("\tmov al, [rbx + rcx]")
	w("\tmov [r13 + r14], al")
	w("\tadd r14, 1")
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_td_pcp")
	w(".Lssa_td_pcpd:")
	w("\tmov byte ptr [r13 + r14], 45") // '-'
	w("\tadd r14, 1")
	// Decimal digits of r15, written from the end once counted.
	w("\tmov rax, r15")
	w("\txor r9d, r9d")
	w(".Lssa_td_cnt:")
	w("\txor edx, edx")
	w("\tmov ecx, 10")
	w("\tdiv rcx")
	w("\tadd r9, 1")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_td_cnt")
	w("\tlea r10, [r14 + r9 - 1]")
	w("\tmov rax, r15")
	w(".Lssa_td_wr:")
	w("\txor edx, edx")
	w("\tmov ecx, 10")
	w("\tdiv rcx")
	w("\tadd dl, 48")
	w("\tmov [r13 + r10], dl")
	w("\tsub r10, 1")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_td_wr")
	w("\tadd r14, r9")
	w("\tmov byte ptr [r13 + r14], 0")
	w("\tmov edi, -100") // AT_FDCWD
	w("\tmov rsi, r13")
	w("\tmov edx, 448") // 0700
	w("\tmov eax, 258") // mkdirat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_td_err")
	w("\tlea rdx, [r14 + %d]", strBlockBytes) // header + length + NUL
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov dword ptr [rax + 4], r14d")
	w("\tadd rax, 8")
	emitBcopyCall(w, "rax", "r13", "r14")
	w("\tmov byte ptr [rax + r14], 0")
	w("\tmov r12, rax")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_td_ret")
	w(".Lssa_td_einval:")
	w("\tmov eax, 22") // EINVAL
	w("\tjmp .Lssa_td_errno")
	w(".Lssa_td_err:")
	w("\tneg rax")
	w(".Lssa_td_errno:")
	ssaIoErr(w)
	w(".Lssa_td_ret:")
	w("\tadd rsp, 16")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitClockHelper returns the emitter for a clock reading: clock_gettime on
// the given clock, reported as tv_sec * secMul + tv_nsec / nsDiv, so
// monotonic_ns is (1, 1e9, 1) and now_unix_ms is (0, 1e3, 1e6). Leaf; the
// timespec lives in the frame.
func emitClockHelper(name string, clock int, secMul, nsDiv int64) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tsub rsp, 24")
		w("\tmov edi, %d", clock)
		w("\tmov rsi, rsp")
		w("\tmov eax, 228") // clock_gettime
		w("\tsyscall")
		w("\tmov rax, [rsp + 8]")
		if nsDiv != 1 {
			w("\txor edx, edx")
			w("\tmov ecx, %d", nsDiv)
			w("\tdiv rcx")
		}
		w("\tmov rcx, [rsp]")
		w("\timul rcx, rcx, %d", secMul)
		w("\tadd rax, rcx")
		w("\tadd rsp, 24")
		w("\tret")
	}
}

// emitSleepMsHelper writes sleep_ms(ms): nanosleep for ms milliseconds, and
// nothing at all for a non-positive count. Leaf; the timespec lives in the
// frame.
func emitSleepMsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("sleep_ms"))
	w("\ttest rdi, rdi") // ms is an i64
	w("\tjle .Lssa_sleep_done")
	w("\tsub rsp, 24")
	w("\tmov rax, rdi")
	w("\txor edx, edx")
	w("\tmov ecx, 1000")
	w("\tdiv rcx") // rax = seconds, rdx = milliseconds over
	w("\tmov [rsp], rax")
	w("\timul rdx, rdx, 1000000")
	w("\tmov [rsp + 8], rdx")
	w("\tmov rdi, rsp")
	w("\txor esi, esi")
	w("\tmov eax, 35") // nanosleep
	w("\tsyscall")
	w("\tadd rsp, 24")
	w(".Lssa_sleep_done:")
	w("\tret")
}
