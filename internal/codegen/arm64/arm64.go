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
	g := &generator{info: info}
	g.line(`.arch armv8-a`)
	g.line(`.text`)
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	g.line(`.section .note.GNU-stack,"",%progbits`)
	return g.out.String(), nil
}

type generator struct {
	out       strings.Builder
	info      *checker.Info
	indent    int
	labelN    int
	current   *ast.FuncDecl
	currentIR *ir.Func
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

	case ir.OpReturn:
		g.pop()
		g.emit("b %s", retLabel)

	case ir.OpDrop:
		g.emit("add sp, sp, #16")

	// -------- arithmetic (i32) --------

	case ir.OpAdd:
		g.binPop()
		g.emit("add w0, w1, w0")
		g.push()
	case ir.OpSub:
		g.binPop()
		g.emit("sub w0, w1, w0")
		g.push()
	case ir.OpMul:
		g.binPop()
		g.emit("mul w0, w1, w0")
		g.push()
	case ir.OpDivS:
		// Signed division. ARMv8-A includes sdiv as a base-ISA
		// instruction (no divider extension required like on
		// armv7-a).
		g.binPop()
		g.emit("sdiv w0, w1, w0")
		g.push()
	case ir.OpRemS:
		// rem = lhs - (lhs / rhs) * rhs. arm64 has `msub` for
		// the multiply-subtract step (Rd = Ra - Rn * Rm).
		g.binPop()
		g.emit("sdiv w2, w1, w0") // w2 = lhs / rhs
		g.emit("msub w0, w2, w0, w1") // w0 = lhs - (q * rhs)
		g.push()
	case ir.OpAnd:
		g.binPop()
		g.emit("and w0, w1, w0")
		g.push()
	case ir.OpOr:
		g.binPop()
		g.emit("orr w0, w1, w0")
		g.push()
	case ir.OpXor:
		g.binPop()
		g.emit("eor w0, w1, w0")
		g.push()
	case ir.OpShl:
		g.binPop()
		g.emit("lsl w0, w1, w0")
		g.push()
	case ir.OpShrS:
		g.binPop()
		g.emit("asr w0, w1, w0")
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
		// `cbz x0, elseL` branches when x0 == 0 — the
		// arm64 fast-path equivalent of arm32's `cmp / beq`.
		// CBZ accepts a 19-bit signed offset (~±1 MiB) which
		// is plenty for any realistic function body.
		g.pop()
		elseL := g.freshLabel("ifElse")
		endL := g.freshLabel("ifEnd")
		g.emit("cbz x0, %s", elseL)
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
		g.emit("cbnz x0, %s", target)

	case ir.OpCallDirect:
		// AAPCS64: load args 0..n-1 from the operand stack into
		// x0..x{n-1} (rightmost-on-top, so we pop in reverse
		// order), then `bl target`. Result lands in x0; push it.
		argc := int(op.I32)
		if argc > regArgs {
			return fmt.Errorf("arm64: more than %d call args not yet supported (got %d for %q)", regArgs, argc, op.Str)
		}
		for i := argc - 1; i >= 0; i-- {
			g.emit("ldr x%d, [sp], #16", i)
		}
		g.emit("bl %s", op.Str)
		g.push()

	default:
		return fmt.Errorf("arm64: unsupported IR op %s", op.Kind)
	}
	return nil
}
