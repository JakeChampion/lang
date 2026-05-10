// IR-driven ARM32 emitter, sitting alongside the AST-walking emitter
// in this same package while we cut over. EmitFromIR mirrors Emit's
// public shape but consumes a lowered ir.Program for every function
// body — the module preamble, .rodata layout, and runtime helpers
// stay the same as the AST path.
//
// The IR is a stack-machine; the ARM32 backend realises that stack as
// the actual call stack. Each "push i32" becomes `push {r0}`, each
// "pop i32" becomes `pop {r0}`, and the working value of every op
// lives in r0 between phases. The structured-CF ops (block / loop /
// if / br / br_if) are translated through a per-function scope stack
// of labels, with `br N` resolving to scope[len-1-N]'s branch target
// (the start label for `loop`, the end label for `block` / `if`).
//
// Tail-call optimisation is handled before emit time by ir.TailCallOptimize,
// which rewrites every `OpCallDirect <self>; OpReturn` pair into a
// parameter rebind plus a backward `OpBr` to a synthetic loop wrapping
// the body. From this emitter's point of view there's nothing special
// about a tail call — it's just a `br` to an outer loop.

package codegen

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
)

// EmitFromIR returns ARM32 assembly for prog by lowering through the
// IR, running TCO, and walking each function's ops. The output is
// peephole-optimised through the same pass that Emit uses.
func EmitFromIR(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	ip, err := ir.Lower(prog, info)
	if err != nil {
		return "", err
	}
	// Fold first so TCO sees its constant-folded body — a
	// hand-written `return f(N + 1)` for some literal N becomes
	// `return f(constant)` after Fold, which still tail-call-rewrites
	// cleanly. Order matters: TCO wraps the body in a loop and any
	// post-TCO Fold would have to know about that wrapper to stay
	// useful.
	ir.Inline(ip)
	ir.FuseTee(ip)
	// TCO runs BEFORE FlattenBranches: TCO recognises the
	// `OpCallDirect <self> ; OpReturn` adjacency, but flattening
	// would push the call into a typed-if arm and break that
	// pattern. Once TCO has rewritten the recursive shape, the
	// body's outer scope becomes the wrap loop and FlattenBranches
	// (which only fires at function-root depth) leaves the
	// recursive functions alone.
	ir.TailCallOptimize(ip)
	ir.FlattenBranches(ip)
	// PropagateCopies + ConstPropagate + Fold + ReduceStrength
	// expose new opportunities for each other; run them to a fixed
	// point so the cascade settles.
	ir.OptimizeCleanup(ip)
	ir.EliminateDeadCode(ip)

	g := &generator{
		info:        info,
		stringLabel: map[string]string{},
		srcFile:     opts.SourceFile,
		stateDecls:  prog.States,
	}
	// Whether the checker synthesised `__state_init` — emitted
	// in prog.Funcs only when at least one state var has a
	// non-literal init expression. _start branches into it
	// before calling main only when this is set.
	for _, fn := range prog.Funcs {
		if fn.Name == "__state_init" {
			g.hasStateInit = true
			break
		}
	}
	// Detect args() / write() / eprint() usage up-front. `usesArgs`
	// matters specifically because the prologue insertion in `main`
	// depends on it, and main is usually emitted first — without
	// this scan the side-effect set in emitCallDirect would arrive
	// too late. The other flags ride along for symmetry; emitting
	// the helpers ahead of OpCallDirect side-effects has no cost.
	for _, irFn := range ip.Funcs {
		for j, op := range irFn.Ops {
			if op.Kind != ir.OpCallDirect {
				continue
			}
			switch op.Str {
			case "args":
				g.usesArgs = true
				g.usesAlloc = true
			case "print":
				// Only pull in the runtime helper when there's
				// at least one *non-literal* call. Literal calls
				// fold to inline `write(2)` syscalls in
				// emitOpsFromIR's peephole, so a program where
				// every `print` is a literal drops `__lang_puts`
				// from the binary entirely.
				if !literalCallable(irFn.Ops, j) {
					g.usesPuts = true
				}
			case "putchar":
				g.usesPutchar = true
			case "write":
				if !literalCallable(irFn.Ops, j) {
					g.usesWrite = true
				}
			case "eprint":
				if !literalCallable(irFn.Ops, j) {
					g.usesEprint = true
				}
			case "read_line":
				g.usesReadLine = true
				g.usesAlloc = true
			case "env":
				g.usesEnv = true
				g.usesAlloc = true
			case "exit":
				g.usesExit = true
			case "arena_save", "arena_restore":
				g.usesArena = true
			case "random_bytes":
				g.usesRandomBytes = true
				g.usesAlloc = true
			case "tcp_listen", "tcp_accept", "tcp_recv", "tcp_send", "tcp_close":
				g.usesTcp = true
				g.usesAlloc = true
			case "read_file":
				g.usesReadFile = true
				g.usesAlloc = true
			case "write_file":
				g.usesWriteFile = true
				g.usesAlloc = true
			case "open_reader", "open_writer", "open_appender":
				g.usesStreamIO = true
				g.usesAlloc = true
			case "stdin", "stdout", "stderr":
				g.usesStdStreams = true
				g.usesAlloc = true
			}
			if strings.HasPrefix(op.Str, "__method_Reader_") ||
				strings.HasPrefix(op.Str, "__method_Writer_") {
				g.usesStreamIO = true
				g.usesAlloc = true
			}
		}
	}
	g.line(`.arch armv7-a`)
	// VFPv2 for float ops, idiv extension for `sdiv` / `mls`
	// (which we use directly in OpDivS / OpRemS instead of
	// calling __aeabi_idiv from libgcc). Both extensions are
	// available on every ARMv7-A core in qemu-arm and on real
	// Cortex-A15+ silicon, so this is portable in practice.
	g.line(`.fpu vfpv2`)
	g.line(`.arch_extension idiv`)
	g.line(`.text`)
	if g.srcFile != "" {
		g.line(fmt.Sprintf(`.file 1 %q`, g.srcFile))
	}
	// `_start` is always emitted: it's the binary's entry point
	// under `-nostdlib` (no libc, no glibc startfiles). _start
	// captures argc / argv / envp from the kernel stack into
	// .bss globals, initialises the bump heap, and calls main.
	g.emitStartRuntime()
	g.emitHeapInitRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunctionFromIR(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	// Always-on helpers: alloc + memcpy + strcmp anchor every
	// program's heap-/string-using path, so emit unconditionally.
	g.emitAllocRuntime()
	g.emitMemcpyRuntime()
	g.emitStrcmpRuntime()
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	if g.usesArgs || g.usesEnv {
		g.emitStrlenRuntime()
	}
	if g.usesPuts {
		g.emitPutsRuntime()
	}
	if g.usesPutchar {
		g.emitPutcharRuntime()
	}
	if g.usesArgs {
		g.emitArgsRuntime()
	}
	if g.usesWrite {
		g.emitWriteRuntime()
	}
	if g.usesEprint {
		g.emitEprintRuntime()
	}
	if g.usesReadLine {
		g.emitReadLineRuntime()
	}
	if g.usesEnv {
		g.emitEnvRuntime()
		g.emitMemcmpNRuntime()
	}
	if g.usesExit {
		g.emitExitRuntime()
	}
	if g.usesArena {
		g.emitArenaSaveRuntime()
		g.emitArenaRestoreRuntime()
	}
	if g.usesRandomBytes {
		g.emitRandomBytesRuntime()
	}
	if g.usesTcp {
		g.emitTcpListenRuntime()
		g.emitTcpAcceptRuntime()
		g.emitTcpRecvRuntime()
		g.emitTcpSendRuntime()
		g.emitTcpCloseRuntime()
	}
	if g.usesReadFile || g.usesWriteFile || g.usesStreamIO {
		g.emitFileIORuntime()
	}
	if g.usesStreamIO {
		g.emitStreamIORuntime()
	}
	if g.usesStdStreams {
		g.emitStdStreamRuntime()
	}
	needsNewline := g.usesEprint || g.usesPuts
	g.line("")
	g.line(`.section .rodata`)
	for _, s := range g.stringOrder {
		g.line(`.align 2`)
		g.line(fmt.Sprintf("\t.4byte %d", len(s)))
		g.label(g.stringLabel[s])
		g.line("\t.asciz " + escapeForGAS(s))
	}
	// Compile-time `print` / `eprint` line buffers:
	// `data + "\n"` with no prefix and no NUL. Each one is
	// the sole target of a single inline `write(fd, ptr,
	// len(data)+1)` syscall the literal-fold peephole
	// generated.
	for _, s := range g.lineBufferOrder {
		g.label(g.lineBufferLabel[s])
		g.line("\t.ascii " + escapeForGAS(s+"\n"))
	}
	if needsNewline {
		// Single-byte newline buffer used by the second
		// `write(2)` syscall in `__lang_puts` / `__lang_eprint`.
		// Plain `.byte` (no length prefix) — both helpers pass
		// the address directly to the kernel with count=1.
		g.label(".LLangNewline")
		g.line(`	.byte 10`)
	}
	if g.usesReadFile || g.usesWriteFile || g.usesStreamIO {
		// Length-prefixed lang string used by the IoError.Other
		// variant's "msg" payload. Layout matches user strings:
		// 4-byte little-endian length followed by .asciz data,
		// with the label pointing at the data start.
		g.line(`.align 2`)
		g.line(`	.4byte 8`)
		g.label(".Lioe_msg")
		g.line(`	.asciz "io error"`)
	}
	// state{}-block module-globals. Each `state { var NAME: T = LIT; }`
	// becomes a `.data` label with the literal pre-baked at link
	// time — no runtime init code needed (the loader maps the
	// section into memory with the values already in place).
	// OpLoadGlobal / OpStoreGlobal use the `state_<name>` label
	// as a `ldr =` operand, then dereference.
	if len(g.stateDecls) > 0 {
		g.line("")
		g.line(`.section .data`)
		for _, sd := range g.stateDecls {
			for _, v := range sd.Vars {
				g.line(`.align 2`)
				g.label("state_" + v.Name)
				g.line("\t" + arm32StateInitDirective(v.Type, v.Init))
			}
		}
	}

	g.line("")
	g.line(`.section .bss`)
	// argc / argv / envp are always saved by _start; emitting
	// the globals unconditionally keeps the runtime layout
	// uniform across all programs.
	g.line(`.align 2`)
	g.label("__lang_argc")
	g.line(`	.word 0`)
	g.line(`.align 2`)
	g.label("__lang_argv")
	g.line(`	.word 0`)
	g.line(`.align 2`)
	g.label("__lang_envp")
	g.line(`	.word 0`)
	// Bump heap cursor + limit, populated by __lang_heap_init
	// at process entry. Always emitted because every program
	// that allocates lives or dies by these two words.
	g.line(`.align 2`)
	g.label("__lang_heap_ptr")
	g.line(`	.word 0`)
	g.line(`.align 2`)
	g.label("__lang_heap_end")
	g.line(`	.word 0`)
	if g.usesArgs {
		g.line(`.align 2`)
		g.label("__lang_args_cache")
		g.line(`	.word 0`)
	}
	if g.usesReadLine || g.usesStreamIO {
		// 4 KiB scratch buffer shared by the legacy stdin
		// reader (`__lang_read_line`) and the streaming
		// Reader.read_line method. Lines longer than 4 KiB
		// truncate at the boundary; follow-ups can switch to
		// a growing alloc when users hit it.
		g.line(`.align 2`)
		g.label("__lang_read_line_buf")
		g.line(`	.space 4096`)
	}
	g.line("")
	g.line(`.section .note.GNU-stack,"",%progbits`)
	return peephole(g.out.String()), nil
}

// irScope is one entry in the per-function control-scope stack used
// by the IR walker to resolve OpBr / OpBrIf depths to assembly
// labels. brTarget is the label `br N` should jump to: the start
// label for loops, the end label for blocks and ifs.
type irScope struct {
	kind     ir.OpKind // OpBlock / OpLoop / OpIf
	brTarget string    // label `br` should branch to
	endLabel string    // emitted at OpEnd
	// If-only: elseLabel is the branch target the OpIf installed for
	// "condition was false". If no OpElse appears before OpEnd, we
	// emit elseLabel just before endLabel so the if's forward branch
	// has somewhere to land.
	elseLabel string
	hasElse   bool
}

// emitFunctionFromIR is the IR analogue of emitFunction. The outer
// shape (.global / .type / prologue / .cfi_* / epilogue / .size) is
// identical so existing tools recognise the ABI; only the body comes
// from IR ops rather than an AST walk.
func (g *generator) emitFunctionFromIR(fn *ast.FuncDecl, irFn *ir.Func) error {
	g.line("")
	g.line(fmt.Sprintf(".global %s", fn.Name))
	g.line(fmt.Sprintf(".type %s, %%function", fn.Name))
	g.label(fn.Name)
	g.cfi(".cfi_startproc")

	// Leaf-function check: pin params to callee-saved r4..r7 instead
	// of paying for a stack spill. Anything that emits `bl` (direct
	// or indirect calls plus the alloc / strcat / streq runtime
	// helpers) disqualifies the function.
	leaf := len(irFn.Params) >= 1 && len(irFn.Params) <= regArgs && !irHasCall(irFn.Ops)

	// slotOffset[i] is the fp-relative offset (in bytes) for IR slot i,
	// or 0 for leaf-pinned params (which use paramReg instead).
	numParams := len(irFn.Params)
	numLocals := len(irFn.Locals)
	numScratch := len(irFn.ScratchTypes)
	slotOffset := make([]int, numParams+numLocals+numScratch)
	paramReg := map[int]int{} // slot index → r4..r7
	off := 0
	if leaf {
		for i := range irFn.Params {
			paramReg[i] = 4 + i
		}
	} else {
		for i := range irFn.Params {
			off += 4
			slotOffset[i] = -off
		}
	}
	for i := 0; i < numLocals; i++ {
		off += 4
		slotOffset[numParams+i] = -off
	}
	for i := 0; i < numScratch; i++ {
		off += 4
		slotOffset[numParams+numLocals+i] = -off
	}
	if off%8 != 0 {
		off += 4
	}

	// Prologue.
	if leaf {
		regs := []string{}
		for i := 0; i < numParams; i++ {
			regs = append(regs, fmt.Sprintf("r%d", 4+i))
		}
		regs = append(regs, "fp", "lr")
		g.emit("push {%s}", strings.Join(regs, ", "))
		pushBytes := (numParams + 2) * 4
		g.cfi(".cfi_def_cfa_offset %d", pushBytes)
		for i := 0; i < numParams; i++ {
			g.cfi(".cfi_offset r%d, %d", 4+i, -pushBytes+i*4)
		}
		g.cfi(".cfi_offset fp, -8")
		g.cfi(".cfi_offset lr, -4")
		g.emit("add fp, sp, #%d", 4*numParams)
		g.cfi(".cfi_def_cfa_register fp")
		if off > 0 {
			g.emit("sub sp, sp, #%d", off)
		}
		for i := 0; i < numParams; i++ {
			g.emit("mov r%d, r%d", 4+i, i)
		}
	} else {
		g.emit("push {fp, lr}")
		g.cfi(".cfi_def_cfa_offset 8")
		g.cfi(".cfi_offset fp, -8")
		g.cfi(".cfi_offset lr, -4")
		g.emit("mov fp, sp")
		g.cfi(".cfi_def_cfa_register fp")
		if off > 0 {
			g.emit("sub sp, sp, #%d", off)
		}
		// Spill incoming params: r0..r3 directly, extras come from the
		// caller's stack at fp+8, fp+12, …
		for i := 0; i < numParams; i++ {
			if i < regArgs {
				g.emit("str r%d, [fp, #%d]", i, slotOffset[i])
			} else {
				callerOff := 8 + (i-regArgs)*4
				g.emit("ldr r12, [fp, #%d]", callerOff)
				g.emit("str r12, [fp, #%d]", slotOffset[i])
			}
		}
	}

	epilogue := g.freshLabel("epi_" + fn.Name)
	bodyLabel := g.freshLabel("body_" + fn.Name)
	g.label(bodyLabel)

	// (No per-main argc/argv save: `_start` does it once for
	// every program, into __lang_argc / __lang_argv globals.)

	// Walk the IR ops.
	if err := g.emitOpsFromIR(irFn, slotOffset, paramReg, epilogue); err != nil {
		return err
	}

	g.label(epilogue)
	if leaf {
		// Step sp back to where r4 was pushed (fp - 4*P), so the
		// pop list matches the prologue's push order. `mov sp, fp`
		// alone would leave sp pointing at the saved fp word, and
		// the pop would read r4 = saved_fp. Verified by qemu-arm
		// e2e tests which were exit=-1'ing before this fix.
		g.emit("sub sp, fp, #%d", 4*numParams)
		regs := []string{}
		for i := 0; i < numParams; i++ {
			regs = append(regs, fmt.Sprintf("r%d", 4+i))
		}
		regs = append(regs, "fp", "lr")
		g.emit("pop {%s}", strings.Join(regs, ", "))
	} else {
		g.emit("mov sp, fp")
		g.emit("pop {fp, lr}")
	}
	g.emit("bx lr")
	g.cfi(".cfi_endproc")
	g.line(fmt.Sprintf(".size %s, .-%s", fn.Name, fn.Name))
	// `.ltorg` flushes the literal pool the GAS assembler
	// accumulates as it sees `ldr rN, =LABEL` operands.
	// Without an explicit flush, GAS waits until end-of-input
	// and ends up with `=LABEL` references too far from their
	// `ldr` (the `pc + offset12` encoding is limited to ±4 KiB).
	// Emitting once per function caps the unflushed pool at the
	// function's size, which is well under the 4 KiB cap for
	// every function we generate today (the biggest user-
	// authored functions stay around a few hundred ops; the
	// runtime helpers in arm32_syscall.go already emit `.ltorg`
	// inline). Idempotent on small functions — the assembler
	// emits nothing when the pending pool is empty.
	g.line(".ltorg")
	return nil
}

// literalCallable reports whether the OpCallDirect at ops[j] is
// a `<builtin>(literal_string)` call that the print / write /
// eprint peephole in emitOpsFromIR will fold to an inline
// `write(2)` syscall — i.e. the immediately preceding op is an
// OpConstStr and the call has exactly one arg. Used by the
// up-front scan to decide whether to emit the runtime helper:
// if every call site at ops[j] is foldable, the helper is dead
// and we drop it from the binary.
func literalCallable(ops []ir.Op, j int) bool {
	return j > 0 &&
		ops[j-1].Kind == ir.OpConstStr &&
		ops[j].I32 == 1
}

// irHasCall reports whether ops contains any instruction that emits a
// `bl` (and so requires a non-leaf prologue / epilogue saving lr).
// This includes user-level calls plus the runtime helpers the heap-
// backed and string ops bottom out in.
func irHasCall(ops []ir.Op) bool {
	for _, op := range ops {
		switch op.Kind {
		case ir.OpCallDirect, ir.OpCallIndirect,
			ir.OpAlloc, ir.OpStrConcat, ir.OpStrEq,
			ir.OpMakeClosure:
			return true
		}
	}
	return false
}
