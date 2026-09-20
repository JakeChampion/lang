package x86_64ssa

// The helpers that work over a path rather than a descriptor: access, getcwd
// and read_link, which answer a question about a name, and the family built on
// ssaPathOpHelper, which changes one — mkdir, rmdir, link, symlink, rename,
// chmod, chown, mknod, chdir and truncate. Each NUL-terminates its path into a
// fresh heap block (Fern strings carry a length, not a terminator) and reports
// a refusal as Err(IoError) through __fern_io_error with the path as given, as
// the whole-file helpers in files.go do.

// ssaStrFromBytes copies n bytes at src into a fresh single-word rc string
// (rc@base, len@base+4, data@base+8, NUL-terminated so a C consumer can read
// it back) and leaves the data pointer in rax.
//
// src and n must both be callee-saved, since the copy is a call, and n must be
// one of r8-r15: its low half is written with the `d` suffix.
func ssaStrFromBytes(w func(string, ...any), src, n string) {
	w("\tlea rdx, [%s + %d]", n, strBlockBytes)
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov dword ptr [rax + 4], %sd", n)
	w("\tadd rax, 8")
	emitBcopyCall(w, "rax", src, n)
	w("\tmov byte ptr [rax + %s], 0", n)
}

// emitAccessHelper writes access(path, mode) -> Result[(), IoError]:
// faccessat2(AT_FDCWD, path, mode, AT_EACCESS).
//
// faccessat2 (439) and not faccessat (269): only the newer call takes a flags
// word, and AT_EACCESS is the point — `test -r` asks about the EFFECTIVE ids.
// A kernel without it (before 5.8) answers ENOSYS, which the caller sees as an
// IoError rather than silently getting the real-id answer instead.
//
// rbx = path, r12 = pathz, r13 = its length, r14 = mode.
func emitAccessHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("access"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tsub rsp, 8") // four pushes and one slot keep rsp 16-aligned
	w("\tmov rbx, rdi")
	w("\tmov r14d, esi") // mode, across the guard call inside ssaPathz
	ssaPathz(w, "acc")
	ssaAtFdcwd(w, "edi")
	w("\tmov rsi, r12")
	w("\tmov edx, r14d")
	w("\tmov r10d, 512") // AT_EACCESS
	w("\tmov eax, 439")  // faccessat2
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_acc_err")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_acc_ret")
	w(".Lssa_acc_err:")
	w("\tneg rax")
	ssaIoErr(w)
	w(".Lssa_acc_ret:")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitGetcwdHelper writes getcwd() -> string: the process's working directory
// in a fresh rc string. The kernel fills a caller's buffer and answers the
// length including the NUL; a refusal — an unlinked working directory, an
// unreadable ancestor, a path past the page the kernel builds it in — takes
// the zero-length path and answers the empty string.
//
// rbx = the buffer, r12 = the length to its NUL, both across the guard call.
func emitGetcwdHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("getcwd"))
	w("\tpush rbx")
	w("\tpush r12")
	// PATH_MAX plus the eight bytes that keep rsp 16-aligned past two pushes.
	w("\tsub rsp, 4104")
	w("\tmov rdi, rsp")
	w("\tmov esi, 4096")
	w("\tmov eax, 79") // getcwd
	w("\tsyscall")
	w("\tmov rbx, rsp")
	w("\txor r12d, r12d")
	w("\ttest rax, rax")
	w("\tjle .Lssa_cwd_alloc")
	w(".Lssa_cwd_len:")
	w("\tcmp byte ptr [rbx + r12], 0")
	w("\tje .Lssa_cwd_alloc")
	w("\tadd r12, 1")
	w("\tjmp .Lssa_cwd_len")
	w(".Lssa_cwd_alloc:")
	ssaStrFromBytes(w, "rbx", "r12")
	w("\tadd rsp, 4104")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitReadLinkHelper writes read_link(path) -> Result[string, IoError]:
// readlinkat(AT_FDCWD, path, buf, 4096).
//
// readlinkat truncates silently into a buffer that is too small and reports no
// error, so the answer is only trustworthy when it is SHORTER than the buffer.
// PATH_MAX is the kernel's own bound on a stored link target, so a full buffer
// means ENAMETOOLONG rather than a truncated answer.
//
// rbx = path, r12 = pathz then the target buffer, r13 = the path length,
// r14 = the target length.
func emitReadLinkHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("read_link"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	// PATH_MAX plus the eight bytes that keep rsp 16-aligned past four pushes.
	w("\tsub rsp, 4104")
	w("\tmov rbx, rdi")
	ssaPathz(w, "rlnk")
	ssaAtFdcwd(w, "edi")
	w("\tmov rsi, r12")
	w("\tmov rdx, rsp")
	w("\tmov r10d, 4096")
	w("\tmov eax, 267") // readlinkat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_rlnk_err")
	w("\tcmp rax, 4096")
	w("\tjb .Lssa_rlnk_fits")
	w("\tmov eax, 36") // ENAMETOOLONG
	w("\tjmp .Lssa_rlnk_errno")
	w(".Lssa_rlnk_fits:")
	w("\tmov r14, rax") // target length
	w("\tmov r12, rsp") // the target, in place of the pathz it replaces
	ssaStrFromBytes(w, "r12", "r14")
	w("\tmov r12, rax")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_rlnk_ret")
	w(".Lssa_rlnk_err:")
	w("\tneg rax")
	w(".Lssa_rlnk_errno:")
	ssaIoErr(w)
	w(".Lssa_rlnk_ret:")
	w("\tadd rsp, 4104")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// ssaPathOpHelper returns the emitter for the family of helpers that name one
// or two paths, make a single syscall over them, and answer Result[(),
// IoError]: the *at forms of mkdir, rmdir, link, symlink, rename, chmod,
// chown and mknod, plus chdir and truncate. `args` fills the syscall's
// argument registers; `sysno` is the x86-64 number, which differs from the
// asm-generic one arm64ssa uses for most of these.
//
// rbx = the first path, r12 = its NUL-terminated copy. With two paths, r15 is
// the second path and r14 its copy, and the IoError names the second — the one
// being created or moved to. With scalars instead, r13, r14 and r15 hold them:
// they arrive in caller-saved registers and the NUL-termination is a call, so
// they cross it on the frame. A helper never has both.
func ssaPathOpHelper(name, tag string, sysno, paths, scalars int, args func(w func(string, ...any))) func(func(string, ...any)) {
	return ssaPathOpHelperSys(name, tag, paths, scalars, func(w func(string, ...any)) {
		args(w)
		w("\tmov eax, %d", sysno)
		w("\tsyscall")
	})
}

// ssaPathOpHelperSys is ssaPathOpHelper with the syscall itself left to
// `body`, for a helper whose syscall NUMBER depends on an argument. `body`
// must leave the kernel's answer in rax.
func ssaPathOpHelperSys(name, tag string, paths, scalars int, body func(w func(string, ...any))) func(func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx")
		w("\tpush r12")
		w("\tpush r13")
		w("\tpush r14")
		w("\tpush r15")
		// Five pushes past the return address leave rsp 16-aligned, and the
		// scalar spill is a whole number of 16-byte units, so it stays so.
		if scalars > 0 {
			w("\tsub rsp, 32")
			for i, src := range []string{"rsi", "rdx", "rcx"}[:scalars] {
				w("\tmov [rsp + %d], %s", 8*i, src)
			}
		}
		w("\tmov rbx, rdi")
		if paths == 2 {
			w("\tmov r15, rsi")
		}
		ssaPathz(w, tag+"1")
		if paths == 2 {
			ssaPathzInto(w, tag+"2", "r15", "r14", "r13")
		}
		for i, dst := range []string{"r13", "r14", "r15"}[:scalars] {
			w("\tmov %s, [rsp + %d]", dst, 8*i)
		}
		body(w)
		w("\ttest rax, rax")
		w("\tjs .Lssa_%s_err", tag)
		ssaOptionBox(w, 0, "")
		w("\tjmp .Lssa_%s_ret", tag)
		w(".Lssa_%s_err:", tag)
		w("\tneg rax")
		if paths == 2 {
			w("\tmov rbx, r15")
		}
		ssaIoErr(w)
		w(".Lssa_%s_ret:", tag)
		if scalars > 0 {
			w("\tadd rsp, 32")
		}
		w("\tpop r15")
		w("\tpop r14")
		w("\tpop r13")
		w("\tpop r12")
		w("\tpop rbx")
		w("\tret")
	}
}

// ssaAtFdcwd loads AT_FDCWD into a syscall argument register. The kernel reads
// a dirfd as an int, so the zero-extended 32-bit -100 the `mov` leaves is the
// value it compares against.
func ssaAtFdcwd(w func(string, ...any), reg string) {
	w("\tmov %s, -100", reg)
}

// emitChdirHelper writes chdir(path) -> Result[(), IoError] — chdir(2), the
// move `getcwd` only ever reported.
func emitChdirHelper(w func(string, ...any)) {
	ssaPathOpHelper("chdir", "chdir", 80, 1, 0, func(w func(string, ...any)) {
		w("\tmov rdi, r12")
	})(w)
}

// emitChrootHelper writes chroot(path) -> Result[(), IoError] — chroot(2),
// the same move one level up from chdir: what every later path resolves
// against rather than where relative ones start (#9678).
func emitChrootHelper(w func(string, ...any)) {
	ssaPathOpHelper("chroot", "chroot", 161, 1, 0, func(w func(string, ...any)) {
		w("\tmov rdi, r12")
	})(w)
}

// emitCreateDirHelper writes create_dir(path, mode) -> Result[(), IoError]:
// mkdirat(AT_FDCWD, path, mode). One directory, no parents, and EEXIST
// reaches the caller — the whole difference from create_dir_all.
func emitCreateDirHelper(w func(string, ...any)) {
	ssaPathOpHelper("create_dir", "cdir", 258, 1, 1, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		w("\tmov edx, r13d")
		w("\tand edx, 4095")
	})(w)
}

// emitRemoveDirHelper writes remove_dir(path) -> Result[(), IoError]:
// unlinkat(AT_FDCWD, path, AT_REMOVEDIR), which is rmdir(2). A non-empty
// directory is ENOTEMPTY and reaches the caller.
func emitRemoveDirHelper(w func(string, ...any)) {
	ssaPathOpHelper("remove_dir", "rdir", 263, 1, 0, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		w("\tmov edx, 512") // AT_REMOVEDIR
	})(w)
}

// emitCreateLinkHelper writes create_link(target, path) -> Result[(),
// IoError]: linkat(AT_FDCWD, target, AT_FDCWD, path, 0). No AT_SYMLINK_FOLLOW,
// so a symlink named as the target is linked to itself.
func emitCreateLinkHelper(w func(string, ...any)) {
	ssaPathOpHelper("create_link", "clink", 265, 2, 0, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		ssaAtFdcwd(w, "edx")
		w("\tmov r10, r14")
		w("\txor r8d, r8d")
	})(w)
}

// emitCreateSymlinkHelper writes create_symlink(target, path) -> Result[(),
// IoError]: symlinkat(target, AT_FDCWD, path). `target` is stored verbatim and
// never resolved.
func emitCreateSymlinkHelper(w func(string, ...any)) {
	ssaPathOpHelper("create_symlink", "csym", 266, 2, 0, func(w func(string, ...any)) {
		w("\tmov rdi, r12")
		ssaAtFdcwd(w, "esi")
		w("\tmov rdx, r14")
	})(w)
}

// emitRenameHelper writes rename(from, to) -> Result[(), IoError]:
// renameat(AT_FDCWD, from, AT_FDCWD, to). An existing `to` of a compatible
// type is replaced atomically, and a rename across filesystems is EXDEV rather
// than a copy.
func emitRenameHelper(w func(string, ...any)) {
	ssaPathOpHelper("rename", "rnam", 264, 2, 0, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		ssaAtFdcwd(w, "edx")
		w("\tmov r10, r14")
	})(w)
}

// emitChmodHelper writes chmod(path, mode) -> Result[(), IoError]:
// fchmodat(AT_FDCWD, path, mode). The umask is not consulted — it filters a
// creation, and this is not one — so the low twelve bits land verbatim.
//
// Linux's fchmodat takes three arguments, not four: the flags-taking form is
// fchmodat2, which chmod_at below issues for its nofollow case.
func emitChmodHelper(w func(string, ...any)) {
	ssaPathOpHelper("chmod", "chmd", 268, 1, 1, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		w("\tmov edx, r13d")
		w("\tand edx, 4095")
	})(w)
}

// emitChmodAtHelper writes chmod_at(path, mode, follow) -> Result[(),
// IoError]: fchmodat(AT_FDCWD, path, mode) when follow, otherwise
// fchmodat2(AT_FDCWD, path, mode, AT_SYMLINK_NOFOLLOW). The flag picks the
// syscall NUMBER, which is why this helper issues its own: the older call has
// no flags word. The kernel's answer to the flag on a symlink — EOPNOTSUPP, or
// ENOSYS below fchmodat2 — passes through as the Err.
func emitChmodAtHelper(w func(string, ...any)) {
	ssaPathOpHelperSys("chmod_at", "chma", 1, 2, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		w("\tmov edx, r13d") // mode
		w("\tand edx, 4095")
		w("\ttest r14, r14") // follow
		w("\tjz .Lssa_chma_nofollow")
		w("\tmov eax, 268") // fchmodat
		w("\tsyscall")
		w("\tjmp .Lssa_chma_done")
		w(".Lssa_chma_nofollow:")
		w("\tmov r10d, 256") // AT_SYMLINK_NOFOLLOW
		w("\tmov eax, 452")  // fchmodat2
		w("\tsyscall")
		w(".Lssa_chma_done:")
	})(w)
}

// emitTruncateHelper writes truncate(path, length) -> Result[(), IoError]:
// truncate(2), the path form. The length reaches the kernel unmasked — a
// negative one is its EINVAL, where a clamp here would resize to something the
// caller did not ask for.
func emitTruncateHelper(w func(string, ...any)) {
	ssaPathOpHelper("truncate", "trnc", 76, 1, 1, func(w func(string, ...any)) {
		w("\tmov rdi, r12")
		w("\tmov rsi, r13")
	})(w)
}

// emitMknodHelper writes mknod(path, mode, major, minor) -> Result[(),
// IoError]: mknodat(AT_FDCWD, path, mode, dev).
//
// The major / minor pair is packed into Linux's dev_t here rather than by the
// caller, because the layout is the kernel's: minor[7:0] at the bottom,
// major[11:0] above it, and minor[19:8] from bit 20 — the minor SPLIT around
// the major, which the legacy 8+8 layout matches for every pair below 256 and
// diverges from above it.
func emitMknodHelper(w func(string, ...any)) {
	ssaPathOpHelper("mknod", "mknd", 259, 1, 3, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		w("\tmov rdx, r13")
		// A pair that does not fit the 12 + 20 bits below would be packed
		// LOSSILY into a different, valid node. The kernel ignores every bit
		// of `dev` above 31, so there is nothing to hand it that it would
		// reject on its own — instead the mode becomes an S_IFMT no type
		// uses, for which it answers EINVAL.
		w("\tmov r9d, r15d")
		w("\tshr r9d, 20")
		w("\tjnz .Lssa_mknd_bad")
		w("\tmov r9d, r14d")
		w("\tshr r9d, 12")
		w("\tjz .Lssa_mknd_dev")
		w(".Lssa_mknd_bad:")
		w("\tmov edx, 61440") // 0o170000
		w(".Lssa_mknd_dev:")
		w("\tmov r10d, r15d")
		w("\tand r10d, 255") // minor[7:0]
		w("\tmov r9d, r14d")
		w("\tand r9d, 4095") // major[11:0]
		w("\tshl r9, 8")
		w("\tor r10, r9")
		w("\tmov r9d, r15d")
		w("\tand r9d, 1048320") // minor[19:8]
		w("\tshl r9, 12")
		w("\tor r10, r9")
	})(w)
}

// emitChownAtHelper writes chown_at(path, uid, gid, follow) -> Result[(),
// IoError]: fchownat(AT_FDCWD, path, uid, gid, follow ? 0 :
// AT_SYMLINK_NOFOLLOW).
//
// The ids move as 32-bit registers, which is what makes -1 mean "leave this
// one alone": uid_t is unsigned, so the kernel reads the 0xffffffff it
// compares against.
func emitChownAtHelper(w func(string, ...any)) {
	ssaPathOpHelper("chown_at", "chwn", 260, 1, 3, func(w func(string, ...any)) {
		ssaAtFdcwd(w, "edi")
		w("\tmov rsi, r12")
		w("\tmov edx, r13d")  // uid
		w("\tmov r10d, r14d") // gid
		w("\tmov r8d, 256")   // AT_SYMLINK_NOFOLLOW
		w("\ttest r15, r15")
		w("\tjz .Lssa_chwn_flag")
		w("\txor r8d, r8d") // follow clears it
		w(".Lssa_chwn_flag:")
	})(w)
}

// emitSetFileTimesHelper writes set_file_times(path, atime_sec, atime_nsec,
// mtime_sec, mtime_nsec, flags) -> Result[(), IoError]: utimensat(AT_FDCWD,
// path, times, flags).
//
// The two `struct timespec`s go on the frame in the kernel's order, access
// time first. `flags` is Fern's word rather than the kernel's: bit 0 becomes
// AT_SYMLINK_NOFOLLOW, bits 3 and 4 become UTIME_NOW and bits 1 and 2
// UTIME_OMIT in the nanosecond half of the timespec they name — each is a
// sentinel VALUE to utimensat, not a flag, and the seconds half is then not
// read. Omit is written after now so that it wins when both name one half.
//
// rbx = path, r12 = pathz, r13 = its length, r14 = the flags.
func emitSetFileTimesHelper(w func(string, ...any)) {
	const (
		utimeNow  = 1073741823
		utimeOmit = 1073741822
	)
	w("")
	w("%s:", fnLabel("set_file_times"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	// The timespec pair plus the eight bytes that keep rsp 16-aligned past
	// four pushes.
	w("\tsub rsp, 40")
	w("\tmov rbx, rdi")
	w("\tmov r14, r9") // flags, across the NUL-termination call
	w("\tmov [rsp], rsi")
	w("\tmov [rsp + 8], rdx")
	w("\tmov [rsp + 16], rcx")
	w("\tmov [rsp + 24], r8")
	for _, sub := range []struct {
		val  int
		aBit int
		mBit int
		aLbl string
		mLbl string
	}{
		{utimeNow, 8, 16, "na", "nm"},
		{utimeOmit, 2, 4, "a", "m"},
	} {
		w("\tmov eax, %d", sub.val)
		w("\ttest r14d, %d", sub.aBit)
		w("\tjz .Lssa_sft_%s", sub.aLbl)
		w("\tmov qword ptr [rsp], 0")
		w("\tmov [rsp + 8], rax")
		w(".Lssa_sft_%s:", sub.aLbl)
		w("\ttest r14d, %d", sub.mBit)
		w("\tjz .Lssa_sft_%s", sub.mLbl)
		w("\tmov qword ptr [rsp + 16], 0")
		w("\tmov [rsp + 24], rax")
		w(".Lssa_sft_%s:", sub.mLbl)
	}
	ssaPathz(w, "sft")
	ssaAtFdcwd(w, "edi")
	w("\tmov rsi, r12")
	w("\tmov rdx, rsp") // &times
	w("\txor r10d, r10d")
	w("\ttest r14d, 1")
	w("\tjz .Lssa_sft_go")
	w("\tmov r10d, 256") // AT_SYMLINK_NOFOLLOW
	w(".Lssa_sft_go:")
	w("\tmov eax, 280") // utimensat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_sft_err")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_sft_ret")
	w(".Lssa_sft_err:")
	w("\tneg rax")
	ssaIoErr(w)
	w(".Lssa_sft_ret:")
	w("\tadd rsp, 40")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
