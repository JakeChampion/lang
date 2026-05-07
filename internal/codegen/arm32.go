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
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
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
	return EmitFromIR(prog, info, opts)
}

type generator struct {
	out         strings.Builder
	info        *checker.Info
	labelN      int
	stringLabel map[string]string // value -> label
	stringOrder []string          // insertion order so output is deterministic
	usesStrcat  bool              // true if the program needs the strcat helper
	usesAlloc   bool              // true if the program needs the alloc helper (any heap-backed array / struct / closure)
	usesArgs      bool            // true if the program calls args() — pulls in the runtime helper + main argc/argv save
	usesWrite     bool            // true if the program calls write() — pulls in __lang_write
	usesEprint    bool            // true if the program calls eprint() — pulls in __lang_eprint and the newline byte
	usesReadLine  bool            // true if the program calls read_line() — pulls in __lang_read_line and the .bss buffer
	usesEnv       bool            // true if the program calls env() — pulls in __lang_env shim
	usesReadFile  bool            // true if the program calls read_file() — pulls in __lang_read_file + __build_io_error
	usesWriteFile bool            // true if the program calls write_file() — pulls in __lang_write_file + __build_io_error
	srcFile     string            // non-empty enables DWARF .file/.loc directives
}

// emitAllocRuntime emits a tiny `__lang_alloc(size)` helper that
// forwards to libc `malloc`. The wrapper exists so arrays / structs
// / closures all share one named entry point — both the AST-walking
// emitter and the upcoming IR-driven backend bottom out here, which
// matches the WASM backend's `$__lang_alloc` shape and gives the
// language a single chokepoint if it ever grows a real GC.
//
// The helper preserves the standard prologue/epilogue for stack
// alignment but doesn't otherwise touch r0, so the result stays in
// place across the libc call.
func (g *generator) emitAllocRuntime() {
	g.line("")
	g.line(".global __lang_alloc")
	g.line(".type __lang_alloc, %function")
	g.label("__lang_alloc")
	g.emit("push {lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	g.emit("bl malloc")
	g.emit("add sp, sp, #4")
	g.emit("pop {lr}")
	g.emit("bx lr")
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
	g.emit("bl memcpy")
	g.emit("add r0, r4, r6") // dst = result + la
	g.emit("mov r1, r5")     // src = b
	g.emit("add r2, r7, #1") // n = lb + 1 (include NUL)
	g.emit("bl memcpy")
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
	g.emit("bl strlen")
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
	g.emit("bl memcpy")

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
// the language-level `write(s)` into a libc `write(1, s, len)`
// syscall. The string's length lives at `s - 4` so we read it
// without scanning. The helper preserves stack alignment for the
// call but doesn't touch any callee-saved register the caller
// expects to keep — it pushes only `lr`.
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".global __lang_write")
	g.line(".type __lang_write, %function")
	g.label("__lang_write")
	g.emit("push {lr}")
	g.emit("sub sp, sp, #4")    // 8-byte alignment
	g.emit("ldr r2, [r0, #-4]") // r2 = length
	g.emit("mov r1, r0")        // r1 = buf
	g.emit("mov r0, #1")        // r0 = fd (stdout)
	g.emit("bl write")
	g.emit("add sp, sp, #4")
	g.emit("pop {lr}")
	g.emit("bx lr")
	g.line(".size __lang_write, .-__lang_write")
}

// emitEprintRuntime emits `__lang_eprint`, the stderr counterpart
// to libc `puts`. It performs two libc `write` syscalls: one for
// the user's string, one for a single-byte newline buffer interned
// at `.LLangNewline`. We can't just call `puts` here because puts
// always writes to stdout; we want fd=2 throughout.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".global __lang_eprint")
	g.line(".type __lang_eprint, %function")
	g.label("__lang_eprint")
	g.emit("push {lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	// First call: write(2, s, len(s))
	g.emit("ldr r2, [r0, #-4]")
	g.emit("mov r1, r0")
	g.emit("mov r0, #2")
	g.emit("bl write")
	// Second call: write(2, .LLangNewline, 1)
	g.emit("ldr r1, =.LLangNewline")
	g.emit("mov r2, #1")
	g.emit("mov r0, #2")
	g.emit("bl write")
	g.emit("add sp, sp, #4")
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
	g.emit("bl read")
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
	g.emit("bl memcpy")
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

// emitEnvRuntime emits `__lang_env`, the getenv shim. Returns an
// `Option[string]` heap object: `Some(value)` for a present key
// (the C-string is copied into a fresh length-prefixed lang
// string before being wrapped), `None` for a missing key. The
// lang string passed as `name` already has a trailing NUL byte
// past its data (the strcat runtime preserves that), so we can
// hand its data pointer to libc getenv directly without copying.
//
// Tag layout matches `__lang_read_line` and the WASM helper:
// 0 = Some, 1 = None. Some is 8 bytes [tag, str_ptr];
// None is 4 bytes [tag].
func (g *generator) emitEnvRuntime() {
	g.line("")
	g.line(".global __lang_env")
	g.line(".type __lang_env, %function")
	g.label("__lang_env")
	g.emit("push {r4, r5, lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	g.emit("bl getenv")
	g.emit("cmp r0, #0")
	g.emit("beq .Lenv_missing")
	// r4 = char*, r5 = strlen(r4)
	g.emit("mov r4, r0")
	g.emit("bl strlen")
	g.emit("mov r5, r0")
	// Allocate strlen + 5 (4 prefix + N data + trailing NUL).
	g.emit("add r0, r5, #5")
	g.emit("bl __lang_alloc")
	g.emit("add r0, r0, #4")    // r0 = string data ptr
	g.emit("str r5, [r0, #-4]") // length prefix
	// memcpy(str_data, char_star, strlen + 1) — include NUL.
	g.emit("push {r0}")
	g.emit("sub sp, sp, #4") // realign after push
	g.emit("mov r1, r4")
	g.emit("add r2, r5, #1")
	g.emit("bl memcpy")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4}") // r4 = string data ptr (recover for wrap)
	// Wrap as Some(string) — alloc 8 bytes, store [tag=0, str_ptr].
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")     // tag = 0 (Some)
	g.emit("str r4, [r0, #4]") // payload = string data ptr
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.label(".Lenv_missing")
	// Allocate None — 4 bytes for the tag alone.
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]") // tag = 1 (None)
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, lr}")
	g.emit("bx lr")
	g.line(".size __lang_env, .-__lang_env")
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

// emitReadFileRuntime — see emitFileIORuntime comment. The
// arm32 strategy uses lseek to discover file size up front,
// allocates a single length-prefixed buffer of the exact
// size, and reads in a loop that handles partial returns by
// re-issuing read(2). This keeps the result contiguous
// without a chunk-stitching pass — libc malloc allocates
// each block separately, so the wasm-side "bump-anchor +
// un-bump tail" trick can't apply here.
func (g *generator) emitReadFileRuntime() {
	g.line("")
	g.line(".global __lang_read_file")
	g.line(".type __lang_read_file, %function")
	g.label("__lang_read_file")
	g.emit("push {r4, r5, r6, r7, r8, lr}")
	g.emit("mov r4, r0") // r4 = path

	// open(path, O_RDONLY=0)
	g.emit("mov r1, #0")
	g.emit("bl open")
	g.emit("cmp r0, #0")
	g.emit("bge .Lrf_opened")
	g.emit("bl __errno_location")
	g.emit("ldr r0, [r0]")
	g.emit("mov r1, r4")
	g.emit("bl __build_io_error")
	g.emit("mov r5, r0")
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("str r5, [r0, #4]")
	g.emit("pop {r4, r5, r6, r7, r8, lr}")
	g.emit("bx lr")

	g.label(".Lrf_opened")
	g.emit("mov r5, r0") // r5 = fd

	// size = lseek(fd, 0, SEEK_END=2); rewind via lseek to 0.
	g.emit("mov r0, r5")
	g.emit("mov r1, #0")
	g.emit("mov r2, #2")
	g.emit("bl lseek")
	g.emit("mov r6, r0") // r6 = file size
	g.emit("mov r0, r5")
	g.emit("mov r1, #0")
	g.emit("mov r2, #0")
	g.emit("bl lseek")

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
	g.emit("bl read")
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
	g.emit("bl close")

	// Build Ok(r7).
	g.emit("mov r0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #0")
	g.emit("str r1, [r0]")
	g.emit("str r7, [r0, #4]")
	g.emit("pop {r4, r5, r6, r7, r8, lr}")
	g.emit("bx lr")
	g.line(".size __lang_read_file, .-__lang_read_file")
}

// emitWriteFileRuntime — see emitFileIORuntime comment.
// One open(2) + write(2) + close(2). Returns Option[IoError].
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
	g.emit("bl open")
	g.emit("cmp r0, #0")
	g.emit("bge .Lwf_opened")
	g.emit("bl __errno_location")
	g.emit("ldr r0, [r0]")
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
	g.emit("bl write")
	g.emit("cmp r0, #0")
	g.emit("blt .Lwf_write_err")

	// close(fd) and return None.
	g.emit("mov r0, r6")
	g.emit("bl close")
	g.emit("mov r0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov r1, #1")
	g.emit("str r1, [r0]")
	g.emit("pop {r4, r5, r6, lr}")
	g.emit("bx lr")

	g.label(".Lwf_write_err")
	// Capture errno BEFORE close (which would overwrite it).
	g.emit("bl __errno_location")
	g.emit("ldr r5, [r0]") // r5 = errno
	g.emit("mov r0, r6")
	g.emit("bl close")
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

// escapeForGAS wraps s in double quotes and emits each byte either as
// itself (printable ASCII apart from " and \), as a recognised C-style
// escape, or as a three-digit octal escape. The result is suitable as
// the operand of `.asciz`.
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

