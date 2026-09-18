package x86_64ssa

import (
	"strconv"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/ir"
)

// The process arguments and environment, captured once in _start and read by
// the args() and env() helpers. arm64ssa's are the model; what differs here
// is where the kernel leaves them (argc at [rsp], argv at rsp+8, envp past
// argv's NULL) and the System V register file.
const (
	argcSym      = "__ssa_argc"
	argvSym      = "__ssa_argv"
	argsCacheSym = "__ssa_args_cache"
	envpSym      = "__ssa_envp"
	environCache = "__ssa_environ_cache"
)

// usesArgs reports whether the module references args(), so _start captures
// argc/argv and the .bss slots exist. usesEnv is the same for the two helpers
// that read the environment vector.
func usesArgs(helpers []string) bool { return referencesHelper(helpers, "args") }
func usesEnv(helpers []string) bool {
	return referencesHelper(helpers, "env") || referencesHelper(helpers, "environ") ||
		referencesHelper(helpers, "proc_exec")
}

func referencesHelper(helpers []string, name string) bool {
	for _, h := range helpers {
		if h == name {
			return true
		}
	}
	return false
}

// emitProcCapture writes the _start prologue that snapshots argc, argv and
// envp into .bss before anything else runs. It uses rax alone: the entry
// arguments have not been loaded yet, and the heap reservation that follows
// clobbers what it needs.
func emitProcCapture(w func(string, ...any), withArgs, withEnv bool) {
	if withArgs {
		w("\tmov rax, [rsp]") // argc
		w("\tmov [rip + %s], rax", argcSym)
		w("\tlea rax, [rsp + 8]") // &argv[0]
		w("\tmov [rip + %s], rax", argvSym)
	}
	if withEnv {
		// envp = rsp + 8 (argc) + argc*8 (the vector) + 8 (its NULL).
		w("\tmov rax, [rsp]")
		w("\tlea rax, [rsp + 16 + rax*8]")
		w("\tmov [rip + %s], rax", envpSym)
	}
}

// emitProcBss writes the .bss slots the capture fills.
func emitProcBss(w func(string, ...any), withArgs, withEnv bool) {
	if withArgs {
		w(".section .bss")
		w(".align 8")
		w("%s:", argcSym)
		w("\t.quad 0")
		w("%s:", argvSym)
		w("\t.quad 0")
		w("%s:", argsCacheSym)
		w("\t.quad 0")
	}
	if withEnv {
		w(".section .bss")
		w(".align 8")
		w("%s:", envpSym)
		w("\t.quad 0")
		w("%s:", environCache)
		w("\t.quad 0")
	}
}

// emitArgsHelper writes args() -> string[] over the argv vector, and
// emitEnvironHelper writes environ() -> string[] over envp. They differ only
// in how the entry count is reached: argv's came from the kernel in argc,
// while envp carries no count word and has to be scanned to its NULL.
func emitArgsHelper(w func(string, ...any)) {
	emitCStringVectorHelper(w, "args", "args", argsCacheSym, func(w func(string, ...any)) {
		w("\tmov rbx, [rip + %s]", argcSym)
		w("\tmov r12, [rip + %s]", argvSym)
	})
}

// emitEnvironHelper writes environ() -> string[]: every "NAME=VALUE" entry of
// the envp vector _start captured.
func emitEnvironHelper(w func(string, ...any)) {
	emitCStringVectorHelper(w, "environ", "environ", environCache, func(w func(string, ...any)) {
		w("\tmov r12, [rip + %s]", envpSym)
		w("\txor ebx, ebx")
		w(".Lssa_environ_count:")
		w("\tcmp qword ptr [r12 + rbx*8], 0")
		w("\tje .Lssa_environ_counted")
		w("\tinc rbx")
		w("\tjmp .Lssa_environ_count")
		w(".Lssa_environ_counted:")
	})
}

// emitCStringVectorHelper writes a helper that turns a NUL-terminated C string
// vector into a string[]: one single-word rc string per entry in a container
// whose header carries the static rc sentinel, as the flat backend's does,
// memoised in .bss so every call hands back the same buffer. The container's
// data pointer is 16-aligned (the bump base is), so element 0 lands at a
// 16-aligned address with cap / rc / len at data-12 / -8 / -4.
//
// `load` leaves the entry count in rbx and the vector base in r12. rbp and
// r13-r15 carry the loop; six pushes and the realigning slot keep rsp
// 16-aligned across the two allocations, each of which calls the heap guard.
func emitCStringVectorHelper(w func(string, ...any), name, tag, cacheSym string, load func(w func(string, ...any))) {
	w("")
	w("%s:", fnLabel(name))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	w("\tpush rbp")
	w("\tsub rsp, 8") // six pushes past the return address: realign
	w("\tmov rax, [rip + %s]", cacheSym)
	w("\ttest rax, rax")
	w("\tjnz .Lssa_%s_ret", tag)
	load(w)
	w("\tlea rdx, [rbx*8 + 16]")
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tlea r14, [rax + 16]")
	w("\tmov dword ptr [r14 - 12], ebx")       // cap
	w("\tmov dword ptr [r14 - 8], 0x80000000") // rc = static sentinel
	w("\tmov dword ptr [r14 - 4], ebx")        // len
	w("\txor r13d, r13d")
	w(".Lssa_%s_loop:", tag)
	w("\tcmp r13, rbx")
	w("\tjae .Lssa_%s_done", tag)
	w("\tmov r15, [r12 + r13*8]") // the entry, NUL-terminated
	w("\txor ecx, ecx")
	w(".Lssa_%s_slen:", tag)
	w("\tcmp byte ptr [r15 + rcx], 0")
	w("\tje .Lssa_%s_slend", tag)
	w("\tinc rcx")
	w("\tjmp .Lssa_%s_slen", tag)
	w(".Lssa_%s_slend:", tag)
	w("\tmov rbp, rcx")
	w("\tlea rdx, [rbp + %d]", strBlockBytes) // rc header (8) + bytes + NUL
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov [rax + 4], ebp")     // len
	w("\tlea rdi, [rax + 8]")     // data
	w("\txor ecx, ecx")
	w(".Lssa_%s_cp:", tag)
	w("\tcmp rcx, rbp")
	w("\tjae .Lssa_%s_cpd", tag)
	w("\tmov dl, [r15 + rcx]")
	w("\tmov [rdi + rcx], dl")
	w("\tinc rcx")
	w("\tjmp .Lssa_%s_cp", tag)
	w(".Lssa_%s_cpd:", tag)
	w("\tmov byte ptr [rdi + rbp], 0")
	w("\tmov [r14 + r13*8], rdi")
	w("\tinc r13")
	w("\tjmp .Lssa_%s_loop", tag)
	w(".Lssa_%s_done:", tag)
	w("\tmov [rip + %s], r14", cacheSym)
	w("\tmov rax, r14")
	w(".Lssa_%s_ret:", tag)
	w("\tadd rsp, 8")
	w("\tpop rbp")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitEnvHelper writes env(name) -> Option[string]: walk envp for an entry
// whose first name_len bytes match `name` and whose next byte is '=', and
// hand back a fresh rc string of what follows in a Some box; None when no
// entry matches.
//
// rdi = name. rbx = envp cursor, r12 = name data, r13 = name len, r14 =
// envp[i] then the value's first byte, r15 = value len, rbp = value data.
func emitEnvHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("env"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	w("\tpush rbp")
	w("\tsub rsp, 8")
	w("\tmov r12, rdi")
	w("\tmov r13d, %s", memRef("rdi", -4))
	w("\tmov rbx, [rip + %s]", envpSym)
	w(".Lssa_env_loop:")
	w("\tmov r14, [rbx]")
	w("\ttest r14, r14")
	w("\tjz .Lssa_env_none")
	w("\txor ecx, ecx")
	w(".Lssa_env_cmp:")
	w("\tcmp rcx, r13")
	w("\tjae .Lssa_env_eq")
	w("\tmov al, [r14 + rcx]")
	w("\tcmp al, [r12 + rcx]")
	w("\tjne .Lssa_env_next")
	w("\tinc rcx")
	w("\tjmp .Lssa_env_cmp")
	w(".Lssa_env_eq:")
	w("\tcmp byte ptr [r14 + r13], 61") // '='
	w("\tjne .Lssa_env_next")
	w("\tlea r14, [r14 + r13 + 1]") // the value's bytes
	w("\txor ecx, ecx")
	w(".Lssa_env_slen:")
	w("\tcmp byte ptr [r14 + rcx], 0")
	w("\tje .Lssa_env_slend")
	w("\tinc rcx")
	w("\tjmp .Lssa_env_slen")
	w(".Lssa_env_slend:")
	w("\tmov r15, rcx")
	w("\tlea rdx, [r15 + %d]", strBlockBytes)
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov [rax + 4], r15d")    // len
	w("\tlea rbp, [rax + 8]")     // data
	w("\txor ecx, ecx")
	w(".Lssa_env_cp:")
	w("\tcmp rcx, r15")
	w("\tjae .Lssa_env_cpd")
	w("\tmov dl, [r14 + rcx]")
	w("\tmov [rbp + rcx], dl")
	w("\tinc rcx")
	w("\tjmp .Lssa_env_cp")
	w(".Lssa_env_cpd:")
	w("\tmov byte ptr [rbp + r15], 0")
	ssaOptionBox(w, 0, "rbp")
	w("\tjmp .Lssa_env_ret")
	w(".Lssa_env_next:")
	w("\tadd rbx, 8")
	w("\tjmp .Lssa_env_loop")
	w(".Lssa_env_none:")
	ssaOptionBox(w, 1, "")
	w(".Lssa_env_ret:")
	w("\tadd rsp, 8")
	w("\tpop rbp")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitStatHelper writes stat(path) -> Result[FileStat, IoError].
func emitStatHelper(w func(string, ...any)) {
	emitStatLikeHelper(w, "stat", "stat", 0, false)
}

// emitLstatHelper writes lstat(path) -> Result[FileStat, IoError]: the same
// body with AT_SYMLINK_NOFOLLOW, so a symlink reports itself rather than its
// target.
func emitLstatHelper(w func(string, ...any)) {
	emitStatLikeHelper(w, "lstat", "lstat", 256, false)
}

// emitFdStatHelper writes __method_Reader_stat / __method_Writer_stat
// (handle) -> Result[FileStat, IoError]: fstat(2) of the fd at [handle+8],
// projected by the same body as stat(path), as arm64ssa's emitFdStatHelper
// is. `lp` prefixes the local labels so both methods can live in one object.
func emitFdStatHelper(name, lp string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		emitStatLikeHelper(w, name, lp, 0, true)
	}
}

// emitStatLikeHelper writes the shared body behind stat(path) and the two fd
// methods: the stat record into a 144-byte frame buffer, projected onto a
// fresh FileStat box by the same table the flat x86-64 backend reads
// (nativex86_64.LinuxStatFields), and wrapped in Ok; -errno goes through
// __fern_io_error and comes back in Err. The record box is {rc=1, fields@+8},
// as arm64ssa's is.
//
// fdBased picks newfstatat(AT_FDCWD, pathz, buf, atFlags) over fstat(fd, buf),
// and with it what the error reports: the path as given, or an empty string
// when the handle never carried one. atFlags is the newfstatat flag word, 0
// for stat and AT_SYMLINK_NOFOLLOW for lstat. `lp` prefixes the local labels,
// so the path form and both fd forms can be emitted into one object.
//
// rdi = path or handle. rbx = path, r12 = pathz then the FileStat box,
// r13 = path len then st_size, r14 = is_file, r15 = is_dir.
func emitStatLikeHelper(w func(string, ...any), name, lp string, atFlags int, fdBased bool) {
	w("")
	w("%s:", fnLabel(name))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	// Five pushes past the return address leave rsp 16-aligned; the 144-byte
	// statbuf keeps it so.
	w("\tsub rsp, 144")
	if fdBased {
		// fstat(fd, statbuf); the fd sits at [handle+8].
		w("\tmov edi, dword ptr [rdi + 8]")
		w("\tmov rsi, rsp")
		w("\tmov eax, 5")
		w("\tsyscall")
	} else {
		w("\tmov rbx, rdi")
		w("\tmov r13d, %s", memRef("rbx", -4))
		w("\tlea rdx, [r13 + 1]")
		ssaBumpAlloc(w, "rax", "rdx")
		w("\tmov r12, rax") // pathz
		w("\txor ecx, ecx")
		w(".Lssa_%s_cp:", lp)
		w("\tcmp rcx, r13")
		w("\tjae .Lssa_%s_cpd", lp)
		w("\tmov al, [rbx + rcx]")
		w("\tmov [r12 + rcx], al")
		w("\tinc rcx")
		w("\tjmp .Lssa_%s_cp", lp)
		w(".Lssa_%s_cpd:", lp)
		w("\tmov byte ptr [r12 + r13], 0")
		// newfstatat(AT_FDCWD, pathz, statbuf, atFlags)
		w("\tmov edi, -100")
		w("\tmov rsi, r12")
		w("\tmov rdx, rsp")
		if atFlags == 0 {
			w("\txor r10d, r10d")
		} else {
			w("\tmov r10d, %d", atFlags)
		}
		w("\tmov eax, 262")
		w("\tsyscall")
	}
	w("\ttest rax, rax")
	w("\tjs .Lssa_%s_err", lp)
	w("\tmov eax, [rsp + 24]") // st_mode
	w("\tand eax, 61440")      // S_IFMT
	w("\txor r14d, r14d")
	w("\tcmp eax, 32768") // S_IFREG
	w("\tsete r14b")
	w("\txor r15d, r15d")
	w("\tcmp eax, 16384") // S_IFDIR
	w("\tsete r15b")
	w("\tmov r13, [rsp + 48]") // st_size
	// The statbuf stays at [rsp] across the allocation, so the rest of the
	// record is copied out once the box exists.
	ssaBumpAlloc(w, "rax", strconv.Itoa(8+int(ir.FileStat.Bytes)))
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tlea r12, [rax + 8]")     // FileStat data
	w("\tmov [r12 + %d], r14d", ir.FileStat.IsFile)
	w("\tmov [r12 + %d], r15d", ir.FileStat.IsDir)
	w("\tmov [r12 + %d], r13", ir.FileStat.Size)
	for _, f := range nativex86_64.LinuxStatFields {
		if f.Width == 4 {
			w("\tmov r9d, [rsp + %d]", f.Src)
			w("\tmov [r12 + %d], r9d", f.Box)
			continue
		}
		w("\tmov r9, [rsp + %d]", f.Src)
		w("\tmov [r12 + %d], r9", f.Box)
	}
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_%s_ret", lp)
	w(".Lssa_%s_err:", lp)
	w("\tneg rax")
	if fdBased {
		// No path reached this helper, so the error carries an empty one,
		// as a failed read does.
		w("\tmov r13, rax") // errno, across the allocation
		ssaEmptyString(w, "rsi")
		w("\tmov edi, r13d")
	} else {
		ssaRetainPathForIoErr(w)
		w("\tmov edi, eax") // errno
		w("\tmov rsi, rbx") // the path, as given
	}
	w("\tcall %s", fnLabel("__fern_io_error"))
	w("\tmov r12, rax")
	ssaOptionBox(w, 1, "r12")
	w(".Lssa_%s_ret:", lp)
	w("\tadd rsp, 144")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
