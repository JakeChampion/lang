package x86_64ssa

import (
	"strconv"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/ir"
)

// What a process can ask about the system it is running on: the filesystem a
// path sits on, the kernel's own name for itself, and the supplementary group
// set.

// getgroupsCache memoises getgroups()'s answer. The set cannot change under a
// process that is not asking to change it, and the container is handed out
// with the static rc sentinel, so one buffer serves every call.
const getgroupsCache = "__ssa_getgroups_cache"

// emitStatfsHelper writes statfs(path) -> Result[FsStat, IoError]: the
// geometry and the length limits of the filesystem the path resolves on.
//
// The same shape as stat(path) — a NUL-terminated path copy, a frame buffer,
// and the two boxed results — because it is the same contract. Linux-only,
// like the rest of this emitter, so there is no pathconf branch: PATH_MAX is
// the kernel's own constant for every filesystem it mounts.
//
// rbx = path, r12 = pathz, r13 = its length then the FsStat box.
func emitStatfsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("statfs"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned; the
	// 120-byte record rounded to 128 keeps it so.
	w("\tsub rsp, 128")
	w("\tmov rbx, rdi")
	ssaPathz(w, "sfs")
	w("\tmov rdi, r12")
	w("\tmov rsi, rsp")
	w("\tmov eax, 137") // statfs
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_sfs_err")
	// The record stays at [rsp] across the allocation, so it is copied out
	// once the box exists.
	ssaBumpAlloc(w, "rax", strconv.Itoa(8+int(ir.FsStat.Bytes)))
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tlea r13, [rax + 8]")     // FsStat data
	for _, f := range nativex86_64.LinuxStatfsFields {
		w("\tmov r9, [rsp + %d]", f.Src)
		w("\tmov [r13 + %d], r9", f.Box)
	}
	w("\tmov r9d, %d", nativex86_64.LinuxPathMax)
	w("\tmov [r13 + %d], r9", ir.FsStat.PathMax)
	ssaOptionBox(w, 0, "r13")
	w("\tjmp .Lssa_sfs_ret")
	w(".Lssa_sfs_err:")
	w("\tneg rax")
	ssaIoErr(w)
	w(".Lssa_sfs_ret:")
	w("\tadd rsp, 128")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitUnameFieldHelper writes uname_field(i) -> string: one field of the
// utsname record uname(2) fills. Six 65-byte fields, each NUL-terminated
// within its own, indexed 0 sysname through 4 machine. An index naming no
// field, a refused uname and an empty field all take the zero-length path.
//
// rbx = the field's bytes, r12 = its length, both across the guard call.
func emitUnameFieldHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("uname_field"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// 390 bytes of struct utsname, rounded up to keep rsp 16-aligned past
	// the three pushes.
	w("\tsub rsp, 400")
	w("\tmov r13d, edi") // the field index
	w("\tmov rbx, rsp")
	w("\txor r12d, r12d")
	w("\tcmp r13d, 0")
	w("\tjl .Lssa_uf_alloc")
	w("\tcmp r13d, 5")
	w("\tjge .Lssa_uf_alloc")
	w("\tmov rdi, rsp")
	w("\tmov eax, 63") // uname
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjnz .Lssa_uf_alloc") // refused: an empty field
	w("\timul r13d, r13d, 65")
	w("\tadd rbx, r13") // buffer + 65 * index
	w(".Lssa_uf_len:")
	w("\tcmp byte ptr [rbx + r12], 0")
	w("\tje .Lssa_uf_alloc")
	w("\tadd r12, 1")
	w("\tjmp .Lssa_uf_len")
	w(".Lssa_uf_alloc:")
	ssaStrFromBytes(w, "rbx", "r12")
	w("\tadd rsp, 400")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitGetgroupsHelper writes getgroups() -> i64[]: the process's
// supplementary group set, memoised.
//
// The kernel writes 32-bit gids and the array the language sees is i64[], so
// the widening walks BACKWARDS from the last element — forwards would
// overwrite the gid at 2i before reading the one at i.
//
// A refusal, or an empty set, answers an empty array rather than an error:
// getgroups has no failure a caller can act on here, and the language's
// signature has nowhere to put one.
//
// rbx = the count, r12 = the container's data, r13 = the widening cursor.
func emitGetgroupsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("getgroups"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	// Three pushes past the return address leave rsp 16-aligned.
	w("\tmov rax, [rip + %s]", getgroupsCache)
	w("\ttest rax, rax")
	w("\tjnz .Lssa_gg_ret")
	w("\txor edi, edi") // size 0 asks for the count
	w("\txor esi, esi")
	w("\tmov eax, 115") // getgroups
	w("\tsyscall")
	w("\tmov rbx, rax")
	w("\ttest rbx, rbx")
	w("\tjg .Lssa_gg_alloc")
	w("\txor ebx, ebx")
	w(".Lssa_gg_alloc:")
	// 16-byte header + count*8, as every array container here is laid out.
	w("\tlea rdx, [rbx*8 + 16]")
	ssaBumpAlloc(w, "rax", "rdx")
	w("\tlea r12, [rax + 16]")
	w("\tmov dword ptr [r12 - 12], ebx")       // cap
	w("\tmov dword ptr [r12 - 8], 0x80000000") // rc = static sentinel (cached, immortal)
	w("\tmov dword ptr [r12 - 4], ebx")        // len
	w("\ttest rbx, rbx")
	w("\tjz .Lssa_gg_done")
	w("\tmov edi, ebx")
	w("\tmov rsi, r12")
	w("\tmov eax, 115") // getgroups
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjle .Lssa_gg_zero")
	w("\tmov rbx, rax")
	w("\tmov dword ptr [r12 - 4], ebx")
	w("\tmov r13, rbx")
	w(".Lssa_gg_widen:")
	w("\tsub r13, 1")
	w("\tjs .Lssa_gg_done")
	w("\tmov eax, [r12 + r13*4]")
	w("\tmov [r12 + r13*8], rax")
	w("\tjmp .Lssa_gg_widen")
	w(".Lssa_gg_zero:")
	w("\tmov dword ptr [r12 - 4], 0")
	w(".Lssa_gg_done:")
	w("\tmov [rip + %s], r12", getgroupsCache)
	w("\tmov rax, r12")
	w(".Lssa_gg_ret:")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitGetgroupsBss writes the memo slot, emitted only when the module calls
// getgroups.
func emitGetgroupsBss(w func(string, ...any)) {
	w(".section .bss")
	w(".align 8")
	w("%s:", getgroupsCache)
	w("\t.quad 0")
}
