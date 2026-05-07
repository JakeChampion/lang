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
	ir.Fold(ip)
	ir.TailCallOptimize(ip)

	g := &generator{
		info:        info,
		stringLabel: map[string]string{},
		srcFile:     opts.SourceFile,
	}
	g.line(`.arch armv7-a`)
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
	if g.usesAlloc {
		g.emitAllocRuntime()
	}
	if len(g.stringOrder) > 0 {
		g.line("")
		g.line(`.section .rodata`)
		for _, s := range g.stringOrder {
			g.line(`.align 2`)
			g.line(fmt.Sprintf("\t.4byte %d", len(s)))
			g.label(g.stringLabel[s])
			g.line("\t.asciz " + escapeForGAS(s))
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
	numScratch := int(irFn.NumScratch)
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
