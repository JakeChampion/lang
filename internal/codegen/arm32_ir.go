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
	}
	// Detect args() / write() / eprint() usage up-front. `usesArgs`
	// matters specifically because the prologue insertion in `main`
	// depends on it, and main is usually emitted first — without
	// this scan the side-effect set in emitCallDirect would arrive
	// too late. The other flags ride along for symmetry; emitting
	// the helpers ahead of OpCallDirect side-effects has no cost.
	for _, irFn := range ip.Funcs {
		for _, op := range irFn.Ops {
			if op.Kind != ir.OpCallDirect {
				continue
			}
			switch op.Str {
			case "args":
				g.usesArgs = true
				g.usesAlloc = true
			case "write":
				g.usesWrite = true
			case "eprint":
				g.usesEprint = true
			case "read_line":
				g.usesReadLine = true
				g.usesAlloc = true
			case "env":
				g.usesEnv = true
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
	// Enable VFPv2 so we can emit `vmov` / `vadd.f32` /
	// `vcmp.f32` etc. for the float ops the IR may carry.
	// The toolchain's float ABI doesn't matter at our level —
	// we keep float values flowing through general-purpose
	// registers as raw 32-bit bit patterns, only pulling them
	// into VFP s-registers for the actual arithmetic. That
	// keeps our calling convention identical to the int path
	// and means no -mfloat-abi mismatch with the host gcc /
	// linker.
	g.line(`.fpu vfpv2`)
	g.line(`.text`)
	if g.srcFile != "" {
		g.line(fmt.Sprintf(`.file 1 %q`, g.srcFile))
	}
	for i, fn := range prog.Funcs {
		if err := g.emitFunctionFromIR(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	if g.usesStrcat {
		g.usesAlloc = true
		g.emitStrcatRuntime()
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
	if g.usesAlloc {
		g.emitAllocRuntime()
	}
	if len(g.stringOrder) > 0 || g.usesEprint || g.usesReadFile || g.usesWriteFile || g.usesStreamIO {
		g.line("")
		g.line(`.section .rodata`)
		for _, s := range g.stringOrder {
			g.line(`.align 2`)
			g.line(fmt.Sprintf("\t.4byte %d", len(s)))
			g.label(g.stringLabel[s])
			g.line("\t.asciz " + escapeForGAS(s))
		}
		if g.usesEprint {
			// Single-byte newline buffer for `__lang_eprint`'s
			// second `write` call. Plain `.byte` (no length
			// prefix) — eprint passes the address directly to
			// the libc write syscall with count=1.
			g.label(".LLangNewline")
			g.line(`	.byte 10`)
		}
		if g.usesReadFile || g.usesWriteFile || g.usesStreamIO {
			// Length-prefixed lang string used by the
			// IoError.Other variant's "msg" payload. Layout
			// matches user strings: 4-byte little-endian
			// length followed by .asciz data, with the label
			// pointing at the data start.
			g.line(`.align 2`)
			g.line(`	.4byte 8`)
			g.label(".Lioe_msg")
			g.line(`	.asciz "io error"`)
		}
	}
	if g.usesArgs || g.usesReadLine || g.usesStreamIO {
		g.line("")
		g.line(`.section .bss`)
		if g.usesArgs {
			g.line(`.align 2`)
			g.label("__lang_argc")
			g.line(`	.word 0`)
			g.line(`.align 2`)
			g.label("__lang_argv")
			g.line(`	.word 0`)
			g.line(`.align 2`)
			g.label("__lang_args_cache")
			g.line(`	.word 0`)
		}
		if g.usesReadLine || g.usesStreamIO {
			// 4 KiB scratch buffer shared by the legacy
			// stdin reader (`__lang_read_line`) and the
			// streaming Reader.read_line method. Lines
			// longer than 4 KiB truncate at the boundary;
			// follow-ups can switch to a growing alloc when
			// users hit it.
			g.line(`.align 2`)
			g.label("__lang_read_line_buf")
			g.line(`	.space 4096`)
		}
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
	g.emit(".cfi_startproc")

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
		g.emit(".cfi_def_cfa_offset %d", pushBytes)
		for i := 0; i < numParams; i++ {
			g.emit(".cfi_offset r%d, %d", 4+i, -pushBytes+i*4)
		}
		g.emit(".cfi_offset fp, -8")
		g.emit(".cfi_offset lr, -4")
		g.emit("add fp, sp, #%d", 4*numParams)
		g.emit(".cfi_def_cfa_register fp")
		if off > 0 {
			g.emit("sub sp, sp, #%d", off)
		}
		for i := 0; i < numParams; i++ {
			g.emit("mov r%d, r%d", 4+i, i)
		}
	} else {
		g.emit("push {fp, lr}")
		g.emit(".cfi_def_cfa_offset 8")
		g.emit(".cfi_offset fp, -8")
		g.emit(".cfi_offset lr, -4")
		g.emit("mov fp, sp")
		g.emit(".cfi_def_cfa_register fp")
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

	// If the program calls `args()` anywhere, save the C runtime's
	// argc / argv (delivered in r0 / r1 to `main`) into globals so
	// the runtime helper can find them. main always has zero
	// declared params, so r0 / r1 are still live here.
	if g.usesArgs && fn.Name == "main" {
		g.emit("ldr r12, =__lang_argc")
		g.emit("str r0, [r12]")
		g.emit("ldr r12, =__lang_argv")
		g.emit("str r1, [r12]")
	}

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
	g.emit(".cfi_endproc")
	g.line(fmt.Sprintf(".size %s, .-%s", fn.Name, fn.Name))
	return nil
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
