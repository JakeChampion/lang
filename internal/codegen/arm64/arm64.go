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
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Linux arm64 syscall numbers from the asm-generic table.
// Only what the runtime needs at this stage.
const (
	sysExitGroup = 94
)

// regArgs is the AAPCS64 register-argument count: args 0..7
// arrive in x0..x7. Anything beyond that goes through the
// caller's stack frame. arm32 has 4 register-arg slots; arm64
// gives us 8 — enough to keep most user functions register-
// only.
const regArgs = 8

// Options tunes the emit. Currently empty.
type Options struct{}

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
	g := &generator{info: info, stringLabel: map[string]string{}}
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
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	g.emitDataSections()
	g.line(`.section .note.GNU-stack,"",%progbits`)
	return g.out.String(), nil
}

// emitDataSections writes `.rodata` (interned string literals)
// and `.bss` (the bump-allocator cursor + heap-end sentinel).
// All entries are gated on usage so unused programs pay
// nothing — `.bss` is omitted entirely when the allocator
// isn't pulled in.
func (g *generator) emitDataSections() {
	g.line("")
	g.line(`.section .rodata`)
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
	if g.usesAlloc {
		g.line("")
		g.line(`.section .bss`)
		g.line(`.align 3`) // 8-byte alignment for the cursor pair
		g.label("__lang_heap_ptr")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_heap_end")
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
	g.line(".type __lang_alloc, %function")
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
	g.emit("adrp x1, __lang_heap_ptr")
	g.emit("add x1, x1, :lo12:__lang_heap_ptr")
	g.emit("ldr x2, [x1]")
	g.emit("cbnz x2, .Lalloc_have_heap")
	// mmap(NULL, 64 MiB, RW, PRIVATE|ANON, -1, 0)
	g.emit("mov x9, x0")        // save size across the syscall
	g.emit("mov x0, #0")        // addr = NULL
	g.emit("ldr x1, =%d", heapBytes) // length = 64 MiB
	g.emit("mov x2, #3")        // PROT_READ | PROT_WRITE
	g.emit("mov x3, #0x22")     // MAP_PRIVATE | MAP_ANONYMOUS
	g.emit("mov x4, #-1")
	g.emit("mov x5, #0")
	g.emit("mov x8, #222")      // sys_mmap
	g.emit("svc #0")
	// On failure mmap returns -errno (negative). Trap.
	g.emit("cmn x0, #0")
	g.emit("blt .Lalloc_oom")
	g.emit("mov x10, x0")       // x10 = base
	g.emit("adrp x1, __lang_heap_ptr")
	g.emit("add x1, x1, :lo12:__lang_heap_ptr")
	g.emit("str x10, [x1]")
	g.emit("adrp x2, __lang_heap_end")
	g.emit("add x2, x2, :lo12:__lang_heap_end")
	g.emit("ldr x3, =%d", heapBytes)
	g.emit("add x3, x10, x3")
	g.emit("str x3, [x2]")
	g.emit("mov x0, x9")        // restore size
	g.label(".Lalloc_have_heap")
	// Bump the cursor: ptr = heap_ptr; heap_ptr += size; return ptr.
	g.emit("adrp x1, __lang_heap_ptr")
	g.emit("add x1, x1, :lo12:__lang_heap_ptr")
	g.emit("ldr x2, [x1]")      // x2 = current ptr
	g.emit("add x3, x2, x0")    // x3 = wanted top
	g.emit("adrp x4, __lang_heap_end")
	g.emit("add x4, x4, :lo12:__lang_heap_end")
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
	g.emit("mov x8, #94")
	g.emit("svc #0")
	g.line(".size __lang_alloc, .-__lang_alloc")
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
	g.line(".type __lang_memcpy, %function")
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
	g.line(".size __lang_memcpy, .-__lang_memcpy")
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
	g.line(".type __lang_strcat, %function")
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
	g.line(".size __lang_strcat, .-__lang_strcat")
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

// emitStartRuntime writes `_start`, the binary's entry under
// `-nostdlib` on Linux arm64. The kernel hands us a 16-aligned
// sp at process entry; we trust it (no explicit re-alignment
// — `and sp, sp, #imm` is rejected by the assembler since
// AArch64 logical-immediate AND can't take SP as a destination).
// args() / env() aren't wired yet, so the prologue is minimal:
// branch to main, then exit_group with main's return value.
func (g *generator) emitStartRuntime() {
	g.line("")
	g.line(".global _start")
	g.line(".type _start, %function")
	g.label("_start")
	g.emit("bl main")
	// exit_group(retval). x0 holds main's return value.
	g.emit("mov x8, #%d", sysExitGroup)
	g.emit("svc #0")
	g.line(".size _start, .-_start")
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
	g.line(fmt.Sprintf(".type %s, %%function", fn.Name))
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
	// First restore sp past the locals region, then ldp the
	// saved fp/lr pair while popping sp by 16 (the
	// post-increment form mirrors the prologue's `stp ... ,
	// #-16!`). Splitting the restore lets us keep the locals'
	// `[x29, #-N]` offsets fixed throughout the body.
	g.label(retLabel)
	if localsSize > 0 {
		g.emit("add sp, sp, #%d", localsSize)
	}
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.line(fmt.Sprintf(".size %s, .-%s", fn.Name, fn.Name))
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
		g.emit("adrp x0, %s", lbl)
		g.emit("add x0, x0, :lo12:%s", lbl)
		g.push()

	case ir.OpReturn:
		g.pop()
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
