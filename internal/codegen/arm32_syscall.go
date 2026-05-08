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
	sysExit       = 1
	sysRead       = 3
	sysWrite      = 4
	sysOpen       = 5
	sysClose      = 6
	sysLseek      = 19
	sysBrk        = 45
	sysWritev     = 146
	sysMmap2      = 192
	sysFstat64    = 197
	sysExitGroup  = 248
	sysGetrandom  = 384
)

// heapReserveBytes is the size of the anonymous mmap region we
// reserve for the bump arena at startup. Linux only commits
// physical pages on first touch, so this is virtual-address-
// space cost only — fine on 32-bit ARM (3 GB user VAS) and
// it sidesteps every brk-grow syscall that the bump path would
// otherwise hit. 64 MiB covers anything the CLI / edge-function
// targets are likely to allocate.
const heapReserveBytes = 64 * 1024 * 1024

// mmap flags / prot bits we need from <sys/mman.h>. Keeping
// them as named constants to make the heap_init body readable.
const (
	mmapProtReadWrite = 0x3  // PROT_READ | PROT_WRITE
	mmapPrivateAnon   = 0x22 // MAP_PRIVATE | MAP_ANONYMOUS
)

// fstat64 puts st_size's lo32 at offset 48 in the buffer it
// fills (kernel struct stat64 layout from
// arch/arm/include/uapi/asm/stat.h, verified empirically). We
// only support files <4 GiB, so the hi32 at offset 52 is
// ignored.
const stat64SizeOffset = 48

// stat64BufferBytes is the stack space `__lang_read_file`
// reserves for the kernel-filled stat buffer. The kernel
// struct is ~96 bytes; we round up to leave room for any
// future kernel additions and keep sp 8-byte aligned.
const stat64BufferBytes = 112

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

// emitHeapInitRuntime reserves the bump arena via a single
// `mmap2(NULL, 64 MiB, RW, MAP_PRIVATE|ANONYMOUS, -1, 0)`
// syscall. Linux populates physical pages lazily on first
// touch, so this costs virtual-address-space only — none of
// the 64 MiB is actually pinned until something allocates.
//
// Compared to the older `brk(0)` + per-grow `brk(new_end)`
// shape, this gives `__lang_alloc`'s fast path a single
// pointer bump with no slow-path syscall: the heap is simply
// big enough up front. brk is also somewhat deprecated in
// modern kernels (musl, bionic, jemalloc, mimalloc all use
// mmap), so the move is forward-looking.
//
// On failure (extremely unlikely — a 64 MiB anonymous mmap
// always succeeds on 32-bit ARM Linux) we exit_group(137) to
// match the OOM-killer convention.
func (g *generator) emitHeapInitRuntime() {
	g.line("")
	g.line(".type __lang_heap_init, %function")
	g.label("__lang_heap_init")
	g.emit("push {r4, r5, lr}")
	g.emit("sub sp, sp, #4") // 16-byte alignment after r4+r5+lr+pad
	g.emit("mov r0, #0")     // addr = NULL → kernel chooses
	g.emit("ldr r1, =%d", heapReserveBytes)
	g.emit("mov r2, #%d", mmapProtReadWrite)
	g.emit("mov r3, #%d", mmapPrivateAnon)
	g.emit("mov r4, #-1") // fd = -1 (anonymous)
	g.emit("mov r5, #0")  // pgoffset
	g.emitSyscall(sysMmap2)
	// Linux returns the address (positive) on success, or
	// -errno (negative) on failure. Addresses on 32-bit user
	// space are always positive.
	g.emit("cmp r0, #0")
	g.emit("blt .Lhinit_oom")
	g.emit("ldr r1, =__lang_heap_ptr")
	g.emit("str r0, [r1]")
	g.emit("ldr r1, =__lang_heap_end")
	g.emit("ldr r2, =%d", heapReserveBytes)
	g.emit("add r2, r0, r2")
	g.emit("str r2, [r1]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.label(".Lhinit_oom")
	g.emit("mov r0, #137")
	g.emitSyscall(sysExitGroup)
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
// its length in r0. Word-grain on the bulk path: each iteration
// reads a 4-byte word and tests for a NUL byte using the
// classic `(word - 0x01010101) & ~word & 0x80808080` bit-trick
// (non-zero iff some byte in the word is zero), falling into a
// short byte-scan to find the exact NUL only when one is
// detected. ~4× faster than the byte-grain loop on long
// strings; the overhead on short strings is one extra word
// load + the bit-twiddle, which is still cheaper than four
// individual byte-loads.
//
// Used only by the env() / args() helpers when they're copying
// kernel-provided strings into lang strings — length-prefixed
// lang strings already know their length.
//
// Safe against page-fault past end: env / argv strings live in
// the kernel-provided initial-stack region which is mapped
// continuously, so reading 4 bytes past any NUL stays within
// mapped memory.
func (g *generator) emitStrlenRuntime() {
	g.line("")
	g.line(".type __lang_strlen, %function")
	g.label("__lang_strlen")
	g.emit("push {r4, lr}")           // save callee-saved r4 + lr (paired for 8-byte sp align)
	g.emit("mov r4, r0")              // r4 = saved start ptr (for length compute at end)
	g.emit("ldr r2, =0x01010101")     // magic1 — subtrahend
	g.emit("ldr r3, =0x80808080")     // magic2 — high-bit mask
	g.label(".Lsl_word")
	g.emit("ldr r12, [r0]")           // r12 = word at current ptr
	g.emit("sub r1, r12, r2")         // r1 = word - magic1
	g.emit("bic r1, r1, r12")         //     & ~word
	g.emit("ands r1, r1, r3")         //     & magic2 ; sets Z if no NUL
	g.emit("bne .Lsl_byte")
	g.emit("add r0, r0, #4")
	g.emit("b .Lsl_word")
	g.label(".Lsl_byte")
	// r12 holds the word containing a NUL; r0 points at it.
	// Find the NUL byte by testing each byte position.
	g.emit("tst r12, #0xff")
	g.emit("beq .Lsl_done")
	g.emit("add r0, r0, #1")
	g.emit("tst r12, #0xff00")
	g.emit("beq .Lsl_done")
	g.emit("add r0, r0, #1")
	g.emit("tst r12, #0xff0000")
	g.emit("beq .Lsl_done")
	g.emit("add r0, r0, #1") // must be byte 3 — the bit-trick guaranteed some byte is NUL
	g.label(".Lsl_done")
	g.emit("sub r0, r0, r4") // r0 = end - start = length
	g.emit("pop {r4, lr}")
	g.emit("bx lr")
	g.line(".size __lang_strlen, .-__lang_strlen")
}

// emitStrcmpRuntime emits the equality-only string comparator
// the IR's OpStrEq calls. Three layered short-circuits:
//
//  1. Pointer equality: if the two args are the same address
//     (cheap interning catches every repeated literal —
//     `internString` deduplicates `.LStr_*` labels), they're
//     trivially equal. One cmp + branch.
//  2. Length equality: lang strings carry a 4-byte little-
//     endian length prefix at `ptr - 4`. Different lengths →
//     definitely unequal, no byte comparison needed.
//  3. Word-grain bulk + byte-grain tail: with the length known
//     in advance we don't need a NUL check inside the loop,
//     just bound the iteration by the length. Four bytes per
//     iter on the bulk path.
//
// Returns 0 when the strings match, 1 otherwise — OpStrEq
// collapses the result via `cmp r0, #0 ; moveq r0, #1 ;
// movne r0, #0`. (Sign of the non-zero return is intentionally
// not preserved; libc strcmp's lexicographic ordering isn't
// used by the language.)
func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".type __lang_strcmp, %function")
	g.label("__lang_strcmp")
	// 1. Same pointer? Equal.
	g.emit("cmp r0, r1")
	g.emit("beq .Lscmp_eq")
	// 2. Same length?
	g.emit("ldr r2, [r0, #-4]")
	g.emit("ldr r3, [r1, #-4]")
	g.emit("cmp r2, r3")
	g.emit("bne .Lscmp_neq")
	// 3a. Word-grain bulk — r2 holds remaining bytes.
	g.label(".Lscmp_word")
	g.emit("cmp r2, #4")
	g.emit("blt .Lscmp_tail")
	g.emit("ldr r12, [r0], #4")
	g.emit("ldr r3, [r1], #4")
	g.emit("cmp r12, r3")
	g.emit("bne .Lscmp_neq")
	g.emit("sub r2, r2, #4")
	g.emit("b .Lscmp_word")
	// 3b. Byte-grain tail.
	g.label(".Lscmp_tail")
	g.emit("cmp r2, #0")
	g.emit("beq .Lscmp_eq")
	g.emit("ldrb r12, [r0], #1")
	g.emit("ldrb r3, [r1], #1")
	g.emit("cmp r12, r3")
	g.emit("bne .Lscmp_neq")
	g.emit("sub r2, r2, #1")
	g.emit("b .Lscmp_tail")
	g.label(".Lscmp_eq")
	g.emit("mov r0, #0")
	g.emit("bx lr")
	g.label(".Lscmp_neq")
	g.emit("mov r0, #1")
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

// emitRandomBytesRuntime emits `__lang_random_bytes(n)` —
// allocates a fresh length-prefixed lang string of n bytes
// and fills it with kernel-provided cryptographic randomness
// via the `getrandom(2)` syscall. Returns the string's data
// pointer.
//
// `getrandom(buf, n, 0)` reads from the kernel's CSPRNG,
// blocking until enough entropy is available (rare on a
// running system; typically returns immediately). flags=0
// means "draw from /dev/urandom". The syscall fills the
// buffer with cryptographically-strong random bytes,
// suitable for session IDs, request IDs, etc.
func (g *generator) emitRandomBytesRuntime() {
	g.line("")
	g.line(".global __lang_random_bytes")
	g.line(".type __lang_random_bytes, %function")
	g.label("__lang_random_bytes")
	g.emit("push {r4, r5, lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment for bl __lang_alloc
	g.emit("mov r4, r0")     // r4 = N
	// Allocate N + 5 bytes (4 prefix + N data + 1 trailing NUL).
	g.emit("add r0, r4, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r5, r0, #4")    // r5 = data ptr (post-prefix)
	g.emit("str r4, [r5, #-4]") // length prefix
	// getrandom(buf, len, flags=0) — blocking, /dev/urandom source.
	g.emit("mov r0, r5")
	g.emit("mov r1, r4")
	g.emit("mov r2, #0")
	g.emitSyscall(sysGetrandom)
	// Trailing NUL keeps the libc-shaped invariant our other
	// helpers rely on (`__lang_strcat` etc. peek at one byte
	// past the data pointer).
	g.emit("add r1, r5, r4")
	g.emit("mov r2, #0")
	g.emit("strb r2, [r1]")
	g.emit("mov r0, r5")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.line(".size __lang_random_bytes, .-__lang_random_bytes")
}

// emitExitRuntime emits the exit() builtin as a direct
// `exit_group` syscall. exit_group reaps the whole process
// group; we don't have threads, but the kernel's recommendation
// for a "really exit" call is exit_group regardless.
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

// emitArenaSaveRuntime returns the current bump-allocator
// cursor as an integer. Pair with __lang_arena_restore to free
// everything allocated in between in one pointer-store. Two
// instructions (load + return) — the operation is essentially
// free.
func (g *generator) emitArenaSaveRuntime() {
	g.line("")
	g.line(".type __lang_arena_save, %function")
	g.label("__lang_arena_save")
	g.emit("ldr r0, =__lang_heap_ptr")
	g.emit("ldr r0, [r0]")
	g.emit("bx lr")
	g.line(".size __lang_arena_save, .-__lang_arena_save")
}

// emitArenaRestoreRuntime rewinds the bump-allocator cursor
// to the value returned by an earlier arena_save. Anything
// allocated since that save is reclaimed in one store. The
// runtime trusts the caller not to hold pointers into the
// reclaimed region — no compile-time check.
func (g *generator) emitArenaRestoreRuntime() {
	g.line("")
	g.line(".type __lang_arena_restore, %function")
	g.label("__lang_arena_restore")
	g.emit("ldr r1, =__lang_heap_ptr")
	g.emit("str r0, [r1]")
	g.emit("bx lr")
	g.line(".size __lang_arena_restore, .-__lang_arena_restore")
}
