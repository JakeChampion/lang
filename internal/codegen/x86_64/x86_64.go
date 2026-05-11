// Package x86_64 emits System V AMD64 (Linux ELF) assembly
// from a checked + monomorphised lang program. Third native
// backend alongside arm64 and wasm; shares the IR layer with
// both and emits its own ISA + Linux x86-64 syscall wiring.
//
// First-cut scope (PR 1): scaffolding plus
// `function main(): i32 { return N; }`. Subsequent PRs layer
// in arithmetic + control flow + locals + direct calls
// (PR 2), strings + the alloc / memcpy / puts runtime
// (PR 3), composite types + arrays (PR 4), TCP + HTTP
// handler (PR 5), and the parked `ir.TailCallOptimize` pass
// (PR 6).
//
// ABI: System V AMD64 (Linux).
//   - Integer args:    rdi rsi rdx rcx r8 r9
//   - Syscall args:    rdi rsi rdx r10 r8 r9  (note r10, not rcx)
//   - Return:          rax (eax for i32; rax for i64 / pointer)
//   - Callee-save:     rbx rbp r12 r13 r14 r15
//   - Caller-save:     rax rcx rdx rsi rdi r8 r9 r10 r11
//   - Stack alignment: 16-byte at every call boundary
//   - Syscalls:        rax = number, `syscall` instruction
//
// Operand stack: simulated on the physical rsp via paired
// 16-byte slots (8 bytes value + 8 bytes pad). Matches the
// arm64 backend's 16-byte slot discipline; keeps rsp
// 16-aligned trivially without per-call padding. Cost is one
// extra 8 bytes of stack per push.
//
// Frame layout per function (mirroring arm64):
//
//   push rbp                    ; save caller's frame pointer
//   mov  rbp, rsp               ; rbp = saved-pair top
//   sub  rsp, <localsSize>      ; reserve locals
//   mov  [rbp-8],  rdi          ; spill register args
//   mov  [rbp-16], rsi          ; ...
//   ...
//
// Local slot `i` lives at `[rbp - 8*(i+1)]` — 8 bytes per slot,
// same encoding shape as arm64's `[x29, #-(i+1)*8]`.
//
// Syscalls: see Linux x86-64 asm-generic table —
//   read=0  write=1  close=3  mmap=9  socket=41  accept=43
//   bind=49 listen=50 exit_group=231
package x86_64

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Linux x86-64 syscall numbers — only what PR 1 (the exit
// path) needs. Each follow-up PR extends as it adds runtime
// helpers that need a new syscall.
const (
	sysExitGroup = 231
)

// Options tunes the emit. Currently empty; reserved for the
// future `Darwin bool` flip when (if) a Mach-O variant
// arrives. Kept as a struct rather than a sentinel value so
// adding fields doesn't change call sites.
type Options struct{}

// Emit produces assembly text for prog targeting x86-64 Linux.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithOptions runs treeshake, lowers to IR with ptrW=8,
// then walks each surviving function emitting GAS-flavoured
// AT&T assembly... no wait, Intel syntax. We deliberately use
// Intel syntax (`.intel_syntax noprefix`) for readability —
// the rest of the codebase's runtime asm is comparable to the
// arm64 style (mnemonic dst, src), and Intel x86 syntax
// matches that ordering. The default GAS AT&T (mnemonic src,
// dst) flips the operands and is harder to align with the
// arm64 emit.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	treeshake.Run(prog)
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		return "", err
	}
	g := &generator{info: info}
	g.line(".intel_syntax noprefix")
	g.line(".text")
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	// ELF non-executable-stack marker. Without this the
	// linker warns (or refuses, on hardened distros) about
	// the binary having an implicit executable stack.
	g.line(".section .note.GNU-stack,\"\",@progbits")
	return g.out.String(), nil
}

// generator carries the running output buffer plus per-
// program state. PR 1 has very little state; the struct
// shape exists so later PRs can hang `usesAlloc` /
// `usesMemcpy` / `stringLabel` etc. off the same place
// without churning the call surface.
type generator struct {
	info *checker.Info
	out  strings.Builder
	// labelCounter generates unique branch / scope labels
	// (`.Lret_main_0`, `.Lif_3` etc.). Per-program rather
	// than per-function so labels stay globally unique even
	// when multiple functions share a name (which can't
	// happen today but is cheap insurance).
	labelCounter int
}

// emitStartRuntime writes the program entry point. Linux
// hands us a 16-byte-aligned rsp with [argc, argv[0],
// argv[1], ..., NULL, envp[0], ..., NULL, auxv...] on the
// stack. PR 1 ignores argc/argv and just calls main, then
// translates main's return value into an `exit_group`
// syscall.
//
// `call main` pushes the return address (8 bytes), so rsp
// enters main 8-byte-misaligned w.r.t. 16. main's prologue's
// `push rbp` brings it back to 16-aligned, matching the AAPCS
// invariant the rest of the code expects.
func (g *generator) emitStartRuntime() {
	g.line("")
	g.line(".globl _start")
	g.label("_start")
	g.emit("call main")
	g.emit("mov edi, eax")            // exit code = main's return value
	g.emit(fmt.Sprintf("mov eax, %d", sysExitGroup))
	g.emit("syscall")
}

// emitFunc lowers one function to assembly. Per-function
// scope-tracking state lives in `scope` (currently unused —
// PR 1 has no block / loop / if ops to dispatch).
func (g *generator) emitFunc(fn *ast.FuncDecl, irFn *ir.Func) error {
	// Compute frame size from the highest local slot the IR
	// referenced plus the parameter slots. Rounded up to a
	// 16-byte multiple so `sub rsp, N` leaves rsp 16-aligned
	// post-prologue.
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
	localsSize := numSlots * 8
	if localsSize%16 != 0 {
		localsSize += 8
	}

	g.line("")
	g.line(fmt.Sprintf(".globl %s", fn.Name))
	g.line(fmt.Sprintf(".type %s, @function", fn.Name))
	g.label(fn.Name)
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	if localsSize > 0 {
		g.emit(fmt.Sprintf("sub rsp, %d", localsSize))
	}
	// Param spill into local slots. Args 0..5 arrive in
	// rdi/rsi/rdx/rcx/r8/r9; args beyond that come on the
	// stack and aren't yet supported — fail loudly.
	regArgs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
	for i := range fn.Params {
		if i >= len(regArgs) {
			return fmt.Errorf("x86_64: more than %d function parameters not yet supported (got %d)", len(regArgs), len(fn.Params))
		}
		g.emit(fmt.Sprintf("mov [rbp-%d], %s", (i+1)*8, regArgs[i]))
	}

	retLabel := fmt.Sprintf(".Lret_%s_%d", fn.Name, g.fresh())
	var scope []irScope
	for _, op := range irFn.Ops {
		if err := g.emitOp(op, retLabel, &scope); err != nil {
			return err
		}
	}

	// Epilogue. OpReturn already emitted a `jmp retLabel`;
	// falling off the end (e.g. for void functions) lands
	// here naturally and exits cleanly.
	g.label(retLabel)
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(fmt.Sprintf(".size %s, .-%s", fn.Name, fn.Name))
	return nil
}

// irScope tracks one open OpBlock / OpLoop / OpIf scope. Same
// shape as the arm64 backend's irScope — branch targets resolve
// at OpBr / OpBrIf by indexing `scope` from the top with the
// op's relative depth. `endLabel` is the post-scope label;
// `brTarget` is what `br N` resolves to (= endLabel for blocks
// and ifs, = loop-top label for loops).
type irScope struct {
	kind      ir.OpKind
	brTarget  string
	endLabel  string
	elseLabel string // OpIf only
	hasElse   bool   // set when OpElse fires; OpEnd uses it to know whether elseLabel was wired up
}

// emitOp dispatches a single IR op. PR 2 scope: arithmetic
// + comparison + locals + control flow + direct calls (no
// strings, no allocator, no composite types — those land in
// PR 3+). Unknown ops surface an explicit error so the next
// PR's additions appear as test failures with a clean cause.
func (g *generator) emitOp(op ir.Op, retLabel string, scope *[]irScope) error {
	switch op.Kind {
	case ir.OpConstI32:
		// Materialise the immediate into rax, then push.
		// `mov eax, imm32` zero-extends the high 32 bits of
		// rax, keeping the encoding compact for the common
		// case. Negative values are written as `mov eax, N`
		// with N's two's-complement bit pattern (assembler
		// accepts negative imm32 directly).
		g.emit(fmt.Sprintf("mov eax, %d", op.I32))
		g.push()
	case ir.OpReturn:
		g.pop()
		g.emit(fmt.Sprintf("jmp %s", retLabel))
	case ir.OpReturnVoid:
		g.emit(fmt.Sprintf("jmp %s", retLabel))

	case ir.OpDrop:
		// Skip the top 16-byte slot.
		g.emit("add rsp, 16")

	// -------- arithmetic --------
	//
	// 64-bit register form (rax/rcx) for the same reason
	// arm64 uses x-form: pointer-shaped values (full 64-bit)
	// must survive arithmetic. `len(s)` is `ptr - 4` via
	// OpSub — narrowing to eax/ecx would zero-extend and
	// drop the pointer's high 32 bits, faulting on the
	// subsequent load. Downstream i32 consumers (`mov [..],
	// eax`, `cmp eax, ...`) only read the low 32 anyway, so
	// using 64-bit form for arithmetic doesn't change
	// observable i32 semantics.

	case ir.OpAdd:
		g.binPop()
		g.emit("add rax, rcx")
		g.push()
	case ir.OpSub:
		g.binPop()
		g.emit("sub rax, rcx")
		g.push()
	case ir.OpMul:
		g.binPop()
		g.emit("imul rax, rcx")
		g.push()
	case ir.OpDivS:
		// 32-bit signed divide: cdq sign-extends eax → edx,
		// then idiv ecx puts quotient in eax / remainder in
		// edx. Matches arm64's `sdiv w0, w1, w0` shape (w-
		// form). Unsigned divide is xor edx, edx + div ecx.
		g.binPop()
		if op.Unsigned {
			g.emit("xor edx, edx")
			g.emit("div ecx")
		} else {
			g.emit("cdq")
			g.emit("idiv ecx")
		}
		g.push()
	case ir.OpRemS:
		// Same prologue as OpDivS; remainder lives in edx
		// post-instruction. mov eax, edx routes it back to
		// the canonical "result in rax" lane before the push.
		g.binPop()
		if op.Unsigned {
			g.emit("xor edx, edx")
			g.emit("div ecx")
		} else {
			g.emit("cdq")
			g.emit("idiv ecx")
		}
		g.emit("mov eax, edx")
		g.push()
	case ir.OpAnd:
		g.binPop()
		g.emit("and rax, rcx")
		g.push()
	case ir.OpOr:
		g.binPop()
		g.emit("or rax, rcx")
		g.push()
	case ir.OpXor:
		g.binPop()
		g.emit("xor rax, rcx")
		g.push()
	case ir.OpShl:
		// x86 shift count comes from cl (low 8 of rcx). The
		// binPop's pop-rhs-first order already puts the count
		// in rcx, so we can shift directly.
		g.binPop()
		g.emit("shl rax, cl")
		g.push()
	case ir.OpShrS:
		// sar (arithmetic right shift) preserves the sign
		// bit for signed values; shr (logical) zero-fills
		// for unsigned.
		g.binPop()
		if op.Unsigned {
			g.emit("shr rax, cl")
		} else {
			g.emit("sar rax, cl")
		}
		g.push()

	// -------- comparison (i32) --------
	//
	// 32-bit cmp + setcc + movzx. cmp eax, ecx sets flags;
	// setcc al writes 0 or 1; movzx eax, al zero-extends the
	// boolean into the canonical i32-result lane. Signedness
	// changes the cc letter (l/g/le/ge vs b/a/be/ae).

	case ir.OpEq:
		g.binPop()
		g.emit("cmp eax, ecx")
		g.emit("sete al")
		g.emit("movzx eax, al")
		g.push()
	case ir.OpNe:
		g.binPop()
		g.emit("cmp eax, ecx")
		g.emit("setne al")
		g.emit("movzx eax, al")
		g.push()
	case ir.OpLtS:
		g.binPop()
		g.emit("cmp eax, ecx")
		if op.Unsigned {
			g.emit("setb al")
		} else {
			g.emit("setl al")
		}
		g.emit("movzx eax, al")
		g.push()
	case ir.OpLeS:
		g.binPop()
		g.emit("cmp eax, ecx")
		if op.Unsigned {
			g.emit("setbe al")
		} else {
			g.emit("setle al")
		}
		g.emit("movzx eax, al")
		g.push()
	case ir.OpGtS:
		g.binPop()
		g.emit("cmp eax, ecx")
		if op.Unsigned {
			g.emit("seta al")
		} else {
			g.emit("setg al")
		}
		g.emit("movzx eax, al")
		g.push()
	case ir.OpGeS:
		g.binPop()
		g.emit("cmp eax, ecx")
		if op.Unsigned {
			g.emit("setae al")
		} else {
			g.emit("setge al")
		}
		g.emit("movzx eax, al")
		g.push()

	// -------- logical / unary --------

	case ir.OpNot:
		// Boolean not: 0 → 1, non-zero → 0. test+setz+movzx
		// reads the low 32 bits (matching arm64's `cmp w0,
		// #0; cset w0, eq` shape).
		g.pop()
		g.emit("test eax, eax")
		g.emit("setz al")
		g.emit("movzx eax, al")
		g.push()

	// -------- locals --------
	//
	// Slot i sits at [rbp - (i+1)*8] — mirrors arm64's
	// `[x29, -((i+1)*8)]` exactly so the spill / reload
	// pattern is identical across backends. Always 8-byte
	// (mov rax / mov rcx) so pointer-shaped locals
	// round-trip without truncating the high 32 bits.

	case ir.OpLoadLocal:
		g.emit(fmt.Sprintf("mov rax, [rbp-%d]", (op.I32+1)*8))
		g.push()
	case ir.OpStoreLocal:
		g.pop()
		g.emit(fmt.Sprintf("mov [rbp-%d], rax", (op.I32+1)*8))
	case ir.OpTeeLocal:
		// Pop, store, push back — keeps the value on the
		// operand stack for the next consumer.
		g.pop()
		g.emit(fmt.Sprintf("mov [rbp-%d], rax", (op.I32+1)*8))
		g.push()

	// -------- control flow --------
	//
	// Same scope-stack discipline as arm64. OpBlock / OpLoop
	// open a scope with brTarget = endLabel / startLabel;
	// OpBr / OpBrIf resolve their depth-N relative target
	// by indexing scope from the top.

	case ir.OpBlock:
		endL := g.freshLabel("blkEnd")
		*scope = append(*scope, irScope{kind: ir.OpBlock, brTarget: endL, endLabel: endL})
	case ir.OpLoop:
		startL := g.freshLabel("loopTop")
		endL := g.freshLabel("loopEnd")
		g.label(startL)
		*scope = append(*scope, irScope{kind: ir.OpLoop, brTarget: startL, endLabel: endL})
	case ir.OpIf:
		// Pop cond; if zero, jump to else-label. test eax /
		// jz mirrors arm64's `cbz w0` — only the low 32 bits
		// participate in truthiness, matching the i32-shape
		// of bool values.
		g.pop()
		elseL := g.freshLabel("ifElse")
		endL := g.freshLabel("ifEnd")
		g.emit("test eax, eax")
		g.emit(fmt.Sprintf("jz %s", elseL))
		*scope = append(*scope, irScope{kind: ir.OpIf, brTarget: endL, endLabel: endL, elseLabel: elseL})
	case ir.OpElse:
		top := &(*scope)[len(*scope)-1]
		top.hasElse = true
		g.emit(fmt.Sprintf("jmp %s", top.endLabel))
		g.label(top.elseLabel)
	case ir.OpEnd:
		top := (*scope)[len(*scope)-1]
		*scope = (*scope)[:len(*scope)-1]
		if top.kind == ir.OpIf && !top.hasElse {
			// No OpElse appeared; the if's `jz` was wired to
			// elseLabel, which we now alias as the end so a
			// false condition simply skips the if body.
			g.label(top.elseLabel)
		}
		g.label(top.endLabel)
	case ir.OpBr:
		target := (*scope)[len(*scope)-1-int(op.I32)].brTarget
		g.emit(fmt.Sprintf("jmp %s", target))
	case ir.OpBrIf:
		// w0-form test like arm64's `cbnz w0, target`.
		g.pop()
		target := (*scope)[len(*scope)-1-int(op.I32)].brTarget
		g.emit("test eax, eax")
		g.emit(fmt.Sprintf("jnz %s", target))

	// -------- direct calls --------
	//
	// System V AMD64: args 0..5 in rdi/rsi/rdx/rcx/r8/r9,
	// result in rax. Pop args in reverse from the operand
	// stack so the rightmost-on-top push order matches the
	// register assignment.

	case ir.OpCallDirect:
		argc := int(op.I32)
		regArgs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
		if argc > len(regArgs) {
			return fmt.Errorf("x86_64: more than %d call args not yet supported (got %d for %q)", len(regArgs), argc, op.Str)
		}
		for i := argc - 1; i >= 0; i-- {
			g.emit(fmt.Sprintf("mov %s, [rsp]", regArgs[i]))
			g.emit("add rsp, 16")
		}
		g.emit(fmt.Sprintf("call %s", op.Str))
		g.push()

	default:
		return fmt.Errorf("x86_64: unsupported IR op %s", op.Kind)
	}
	return nil
}

// binPop pops two operand-stack values: rhs (top) → rcx, lhs
// (next) → rax. Matches arm64's binPop, which puts rhs in x0
// + lhs in x1 — same operand order, just different register
// names. Two-op x86 ops then read `op rax, rcx` (`rax = rax
// op rcx`), where rax holds the lhs and rcx the rhs.
func (g *generator) binPop() {
	g.emit("mov rcx, [rsp]") // rhs (top of stack)
	g.emit("add rsp, 16")
	g.emit("mov rax, [rsp]") // lhs (next)
	g.emit("add rsp, 16")
}

// freshLabel composes a per-program label with a `.L` prefix
// (so the assembler treats it as local and ld doesn't export
// it). Counter-suffixed for uniqueness across functions.
func (g *generator) freshLabel(prefix string) string {
	return fmt.Sprintf(".L%s_%d", prefix, g.fresh())
}

// push rax onto the operand stack — 16-byte slot, value at
// `[rsp]`, the upper 8 bytes are dead. Matches arm64's `str
// x0, [sp, #-16]!` discipline.
func (g *generator) push() {
	g.emit("sub rsp, 16")
	g.emit("mov [rsp], rax")
}

// pop into rax — 16-byte slot consumed.
func (g *generator) pop() {
	g.emit("mov rax, [rsp]")
	g.emit("add rsp, 16")
}

func (g *generator) line(s string) {
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

func (g *generator) label(name string) {
	g.out.WriteString(name)
	g.out.WriteString(":\n")
}

func (g *generator) emit(s string) {
	g.out.WriteByte('\t')
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

// fresh returns the next per-program label suffix. Currently
// only used for the per-function return label; later PRs
// will add per-scope branch labels off the same counter.
func (g *generator) fresh() int {
	n := g.labelCounter
	g.labelCounter++
	return n
}
