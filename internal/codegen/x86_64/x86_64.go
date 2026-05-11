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
	"math"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Linux x86-64 syscall numbers. See the asm-generic table
// for the full set.
const (
	sysRead      = 0
	sysWrite     = 1
	sysClose     = 3
	sysMmap      = 9
	sysSocket    = 41
	sysAccept    = 43
	sysBind      = 49
	sysListen    = 50
	sysExitGroup = 231
	sysGetrandom = 318
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
	// Tail-call optimisation. The pass rewrites
	// `OpCallDirect <self> ; OpReturn` into a parameter
	// rebind plus `OpBr` back to the function entry — turns
	// self-recursive functions into loops, so the deepest
	// "tail call" doesn't grow the stack. x86-64 is the
	// first consumer; the IR-level pass has been parked in
	// `internal/ir/tco.go` since the arm32 retirement
	// waiting for a native backend that wires it in.
	// Arm64 + wasm don't call it yet — adding it there is
	// safe but out of this PR's scope.
	ir.TailCallOptimize(ip)
	// Defunctionalise + ElideClosurePair turn many indirect
	// closure calls into direct ones (when the closure flow
	// is monomorphic enough for the pass to prove the
	// target statically). That collapses the closure-pair
	// representation to a single env_ptr in the slot,
	// letting us implement closures with only OpMakeEnv +
	// OpCallClosureDirect — no closure-pair handling in
	// OpCallIndirect needed for the cases these passes
	// can rewrite. Cases that don't defunctionalise still
	// fall back to the existing top-level fn-pointer
	// OpConstFunc / OpCallIndirect path (those work today;
	// see PR #273's TestX86_64IndirectCall).
	// Native closure pair: 16 bytes total, env_ptr at offset 8
	// (wasm uses 8 bytes / +4 — see Defunctionalise comment).
	ir.Defunctionalise(ip, 8)
	ir.ElideClosurePair(ip, 8)
	g := &generator{info: info, stringLabel: map[string]string{}, funcs: map[string]*ast.FuncDecl{}}
	// Pre-scan call sites for runtime-helper use-flags before
	// touching any code emission, so emitDataSections + the
	// runtime emitters below know which helpers to include
	// (and the .bss reservations match the helpers).
	for _, fn := range prog.Funcs {
		g.funcs[fn.Name] = fn
	}
	for _, fn := range ip.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == ir.OpCallDirect {
				g.recordUse(op.Str)
			}
			if op.Kind == ir.OpAlloc {
				g.usesAlloc = true
			}
			if op.Kind == ir.OpStrEq {
				g.usesStrcmp = true
			}
			if op.Kind == ir.OpStrConcat {
				g.usesStrcat = true
				g.usesAlloc = true
				g.usesMemcpy = true
			}
			if op.Kind == ir.OpMakeClosure || op.Kind == ir.OpMakeEnv {
				// Closure env block + (optional) pair both
				// come from __lang_alloc.
				g.usesAlloc = true
			}
		}
	}
	g.line(".intel_syntax noprefix")
	g.line(".text")
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	// Runtime helpers — gated on use-flags so unused programs
	// pay nothing extra in binary size.
	if g.usesAlloc {
		g.emitAllocRuntime()
	}
	if g.usesMemcpy {
		g.emitMemcpyRuntime()
	}
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	if g.usesStrcmp {
		g.emitStrcmpRuntime()
	}
	if g.usesPuts {
		g.emitPutsRuntime()
	}
	if g.usesWrite {
		g.emitWriteRuntime()
	}
	if g.usesPutchar {
		g.emitPutcharRuntime()
	}
	if g.usesTcp {
		g.emitTcpListenRuntime()
		g.emitTcpAcceptRuntime()
		g.emitTcpRecvRuntime()
		g.emitTcpSendRuntime()
		g.emitTcpCloseRuntime()
	}
	if g.usesEnv {
		g.emitEnvRuntime()
	}
	if g.usesArgs {
		g.emitArgsRuntime()
	}
	if g.usesArena {
		g.emitArenaRuntime()
	}
	if g.usesRandomBytes {
		g.emitRandomBytesRuntime()
	}
	if g.usesReadLine {
		g.emitReadLineRuntime()
	}
	if g.usesStdin {
		g.emitStdinRuntime()
	}
	if g.usesAllocU8 {
		g.emitAllocU8Runtime()
	}
	if g.usesStringFromBytes {
		g.emitStringFromBytesRuntime()
	}
	if g.usesStrSlice {
		g.emitStrSliceRuntime()
	}
	g.emitDataSections()
	// ELF non-executable-stack marker. Without this the
	// linker warns (or refuses, on hardened distros) about
	// the binary having an implicit executable stack.
	g.line(".section .note.GNU-stack,\"\",@progbits")
	return g.out.String(), nil
}

// generator carries the running output buffer plus per-
// program state.
type generator struct {
	info *checker.Info
	out  strings.Builder
	// labelCounter generates unique branch / scope labels
	// (`.Lret_main_0`, `.Lif_3` etc.). Per-program rather
	// than per-function so labels stay globally unique even
	// when multiple functions share a name (which can't
	// happen today but is cheap insurance).
	labelCounter int
	// stringLabel / stringOrder hold the string-pool scheme:
	// each unique string literal gets a single `.LStr_N`
	// .rodata label with a 4-byte little-endian length
	// prefix followed by `.asciz` data. Pointers handed to
	// user code address the .asciz base; `len(s)` reads
	// `[ptr - 4]`. Maintained in insertion order so the
	// emitted `.rodata` section is deterministic.
	stringLabel map[string]string
	stringOrder []string
	// funcs maps a top-level function name to its AST
	// declaration. Populated at Emit time so OpMakeEnv /
	// OpMakeClosure can look up the hoisted function's
	// `Captures` list — closureconv stamps it with the
	// capture parameters' types, which is what drives env-
	// block layout (slot sizes, offsets).
	funcs map[string]*ast.FuncDecl
	// Each `uses<Helper>` flag mirrors arm64's pattern —
	// only emit the helper if the program references it,
	// so trivial programs stay small. recordUse() sets
	// these from a pre-scan of the IR's OpCallDirect ops.
	usesAlloc   bool
	usesMemcpy  bool
	usesStrcat  bool
	usesStrcmp  bool
	usesPuts    bool
	usesWrite   bool
	usesPutchar bool
	usesTcp             bool
	usesEnv             bool
	usesArgs            bool
	usesArena           bool
	usesAllocU8         bool
	usesStringFromBytes bool
	usesStrSlice        bool
	usesRandomBytes     bool
	usesReadLine        bool
	usesStdin           bool
}

// recordUse flips the right use-flag for a callee name the
// IR mentions. PR 3 covered print/strings/alloc; PR 5 adds
// the TCP family + env() + the arena helpers used by the
// auto-`main()`-from-`handle()` synthesis.
func (g *generator) recordUse(target string) {
	switch target {
	case "__memcpy":
		g.usesMemcpy = true
	case "__alloc":
		g.usesAlloc = true
	case "__lang_strcat":
		g.usesStrcat = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "print":
		g.usesPuts = true
	case "write":
		g.usesWrite = true
	case "putchar":
		g.usesPutchar = true
	case "tcp_listen", "tcp_accept", "tcp_recv", "tcp_send", "tcp_close":
		g.usesTcp = true
		// tcp_recv allocates a string buffer for the read.
		if target == "tcp_recv" {
			g.usesAlloc = true
		}
	case "env":
		g.usesEnv = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "args":
		g.usesArgs = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "arena_save", "arena_restore":
		g.usesArena = true
		// arena helpers read/write __lang_heap_ptr; pull
		// in the allocator's .bss reservations.
		g.usesAlloc = true
	case "__alloc_u8":
		g.usesAllocU8 = true
		g.usesAlloc = true
	case "string_from_bytes":
		g.usesStringFromBytes = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "__str_slice":
		g.usesStrSlice = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "random_bytes":
		g.usesRandomBytes = true
		g.usesAlloc = true
	case "read_line", "__method_Reader_read_line":
		g.usesReadLine = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "stdin":
		g.usesStdin = true
	}
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
	// argc is at [rsp+0]; argv starts at [rsp+8]; envp at
	// [rsp + 8 + (argc+1)*8] (NULL-terminator after argv).
	// Stash whichever of (argc, argv, envp) the program
	// actually needs — gated so trivial programs don't pay
	// for the extra mov chain.
	if g.usesArgs {
		g.emit("mov rax, [rsp]")
		g.emit("mov [rip + __lang_argc], rax")
		g.emit("lea rcx, [rsp + 8]")
		g.emit("mov [rip + __lang_argv], rcx")
	}
	if g.usesEnv {
		g.emit("mov rax, [rsp]")             // argc
		g.emit("lea rdi, [rsp + 8]")          // rdi = &argv[0]
		g.emit("lea rdi, [rdi + rax*8 + 8]")  // skip argv + NULL terminator
		g.emit("mov [rip + __lang_envp], rdi")
	}
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
	case ir.OpConstStr:
		// Materialise the .rodata string-literal address
		// into rax. RIP-relative `lea` works for both PIE
		// and non-PIE links — the assembler resolves the
		// label at link time; the binary just dereferences
		// the right place at run time.
		lbl := g.internString(op.Str)
		g.emit(fmt.Sprintf("lea rax, [rip + %s]", lbl))
		g.push()
	case ir.OpConstFunc:
		// Function values materialise as the direct code
		// address of the named function. Same RIP-relative
		// `lea` shape as OpConstStr — there's no funcref
		// table abstraction at the x86-64 level, just plain
		// code pointers.
		g.emit(fmt.Sprintf("lea rax, [rip + %s]", op.Str))
		g.push()
	case ir.OpConstI64:
		// i64 literal: full 64-bit immediate via `movabs`.
		// (`mov rax, imm64` is the same instruction in
		// Intel syntax; the assembler picks the encoding.)
		g.emit(fmt.Sprintf("movabs rax, %d", op.I64))
		g.push()
	case ir.OpConstF32:
		// Stash the raw 32-bit bit pattern as an i32 on the
		// operand stack — same shape as arm64, where floats
		// live as raw bits on the stack and only move into
		// xmm registers at op time. Two-line dance: zero-
		// extend the bit pattern into rax, push.
		bits := math.Float32bits(op.F32)
		g.emit(fmt.Sprintf("mov eax, %d", int32(bits)))
		g.push()
	case ir.OpConstF64:
		// Same idea, 64-bit bit pattern.
		bits := math.Float64bits(op.F64)
		g.emit(fmt.Sprintf("movabs rax, %d", int64(bits)))
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

	// -------- floats --------
	//
	// Floats live on the operand stack as raw integer bit
	// patterns (same convention as arm64). At op time we
	// move into xmm0 / xmm1 via `movd` (32) or `movq` (64),
	// run the SSE op, move back to rax. Cost is one
	// shuffle per op but keeps the operand stack
	// homogeneous — every slot is a 64-bit integer regardless
	// of declared lang type.

	case ir.OpFAdd:
		g.fbinPop(op.Width)
		if op.Width == 64 {
			g.emit("addsd xmm1, xmm0")
			g.emit("movq rax, xmm1")
		} else {
			g.emit("addss xmm1, xmm0")
			g.emit("movd eax, xmm1")
		}
		g.push()
	case ir.OpFSub:
		g.fbinPop(op.Width)
		if op.Width == 64 {
			g.emit("subsd xmm1, xmm0")
			g.emit("movq rax, xmm1")
		} else {
			g.emit("subss xmm1, xmm0")
			g.emit("movd eax, xmm1")
		}
		g.push()
	case ir.OpFMul:
		g.fbinPop(op.Width)
		if op.Width == 64 {
			g.emit("mulsd xmm1, xmm0")
			g.emit("movq rax, xmm1")
		} else {
			g.emit("mulss xmm1, xmm0")
			g.emit("movd eax, xmm1")
		}
		g.push()
	case ir.OpFDiv:
		g.fbinPop(op.Width)
		if op.Width == 64 {
			g.emit("divsd xmm1, xmm0")
			g.emit("movq rax, xmm1")
		} else {
			g.emit("divss xmm1, xmm0")
			g.emit("movd eax, xmm1")
		}
		g.push()
	case ir.OpFNeg:
		// Negate via subtract-from-zero. `xorps xmm0, xmm0`
		// gives a zero float; the subtract flips the sign.
		// Avoids the sign-bit-XOR variant which would need
		// a mask constant.
		g.pop()
		if op.Width == 64 {
			g.emit("movq xmm1, rax")
			g.emit("xorpd xmm0, xmm0")
			g.emit("subsd xmm0, xmm1")
			g.emit("movq rax, xmm0")
		} else {
			g.emit("movd xmm1, eax")
			g.emit("xorps xmm0, xmm0")
			g.emit("subss xmm0, xmm1")
			g.emit("movd eax, xmm0")
		}
		g.push()
	case ir.OpFEq, ir.OpFNe, ir.OpFLt, ir.OpFLe, ir.OpFGt, ir.OpFGe:
		// `ucomi[ss|sd]` sets ZF / CF / PF per IEEE 754
		// ordered semantics; setcc + movzx funnel the
		// result into the canonical i32 result lane. NaN
		// comparisons all set PF=1 — for `eq` we'd want
		// to also check `np` (not parity) but the lang
		// doesn't yet specify NaN semantics, so we match
		// arm64's "ordered comparison without NaN-aware
		// behaviour" choice.
		g.fbinPop(op.Width)
		if op.Width == 64 {
			g.emit("ucomisd xmm1, xmm0")
		} else {
			g.emit("ucomiss xmm1, xmm0")
		}
		switch op.Kind {
		case ir.OpFEq:
			g.emit("sete al")
		case ir.OpFNe:
			g.emit("setne al")
		case ir.OpFLt:
			g.emit("setb al")
		case ir.OpFLe:
			g.emit("setbe al")
		case ir.OpFGt:
			g.emit("seta al")
		case ir.OpFGe:
			g.emit("setae al")
		}
		g.emit("movzx eax, al")
		g.push()

	case ir.OpFLoad:
		// Bit-pattern semantics: a float in memory is just
		// 4 or 8 bytes; reusing the OpLoad path keeps the
		// dispatch uniform. xmm-register moves happen at
		// arithmetic time, not load time.
		g.pop()
		if op.Width == 64 {
			g.emit("mov rax, [rax]")
		} else {
			g.emit("mov eax, [rax]")
		}
		g.push()
	case ir.OpFStore:
		g.binPop()
		if op.Width == 64 {
			g.emit("mov [rax], rcx")
		} else {
			g.emit("mov [rax], ecx")
		}

	// -------- int <-> float conversions --------
	//
	// All go through xmm via movd/movq. Unsigned variants
	// would need a 2-step trick for the >2^63 case; for
	// now the implemented path is what every test in the
	// suite uses (signed conversions on positive values).

	case ir.OpExtendI32S:
		// i32 → i64, sign-extend.
		g.pop()
		g.emit("movsx rax, eax")
		g.push()
	case ir.OpExtendI32U:
		// i32 → i64, zero-extend. `mov eax, eax` zero-
		// extends to rax (the standard idiom).
		g.pop()
		g.emit("mov eax, eax")
		g.push()
	case ir.OpWrapI64:
		// i64 → i32. Truncate via the same zero-extending
		// mov-into-eax (high bits discarded).
		g.pop()
		g.emit("mov eax, eax")
		g.push()
	case ir.OpFPromoteF32:
		// f32 → f64.
		g.pop()
		g.emit("movd xmm0, eax")
		g.emit("cvtss2sd xmm0, xmm0")
		g.emit("movq rax, xmm0")
		g.push()
	case ir.OpFDemoteF64:
		// f64 → f32.
		g.pop()
		g.emit("movq xmm0, rax")
		g.emit("cvtsd2ss xmm0, xmm0")
		g.emit("movd eax, xmm0")
		g.push()
	case ir.OpFConvertI32:
		// i32 → f32 / f64 (signed). Use `cvtsi2sX rax, eax`
		// — Intel syntax names the source register width
		// via the explicit operand.
		g.pop()
		if op.Width == 64 {
			g.emit("cvtsi2sd xmm0, eax")
			g.emit("movq rax, xmm0")
		} else {
			g.emit("cvtsi2ss xmm0, eax")
			g.emit("movd eax, xmm0")
		}
		g.push()
	case ir.OpFConvertI64:
		// i64 → f32 / f64.
		g.pop()
		if op.Width == 64 {
			g.emit("cvtsi2sd xmm0, rax")
			g.emit("movq rax, xmm0")
		} else {
			g.emit("cvtsi2ss xmm0, rax")
			g.emit("movd eax, xmm0")
		}
		g.push()
	case ir.OpITruncF32:
		// f32 → i32 / i64 (truncate toward zero).
		g.pop()
		g.emit("movd xmm0, eax")
		if op.Width == 64 {
			g.emit("cvttss2si rax, xmm0")
		} else {
			g.emit("cvttss2si eax, xmm0")
		}
		g.push()
	case ir.OpITruncF64:
		// f64 → i32 / i64.
		g.pop()
		g.emit("movq xmm0, rax")
		if op.Width == 64 {
			g.emit("cvttsd2si rax, xmm0")
		} else {
			g.emit("cvttsd2si eax, xmm0")
		}
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

	// -------- memory load / store --------
	//
	// Width=0 → 4-byte (i32 / sub-i32). Width=64 or
	// WidthPtr → 8-byte (i64 / heap pointer). Same shape
	// arm64 uses; the IR's `WidthPtr` sentinel resolves
	// here to 8-byte ops so pointer-typed values survive
	// the round-trip unmangled.

	case ir.OpLoad:
		g.pop()
		if op.Width == 64 || op.Width == ir.WidthPtr {
			g.emit("mov rax, [rax]")
		} else {
			g.emit("mov eax, [rax]") // zero-extends to rax
		}
		g.push()
	case ir.OpLoadByte:
		g.pop()
		g.emit("movzx eax, byte ptr [rax]")
		g.push()
	case ir.OpStore:
		// Stack: [addr, value], top = value. Pop value into
		// rcx then addr into rax (binPop's pattern); store
		// according to width.
		g.binPop()
		if op.Width == 64 || op.Width == ir.WidthPtr {
			g.emit("mov [rax], rcx")
		} else {
			g.emit("mov [rax], ecx")
		}
	case ir.OpStoreI8:
		g.binPop()
		g.emit("mov byte ptr [rax], cl")
	case ir.OpStoreI16:
		g.binPop()
		g.emit("mov word ptr [rax], cx")

	case ir.OpAlloc:
		// Single i32 arg (byte count) — translate to a call
		// to the bump allocator. Recorded use-flag at scan
		// time so __lang_alloc actually gets emitted.
		g.pop()
		g.emit("mov rdi, rax")
		g.emit("call __lang_alloc")
		g.push()

	case ir.OpStrConcat:
		// The IR's `+` between strings lowers directly to
		// OpStrConcat (rather than going through
		// OpCallDirect __lang_strcat) so codegen owns the
		// dispatch. Stack: [a, b], top = b. Pop into rsi /
		// rdi to match the System V `__lang_strcat(a, b)`
		// signature, call, push result.
		g.binPop() // rcx = b, rax = a
		g.emit("mov rdi, rax")
		g.emit("mov rsi, rcx")
		g.emit("call __lang_strcat")
		g.push()

	case ir.OpStrEq:
		// String equality reduces to `__lang_strcmp(a, b) == 0`.
		// Pop both, call, test result for zero, push 0 / 1.
		g.binPop()
		g.emit("mov rdi, rax")
		g.emit("mov rsi, rcx")
		g.emit("call __lang_strcmp")
		g.emit("test eax, eax")
		g.emit("setz al")
		g.emit("movzx eax, al")
		g.push()

	// -------- direct calls --------
	//
	// System V AMD64: args 0..5 in rdi/rsi/rdx/rcx/r8/r9,
	// result in rax. Pop args in reverse from the operand
	// stack so the rightmost-on-top push order matches the
	// register assignment.
	//
	// A small alias table rewrites lang-level builtins to
	// their runtime symbols (`print → __lang_puts`, etc.),
	// matching arm64's shape. The use-flag pre-scan already
	// fired when these names appeared in the IR, so the
	// runtime emitters above included them.

	case ir.OpCallClosureDirect:
		// The IR's defunctionalise pass rewrites closure
		// calls into direct calls when the closure target is
		// statically known. The caller has already pushed
		// (args..., env_ptr) onto the operand stack — same
		// shape as OpCallDirect with one extra arg. Reuse the
		// arg-pop loop with argc+1.
		argc := int(op.I32)
		regArgs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
		if argc > len(regArgs) {
			return fmt.Errorf("x86_64: more than %d closure-call args not yet supported (got %d for %q)", len(regArgs), argc, op.Str)
		}
		for i := argc - 1; i >= 0; i-- {
			g.emit(fmt.Sprintf("mov %s, [rsp]", regArgs[i]))
			g.emit("add rsp, 16")
		}
		g.emit(fmt.Sprintf("call %s", op.Str))
		g.push()

	case ir.OpMakeClosure, ir.OpMakeEnv:
		return g.emitMakeClosureOrEnv(op)

	case ir.OpCallIndirect:
		// Function-value call: the IR emitted the function-
		// pointer immediately before the call op (via
		// OpLoadLocal / OpConstFunc), so the pointer is on
		// top of the stack and args are below it in
		// left-to-right order. Pop the ptr into r11 (caller-
		// save, otherwise unused), pop args into rdi..r9, then
		// `call r11`.
		argc := int(op.I32)
		regArgs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
		if argc > len(regArgs) {
			return fmt.Errorf("x86_64: more than %d indirect-call args not yet supported (got %d)", len(regArgs), argc)
		}
		g.emit("mov r11, [rsp]") // r11 = function pointer
		g.emit("add rsp, 16")
		for i := argc - 1; i >= 0; i-- {
			g.emit(fmt.Sprintf("mov %s, [rsp]", regArgs[i]))
			g.emit("add rsp, 16")
		}
		g.emit("call r11")
		g.push()

	case ir.OpCallDirect:
		target := op.Str
		switch target {
		case "__alloc":
			target = "__lang_alloc"
		case "__memcpy":
			target = "__lang_memcpy"
		case "print":
			target = "__lang_puts"
		case "write":
			target = "__lang_write"
		case "putchar":
			target = "__lang_putchar"
		case "tcp_listen":
			target = "__lang_tcp_listen"
		case "tcp_accept":
			target = "__lang_tcp_accept"
		case "tcp_recv":
			target = "__lang_tcp_recv"
		case "tcp_send":
			target = "__lang_tcp_send"
		case "tcp_close":
			target = "__lang_tcp_close"
		case "env":
			target = "__lang_env"
		case "args":
			target = "__lang_args"
		case "arena_save":
			target = "__lang_arena_save"
		case "arena_restore":
			target = "__lang_arena_restore"
		case "random_bytes":
			target = "__lang_random_bytes"
		case "read_line", "__method_Reader_read_line":
			target = "__lang_read_line"
		case "stdin":
			target = "__lang_stdin"
		case "__str_idx", "__arr_idx", "__arr_idx_2", "__arr_idx_8",
			"__slice_idx", "__slice_idx_1", "__slice_idx_2", "__slice_idx_8":
			// IR-side bounds-check stubs the lang runtime
			// would otherwise dispatch to. Inline as a plain
			// `lea rax, [base + idx*N]` — the element-stride
			// is encoded in the helper name. Mirrors arm64's
			// `emitInlineIdxHelper`. Out-of-range indices
			// are undefined behaviour; the type checker
			// forbids static OOB so well-typed programs
			// don't reach a bad index.
			return g.emitInlineIdxHelper(target)
		}
		argc := int(op.I32)
		regArgs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
		if argc > len(regArgs) {
			return fmt.Errorf("x86_64: more than %d call args not yet supported (got %d for %q)", len(regArgs), argc, op.Str)
		}
		for i := argc - 1; i >= 0; i-- {
			g.emit(fmt.Sprintf("mov %s, [rsp]", regArgs[i]))
			g.emit("add rsp, 16")
		}
		g.emit(fmt.Sprintf("call %s", target))
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

// fbinPop pops two float-shaped values off the operand stack
// (they ride as raw bit patterns) and moves them into xmm
// registers. Width selects 32-bit (movd, single-precision)
// or 64-bit (movq, double-precision). xmm1 = lhs, xmm0 =
// rhs — same order as the integer binPop so x86's
// destination-first 2-op form (`addss xmm1, xmm0`) reads
// `lhs += rhs`.
func (g *generator) fbinPop(width int) {
	g.emit("mov rcx, [rsp]") // rhs
	g.emit("add rsp, 16")
	g.emit("mov rax, [rsp]") // lhs
	g.emit("add rsp, 16")
	if width == 64 {
		g.emit("movq xmm0, rcx")
		g.emit("movq xmm1, rax")
	} else {
		g.emit("movd xmm0, ecx")
		g.emit("movd xmm1, eax")
	}
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

// captureSlotSize mirrors closureconv.captureSlotSize for
// ptrW=8. Wide scalars (i64/f64) take 8 bytes; pointer-shaped
// captures take 8 bytes (the heap-pointer width); other
// scalars take 4. Sub-i32 captures round up to 4 bytes for
// the same alignment reason the wasm + arm64 backends use.
func captureSlotSize(t ast.Type, ptrW int) int32 {
	if ast.ElemSizeBytesFor(t, ptrW) == 8 {
		return 8
	}
	if ast.IsPointerType(t) {
		return int32(ptrW)
	}
	return 4
}

// emitMakeClosureOrEnv handles OpMakeClosure / OpMakeEnv:
// pops N captures off the operand stack (in reverse, since
// top-of-stack is the last capture), allocates the env block,
// stores each capture at its closureconv-computed offset,
// and pushes the env pointer (OpMakeEnv) or a freshly-built
// closure pair {fn_ptr, env_ptr} (OpMakeClosure).
//
// With the IR's Defunctionalise + ElideClosurePair passes run
// upstream (see EmitWithOptions), most closure-using programs
// reduce to OpMakeEnv — the closure-pair slot dies and only
// env_ptr survives, consumed by OpCallClosureDirect. The
// OpMakeClosure path is here for the cases that don't elide.
//
// Capture slot layout matches closureconv.captureSlotSize:
// wide scalars (i64 / f64) take 8 bytes; pointer-shaped
// captures take ptrW (=8 on arm64 / x86-64); other scalars
// take 4. Stores honour width too — `mov [..], rax` for
// 8-byte slots, `mov [..], eax` for 4.
func (g *generator) emitMakeClosureOrEnv(op ir.Op) error {
	envOnly := op.Kind == ir.OpMakeEnv
	hoisted, ok := g.funcs[op.Str]
	if !ok {
		return fmt.Errorf("x86_64: closure target %q not in prog.Funcs", op.Str)
	}
	n := int(op.I32)
	if n != len(hoisted.Captures) {
		return fmt.Errorf("x86_64: closure %q expects %d captures, got %d", op.Str, len(hoisted.Captures), n)
	}

	if n == 0 {
		// No captures: env_ptr = 0 placeholder. OpCallClosure
		// Direct callers still expect an env arg slot; 0
		// satisfies it (hoisted body never reads it when
		// Captures is empty).
		if envOnly {
			g.emit("xor eax, eax")
			g.push()
			return nil
		}
		// MakeClosure with zero captures: closure pair
		// {fn_ptr, 0}. Still need the pair allocation
		// because the call site may load both halves.
		g.emit("mov edi, 16")
		g.emit("call __lang_alloc")
		g.emit(fmt.Sprintf("lea rcx, [rip + %s]", op.Str))
		g.emit("mov [rax], rcx")
		g.emit("mov qword ptr [rax + 8], 0")
		g.push()
		return nil
	}

	// Compute env layout. Slot offsets are the running sum
	// of captureSlotSize(t, ptrW). ptrW=8 on x86-64.
	type slot struct {
		off  int32
		size int32
		typ  ast.Type
	}
	slots := make([]slot, n)
	envSize := int32(0)
	for i, cap := range hoisted.Captures {
		s := captureSlotSize(cap.Type, 8)
		slots[i] = slot{off: envSize, size: s, typ: cap.Type}
		envSize += s
	}

	// Pop the N captures off the operand stack into a
	// scratch region just below rsp (the operand stack is
	// already 16-byte-slot-paced, so we can copy values into
	// callee-private scratch and reorder there).
	//
	// Simpler approach: pop each capture into the env block
	// directly. We pop in REVERSE (last capture is top of
	// stack) and store at the corresponding offset.
	//
	// But __lang_alloc clobbers caller-save regs and we need
	// the captures alive across the call. Fix: stash the
	// captures in pushed stack temps, alloc, then store from
	// the stack into the env in declaration order.
	//
	// Actually the operand stack already holds them in
	// 16-byte slots, ready for restamping. After alloc we
	// have env_ptr in rax; reorder reads from operand stack
	// slots (rsp + 16*offset).
	g.emit(fmt.Sprintf("mov edi, %d", envSize))
	// Save caller's r12 (env_ptr) + r13 (loop scratch) — we
	// need them across __lang_alloc.
	g.emit("push r12")
	g.emit("push r13")
	g.emit("call __lang_alloc")
	g.emit("mov r12, rax") // r12 = env_ptr
	// Captures sit on the operand stack just above the
	// pushed callee-saves: top-of-stack (after the 2 pushes)
	// is at offset 16 — wait, we pushed twice (8 bytes
	// each), so the operand-stack values shifted down by 16.
	// The Nth (last) capture is at [rsp + 16] now, the
	// (N-1)th at [rsp + 32], and so on; the first capture is
	// at [rsp + 16 * n].
	for i, s := range slots {
		// Capture i is at operand-stack index (n-1-i) from
		// the bottom; rsp offset = (n - i) * 16 (the +16
		// accounts for the two pushes above the operand
		// stack).
		stkOff := int32(16 + (n-1-i)*16)
		g.emit(fmt.Sprintf("mov rax, [rsp + %d]", stkOff))
		// Store into env at slot offset.
		dst := "[r12]"
		if s.off > 0 {
			dst = fmt.Sprintf("[r12 + %d]", s.off)
		}
		if s.size == 8 {
			g.emit(fmt.Sprintf("mov %s, rax", dst))
		} else {
			g.emit(fmt.Sprintf("mov %s, eax", dst))
		}
	}
	// Drop the N operand-stack slots we consumed.
	g.emit(fmt.Sprintf("add rsp, %d", n*16))
	g.emit("mov rax, r12") // env_ptr in rax
	g.emit("pop r13")
	g.emit("pop r12")

	if envOnly {
		g.push()
		return nil
	}
	// OpMakeClosure: also allocate the closure pair.
	g.emit("push rax") // save env_ptr (16-byte slot)
	g.emit("sub rsp, 8")
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	// rax = pair ptr. Load fn ptr and env ptr, store.
	g.emit(fmt.Sprintf("lea rcx, [rip + %s]", op.Str))
	g.emit("mov [rax], rcx")
	g.emit("mov rcx, [rsp + 8]") // env_ptr from the temp push
	g.emit("mov [rax + 8], rcx")
	g.emit("add rsp, 16")
	g.push()
	return nil
}

// emitInlineIdxHelper inlines a `__str_idx` / `__arr_idx` /
// `__slice_idx_*` bounds-check call as a plain address
// compute (`base + index * stride`). The IR walker emits
// these as OpCallDirect with the stride encoded in the
// helper name; the actual runtime helper would do a bounds
// check first, but in-range accesses produce the same
// address either way and the IR's static type checker
// rejects statically-OOB indexes. Subsequent OpLoad /
// OpStore consumes the address in rax.
//
// x86-64 has a `lea base + idx*scale` addressing form
// directly for scale 1/2/4/8 — strictly faster than
// arm64's `add rN, rN, rN, lsl #M` for the same job.
func (g *generator) emitInlineIdxHelper(name string) error {
	// Pop in the order the OpCallDirect dispatch would
	// use: rhs (idx, top of stack) first, lhs (base, next)
	// second.
	g.emit("mov rcx, [rsp]") // idx
	g.emit("add rsp, 16")
	g.emit("mov rax, [rsp]") // base
	g.emit("add rsp, 16")
	switch name {
	case "__str_idx", "__slice_idx_1":
		g.emit("lea rax, [rax + rcx]")
	case "__arr_idx_2", "__slice_idx_2":
		g.emit("lea rax, [rax + rcx*2]")
	case "__arr_idx", "__slice_idx":
		g.emit("lea rax, [rax + rcx*4]")
	case "__arr_idx_8", "__slice_idx_8":
		g.emit("lea rax, [rax + rcx*8]")
	default:
		return fmt.Errorf("x86_64: unknown index helper %q", name)
	}
	g.push()
	return nil
}

// fresh returns the next per-program label suffix.
func (g *generator) fresh() int {
	n := g.labelCounter
	g.labelCounter++
	return n
}

// internString returns a unique .rodata label for s, adding
// to the per-program string pool the first time `s` is seen.
// Programs that reference the same literal multiple times
// share one entry.
func (g *generator) internString(s string) string {
	if lbl, ok := g.stringLabel[s]; ok {
		return lbl
	}
	lbl := fmt.Sprintf(".LStr_%d", len(g.stringOrder))
	g.stringLabel[s] = lbl
	g.stringOrder = append(g.stringOrder, s)
	return lbl
}

// emitDataSections writes `.rodata` (interned string
// literals) and `.bss` (the bump-allocator cursor +
// heap-end sentinel). All entries are gated on usage so
// unused programs pay nothing — `.bss` is omitted entirely
// when the allocator isn't pulled in.
func (g *generator) emitDataSections() {
	if len(g.stringOrder) > 0 || g.usesPuts {
		g.line("")
		g.line(".section .rodata")
		for _, s := range g.stringOrder {
			// 4-byte little-endian length prefix followed by
			// the .asciz data. Pointers handed to user code
			// address the .asciz base (.LStr_N); `len()` reads
			// `[ptr - 4]`. Same byte-level shape as wasm /
			// arm64 — the rodata layout is portable.
			g.line(".align 4")
			g.line(fmt.Sprintf("\t.4byte %d", len(s)))
			g.label(g.stringLabel[s])
			g.line("\t.asciz " + escapeForGAS(s))
		}
		if g.usesPuts {
			// Trailing newline byte for __lang_puts. Stored
			// in the same section as the string literals so
			// the loader maps it read-only.
			g.label(".LLangNewline")
			g.line(`	.asciz "\n"`)
		}
	}
	if g.usesAlloc || g.usesEnv || g.usesArgs || g.usesReadLine {
		g.line("")
		g.line(".section .bss")
		if g.usesAlloc {
			g.line(".align 8")
			g.label("__lang_heap_ptr")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__lang_heap_end")
			g.line("\t.quad 0")
		}
		if g.usesEnv {
			g.line(".align 8")
			g.label("__lang_envp")
			g.line("\t.quad 0")
		}
		if g.usesArgs {
			g.line(".align 8")
			g.label("__lang_argc")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__lang_argv")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__lang_args_cache")
			g.line("\t.quad 0")
		}
		if g.usesReadLine {
			// 4 KiB scratch buffer for the byte-by-byte
			// read loop. Same fixed cap arm64 uses; if a
			// real workload ever needs longer lines we
			// move to a growable read buffer.
			g.line(".align 8")
			g.label("__lang_read_line_buf")
			g.line("\t.space 4096")
		}
	}
}

// emitAllocRuntime emits `__lang_alloc(size: i64) -> i64`,
// the mmap-backed bump allocator. Same shape as arm64's:
// lazy mmap reservation on first call, then a cursor bump
// per allocation. 64 MiB virtual reservation is plenty for
// the CLI / edge-handler workloads we target. The arena is
// reclaimed by the OS at process exit — no `free`.
//
// Allocations are rounded up to a 16-byte boundary so
// subsequent allocs stay 16-aligned for any pointer-pair
// operations the caller might issue.
func (g *generator) emitAllocRuntime() {
	const heapBytes = 64 * 1024 * 1024 // 64 MiB
	g.line("")
	g.line(".globl __lang_alloc")
	g.line(".type __lang_alloc, @function")
	g.label("__lang_alloc")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	// Round size up to 16-byte alignment: size = (size + 15) & ~15.
	g.emit("add rdi, 15")
	g.emit("and rdi, -16")
	// Lazy heap init: if heap_ptr == 0, mmap-reserve the arena.
	g.emit("mov rax, [rip + __lang_heap_ptr]")
	g.emit("test rax, rax")
	g.emit("jnz .Lalloc_have_heap")
	// Save size on the stack across the syscall — r12+ are
	// all callee-save in System V so clobbering them in a
	// leaf-ish runtime helper would silently destroy caller
	// state (caught when __lang_strcat passes a / b through
	// r12 / r13 and the alloc clobbers them). Push rdi
	// (size) onto the stack, syscall, restore.
	g.emit("push rdi")
	g.emit("sub rsp, 8") // align rsp to 16 for the syscall
	// mmap(NULL, heapBytes, RW, PRIVATE|ANON, -1, 0).
	g.emit("xor edi, edi")
	g.emit(fmt.Sprintf("mov esi, %d", heapBytes))
	g.emit("mov edx, 3")     // PROT_READ | PROT_WRITE
	g.emit("mov r10d, 0x22") // MAP_PRIVATE | MAP_ANONYMOUS (Linux)
	g.emit("mov r8d, -1")
	g.emit("xor r9d, r9d")
	g.emit(fmt.Sprintf("mov eax, %d", sysMmap))
	g.emit("syscall")
	g.emit("add rsp, 8")
	g.emit("pop rdi") // restore size
	// On failure mmap returns -errno (negative). Trap by
	// jumping to an exit_group(137) — analogous to arm64's
	// hard-OOM path.
	g.emit("cmp rax, 0")
	g.emit("jl .Lalloc_oom")
	g.emit("mov [rip + __lang_heap_ptr], rax")
	g.emit("lea rcx, [rax + " + fmt.Sprintf("%d", heapBytes) + "]")
	g.emit("mov [rip + __lang_heap_end], rcx")
	g.label(".Lalloc_have_heap")
	// Bump: ptr = heap_ptr; heap_ptr += size; if (heap_ptr > heap_end) OOM.
	g.emit("mov rax, [rip + __lang_heap_ptr]")
	g.emit("lea rcx, [rax + rdi]")
	g.emit("cmp rcx, [rip + __lang_heap_end]")
	g.emit("ja .Lalloc_oom")
	g.emit("mov [rip + __lang_heap_ptr], rcx")
	g.emit("pop rbp")
	g.emit("ret")
	g.label(".Lalloc_oom")
	g.emit("mov edi, 137")
	g.emit(fmt.Sprintf("mov eax, %d", sysExitGroup))
	g.emit("syscall")
	g.line(".size __lang_alloc, .-__lang_alloc")
}

// emitMemcpyRuntime emits `__lang_memcpy(dst, src, n)` —
// AAPCS-style return-the-dst contract (matches arm64). Uses
// `rep movsb` for the copy. Simple, correct, and on modern
// x86-64 CPUs the microcoded fast-string path is competitive
// with hand-rolled 8-byte loops for the buffer sizes the lang
// runtime sees (HTTP buffers, JSON, map entries).
func (g *generator) emitMemcpyRuntime() {
	g.line("")
	g.line(".globl __lang_memcpy")
	g.line(".type __lang_memcpy, @function")
	g.label("__lang_memcpy")
	g.emit("mov rax, rdi")  // save dst for return
	g.emit("mov rcx, rdx")  // count → rcx for `rep movsb`
	g.emit("cld")            // direction-flag = forward
	g.emit("rep movsb")     // [rdi++] = [rsi++], rcx times
	g.emit("ret")
	g.line(".size __lang_memcpy, .-__lang_memcpy")
}

// emitStrcatRuntime emits `__lang_strcat(a, b)` — concat two
// length-prefixed strings into a fresh allocation. a / b
// are data pointers (post-prefix); 4-byte length lives at
// `[ptr - 4]`. Returns the new data pointer.
//
// Uses callee-save r12..r15 to keep state across the calls
// to __lang_alloc and __lang_memcpy.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".globl __lang_strcat")
	g.line(".type __lang_strcat, @function")
	g.label("__lang_strcat")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	// rbx + r12..r15 are all System V callee-save — we save
	// every one we touch, including rbx (latent bug in PR 3
	// draft: clobbered without saving, broke any multi-
	// strcat call chain). The extra `push rbx` keeps the
	// stack 16-byte aligned alongside the four r12+ pushes.
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("push r15")
	g.emit("sub rsp, 8") // re-align rsp to 16 (5 saved regs + return = 48, +8 = 56 odd; +8 more = 64)
	g.emit("mov r12, rdi") // r12 = a
	g.emit("mov r13, rsi") // r13 = b
	// la = *(a - 4); lb = *(b - 4)
	g.emit("mov r14d, [r12 - 4]")
	g.emit("mov r15d, [r13 - 4]")
	// alloc(la + lb + 5) — 4 prefix + N data + 1 NUL.
	g.emit("lea rdi, [r14 + r15 + 5]")
	g.emit("call __lang_alloc")
	// rax = base; data ptr = base + 4. Stash dst in rbx
	// (callee-save) so it survives both __lang_memcpy
	// calls, then return it at the end.
	g.emit("lea rbx, [rax + 4]")
	// length prefix at base + 0.
	g.emit("mov ecx, r14d")
	g.emit("add ecx, r15d")
	g.emit("mov [rax], ecx")
	// memcpy(dst, a, la)
	g.emit("mov rdi, rbx")
	g.emit("mov rsi, r12")
	g.emit("mov rdx, r14")
	g.emit("call __lang_memcpy")
	// memcpy(dst + la, b, lb)
	g.emit("lea rdi, [rbx + r14]")
	g.emit("mov rsi, r13")
	g.emit("mov rdx, r15")
	g.emit("call __lang_memcpy")
	// Trailing NUL at dst + la + lb.
	g.emit("lea rdi, [rbx + r14]")
	g.emit("add rdi, r15")
	g.emit("mov byte ptr [rdi], 0")
	g.emit("mov rax, rbx")
	g.emit("add rsp, 8")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_strcat, .-__lang_strcat")
}

// emitStrcmpRuntime emits `__lang_strcmp(a, b)` — returns 0
// if a and b have the same length AND the same bytes, else 1.
// Pure equality comparator (no lex ordering), matching arm64.
func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".globl __lang_strcmp")
	g.line(".type __lang_strcmp, @function")
	g.label("__lang_strcmp")
	// Same pointer? Equal.
	g.emit("cmp rdi, rsi")
	g.emit("je .Lscmp_eq")
	// Same length?
	g.emit("mov ecx, [rdi - 4]")
	g.emit("mov edx, [rsi - 4]")
	g.emit("cmp ecx, edx")
	g.emit("jne .Lscmp_neq")
	// rep cmpsb wants the count in rcx and the pointers in
	// rsi (source 1) / rdi (source 2). cld → forward.
	g.emit("cld")
	g.emit("repe cmpsb")
	g.emit("jne .Lscmp_neq")
	g.label(".Lscmp_eq")
	g.emit("xor eax, eax")
	g.emit("ret")
	g.label(".Lscmp_neq")
	g.emit("mov eax, 1")
	g.emit("ret")
	g.line(".size __lang_strcmp, .-__lang_strcmp")
}

// emitPutsRuntime emits `__lang_puts(s)` — write the string,
// then a single trailing newline. Two write(2) calls keeps
// the code simple at the cost of one extra syscall per call;
// per-call cost is dominated by the syscall itself either
// way. Preserves r12 across the second write so we can
// return the original data pointer for libc-puts
// consistency.
func (g *generator) emitPutsRuntime() {
	g.line("")
	g.line(".globl __lang_puts")
	g.line(".type __lang_puts, @function")
	g.label("__lang_puts")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push r12")
	g.emit("sub rsp, 8") // keep rsp 16-aligned post-prologue
	g.emit("mov r12, rdi")        // r12 = data ptr (saved for return)
	// write(1, s, len(s))
	g.emit("mov edx, [rdi - 4]")  // length
	g.emit("mov rsi, rdi")        // buf
	g.emit("mov edi, 1")          // fd = stdout
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	// write(1, "\n", 1)
	g.emit("lea rsi, [rip + .LLangNewline]")
	g.emit("mov edx, 1")
	g.emit("mov edi, 1")
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("mov rax, r12")
	g.emit("add rsp, 8")
	g.emit("pop r12")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_puts, .-__lang_puts")
}

// emitWriteRuntime emits `__lang_write(s)` — write the
// string with no trailing newline. Single write(2) syscall.
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".globl __lang_write")
	g.line(".type __lang_write, @function")
	g.label("__lang_write")
	g.emit("mov edx, [rdi - 4]")  // length
	g.emit("mov rsi, rdi")        // buf
	g.emit("mov rax, rdi")        // save for return
	g.emit("mov edi, 1")          // fd = stdout
	g.emit("push rax")
	g.emit("sub rsp, 8")          // align
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("add rsp, 8")
	g.emit("pop rax")
	g.emit("ret")
	g.line(".size __lang_write, .-__lang_write")
}

// emitPutcharRuntime emits `__lang_putchar(c)` — write a
// single byte to stdout. Stash on the stack, write(1, &c, 1).
func (g *generator) emitPutcharRuntime() {
	g.line("")
	g.line(".globl __lang_putchar")
	g.line(".type __lang_putchar, @function")
	g.label("__lang_putchar")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")        // 1 byte slot + alignment
	g.emit("mov [rsp], dil")     // byte value
	g.emit("mov edi, 1")         // fd = stdout
	g.emit("mov rsi, rsp")       // buf = &slot
	g.emit("mov edx, 1")         // count = 1
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_putchar, .-__lang_putchar")
}

// emitTcpListenRuntime emits `__lang_tcp_listen(port)` —
// opens a TCP listening socket on 0.0.0.0:port. Returns the
// listener fd on success, or `-errno` on failure. C-style
// API; callers check `if (fd < 0)`. Same shape as arm64's
// helper, just with x86-64 syscall numbers + register
// conventions.
//
// Steps: socket(AF_INET, SOCK_STREAM, 0); bind to a stack-
// allocated sockaddr_in; listen with backlog=128.
func (g *generator) emitTcpListenRuntime() {
	g.line("")
	g.line(".globl __lang_tcp_listen")
	g.line(".type __lang_tcp_listen, @function")
	g.label("__lang_tcp_listen")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // callee-save scratch
	g.emit("push r12") // callee-save: port
	g.emit("sub rsp, 16") // sockaddr_in (16 bytes) — also keeps rsp 16-aligned for the syscall
	g.emit("mov r12d, edi")  // r12 = port
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	g.emit("mov edi, 2")
	g.emit("mov esi, 1")
	g.emit("xor edx, edx")
	g.emit(fmt.Sprintf("mov eax, %d", sysSocket))
	g.emit("syscall")
	g.emit("test eax, eax")
	g.emit("js .Ltcp_lst_err")
	g.emit("mov ebx, eax") // ebx = listener fd
	// Build sockaddr_in on the stack (16 bytes, at rsp+0):
	//   sin_family=AF_INET (2 bytes)
	//   sin_port=htons(port) (2 bytes)
	//   sin_addr=INADDR_ANY (4 bytes, zero)
	//   sin_zero=0 (8 bytes)
	g.emit("mov word ptr [rsp], 2")
	g.emit("mov eax, r12d")
	g.emit("xchg al, ah")         // htons low 16
	g.emit("mov word ptr [rsp+2], ax")
	g.emit("mov dword ptr [rsp+4], 0")
	g.emit("mov qword ptr [rsp+8], 0")
	// bind(fd, sa, 16)
	g.emit("mov edi, ebx")
	g.emit("mov rsi, rsp")
	g.emit("mov edx, 16")
	g.emit(fmt.Sprintf("mov eax, %d", sysBind))
	g.emit("syscall")
	g.emit("test eax, eax")
	g.emit("js .Ltcp_lst_err")
	// listen(fd, 128)
	g.emit("mov edi, ebx")
	g.emit("mov esi, 128")
	g.emit(fmt.Sprintf("mov eax, %d", sysListen))
	g.emit("syscall")
	g.emit("test eax, eax")
	g.emit("js .Ltcp_lst_err")
	// Return listener fd.
	g.emit("mov eax, ebx")
	g.emit("jmp .Ltcp_lst_done")
	g.label(".Ltcp_lst_err")
	// On failure rax already holds -errno from the failing syscall.
	g.label(".Ltcp_lst_done")
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_tcp_listen, .-__lang_tcp_listen")
}

// emitTcpAcceptRuntime emits `__lang_tcp_accept(listener)` —
// blocks waiting for a connection. Returns the new client fd
// or -errno. accept(fd, NULL, NULL) discards peer address.
func (g *generator) emitTcpAcceptRuntime() {
	g.line("")
	g.line(".globl __lang_tcp_accept")
	g.line(".type __lang_tcp_accept, @function")
	g.label("__lang_tcp_accept")
	// fd in rdi; pass NULL/NULL for addr/addrlen.
	g.emit("xor esi, esi")
	g.emit("xor edx, edx")
	g.emit(fmt.Sprintf("mov eax, %d", sysAccept))
	g.emit("syscall")
	g.emit("ret")
	g.line(".size __lang_tcp_accept, .-__lang_tcp_accept")
}

// emitTcpRecvRuntime emits `__lang_tcp_recv(fd, max)` —
// reads up to `max` bytes from the socket fd into a fresh
// length-prefixed lang string. EOF / error → length 0.
// Saves r12 across the syscall so the data pointer survives.
func (g *generator) emitTcpRecvRuntime() {
	g.line("")
	g.line(".globl __lang_tcp_recv")
	g.line(".type __lang_tcp_recv, @function")
	g.label("__lang_tcp_recv")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // fd
	g.emit("push r12") // max
	g.emit("push r13") // data ptr
	g.emit("sub rsp, 8") // align
	g.emit("mov ebx, edi")    // rbx = fd
	g.emit("mov r12d, esi")   // r12 = max
	// Allocate max + 5 bytes (4 prefix + max data + 1 NUL).
	g.emit("lea edi, [r12 + 5]")
	g.emit("call __lang_alloc")
	g.emit("lea r13, [rax + 4]") // r13 = data ptr
	// read(fd, data, max)
	g.emit("mov edi, ebx")
	g.emit("mov rsi, r13")
	g.emit("mov edx, r12d")
	g.emit(fmt.Sprintf("mov eax, %d", sysRead))
	g.emit("syscall")
	// Clamp to >= 0 (read returns -errno or 0 on EOF).
	g.emit("test rax, rax")
	g.emit("jns .Ltcp_recv_ok")
	g.emit("xor eax, eax")
	g.label(".Ltcp_recv_ok")
	g.emit("mov [r13 - 4], eax")  // length prefix
	g.emit("mov byte ptr [r13 + rax], 0") // trailing NUL
	g.emit("mov rax, r13")
	g.emit("add rsp, 8")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_tcp_recv, .-__lang_tcp_recv")
}

// emitTcpSendRuntime emits `__lang_tcp_send(fd, data)` —
// writes the full length-prefixed string to the socket.
// Returns the byte count or -errno on the first write.
// Single write(2) call — no buffering / partial-write loop;
// callers needing >page-sized payloads should chunk
// themselves.
func (g *generator) emitTcpSendRuntime() {
	g.line("")
	g.line(".globl __lang_tcp_send")
	g.line(".type __lang_tcp_send, @function")
	g.label("__lang_tcp_send")
	g.emit("mov edx, [rsi - 4]") // length from data prefix
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("ret")
	g.line(".size __lang_tcp_send, .-__lang_tcp_send")
}

// emitTcpCloseRuntime emits `__lang_tcp_close(fd)` —
// closes the socket via the close syscall. Returns 0 or
// -errno.
func (g *generator) emitTcpCloseRuntime() {
	g.line("")
	g.line(".globl __lang_tcp_close")
	g.line(".type __lang_tcp_close, @function")
	g.label("__lang_tcp_close")
	g.emit(fmt.Sprintf("mov eax, %d", sysClose))
	g.emit("syscall")
	g.emit("ret")
	g.line(".size __lang_tcp_close, .-__lang_tcp_close")
}

// emitEnvRuntime emits `__lang_env(name)` — walks the envp
// vector for NAME=VALUE entries. Returns Option[string]: a
// 16-byte heap object [tag:i32, _pad:i32, str_ptr:i64].
// Payload offset 8 matches the IR's PR #267 layout (8-byte-
// aligned pointer payload).
func (g *generator) emitEnvRuntime() {
	g.line("")
	g.line(".globl __lang_env")
	g.line(".type __lang_env, @function")
	g.label("__lang_env")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // envp cursor
	g.emit("push r12") // name data ptr
	g.emit("push r13") // name length
	g.emit("push r14") // value data ptr (post-strcat)
	g.emit("push r15") // value length
	g.emit("sub rsp, 8") // align
	g.emit("mov r12, rdi")            // r12 = name
	g.emit("mov r13d, [r12 - 4]")     // r13 = name length
	g.emit("mov rbx, [rip + __lang_envp]")
	g.label(".Lenv_loop")
	g.emit("mov rdi, [rbx]")
	g.emit("test rdi, rdi")
	g.emit("jz .Lenv_none")
	// Compare first name_len bytes of envp[i] with name.
	g.emit("mov rsi, r12")
	g.emit("mov ecx, r13d")
	g.emit("cld")
	g.emit("repe cmpsb")
	g.emit("jne .Lenv_next")
	// Check that byte at [rdi] is '=' (the '=' separator).
	g.emit("cmp byte ptr [rdi], 61")
	g.emit("jne .Lenv_next")
	// Found. Compute value start = rdi + 1.
	g.emit("inc rdi")
	g.emit("mov r14, rdi")
	// Inline strlen.
	g.emit("xor ecx, ecx")
	g.label(".Lenv_strlen")
	g.emit("mov al, [rdi + rcx]")
	g.emit("test al, al")
	g.emit("jz .Lenv_strlen_done")
	g.emit("inc rcx")
	g.emit("jmp .Lenv_strlen")
	g.label(".Lenv_strlen_done")
	g.emit("mov r15, rcx")
	// Allocate len+5 bytes for the value string (4 prefix + N data + 1 NUL).
	g.emit("lea edi, [r15 + 5]")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], r15d") // length prefix
	g.emit("lea rdi, [rax + 4]")
	g.emit("mov rsi, r14")
	g.emit("mov rdx, r15")
	g.emit("call __lang_memcpy")
	// rax = data ptr returned from memcpy. Build Option[string]:
	//   16 bytes [tag=0, pad, ptr]
	g.emit("mov r14, rax") // stash str ptr
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0") // tag = 0 (Some)
	g.emit("mov [rax + 8], r14")     // payload at +8 (8-byte slot)
	g.emit("jmp .Lenv_done")
	g.label(".Lenv_next")
	g.emit("add rbx, 8")
	g.emit("jmp .Lenv_loop")
	g.label(".Lenv_none")
	g.emit("mov edi, 8")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1") // tag = 1 (None)
	g.label(".Lenv_done")
	g.emit("add rsp, 8")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_env, .-__lang_env")
}

// emitArgsRuntime emits `__lang_args()` — returns a length-
// prefixed `string[]` materialised from the argc / argv pair
// captured at `_start`. Each entry is a fresh length-prefixed
// string with a trailing NUL preserved (for libc-shape
// consumers like `puts`). Result is cached in
// `__lang_args_cache` so repeat calls are O(1). Same shape
// arm64 uses (PR #267 ptr-width-stride layout):
//
//   [pad:4 | len:4 | argv0_ptr:8 | argv1_ptr:8 | ...]
//
// data ptr = base + 8 (8-aligned). length prefix at
// `data - 4`. Element stride 8 bytes, one full pointer per
// argv entry.
func (g *generator) emitArgsRuntime() {
	g.line("")
	g.line(".globl __lang_args")
	g.line(".type __lang_args, @function")
	g.label("__lang_args")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")  // argc
	g.emit("push r12")  // argv (char**)
	g.emit("push r13")  // i (loop)
	g.emit("push r14")  // result data ptr
	g.emit("push r15")  // current argv[i] / strlen
	g.emit("sub rsp, 8") // align
	// Fast path: cached?
	g.emit("mov rax, [rip + __lang_args_cache]")
	g.emit("test rax, rax")
	g.emit("jnz .Largs_ret")
	// argc / argv from globals captured by _start.
	g.emit("mov rbx, [rip + __lang_argc]")
	g.emit("mov r12, [rip + __lang_argv]")
	// alloc(argc * 8 + 8) — 8-byte header keeps element 0
	// at an 8-aligned offset; length prefix lives at
	// data-4, padding fills data-8..data-4.
	g.emit("lea rdi, [rbx * 8 + 8]")
	g.emit("call __lang_alloc")
	g.emit("lea r14, [rax + 8]")  // r14 = data ptr (8-aligned)
	g.emit("mov [r14 - 4], ebx")  // length prefix = argc
	g.emit("xor r13d, r13d")       // i = 0
	g.label(".Largs_loop")
	g.emit("cmp r13, rbx")
	g.emit("jge .Largs_done")
	// r15 = argv[i] (C string pointer).
	g.emit("mov r15, [r12 + r13*8]")
	// Inline strlen on r15.
	g.emit("xor ecx, ecx")
	g.label(".Largs_strlen")
	g.emit("mov al, [r15 + rcx]")
	g.emit("test al, al")
	g.emit("jz .Largs_strlen_done")
	g.emit("inc rcx")
	g.emit("jmp .Largs_strlen")
	g.label(".Largs_strlen_done")
	// rcx = strlen. alloc(strlen + 5).
	g.emit("mov rdx, rcx")          // save strlen (r15 is C ptr; need it for memcpy)
	g.emit("lea edi, [rcx + 5]")
	g.emit("push rdx")
	g.emit("call __lang_alloc")
	g.emit("pop rdx")
	g.emit("mov [rax], edx")        // length prefix
	// memcpy(data, argv[i], strlen + 1) — include NUL.
	g.emit("lea rdi, [rax + 4]")    // dst
	g.emit("mov rsi, r15")           // src = argv[i]
	g.emit("lea rdx, [rdx + 1]")
	g.emit("push rax")
	g.emit("call __lang_memcpy")
	g.emit("pop rax")
	// result[i] = data ptr (full 8 bytes — pointer-stride).
	g.emit("lea rcx, [rax + 4]")
	g.emit("mov [r14 + r13*8], rcx")
	g.emit("inc r13")
	g.emit("jmp .Largs_loop")
	g.label(".Largs_done")
	g.emit("mov [rip + __lang_args_cache], r14")
	g.emit("mov rax, r14")
	g.label(".Largs_ret")
	g.emit("add rsp, 8")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_args, .-__lang_args")
}

// emitRandomBytesRuntime emits `__lang_random_bytes(n)` —
// allocates a fresh length-prefixed lang string of n bytes
// and fills it with kernel CSPRNG output via a single
// `getrandom(buf, n, 0)` syscall (Linux x86-64 #318;
// blocks at most very briefly until the urandom pool is
// initialised; flags=0). Returns the data pointer.
func (g *generator) emitRandomBytesRuntime() {
	g.line("")
	g.line(".globl __lang_random_bytes")
	g.line(".type __lang_random_bytes, @function")
	g.label("__lang_random_bytes")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")  // n
	g.emit("push r12")  // data ptr
	g.emit("mov ebx, edi")  // rbx = n
	// Allocate n + 5 (4 prefix + n data + 1 trailing NUL).
	g.emit("lea edi, [rbx + 5]")
	g.emit("call __lang_alloc")
	g.emit("lea r12, [rax + 4]")     // r12 = data ptr
	g.emit("mov [r12 - 4], ebx")     // length prefix
	// getrandom(buf=r12, n=rbx, flags=0)
	g.emit("mov rdi, r12")
	g.emit("mov rsi, rbx")
	g.emit("xor edx, edx")
	g.emit(fmt.Sprintf("mov eax, %d", sysGetrandom))
	g.emit("syscall")
	// Trailing NUL at data + n. (getrandom doesn't write
	// past the requested length.)
	g.emit("mov byte ptr [r12 + rbx], 0")
	g.emit("mov rax, r12")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_random_bytes, .-__lang_random_bytes")
}

// emitReadLineRuntime emits `__lang_read_line()` — reads
// stdin one byte at a time into the 4 KiB
// `__lang_read_line_buf` (.bss), stops at '\n' (kept in
// the result) or 4 KiB or EOF/error. Returns
// Option[string]: Some(line) when at least one byte was
// read, None when the very first read returned 0 (EOF
// before any input).
//
// Option payload layout matches the IR's PR #267 shape:
//
//	Some: [tag=0:4][pad:4][str_ptr:8]   (16 bytes; payload at +8)
//	None: [tag=1:4]                      (heap-allocated; tag-only)
//
// Callee-save rbx / r12 / r13 hold buf base, bytes-read,
// and stash slots across the inner read syscall + alloc /
// memcpy calls.
func (g *generator) emitReadLineRuntime() {
	g.line("")
	g.line(".globl __lang_read_line")
	g.line(".type __lang_read_line, @function")
	g.label("__lang_read_line")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")  // buf base
	g.emit("push r12")  // bytes-read counter
	g.emit("push r13")  // stash (str ptr across alloc, etc.)
	g.emit("sub rsp, 8")
	g.emit("lea rbx, [rip + __lang_read_line_buf]")
	g.emit("xor r12d, r12d")  // bytes read = 0
	g.label(".Lrl_loop")
	g.emit("cmp r12, 4096")
	g.emit("jge .Lrl_done")
	// read(0, buf + r12, 1)
	g.emit("xor edi, edi")
	g.emit("lea rsi, [rbx + r12]")
	g.emit("mov edx, 1")
	g.emit(fmt.Sprintf("mov eax, %d", sysRead))
	g.emit("syscall")
	// EOF (0) or error (<0) → finish.
	g.emit("cmp rax, 1")
	g.emit("jl .Lrl_done")
	// Examine the just-read byte. r12 not yet incremented;
	// access via [rbx + r12].
	g.emit("mov al, [rbx + r12]")
	g.emit("inc r12")
	g.emit("cmp al, 10")  // '\n'
	g.emit("je .Lrl_done")
	g.emit("jmp .Lrl_loop")
	g.label(".Lrl_done")
	// EOF before any byte → return None.
	g.emit("test r12, r12")
	g.emit("jnz .Lrl_some")
	g.emit("mov edi, 4")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1")  // tag = 1 (None)
	g.emit("jmp .Lrl_ret")
	g.label(".Lrl_some")
	// alloc(len + 5): 4 prefix + N data + 1 trailing NUL.
	g.emit("lea edi, [r12 + 5]")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], r12d")        // length prefix
	g.emit("lea r13, [rax + 4]")     // r13 = data ptr
	// memcpy(r13, rbx, r12)
	g.emit("mov rdi, r13")
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, r12")
	g.emit("call __lang_memcpy")
	// Trailing NUL.
	g.emit("mov byte ptr [r13 + r12], 0")
	// Build Option[string]: 16 bytes [tag=0, pad, ptr@+8].
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0")  // tag = 0 (Some)
	g.emit("mov [rax + 8], r13")       // payload at +8 (8-byte slot)
	g.label(".Lrl_ret")
	g.emit("add rsp, 8")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_read_line, .-__lang_read_line")
}

// emitStdinRuntime emits `__lang_stdin()` — a 1-instruction
// stub returning 0. The checker requires `stdin()` to be
// callable but the backend doesn't yet model per-fd
// Readers, so the receiver value is unused; any sentinel
// works. Matches arm64's shape.
func (g *generator) emitStdinRuntime() {
	g.line("")
	g.line(".globl __lang_stdin")
	g.line(".type __lang_stdin, @function")
	g.label("__lang_stdin")
	g.emit("xor eax, eax")
	g.emit("ret")
	g.line(".size __lang_stdin, .-__lang_stdin")
}

// emitArenaRuntime emits the bump-cursor snapshot/rewind pair:
// `__lang_arena_save()` returns the current heap pointer;
// `__lang_arena_restore(saved)` resets it. Used by tcp_serve
// to bracket each request's allocations so they're reclaimed
// before the next accept.
func (g *generator) emitArenaRuntime() {
	g.line("")
	g.line(".globl __lang_arena_save")
	g.line(".type __lang_arena_save, @function")
	g.label("__lang_arena_save")
	g.emit("mov rax, [rip + __lang_heap_ptr]")
	g.emit("ret")
	g.line(".size __lang_arena_save, .-__lang_arena_save")
	g.line("")
	g.line(".globl __lang_arena_restore")
	g.line(".type __lang_arena_restore, @function")
	g.label("__lang_arena_restore")
	g.emit("mov [rip + __lang_heap_ptr], rdi")
	g.emit("ret")
	g.line(".size __lang_arena_restore, .-__lang_arena_restore")
}

// emitAllocU8Runtime emits `__alloc_u8(n)` — allocates a
// fresh length-prefixed `u8[]` of n bytes. Returns the data
// pointer (header + 4); length lives at `[data - 4]`.
func (g *generator) emitAllocU8Runtime() {
	g.line("")
	g.line(".globl __alloc_u8")
	g.line(".type __alloc_u8, @function")
	g.label("__alloc_u8")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("sub rsp, 8")
	g.emit("mov ebx, edi")     // rbx = n
	g.emit("lea edi, [rbx + 4]")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], ebx")    // length prefix
	g.emit("lea rax, [rax + 4]") // data ptr
	g.emit("add rsp, 8")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __alloc_u8, .-__alloc_u8")
}

// emitStringFromBytesRuntime emits `string_from_bytes(bs)` —
// copies a `u8[]` payload into a fresh length-prefixed
// string. Round-trip companion to `s.bytes()`.
func (g *generator) emitStringFromBytesRuntime() {
	g.line("")
	g.line(".globl string_from_bytes")
	g.line(".type string_from_bytes, @function")
	g.label("string_from_bytes")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("mov rbx, rdi")        // bs
	g.emit("mov r12d, [rbx - 4]") // length
	g.emit("lea edi, [r12 + 4]")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], r12d")     // length prefix
	g.emit("lea rdi, [rax + 4]")
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, r12")
	g.emit("push rax")
	g.emit("sub rsp, 8")          // align
	g.emit("call __lang_memcpy")
	g.emit("add rsp, 8")
	g.emit("pop rax")
	g.emit("lea rax, [rax + 4]")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size string_from_bytes, .-string_from_bytes")
}

// emitStrSliceRuntime emits `__str_slice(base, low, high)` —
// returns a fresh string holding `base[low..high]`. Traps on
// out-of-range indices via exit_group(134) (matches arm64's
// strslice trap shape).
func (g *generator) emitStrSliceRuntime() {
	g.line("")
	g.line(".globl __str_slice")
	g.line(".type __str_slice, @function")
	g.label("__str_slice")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("sub rsp, 8") // align
	g.emit("mov rbx, rdi")        // base
	g.emit("mov r12, rsi")        // low
	g.emit("mov r13, rdx")        // high
	g.emit("mov r14d, [rbx - 4]") // src_len
	// Bounds checks: low < 0 OR high > src_len OR low > high → trap.
	g.emit("test r12, r12")
	g.emit("js .Lstrslice_trap")
	g.emit("cmp r13, r14")
	g.emit("ja .Lstrslice_trap")
	g.emit("cmp r12, r13")
	g.emit("jg .Lstrslice_trap")
	// new_len = high - low.
	g.emit("mov rax, r13")
	g.emit("sub rax, r12")
	g.emit("mov r14, rax") // r14 = new_len
	g.emit("lea edi, [r14 + 4]")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], r14d")     // length prefix
	g.emit("lea rdi, [rax + 4]")  // dst
	g.emit("lea rsi, [rbx + r12]") // src = base + low
	g.emit("mov rdx, r14")
	g.emit("push rax")
	g.emit("sub rsp, 8") // align
	g.emit("call __lang_memcpy")
	g.emit("add rsp, 8")
	g.emit("pop rax")
	g.emit("lea rax, [rax + 4]")
	g.emit("add rsp, 8")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.label(".Lstrslice_trap")
	g.emit("mov edi, 134")
	g.emit(fmt.Sprintf("mov eax, %d", sysExitGroup))
	g.emit("syscall")
	g.line(".size __str_slice, .-__str_slice")
}

// escapeForGAS escapes a string for the GAS `.asciz`
// directive. Same shape as the arm64 backend's helper —
// only the minimum set of escapes the runtime strings
// need.
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
