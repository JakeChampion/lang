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
	for _, op := range irFn.Ops {
		if err := g.emitOp(op, retLabel); err != nil {
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

// emitOp dispatches a single IR op. PR 1 only implements
// the ops needed by `function main(): i32 { return N; }`:
// OpConstI32 to materialise the literal, OpReturn to pop +
// jump to the epilogue. OpReturnVoid is also handled because
// the IR sometimes appends a trailing implicit return.
//
// Unknown ops surface explicit errors so the next PR's
// additions show up as test failures with a clear cause.
func (g *generator) emitOp(op ir.Op, retLabel string) error {
	switch op.Kind {
	case ir.OpConstI32:
		// Materialise the immediate into rax, then push.
		// `mov eax, imm32` is enough for 32-bit literals
		// (eax writes zero-extend the high 32 bits of rax),
		// keeping the encoding compact.
		g.emit(fmt.Sprintf("mov eax, %d", op.I32))
		g.push()
	case ir.OpReturn:
		g.pop()
		g.emit(fmt.Sprintf("jmp %s", retLabel))
	case ir.OpReturnVoid:
		g.emit(fmt.Sprintf("jmp %s", retLabel))
	default:
		return fmt.Errorf("x86_64: unsupported IR op %s", op.Kind)
	}
	return nil
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
