// Direct-syscall runtime for the ARM32 backend. We build with
// `gcc -nostdlib`, so the binary contains no libc — every I/O
// operation goes straight to the kernel via `svc 0`, the heap is
// a brk-extended bump arena, and string / memory primitives are
// handed-rolled. The payoff is a smaller, faster-startup binary
// that matches the language's "small CLI / short-lived edge
// function" use case.
//
// All helpers in this file follow the standard ARM EABI
// convention: args in r0..r3, return in r0, callees preserve
// r4..r11 + lr. Helpers don't allocate stack frames unless they
// need to (most are leaf).

package codegen

// ARM EABI Linux syscall numbers used by the no-libc backend.
// See <asm-generic/unistd.h> + the arm syscall table for the
// full set; we only pull in the handful the language needs.
const (
	sysExit      = 1
	sysRead      = 3
	sysWrite     = 4
	sysOpen      = 5
	sysClose     = 6
	sysLseek     = 19
	sysBrk       = 45
	sysWritev    = 146
	sysExitGroup = 248
)

// emitSyscall lowers a save-restore-bracketed `svc 0`. The
// kernel returns the result (or `-errno` on failure) in r0;
// every other register (including r7) is preserved across the
// `svc` itself, but loading the syscall number into r7
// destroys whatever the caller had there. Since r7 sits in the
// AAPCS callee-saved range (r4..r11), helpers that issue
// syscalls would otherwise be silently violating the
// convention — strcat / read_file / etc. routinely stash live
// values in r7 across `bl __lang_alloc`. Bracketing with
// `push {r7}` / `pop {r7}` makes every syscall transparent
// to its caller; the cost is two extra memory ops, dwarfed by
// the kernel transition itself.
func (g *generator) emitSyscall(num int) {
	g.emit("push {r7}")
	g.emit("mov r7, #%d", num)
	g.emit("svc 0")
	g.emit("pop {r7}")
}

// emitStartRuntime emits `_start`, the binary's entry point under
// `-nostdlib`. Linux ARM32 hands us the initial stack as:
//
//	sp[0]   = argc
//	sp[1..] = argv[0..argc-1], NULL, envp[0..], NULL, auxv...
//
// We capture argc / argv / envp into .bss globals so `args()` and
// `env()` can find them later, initialise the bump heap with one
// `brk(0)` syscall, hand control to the user's `main`, and end
// the process with `exit_group(retval)`.
func (g *generator) emitStartRuntime() {
	g.line("")
	g.line(".global _start")
	g.line(".type _start, %function")
	g.label("_start")
	// argc, argv, envp.
	g.emit("ldr r0, [sp]")           // argc
	g.emit("add r1, sp, #4")         // argv = &sp[1]
	g.emit("add r2, r0, #1")         // argc + 1
	g.emit("add r2, r1, r2, lsl #2") // envp = argv + (argc+1)*4
	g.emit("ldr r3, =__lang_argc")
	g.emit("str r0, [r3]")
	g.emit("ldr r3, =__lang_argv")
	g.emit("str r1, [r3]")
	g.emit("ldr r3, =__lang_envp")
	g.emit("str r2, [r3]")
	// AAPCS expects sp to be 8-byte aligned at function call
	// boundaries. The kernel hands us a word-aligned sp at
	// process entry, so we round it down before our first
	// `bl`. Done after the argc/argv/envp captures so the
	// reads don't depend on the round.
	g.emit("bic sp, sp, #7")
	// Init the bump heap.
	g.emit("bl __lang_heap_init")
	// Hand control to user's main(argc, argv).
	g.emit("ldr r0, =__lang_argc")
	g.emit("ldr r0, [r0]")
	g.emit("ldr r1, =__lang_argv")
	g.emit("ldr r1, [r1]")
	g.emit("bl main")
	// exit_group(retval). No return.
	g.emitSyscall(sysExitGroup)
	g.line(".size _start, .-_start")
}

// emitHeapInitRuntime captures the kernel-provided initial program
// break (via `brk(0)`) into both __lang_heap_ptr (bump cursor) and
// __lang_heap_end (current limit). Subsequent allocations bump the
// pointer; when it would cross the end, the alloc helper extends
// the break in 64 KiB increments to amortise the syscall.
func (g *generator) emitHeapInitRuntime() {
	g.line("")
	g.line(".type __lang_heap_init, %function")
	g.label("__lang_heap_init")
	g.emit("push {lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment for the syscall
	g.emit("mov r0, #0")
	g.emitSyscall(sysBrk)
	g.emit("ldr r1, =__lang_heap_ptr")
	g.emit("str r0, [r1]")
	g.emit("ldr r1, =__lang_heap_end")
	g.emit("str r0, [r1]")
	g.emit("add sp, sp, #4")
	g.emit("pop {lr}")
	g.emit("bx lr")
	g.line(".size __lang_heap_init, .-__lang_heap_init")
}

// emitMemcpyRuntime emits a word-grain memcpy: copy four bytes
// at a time on the bulk path, then a byte-grain tail for the
// last <4 bytes. ARMv7-A allows unaligned word access by
// default (Linux user-space sets SCTLR.A=0), so we don't need
// to align the pointers first.
//
// The bulk loop is three instructions per iteration (load,
// store, dec, branch) covering four bytes — 4× faster than the
// byte-grain version it replaces. Every alloc-and-copy path in
// the runtime (strcat, args, env, read_file, the streaming
// Reader) sees the speedup directly.
//
// Clobbers r0..r3 and r12; r4..r11 stay untouched so callers
// can keep state across the call.
func (g *generator) emitMemcpyRuntime() {
	g.line("")
	g.line(".type __lang_memcpy, %function")
	g.label("__lang_memcpy")
	g.emit("mov r3, r0") // r3 = dst (saved for return)
	g.label(".Lmcp_word")
	g.emit("cmp r2, #4")
	g.emit("blt .Lmcp_tail")
	g.emit("ldr r12, [r1], #4")
	g.emit("str r12, [r0], #4")
	g.emit("sub r2, r2, #4")
	g.emit("b .Lmcp_word")
	g.label(".Lmcp_tail")
	g.emit("cmp r2, #0")
	g.emit("beq .Lmcp_done")
	g.emit("ldrb r12, [r1], #1")
	g.emit("strb r12, [r0], #1")
	g.emit("sub r2, r2, #1")
	g.emit("b .Lmcp_tail")
	g.label(".Lmcp_done")
	g.emit("mov r0, r3")
	g.emit("bx lr")
	g.line(".size __lang_memcpy, .-__lang_memcpy")
}

// emitStrlenRuntime walks a NUL-terminated C string and returns
// its length in r0. Used only by the env() / args() helpers when
// they're copying kernel-provided strings into lang strings —
// length-prefixed lang strings already know their length.
func (g *generator) emitStrlenRuntime() {
	g.line("")
	g.line(".type __lang_strlen, %function")
	g.label("__lang_strlen")
	g.emit("mov r1, r0")
	g.label(".Lsl_loop")
	g.emit("ldrb r2, [r1], #1")
	g.emit("cmp r2, #0")
	g.emit("bne .Lsl_loop")
	// r1 points one past the NUL → length = r1 - r0 - 1.
	g.emit("sub r0, r1, r0")
	g.emit("sub r0, r0, #1")
	g.emit("bx lr")
	g.line(".size __lang_strlen, .-__lang_strlen")
}

// emitStrcmpRuntime is the libc-shape string comparator we use
// from the IR's OpStrEq. Returns 0 when the strings match, non-zero
// otherwise; OpStrEq immediately collapses the result with
// `cmp r0, #0; moveq #1; movne #0`. Both args are NUL-terminated
// (lang strings carry a trailing NUL alongside their length
// prefix specifically for this kind of byte-walk).
func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".type __lang_strcmp, %function")
	g.label("__lang_strcmp")
	g.label(".Lscmp_loop")
	g.emit("ldrb r2, [r0], #1")
	g.emit("ldrb r3, [r1], #1")
	g.emit("cmp r2, r3")
	g.emit("bne .Lscmp_neq")
	g.emit("cmp r2, #0")
	g.emit("bne .Lscmp_loop")
	g.emit("mov r0, #0")
	g.emit("bx lr")
	g.label(".Lscmp_neq")
	g.emit("sub r0, r2, r3")
	g.emit("bx lr")
	g.line(".size __lang_strcmp, .-__lang_strcmp")
}

// emitPutsRuntime emits the print() builtin as a single
// `writev(2)` syscall over a 2-iovec gather: the user's
// length-prefixed string, then a single-byte newline. One
// kernel transition instead of two write(2) calls — exactly
// the kind of `\n`-suffix collapse that bun-style runtimes
// reach for.
//
// The iovec table sits on the stack (16 bytes — two 8-byte
// `{base, len}` records); we tear it down on exit. r4 holds
// the data ptr through the syscall so we can return it for
// libc-puts consistency.
func (g *generator) emitPutsRuntime() {
	g.line("")
	g.line(".type __lang_puts, %function")
	g.label("__lang_puts")
	g.emit("push {r4, lr}")
	g.emit("mov r4, r0") // r4 = data ptr (saved for return)
	g.emit("sub sp, sp, #16")
	g.emit("ldr r2, [r0, #-4]") // r2 = length
	g.emit("str r0, [sp]")      // iov[0].base = data ptr
	g.emit("str r2, [sp, #4]")  // iov[0].len  = length
	g.emit("ldr r3, =.LLangNewline")
	g.emit("str r3, [sp, #8]") // iov[1].base = newline
	g.emit("mov r3, #1")
	g.emit("str r3, [sp, #12]") // iov[1].len  = 1
	g.emit("mov r0, #1")        // fd 1 (stdout)
	g.emit("mov r1, sp")        // iovec*
	g.emit("mov r2, #2")        // iovcnt
	g.emitSyscall(sysWritev)
	g.emit("add sp, sp, #16")
	g.emit("mov r0, r4")
	g.emit("pop {r4, lr}")
	g.emit("bx lr")
	g.line(".size __lang_puts, .-__lang_puts")
}

// emitPutcharRuntime emits the putchar() builtin: writes one byte
// (the low 8 bits of r0) to stdout. The byte goes onto the stack
// at sp+0 because write(2) needs a buffer address; the stack is
// the cheapest scratch we have.
func (g *generator) emitPutcharRuntime() {
	g.line("")
	g.line(".type __lang_putchar, %function")
	g.label("__lang_putchar")
	g.emit("sub sp, sp, #4")
	g.emit("strb r0, [sp]")
	g.emit("mov r1, sp")
	g.emit("mov r2, #1")
	g.emit("mov r0, #1")
	g.emitSyscall(sysWrite)
	g.emit("add sp, sp, #4")
	g.emit("bx lr")
	g.line(".size __lang_putchar, .-__lang_putchar")
}

// emitExitRuntime emits the exit() builtin as a direct
// `exit_group` syscall. exit_group reaps the whole process group;
// we don't have threads, but the kernel's recommendation for a
// "really exit" call is exit_group regardless.
func (g *generator) emitExitRuntime() {
	g.line("")
	g.line(".type __lang_exit, %function")
	g.label("__lang_exit")
	g.emitSyscall(sysExitGroup)
	// Unreachable, but the assembler likes a real terminating
	// instruction.
	g.emit("bx lr")
	g.line(".size __lang_exit, .-__lang_exit")
}
