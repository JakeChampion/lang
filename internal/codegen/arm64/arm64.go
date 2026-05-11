// Package arm64 emits ARM64 (aarch64) Linux assembly from a
// checked + monomorphised lang program. Companion to the
// existing arm32 backend; shares the IR layer but emits its
// own ISA + syscall wiring.
//
// ABI: AAPCS64. Args in x0..x7; return value in x0; frame
// pointer x29; link register x30; stack pointer must stay
// 16-byte aligned at memory accesses (Linux sets SCTLR.SA so
// any sp-relative access with mis-aligned sp faults).
//
// Operand stack: simulated on the physical sp via paired
// push/pop. Each push uses `str x0, [sp, #-16]!` — a 16-byte
// stride (8 bytes wasted per slot) keeps sp 16-aligned for
// the next memory access. Future PR can switch to packed
// pairs (stp / ldp) once the codegen consistently knows when
// two consecutive pushes happen.
//
// Syscalls: x8 holds the syscall number, x0..x5 carry args,
// `svc #0` traps to the kernel. Numbers come from the
// asm-generic table (Linux arm64 has no legacy renumbering
// — the same numbers apply to every distribution).
package arm64

import (
	"fmt"
	"math"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Linux arm64 syscall numbers from the asm-generic table.
// Only what the runtime needs at this stage. arm32 uses the
// older legacy EABI table with completely different numbers
// (e.g. write=4 vs arm64's 64); we don't share constants
// between backends.
const (
	sysRead      = 63
	sysWrite     = 64
	sysClose     = 57
	sysExitGroup = 94
	sysSocket    = 198
	sysBind      = 200
	sysListen    = 201
	sysAccept    = 202
)

// Darwin BSD syscall numbers (xnu/bsd/kern/syscalls.master).
// Completely disjoint from the Linux table above. Darwin uses
// `svc #0x80` with the number in x16, and on error returns
// +errno in x0 with the C flag set (vs Linux's -errno in x0).
const (
	darRead   = 3
	darWrite  = 4
	darClose  = 6
	darExit   = 1
	darAccept = 30
	darSocket = 97
	darBind   = 104
	darListen = 106
)

// linuxDarwinSysno maps a logical syscall name to (Linux, Darwin)
// numbers. Used by syscall() to pick the right immediate.
var linuxDarwinSysno = map[string][2]int{
	"read":   {sysRead, darRead},
	"write":  {sysWrite, darWrite},
	"close":  {sysClose, darClose},
	"socket": {sysSocket, darSocket},
	"bind":   {sysBind, darBind},
	"listen": {sysListen, darListen},
	"accept": {sysAccept, darAccept},
}

// regArgs is the AAPCS64 register-argument count: args 0..7
// arrive in x0..x7. Anything beyond that goes through the
// caller's stack frame. arm32 has 4 register-arg slots; arm64
// gives us 8 — enough to keep most user functions register-
// only.
const regArgs = 8

// Options tunes the emit. Currently empty.
type Options struct {
	// Darwin selects Mach-O / Apple-style assembly conventions
	// (leading-underscore symbol prefix `_<name>`, macOS BSD
	// syscall numbers, `svc #0x80` with x16 as the syscall
	// number register). Disabled by default (Linux ELF). The
	// driver flips this on for `-target arm64-darwin`. Real
	// Mach-O Object emission happens at the clang+lld step;
	// the asm we produce is the GAS-flavoured cross-platform
	// shape both Linux's `as` and macOS's `clang -c -arch
	// arm64` accept.
	Darwin bool
}

// Emit produces the assembly text for prog.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithOptions returns the assembly text for prog. Lowers
// each surviving (post-treeshake) function via the IR layer.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	treeshake.Run(prog)
	ip, err := ir.Lower(prog, info)
	if err != nil {
		return "", err
	}
	g := &generator{info: info, stringLabel: map[string]string{}, darwin: opts.Darwin}
	// Pre-scan IR functions to set use-flags that emitStartRuntime
	// reads. emitStartRuntime runs before the per-function walk
	// so any flag set inside emitOp wouldn't influence the
	// prologue. For args() / env() the prologue needs to know
	// in advance so it can stash argc / argv / envp from the
	// kernel-delivered stack before main runs.
	for _, fn := range ip.Funcs {
		for _, op := range fn.Ops {
			if op.Kind != ir.OpCallDirect {
				continue
			}
			switch op.Str {
			case "args":
				g.usesArgs = true
			case "env":
				g.usesEnv = true
			}
		}
	}
	g.line(`.arch armv8-a`)
	g.line(`.text`)
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	if g.usesAlloc {
		g.emitAllocRuntime()
	}
	if g.usesMemcpy {
		g.emitMemcpyRuntime()
	}
	if g.usesStrcmp {
		g.emitStrcmpRuntime()
	}
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	if g.usesRawIntPokes {
		g.emitRawIntPokesRuntime()
	}
	if g.usesMemset {
		g.emitMemsetRuntime()
	}
	if g.usesTcp {
		g.emitTcpListenRuntime()
		g.emitTcpAcceptRuntime()
		g.emitTcpRecvRuntime()
		g.emitTcpSendRuntime()
		g.emitTcpCloseRuntime()
	}
	if g.usesStrSlice {
		g.emitStrSliceRuntime()
	}
	if g.usesEnv {
		g.emitEnvRuntime()
	}
	if g.usesAllocU8 {
		g.emitAllocU8Runtime()
	}
	if g.usesStringFromBytes {
		g.emitStringFromBytesRuntime()
	}
	if g.usesPuts {
		g.emitPutsRuntime()
	}
	if g.usesWrite {
		g.emitWriteRuntime()
	}
	if g.usesPutchar {
		g.emitPutcharRuntime()
	}
	if g.usesEprint {
		g.emitEprintRuntime()
	}
	if g.usesExit {
		g.emitExitRuntime()
	}
	if g.usesArgs {
		g.emitArgsRuntime()
	}
	g.emitDataSections()
	if !g.darwin {
		// `.note.GNU-stack` is an ELF-only directive — it
		// marks the program's stack as non-executable. Mach-O
		// has the equivalent via header flags; the assembler
		// here rejects the directive on Darwin.
		g.line(`.section .note.GNU-stack,"",%progbits`)
	}
	return g.out.String(), nil
}

// emitDataSections writes `.rodata` (interned string literals)
// and `.bss` (the bump-allocator cursor + heap-end sentinel).
// All entries are gated on usage so unused programs pay
// nothing — `.bss` is omitted entirely when the allocator
// isn't pulled in.
func (g *generator) emitDataSections() {
	g.line("")
	if g.darwin {
		// Mach-O cstring section — read-only ASCII strings.
		// Mach-O's native equivalent of ELF's `.section
		// .rodata`. The `cstring_literals` attribute lets the
		// linker dedupe identical NUL-terminated string
		// constants across object files.
		g.line(`.section __TEXT,__cstring,cstring_literals`)
	} else {
		g.line(`.section .rodata`)
	}
	for _, s := range g.stringOrder {
		// 4-byte little-endian length prefix + .asciz data.
		// Pointers handed to user code address the .asciz base
		// (.LStr_N); `len()` reads `[ptr - 4]`. Same layout as
		// arm32; same as wasm at the byte level.
		g.line(`.align 2`)
		g.line(fmt.Sprintf("\t.4byte %d", len(s)))
		g.label(g.stringLabel[s])
		g.line("\t.asciz " + escapeForGAS(s))
	}
	if g.usesPuts || g.usesEprint {
		// Single newline byte emitted into the same section as
		// the string literals. __lang_puts / __lang_eprint
		// write `s` followed by a 1-byte write of this label.
		// We use `.asciz` rather than `.byte 10` so Mach-O's
		// `cstring_literals` attribute (which requires NUL-
		// terminated strings) accepts the entry — the trailing
		// NUL is harmless, the write only reads the first byte.
		g.label(".LLangNewline")
		g.line(`	.asciz "\n"`)
	}
	if g.usesAlloc || g.usesEnv || g.usesArgs {
		g.line("")
		if g.darwin {
			// Mach-O zero-initialised data lives in
			// __DATA,__bss. The `zero_fill` directive shape
			// (`.zerofill SEGMENT,SECTION,SYMBOL,SIZE`) is the
			// idiomatic way but a `.section` + `.space` pair
			// also works and keeps the code path uniform with
			// the ELF emit.
			g.line(`.section __DATA,__bss`)
		} else {
			g.line(`.section .bss`)
		}
	}
	if g.usesAlloc {
		g.line(`.align 3`) // 8-byte alignment for the cursor pair
		g.label("__lang_heap_ptr")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_heap_end")
		g.line(`	.quad 0`)
	}
	if g.usesEnv {
		g.line(`.align 3`)
		g.label("__lang_envp")
		g.line(`	.quad 0`)
	}
	if g.usesArgs {
		g.line(`.align 3`)
		g.label("__lang_argc")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_argv")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_args_cache")
		g.line(`	.quad 0`)
	}
}

// emitAllocRuntime emits `__lang_alloc(size: i64) -> i64`
// using mmap2 — same shape as arm32 but with arm64 syscall
// numbers (sysMmap = 222) and 64-bit pointer arithmetic.
// First call lazily reserves the heap arena via mmap; later
// calls bump the cursor.
//
// Bump-only allocator (no free) — matches wasm's semantics
// and the arm32 backend's choice. The arena is reclaimed
// by the OS at process exit.
func (g *generator) emitAllocRuntime() {
	const heapBytes = 64 * 1024 * 1024 // 64 MiB virtual reservation
	g.line("")
	g.line(".global __lang_alloc")
	g.typeDirective("__lang_alloc")
	g.label("__lang_alloc")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	// Round size up to 16-byte alignment so subsequent allocs
	// stay 16-aligned for `stp` / `ldp`.
	g.emit("add x0, x0, #15")
	g.emit("and x0, x0, #-16")
	// Lazy heap init: if heap_ptr == 0, mmap-reserve a 64 MiB
	// arena. Linux populates physical pages on first touch, so
	// the virtual reservation costs essentially nothing.
	g.adrpAdd("x1", "__lang_heap_ptr")
	g.emit("ldr x2, [x1]")
	g.emit("cbnz x2, .Lalloc_have_heap")
	// mmap(NULL, 64 MiB, RW, PRIVATE|ANON, -1, 0)
	g.emit("mov x9, x0")        // save size across the syscall
	// Hint the mmap to a low (i32-fittable) address. The
	// lang prelude stores pointers as i32 (designed against
	// wasm32); on arm64 we need every heap address to fit
	// in 32 bits so the prelude's __store_i32 / __load_i32
	// don't truncate when round-tripping pointers. 256 MiB
	// is well below the 4 GiB i32 ceiling and far enough
	// from the binary's text/data that collisions are rare.
	// Linux usually honours the hint when the requested
	// range is free; if it doesn't, the bump allocator
	// would silently truncate addresses above 4 GiB, which
	// is the migration-to-i64-pointers fix tracked
	// separately.
	g.emit("mov x0, #0x1000")
	g.emit("lsl x0, x0, #16")    // x0 = 0x10000000 = 256 MiB
	g.emit("ldr x1, =%d", heapBytes) // length = 64 MiB
	g.emit("mov x2, #3")        // PROT_READ | PROT_WRITE (same on both)
	if g.darwin {
		// Darwin BSD MAP_PRIVATE=0x02 + MAP_ANON=0x1000 = 0x1002.
		// (Linux uses 0x20 for MAP_ANONYMOUS.) Darwin mmap is
		// syscall #197 with svc #0x80; Linux is #222 with
		// svc #0. Same in-register arg shape (x0..x5).
		//
		// macOS ignores our 0x10000000 addr hint and returns
		// a high address. That's only a problem for programs
		// that round-trip heap pointers through 32-bit storage
		// slots (the lang Map runtime via __store_i32 /
		// __load_i32). Plain string concat / array literals
		// keep pointers 64-bit on the operand stack and work
		// regardless. Tried MAP_FIXED + -pagezero_size on
		// macos-14 ld64 first but the shrunk PAGEZERO
		// produced a binary the loader rejected even for
		// non-allocating programs. Proper fix is widening
		// the prelude's pointer storage to i64; tracked
		// separately.
		g.emit("mov x3, #0x1002") // MAP_PRIVATE | MAP_ANON (Darwin)
		g.emit("mov x4, #-1")
		g.emit("mov x5, #0")
		g.emit("mov x16, #197")   // SYS_mmap (Darwin BSD)
		g.emit("svc #0x80")
		// Darwin mmap returns MAP_FAILED == -1 cast to ptr on
		// error (vs Linux's -errno). The cmn below still
		// catches both shapes: -errno is negative, and -1 is
		// also negative when read as signed.
	} else {
		g.emit("mov x3, #0x22")     // MAP_PRIVATE | MAP_ANONYMOUS (Linux)
		g.emit("mov x4, #-1")
		g.emit("mov x5, #0")
		g.emit("mov x8, #222")      // sys_mmap (Linux asm-generic)
		g.emit("svc #0")
	}
	// On failure mmap returns -errno (negative). Trap.
	g.emit("cmn x0, #0")
	g.emit("blt .Lalloc_oom")
	g.emit("mov x10, x0")       // x10 = base
	g.adrpAdd("x1", "__lang_heap_ptr")
	g.emit("str x10, [x1]")
	g.adrpAdd("x2", "__lang_heap_end")
	g.emit("ldr x3, =%d", heapBytes)
	g.emit("add x3, x10, x3")
	g.emit("str x3, [x2]")
	g.emit("mov x0, x9")        // restore size
	g.label(".Lalloc_have_heap")
	// Bump the cursor: ptr = heap_ptr; heap_ptr += size; return ptr.
	g.adrpAdd("x1", "__lang_heap_ptr")
	g.emit("ldr x2, [x1]")      // x2 = current ptr
	g.emit("add x3, x2, x0")    // x3 = wanted top
	g.adrpAdd("x4", "__lang_heap_end")
	g.emit("ldr x4, [x4]")
	g.emit("cmp x3, x4")
	g.emit("bhi .Lalloc_oom")
	g.emit("str x3, [x1]")
	g.emit("mov x0, x2")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.label(".Lalloc_oom")
	// SIGABRT-style trap (137 = SIGKILL+128, OOM-killer convention).
	g.emit("mov x0, #137")
	g.syscallExit()
	g.sizeDirective("__lang_alloc")
	g.line(".ltorg")
}

// emitMemcpyRuntime emits `__lang_memcpy(dst, src, n)` —
// byte-grain copy. Word-grain bulk path runs in 8-byte chunks
// (vs arm32's 4) since arm64 has 64-bit registers; tail loop
// handles the residue. Pointers may be unaligned (arm64
// allows unaligned access by default in user-mode Linux, same
// as arm32).
func (g *generator) emitMemcpyRuntime() {
	g.line("")
	g.line(".global __lang_memcpy")
	g.typeDirective("__lang_memcpy")
	g.label("__lang_memcpy")
	// r0 = dst (saved for return), r1 = src, r2 = n.
	g.emit("mov x3, x0") // x3 = dst saved
	g.label(".Lmcp_word")
	g.emit("cmp x2, #8")
	g.emit("blt .Lmcp_tail")
	g.emit("ldr x4, [x1], #8")
	g.emit("str x4, [x0], #8")
	g.emit("sub x2, x2, #8")
	g.emit("b .Lmcp_word")
	g.label(".Lmcp_tail")
	g.emit("cmp x2, #0")
	g.emit("beq .Lmcp_done")
	g.emit("ldrb w4, [x1], #1")
	g.emit("strb w4, [x0], #1")
	g.emit("sub x2, x2, #1")
	g.emit("b .Lmcp_tail")
	g.label(".Lmcp_done")
	g.emit("mov x0, x3")
	g.emit("ret")
	g.sizeDirective("__lang_memcpy")
	g.line(".ltorg")
}

// emitStrcatRuntime emits `__lang_strcat(a, b)` — concat two
// length-prefixed strings into a fresh allocation. Same shape
// as arm32; both string operands are data pointers (post-
// prefix) with the 4-byte length at `[ptr - 4]`.
//
// Uses callee-save x19..x23 to keep state across the calls
// to __lang_alloc and __lang_memcpy. AAPCS64 says x19..x28
// must be preserved by the callee, so the saved-pair pattern
// at function entry / exit guarantees the values are restored
// before returning to the strcat caller.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".global __lang_strcat")
	g.typeDirective("__lang_strcat")
	g.label("__lang_strcat")
	// Frame: 64 bytes — saved fp/lr (16) + 6 callee-save
	// registers (40 used + 8 pad) rounded up to 64 for sp
	// alignment. x19=a, x20=b, x21=la, x22=lb, x23=new_base.
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("str x23, [sp, #48]")
	g.emit("mov x19, x0")
	g.emit("mov x20, x1")
	// Load lengths from [ptr - 4].
	g.emit("ldur w21, [x19, #-4]")
	g.emit("ldur w22, [x20, #-4]")
	// alloc(la + lb + 4) for the new buffer (length prefix + data).
	g.emit("add x0, x21, x22")
	g.emit("add x0, x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov x23, x0") // x23 = new base (callee-save survives bl)
	// Write length prefix.
	g.emit("add w5, w21, w22")
	g.emit("str w5, [x23]")
	// dst = base + 4; copy a then b.
	g.emit("add x0, x23, #4")
	g.emit("mov x1, x19")
	g.emit("mov x2, x21")
	g.emit("bl __lang_memcpy")
	g.emit("add x0, x23, #4")
	g.emit("add x0, x0, x21")
	g.emit("mov x1, x20")
	g.emit("mov x2, x22")
	g.emit("bl __lang_memcpy")
	// Return data pointer (base + 4).
	g.emit("add x0, x23, #4")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__lang_strcat")
	g.line(".ltorg")
}

// emitInlineIdxHelper inlines a `__str_idx` / `__arr_idx` /
// `__slice_idx_*` bounds-check call as a plain address compute
// (`base + index * stride`). The IR walker follows the helper
// call with an OpLoad / OpLoadByte that reads from x0.
//
// arm64-specific: we can use `add x0, x1, x0, lsl #N` where N
// is the log2 stride. AArch64 supports an LSL shift amount in
// the operand-2 position — folds the multiply into the add.
func (g *generator) emitInlineIdxHelper(name string) error {
	g.emit("ldr x0, [sp], #16") // idx
	g.emit("ldr x1, [sp], #16") // base
	switch name {
	case "__str_idx", "__slice_idx_1":
		g.emit("add x0, x1, x0")
	case "__slice_idx_2":
		g.emit("add x0, x1, x0, lsl #1")
	case "__arr_idx", "__slice_idx":
		g.emit("add x0, x1, x0, lsl #2")
	case "__slice_idx_8":
		g.emit("add x0, x1, x0, lsl #3")
	default:
		return fmt.Errorf("arm64: unknown index helper %q", name)
	}
	g.push()
	return nil
}

// emitStrcmpRuntime emits `__lang_strcmp(a, b)` — equality
// comparator returning 0 (equal) / 1 (different). Same shape
// as arm32 (length-prefix + word-grain bulk + byte-grain
// tail); pointer args are post-prefix.
func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".global __lang_strcmp")
	g.typeDirective("__lang_strcmp")
	g.label("__lang_strcmp")
	// 1. Same pointer? Equal.
	g.emit("cmp x0, x1")
	g.emit("beq .Lscmp_eq")
	// 2. Same length?
	g.emit("ldur w2, [x0, #-4]")
	g.emit("ldur w3, [x1, #-4]")
	g.emit("cmp w2, w3")
	g.emit("bne .Lscmp_neq")
	// 3a. Word-grain bulk — w2 holds remaining bytes.
	g.label(".Lscmp_word")
	g.emit("cmp w2, #4")
	g.emit("blt .Lscmp_tail")
	g.emit("ldr w4, [x0], #4")
	g.emit("ldr w5, [x1], #4")
	g.emit("cmp w4, w5")
	g.emit("bne .Lscmp_neq")
	g.emit("sub w2, w2, #4")
	g.emit("b .Lscmp_word")
	// 3b. Byte-grain tail.
	g.label(".Lscmp_tail")
	g.emit("cmp w2, #0")
	g.emit("beq .Lscmp_eq")
	g.emit("ldrb w4, [x0], #1")
	g.emit("ldrb w5, [x1], #1")
	g.emit("cmp w4, w5")
	g.emit("bne .Lscmp_neq")
	g.emit("sub w2, w2, #1")
	g.emit("b .Lscmp_tail")
	g.label(".Lscmp_eq")
	g.emit("mov x0, #0")
	g.emit("ret")
	g.label(".Lscmp_neq")
	g.emit("mov x0, #1")
	g.emit("ret")
	g.sizeDirective("__lang_strcmp")
	g.line(".ltorg")
}

// emitRawIntPokesRuntime emits `__store_i32(addr, val)` and
// `__load_i32(addr) -> i32`. The lang Map runtime calls these
// for its mixed bucket-index + entries buffer where the
// caller owns the layout (no length prefix). Single STR / LDR
// + ret each — leaf functions.
//
// Also emits `__store_ptr(addr, val)` / `__load_ptr(addr)`,
// the pointer-width counterparts. On 64-bit arm64 these
// store/load 8 bytes so that heap addresses round-trip
// without truncation. The Map runtime uses these for its
// data-ptr field (`m → buf` handle indirection); the rest
// of its mixed buffer stays i32 for compact length / hash /
// bucket-index storage. Same flag gates both pairs so a
// Map-using program pulls them in together.
func (g *generator) emitRawIntPokesRuntime() {
	g.line("")
	g.line(".global __load_i32")
	g.typeDirective("__load_i32")
	g.label("__load_i32")
	g.emit("ldr w0, [x0]")
	g.emit("ret")
	g.sizeDirective("__load_i32")

	g.line("")
	g.line(".global __store_i32")
	g.typeDirective("__store_i32")
	g.label("__store_i32")
	g.emit("str w1, [x0]")
	g.emit("ret")
	g.sizeDirective("__store_i32")

	g.line("")
	g.line(".global __load_ptr")
	g.typeDirective("__load_ptr")
	g.label("__load_ptr")
	g.emit("ldr x0, [x0]") // 8-byte load
	g.emit("ret")
	g.sizeDirective("__load_ptr")

	g.line("")
	g.line(".global __store_ptr")
	g.typeDirective("__store_ptr")
	g.label("__store_ptr")
	g.emit("str x1, [x0]") // 8-byte store
	g.emit("ret")
	g.sizeDirective("__store_ptr")
	g.line(".ltorg")
}

// emitMemsetRuntime emits `__memset(dst, byte, n)` — byte-
// grain fill matching the wasm bulk-memory shim. Word-grain
// bulk path replicates the byte across all eight lanes;
// byte-grain tail handles the residue. Pairs with the Map
// runtime's clear path.
func (g *generator) emitMemsetRuntime() {
	g.line("")
	g.line(".global __memset")
	g.typeDirective("__memset")
	g.label("__memset")
	// x0 = dst, w1 = byte (low 8 bits), x2 = n.
	g.emit("and w1, w1, #0xff")
	// Replicate the byte across 8 bytes (64 bits).
	g.emit("orr w3, w1, w1, lsl #8")
	g.emit("orr w3, w3, w3, lsl #16")
	g.emit("orr x3, x3, x3, lsl #32")
	g.label(".Lmset_word")
	g.emit("cmp x2, #8")
	g.emit("blt .Lmset_tail")
	g.emit("str x3, [x0], #8")
	g.emit("sub x2, x2, #8")
	g.emit("b .Lmset_word")
	g.label(".Lmset_tail")
	g.emit("cmp x2, #0")
	g.emit("beq .Lmset_done")
	g.emit("strb w1, [x0], #1")
	g.emit("sub x2, x2, #1")
	g.emit("b .Lmset_tail")
	g.label(".Lmset_done")
	g.emit("ret")
	g.sizeDirective("__memset")
	g.line(".ltorg")
}

// emitAllocU8Runtime emits `__alloc_u8(n)` — allocates a
// fresh length-prefixed `u8[]` of n bytes. Returns the data
// pointer (header + 4); length at `[data - 4]`.
func (g *generator) emitAllocU8Runtime() {
	g.line("")
	g.line(".global __alloc_u8")
	g.typeDirective("__alloc_u8")
	g.label("__alloc_u8")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0")     // x19 = n (callee-save, survives bl)
	g.emit("add x0, x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("str w19, [x0]")   // length prefix
	g.emit("add x0, x0, #4")  // return data ptr
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__alloc_u8")
	g.line(".ltorg")
}

// emitStringFromBytesRuntime emits `string_from_bytes(bs)` —
// copy a `u8[]` payload into a fresh length-prefixed string.
// Round-trip companion to `s.bytes()`.
func (g *generator) emitStringFromBytesRuntime() {
	g.line("")
	g.line(".global string_from_bytes")
	g.typeDirective("string_from_bytes")
	g.label("string_from_bytes")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0")           // x19 = bs
	g.emit("ldur w20, [x19, #-4]")  // x20 = length
	g.emit("add x0, x20, #4")
	g.emit("bl __lang_alloc")
	g.emit("str w20, [x0]")
	g.emit("mov x21, x0")           // x21 = alloc base (callee-save)
	g.emit("add x0, x0, #4")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __lang_memcpy")
	g.emit("add x0, x21, #4")       // return data ptr
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("string_from_bytes")
	g.line(".ltorg")
}

// emitStrSliceRuntime emits `__str_slice(base, low, high)` —
// allocates a fresh length-prefixed string holding
// `base[low..high]`. Bounds-traps on `low < 0`, `high >
// src_len`, or `low > high`. Used by every `s[a:b]` slice
// expression on a string.
func (g *generator) emitStrSliceRuntime() {
	g.line("")
	g.line(".global __str_slice")
	g.typeDirective("__str_slice")
	g.label("__str_slice")
	// Args: x0 = base, x1 = low, x2 = high.
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("mov x19, x0") // x19 = base
	g.emit("mov x20, x1") // x20 = low
	g.emit("mov x21, x2") // x21 = high
	g.emit("ldur w22, [x19, #-4]") // x22 = src_len
	// low < 0 → trap
	g.emit("cmp x20, #0")
	g.emit("blt .Lstrslice_trap")
	// high > src_len → trap (unsigned)
	g.emit("cmp x21, x22")
	g.emit("bhi .Lstrslice_trap")
	// low > high → trap
	g.emit("cmp x20, x21")
	g.emit("bgt .Lstrslice_trap")
	// new_len = high - low; alloc(new_len + 4).
	g.emit("sub x0, x21, x20")
	g.emit("add x0, x0, #4")
	g.emit("bl __lang_alloc")
	// x0 = alloc base. Write length prefix.
	g.emit("sub w3, w21, w20")     // new_len (i32)
	g.emit("str w3, [x0]")
	// memcpy(out + 4, base + low, new_len).
	g.emit("add x4, x0, #4")       // dst
	g.emit("add x1, x19, x20")     // src = base + low
	g.emit("mov x2, x3")           // n
	g.emit("mov x19, x0")          // stash alloc base in x19 across bl
	g.emit("mov x0, x4")
	g.emit("bl __lang_memcpy")
	g.emit("add x0, x19, #4")      // return data ptr (alloc base + 4)
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.label(".Lstrslice_trap")
	g.emit("mov x0, #134")
	g.syscallExit()
	g.sizeDirective("__str_slice")
	g.line(".ltorg")
}

// emitEnvRuntime emits `__lang_env(name)` — walks the envp
// vector for `NAME=VALUE` and returns the value as a fresh
// lang Option[string]. None is the heap-allocated Option
// variant (tag=1); Some carries the value pointer in payload+0.
//
// First-PR scope returns a raw string pointer (Some(value) as
// just the value's data ptr) — matches the simpler shape
// the synthesised `__port_from_env` expects. The full
// Option enum encoding (tag + payload) on arm64 will fall
// out from existing enum lowering work; for now we encode
// Option[string] manually: a 8-byte heap object [tag, ptr],
// where tag=0 = Some, tag=1 = None.
func (g *generator) emitEnvRuntime() {
	g.line("")
	g.line(".global __lang_env")
	g.typeDirective("__lang_env")
	g.label("__lang_env")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("mov x19, x0")  // x19 = name (string data ptr)
	g.emit("ldur w20, [x19, #-4]") // x20 = name_len
	g.adrpAdd("x21", "__lang_envp")
	g.emit("ldr x21, [x21]")       // x21 = envp
	g.label(".Lenv_loop")
	g.emit("ldr x22, [x21]")        // x22 = envp[i]
	g.emit("cbz x22, .Lenv_none")   // NULL terminator → return None
	// Compare first name_len bytes of envp[i] with name, then check '='.
	g.emit("mov x0, x22")           // candidate envp entry
	g.emit("mov x1, x19")           // name
	g.emit("mov x2, x20")           // n
	g.emit("bl __memcmp_n_env")
	g.emit("cbnz w0, .Lenv_next")   // not equal
	// Check that byte at offset name_len is '='.
	g.emit("ldrb w0, [x22, x20]")
	g.emit("cmp w0, #61")           // '='
	g.emit("bne .Lenv_next")
	// Found. Build a fresh lang string holding the value after '='.
	g.emit("add x0, x22, x20")
	g.emit("add x0, x0, #1")        // x0 = start of value (NUL-terminated)
	g.emit("mov x1, x0")
	g.label(".Lenv_strlen")
	g.emit("ldrb w2, [x1]")
	g.emit("cbz w2, .Lenv_strlen_done")
	g.emit("add x1, x1, #1")
	g.emit("b .Lenv_strlen")
	g.label(".Lenv_strlen_done")
	g.emit("sub x2, x1, x0")        // x2 = value length
	g.emit("mov x19, x0")           // stash value src ptr
	g.emit("mov x20, x2")           // stash value len
	g.emit("add x0, x2, #4")
	g.emit("bl __lang_alloc")
	g.emit("str w20, [x0]")          // length prefix
	g.emit("mov x22, x0")           // stash alloc base
	g.emit("add x0, x0, #4")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __lang_memcpy")
	// Build Option[string]: 8-byte heap [tag=0, value_ptr].
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")          // tag = 0 (Some)
	g.emit("add x1, x22, #4")        // value data ptr
	g.emit("str x1, [x0, #4]")
	g.emit("b .Lenv_done")
	g.label(".Lenv_next")
	g.emit("add x21, x21, #8")
	g.emit("b .Lenv_loop")
	g.label(".Lenv_none")
	// None: heap [tag=1].
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.label(".Lenv_done")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_env")
	g.line(".ltorg")

	// __memcmp_n_env(a, b, n) — returns 0 if first n bytes
	// of `a` equal first n bytes of `b`, non-zero otherwise.
	// `b` is a length-prefixed lang string; `a` is a raw
	// NUL-terminated C string from envp.
	g.line("")
	g.typeDirective("__memcmp_n_env")
	g.label("__memcmp_n_env")
	g.label(".Lmcn_loop")
	g.emit("cbz x2, .Lmcn_eq")
	g.emit("ldrb w3, [x0], #1")
	g.emit("ldrb w4, [x1], #1")
	g.emit("cmp w3, w4")
	g.emit("bne .Lmcn_neq")
	g.emit("sub x2, x2, #1")
	g.emit("b .Lmcn_loop")
	g.label(".Lmcn_eq")
	g.emit("mov x0, #0")
	g.emit("ret")
	g.label(".Lmcn_neq")
	g.emit("mov x0, #1")
	g.emit("ret")
	g.sizeDirective("__memcmp_n_env")
	g.line(".ltorg")
}

// emitTcpListenRuntime emits `__lang_tcp_listen(port)` —
// opens a TCP listening socket on 0.0.0.0:port. Returns the
// listener fd on success, or `-errno` on failure. C-style
// API; callers check `if (fd < 0)`. Mirrors arm32's shape.
//
// Steps: socket(AF_INET, SOCK_STREAM, 0); bind to a stack-
// allocated sockaddr_in; listen with backlog=128.
func (g *generator) emitTcpListenRuntime() {
	g.line("")
	g.line(".global __lang_tcp_listen")
	g.typeDirective("__lang_tcp_listen")
	g.label("__lang_tcp_listen")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("mov x19, x0") // x19 = port (callee-save across calls)
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	g.emit("mov x0, #2")
	g.emit("mov x1, #1")
	g.emit("mov x2, #0")
	g.syscall("socket")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	g.emit("mov x20, x0") // x20 = listener fd
	// Build sockaddr_in on the stack (16 bytes).
	g.emit("sub sp, sp, #16")
	g.emit("mov w0, #2")
	g.emit("strh w0, [sp]")      // sin_family
	g.emit("rev16 w0, w19")      // htons(port)
	g.emit("strh w0, [sp, #2]")
	g.emit("str wzr, [sp, #4]")  // sin_addr = 0
	g.emit("str xzr, [sp, #8]")  // sin_zero[0..7]
	// bind(fd, sa, 16)
	g.emit("mov x0, x20")
	g.emit("mov x1, sp")
	g.emit("mov x2, #16")
	g.syscall("bind")
	g.emit("add sp, sp, #16")    // pop sockaddr_in
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	// listen(fd, 128)
	g.emit("mov x0, x20")
	g.emit("mov x1, #128")
	g.syscall("listen")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	g.emit("mov x0, x20")        // return fd
	g.emit("b .Ltcp_lst_done")
	g.label(".Ltcp_lst_err")
	// x0 holds -errno from the failed syscall.
	g.label(".Ltcp_lst_done")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_listen")
	g.line(".ltorg")
}

// emitTcpAcceptRuntime emits `__lang_tcp_accept(fd)` —
// accepts a connection on the listener fd, returns the new
// connection fd or `-errno`. Passes NULL addr/addrlen
// out-params; callers don't need the peer address.
func (g *generator) emitTcpAcceptRuntime() {
	g.line("")
	g.line(".global __lang_tcp_accept")
	g.typeDirective("__lang_tcp_accept")
	g.label("__lang_tcp_accept")
	// x0 = listener fd (already in x0 from caller).
	g.emit("mov x1, #0") // addr = NULL
	g.emit("mov x2, #0") // addrlen = NULL
	g.syscall("accept")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_accept")
	g.line(".ltorg")
}

// emitTcpRecvRuntime emits `__lang_tcp_recv(fd, max)` —
// reads up to `max` bytes from the socket fd, returns a
// fresh length-prefixed lang string with the bytes read.
// On error or EOF the returned string has length 0.
func (g *generator) emitTcpRecvRuntime() {
	g.line("")
	g.line(".global __lang_tcp_recv")
	g.typeDirective("__lang_tcp_recv")
	g.label("__lang_tcp_recv")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("mov x19, x0") // x19 = fd
	g.emit("mov x20, x1") // x20 = max
	// Allocate `max + 5` bytes (4 prefix + max data + 1 NUL).
	g.emit("add x0, x20, #5")
	g.emit("bl __lang_alloc")
	g.emit("add x21, x0, #4")
	g.emit("str x21, [sp]")      // stash data ptr in a scratch slot
	// read(fd, data, max).
	g.emit("mov x0, x19")
	g.emit("mov x1, x21")
	g.emit("mov x2, x20")
	g.syscall("read")
	// Clamp to ≥ 0 — read returns -errno or 0 on EOF.
	g.emit("cmp x0, #0")
	g.emit("csel x0, x0, xzr, ge")
	g.emit("stur w0, [x21, #-4]") // length prefix
	// Trailing NUL at data + n.
	g.emit("add x1, x21, x0")
	g.emit("strb wzr, [x1]")
	g.emit("mov x0, x21")         // return data ptr
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_recv")
	g.line(".ltorg")
}

// emitTcpSendRuntime emits `__lang_tcp_send(fd, data)` —
// writes the entire string to the fd via `write(2)`. Returns
// the syscall result (bytes written or `-errno`).
func (g *generator) emitTcpSendRuntime() {
	g.line("")
	g.line(".global __lang_tcp_send")
	g.typeDirective("__lang_tcp_send")
	g.label("__lang_tcp_send")
	// x0 = fd, x1 = data ptr.
	g.emit("ldur w2, [x1, #-4]") // x2 = len(data)
	g.syscall("write")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_send")
	g.line(".ltorg")
}

// emitTcpCloseRuntime emits `__lang_tcp_close(fd)` — thin
// wrapper around `close(2)`. Returns 0 or `-errno`.
func (g *generator) emitTcpCloseRuntime() {
	g.line("")
	g.line(".global __lang_tcp_close")
	g.typeDirective("__lang_tcp_close")
	g.label("__lang_tcp_close")
	g.syscall("close")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_close")
	g.line(".ltorg")
}

// emitWriteRuntime emits `__lang_write(s)` — single write(1, s,
// len) syscall, no trailing newline. Length lives at `[s - 4]`;
// no `bl` happens so this stays a leaf function.
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".global __lang_write")
	g.typeDirective("__lang_write")
	g.label("__lang_write")
	g.emit("ldur w2, [x0, #-4]") // x2 = length
	g.emit("mov x1, x0")          // x1 = buf
	g.emit("mov x0, #1")          // x0 = fd (stdout)
	g.syscall("write")
	g.emit("ret")
	g.sizeDirective("__lang_write")
	g.line(".ltorg")
}

// emitPutsRuntime emits `__lang_puts(s)` — write the string,
// then a single trailing newline. Two write(2) calls (vs
// arm32's single writev) keeps the code simple at the cost of
// one extra kernel transition; per-call cost is dominated by
// the syscall itself either way. Preserves x19 across the
// second write so we can return the original data pointer for
// libc-puts consistency.
func (g *generator) emitPutsRuntime() {
	g.line("")
	g.line(".global __lang_puts")
	g.typeDirective("__lang_puts")
	g.label("__lang_puts")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0")         // x19 = data ptr (saved for return)
	g.emit("ldur w2, [x0, #-4]") // x2 = length
	g.emit("mov x1, x0")          // x1 = buf
	g.emit("mov x0, #1")          // x0 = fd
	g.syscall("write")
	g.adrpAdd("x1", ".LLangNewline")
	g.emit("mov x2, #1")
	g.emit("mov x0, #1")
	g.syscall("write")
	g.emit("mov x0, x19")         // return original ptr
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_puts")
	g.line(".ltorg")
}

// emitPutcharRuntime emits `__lang_putchar(c)` — write the
// low byte of x0 to fd 1. We materialise the byte on the
// caller's stack frame so the kernel has a real address to
// read from (the byte itself is a register value).
func (g *generator) emitPutcharRuntime() {
	g.line("")
	g.line(".global __lang_putchar")
	g.typeDirective("__lang_putchar")
	g.label("__lang_putchar")
	g.emit("sub sp, sp, #16")     // 16-byte slot for sp alignment
	g.emit("strb w0, [sp]")        // store byte on the stack
	g.emit("mov x1, sp")           // buf
	g.emit("mov x2, #1")           // len
	g.emit("mov x0, #1")           // fd
	g.syscall("write")
	g.emit("add sp, sp, #16")
	g.emit("ret")
	g.sizeDirective("__lang_putchar")
	g.line(".ltorg")
}

// emitEprintRuntime emits `__lang_eprint(s)` — stderr
// counterpart to __lang_puts. Two write(2)s to fd 2 (string +
// newline). Preserves x19 so we can return the input pointer
// for the consistency `__lang_puts` already offers.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".global __lang_eprint")
	g.typeDirective("__lang_eprint")
	g.label("__lang_eprint")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0")         // x19 = data ptr (saved)
	g.emit("ldur w2, [x0, #-4]") // x2 = length
	g.emit("mov x1, x0")          // x1 = buf
	g.emit("mov x0, #2")          // x0 = fd (stderr)
	g.syscall("write")
	g.adrpAdd("x1", ".LLangNewline")
	g.emit("mov x2, #1")
	g.emit("mov x0, #2")
	g.syscall("write")
	g.emit("mov x0, x19")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_eprint")
	g.line(".ltorg")
}

// emitExitRuntime emits `__lang_exit(code)` — direct exit
// syscall. x0 already holds the user-supplied exit code from
// the caller's argument; syscallExit handles the Linux/Darwin
// ABI split. Never returns, so the trailing `ret` is for
// assembler-completeness only.
func (g *generator) emitExitRuntime() {
	g.line("")
	g.line(".global __lang_exit")
	g.typeDirective("__lang_exit")
	g.label("__lang_exit")
	g.syscallExit()
	g.emit("ret")
	g.sizeDirective("__lang_exit")
	g.line(".ltorg")
}

// emitArgsRuntime emits `__lang_args()` — returns a length-
// prefixed `string[]` materialised from the argc/argv pair
// captured by emitStartRuntime. Each entry is a fresh
// length-prefixed string with a trailing NUL preserved (for
// libc-shaped consumers like `puts`). Result is cached in
// `__lang_args_cache` so repeat calls are O(1).
//
// Slot layout uses callee-save x19..x23 across the inner
// __lang_alloc / __lang_memcpy calls; AAPCS64 mandates
// preservation, so the saved-pair pattern at function entry
// keeps them coherent across the bl chain.
func (g *generator) emitArgsRuntime() {
	g.line("")
	g.line(".global __lang_args")
	g.typeDirective("__lang_args")
	g.label("__lang_args")
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("str x23, [sp, #48]")
	// Fast path: cached pointer non-zero → return it.
	g.adrpAdd("x0", "__lang_args_cache")
	g.emit("ldr x1, [x0]")
	g.emit("cbz x1, .Largs_build")
	g.emit("mov x0, x1")
	g.emit("b .Largs_ret")
	g.label(".Largs_build")
	// x19 = argc, x20 = argv (pointer to char**)
	g.adrpAdd("x19", "__lang_argc")
	g.emit("ldr x19, [x19]")
	g.adrpAdd("x20", "__lang_argv")
	g.emit("ldr x20, [x20]")
	// Allocate the result string[] container: 4 bytes length
	// prefix + argc * 4 bytes for entry pointers. Slots are 4
	// bytes wide because the lang prelude treats array-of-T
	// where T is a pointer as i32-stride; on arm64 this is the
	// same wasm32-inherited assumption that the Map runtime
	// makes. Programs that fit in the low 4 GiB of address
	// space round-trip fine.
	g.emit("lsl x0, x19, #2")
	g.emit("add x0, x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("add x21, x0, #4")     // x21 = result data pointer
	g.emit("stur w19, [x21, #-4]") // length prefix = argc
	// for (i = 0; i < argc; i++)
	g.emit("mov x22, #0") // x22 = i
	g.label(".Largs_loop")
	g.emit("cmp x22, x19")
	g.emit("bge .Largs_done")
	// x23 = argv[i] (C string).
	g.emit("ldr x23, [x20, x22, lsl #3]")
	// Inline strlen on the C string.
	g.emit("mov x0, x23")
	g.label(".Largs_strlen")
	g.emit("ldrb w1, [x0]")
	g.emit("cbz w1, .Largs_strlen_done")
	g.emit("add x0, x0, #1")
	g.emit("b .Largs_strlen")
	g.label(".Largs_strlen_done")
	g.emit("sub x0, x0, x23")     // x0 = strlen
	g.emit("mov x9, x0")          // x9 = saved strlen (caller-save, not preserved across bl)
	// Allocate strlen + 5 (4 prefix + N data + 1 trailing NUL).
	g.emit("add x0, x0, #5")
	g.emit("bl __lang_alloc")
	g.emit("add x10, x0, #4")     // x10 = string data pointer
	g.emit("stur w9, [x10, #-4]") // length prefix
	// Stash data pointer in the reserved scratch slot at
	// `[sp, #56]` (frame is 64 bytes: 16 fp/lr + 16 x19/x20
	// + 16 x21/x22 + 8 x23 + 8 scratch). Caller-save x10 won't
	// survive the bl. Don't use `[sp]` — that overwrites saved
	// x29.
	g.emit("str x10, [sp, #56]")
	// memcpy(x10, x23, strlen + 1) — include NUL.
	g.emit("mov x0, x10")
	g.emit("mov x1, x23")
	g.emit("add x2, x9, #1")
	g.emit("bl __lang_memcpy")
	// result[i] = x10
	g.emit("ldr x10, [sp, #56]")
	g.emit("str w10, [x21, x22, lsl #2]")
	g.emit("add x22, x22, #1")
	g.emit("b .Largs_loop")
	g.label(".Largs_done")
	// Cache + return.
	g.adrpAdd("x0", "__lang_args_cache")
	g.emit("str x21, [x0]")
	g.emit("mov x0, x21")
	g.label(".Largs_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__lang_args")
	g.line(".ltorg")
}

// internString returns a unique .rodata label for s, allocating
// a new one the first time we see this exact string and reusing
// it on repeats.
func (g *generator) internString(s string) string {
	if lbl, ok := g.stringLabel[s]; ok {
		return lbl
	}
	lbl := fmt.Sprintf(".LStr_%d", len(g.stringOrder))
	g.stringLabel[s] = lbl
	g.stringOrder = append(g.stringOrder, s)
	return lbl
}

// escapeForGAS escapes a string for the GAS `.asciz`
// directive. Only the minimum set of escapes the runtime
// strings need; the assembler's own escape map handles `\\` /
// `\n` / `\t` / `\"` etc.
func escapeForGAS(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 32 || c == 127:
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type generator struct {
	out       strings.Builder
	info      *checker.Info
	indent    int
	labelN    int
	current   *ast.FuncDecl
	currentIR *ir.Func
	// darwin enables Mach-O / Apple-style conventions.
	// Currently affects: (1) the program entry point — Apple
	// links against `_main` rather than `_start`, so we emit
	// a `_main` wrapper that calls the user's `main`; (2) the
	// syscall ABI — macOS BSD uses x16 for the syscall number
	// and traps with `svc #0x80` instead of `svc #0`; (3) the
	// syscall numbers themselves (different table from Linux);
	// (4) `.note.GNU-stack` section directive is Linux-only.
	// Local symbols (user's `main`, runtime helpers like
	// `__lang_alloc`) stay with their Linux-style names —
	// they're internal references the assembler resolves
	// locally before the object format matters.
	darwin bool

	// stringLabel / stringOrder mirror the arm32 string-pool
	// scheme: each unique string literal in the program gets
	// a single `.LStr_N` .rodata label with a 4-byte little-
	// endian length prefix followed by `.asciz` data. Programs
	// that reference the same literal multiple times share
	// the entry. Maintained in insertion order so the emitted
	// `.rodata` section is deterministic.
	stringLabel map[string]string
	stringOrder []string

	// usesAlloc / usesStrcat / usesMemcpy track whether the
	// program reaches for the matching runtime helper. Each
	// helper is gated so programs that don't need it pay
	// nothing extra in binary size. The arm32 backend uses
	// the same pattern.
	usesAlloc   bool
	usesStrcat  bool
	usesMemcpy  bool
	usesStrcmp  bool
	// usesTcp pulls in the full TCP socket runtime
	// (__lang_tcp_listen / __lang_tcp_accept / __lang_tcp_recv
	// / __lang_tcp_send / __lang_tcp_close). Gated on call-
	// site reachability so non-server programs don't pay for
	// the socket boilerplate.
	usesTcp bool
	// usesStrSlice pulls in `__str_slice(base, low, high)` —
	// a length-prefix-aware substring extractor that
	// allocates a fresh string. The IR's `s[a:b]` slice
	// expression lowers to OpCallDirect{__str_slice}.
	usesStrSlice bool
	// usesEnv pulls in `__lang_env(name)` — walks envp for a
	// NAME=VALUE match. Used by the synthesised auto-main's
	// `__port_from_env("PORT", 8080)` call.
	usesEnv bool
	// usesAllocU8 + usesStringFromBytes mirror the arm32 flags
	// from PR #230 — string-handling prelude helpers that
	// allocate length-prefixed u8[] / string buffers.
	usesAllocU8         bool
	usesStringFromBytes bool
	// usesRawIntPokes tracks whether the program calls
	// __load_i32 / __store_i32 — primitives the lang Map
	// runtime uses for its mixed bucket-index + entries
	// buffer. Single LDR / STR + ret each.
	usesRawIntPokes bool
	// usesMemset gates emission of the byte-grain
	// __memset(dst, byte, n) helper the Map clear path uses.
	usesMemset bool
	// usesPuts / usesWrite / usesPutchar pull in the stdout
	// builtins:
	//   print(s)   → __lang_puts    (string + newline, two write()s)
	//   write(s)   → __lang_write   (raw string, no newline)
	//   putchar(c) → __lang_putchar (1-byte write)
	// All routed through write(2) — fd 1, syscall numbers from
	// the linuxDarwinSysno map.
	usesPuts    bool
	usesWrite   bool
	usesPutchar bool
	// usesEprint pulls in `__lang_eprint(s)` — stderr counterpart
	// to print(). Two write(2)s to fd 2.
	usesEprint bool
	// usesExit pulls in `__lang_exit(code)` — direct exit syscall.
	// Doesn't return; the post-call push x0 the caller emits is
	// harmless because exit() never comes back.
	usesExit bool
	// usesArgs pulls in `__lang_args()` — materialises a fresh
	// length-prefixed `string[]` from the argc/argv stash the
	// `_start` / `_main` prologue captures off the kernel stack.
	// Result cached via `__lang_args_cache` so repeat calls are
	// O(1).
	usesArgs bool
}

func (g *generator) line(s string) {
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

func (g *generator) emit(format string, args ...any) {
	g.out.WriteByte('\t')
	g.out.WriteString(fmt.Sprintf(format, args...))
	g.out.WriteByte('\n')
}

func (g *generator) label(name string) {
	g.out.WriteString(name)
	g.out.WriteString(":\n")
}

// fresh returns a unique numeric suffix for synthesised
// branch labels (`.Lret_main_3`, etc.). Per-function counter
// reset is handled by emitFunc's prologue.
func (g *generator) fresh() int {
	g.labelN++
	return g.labelN
}

// typeDirective + sizeDirective emit ELF-only `.type FUNC,
// %function` and `.size FUNC, .-FUNC` declarations. Mach-O
// rejects them — the format doesn't carry per-symbol size or
// type metadata (the linker derives both from section
// membership). On Darwin both methods are no-ops.
func (g *generator) typeDirective(name string) {
	if g.darwin {
		return
	}
	g.line(fmt.Sprintf(".type %s, %%function", name))
}

func (g *generator) sizeDirective(name string) {
	if g.darwin {
		return
	}
	g.line(fmt.Sprintf(".size %s, .-%s", name, name))
}

// syscallExit emits `exit_group(retval)` (Linux) / `exit(retval)`
// (Darwin). The exit syscall is what every fatal path in the
// runtime reaches for — OOM, bounds traps, the OS-handoff at
// the end of `_start` / `_main`. x0 already holds the exit
// value; the helper just sets the syscall register and traps.
func (g *generator) syscallExit() {
	if g.darwin {
		g.emit("mov x16, #%d", darExit)
		g.emit("svc #0x80")
	} else {
		g.emit("mov x8, #%d", sysExitGroup)
		g.emit("svc #0")
	}
}

// syscall emits the right `mov`/`svc` pair for `name` and
// normalises the error shape so that callers can rely on
// `x0 < 0 ⇔ error` regardless of target:
//
//   - Linux returns -errno in x0 on error; nothing to do.
//   - Darwin BSD returns +errno in x0 with the carry flag
//     set on error. We negate after the trap when C is set
//     so x0 ends up holding -errno just like Linux.
//
// Args must already be in x0..x5 per AAPCS64; the helper
// only touches x0 (on the Darwin error path) and x8/x16
// (syscall number).
func (g *generator) syscall(name string) {
	nums, ok := linuxDarwinSysno[name]
	if !ok {
		panic("arm64 syscall: unknown name " + name)
	}
	if g.darwin {
		g.emit("mov x16, #%d", nums[1])
		g.emit("svc #0x80")
		// Carry clear = success. Negate on error so callers'
		// `cmp x0, #0; blt` checks see Linux-shaped -errno.
		lbl := g.freshLabel("sysc_ok")
		g.emit("b.cc %s", lbl)
		g.emit("neg x0, x0")
		g.label(lbl)
	} else {
		g.emit("mov x8, #%d", nums[0])
		g.emit("svc #0")
	}
}

// adrpAdd emits the canonical AArch64 PC-relative
// symbol-address pair, paving over the GNU-vs-Apple
// relocation-syntax split:
//
//	ELF (Linux):
//	  adrp Xd, sym
//	  add  Xd, Xd, :lo12:sym
//
//	Mach-O (Darwin):
//	  adrp Xd, sym@PAGE
//	  add  Xd, Xd, sym@PAGEOFF
//
// Both pairs produce the same 64-bit symbol address. Apple's
// integrated assembler rejects the `:lo12:` form; GNU as
// rejects `@PAGE` / `@PAGEOFF`.
func (g *generator) adrpAdd(reg, sym string) {
	if g.darwin {
		g.emit("adrp %s, %s@PAGE", reg, sym)
		g.emit("add %s, %s, %s@PAGEOFF", reg, reg, sym)
	} else {
		g.emit("adrp %s, %s", reg, sym)
		g.emit("add %s, %s, :lo12:%s", reg, reg, sym)
	}
}

// emitStartRuntime writes `_start`, the binary's entry under
// `-nostdlib` on Linux arm64. The kernel hands us a 16-aligned
// sp at process entry; we trust it (no explicit re-alignment
// — `and sp, sp, #imm` is rejected by the assembler since
// AArch64 logical-immediate AND can't take SP as a destination).
// args() / env() aren't wired yet, so the prologue is minimal:
// branch to main, then exit_group with main's return value.
func (g *generator) emitStartRuntime() {
	g.line("")
	// Entry symbol differs by platform. Linux ELF: `_start`
	// (the kernel jumps here directly). Mach-O on Darwin:
	// `_main` — the Apple linker (ld64 / lld's Mach-O backend)
	// defaults to that as the entry. We don't link against
	// libSystem's `start` stub, so we play the role of the
	// crt: capture argv / envp from the kernel-delivered
	// stack, branch to the user's `main`, exit_group.
	entry := "_start"
	if g.darwin {
		entry = "_main"
	}
	g.line(fmt.Sprintf(".global %s", entry))
	if !g.darwin {
		// `.type x, %function` is GAS-specific (ELF). Mach-O
		// assemblers reject it; the Mach-O object format
		// records function-vs-data via the section, not via
		// the symbol's `.type`.
		g.typeDirective(entry)
	}
	g.label(entry)
	if g.usesEnv || g.usesArgs {
		// Capture argc / argv (and derive envp on Linux) from
		// the platform's process-entry convention. The two
		// targets disagree:
		//
		//   Linux: kernel jumps to `_start` with sp pointing
		//   at [argc, argv[0..], NULL, envp[0..], NULL, auxv].
		//   We read argc from sp[0], argv = &sp[1], and walk
		//   past argv + NULL to find envp.
		//
		//   Darwin: with the default LC_MAIN load command, dyld
		//   calls `_main(argc, argv, envp, apple)` per AAPCS64
		//   — argc in x0, argv in x1, envp in x2. The stack
		//   doesn't carry the SVR4-shaped argc-then-argv layout
		//   at all here.
		if g.darwin {
			// x0 / x1 / x2 already hold argc / argv / envp.
			if g.usesEnv {
				g.adrpAdd("x3", "__lang_envp")
				g.emit("str x2, [x3]")
			}
			if g.usesArgs {
				g.adrpAdd("x3", "__lang_argc")
				g.emit("str x0, [x3]")
				g.adrpAdd("x3", "__lang_argv")
				g.emit("str x1, [x3]")
			}
		} else {
			g.emit("ldr x0, [sp]")            // argc
			g.emit("add x1, sp, #8")          // argv = &sp[1]
			if g.usesEnv {
				g.emit("add x2, x0, #1")          // argc + 1
				g.emit("add x2, x1, x2, lsl #3")  // envp = argv + (argc+1)*8
				g.adrpAdd("x3", "__lang_envp")
				g.emit("str x2, [x3]")
			}
			if g.usesArgs {
				g.adrpAdd("x3", "__lang_argc")
				g.emit("str x0, [x3]")
				g.adrpAdd("x3", "__lang_argv")
				g.emit("str x1, [x3]")
			}
		}
	}
	g.emit("bl main")
	g.syscallExit()
	if !g.darwin {
		g.sizeDirective(entry)
	}
}

// emitFunc lowers one IR function. Stack frame layout
// (canonical AAPCS64 leaf-function shape):
//
//	[caller's sp]                     ← old sp (before prologue)
//	[caller's sp - 8]   saved x30 (lr)
//	[caller's sp - 16]  saved x29 (fp), x29 points HERE after `mov`
//	[caller's sp - 16 - 8*1]   slot 0
//	[caller's sp - 16 - 8*2]   slot 1
//	...
//	[caller's sp - frameSize]  ← new sp (locals + alignment pad)
//
// Slot 0..N-1 indexes match the IR's local indexing: param i
// occupies slot i (parameters arrive in x0..x7 and are stored
// to their slots at function entry); locals beyond the param
// count occupy slots N+0, N+1, ... All slots are 8 bytes
// (i32 values waste 4 bytes per slot — future PR can pack
// i32s back-to-back).
//
// Stack-relative addressing uses sp (locals at `[sp, #i*8]`)
// rather than fp because sp doesn't move during a function
// body (the operand-stack pushes / pops happen on a separate
// scratch region above sp via `[sp, #-16]!` pre-decrement —
// no wait, that does move sp). Reconciling both: locals are
// at fixed offsets from x29 (the frame pointer), which the
// prologue pinned to the saved-pair address; that stays
// stable across operand-stack pushes/pops since we never
// touch x29 between prologue and epilogue.
//
// We reserve a fixed slot count per function — the IR doesn't
// expose a "max locals" field directly, so we walk irFn.Ops
// and find the highest-numbered slot referenced.
func (g *generator) emitFunc(fn *ast.FuncDecl, irFn *ir.Func) error {
	g.current = fn
	g.currentIR = irFn
	defer func() { g.current = nil; g.currentIR = nil }()

	maxSlot := int32(-1)
	for _, op := range irFn.Ops {
		switch op.Kind {
		case ir.OpLoadLocal, ir.OpStoreLocal, ir.OpTeeLocal:
			if op.I32 > maxSlot {
				maxSlot = op.I32
			}
		}
	}
	for i := range fn.Params {
		if int32(i) > maxSlot {
			maxSlot = int32(i)
		}
	}
	numSlots := int(maxSlot + 1)
	// localsSize = numSlots * 8 rounded up to 16-byte alignment
	// so the post-allocate sp stays aligned for sp-relative
	// memory accesses. Saved fp/lr is a separate 16 above the
	// locals region.
	localsSize := numSlots * 8
	if localsSize%16 != 0 {
		localsSize += 8
	}
	frameSize := 16 + localsSize

	g.line("")
	g.line(fmt.Sprintf(".global %s", fn.Name))
	g.typeDirective(fn.Name)
	g.label(fn.Name)
	// Prologue:
	//   stp x29, x30, [sp, #-16]!  ; save fp/lr, sp -= 16
	//   mov x29, sp                ; fp = sp (points at saved pair)
	//   sub sp, sp, #localsSize    ; allocate locals below the pair
	// Slot i lives at [x29, #-(i+1)*8] OR equivalently [sp,
	// #localsSize - (i+1)*8]. We use the [sp, #N] form so all
	// local accesses are positive offsets — easier to reason
	// about and avoids the negative-offset encoding edge cases.
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	if localsSize > 0 {
		g.emit("sub sp, sp, #%d", localsSize)
	}
	// Spill parameter registers x0..x{n-1} into their slots.
	// Args beyond regArgs are already on the caller's stack;
	// we don't yet handle that case. Slot i is anchored at
	// `[x29, #-(i+1)*8]` — x29 stays fixed across the function
	// body so operand-stack pushes/pops on sp don't shift the
	// slot addresses.
	for i := range fn.Params {
		if i >= regArgs {
			return fmt.Errorf("arm64: more than %d function parameters not yet supported (got %d)", regArgs, len(fn.Params))
		}
		g.emit("stur x%d, [x29, #%d]", i, -(i+1)*8)
	}
	_ = frameSize // reserved for the eventual debug-info / unwind tables

	retLabel := fmt.Sprintf(".Lret_%s_%d", fn.Name, g.fresh())
	var scope []irScope
	for _, op := range irFn.Ops {
		if err := g.emitOp(op, frameSize, retLabel, &scope); err != nil {
			return err
		}
	}

	// Epilogue. OpReturn already emits `b retLabel`; if the
	// function falls off the end without an explicit return
	// (e.g. void functions), we land here naturally.
	//
	// `mov sp, x29` restores sp to the saved-pair address
	// regardless of how the operand stack ended up — robust
	// to void-call leaks where OpCallDirect always pushes
	// x0 even when the function returns nothing. Without
	// the fp-based unwind, leaked operand-stack pushes leave
	// sp below where the prologue put it, and the `ldp`
	// loads garbage as fp/lr → ret to a bad address →
	// SEGV. arm32 uses the equivalent `mov sp, fp` for the
	// same reason; we mirror it here.
	g.label(retLabel)
	g.emit("mov sp, x29")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective(fn.Name)
	g.line(".ltorg")
	return nil
}

// push x0 — store r0 to the top of the operand stack and bump
// sp down by 16 bytes (the 16-byte alignment dance — single-
// value slots use 16 bytes with the upper 8 wasted).
func (g *generator) push() {
	g.emit("str x0, [sp, #-16]!")
}

// pop into x0.
func (g *generator) pop() {
	g.emit("ldr x0, [sp], #16")
}

// binPop — pop two values off the operand stack into x1 (lhs)
// and x0 (rhs). Mirrors arm32's binPop. Produces the
// natural form for non-commutative ops where the lhs ends up
// in the second source register.
func (g *generator) binPop() {
	g.emit("ldr x0, [sp], #16") // rhs (top of stack)
	g.emit("ldr x1, [sp], #16") // lhs (next)
}

// fbinPop32 pops two raw 32-bit float bit-patterns off the
// operand stack and loads them into s0 (rhs) and s1 (lhs)
// via fmov. Mirrors arm32's fbinPop. The bit patterns are
// stored as i32 on the operand stack to keep the
// push/pop discipline uniform across i32 / f32 / i64 / f64;
// the V-register file gets involved only at op time.
func (g *generator) fbinPop32() {
	g.emit("ldr x0, [sp], #16") // rhs raw bits
	g.emit("ldr x1, [sp], #16") // lhs raw bits
	g.emit("fmov s0, w0")
	g.emit("fmov s1, w1")
}

// fbinPop64 is fbinPop32's f64 counterpart — uses the full
// 64-bit x-regs and double-precision d-regs.
func (g *generator) fbinPop64() {
	g.emit("ldr x0, [sp], #16")
	g.emit("ldr x1, [sp], #16")
	g.emit("fmov d0, x0")
	g.emit("fmov d1, x1")
}

// fcmpPop pops two floats, runs `fcmp` and `cset` to
// normalise the flag-state to 0 / 1, then pushes the i32
// result. The condition code chooses between the comparison
// shapes (eq / ne / mi / ls / gt / ge — same set arm32 uses).
func (g *generator) fcmpPop(width int, cc string) {
	if width == 64 {
		g.fbinPop64()
		g.emit("fcmp d1, d0")
	} else {
		g.fbinPop32()
		g.emit("fcmp s1, s0")
	}
	g.emit("cset w0, %s", cc)
	g.push()
}

// irScope tracks one open OpBlock / OpLoop / OpIf scope. The
// IR's `br` instruction targets a scope by relative depth from
// the top, and the destination label depends on the scope kind:
//   - OpBlock: br jumps to the end of the block (forward).
//   - OpLoop:  br jumps to the start of the loop (backward).
//   - OpIf:    br jumps to the end of the if/else.
// `endLabel` is always the post-scope label; `brTarget` is what
// `br N` resolves to. For if-without-else we lazily define the
// elseLabel at the end so the OpIf's `cbz` fall-through skips
// the body.
type irScope struct {
	kind      ir.OpKind
	brTarget  string
	endLabel  string
	elseLabel string
	hasElse   bool
}

func (g *generator) freshLabel(prefix string) string {
	g.labelN++
	return fmt.Sprintf(".L%s_%d", prefix, g.labelN)
}

// emitOp dispatches a single IR op to its arm64 lowering.
// Each op consumes / produces operand-stack values via
// push() / pop(). Unsupported ops surface explicit errors so
// missing pieces are obvious; the per-op switch grows toward
// arm32 parity over follow-up PRs.
//
// The `scope` slice tracks open OpBlock / OpLoop / OpIf scopes
// for `br` / `br_if` / `else` / `end` resolution. We pass it
// by pointer so OpBlock etc. can append; the caller (emitFunc)
// owns the slice.
func (g *generator) emitOp(op ir.Op, frameSize int, retLabel string, scope *[]irScope) error {
	switch op.Kind {
	case ir.OpConstI32:
		// Materialise the constant into x0 and push. Small
		// values fit `mov` (imm16); larger ones use `ldr =N`
		// (assembler pseudo-instruction backed by a literal
		// pool). i32s use the w0 alias when narrowing matters,
		// but for the 64-bit operand stack we keep them in x0.
		v := op.I32
		if v >= 0 && v <= 0xffff {
			g.emit("mov x0, #%d", v)
		} else {
			g.emit("ldr w0, =%d", v)
		}
		g.push()

	case ir.OpConstStr:
		// String literals live in .rodata with a 4-byte length
		// prefix at `[label - 4]`; the .LStr_N label points at
		// the .asciz data so the runtime carries data pointers
		// (post-prefix). Pointer materialised via the
		// `adrp` + `add :lo12:` pair — the canonical AArch64
		// PC-relative addressing for absolute symbol values.
		lbl := g.internString(op.Str)
		g.adrpAdd("x0", lbl)
		g.push()

	case ir.OpConstFunc:
		// Function values materialise as the direct code
		// address of the named function. AArch64 has no
		// funcref table abstraction (unlike wasm); the
		// assembler resolves `=name` into a literal-pool entry
		// holding the symbol's absolute address. OpCallIndirect
		// then `blr` to it.
		g.adrpAdd("x0", op.Str)
		g.push()

	case ir.OpReturn:
		g.pop()
		g.emit("b %s", retLabel)
	case ir.OpReturnVoid:
		// Void return: no value to pop. The epilogue at
		// retLabel restores the frame and rets.
		g.emit("b %s", retLabel)

	case ir.OpDrop:
		g.emit("add sp, sp, #16")

	// -------- arithmetic --------
	//
	// Use 64-bit register form (x0/x1) for all arithmetic so
	// pointer-shaped values (full 64-bit on aarch64) survive
	// `add` / `sub` / etc. unmangled. The IR's `len(s)` for
	// example does `ptr - 4` via OpSub; with the 32-bit form
	// (`sub w0, w1, w0`) the result would zero-extend, dropping
	// the high 32 bits of the pointer and faulting on the
	// subsequent load.
	//
	// i32 wraparound semantics are technically slightly off
	// (high bits aren't masked between ops), but the language's
	// integer overflow rules don't require modular semantics —
	// the wasm backend uses i32 ops directly which already
	// zero-extend, while consumers that read i32 explicitly
	// (str w0 / ldr w0 / cbz w0) only see the low 32 bits.

	case ir.OpAdd:
		g.binPop()
		g.emit("add x0, x1, x0")
		g.push()
	case ir.OpSub:
		g.binPop()
		g.emit("sub x0, x1, x0")
		g.push()
	case ir.OpMul:
		g.binPop()
		g.emit("mul x0, x1, x0")
		g.push()
	case ir.OpDivS:
		// Signed division. ARMv8-A includes sdiv as a base-ISA
		// instruction (no divider extension required like on
		// armv7-a). Uses w-form so the result is treated as
		// 32-bit signed for the divide; downstream consumers
		// that need 64-bit pointer arithmetic don't go through
		// OpDivS.
		g.binPop()
		g.emit("sdiv w0, w1, w0")
		g.push()
	case ir.OpRemS:
		g.binPop()
		g.emit("sdiv w2, w1, w0")
		g.emit("msub w0, w2, w0, w1")
		g.push()
	case ir.OpAnd:
		g.binPop()
		g.emit("and x0, x1, x0")
		g.push()
	case ir.OpOr:
		g.binPop()
		g.emit("orr x0, x1, x0")
		g.push()
	case ir.OpXor:
		g.binPop()
		g.emit("eor x0, x1, x0")
		g.push()
	case ir.OpShl:
		g.binPop()
		g.emit("lsl x0, x1, x0")
		g.push()
	case ir.OpShrS:
		g.binPop()
		g.emit("asr x0, x1, x0")
		g.push()

	// -------- comparison (i32) --------
	//
	// AArch64 doesn't have flag-producing-as-result
	// instructions; the canonical idiom is `cmp` followed by
	// `cset Wd, <cond>` which writes 0 / 1 based on the flag.

	case ir.OpEq:
		g.binPop()
		g.emit("cmp w1, w0")
		g.emit("cset w0, eq")
		g.push()
	case ir.OpNe:
		g.binPop()
		g.emit("cmp w1, w0")
		g.emit("cset w0, ne")
		g.push()
	case ir.OpLtS:
		g.binPop()
		g.emit("cmp w1, w0")
		g.emit("cset w0, lt")
		g.push()
	case ir.OpLeS:
		g.binPop()
		g.emit("cmp w1, w0")
		g.emit("cset w0, le")
		g.push()
	case ir.OpGtS:
		g.binPop()
		g.emit("cmp w1, w0")
		g.emit("cset w0, gt")
		g.push()
	case ir.OpGeS:
		g.binPop()
		g.emit("cmp w1, w0")
		g.emit("cset w0, ge")
		g.push()

	// -------- logical / unary --------

	case ir.OpNot:
		// Boolean not: 0 → 1, non-zero → 0. `cmp / cset eq`
		// compares with 0 in xzr.
		g.pop()
		g.emit("cmp w0, #0")
		g.emit("cset w0, eq")
		g.push()

	// -------- locals --------

	case ir.OpLoadLocal:
		// Slot i sits at [x29, #-(i+1)*8] — see the prologue
		// comment in emitFunc. `ldur` is the unscaled
		// load/store form; the immediate range is ±256 bytes.
		// Programs needing more than 32 locals (offset > 256)
		// would need a longer addressing form — tracked as a
		// future PR.
		g.emit("ldur x0, [x29, #%d]", -(op.I32+1)*8)
		g.push()
	case ir.OpStoreLocal:
		g.pop()
		g.emit("stur x0, [x29, #%d]", -(op.I32+1)*8)
	case ir.OpTeeLocal:
		// Pop, store, push back so the value stays on the
		// operand stack. arm64 has `ldr/str` post-increment but
		// no fused tee — issue the pop / str / push sequence.
		g.pop()
		g.emit("stur x0, [x29, #%d]", -(op.I32+1)*8)
		g.push()

	// -------- direct calls --------

	// -------- control flow --------

	case ir.OpBlock:
		endL := g.freshLabel("blkEnd")
		*scope = append(*scope, irScope{kind: ir.OpBlock, brTarget: endL, endLabel: endL})
	case ir.OpLoop:
		startL := g.freshLabel("loopTop")
		endL := g.freshLabel("loopEnd")
		g.label(startL)
		*scope = append(*scope, irScope{kind: ir.OpLoop, brTarget: startL, endLabel: endL})
	case ir.OpIf:
		// `cbz w0, elseL` branches when w0 == 0 — the
		// arm64 fast-path equivalent of arm32's `cmp / beq`.
		// Tests only the low 32 bits because i32 truthiness
		// is i32-shaped; using `cbz x0` would also test high
		// bits which the 64-bit-arithmetic mode (see comments
		// on OpAdd) deliberately leaves dirty.
		g.pop()
		elseL := g.freshLabel("ifElse")
		endL := g.freshLabel("ifEnd")
		g.emit("cbz w0, %s", elseL)
		*scope = append(*scope, irScope{kind: ir.OpIf, brTarget: endL, endLabel: endL, elseLabel: elseL})
	case ir.OpElse:
		top := &(*scope)[len(*scope)-1]
		top.hasElse = true
		g.emit("b %s", top.endLabel)
		g.label(top.elseLabel)
	case ir.OpEnd:
		top := (*scope)[len(*scope)-1]
		*scope = (*scope)[:len(*scope)-1]
		if top.kind == ir.OpIf && !top.hasElse {
			// No OpElse appeared; the OpIf's cbz was wired to
			// elseLabel, which we now define as the end so the
			// "condition false" branch simply skips the if body.
			g.label(top.elseLabel)
		}
		g.label(top.endLabel)
	case ir.OpBr:
		target := (*scope)[len(*scope)-1-int(op.I32)].brTarget
		g.emit("b %s", target)
	case ir.OpBrIf:
		g.pop()
		target := (*scope)[len(*scope)-1-int(op.I32)].brTarget
		// w0 form for the same reason as OpIf — only test the
		// low 32 bits since i32 truthiness is i32-shaped.
		g.emit("cbnz w0, %s", target)

	// -------- memory load / store --------

	case ir.OpLoad:
		// Pop addr; load 4 bytes (i32) or 8 bytes (i64) from
		// it. The IR distinguishes width via op.Width: 0 / 32
		// → 4-byte, 64 → 8-byte. The i32 path uses the w0
		// alias so the high half of x0 zero-extends cleanly.
		g.pop()
		if op.Width == 64 {
			g.emit("ldr x0, [x0]")
		} else {
			g.emit("ldr w0, [x0]")
		}
		g.push()
	case ir.OpLoadByte:
		g.pop()
		g.emit("ldrb w0, [x0]")
		g.push()
	case ir.OpStore:
		// Stack: [addr, value], top = value.
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		if op.Width == 64 {
			g.emit("str x0, [x1]")
		} else {
			g.emit("str w0, [x1]")
		}
	case ir.OpStoreI8:
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		g.emit("strb w0, [x1]")
	case ir.OpStoreI16:
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		g.emit("strh w0, [x1]")

	case ir.OpAlloc:
		g.usesAlloc = true
		g.pop()
		g.emit("bl __lang_alloc")
		g.push()

	case ir.OpStrEq:
		// Strings carry a trailing NUL alongside the length
		// prefix; equality returns 0 / 1 from __lang_strcmp.
		// Same shape as arm32.
		g.usesStrcmp = true
		g.emit("ldr x1, [sp], #16")
		g.emit("ldr x0, [sp], #16")
		g.emit("bl __lang_strcmp")
		g.emit("cmp x0, #0")
		g.emit("cset w0, eq")
		g.push()

	// -------- floats (f32 / f64) --------
	//
	// Float values live as raw bit patterns on the operand
	// stack (same shape as arm32 — i32 / i64 / f32 / f64 all
	// occupy 16-byte stack slots regardless of underlying
	// type). For arithmetic + comparison the codegen moves
	// the bit pattern into the V-register file (s0/s1 for
	// single-precision, d0/d1 for double-precision), runs the
	// op, and `fmov`s the result back. AArch64 has direct
	// `fmov` between x-regs and v-regs so this is a one-cycle
	// shuffle on most cores.

	case ir.OpConstF32:
		// Materialise the f32 bit pattern as an i32 literal —
		// same trick as arm32. The bit pattern bypasses the
		// V-register file entirely, going straight onto the
		// operand stack as a 32-bit raw value.
		bits := math.Float32bits(op.F32)
		g.emit("mov x0, #%d", int64(bits))
		g.push()
	case ir.OpConstF64:
		bits := math.Float64bits(op.F64)
		g.emit("ldr x0, =%d", int64(bits))
		g.push()

	case ir.OpFAdd:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fadd d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fadd s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFSub:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fsub d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fsub s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFMul:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fmul d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fmul s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFDiv:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fdiv d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fdiv s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFNeg:
		if op.Width == 64 {
			g.pop()
			g.emit("fmov d0, x0")
			g.emit("fneg d0, d0")
			g.emit("fmov x0, d0")
		} else {
			g.pop()
			g.emit("fmov s0, w0")
			g.emit("fneg s0, s0")
			g.emit("fmov w0, s0")
		}
		g.push()

	case ir.OpFEq:
		g.fcmpPop(op.Width, "eq")
	case ir.OpFNe:
		g.fcmpPop(op.Width, "ne")
	case ir.OpFLt:
		g.fcmpPop(op.Width, "mi")
	case ir.OpFLe:
		g.fcmpPop(op.Width, "ls")
	case ir.OpFGt:
		g.fcmpPop(op.Width, "gt")
	case ir.OpFGe:
		g.fcmpPop(op.Width, "ge")

	case ir.OpFLoad:
		// Float values live as raw bit patterns; OpFLoad reads
		// 4 / 8 bytes from memory directly into x0. The V-
		// register file isn't involved on the read path.
		g.pop()
		if op.Width == 64 {
			g.emit("ldr x0, [x0]")
		} else {
			g.emit("ldr w0, [x0]")
		}
		g.push()
	case ir.OpFStore:
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		if op.Width == 64 {
			g.emit("str x0, [x1]")
		} else {
			g.emit("str w0, [x1]")
		}

	case ir.OpFPromoteF32:
		// f32 → f64. Move into s0, promote, move back as x0.
		g.pop()
		g.emit("fmov s0, w0")
		g.emit("fcvt d0, s0")
		g.emit("fmov x0, d0")
		g.push()
	case ir.OpFDemoteF64:
		// f64 → f32. Inverse: move into d0, demote, move back.
		g.pop()
		g.emit("fmov d0, x0")
		g.emit("fcvt s0, d0")
		g.emit("fmov w0, s0")
		g.push()

	case ir.OpFConvertI32:
		// i32 → f32 / f64. scvtf signed / ucvtf unsigned.
		g.pop()
		if op.Width == 64 {
			if op.Unsigned {
				g.emit("ucvtf d0, w0")
			} else {
				g.emit("scvtf d0, w0")
			}
			g.emit("fmov x0, d0")
		} else {
			if op.Unsigned {
				g.emit("ucvtf s0, w0")
			} else {
				g.emit("scvtf s0, w0")
			}
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpITruncF32:
		// f32 → i32 / i64. fcvtzs truncates toward zero.
		g.pop()
		g.emit("fmov s0, w0")
		if op.Width == 64 {
			g.emit("fcvtzs x0, s0")
		} else {
			g.emit("fcvtzs w0, s0")
		}
		g.push()
	case ir.OpITruncF64:
		g.pop()
		g.emit("fmov d0, x0")
		if op.Width == 64 {
			g.emit("fcvtzs x0, d0")
		} else {
			g.emit("fcvtzs w0, d0")
		}
		g.push()

	case ir.OpStrConcat:
		// The IR's `+` between strings lowers directly to
		// OpStrConcat (rather than going through OpCallDirect
		// to "__lang_strcat") so codegen owns the dispatch and
		// can target-specialise. Stack: [a, b], top = b. Pop
		// into x1 / x0 to match the `__lang_strcat(a, b)`
		// signature.
		g.usesStrcat = true
		g.usesAlloc = true
		g.usesMemcpy = true
		g.emit("ldr x1, [sp], #16") // b
		g.emit("ldr x0, [sp], #16") // a
		g.emit("bl __lang_strcat")
		g.push()

	case ir.OpCallIndirect:
		// Function-value call: the IR emitted the function-
		// pointer immediately before the call op (via
		// OpLoadLocal / OpConstFunc), so the pointer is on top
		// of the stack and args are below it in left-to-right
		// order. arm64 uses `blr x16` (branch-with-link
		// register) — x16 is a caller-save scratch.
		argc := int(op.I32)
		if argc > regArgs {
			return fmt.Errorf("arm64: more than %d call args not yet supported (got %d for OpCallIndirect)", regArgs, argc)
		}
		g.emit("ldr x16, [sp], #16") // x16 = function pointer
		for i := argc - 1; i >= 0; i-- {
			g.emit("ldr x%d, [sp], #16", i)
		}
		g.emit("blr x16")
		g.push()

	case ir.OpCallDirect:
		// AAPCS64: load args 0..n-1 from the operand stack into
		// x0..x{n-1} (rightmost-on-top, so we pop in reverse
		// order), then `bl target`. Result lands in x0; push it.
		// Rewrite a small set of names that the lang prelude
		// uses but arm32 ships under different symbol names —
		// same approach as arm32_ops.go for `__memcpy` →
		// `__lang_memcpy` etc.
		target := op.Str
		switch target {
		case "__memcpy":
			target = "__lang_memcpy"
			g.usesMemcpy = true
		case "__lang_strcat":
			g.usesStrcat = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__alloc":
			target = "__lang_alloc"
			g.usesAlloc = true
		case "__store_i32", "__load_i32", "__store_ptr", "__load_ptr":
			g.usesRawIntPokes = true
		case "__memset":
			g.usesMemset = true
		case "__alloc_u8":
			g.usesAllocU8 = true
			g.usesAlloc = true
		case "string_from_bytes":
			g.usesStringFromBytes = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "tcp_listen":
			target = "__lang_tcp_listen"
			g.usesTcp = true
			g.usesAlloc = true
		case "tcp_accept":
			target = "__lang_tcp_accept"
			g.usesTcp = true
		case "tcp_recv":
			target = "__lang_tcp_recv"
			g.usesTcp = true
			g.usesAlloc = true
		case "tcp_send":
			target = "__lang_tcp_send"
			g.usesTcp = true
		case "tcp_close":
			target = "__lang_tcp_close"
			g.usesTcp = true
		// Map / MapIter — same alias rewrites as arm32_ops.go
		// (see PR #234). The lang Map runtime lives entirely
		// in the lang prelude under `_impl`-suffixed names;
		// user-facing call sites use the unsuffixed mangled
		// name and codegen rewrites here.
		case "map_new":
			target = "map_new_impl"
		case "__method_Map_len":
			target = "__map_len_impl"
		case "__method_Map_has":
			target = "__map_has_impl"
		case "__method_Map_get":
			target = "__map_get_impl"
		case "__method_Map_get_or":
			target = "__map_get_or_impl"
		case "__method_Map_set":
			target = "__map_set_impl"
		case "__method_Map_delete":
			target = "__map_delete_impl"
		case "__method_Map_clear":
			target = "__map_clear_impl"
		case "__method_Map_keys":
			target = "__map_keys_impl"
		case "__method_Map_values":
			target = "__map_values_impl"
		case "__method_Map_iter":
			target = "__map_iter_impl"
		case "__method_MapIter_has_next":
			target = "__mapiter_has_next_impl"
		case "__method_MapIter_key":
			target = "__mapiter_key_impl"
		case "__method_MapIter_value":
			target = "__mapiter_value_impl"
		case "__method_MapIter_advance":
			target = "__mapiter_advance_impl"
		case "__str_idx", "__arr_idx", "__slice_idx",
			"__slice_idx_1", "__slice_idx_2", "__slice_idx_8":
			// IR-side bounds-check stubs the lang runtime
			// would otherwise dispatch to. arm64 doesn't yet
			// ship the helpers, so inline an unchecked
			// address compute that matches the wasm
			// behaviour for in-range indices. The element-
			// stride is encoded in the helper name.
			return g.emitInlineIdxHelper(target)
		case "__str_slice":
			// String slice — allocates a fresh substring.
			// Real runtime helper, not inlined.
			g.usesStrSlice = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "env":
			target = "__lang_env"
			g.usesEnv = true
			g.usesAlloc = true
		case "print":
			// print(s): write string + newline. Same name
			// rewrite arm32 does; the runtime helper handles
			// both writes.
			target = "__lang_puts"
			g.usesPuts = true
		case "write":
			// write(s): write string, no newline.
			target = "__lang_write"
			g.usesWrite = true
		case "putchar":
			// putchar(c): write the single byte.
			target = "__lang_putchar"
			g.usesPutchar = true
		case "eprint":
			// eprint(s): print to stderr (fd 2) + newline.
			target = "__lang_eprint"
			g.usesEprint = true
		case "exit":
			// exit(code): direct exit syscall. Never returns,
			// but codegen still emits the post-call stack-
			// push for stack-discipline; harmless because the
			// call never comes back.
			target = "__lang_exit"
			g.usesExit = true
		case "args":
			// args(): returns a length-prefixed string[] of
			// argv. Caches the result so repeat calls are O(1).
			target = "__lang_args"
			g.usesArgs = true
			g.usesAlloc = true
			g.usesMemcpy = true
		}
		argc := int(op.I32)
		if argc > regArgs {
			return fmt.Errorf("arm64: more than %d call args not yet supported (got %d for %q)", regArgs, argc, op.Str)
		}
		for i := argc - 1; i >= 0; i-- {
			g.emit("ldr x%d, [sp], #16", i)
		}
		g.emit("bl %s", target)
		g.push()

	default:
		return fmt.Errorf("arm64: unsupported IR op %s", op.Kind)
	}
	return nil
}
