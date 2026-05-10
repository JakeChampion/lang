// Package codegen emits ARM 32-bit assembly (GNU syntax, suitable for
// `arm-linux-gnueabihf-as` / gcc) from a checked Program. Production
// emit goes through the IR — Lower, Inline, FuseTee, TailCallOptimize,
// FlattenBranches, and the OptimizeCleanup driver (PropagateCopies,
// ConstPropagate, Fold, ReduceStrength) all run before EmitFromIR
// walks the IR ops to assembly. The pipeline is shared with the WASM
// backend so a new language feature lands once at the IR layer and
// both backends pick it up.
//
// Calling convention: standard AAPCS.
//
//   * Every expression's value lives in r0; the IR's operand stack
//     maps to the runtime stack via push / pop {r0}.
//   * Binary operators pop the right operand into r0 and the left
//     into r1, do the work, and push the result.
//   * Args 0..3 ride in r0..r3; extras come from the caller's stack
//     frame at fp+8, fp+12, …
//   * Leaf functions (no calls in the body) pin their parameters to
//     callee-saved r4..r{4+P-1} instead of spilling.
//   * Locals, spilled parameters, and synthetic scratch slots
//     (ArrayLit / StructLit / Switch / inlined-callee bindings) all
//     live at negative offsets from fp. Heap-backed values (arrays,
//     strings, structs) come from `__lang_alloc`, a tiny libc-malloc
//     wrapper this emitter generates on demand.
//
// The output is plain `.s` text — feed it to `gcc` (or `as` + `ld`)
// to produce a runnable executable, and run it with `qemu-arm` if
// you're not on an ARM host.
package codegen

import (
	"fmt"
	"math"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/treeshake"
)

// regArgs is the number of arguments that AAPCS passes in registers
// (r0..r3). Anything beyond that goes through the caller's stack frame.
const regArgs = 4

// Options tunes Emit. The zero value is fine for production codegen;
// pass SourceFile to opt into DWARF line-info via .file/.loc directives
// (use it together with `gcc -g` at the link step).
type Options struct {
	SourceFile string
}

// Emit returns the assembly text for prog. It's a thin wrapper over
// EmitWithOptions for callers that don't need debug info.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithOptions returns the assembly text for prog. When
// opts.SourceFile is set, the output starts with `.file 1 "<name>"`
// and every statement is preceded by a `.loc 1 <line> <col>` directive
// so `gcc -g` produces a usable DWARF line-number table.
//
// Production codegen routes through the IR — closure conversion +
// stack-machine lowering + tail-call optimisation, all in one
// shared pipeline with the WASM backend.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	// Tree-shake unreferenced functions before lowering so the
	// auto-injected lang prelude (internal/prelude) only pays
	// for what user code actually calls. Crucial for arm32 in
	// particular since prelude helpers may use ops not yet
	// supported on arm32 (e.g. i64 arithmetic) — those funcs
	// stay out of the lowered program when the user code
	// doesn't call them.
	treeshake.Run(prog)
	return EmitFromIR(prog, info, opts)
}

type generator struct {
	out         strings.Builder
	info        *checker.Info
	labelN      int
	stringLabel map[string]string // value -> label
	stringOrder []string          // insertion order so output is deterministic
	// stateDecls preserves the source program's state-block
	// declarations so the data-section emitter at the end of
	// EmitFromIR can render `.data` labels for each state var.
	// Empty for programs without state blocks; OpLoadGlobal /
	// OpStoreGlobal is unused in that case.
	stateDecls []*ast.StateDecl
	// hasStateInit is true when the synthesised `__state_init`
	// function exists (state{} declared at least one
	// non-literal init). emitStartRuntime gates the
	// `bl __state_init` call on this flag so programs without
	// runtime state init don't pay an unconditional branch.
	hasStateInit bool
	// lineBufferLabel/lineBufferOrder dedupe the
	// `data + "\n"` buffers used by the `print(literal)` fold.
	// Same shape as stringLabel/stringOrder but with the
	// trailing newline baked in and no length-prefix / NUL —
	// the inline `write(2)` syscall takes a raw (ptr, len)
	// pair, not a lang string.
	lineBufferLabel map[string]string
	lineBufferOrder []string
	// nonLiteralPrintCount is the number of `print(…)` call
	// sites whose arg isn't a string literal — used to decide
	// whether the legacy `__lang_puts` helper still needs to be
	// emitted. When zero (every print is a literal), we drop
	// the helper entirely and the binary is purely inline writes.
	nonLiteralPrintCount int
	usesStrcat  bool              // true if the program needs the strcat helper
	usesAlloc   bool              // true if the program needs the alloc helper (any heap-backed array / struct / closure)
	usesArgs      bool            // true if the program calls args() — pulls in the runtime helper + main argc/argv save
	usesPuts      bool            // true if the program calls print() — pulls in __lang_puts (write+newline)
	usesPutchar   bool            // true if the program calls putchar() — pulls in __lang_putchar (1-byte write)
	usesWrite     bool            // true if the program calls write() — pulls in __lang_write
	usesEprint    bool            // true if the program calls eprint() — pulls in __lang_eprint and the newline byte
	usesReadLine  bool            // true if the program calls read_line() — pulls in __lang_read_line and the .bss buffer
	usesEnv       bool            // true if the program calls env() — pulls in __lang_env + envp walker
	usesExit      bool            // true if the program calls exit() — pulls in __lang_exit (direct exit_group)
	usesArena     bool            // true if the program calls arena_save / arena_restore — pulls in the two heap-cursor helpers
	usesRandomBytes bool          // true if the program calls random_bytes() — pulls in __lang_random_bytes (getrandom syscall)
	usesTcp         bool          // true if the program calls any of tcp_* — pulls in the TCP socket helpers
	usesReadFile  bool            // true if the program calls read_file() — pulls in __lang_read_file + __build_io_error
	usesWriteFile bool            // true if the program calls write_file() — pulls in __lang_write_file + __build_io_error
	usesStreamIO  bool            // true if the program calls open_reader / open_writer / a Reader|Writer method
	usesStdStreams bool           // true if the program calls stdin / stdout / stderr
	srcFile     string            // non-empty enables DWARF .file/.loc directives
}

// emitAllocRuntime emits the bump-style `__lang_alloc(size)`
// helper — the only allocator in the runtime. The arena is the
// region between `__lang_heap_ptr` (next free byte) and
// `__lang_heap_end` (the end of the 64 MiB anonymous mmap'd
// region heap_init reserved at startup). Allocations bump the
// pointer by `size` (rounded up to 4 bytes for natural
// alignment); if the bump would cross `__lang_heap_end` we
// exit_group(137), matching the OOM-killer convention.
//
// There's no `free` and no slow-path syscall — that's the
// whole point. Linux populates physical pages lazily on first
// touch of the mmap'd region, so the bump path is purely
// in-process, six instructions and a branch. Programs that
// genuinely need >64 MiB are out of scope for the CLI / edge-
// function targets the language is aimed at.
//
// A future arena-reset primitive snapshots / restores
// `__lang_heap_ptr` for the long-lived HTTP server case.
func (g *generator) emitAllocRuntime() {
	g.line("")
	g.line(".global __lang_alloc")
	g.line(".type __lang_alloc, %function")
	g.label("__lang_alloc")
	// Round size up to a multiple of 4 so subsequent allocs land
	// on natural alignment. ARM has no `align` instruction; the
	// (n + 3) & ~3 idiom takes two opcodes.
	g.emit("add r0, r0, #3")
	g.emit("bic r0, r0, #3")
	g.emit("ldr r1, =__lang_heap_ptr")
	g.emit("ldr r2, [r1]")   // r2 = current ptr
	g.emit("add r3, r2, r0") // r3 = wanted top
	g.emit("ldr r12, =__lang_heap_end")
	g.emit("ldr r12, [r12]")
	g.emit("cmp r3, r12")
	g.emit("bhi .Lalloc_oom")
	g.emit("str r3, [r1]")
	g.emit("mov r0, r2")
	g.emit("bx lr")
	g.label(".Lalloc_oom")
	g.emit("mov r0, #137") // exit code 137 mirrors the OOM-killer SIGKILL
	g.emitSyscall(sysExitGroup)
	g.line(".size __lang_alloc, .-__lang_alloc")
}

// emitStrcatRuntime emits a leaf-style helper that allocates a fresh
// length-prefixed buffer holding the concatenation of two strings:
//
//	r0 = a (data ptr), r1 = b (data ptr)   →   r0 = ptr to combined data
//
// Layout matches array literals — a 4-byte little-endian length sits
// at `result - 4`, with a trailing NUL byte kept past the data so
// libc strlen / strcmp keep working on the same pointer. The buffer
// is never freed; strings are immutable but not GC'd.
//
// Operands arrive as data pointers (post-prefix), but the lengths
// live at `ptr - 4`, so the helper grabs them with a single load
// each rather than walking the buffer with strlen.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".global __lang_strcat")
	g.line(".type __lang_strcat, %function")
	g.label("__lang_strcat")
	g.emit("push {r4, r5, r6, r7, lr}")
	g.emit("sub sp, sp, #4")    // 8-byte alignment
	g.emit("mov r4, r0")        // r4 = a (data ptr)
	g.emit("mov r5, r1")        // r5 = b (data ptr)
	g.emit("ldr r6, [r0, #-4]") // r6 = la (length prefix)
	g.emit("ldr r7, [r1, #-4]") // r7 = lb
	g.emit("add r0, r6, r7")
	g.emit("add r0, r0, #5") // 4 (prefix) + la + lb + 1 (trailing NUL)
	g.emit("bl __lang_alloc")
	g.emit("add r0, r0, #4") // r0 = data pointer (skip prefix)
	g.emit("add r1, r6, r7")
	g.emit("str r1, [r0, #-4]") // store length prefix
	g.emit("mov r1, r4")        // src = a
	g.emit("mov r2, r6")        // n = la
	g.emit("mov r4, r0")        // r4 = result data ptr
	g.emit("bl __lang_memcpy")
	g.emit("add r0, r4, r6") // dst = result + la
	g.emit("mov r1, r5")     // src = b
	g.emit("add r2, r7, #1") // n = lb + 1 (include NUL)
	g.emit("bl __lang_memcpy")
	g.emit("mov r0, r4")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, r6, r7, lr}")
	g.emit("bx lr")
	g.line(".size __lang_strcat, .-__lang_strcat")
}

// emitArgsRuntime emits the `__lang_args` helper. It takes no
// arguments (the underlying argc / argv come from .bss globals
// `__lang_argc` / `__lang_argv` populated by `main`'s prologue),
// returns a length-prefixed `string[]` data pointer, and caches
// the result in `__lang_args_cache` so repeat calls are O(1).
//
// On the slow path the helper allocates the result string[],
// then for each argv entry runs strlen / __lang_alloc / memcpy
// to build a fresh length-prefixed copy. We include the trailing
// NUL in each copy so libc-shaped consumers (puts / strlen) keep
// working on the same data pointer the rest of the language
// hands around.
func (g *generator) emitArgsRuntime() {
	g.line("")
	g.line(".global __lang_args")
	g.line(".type __lang_args, %function")
	g.label("__lang_args")
	// 8 registers × 4 bytes = 32 bytes — already 8-byte aligned,
	// so we don't need an extra `sub sp, sp, #4` for AAPCS calls.
	g.emit("push {r4, r5, r6, r7, r8, r9, r10, lr}")

	// Fast path: cached pointer is non-zero → return it.
	g.emit("ldr r0, =__lang_args_cache")
	g.emit("ldr r0, [r0]")
	g.emit("cmp r0, #0")
	g.emit("beq .Largs_build")
	g.emit("pop {r4, r5, r6, r7, r8, r9, r10, lr}")
	g.emit("bx lr")

	g.label(".Largs_build")
	// r4 = argc, r5 = argv (pointer to char**)
	g.emit("ldr r4, =__lang_argc")
	g.emit("ldr r4, [r4]")
	g.emit("ldr r5, =__lang_argv")
	g.emit("ldr r5, [r5]")

	// Allocate the result string[] container: 4 bytes for length
	// prefix + argc * 4 bytes for entry pointers.
	g.emit("lsl r0, r4, #2")
	g.emit("add r0, r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("add r6, r0, #4")    // r6 = result data pointer (post-prefix)
	g.emit("str r4, [r6, #-4]") // length prefix = argc

	// for (i = 0; i < argc; i++)
	g.emit("mov r7, #0") // r7 = i
	g.label(".Largs_loop")
	g.emit("cmp r7, r4")
	g.emit("bge .Largs_done")

	// r8 = argv[i] (the C string)
	g.emit("ldr r8, [r5, r7, lsl #2]")

	// r9 = strlen(r8)
	g.emit("mov r0, r8")
	g.emit("bl __lang_strlen")
	g.emit("mov r9, r0")

	// Allocate strlen + 5 bytes (4 prefix + N data + 1 trailing NUL).
	g.emit("add r0, r9, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r10, r0, #4")    // r10 = string data pointer
	g.emit("str r9, [r10, #-4]") // length prefix

	// memcpy(r10, r8, r9 + 1) — include NUL.
	g.emit("mov r0, r10")
	g.emit("mov r1, r8")
	g.emit("add r2, r9, #1")
	g.emit("bl __lang_memcpy")

	// result[i] = r10
	g.emit("str r10, [r6, r7, lsl #2]")

	g.emit("add r7, r7, #1")
	g.emit("b .Largs_loop")

	g.label(".Largs_done")
	// Cache and return.
	g.emit("ldr r0, =__lang_args_cache")
	g.emit("str r6, [r0]")
	g.emit("mov r0, r6")
	g.emit("pop {r4, r5, r6, r7, r8, r9, r10, lr}")
	g.emit("bx lr")
	g.line(".size __lang_args, .-__lang_args")
}

// emitWriteRuntime emits `__lang_write`, the 1-arg shim that turns
// `write(s)` into a `write(1, s, len)` direct syscall. The string's
// length lives at `s - 4` so we read it without scanning. Leaf —
// no `lr` save needed since we don't `bl` anything.
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".global __lang_write")
	g.line(".type __lang_write, %function")
	g.label("__lang_write")
	g.emit("ldr r2, [r0, #-4]") // r2 = length
	g.emit("mov r1, r0")        // r1 = buf
	g.emit("mov r0, #1")        // r0 = fd (stdout)
	g.emitSyscall(sysWrite)
	g.emit("bx lr")
	g.line(".size __lang_write, .-__lang_write")
}

// emitEprintRuntime emits `__lang_eprint`, the stderr
// counterpart to `__lang_puts`. Single `writev(2)` over a
// 2-iovec gather (string + newline) — same pattern as puts,
// just routed to fd 2.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".global __lang_eprint")
	g.line(".type __lang_eprint, %function")
	g.label("__lang_eprint")
	g.emit("push {lr}")
	g.emit("sub sp, sp, #20") // 16 bytes iovec + 4 bytes alignment pad
	g.emit("ldr r2, [r0, #-4]") // r2 = length
	g.emit("str r0, [sp]")      // iov[0].base = data ptr
	g.emit("str r2, [sp, #4]")  // iov[0].len  = length
	g.emit("ldr r3, =.LLangNewline")
	g.emit("str r3, [sp, #8]") // iov[1].base = newline
	g.emit("mov r3, #1")
	g.emit("str r3, [sp, #12]") // iov[1].len  = 1
	g.emit("mov r0, #2")        // fd 2 (stderr)
	g.emit("mov r1, sp")        // iovec*
	g.emit("mov r2, #2")        // iovcnt
	g.emitSyscall(sysWritev)
	g.emit("add sp, sp, #20")
	g.emit("pop {lr}")
	g.emit("bx lr")
	g.line(".size __lang_eprint, .-__lang_eprint")
}

// emitReadLineRuntime emits `__lang_read_line`, a stdin one-byte
// reader that returns an `Option[string]` heap object. The
// result is `Some(line)` when at least one byte was read (the
// line preserves its trailing `\n`); `None` when the first read
// returned 0. Tag 0 is `Some`, tag 1 is `None` — same convention
// as the WASM helper, hardcoded to match the auto-injected
// Option enum.
//
// Layout:
//
//	Some: [tag=0 : 4][string_ptr : 4]   (8 bytes)
//	None: [tag=1 : 4]                    (4 bytes)
func (g *generator) emitReadLineRuntime() {
	g.line("")
	g.line(".global __lang_read_line")
	g.line(".type __lang_read_line, %function")
	g.label("__lang_read_line")
	g.emit("push {r4, r5, r6, lr}") // 16 bytes — already 8-aligned
	g.emit("ldr r4, =__lang_read_line_buf")
	g.emit("mov r5, #0") // r5 = bytes read so far
	g.label(".Lrl_loop")
	g.emit("cmp r5, #4096")
	g.emit("bge .Lrl_done")
	// read(0, buf + r5, 1)
	g.emit("mov r0, #0")
	g.emit("add r1, r4, r5")
	g.emit("mov r2, #1")
	g.emitSyscall(sysRead)
	// EOF (or error) → finish.
	g.emit("cmp r0, #1")
	g.emit("blt .Lrl_done")
	// Examine the byte we just read.
	g.emit("add r6, r4, r5")
	g.emit("ldrb r6, [r6]")
	g.emit("add r5, r5, #1")
	// If it's '\n', the line is complete.
	g.emit("cmp r6, #10")
	g.emit("beq .Lrl_done")
	g.emit("b .Lrl_loop")
	g.label(".Lrl_done")
	// EOF on first byte → return None.
	g.emit("cmp r5, #0")
	g.emit("bne .Lrl_some")
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]") // tag = 1 (None)
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")
	g.label(".Lrl_some")
	// Allocate length + 5 (4 prefix + N data + 1 trailing NUL for libc shape).
	g.emit("add r0, r5, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r6, r0, #4")    // r6 = data ptr
	g.emit("str r5, [r6, #-4]") // length prefix
	// memcpy(r6, buf, r5)
	g.emit("mov r0, r6")
	g.emit("mov r1, r4")
	g.emit("mov r2, r5")
	g.emit("bl __lang_memcpy")
	// Trailing NUL at r6 + r5
	g.emit("add r0, r6, r5")
	g.emit("mov r1, #0")
	g.emit("strb r1, [r0]")
	// Wrap as Some(r6) — alloc 8 bytes, store [tag=0, str_ptr].
	g.emit("mov r4, r6") // save string data ptr in r4
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")     // tag = 0 (Some)
	g.emit("str r4, [r0, #4]") // payload = string data ptr
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")
	g.line(".size __lang_read_line, .-__lang_read_line")
}

// emitEnvRuntime emits `__lang_env(name)`, the no-libc replacement
// for getenv. Walks the kernel-provided envp vector (saved by
// _start) looking for an entry of the form `NAME=VALUE` whose
// `NAME` matches the lang string `name` byte-for-byte. Returns
// `Option[string]`: `Some(VALUE)` on a match (the value is copied
// into a fresh length-prefixed lang string), `None` otherwise.
//
// envp is a NULL-terminated array of `char*`; each pointer is to a
// NUL-terminated `KEY=VALUE` C string. The lang `name` arrives as a
// length-prefixed lang string with a trailing NUL kept past the
// data, so we can compare it byte-for-byte with strcmp-like logic
// up to the `=`.
//
// Tag layout matches `__lang_read_line`: 0 = Some, 1 = None.
// Some is 8 bytes [tag, str_ptr]; None is 4 bytes [tag].
func (g *generator) emitEnvRuntime() {
	g.line("")
	g.line(".global __lang_env")
	g.line(".type __lang_env, %function")
	g.label("__lang_env")
	g.emit("push {r4, r5, r6, r7, r8, lr}")
	g.emit("mov r4, r0") // r4 = name (lang string data ptr)
	g.emit("ldr r5, [r0, #-4]") // r5 = name length
	g.emit("ldr r6, =__lang_envp")
	g.emit("ldr r6, [r6]") // r6 = envp
	g.label(".Lenv_loop")
	g.emit("ldr r7, [r6]") // r7 = envp[i]
	g.emit("cmp r7, #0")
	g.emit("beq .Lenv_missing")
	// Compare r5 bytes of name to envp[i].
	g.emit("mov r0, r4")
	g.emit("mov r1, r7")
	g.emit("mov r2, r5")
	g.emit("bl __lang_memcmp_n")
	g.emit("cmp r0, #0")
	g.emit("bne .Lenv_next")
	// Bytes match — next byte must be '=' for it to be the right key.
	g.emit("ldrb r0, [r7, r5]")
	g.emit("cmp r0, #61") // '='
	g.emit("bne .Lenv_next")
	// Found it. r8 = pointer to value (envp[i] + name_len + 1).
	g.emit("add r8, r7, r5")
	g.emit("add r8, r8, #1")
	// Length of value = strlen(r8).
	g.emit("mov r0, r8")
	g.emit("bl __lang_strlen")
	g.emit("mov r5, r0") // r5 = value length (overwrite name len, no longer needed)
	// Allocate length + 5 (4 prefix + N data + trailing NUL).
	g.emit("add r0, r5, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r4, r0, #4")    // r4 = string data ptr
	g.emit("str r5, [r4, #-4]") // length prefix
	g.emit("mov r0, r4")
	g.emit("mov r1, r8")
	g.emit("add r2, r5, #1") // include trailing NUL
	g.emit("bl __lang_memcpy")
	// Wrap as Some(string) — alloc 8 bytes, store [tag=0, str_ptr].
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r4, [r0, #4]")
	g.emit("pop {r4, r5, r6, r7, r8, lr}")
	g.emit("bx lr")
	g.label(".Lenv_next")
	g.emit("add r6, r6, #4") // envp++
	g.emit("b .Lenv_loop")
	g.label(".Lenv_missing")
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("pop {r4, r5, r6, r7, r8, lr}")
	g.emit("bx lr")
	g.line(".size __lang_env, .-__lang_env")
}

// emitMemcmpNRuntime emits `__lang_memcmp_n(a, b, n)`, a fixed-
// length byte comparator used only by `__lang_env` to test the
// "name" prefix of a `KEY=VALUE` envp entry. Returns 0 on match,
// non-zero on mismatch (libc-shape, but we don't propagate sign
// since callers only care about equality).
func (g *generator) emitMemcmpNRuntime() {
	g.line("")
	g.line(".type __lang_memcmp_n, %function")
	g.label("__lang_memcmp_n")
	g.label(".Lmcn_loop")
	g.emit("cmp r2, #0")
	g.emit("beq .Lmcn_eq")
	g.emit("ldrb r3, [r0], #1")
	g.emit("ldrb r12, [r1], #1")
	g.emit("cmp r3, r12")
	g.emit("bne .Lmcn_neq")
	g.emit("sub r2, r2, #1")
	g.emit("b .Lmcn_loop")
	g.label(".Lmcn_eq")
	g.emit("mov r0, #0")
	g.emit("bx lr")
	g.label(".Lmcn_neq")
	g.emit("mov r0, #1")
	g.emit("bx lr")
	g.line(".size __lang_memcmp_n, .-__lang_memcmp_n")
}

// emitFileIORuntime emits the libc-shaped runtime for
// `read_file` / `write_file` plus the shared `__build_io_error`
// helper that maps a libc errno to the right `IoError` variant.
//
// Variant indices match the auto-injected enum exactly
// (NotFound=0, PermissionDenied=1, AlreadyExists=2,
// InvalidUtf8=3, Interrupted=4, Unsupported=5, Other=6;
// Result.Ok=0/Err=1; Option.Some=0/None=1).
//
// `read_file` opens with O_RDONLY (=0) and reads in 4 KiB
// chunks onto the heap, packed contiguously by un-bumping
// any unused tail. After EOF the contiguous region is
// memcpy'd into a length-prefixed string and wrapped in
// Ok. `write_file` opens with
// O_WRONLY|O_CREAT|O_TRUNC (=0x241 on Linux/glibc) and a
// single write(2) of the entire content.
//
// Paths are passed straight to libc; the lang string already
// has a trailing NUL kept past the data (the strcat runtime
// preserves that), so `r0` is a valid C string for open().
func (g *generator) emitFileIORuntime() {
	g.emitBuildIoErrorArm()
	g.emitReadFileRuntime()
	g.emitWriteFileRuntime()
}

// emitBuildIoErrorArm emits __build_io_error(errno, path).
// Returns a heap pointer to an IoError variant. errno-to-
// variant mapping uses the typical Linux glibc values:
// ENOENT=2, EACCES=13, EEXIST=17, EINTR=4, ENOTSUP=95.
// Anything else flows into Other(path, "io error").
func (g *generator) emitBuildIoErrorArm() {
	g.line("")
	g.line(".global __build_io_error")
	g.line(".type __build_io_error, %function")
	g.label("__build_io_error")
	g.emit("push {r4, r5, lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	g.emit("mov r4, r0")     // r4 = errno
	g.emit("mov r5, r1")     // r5 = path

	// Each branch emits an alloc + tag + (optional) payload
	// stores, then returns. Variants without a path payload
	// allocate 4 bytes; with-path allocate 8.
	g.emitIoErrCaseArm(2, 0, true)   // ENOENT  → NotFound(path)
	g.emitIoErrCaseArm(13, 1, true)  // EACCES  → PermissionDenied(path)
	g.emitIoErrCaseArm(17, 2, true)  // EEXIST  → AlreadyExists(path)
	g.emitIoErrCaseArm(4, 4, false)  // EINTR   → Interrupted
	g.emitIoErrCaseArm(95, 5, false) // ENOTSUP → Unsupported

	// Fallthrough: Other(path, "io error"). 12 bytes.
	g.label(".Lioe_other")
	g.emit("mov r0, #12")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #6")
	g.emit("str r1, [r0]")
	g.emit("str r5, [r0, #4]")
	g.emit("ldr r1, =.Lioe_msg")
	g.emit("str r1, [r0, #8]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.line(".size __build_io_error, .-__build_io_error")
}

// emitIoErrCaseArm writes one errno → variant branch in
// __build_io_error. See emitIoErrorCase (wasm) for the
// equivalent on the WASM side.
func (g *generator) emitIoErrCaseArm(errno, tagIdx int, withPathPayload bool) {
	tag := fmt.Sprintf(".Lioe_case_%d", errno)
	skip := fmt.Sprintf(".Lioe_skip_%d", errno)
	g.emit("cmp r4, #%d", errno)
	g.emit("bne %s", skip)
	g.label(tag)
	if withPathPayload {
		g.emit("mov r0, #8")
	} else {
		g.emit("mov r0, #4")
	}
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #%d", tagIdx)
	g.emit("str r1, [r0]")
	if withPathPayload {
		g.emit("str r5, [r0, #4]")
	}
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.label(skip)
}

// emitReadFileRuntime — see emitFileIORuntime comment. We
// open the file, ask the kernel for its size with a single
// `fstat64(2)` (one syscall in place of the two `lseek`s a
// SEEK_END / SEEK_SET pair would need), then allocate a
// length-prefixed buffer of the exact size and read into it
// in a loop that handles partial returns by re-issuing
// `read(2)`. The bump allocator gives each open() a
// contiguous slab, so a single allocation suffices.
//
// `fstat64` doesn't move the file pointer, so the read loop
// picks up at offset 0 with no rewind needed.
//
// Direct syscalls — no libc, no `__errno_location`. The
// kernel returns `-errno` directly; we negate it on the
// error path.
func (g *generator) emitReadFileRuntime() {
	g.line("")
	g.line(".global __lang_read_file")
	g.line(".type __lang_read_file, %function")
	g.label("__lang_read_file")
	g.emit("push {r4, r5, r6, r7, r8, lr}")
	g.emit("sub sp, sp, #%d", stat64BufferBytes) // scratch for fstat64
	g.emit("mov r4, r0")                         // r4 = path

	// open(path, O_RDONLY=0)
	g.emit("mov r1, #0")
	g.emitSyscall(sysOpen)
	g.emit("cmp r0, #0")
	g.emit("bge .Lrf_opened")
	g.emit("rsb r0, r0, #0") // r0 = errno (positive)
	g.emit("mov r1, r4")
	g.emit("bl __build_io_error")
	g.emit("mov r5, r0")
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("str r5, [r0, #4]")
	g.emit("add sp, sp, #%d", stat64BufferBytes)
	g.emit("pop {r4, r5, r6, r7, r8, lr}")
	g.emit("bx lr")

	g.label(".Lrf_opened")
	g.emit("mov r5, r0") // r5 = fd

	// fstat64(fd, &buf): fills the kernel struct stat64 in
	// our stack scratch. st_size's lo32 sits at offset 48
	// (verified empirically on ARM EABI Linux).
	g.emit("mov r0, r5")
	g.emit("mov r1, sp")
	g.emitSyscall(sysFstat64)
	g.emit("ldr r6, [sp, #%d]", stat64SizeOffset) // r6 = st_size lo32

	// Allocate result string: 4 (length) + size + 1 (trailing NUL).
	g.emit("add r0, r6, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r7, r0, #4") // r7 = data ptr (post length prefix)
	// Length prefix is set after the read in case partial
	// reads truncate the actual byte count.

	// Read loop: read(fd, data + read_total, size - read_total)
	// repeatedly until we've consumed `size` bytes or read
	// returns 0 / -1.
	g.emit("mov r8, #0") // r8 = read_total
	g.label(".Lrf_loop")
	g.emit("cmp r8, r6")
	g.emit("bge .Lrf_done")
	g.emit("mov r0, r5")
	g.emit("add r1, r7, r8")
	g.emit("sub r2, r6, r8")
	g.emitSyscall(sysRead)
	g.emit("cmp r0, #0")
	g.emit("ble .Lrf_done")
	g.emit("add r8, r8, r0")
	g.emit("b .Lrf_loop")

	g.label(".Lrf_done")
	g.emit("str r8, [r7, #-4]") // length prefix = actual bytes read
	g.emit("add r0, r7, r8")
	g.emit("mov r1, #0")
	g.emit("strb r1, [r0]") // trailing NUL

	// close(fd)
	g.emit("mov r0, r5")
	g.emitSyscall(sysClose)

	// Build Ok(r7).
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r7, [r0, #4]")
	g.emit("add sp, sp, #%d", stat64BufferBytes)
	g.emit("pop {r4, r5, r6, r7, r8, lr}")
	g.emit("bx lr")
	g.line(".size __lang_read_file, .-__lang_read_file")
}

// emitWriteFileRuntime — see emitFileIORuntime comment.
// One open(2) + write(2) + close(2). Returns Option[IoError].
// All three are direct syscalls.
func (g *generator) emitWriteFileRuntime() {
	g.line("")
	g.line(".global __lang_write_file")
	g.line(".type __lang_write_file, %function")
	g.label("__lang_write_file")
	g.emit("push {r4, r5, r6, lr}")
	g.emit("mov r4, r0") // r4 = path
	g.emit("mov r5, r1") // r5 = content (data ptr)

	// open(path, O_WRONLY|O_CREAT|O_TRUNC=0x241, 0644)
	g.emit("mov r1, #0x241")
	g.emit("mov r2, #0644")
	g.emitSyscall(sysOpen)
	g.emit("cmp r0, #0")
	g.emit("bge .Lwf_opened")
	g.emit("rsb r0, r0, #0")
	g.emit("mov r1, r4")
	g.emit("bl __build_io_error")
	g.emit("mov r6, r0")
	// Build Some(ioerr): 8 bytes [tag=0, ioerr]
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r6, [r0, #4]")
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")

	g.label(".Lwf_opened")
	g.emit("mov r6, r0") // r6 = fd

	// write(fd, content_data, len(content))
	g.emit("mov r0, r6")
	g.emit("mov r1, r5")
	g.emit("ldr r2, [r5, #-4]")
	g.emitSyscall(sysWrite)
	g.emit("cmp r0, #0")
	g.emit("blt .Lwf_write_err")

	// close(fd) and return None.
	g.emit("mov r0, r6")
	g.emitSyscall(sysClose)
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")

	g.label(".Lwf_write_err")
	// Save -errno (the syscall return) before close clobbers r0.
	g.emit("rsb r5, r0, #0") // r5 = errno (positive)
	g.emit("mov r0, r6")
	g.emitSyscall(sysClose)
	g.emit("mov r0, r5")
	g.emit("mov r1, r4")
	g.emit("bl __build_io_error")
	g.emit("mov r6, r0")
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r6, [r0, #4]")
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")
	g.line(".size __lang_write_file, .-__lang_write_file")
}

// emitStreamIORuntime emits the open_reader / open_writer /
// open_appender constructors plus the Reader / Writer methods.
// All Reader / Writer values are 4-byte heap structs holding
// just the file descriptor; the methods load it before
// dispatching to libc read(2) / write(2) / close(2).
//
// Variant indices match the auto-injected Option / Result
// (Some=0, None=1, Ok=0, Err=1) and IoError (NotFound=0,
// PermissionDenied=1, AlreadyExists=2, InvalidUtf8=3,
// Interrupted=4, Unsupported=5, Other=6).
func (g *generator) emitStreamIORuntime() {
	g.emitOpenReaderArm()
	g.emitOpenWriterArm()
	g.emitOpenAppenderArm()
	g.emitReaderReadLineArm()
	g.emitReaderReadChunkArm()
	g.emitCloseMethodArm("__method_Reader_close")
	g.emitCloseMethodArm("__method_Writer_close")
	g.emitWriterWriteArm()
}

// emitOpenReaderArm — open(path, O_RDONLY=0). Wraps the
// resulting fd in a 4-byte Reader struct and returns
// Result[Reader, IoError].
func (g *generator) emitOpenReaderArm() {
	g.emitOpenArm("__lang_open_reader", 0, 0)
}

// emitOpenWriterArm — open(path, O_WRONLY|O_CREAT|O_TRUNC=0x241, 0644).
func (g *generator) emitOpenWriterArm() {
	g.emitOpenArm("__lang_open_writer", 0x241, 0644)
}

// emitOpenAppenderArm — open(path, O_WRONLY|O_CREAT|O_APPEND=0x441, 0644).
func (g *generator) emitOpenAppenderArm() {
	g.emitOpenArm("__lang_open_appender", 0x441, 0644)
}

// emitOpenArm is the shared body of the three constructors.
// `flags` is the open(2) flags; `mode` is the file-creation
// mode (used only when O_CREAT is set).
func (g *generator) emitOpenArm(name string, flags, mode int) {
	g.line("")
	g.line(fmt.Sprintf(".global %s", name))
	g.line(fmt.Sprintf(".type %s, %%function", name))
	g.label(name)
	g.emit("push {r4, r5, lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment (12 + 4 = 16)
	g.emit("mov r4, r0")     // r4 = path

	// open(path, flags, mode)
	g.emit("mov r1, #%d", flags)
	g.emit("mov r2, #%d", mode)
	g.emitSyscall(sysOpen)
	g.emit("cmp r0, #0")
	g.emit("bge .L%s_ok", name)
	g.emit("rsb r0, r0, #0")
	g.emit("mov r1, r4")
	g.emit("bl __build_io_error")
	g.emit("mov r5, r0")
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1") // Result.Err = tag 1
	g.emit("str r1, [r0]")
	g.emit("str r5, [r0, #4]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.label(fmt.Sprintf(".L%s_ok", name))
	// Allocate Reader/Writer struct: 4 bytes [fd].
	g.emit("mov r5, r0") // r5 = fd
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("str r5, [r0]")
	g.emit("mov r5, r0") // r5 = struct ptr
	// Build Ok(struct).
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r5, [r0, #4]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.line(fmt.Sprintf(".size %s, .-%s", name, name))
}

// emitReaderReadLineArm — Reader.read_line. Read one byte at
// a time via read(2) into a stack scratch byte, accumulating
// into a fixed .bss buffer (same one __lang_read_line uses
// for stdin), then materialise a length-prefixed string.
// Returns Option[string]; None on EOF before any byte was
// read.
func (g *generator) emitReaderReadLineArm() {
	g.line("")
	g.line(".global __method_Reader_read_line")
	g.line(".type __method_Reader_read_line, %function")
	g.label("__method_Reader_read_line")
	g.emit("push {r4, r5, r6, r7, lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	g.emit("ldr r4, [r0]")   // r4 = fd
	g.emit("ldr r5, =__lang_read_line_buf")
	g.emit("mov r6, #0") // r6 = bytes accumulated
	g.label(".Lrlm_loop")
	g.emit("cmp r6, #4096")
	g.emit("bge .Lrlm_done")
	g.emit("mov r0, r4")
	g.emit("add r1, r5, r6")
	g.emit("mov r2, #1")
	g.emitSyscall(sysRead)
	g.emit("cmp r0, #1")
	g.emit("blt .Lrlm_done")
	g.emit("add r7, r5, r6")
	g.emit("ldrb r7, [r7]")
	g.emit("add r6, r6, #1")
	g.emit("cmp r7, #10")
	g.emit("beq .Lrlm_done")
	g.emit("b .Lrlm_loop")
	g.label(".Lrlm_done")
	// EOF on first byte → None.
	g.emit("cmp r6, #0")
	g.emit("bne .Lrlm_some")
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, r6, r7, lr}")
	g.emit("bx lr")
	g.label(".Lrlm_some")
	// Allocate string: 4 + r6 + 1 (NUL).
	g.emit("add r0, r6, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r7, r0, #4")
	g.emit("str r6, [r7, #-4]")
	g.emit("mov r0, r7")
	g.emit("mov r1, r5")
	g.emit("mov r2, r6")
	g.emit("bl __lang_memcpy")
	g.emit("add r0, r7, r6")
	g.emit("mov r1, #0")
	g.emit("strb r1, [r0]")
	// Some(string).
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r7, [r0, #4]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, r6, r7, lr}")
	g.emit("bx lr")
	g.line(".size __method_Reader_read_line, .-__method_Reader_read_line")
}

// emitReaderReadChunkArm — Reader.read_chunk(size). Single
// read(2) into a heap-allocated buffer; trim length prefix
// to actual bytes read; return Some(string) or None.
func (g *generator) emitReaderReadChunkArm() {
	g.line("")
	g.line(".global __method_Reader_read_chunk")
	g.line(".type __method_Reader_read_chunk, %function")
	g.label("__method_Reader_read_chunk")
	g.emit("push {r4, r5, r6, lr}")
	g.emit("ldr r4, [r0]") // r4 = fd
	g.emit("mov r5, r1")   // r5 = size
	// Allocate `4 + size` for prefix + data.
	g.emit("add r0, r5, #4")
	g.emit("bl __lang_alloc")
	g.emit("add r6, r0, #4") // r6 = data ptr
	// read(fd, data, size)
	g.emit("mov r0, r4")
	g.emit("mov r1, r6")
	g.emit("mov r2, r5")
	g.emitSyscall(sysRead)
	g.emit("cmp r0, #0")
	g.emit("bgt .Lrcm_some")
	// EOF → None.
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")
	g.label(".Lrcm_some")
	g.emit("str r0, [r6, #-4]") // length prefix = actual bytes read
	// Some(data).
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r6, [r0, #4]")
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")
	g.line(".size __method_Reader_read_chunk, .-__method_Reader_read_chunk")
}

// emitCloseMethodArm — both Reader.close and Writer.close
// share this shape: close(fd) → Option[IoError].
func (g *generator) emitCloseMethodArm(name string) {
	g.line("")
	g.line(fmt.Sprintf(".global %s", name))
	g.line(fmt.Sprintf(".type %s, %%function", name))
	g.label(name)
	g.emit("push {r4, lr}")
	g.emit("ldr r0, [r0]") // r0 = fd
	g.emitSyscall(sysClose)
	g.emit("cmp r0, #0")
	g.emit("blt .L%s_err", name)
	// None.
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("pop {r4, lr}")
	g.emit("bx lr")
	g.label(fmt.Sprintf(".L%s_err", name))
	g.emit("rsb r0, r0, #0")
	g.emit("ldr r1, =.Lioe_msg")
	g.emit("bl __build_io_error")
	g.emit("mov r4, r0")
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r4, [r0, #4]")
	g.emit("pop {r4, lr}")
	g.emit("bx lr")
	g.line(fmt.Sprintf(".size %s, .-%s", name, name))
}

// emitWriterWriteArm — Writer.write(s). Single write(2)
// of the entire string; returns Option[IoError].
func (g *generator) emitWriterWriteArm() {
	g.line("")
	g.line(".global __method_Writer_write")
	g.line(".type __method_Writer_write, %function")
	g.label("__method_Writer_write")
	g.emit("push {r4, lr}")
	g.emit("sub sp, sp, #4") // alignment
	g.emit("ldr r2, [r1, #-4]")
	g.emit("ldr r0, [r0]") // r0 = fd
	// r1 = string data ptr (already in r1 from arg)
	// r2 = length (loaded above)
	g.emitSyscall(sysWrite)
	g.emit("cmp r0, #0")
	g.emit("blt .Lwwm_err")
	// None.
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, lr}")
	g.emit("bx lr")
	g.label(".Lwwm_err")
	g.emit("rsb r0, r0, #0")
	g.emit("ldr r1, =.Lioe_msg")
	g.emit("bl __build_io_error")
	g.emit("mov r4, r0")
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r4, [r0, #4]")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, lr}")
	g.emit("bx lr")
	g.line(".size __method_Writer_write, .-__method_Writer_write")
}

// emitStdStreamRuntime emits the trivial __lang_stdin /
// __lang_stdout / __lang_stderr constructors. Each allocates
// a 4-byte Reader / Writer struct around fd 0 / 1 / 2.
func (g *generator) emitStdStreamRuntime() {
	g.emitStdStreamArm("__lang_stdin", 0)
	g.emitStdStreamArm("__lang_stdout", 1)
	g.emitStdStreamArm("__lang_stderr", 2)
}

func (g *generator) emitStdStreamArm(name string, fd int) {
	g.line("")
	g.line(fmt.Sprintf(".global %s", name))
	g.line(fmt.Sprintf(".type %s, %%function", name))
	g.label(name)
	g.emit("push {lr}")
	g.emit("sub sp, sp, #4") // alignment
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #%d", fd)
	g.emit("str r1, [r0]")
	g.emit("add sp, sp, #4")
	g.emit("pop {lr}")
	g.emit("bx lr")
	g.line(fmt.Sprintf(".size %s, .-%s", name, name))
}

// internString returns a unique .rodata label for s, allocating a new one
// the first time we see this exact string and reusing it on repeats.
func (g *generator) internString(s string) string {
	if lbl, ok := g.stringLabel[s]; ok {
		return lbl
	}
	lbl := fmt.Sprintf(".LStr_%d", len(g.stringOrder))
	g.stringLabel[s] = lbl
	g.stringOrder = append(g.stringOrder, s)
	return lbl
}

// internLineBuffer returns a label for `s + "\n"` in .rodata,
// the buffer the `print(literal)` and `eprint(literal)` folds
// write in a single inline `write(2)` syscall. Plain bytes —
// no length prefix, no trailing NUL — so the precomputed
// length sent to the kernel is exactly `len(s) + 1`. print
// and eprint share the pool because both want identical
// `data + "\n"` bytes.
func (g *generator) internLineBuffer(s string) string {
	if lbl, ok := g.lineBufferLabel[s]; ok {
		return lbl
	}
	if g.lineBufferLabel == nil {
		g.lineBufferLabel = map[string]string{}
	}
	lbl := fmt.Sprintf(".LLineBuf_%d", len(g.lineBufferOrder))
	g.lineBufferLabel[s] = lbl
	g.lineBufferOrder = append(g.lineBufferOrder, s)
	return lbl
}

// escapeForGAS wraps s in double quotes and emits each byte either as
// itself (printable ASCII apart from " and \), as a recognised C-style
// escape, or as a three-digit octal escape. The result is suitable as
// the operand of `.asciz`.
// arm32StateInitDirective renders the GAS data directive for a
// state-block var's pre-baked initial value. Scalar widths
// supported on arm32 today: i32 / u32 / f32 / boolean (one
// `.4byte`), i8 / u8 (one `.byte`), i16 / u16 (one `.2byte`).
// 8-byte widths (i64 / u64 / f64) emit two `.4byte`s in
// little-endian order so the storage exists; arithmetic on
// those types still errors at the op site since arm32 hasn't
// shipped i64 codegen yet.
//
// Non-literal initialisers (`"hello, " + "world"`, `1 + 2`)
// can't be evaluated at link time, so they get a zero / null
// placeholder here and the synthesised `__state_init` start
// function (called from `_start` before `main`) overwrites
// the slot with the computed value at runtime.
func arm32StateInitDirective(t ast.Type, init ast.Expr) string {
	if !isArm32StateInitLiteral(init) {
		return arm32StateZeroDirective(t)
	}
	v := evalArm32StateInit(init)
	switch typ := t.(type) {
	case ast.NumberType:
		switch typ.NormalWidth() {
		case 8:
			return fmt.Sprintf(".byte %d", int8(v))
		case 16:
			return fmt.Sprintf(".2byte %d", int16(v))
		case 64:
			return fmt.Sprintf(".4byte %d\n\t.4byte %d",
				uint32(v), uint32(v>>32))
		}
		return fmt.Sprintf(".4byte %d", int32(v))
	case ast.FloatType:
		// Bit-reinterpret the float as i32 / i64 so the linker
		// places the right byte pattern. GAS's `.float` /
		// `.double` directives are equivalent but reinterpreting
		// keeps the codegen path uniform with the integer case.
		f := evalArm32StateFloat(init)
		if typ.Width == 64 {
			bits := math.Float64bits(f)
			return fmt.Sprintf(".4byte 0x%08x\n\t.4byte 0x%08x",
				uint32(bits), uint32(bits>>32))
		}
		return fmt.Sprintf(".4byte 0x%08x", math.Float32bits(float32(f)))
	case ast.BoolType:
		if v != 0 {
			return ".4byte 1"
		}
		return ".4byte 0"
	}
	return ".4byte 0"
}

// isArm32StateInitLiteral matches the shapes that
// arm32StateInitDirective can pre-bake at link time: scalar
// literal AST nodes plus the `Unary{"-", lit}` shape the
// parser produces for negative numbers. Anything else (string
// concat, arithmetic) needs runtime init via `__state_init`.
func isArm32StateInitLiteral(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit:
		return true
	case *ast.Unary:
		if v.Op == "-" {
			return isArm32StateInitLiteral(v.Operand)
		}
	}
	return false
}

// arm32StateZeroDirective returns the width-correct zero /
// null placeholder for a state var whose runtime-init expr
// will overwrite it before user code runs. Strings go to
// `.4byte 0` (null pointer); the runtime init's __lang_alloc
// + str_concat pipeline writes the real pointer.
func arm32StateZeroDirective(t ast.Type) string {
	switch typ := t.(type) {
	case ast.NumberType:
		switch typ.NormalWidth() {
		case 8:
			return ".byte 0"
		case 16:
			return ".2byte 0"
		case 64:
			return ".4byte 0\n\t.4byte 0"
		}
		return ".4byte 0"
	case ast.FloatType:
		if typ.Width == 64 {
			return ".4byte 0\n\t.4byte 0"
		}
		return ".4byte 0"
	}
	return ".4byte 0"
}

// evalArm32StateInit reduces a literal initialiser (NumberLit /
// BoolLit, plus `Unary{"-", lit}`) to its int64 value. Only the
// shapes the checker accepts reach this code; FloatLit follows
// a separate float path.
func evalArm32StateInit(e ast.Expr) int64 {
	switch v := e.(type) {
	case *ast.NumberLit:
		return v.Value
	case *ast.BoolLit:
		if v.Value {
			return 1
		}
		return 0
	case *ast.Unary:
		if v.Op == "-" {
			return -evalArm32StateInit(v.Operand)
		}
	}
	return 0
}

// evalArm32StateFloat is the FloatLit counterpart of
// evalArm32StateInit. Handles the leading-`-` shape that the
// parser produces for `-1.5f64`.
func evalArm32StateFloat(e ast.Expr) float64 {
	switch v := e.(type) {
	case *ast.FloatLit:
		return v.Value
	case *ast.NumberLit:
		return float64(v.Value)
	case *ast.Unary:
		if v.Op == "-" {
			return -evalArm32StateFloat(v.Operand)
		}
	}
	return 0
}

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
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\%03o`, c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------- driver helpers ----------

func (g *generator) line(s string) { g.out.WriteString(s); g.out.WriteByte('\n') }

// cfi emits a .cfi_* directive only when debug info is on
// (signalled by a non-empty SourceFile in Options). The
// directives generate `.eh_frame` for stack unwinding — useful
// for `gdb` and `addr2line` but dead weight in release builds,
// where they'd add ~50 bytes per function with no benefit
// (the nostdlib runtime has no unwinder).
func (g *generator) cfi(format string, args ...any) {
	if g.srcFile == "" {
		return
	}
	g.emit(format, args...)
}

func (g *generator) emit(format string, args ...any) {
	fmt.Fprintf(&g.out, "\t"+format+"\n", args...)
}
func (g *generator) label(name string) {
	g.out.WriteString(name)
	g.out.WriteString(":\n")
}
func (g *generator) freshLabel(stem string) string {
	g.labelN++
	return fmt.Sprintf(".L%s_%d", stem, g.labelN)
}

