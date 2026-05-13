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
	"sort"
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
	// "tail call" doesn't grow the stack. Wired in on all
	// three backends (x86-64 + arm64 + wasm).
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
	// Zero-capture closures escaping past ElideClosurePair (e.g.
	// passed as a function-typed argument — `tryThing(my_lambda)`)
	// rewrite to OpConstFunc so the value materialises as a
	// `lea rax, [rip + __closure_cell_<name>]` against a static
	// `.rodata` cell instead of a 16-byte heap-allocated pair.
	ir.InlineZeroCaptureClosures(ip)
	g := &generator{info: info, stringLabel: map[string]string{}, funcs: map[string]*ast.FuncDecl{}}
	// Pre-scan call sites for runtime-helper use-flags before
	// touching any code emission, so emitDataSections + the
	// runtime emitters below know which helpers to include
	// (and the .bss reservations match the helpers).
	for _, fn := range prog.Funcs {
		g.funcs[fn.Name] = fn
	}
	// State[T] (program-lifetime no-op): hoist prog.States
	// into the generator so emitDataSections can write the
	// .data/.bss slots, and detect __state_init so _start
	// calls it before main.
	g.stateDecls = prog.States
	if info != nil && len(info.StateVars) > 0 {
		for _, fn := range prog.Funcs {
			if fn.Name == "__state_init" {
				g.usesStateInit = true
				break
			}
		}
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
	// Reader/Writer runtime is emitted as a single bundle: every
	// helper (open_*, read_line, read_chunk, close, write,
	// make_handle, stdin/stdout/stderr) ships together. Drag in
	// the bundle's transitive deps (alloc, memcpy, the IoError
	// box helper) whenever the bundle itself is pulled in.
	if g.usesReaderWriter {
		g.usesIoError = true
		g.usesMemcpy = true
		g.usesAlloc = true
	}
	// Runtime helpers — gated on use-flags so unused programs
	// pay nothing extra in binary size.
	if g.usesAlloc {
		g.emitAllocRuntime()
	}
	if g.usesMemcpy {
		g.emitMemcpyRuntime()
	}
	if g.usesSliceMake {
		g.emitSliceMakeRuntime()
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
	if g.usesEprint {
		g.emitEprintRuntime()
	}
	if g.usesExit {
		g.emitExitRuntime()
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
	if g.usesRawIntPokes {
		g.emitRawIntPokesRuntime()
	}
	if g.usesMemset {
		g.emitMemsetRuntime()
	}
	if g.usesIoError {
		g.emitIoErrorRuntime()
	}
	if g.usesReadFile {
		g.emitReadFileRuntime()
	}
	if g.usesWriteFile {
		g.emitWriteFileRuntime()
	}
	if g.usesReaderWriter {
		// Bundle: open_reader/writer/appender + stdin/stdout/
		// stderr handle constructors + Reader.read_line /
		// read_chunk / close + Writer.write / close +
		// __lang_make_handle + __lang_close_fd_box. Shares
		// __lang_read_line_buf with the stdin-only read_line
		// helper (4 KiB scratch).
		g.emitReaderWriterRuntime()
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
	// usesEprint / usesExit — eprint(s) → stderr write+newline;
	// exit(code) → direct exit_group syscall. Both mirror arm64.
	usesEprint bool
	usesExit   bool
	usesTcp             bool
	usesEnv             bool
	usesArgs            bool
	usesArena           bool
	usesAllocU8         bool
	usesStringFromBytes bool
	usesStrSlice        bool
	usesSliceMake       bool
	usesRandomBytes     bool
	usesReadLine        bool
	// usesStrIdx tracks whether any code emits the SSO-aware
	// inlined __str_idx helper, which spills inline-tagged
	// strings to the .bss `__lang_str_idx_scratch` slot before
	// computing the byte address. Set lazily on first emit
	// from emitInlineIdxHelper; gates the slot's .bss
	// reservation so map-only programs (which use str_idx via
	// their hash routine but don't touch strcat / slice) still
	// link cleanly.
	usesStrIdx bool
	// enumSentinelTags is the set of tag values for which a
	// shared `[tag=N]` sentinel is referenced. Populated lazily
	// when OpEnumSentinel emits; one .rodata symbol per unique
	// tag value gets reserved. Programs that never construct
	// payloadless enum variants skip the entire block.
	enumSentinelTags map[int]bool
	// constFuncCells tracks function names referenced via
	// OpConstFunc. Each gets a 16-byte static .rodata cell
	// `{fn_ptr, 0}` so OpCallIndirect can deref every callee
	// (top-level fn value, runtime-built closure) through a
	// uniform pair shape.
	constFuncCells map[string]bool
	// usesArrEmpty gates the `.LArr_Empty` sentinel — a shared
	// static 4-byte `[length=0]` buffer that __alloc_u8(0)
	// returns instead of allocating a fresh length-only block.
	// Mirrors the .LStr_Empty pattern for the array seam.
	usesArrEmpty bool
	usesStdin           bool
	// usesRawIntPokes pulls in `__store_i32` / `__load_i32` /
	// `__store_ptr` / `__load_ptr` / `__ptr_width` — primitives
	// the lang Map runtime uses for its mixed bucket-index +
	// entries buffer. Single mov + ret each.
	usesRawIntPokes bool
	// usesMemset gates emission of the byte-grain
	// `__memset(dst, byte, n)` helper the Map clear path uses.
	usesMemset bool
	// usesReadFile / usesWriteFile pull in the file-I/O
	// runtimes; usesIoError pulls in the shared
	// `__lang_io_error(errno, path) → IoError box` helper.
	usesReadFile  bool
	usesWriteFile bool
	usesIoError   bool

	// stateDecls + usesStateInit drive State[T] codegen.
	// See the arm64 generator for the program-lifetime / no-op
	// interpretation this PR implements.
	stateDecls    []*ast.StateDecl
	usesStateInit bool

	// usesReaderWriter pulls in the full Reader / Writer
	// runtime bundle (stdin/stdout/stderr + open_reader /
	// open_writer / open_appender + Reader/Writer method
	// helpers). Mirrors the arm64 generator's flag.
	usesReaderWriter bool
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
	case "__slice_make":
		g.usesSliceMake = true
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
	case "eprint":
		g.usesEprint = true
	case "exit":
		g.usesExit = true
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
	case "read_line":
		g.usesReadLine = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "__method_Reader_read_line",
		"__method_Reader_read_chunk",
		"__method_Reader_close",
		"__method_Writer_write",
		"__method_Writer_close",
		"open_reader", "open_writer", "open_appender",
		"stdin", "stdout", "stderr":
		g.usesReaderWriter = true
	case "__store_i32", "__load_i32", "__store_ptr", "__load_ptr", "__ptr_width":
		// Map runtime's raw int/pointer pokes — each lowers
		// to a single mov + ret.
		g.usesRawIntPokes = true
	case "__memset":
		// Byte-grain fill used by the Map clear path.
		g.usesMemset = true
	case "read_file":
		g.usesReadFile = true
		g.usesAlloc = true
		g.usesIoError = true
	case "write_file":
		g.usesWriteFile = true
		g.usesAlloc = true
		g.usesIoError = true
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
	if g.usesStateInit {
		// Run the synthesised state-init body before main.
		// Initialises every non-literal state-block var
		// (literal inits live in .data already).
		g.emit("call __state_init")
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
	// rdi/rsi/rdx/rcx/r8/r9; args 6+ come on the caller's
	// stack at [rbp + 16 + 8*(i-6)] (rbp+0 is saved rbp,
	// rbp+8 is the return address pushed by `call`, args
	// follow immediately).
	regArgs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
	for i := range fn.Params {
		if i < len(regArgs) {
			g.emit(fmt.Sprintf("mov [rbp-%d], %s", (i+1)*8, regArgs[i]))
		} else {
			g.emit(fmt.Sprintf("mov rax, [rbp+%d]", 16+8*(i-len(regArgs))))
			g.emit(fmt.Sprintf("mov [rbp-%d], rax", (i+1)*8))
		}
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
		// Function values materialise as static 16-byte
		// closure-pair cells in .rodata: { fn_ptr (8B),
		// env_ptr=0 (8B) }. This mirrors the wasm closure
		// shape so OpCallIndirect can uniformly deref the
		// pair — load fn from [+0] and env from [+8] — for
		// both top-level fn values (this case, env always 0)
		// and runtime-built closures (OpMakeClosure, env
		// points at a heap env block).
		//
		// Without the cell, OpCallIndirect couldn't tell a
		// raw fn pointer apart from a closure-pair pointer
		// and `call r11` on a pair would jump into the pair's
		// data bytes — exactly the `use`-callback segfault
		// we're fixing.
		cell := fmt.Sprintf("__closure_cell_%s", op.Str)
		if g.constFuncCells == nil {
			g.constFuncCells = map[string]bool{}
		}
		g.constFuncCells[op.Str] = true
		g.emit(fmt.Sprintf("lea rax, [rip + %s]", cell))
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
	case ir.OpReturnPair:
		// Multi-value pair-form return: pop the top operand-
		// stack slot into `rdx` (payload, second SysV return
		// reg), pop the next into `rax` (tag, first SysV
		// return reg), then jump to the epilogue. The
		// function-side ABI is now register-pair — callers
		// (OpCallDirectPair) consume `(rax, rdx)` directly,
		// no heap-box round trip.
		g.pop()             // payload → rax
		g.emit("mov rdx, rax")
		g.pop()             // tag → rax
		g.emit(fmt.Sprintf("jmp %s", retLabel))
	case ir.OpMakeSomeI32, ir.OpMakeOkI32:
		// Native fallback — same heap-box shape as emitEnumNew.
		// `op.Width` selects the payload size: zero (default)
		// means i32 → alloc 8, store payload at +4 (4 bytes).
		// WidthPtr means pointer-shape on this target → alloc
		// 16 (matches `payloadLayout` 8-byte alignment for
		// 8-byte payloads), store payload at +8 (8 bytes). The
		// match-side reader uses the same layout so the heap
		// box round-trips correctly.
		g.emitPairFormMaker(op.Width, 0)
	case ir.OpMakeErrI32:
		// Same shape as Some/Ok but tag=1.
		g.emitPairFormMaker(op.Width, 1)
	case ir.OpMakeNoneI32:
		// Multi-value None: push (tag=1, payload=0) as two
		// operand-stack slots. No alloc, no heap-box pointer.
		g.emit("mov rax, 1")
		g.push() // tag
		g.emit("xor eax, eax")
		g.push() // payload (unused for None)

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
		// Flip the sign bit directly. The earlier `0 - x`
		// shape lost negative zero — `0.0 - 0.0` is `+0.0`
		// per IEEE-754, not `-0.0`, so `f32_bits(-0.0)` came
		// out as `0` instead of the expected `0x80000000`.
		// arm64's hardware `fneg` is sign-bit XOR; matching
		// that semantics keeps round-trips faithful (and
		// avoids a redundant `xorps + subss` per negation).
		//
		// Operand-stack values are stored as raw bits in GP
		// registers (no XMM round-trip needed), so XOR the
		// register directly.
		g.pop()
		if op.Width == 64 {
			g.emit("movabs rcx, 0x8000000000000000")
			g.emit("xor rax, rcx")
		} else {
			g.emit("xor eax, -2147483648") // 0x80000000 as a signed-32 immediate
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
	// Sub-i32 sign-extension. The IR emits these after a
	// narrow store + reload so the value re-enters the i32
	// world with the correct sign. Pairs with wasm's
	// `i32.extend8_s` / `i32.extend16_s`.
	case ir.OpSignExtend8:
		g.pop()
		g.emit("movsx eax, al")
		g.push()
	case ir.OpSignExtend16:
		g.pop()
		g.emit("movsx eax, ax")
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
		// i32 → f32 / f64. x86 only has signed cvtsi2sX;
		// for u32 we zero-extend to 64 bits first (the value
		// fits in i64 since it's at most 2^32-1) and then
		// signed-convert from the 64-bit source.
		g.pop()
		src := "eax"
		if op.Unsigned {
			g.emit("mov eax, eax") // zero-extend u32 to rax
			src = "rax"
		}
		if op.Width == 64 {
			g.emit(fmt.Sprintf("cvtsi2sd xmm0, %s", src))
			g.emit("movq rax, xmm0")
		} else {
			g.emit(fmt.Sprintf("cvtsi2ss xmm0, %s", src))
			g.emit("movd eax, xmm0")
		}
		g.push()
	case ir.OpFConvertI64:
		// i64 → f32 / f64. Signed is a direct cvtsi2sX;
		// unsigned uses a 2-step round-half-to-even trick
		// for values >= 2^63 (where signed conversion would
		// overflow to the negative range).
		g.pop()
		if op.Unsigned {
			// if rax >= 0: signed convert as usual.
			// else (msb set, i.e. value >= 2^63):
			//   y = (rax >> 1) | (rax & 1)
			//   convert y signed, then 2x.
			label := fmt.Sprintf(".Lu64f_%d", g.labelCounter)
			g.labelCounter++
			g.emit("test rax, rax")
			g.emit(fmt.Sprintf("js %s_big", label))
			if op.Width == 64 {
				g.emit("cvtsi2sd xmm0, rax")
			} else {
				g.emit("cvtsi2ss xmm0, rax")
			}
			g.emit(fmt.Sprintf("jmp %s_done", label))
			g.label(label + "_big")
			g.emit("mov rcx, rax")
			g.emit("shr rcx, 1")
			g.emit("and eax, 1")
			g.emit("or rcx, rax")
			if op.Width == 64 {
				g.emit("cvtsi2sd xmm0, rcx")
				g.emit("addsd xmm0, xmm0")
			} else {
				g.emit("cvtsi2ss xmm0, rcx")
				g.emit("addss xmm0, xmm0")
			}
			g.label(label + "_done")
		} else {
			if op.Width == 64 {
				g.emit("cvtsi2sd xmm0, rax")
			} else {
				g.emit("cvtsi2ss xmm0, rax")
			}
		}
		if op.Width == 64 {
			g.emit("movq rax, xmm0")
		} else {
			g.emit("movd eax, xmm0")
		}
		g.push()
	case ir.OpReinterpretI32F32, ir.OpReinterpretF32I32:
		// Bit-cast between f32 and i32. The operand stack
		// already stores both as raw 32-bit values (see
		// OpConstF32 — the f32 bit pattern goes onto the
		// stack via `mov eax, <bits>`), and the consuming
		// op picks the right register bank (general-purpose
		// vs XMM) via `movd` when needed. Nothing to emit.
	case ir.OpITruncF32, ir.OpITruncF64:
		// f32 / f64 → i32 / i64 (truncate toward zero). x86
		// only has signed cvttsX2si; we handle unsigned
		// outputs by:
		//   u32: convert to i64, the low 32 bits are the u32.
		//   u64: if value < 2^63, signed conversion is correct.
		//        Else subtract 2^63 (as a double), convert, then
		//        set bit 63 to add 2^63 back.
		g.pop()
		isF64 := op.Kind == ir.OpITruncF64
		suf := "ss"
		if isF64 {
			suf = "sd"
		}
		if isF64 {
			g.emit("movq xmm0, rax")
		} else {
			g.emit("movd xmm0, eax")
		}
		if op.Unsigned && op.Width == 64 {
			// f → u64 with 2^63 trick.
			label := fmt.Sprintf(".Lf2u64_%d", g.labelCounter)
			g.labelCounter++
			// Load 2^63 as a double / float into xmm1.
			if isF64 {
				g.emit("mov rax, 0x43E0000000000000") // 2^63 as f64
				g.emit("movq xmm1, rax")
				g.emit("ucomisd xmm0, xmm1")
			} else {
				g.emit("mov eax, 0x5F000000") // 2^63 as f32
				g.emit("movd xmm1, eax")
				g.emit("ucomiss xmm0, xmm1")
			}
			g.emit(fmt.Sprintf("jae %s_big", label))
			g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
			g.emit(fmt.Sprintf("jmp %s_done", label))
			g.label(label + "_big")
			if isF64 {
				g.emit("subsd xmm0, xmm1")
			} else {
				g.emit("subss xmm0, xmm1")
			}
			g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
			g.emit("btc rax, 63")
			g.label(label + "_done")
		} else if op.Unsigned {
			// f → u32. Convert to i64 (room for the full
			// u32 range), then read low 32 bits.
			g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
		} else if op.Width == 64 {
			g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
		} else {
			g.emit(fmt.Sprintf("cvtt%s2si eax, xmm0", suf))
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

	// -------- state (module-global) vars --------
	//
	// See arm64 generator for the program-lifetime / no-op
	// interpretation. .data / .bss slot reached via rip-
	// relative addressing; persistent-mode toggles are no-ops.

	case ir.OpLoadGlobal:
		w := stateWidthBytes(g.lookupStateType(op.Str))
		if w == 8 {
			g.emit(fmt.Sprintf("mov rax, [rip + .Lstate_%s]", op.Str))
		} else {
			g.emit(fmt.Sprintf("mov eax, [rip + .Lstate_%s]", op.Str))
		}
		g.push()
	case ir.OpStoreGlobal:
		g.pop()
		w := stateWidthBytes(g.lookupStateType(op.Str))
		if w == 8 {
			g.emit(fmt.Sprintf("mov [rip + .Lstate_%s], rax", op.Str))
		} else {
			g.emit(fmt.Sprintf("mov [rip + .Lstate_%s], eax", op.Str))
		}

	case ir.OpPersistentSet:
		// Toggle the allocator mode byte at __lang_alloc_mode
		// and push the previous mode for the matching Restore.
		// Op.I32 carries the new mode (0 = arena, 1 = persistent).
		g.emit("movzx eax, byte ptr [rip + __lang_alloc_mode]")
		g.emit(fmt.Sprintf("mov byte ptr [rip + __lang_alloc_mode], %d", op.I32&1))
		g.push() // push old mode
		g.usesAlloc = true
	case ir.OpPersistentRestore:
		// Pop the saved mode and write it back to the mode byte.
		g.pop()
		g.emit("mov [rip + __lang_alloc_mode], al")
		g.usesAlloc = true

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
	case ir.OpMatchTag:
		// Transitional lowering — same as OpLoad of an i32
		// tag at offset 0. Step 4 of the Option/Result arc
		// swaps this for a tag-register read when the
		// scrutinee was the pair-form result of an
		// OpCallDirectPair.
		g.pop()
		g.emit("mov eax, [rax]")
		g.push()
	case ir.OpLoadByte:
		g.pop()
		g.emit("movzx eax, byte ptr [rax]")
		g.push()
	// Sub-i32 typed loads. Sign-extend variants use `movsx`,
	// the unsigned 16-bit variant uses `movzx` so the high
	// bits of rax stay clean. Pairs with wasm's
	// `i32.load8_s` / `i32.load16_u` / `i32.load16_s`.
	case ir.OpLoadI8S:
		g.pop()
		g.emit("movsx eax, byte ptr [rax]")
		g.push()
	case ir.OpLoadI16U:
		g.pop()
		g.emit("movzx eax, word ptr [rax]")
		g.push()
	case ir.OpLoadI16S:
		g.pop()
		g.emit("movsx eax, word ptr [rax]")
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

	case ir.OpStrLen:
		// IR-level string-length seam. The Go-level helper
		// emitStrLen owns the actual encoding so this case and
		// every runtime helper that reads a string's length stay
		// in sync as SSO follow-ups change the layout.
		g.pop() // rax = str ptr
		g.emitStrLen("eax", "rax")
		g.push()

	case ir.OpEnumSentinel:
		// Push the address of a shared static `[tag=N]` sentinel.
		// One symbol per unique tag value, lazily reserved in
		// .rodata so programs that never construct payloadless
		// variants pay nothing extra.
		tag := int(op.I32)
		if g.enumSentinelTags == nil {
			g.enumSentinelTags = map[int]bool{}
		}
		g.enumSentinelTags[tag] = true
		g.emit(fmt.Sprintf("lea rax, [rip + .LEnumSentinel_%d]", tag))
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
		// shape as OpCallDirect with one extra arg.
		argc := int(op.I32)
		g.emitCallArgsLoad(argc)
		g.emit(fmt.Sprintf("call %s", op.Str))
		g.emitCallArgsCleanup(argc)
		g.push()

	case ir.OpMakeClosure, ir.OpMakeEnv:
		return g.emitMakeClosureOrEnv(op)

	case ir.OpCallIndirect:
		// Function-value call: the IR emitted a closure-pair
		// pointer immediately before the call op. Every
		// function value on natives is now a {fn_ptr, env_ptr}
		// pair — OpConstFunc emits static .rodata cells with
		// env=0; OpMakeClosure allocates heap pairs whose env
		// points at the captured slot block. Either way, the
		// indirect call:
		//   1. Loads fn_ptr from [pair + 0] into r11.
		//   2. Loads env_ptr from [pair + 8] into a scratch
		//      slot (since __lang_alloc clobbers caller-save
		//      registers, we stage env on the operand stack).
		//   3. Pops user args into rdi / rsi / ... in the
		//      System-V order, then pops env into the next
		//      register (one past the user args).
		//   4. call r11.
		//
		// Top-level fns called this way receive env=0 in an
		// extra register slot they don't read — System V's
		// "arguments unused by the callee may hold any value"
		// rule makes that harmless. Hoisted closures (whose
		// body references the captured env block) read it
		// from the same register.
		argc := int(op.I32)
		g.emit("mov r10, [rsp]") // r10 = pair pointer (caller-save scratch)
		g.emit("add rsp, 16")
		g.emit("mov r11, [r10]")        // r11 = fn_ptr (= [pair + 0])
		g.emit("mov rax, [r10 + 8]")   // rax = env_ptr (= [pair + 8])
		// Push env_ptr onto the operand stack so the args-load
		// helper picks it up in the (argc+1)th register slot.
		g.emit("sub rsp, 16")
		g.emit("mov [rsp], rax")
		g.emitCallArgsLoad(argc + 1)
		g.emit("call r11")
		g.emitCallArgsCleanup(argc + 1)
		g.push()

	case ir.OpCallDirect:
		target := op.Str
		switch target {
		case "__alloc":
			target = "__lang_alloc"
		case "__memcpy":
			target = "__lang_memcpy"
		case "__slice_make":
			target = "__lang_slice_make"
		case "print":
			target = "__lang_puts"
		case "write":
			target = "__lang_write"
		case "putchar":
			target = "__lang_putchar"
		case "eprint":
			target = "__lang_eprint"
		case "exit":
			target = "__lang_exit"
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
		case "read_file":
			target = "__lang_read_file"
		case "write_file":
			target = "__lang_write_file"
		case "random_bytes":
			target = "__lang_random_bytes"
		case "read_line":
			target = "__lang_read_line"
		case "__method_Reader_read_line":
			target = "__lang_reader_read_line"
		case "__method_Reader_read_chunk":
			target = "__lang_reader_read_chunk"
		case "__method_Reader_close",
			"__method_Writer_close":
			target = "__lang_close_fd_box"
		case "__method_Writer_write":
			target = "__lang_writer_write"
		case "open_reader":
			target = "__lang_open_reader"
		case "open_writer":
			target = "__lang_open_writer"
		case "open_appender":
			target = "__lang_open_appender"
		case "stdin":
			target = "__lang_stdin"
		case "stdout":
			target = "__lang_stdout"
		case "stderr":
			target = "__lang_stderr"
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
		// Map / MapIter — the lang Map runtime lives entirely
		// in the lang prelude under `_impl`-suffixed names;
		// user-facing call sites use the unsuffixed mangled
		// name and codegen rewrites here. Mirrors arm64.
		case "map_new":
			target = "map_new_impl"
		case "__method_Map_len":
			target = "__map_len_impl"
		case "__method_Map_has":
			target = "__map_has_impl"
		case "__method_Map_get":
			target = "__map_get_impl"
		case "__method_Map_get_or":
			target = "__map_get_or_impl"
		case "__method_Map_set":
			target = "__map_set_impl"
		case "__method_Map_delete":
			target = "__map_delete_impl"
		case "__method_Map_clear":
			target = "__map_clear_impl"
		case "__method_Map_keys":
			target = "__map_keys_impl"
		case "__method_Map_values":
			target = "__map_values_impl"
		case "__method_Map_iter":
			target = "__map_iter_impl"
		case "__method_MapIter_has_next":
			target = "__mapiter_has_next_impl"
		case "__method_MapIter_key":
			target = "__mapiter_key_impl"
		case "__method_MapIter_value":
			target = "__mapiter_value_impl"
		case "__method_MapIter_advance":
			target = "__mapiter_advance_impl"
		}
		argc := int(op.I32)
		g.emitCallArgsLoad(argc)
		g.emit(fmt.Sprintf("call %s", target))
		g.emitCallArgsCleanup(argc)
		g.push()

	case ir.OpCallDirectPair:
		// Multi-value pair-form call: callee returns (tag,
		// payload) in (rax, rdx) per SysV. Push both directly
		// to the operand stack — no heap-box round trip. The
		// caller may follow with OpStoreLocal / OpMatchTag
		// (scrutinee position) or emitRepackPairAsHeapBox
		// (generic position) — the IR-level "two values post-
		// call" contract is now register-backed.
		argc := int(op.I32)
		g.emitCallArgsLoad(argc)
		g.emit(fmt.Sprintf("call %s", op.Str))
		g.emitCallArgsCleanup(argc)
		g.emit("mov r10, rdx") // stash payload (rdx is volatile)
		g.push()               // push rax (tag)
		g.emit("mov rax, r10")
		g.push()               // push payload

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
// emitPairFormMaker is the shared lowering for
// OpMakeSomeI32 / OpMakeOkI32 / OpMakeErrI32. `width` is the
// IR's `Op.Width` operand (`0` for i32 payload, `ir.WidthPtr`
// for pointer-shape payload); `tag` is the variant index
// (`0` for Some/Ok, `1` for Err). Multi-value return ABI:
// leave (tag, payload) as two operand-stack slots so
// OpReturnPair / OpCallDirectPair can route them through the
// SysV `(rax, rdx)` return-register pair without ever
// materialising a heap box.
func (g *generator) emitPairFormMaker(width int, tag int) {
	_ = width // payload width handled by the in-register move below
	g.pop()                                     // payload → rax
	g.emit("mov rcx, rax")                      // save payload
	g.emit(fmt.Sprintf("mov rax, %d", tag))
	g.push()                                    // push tag
	g.emit("mov rax, rcx")                      // restore payload
	g.push()                                    // push payload
}

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

// slotBytes is the operand-stack slot size. 16 bytes today
// (one i64 value + 8 bytes padding); the padding kept rsp
// 16-byte aligned across every push/pop without a runtime
// parity check, which the SysV calling convention requires
// at every `call`. BACKEND-PARITY perf item #3 plans to halve
// this to 8; the constant centralises the value so the flip
// is a one-line change.
const slotBytes = 16

// push rax onto the operand stack — `slotBytes`-byte slot,
// value at `[rsp]`. The upper bytes are dead today (because
// `slotBytes == 16` and the value fits in 8); the flip in
// step 2 of the packed-operand-stack plan will drop them.
func (g *generator) push() {
	g.emit(fmt.Sprintf("sub rsp, %d", slotBytes))
	g.emit("mov [rsp], rax")
}

// pop into rax — one `slotBytes`-byte slot consumed.
func (g *generator) pop() {
	g.emit("mov rax, [rsp]")
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
}

// emitStrLen loads the i32 length of the string whose data
// pointer lives in srcReg into dstReg. Today this is a 4-byte
// little-endian load from `[srcReg - 4]`. Centralised so every
// string-length read in the runtime helpers (strcat / strcmp /
// __lang_write / __lang_puts / __lang_eprint / __lang_env /
// __lang_tcp_send / __str_slice / __lang_write_file / WASI
// stream-write boundary) flows through one site — when small-
// string-optimisation work changes the string encoding, only
// this function (and its peers in the arm64 + wasm backends)
// needs to learn the new shape. Array-length reads stay open-
// coded because arrays may diverge from strings.
// reg32 maps a 64-bit register name to its low-32-bit counterpart.
// emitStrLen / emitStrInlinePack use the 32-bit form to read /
// write the small-string-optimisation length-and-tag byte without
// needing a 64-bit immediate.
func reg32(r64 string) string {
	switch r64 {
	case "rax":
		return "eax"
	case "rbx":
		return "ebx"
	case "rcx":
		return "ecx"
	case "rdx":
		return "edx"
	case "rsi":
		return "esi"
	case "rdi":
		return "edi"
	case "rbp":
		return "ebp"
	case "rsp":
		return "esp"
	case "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15":
		return r64 + "d"
	}
	// Already 32-bit (or unknown) — pass through. emitStrLen
	// callers pre-x86_64-SSO passed `eax` / `r14d` directly,
	// and we still want those to work as the dst register.
	return r64
}

// ssoTagBit is bit 0 of the LSB byte of an inline-tagged string
// value: 1 means inline, 0 means heap pointer. __lang_alloc returns
// 8-aligned pointers so heap-form values always have LSB clear.
const ssoTagBit = 0x01

// Inline string layout (8-byte little-endian register value):
//
//	byte 0:  (length << 1) | 1   — tag bit (bit 0) + length (bits 1..3, range 1..7)
//	bytes 1..7: up to 7 bytes of string data
//
// Length 0 keeps the .LStr_Empty heap sentinel for now — the inline
// "byte 0 == 1" value would also encode it, but the sentinel was
// already on a hot path and the round-trip is identical.
//
// To read length: (value >> 1) & 7.
// To read byte i (0-indexed): (value >> (8 * (i + 1))) & 0xFF.
// To pack: (data_bytes_le << 8) | ((length << 1) | 1).

// emitStrLen loads the i32 length of the string in srcReg into
// dstReg. Branches on the LSB tag bit to handle both heap form
// (4-byte little-endian prefix at `[srcReg - 4]`) and inline form
// (length stored in bits 1..3 of the value). Centralised as the
// READ-side seam of the SSO encoding family.
func (g *generator) emitStrLen(dstReg, srcReg string) {
	dst32 := reg32(dstReg)
	src32 := reg32(srcReg)
	id := g.labelCounter
	g.labelCounter++
	g.emit(fmt.Sprintf("test %s, 1", src32))
	g.emit(fmt.Sprintf("jnz .Lstrlen_inline_%d", id))
	g.emit(fmt.Sprintf("mov %s, [%s - 4]", dst32, srcReg))
	g.emit(fmt.Sprintf("jmp .Lstrlen_done_%d", id))
	g.label(fmt.Sprintf(".Lstrlen_inline_%d", id))
	if dst32 != src32 {
		g.emit(fmt.Sprintf("mov %s, %s", dst32, src32))
	}
	g.emit(fmt.Sprintf("shr %s, 1", dst32))
	g.emit(fmt.Sprintf("and %s, 7", dst32))
	g.label(fmt.Sprintf(".Lstrlen_done_%d", id))
}

// emitStrDataPtr produces a usable byte pointer to the string's
// data in dstReg. For heap-form inputs (LSB=0), the input IS the
// data pointer, so this is just a `mov dstReg, srcReg`. For
// inline inputs (LSB=1), the data bytes live in the register
// itself; we spill srcReg's 8 bytes to the caller-provided
// `scratchMem` (e.g. `[rbp - 24]`) and return `&scratchMem[1]`
// (skipping the leading length-and-tag byte). The scratch buffer
// must outlive any byte read through dstReg — i.e. the caller
// reserves the slot in its frame and the dstReg pointer is dead
// by function return. Used by every runtime helper that reads
// string bytes directly (strcat memcpy src, strcmp byte loop,
// str_slice memcpy src, syscall iovec base).
func (g *generator) emitStrDataPtr(dstReg, srcReg, scratchMem string) {
	id := g.labelCounter
	g.labelCounter++
	g.emit(fmt.Sprintf("test %s, 1", reg32(srcReg)))
	g.emit(fmt.Sprintf("jnz .Lstrdata_inline_%d", id))
	if dstReg != srcReg {
		g.emit(fmt.Sprintf("mov %s, %s", dstReg, srcReg))
	}
	g.emit(fmt.Sprintf("jmp .Lstrdata_done_%d", id))
	g.label(fmt.Sprintf(".Lstrdata_inline_%d", id))
	g.emit(fmt.Sprintf("mov %s, %s", scratchMem, srcReg))
	g.emit(fmt.Sprintf("lea %s, %s", dstReg, scratchMem))
	g.emit(fmt.Sprintf("add %s, 1", dstReg))
	g.label(fmt.Sprintf(".Lstrdata_done_%d", id))
}

// emitStrInlinePack builds an inline-tagged string in dstReg from
// `len` data bytes pointed to by `srcReg` and a 32-bit length in
// `lenReg32`. Caller guarantees `1 <= length <= 7`; length 0 stays
// on the heap-sentinel path. Performs an unaligned 8-byte load to
// scoop up the data bytes at once (over-reads beyond the end of
// the source if length < 7, but heap allocations are page-padded
// so the over-read is safe in practice; masks the irrelevant high
// bytes back to zero).
//
// Layout produced (low → high):
//
//	byte 0:    (length << 1) | 1
//	bytes 1..length: data
//	bytes (length+1)..7: zero
//
// Clobbers a temp 64-bit register `tempReg` distinct from dstReg /
// srcReg / lenReg32's containing 64-bit reg.
func (g *generator) emitStrInlinePack(dstReg, srcReg, lenReg32, tempReg string) {
	// dstReg = 8-byte unaligned load from srcReg.
	g.emit(fmt.Sprintf("mov %s, [%s]", dstReg, srcReg))
	// Build the byte mask in tempReg = (1 << (lenReg32 * 8)) - 1.
	// We can't shift by a 64-bit count from a 32-bit reg directly,
	// so we go through rcx (the x86 shift-by-CL convention).
	g.emit(fmt.Sprintf("mov ecx, %s", lenReg32))
	g.emit("shl ecx, 3") // ecx = length * 8 (bits to keep)
	g.emit(fmt.Sprintf("mov %s, 1", tempReg))
	g.emit(fmt.Sprintf("shl %s, cl", tempReg))
	g.emit(fmt.Sprintf("sub %s, 1", tempReg)) // tempReg = (1 << (length*8)) - 1
	g.emit(fmt.Sprintf("and %s, %s", dstReg, tempReg))
	// dstReg now holds the `length` data bytes in the low (length*8)
	// bits; shift up by 8 to make room for the tag-and-length byte.
	g.emit(fmt.Sprintf("shl %s, 8", dstReg))
	// tempReg = (length << 1) | 1 — the tag+length byte.
	g.emit(fmt.Sprintf("mov %s, %s", reg32(tempReg), lenReg32))
	g.emit(fmt.Sprintf("shl %s, 1", reg32(tempReg)))
	g.emit(fmt.Sprintf("or %s, 1", reg32(tempReg)))
	g.emit(fmt.Sprintf("or %s, %s", dstReg, tempReg))
}

// emitStrLenStore writes the i32 length in srcReg to the 4-byte
// little-endian length prefix at `[dstReg - 4]`, where dstReg is
// the new string's *data pointer* (one past the prefix). Inverse
// of emitStrLen and the second half of the SSO encoding seam:
// string-producing runtime helpers (strcat / str_slice /
// string_from_bytes / random_bytes / env / tcp_recv / read_line)
// all flow through this one site so future encoding changes that
// affect string construction (e.g. tagged-pointer inline-when-
// short) have a single function to update per backend. Array-
// length stores (`__alloc_u8`, `__lang_args` outer array) stay
// open-coded since arrays may diverge.
func (g *generator) emitStrLenStore(srcReg, dstReg string) {
	g.emit(fmt.Sprintf("mov [%s - 4], %s", dstReg, srcReg))
}

// emitStrEmpty materialises the data pointer of the canonical
// empty-string sentinel into dstReg. The sentinel lives in
// .rodata as a length-prefixed string with length=0, shared
// across all callers and the program lifetime. Used by the
// string-constructing runtime helpers (strcat / str_slice /
// string_from_bytes) to short-circuit the alloc + memcpy +
// length-store sequence when the result is zero bytes — the
// helpers already round-trip through emitStrLenStore /
// emitStrLen, so the returned pointer is indistinguishable from
// a freshly allocated 0-length string. Third member of the SSO
// helper family.
func (g *generator) emitStrEmpty(dstReg string) {
	g.emit(fmt.Sprintf("lea %s, [rip + .LStr_Empty]", dstReg))
}

// emitArrayLen loads the i32 length of the length-prefixed array
// whose data pointer lives in srcReg into dstReg. Today this is a
// 4-byte little-endian load from `[srcReg - 4]`. Centralised seam
// for arrays: parallels emitStrLen but stays distinct because
// arrays may diverge from strings under future layout changes.
// Used by __alloc_u8's siblings and string_from_bytes's input
// length read.
func (g *generator) emitArrayLen(dstReg, srcReg string) {
	g.emit(fmt.Sprintf("mov %s, [%s - 4]", dstReg, srcReg))
}

// emitArrayLenStore writes the i32 length in srcReg to the 4-byte
// little-endian length prefix at `[dstReg - 4]`, where dstReg is
// the new array's *data pointer* (one past the prefix). Inverse
// of emitArrayLen. Used by __alloc_u8 and __lang_args (outer
// string[] container). String length stores stay on
// emitStrLenStore.
func (g *generator) emitArrayLenStore(srcReg, dstReg string) {
	g.emit(fmt.Sprintf("mov [%s - 4], %s", dstReg, srcReg))
}

// emitCallArgsLoad places `argc` operand-stack values into
// System V argument slots. First 6 args go to rdi/rsi/rdx/rcx/
// r8/r9; the rest land on the call stack at [rsp+0], [rsp+8],
// ... in source order. The operand stack uses 16-byte slots;
// the call stack uses 8-byte slots, so overflow args get
// compressed via a call-stack overflow area allocated below
// the operand-stack args.
//
// After this call returns, the caller is responsible for the
// `call` / `call r11` and then `emitCallArgsCleanup` to drop
// both the call-stack overflow AND the operand-stack args.
func (g *generator) emitCallArgsLoad(argc int) {
	regs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
	if argc <= len(regs) {
		for i := argc - 1; i >= 0; i-- {
			g.emit(fmt.Sprintf("mov %s, [rsp]", regs[i]))
			g.emit("add rsp, 16")
		}
		return
	}
	overflow := argc - len(regs)
	// Round overflow*8 to a multiple of 16 to keep rsp
	// 16-aligned at the call site (System V requirement).
	stackSize := ((overflow*8 + 15) / 16) * 16
	g.emit(fmt.Sprintf("sub rsp, %d", stackSize))
	// Register args: arg i at [rsp + stackSize + 16*(argc-1-i)].
	for i := 0; i < len(regs); i++ {
		g.emit(fmt.Sprintf("mov %s, [rsp + %d]", regs[i], stackSize+16*(argc-1-i)))
	}
	// Overflow args: compress 16-byte operand slots into 8-byte
	// call-stack slots. arg i (i >= 6) at operand offset
	// stackSize + 16*(argc-1-i), goes to call-stack [rsp + 8*(i-6)].
	for i := len(regs); i < argc; i++ {
		g.emit(fmt.Sprintf("mov rax, [rsp + %d]", stackSize+16*(argc-1-i)))
		g.emit(fmt.Sprintf("mov [rsp + %d], rax", 8*(i-len(regs))))
	}
}

// emitCallArgsCleanup undoes emitCallArgsLoad's stack
// allocation. Caller passes the same argc.
func (g *generator) emitCallArgsCleanup(argc int) {
	regs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
	if argc <= len(regs) {
		// Args were already popped via per-arg `add rsp, 16`.
		return
	}
	overflow := argc - len(regs)
	stackSize := ((overflow*8 + 15) / 16) * 16
	// Drop call-stack overflow + operand-stack args.
	g.emit(fmt.Sprintf("add rsp, %d", stackSize+16*argc))
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
	case "__str_idx":
		// SSO-aware byte indexing. Heap strings: base + idx is
		// the byte address. Inline strings (LSB=1): spill the
		// register value to a global scratch slot in .bss; the
		// data bytes live at scratch+1..scratch+7, so the
		// byte address is `scratch + 1 + idx`. The scratch
		// slot is overwritten on every inline __str_idx call,
		// but the immediate OpLoadByte that follows in the IR
		// consumes the address before the next call, so there
		// is no observable race even in `a[i] + b[j]` shapes.
		g.usesStrIdx = true
		id := g.labelCounter
		g.labelCounter++
		g.emit("test rax, 1")
		g.emit(fmt.Sprintf("jnz .Lstridx_inline_%d", id))
		g.emit("lea rax, [rax + rcx]")
		g.emit(fmt.Sprintf("jmp .Lstridx_done_%d", id))
		g.label(fmt.Sprintf(".Lstridx_inline_%d", id))
		g.emit("mov [rip + __lang_str_idx_scratch], rax")
		g.emit("lea rax, [rip + __lang_str_idx_scratch]")
		g.emit("add rax, rcx")
		g.emit("add rax, 1")
		g.label(fmt.Sprintf(".Lstridx_done_%d", id))
	case "__arr_idx_2":
		g.emit("lea rax, [rax + rcx*2]")
	case "__arr_idx":
		g.emit("lea rax, [rax + rcx*4]")
	case "__arr_idx_8":
		g.emit("lea rax, [rax + rcx*8]")
	// Slice indexing must first dereference the slice header to
	// recover its data_ptr field (4 bytes at [slice + 0]; len is
	// at +4 but we trust the IR's bounds-check pass to have
	// validated `i < len` upstream). After the deref it's the
	// same stride-add shape as the array helpers.
	case "__slice_idx_1":
		g.emit("mov eax, [rax]") // data_ptr (i32)
		g.emit("add rax, rcx")
	case "__slice_idx_2":
		g.emit("mov eax, [rax]")
		g.emit("lea rax, [rax + rcx*2]")
	case "__slice_idx":
		g.emit("mov eax, [rax]")
		g.emit("lea rax, [rax + rcx*4]")
	case "__slice_idx_8":
		g.emit("mov eax, [rax]")
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
	// Emit static closure-pair cells for every function whose
	// name appeared in an OpConstFunc reference. Each cell is
	// 16 bytes: 8 bytes fn_ptr + 8 bytes env=0. OpCallIndirect
	// derefs these cells to recover (fn, env) just like it does
	// for heap-allocated OpMakeClosure pairs.
	if len(g.constFuncCells) > 0 {
		g.line("")
		g.line(".section .rodata")
		names := make([]string, 0, len(g.constFuncCells))
		for n := range g.constFuncCells {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			g.line(".align 8")
			g.label(fmt.Sprintf("__closure_cell_%s", name))
			g.line(fmt.Sprintf("\t.quad %s", name))
			g.line("\t.quad 0")
		}
	}
	needsEmpty := g.usesStrcat || g.usesStrSlice || g.usesStringFromBytes
	needsEnumSentinels := len(g.enumSentinelTags) > 0
	if len(g.stringOrder) > 0 || g.usesPuts || g.usesEprint || needsEmpty || needsEnumSentinels || g.usesArrEmpty {
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
		if g.usesPuts || g.usesEprint {
			// Trailing newline byte shared by __lang_puts and
			// __lang_eprint. Stored in the same section as the
			// string literals so the loader maps it read-only.
			g.label(".LLangNewline")
			g.line(`	.asciz "\n"`)
		}
		if needsEmpty {
			// Empty-string sentinel. String-constructing runtime
			// helpers (__lang_strcat, __str_slice,
			// string_from_bytes) skip the alloc + memcpy when the
			// result is zero bytes and return this static data
			// pointer instead. Layout matches a length-prefixed
			// string with length=0; the pointer addresses the
			// data byte (which is just a trailing NUL).
			g.line(".align 4")
			g.line("\t.4byte 0")
			g.label(".LStr_Empty")
			g.line(`	.asciz ""`)
		}
		if g.usesArrEmpty {
			// Empty u8[] sentinel — __alloc_u8(0) returns this
			// address instead of allocating a fresh 4-byte
			// length-only buffer. Same shape as .LStr_Empty
			// (4-byte length-zero prefix + a single data byte)
			// but kept distinct so the array seam can evolve
			// independently of the string seam.
			g.line(".align 4")
			g.line("\t.4byte 0")
			g.label(".LArr_Empty")
			g.line("\t.byte 0")
		}
		if needsEnumSentinels {
			// Per-tag enum sentinels. One 4-byte symbol per
			// unique tag value referenced by any payloadless-
			// variant construction (Option.None → tag 1,
			// IoError.Interrupted → tag 4, etc.). Match / try
			// sites read `[ptr + 0]` and get the tag, the same
			// as heap-allocated boxes.
			tags := make([]int, 0, len(g.enumSentinelTags))
			for t := range g.enumSentinelTags {
				tags = append(tags, t)
			}
			sort.Ints(tags)
			for _, t := range tags {
				g.line(".align 4")
				g.line(fmt.Sprintf(".LEnumSentinel_%d:", t))
				g.line(fmt.Sprintf("\t.4byte %d", t))
			}
		}
	}
	// SSO inline strings ride in a 64-bit register and don't
	// have a usable memory address until materialised. The
	// __str_idx index helper spills inline values to this
	// global scratch slot before computing `&scratch[1 + idx]`
	// so callers (OpLoadByte after OpCallDirect __str_idx)
	// see a real byte address. Single 8-byte slot, single
	// writer at a time (lang is single-threaded; the inline-
	// path spill is overwritten on each call, but the value
	// is consumed immediately by OpLoadByte before the next
	// __str_idx fires).
	if g.usesAlloc || g.usesEnv || g.usesArgs || g.usesReadLine || g.usesReaderWriter || g.usesStrIdx {
		g.line("")
		g.line(".section .bss")
		if g.usesAlloc {
			// Two-cursor bump allocator. See arm64 + the x86-64
			// emitAllocRuntime comment.
			g.line(".align 8")
			g.label("__lang_heap_ptr")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__lang_heap_end")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__lang_persistent_ptr")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__lang_persistent_end")
			g.line("\t.quad 0")
			g.line(".align 4")
			g.label("__lang_alloc_mode")
			g.line("\t.byte 0")
		}
		if g.usesEnv {
			g.line(".align 8")
			g.label("__lang_envp")
			g.line("\t.quad 0")
		}
		if g.usesStrIdx {
			g.line(".align 8")
			g.label("__lang_str_idx_scratch")
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
		if g.usesReadLine || g.usesReaderWriter {
			// 4 KiB scratch buffer for the byte-by-byte
			// read loop. Shared by stdin-only
			// __lang_read_line and the Reader-receiving
			// __lang_reader_read_line.
			g.line(".align 8")
			g.label("__lang_read_line_buf")
			g.line("\t.space 4096")
		}
	}
	// State[T] globals — emitted after the runtime .bss
	// reservations so the layout is grouped (and any future
	// link-time inspection sees state vars together).
	g.emitStateGlobals()
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
//
// Two-cursor allocator: a 1-byte `__lang_alloc_mode` selects
// which region to bump. mode 0 → arena (per-request, scoped by
// arena_save / arena_restore). mode 1 → persistent (lives for
// the program lifetime). See the arm64 generator's
// `emitAllocRuntime` comment for the full rationale.
func (g *generator) emitAllocRuntime() {
	const heapBytes = 64 * 1024 * 1024 // 64 MiB per region
	g.line("")
	g.line(".globl __lang_alloc")
	g.line(".type __lang_alloc, @function")
	g.label("__lang_alloc")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	// rbx, r12, r13 are callee-save in System V — save all
	// three up-front so we can use them as scratch without
	// stepping on the caller. r13 in particular holds the
	// mmap address hint between the label-pick and the
	// (possibly skipped) mmap call.
	g.emit("push rbx") // holds &ptr (heap or persistent cursor)
	g.emit("push r12") // holds &end
	g.emit("push r13") // holds mmap address hint
	g.emit("add rdi, 15")
	g.emit("and rdi, -16")
	g.emit("movzx eax, byte ptr [rip + __lang_alloc_mode]")
	g.emit("test eax, eax")
	g.emit("jnz .Lalloc_pick_persistent")
	g.emit("lea rbx, [rip + __lang_heap_ptr]")
	g.emit("lea r12, [rip + __lang_heap_end]")
	g.emit("mov r13d, 0x10000000") // arena hint (256 MiB)
	g.emit("jmp .Lalloc_have_labels")
	g.label(".Lalloc_pick_persistent")
	g.emit("lea rbx, [rip + __lang_persistent_ptr]")
	g.emit("lea r12, [rip + __lang_persistent_end]")
	g.emit("mov r13d, 0x20000000") // persistent hint (512 MiB)
	g.label(".Lalloc_have_labels")
	g.emit("mov rax, [rbx]")
	g.emit("test rax, rax")
	g.emit("jnz .Lalloc_have_heap")
	// Lazy mmap. Stash size across the syscall.
	g.emit("push rdi")
	g.emit("sub rsp, 8") // 16-byte align with the four pushes above
	g.emit("mov rdi, r13")
	g.emit(fmt.Sprintf("mov esi, %d", heapBytes))
	g.emit("mov edx, 3")
	g.emit("mov r10d, 0x22")
	g.emit("mov r8d, -1")
	g.emit("xor r9d, r9d")
	g.emit(fmt.Sprintf("mov eax, %d", sysMmap))
	g.emit("syscall")
	g.emit("add rsp, 8")
	g.emit("pop rdi")
	g.emit("cmp rax, 0")
	g.emit("jl .Lalloc_oom")
	g.emit("mov [rbx], rax")
	g.emit("lea rcx, [rax + " + fmt.Sprintf("%d", heapBytes) + "]")
	g.emit("mov [r12], rcx")
	g.label(".Lalloc_have_heap")
	g.emit("mov rax, [rbx]")
	g.emit("lea rcx, [rax + rdi]")
	g.emit("cmp rcx, [r12]")
	g.emit("ja .Lalloc_oom")
	g.emit("mov [rbx], rcx")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.label(".Lalloc_oom")
	g.emit("mov edi, 137")
	g.emit(fmt.Sprintf("mov eax, %d", sysExitGroup))
	g.emit("syscall")
	g.line(".size __lang_alloc, .-__lang_alloc")
}

// emitSliceMakeRuntime emits `__lang_slice_make(data, len)`:
// allocate an 8-byte slice header [data_ptr, len] on the bump
// heap and return its address. The IR's slice-construction path
// (per `*ast.SliceExpr` and `*ast.IndexExpr` write side) calls
// this helper to materialise the header — element indexing is
// inlined as a stride-aware `data_ptr + i * N` via the existing
// __slice_idx_N inline helpers, so there's no per-stride
// dispatch needed here.
//
// Header layout matches the wasm runtime ($__slice_make) so the
// IR's `len(slice)` shape (`[slice + 4]`) and field-load offsets
// stay backend-agnostic: 4 bytes data_ptr, 4 bytes len, 8 bytes
// total. This relies on heap addresses fitting in 32 bits —
// true today for x86-64 Linux + arm64 Linux qemu; arm64-darwin's
// >4 GiB heap is a documented limitation tracked in CLAUDE.md.
//
// Calling convention: rdi = data_ptr (post-stride-offset),
// rsi = len. Returns slice header address in rax. Calls
// __lang_alloc which clobbers rcx / rdx / rsi / rdi (caller-
// save), so we stash both inputs in r12 / r13 around the alloc
// — same trick the strcat / env / args helpers use.
func (g *generator) emitSliceMakeRuntime() {
	g.line("")
	g.line(".globl __lang_slice_make")
	g.line(".type __lang_slice_make, @function")
	g.label("__lang_slice_make")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("mov r12, rdi") // save data_ptr
	g.emit("mov r13, rsi") // save len
	g.emit("mov edi, 8")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], r12d")     // [+0..+3] data_ptr (i32)
	g.emit("mov [rax + 4], r13d") // [+4..+7] len (i32)
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("ret")
	g.line(".size __lang_slice_make, .-__lang_slice_make")
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
	// every one we touch. Plus 24 bytes of frame-local scratch
	// for the SSO encoding family:
	//   [rbp - 48]: 8-byte spill slot for emitStrDataPtr(a)
	//   [rbp - 56]: 8-byte spill slot for emitStrDataPtr(b)
	//   [rbp - 64]: 8-byte buffer where the inline output is
	//               built byte-by-byte before being loaded into
	//               rax as the return value.
	// Frame total: 16 (rbp + return) + 40 (5 callee-saves) +
	// 24 (scratch) = 80 bytes, which is 16-aligned at the call
	// sites below.
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("push r15")
	g.emit("sub rsp, 24")
	g.emit("mov r12, rdi") // r12 = a (may be inline-tagged)
	g.emit("mov r13, rsi") // r13 = b (may be inline-tagged)
	// String lengths via the centralised helper.
	g.emitStrLen("r14d", "r12")
	g.emitStrLen("r15d", "r13")
	// Short-circuit on combined length == 0: return the shared
	// empty-string sentinel without allocating a fresh 0-byte
	// buffer. The sentinel round-trips through emitStrLen as 0,
	// so callers can't tell the difference.
	g.emit("mov eax, r14d")
	g.emit("or eax, r15d")
	g.emit("jnz .Lstrcat_nonzero")
	g.emitStrEmpty("rax")
	g.emit("jmp .Lstrcat_ret")
	g.label(".Lstrcat_nonzero")
	// Total length. If <= 7, build inline output (no alloc);
	// else fall through to the heap path.
	g.emit("mov ecx, r14d")
	g.emit("add ecx, r15d")
	g.emit("cmp ecx, 7")
	g.emit("jg .Lstrcat_heap")
	// --- Inline output path ---
	// Zero the scratch output buffer (so unused trailing bytes
	// after b's data read as zero in the final 8-byte load).
	g.emit("mov qword ptr [rbp - 64], 0")
	// Length-and-tag byte = (total << 1) | 1 at [rbp - 64].
	g.emit("mov edx, ecx")
	g.emit("shl edx, 1")
	g.emit("or edx, 1")
	g.emit("mov byte ptr [rbp - 64], dl")
	// Materialise a / b to byte pointers (heap inputs pass
	// through; inline inputs spill to the per-operand scratch
	// slot and the pointer addresses the first data byte).
	g.emitStrDataPtr("r12", "r12", "[rbp - 48]")
	g.emitStrDataPtr("r13", "r13", "[rbp - 56]")
	// memcpy([rbp - 63], a_data, la) — a's bytes after the
	// length-and-tag byte.
	g.emit("lea rdi, [rbp - 63]")
	g.emit("mov rsi, r12")
	g.emit("mov rdx, r14")
	g.emit("call __lang_memcpy")
	// memcpy([rbp - 63 + la], b_data, lb).
	g.emit("lea rdi, [rbp - 63]")
	g.emit("add rdi, r14")
	g.emit("mov rsi, r13")
	g.emit("mov rdx, r15")
	g.emit("call __lang_memcpy")
	// Load the full 8-byte inline value (length byte + 7 data
	// bytes + zero padding) into rax.
	g.emit("mov rax, [rbp - 64]")
	g.emit("jmp .Lstrcat_ret")
	g.label(".Lstrcat_heap")
	// --- Heap output path ---
	// alloc(la + lb + 5) — 4 prefix + N data + 1 NUL.
	g.emit("lea rdi, [r14 + r15 + 5]")
	g.emit("call __lang_alloc")
	// rax = base; data ptr = base + 4. Stash dst in rbx
	// (callee-save) so it survives both __lang_memcpy
	// calls, then return it at the end.
	g.emit("lea rbx, [rax + 4]")
	// Combined length, then route through emitStrLenStore.
	g.emit("mov ecx, r14d")
	g.emit("add ecx, r15d")
	g.emitStrLenStore("ecx", "rbx")
	// Materialise a / b for the memcpy reads.
	g.emitStrDataPtr("r12", "r12", "[rbp - 48]")
	g.emitStrDataPtr("r13", "r13", "[rbp - 56]")
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
	g.label(".Lstrcat_ret")
	g.emit("add rsp, 24")
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
	// Frame: 32 bytes — saved rbp (8) + 16 bytes for two
	// emitStrDataPtr scratch slots (one per operand) + 8 bytes
	// padding to keep rsp 16-aligned for `rep cmpsb`. Two slots
	// are needed because both operands may be inline at the same
	// time (different inline values of equal length).
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 32")
	// Same value? Equal — covers both same-heap-pointer and
	// both-inline-same-bits cases.
	g.emit("cmp rdi, rsi")
	g.emit("je .Lscmp_eq")
	// Same length?
	g.emitStrLen("ecx", "rdi")
	g.emitStrLen("edx", "rsi")
	g.emit("cmp ecx, edx")
	g.emit("jne .Lscmp_neq")
	// Convert each operand to a byte pointer — heap inputs pass
	// through; inline inputs spill to a frame scratch slot and
	// the returned pointer addresses the first data byte.
	g.emitStrDataPtr("rdi", "rdi", "[rbp - 16]")
	g.emitStrDataPtr("rsi", "rsi", "[rbp - 32]")
	// rep cmpsb wants the count in rcx and the pointers in
	// rsi (source 1) / rdi (source 2). cld → forward.
	g.emit("cld")
	g.emit("repe cmpsb")
	g.emit("jne .Lscmp_neq")
	g.label(".Lscmp_eq")
	g.emit("xor eax, eax")
	g.emit("add rsp, 32")
	g.emit("pop rbp")
	g.emit("ret")
	g.label(".Lscmp_neq")
	g.emit("mov eax, 1")
	g.emit("add rsp, 32")
	g.emit("pop rbp")
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
	g.emit("sub rsp, 16") // 8 bytes scratch for emitStrDataPtr + 8 alignment
	g.emit("mov r12, rdi")        // r12 = original string value (saved for return)
	// write(1, s, len(s))
	g.emitStrLen("edx", "rdi")    // length
	g.emitStrDataPtr("rsi", "rdi", "[rbp - 16]")
	g.emit("mov edi, 1")          // fd = stdout
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	// write(1, "\n", 1)
	g.emit("lea rsi, [rip + .LLangNewline]")
	g.emit("mov edx, 1")
	g.emit("mov edi, 1")
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("mov rax, r12")        // return the original string value (heap or inline)
	g.emit("add rsp, 16")
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
	// Frame: 16 bytes — 8 bytes scratch slot for emitStrDataPtr
	// + 8 bytes for the saved original string value (so we can
	// return it after the syscall clobbers caller-save regs).
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")
	g.emit("mov [rbp - 8], rdi")  // save original
	g.emitStrLen("edx", "rdi")    // length
	g.emitStrDataPtr("rsi", "rdi", "[rbp - 16]")
	g.emit("mov edi, 1")          // fd = stdout
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("mov rax, [rbp - 8]")  // return original
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_write, .-__lang_write")
}

// emitEprintRuntime emits `__lang_eprint(s)` — stderr
// counterpart to print(). Two write(2)s to fd=2: the string,
// then a newline. Mirrors __lang_puts modulo the fd.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".globl __lang_eprint")
	g.line(".type __lang_eprint, @function")
	g.label("__lang_eprint")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push r12")
	g.emit("sub rsp, 16") // 8 bytes scratch for emitStrDataPtr + 8 alignment
	g.emit("mov r12, rdi")        // r12 = original string value (preserved for return)
	g.emitStrLen("edx", "rdi")
	g.emitStrDataPtr("rsi", "rdi", "[rbp - 16]")
	g.emit("mov edi, 2")          // fd = stderr
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("lea rsi, [rip + .LLangNewline]")
	g.emit("mov edx, 1")
	g.emit("mov edi, 2")
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("mov rax, r12")
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_eprint, .-__lang_eprint")
}

// emitExitRuntime emits `__lang_exit(code)` — direct exit
// syscall. rdi already holds the user-supplied exit code from
// the System V arg-pop. exit_group never returns; the trailing
// `ret` is assembler-completeness only.
func (g *generator) emitExitRuntime() {
	g.line("")
	g.line(".globl __lang_exit")
	g.line(".type __lang_exit, @function")
	g.label("__lang_exit")
	// rdi already holds the exit code (System V arg 1).
	g.emit(fmt.Sprintf("mov eax, %d", sysExitGroup))
	g.emit("syscall")
	g.emit("ret")
	g.line(".size __lang_exit, .-__lang_exit")
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
	g.emitStrLenStore("eax", "r13") // length prefix
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
	// Frame: 16 bytes — 8 scratch slot for emitStrDataPtr + 8
	// alignment. The materialisation lets an inline-tagged
	// `data` value yield a real byte pointer for the syscall.
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")
	g.emitStrLen("edx", "rsi")                 // length from data
	g.emitStrDataPtr("rsi", "rsi", "[rbp - 8]") // byte pointer for syscall
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("syscall")
	g.emit("add rsp, 16")
	g.emit("pop rbp")
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
	g.emit("push r12") // name byte ptr (materialised, see below)
	g.emit("push r13") // name length
	g.emit("push r14") // value data ptr (post-strcat)
	g.emit("push r15") // value length
	g.emit("sub rsp, 24") // 8 bytes scratch for emitStrDataPtr + 16 padding
	g.emitStrLen("r13d", "rdi")              // r13 = name length (rdi = caller's value)
	g.emitStrDataPtr("r12", "rdi", "[rbp - 48]") // r12 = name byte pointer
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
	g.emit("lea rdi, [rax + 4]") // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("r15d", "rdi")
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
	g.emit("add rsp, 24")
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
	// Outer string[] container length via the array seam (the
	// per-element string stores in the loop below use
	// emitStrLenStore).
	g.emitArrayLenStore("ebx", "r14")
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
	g.emit("lea rdi, [rax + 4]")    // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("edx", "rdi") // length prefix
	// memcpy(data, argv[i], strlen + 1) — include NUL.
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
	g.emitStrLenStore("ebx", "r12")  // length prefix
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
	g.emit("lea r13, [rax + 4]")     // r13 = data ptr
	g.emitStrLenStore("r12d", "r13") // length prefix
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
	// Short-circuit on n == 0: return the shared static empty-
	// array sentinel rather than allocating a fresh 4-byte
	// length-only buffer.
	g.emit("test ebx, ebx")
	g.emit("jnz .Lallocu8_alloc")
	g.usesArrEmpty = true
	g.emit("lea rax, [rip + .LArr_Empty]")
	g.emit("jmp .Lallocu8_ret")
	g.label(".Lallocu8_alloc")
	g.emit("lea edi, [rbx + 4]")
	g.emit("call __lang_alloc")
	g.emit("lea rax, [rax + 4]") // rax = data ptr
	g.emitArrayLenStore("ebx", "rax")
	g.label(".Lallocu8_ret")
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
	// 16 bytes scratch: [rbp - 24] inline output buffer + 8 padding.
	g.emit("sub rsp, 16")
	g.emit("mov rbx, rdi")        // bs (input u8[] array)
	g.emitArrayLen("r12d", "rbx") // r12 = input array length
	// Short-circuit on input length == 0: return the shared
	// empty-string sentinel without allocating a fresh 0-byte
	// buffer.
	g.emit("test r12d, r12d")
	g.emit("jnz .Lsfb_nonempty")
	g.emitStrEmpty("rax")
	g.emit("jmp .Lsfb_ret")
	g.label(".Lsfb_nonempty")
	// length <= 7? Pack into inline-tagged register value, no alloc.
	g.emit("cmp r12d, 7")
	g.emit("jg .Lsfb_heap")
	// --- Inline output path ---
	g.emit("mov qword ptr [rbp - 24], 0")
	g.emit("mov edx, r12d")
	g.emit("shl edx, 1")
	g.emit("or edx, 1")
	g.emit("mov byte ptr [rbp - 24], dl") // length-and-tag byte
	g.emit("lea rdi, [rbp - 23]")
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, r12")
	g.emit("call __lang_memcpy")
	g.emit("mov rax, [rbp - 24]")
	g.emit("jmp .Lsfb_ret")
	g.label(".Lsfb_heap")
	g.emit("lea edi, [r12 + 4]")
	g.emit("call __lang_alloc")
	g.emit("lea rdi, [rax + 4]")    // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("r12d", "rdi") // length prefix
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, r12")
	g.emit("push rdi")            // save data ptr across memcpy
	g.emit("sub rsp, 8")          // align
	g.emit("call __lang_memcpy")
	g.emit("add rsp, 8")
	g.emit("pop rax")             // rax = data ptr (return value)
	g.label(".Lsfb_ret")
	g.emit("add rsp, 16")
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
	// 16 bytes scratch: [rbp - 40] for emitStrDataPtr(base) and
	// [rbp - 48] for the inline output buffer.
	g.emit("sub rsp, 16")
	g.emit("mov rbx, rdi")        // base (possibly inline-tagged)
	g.emit("mov r12, rsi")        // low
	g.emit("mov r13, rdx")        // high
	g.emitStrLen("r14d", "rbx")   // src_len
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
	// Short-circuit on new_len == 0: return the shared empty-
	// string sentinel rather than allocating a fresh 0-byte
	// buffer.
	g.emit("test r14, r14")
	g.emit("jnz .Lstrslice_nonempty")
	g.emitStrEmpty("rax")
	g.emit("jmp .Lstrslice_ret")
	g.label(".Lstrslice_nonempty")
	// Materialise base → byte ptr (heap inputs pass through; inline
	// inputs spill to [rbp - 40] and rbx points to the first data byte).
	g.emitStrDataPtr("rbx", "rbx", "[rbp - 40]")
	// new_len <= 7? build inline output without allocating.
	g.emit("cmp r14, 7")
	g.emit("jg .Lstrslice_heap")
	// --- Inline output path ---
	// Zero scratch output buffer so unused trailing bytes are 0.
	g.emit("mov qword ptr [rbp - 48], 0")
	// Length-and-tag byte at [rbp - 48].
	g.emit("mov edx, r14d")
	g.emit("shl edx, 1")
	g.emit("or edx, 1")
	g.emit("mov byte ptr [rbp - 48], dl")
	// memcpy([rbp - 47], base_byte_ptr + low, new_len).
	g.emit("lea rdi, [rbp - 47]")
	g.emit("lea rsi, [rbx + r12]")
	g.emit("mov rdx, r14")
	g.emit("call __lang_memcpy")
	g.emit("mov rax, [rbp - 48]")
	g.emit("jmp .Lstrslice_ret")
	g.label(".Lstrslice_heap")
	// --- Heap output path ---
	g.emit("lea edi, [r14 + 4]")
	g.emit("call __lang_alloc")
	g.emit("lea rdi, [rax + 4]")    // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("r14d", "rdi") // length prefix
	g.emit("lea rsi, [rbx + r12]") // src = base_byte_ptr + low
	g.emit("mov rdx, r14")
	g.emit("push rdi")              // save data ptr
	g.emit("sub rsp, 8") // align
	g.emit("call __lang_memcpy")
	g.emit("add rsp, 8")
	g.emit("pop rax")               // rax = data ptr
	g.label(".Lstrslice_ret")
	g.emit("add rsp, 16")
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

// emitRawIntPokesRuntime emits `__store_i32(addr, val)` /
// `__load_i32(addr) -> i32` / `__store_ptr(addr, val)` /
// `__load_ptr(addr) -> i64` / `__ptr_width() -> i32 (=8)`.
// The lang Map runtime calls these for its mixed bucket-index
// + entries buffer where the caller owns the layout (no
// length prefix). Mirrors arm64's `emitRawIntPokesRuntime`;
// single mov + ret each.
func (g *generator) emitRawIntPokesRuntime() {
	g.line("")
	g.line(".globl __load_i32")
	g.line(".type __load_i32, @function")
	g.label("__load_i32")
	g.emit("mov eax, [rdi]")
	g.emit("ret")
	g.line(".size __load_i32, .-__load_i32")

	g.line("")
	g.line(".globl __store_i32")
	g.line(".type __store_i32, @function")
	g.label("__store_i32")
	g.emit("mov [rdi], esi")
	g.emit("ret")
	g.line(".size __store_i32, .-__store_i32")

	g.line("")
	g.line(".globl __load_ptr")
	g.line(".type __load_ptr, @function")
	g.label("__load_ptr")
	g.emit("mov rax, [rdi]")
	g.emit("ret")
	g.line(".size __load_ptr, .-__load_ptr")

	g.line("")
	g.line(".globl __store_ptr")
	g.line(".type __store_ptr, @function")
	g.label("__store_ptr")
	g.emit("mov [rdi], rsi")
	g.emit("ret")
	g.line(".size __store_ptr, .-__store_ptr")

	// `__ptr_width()` returns 8 on x86-64. The Map runtime
	// uses this to size per-entry key/value slots; pairs with
	// the wasm backend's `i32.const 4`.
	g.line("")
	g.line(".globl __ptr_width")
	g.line(".type __ptr_width, @function")
	g.label("__ptr_width")
	g.emit("mov eax, 8")
	g.emit("ret")
	g.line(".size __ptr_width, .-__ptr_width")
}

// emitMemsetRuntime emits `__memset(dst, byte, n)` — byte-
// grain fill matching the wasm bulk-memory shim and arm64's
// helper. 8-byte word loop with the byte replicated across
// all eight lanes; byte-grain tail for the residue.
// rdi = dst, sil (low 8 of rsi) = byte value, rdx = n.
func (g *generator) emitMemsetRuntime() {
	g.line("")
	g.line(".globl __memset")
	g.line(".type __memset, @function")
	g.label("__memset")
	g.emit("movzx ecx, sil")          // ecx = byte (zero-extended)
	g.emit("mov rax, 0x0101010101010101")
	g.emit("imul rax, rcx")           // rax = byte replicated 8x
	g.label(".Lmset_word")
	g.emit("cmp rdx, 8")
	g.emit("jb .Lmset_tail")
	g.emit("mov [rdi], rax")
	g.emit("add rdi, 8")
	g.emit("sub rdx, 8")
	g.emit("jmp .Lmset_word")
	g.label(".Lmset_tail")
	g.emit("test rdx, rdx")
	g.emit("je .Lmset_done")
	g.emit("mov [rdi], al")
	g.emit("inc rdi")
	g.emit("dec rdx")
	g.emit("jmp .Lmset_tail")
	g.label(".Lmset_done")
	g.emit("ret")
	g.line(".size __memset, .-__memset")
}

// emitIoErrorRuntime emits `__lang_io_error(errno, path) → ptr`
// — constructs an `IoError` enum box for a Linux errno. Mirrors
// arm64's emitIoErrorRuntime; same layout (16-byte box with
// pointer-payload at +8, 8-byte tag-only box for payload-less
// variants, 24-byte for Other(path, msg)).
//
// Tag mapping matches the checker's variant declaration order:
//
//	0 NotFound(string)        4 Interrupted
//	1 PermissionDenied(s)     5 Unsupported
//	2 AlreadyExists(s)        6 Other(string, string)
//	3 InvalidUtf8(s)
//
// errno → variant:
//
//	ENOENT (2)  → NotFound          EACCES (13) → PermissionDenied
//	EEXIST (17) → AlreadyExists     EINTR  (4)  → Interrupted
//	default     → Other(path, "")
//
// System V: rdi=errno, rsi=path; result in rax.
func (g *generator) emitIoErrorRuntime() {
	g.line("")
	g.line(".globl __lang_io_error")
	g.line(".type __lang_io_error, @function")
	g.label("__lang_io_error")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")  // callee-save
	g.emit("push r12")  // callee-save
	g.emit("sub rsp, 8") // 16-byte align
	g.emit("mov ebx, edi") // ebx = errno
	g.emit("mov r12, rsi") // r12 = path

	g.emit("cmp ebx, 2")
	g.emit("je .Lioe_notfound")
	g.emit("cmp ebx, 13")
	g.emit("je .Lioe_perm")
	g.emit("cmp ebx, 17")
	g.emit("je .Lioe_exists")
	g.emit("cmp ebx, 4")
	g.emit("je .Lioe_intr")

	// Other(path, ""). 24-byte box: tag, pad, path, "".
	g.emit("mov edi, 24")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 6")
	g.emit("mov [rax + 8], r12")
	g.emit("lea rcx, [rip + .LStr_ioerr_empty]")
	g.emit("mov [rax + 16], rcx")
	g.emit("jmp .Lioe_done")

	g.label(".Lioe_notfound")
	g.emit("xor ebx, ebx")
	g.emit("jmp .Lioe_with_path")
	g.label(".Lioe_perm")
	g.emit("mov ebx, 1")
	g.emit("jmp .Lioe_with_path")
	g.label(".Lioe_exists")
	g.emit("mov ebx, 2")
	g.emit("jmp .Lioe_with_path")
	g.label(".Lioe_intr")
	g.emit("mov edi, 8")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 4")
	g.emit("jmp .Lioe_done")

	g.label(".Lioe_with_path")
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], ebx")     // tag
	g.emit("mov [rax + 8], r12") // path

	g.label(".Lioe_done")
	g.emit("add rsp, 8")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_io_error, .-__lang_io_error")

	// Compile-time empty-string literal for the Other variant.
	// Length=0 prefix + NUL — same layout as user-facing string
	// literals so len("") works and the data is well-formed.
	g.line(".section .rodata")
	g.line(".align 4")
	g.line("\t.4byte 0")
	g.label(".LStr_ioerr_empty")
	g.line("\t.byte 0")
	g.line(".text")
}

// emitReadFileRuntime emits `__lang_read_file(path) →
// Result[string, IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_RDONLY) → fstat → alloc length-prefixed buffer → read-loop
// → close → Result.Ok(string). Syscall errors short-circuit to
// Result.Err via __lang_io_error.
//
// Result box (matches IR):
//
//	tag=0 (Ok)  → payload@+8 = string data ptr
//	tag=1 (Err) → payload@+8 = IoError box ptr
//
// Linux x86-64 syscalls: openat=257, read=0, close=3, fstat=5.
// struct stat: st_size at offset 48 on both arm64 + x86-64.
func (g *generator) emitReadFileRuntime() {
	g.line("")
	g.line(".globl __lang_read_file")
	g.line(".type __lang_read_file, @function")
	g.label("__lang_read_file")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // path byte ptr (materialised below)
	g.emit("push r12") // fd
	g.emit("push r13") // buf base
	g.emit("push r14") // size
	g.emit("push r15") // bytes_read
	// Frame: 168 bytes — 144-byte stat buf (at rsp..rsp+143)
	// + 8 bytes scratch slot for emitStrDataPtr on the path
	// at [rbp - 48] + 16 bytes padding. Keeps rsp 16-aligned
	// at all call sites below.
	g.emit("sub rsp, 168")
	g.emit("mov [rbp - 56], rdi") // save original path string value for the err path
	g.emitStrDataPtr("rbx", "rdi", "[rbp - 48]") // path byte ptr for openat

	// openat(AT_FDCWD=-100, path, O_RDONLY=0, 0)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("xor edx, edx")
	g.emit("xor r10d, r10d")
	g.emit("mov eax, 257")
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lrf_err_open")
	g.emit("mov r12, rax") // fd

	// fstat(fd, [rsp]) — statbuf at top of stack (152 bytes).
	g.emit("mov edi, r12d")
	g.emit("mov rsi, rsp")
	g.emit("mov eax, 5")
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lrf_err_close")
	g.emit("mov r14, [rsp + 48]") // st_size

	// alloc string buf: 4 + size, r13 = data ptr (one past length prefix).
	g.emit("lea rdi, [r14 + 4]")
	g.emit("call __lang_alloc")
	g.emit("lea r13, [rax + 4]")
	g.emitStrLenStore("r14d", "r13")

	g.emit("xor r15, r15") // bytes_read = 0
	g.label(".Lrf_loop")
	g.emit("cmp r15, r14")
	g.emit("jge .Lrf_done")
	g.emit("mov edi, r12d")
	g.emit("lea rsi, [r13 + r15]")
	g.emit("mov rdx, r14")
	g.emit("sub rdx, r15")
	g.emit("xor eax, eax") // read = 0
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lrf_err_close")
	g.emit("jz .Lrf_done")  // EOF (file shrunk between fstat and read)
	g.emit("add r15, rax")
	g.emit("jmp .Lrf_loop")

	g.label(".Lrf_done")
	g.emit("mov edi, r12d")
	g.emit("mov eax, 3") // close
	g.emit("syscall")
	// Result.Ok(string): 16-byte box, tag=0 @0, str_ptr @8.
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0")
	g.emit("mov [rax + 8], r13") // r13 is already the data ptr
	g.emit("jmp .Lrf_return")

	g.label(".Lrf_err_close")
	// errno = -rax, then close fd.
	g.emit("neg rax")
	g.emit("mov r13, rax") // r13 = errno (buf base no longer needed)
	g.emit("mov edi, r12d")
	g.emit("mov eax, 3")
	g.emit("syscall")
	g.emit("jmp .Lrf_err_dispatch")

	g.label(".Lrf_err_open")
	g.emit("neg rax")
	g.emit("mov r13, rax")

	g.label(".Lrf_err_dispatch")
	// __lang_io_error(errno, path) → rax = IoError box.
	g.emit("mov edi, r13d")
	g.emit("mov rsi, [rbp - 56]") // original path string value (heap or inline)
	g.emit("call __lang_io_error")
	g.emit("mov r13, rax") // stash IoError box across the next alloc
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1") // tag=1 (Err)
	g.emit("mov [rax + 8], r13")

	g.label(".Lrf_return")
	g.emit("add rsp, 168")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_read_file, .-__lang_read_file")
}

// emitWriteFileRuntime emits `__lang_write_file(path, content)
// → Option[IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_WRONLY|O_CREAT|O_TRUNC=577, 0644) → write-loop → close →
// None. Errors → Some(IoError).
//
// Option[IoError] layout:
//
//	tag=0 (Some) → payload@+8 = IoError box ptr
//	tag=1 (None) → 8-byte box, no payload
func (g *generator) emitWriteFileRuntime() {
	g.line("")
	g.line(".globl __lang_write_file")
	g.line(".type __lang_write_file, @function")
	g.label("__lang_write_file")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // path byte ptr (materialised)
	g.emit("push r12") // content byte ptr (materialised)
	g.emit("push r13") // fd
	g.emit("push r14") // content_len
	g.emit("push r15") // bytes_written
	g.emit("sub rsp, 24") // 16 bytes scratch (path + content emitStrDataPtr) + 8 for original path value.
	g.emit("mov [rbp - 64], rdi") // save original path string value for __lang_io_error
	g.emitStrLen("r14d", "rsi") // content_len (from caller's rsi before materialise)
	g.emitStrDataPtr("rbx", "rdi", "[rbp - 48]") // path byte ptr
	g.emitStrDataPtr("r12", "rsi", "[rbp - 56]") // content byte ptr

	// openat(AT_FDCWD, path, O_WRONLY|O_CREAT|O_TRUNC=577, 0644)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("mov edx, 577")
	g.emit("mov r10d, 0644")
	g.emit("mov eax, 257")
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lwf_err_open")
	g.emit("mov r13, rax") // fd

	g.emit("xor r15, r15")
	g.label(".Lwf_loop")
	g.emit("cmp r15, r14")
	g.emit("jge .Lwf_done")
	g.emit("mov edi, r13d")
	g.emit("lea rsi, [r12 + r15]")
	g.emit("mov rdx, r14")
	g.emit("sub rdx, r15")
	g.emit("mov eax, 1") // write
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lwf_err_close")
	g.emit("add r15, rax")
	g.emit("jmp .Lwf_loop")

	g.label(".Lwf_done")
	g.emit("mov edi, r13d")
	g.emit("mov eax, 3") // close
	g.emit("syscall")
	// Option.None: 8-byte box, tag=1.
	g.emit("mov edi, 8")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1")
	g.emit("jmp .Lwf_return")

	g.label(".Lwf_err_close")
	g.emit("neg rax")
	g.emit("mov r14, rax") // errno
	g.emit("mov edi, r13d")
	g.emit("mov eax, 3")
	g.emit("syscall")
	g.emit("jmp .Lwf_err_dispatch")

	g.label(".Lwf_err_open")
	g.emit("neg rax")
	g.emit("mov r14, rax")

	g.label(".Lwf_err_dispatch")
	g.emit("mov edi, r14d")
	g.emit("mov rsi, [rbp - 64]") // original path string value (heap or inline)
	g.emit("call __lang_io_error")
	g.emit("mov r14, rax") // stash IoError box
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0") // tag=0 (Some)
	g.emit("mov [rax + 8], r14")

	g.label(".Lwf_return")
	g.emit("add rsp, 24")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_write_file, .-__lang_write_file")
}

// emitReaderWriterRuntime emits the full Reader / Writer
// runtime bundle on x86-64. Mirrors arm64's helper of the same
// name — same handle layout (4-byte i32 fd at +0), same wasm-
// shaped Result/Option boxes, same shared __lang_io_error
// error path. See the arm64 generator's comment for the
// design rationale.
func (g *generator) emitReaderWriterRuntime() {
	// __lang_make_handle(fd) → ptr to {fd:i32 @0}.
	g.line("")
	g.line(".globl __lang_make_handle")
	g.line(".type __lang_make_handle, @function")
	g.label("__lang_make_handle")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("sub rsp, 8")
	g.emit("mov ebx, edi") // stash fd
	g.emit("mov edi, 4")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], ebx")
	g.emit("add rsp, 8")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_make_handle, .-__lang_make_handle")

	// __lang_stdin / __lang_stdout / __lang_stderr.
	for _, e := range []struct {
		sym string
		fd  int
	}{
		{"__lang_stdin", 0},
		{"__lang_stdout", 1},
		{"__lang_stderr", 2},
	} {
		g.line("")
		g.line(".globl " + e.sym)
		g.line(".type " + e.sym + ", @function")
		g.label(e.sym)
		g.emit(fmt.Sprintf("mov edi, %d", e.fd))
		g.emit("jmp __lang_make_handle") // tail-call
		g.line(".size " + e.sym + ", .-" + e.sym)
	}

	// open_reader / open_writer / open_appender.
	for _, e := range []struct {
		sym       string
		flags     int
		mode      int
	}{
		{"__lang_open_reader", 0, 0},
		{"__lang_open_writer", 577, 0644},
		{"__lang_open_appender", 1089, 0644},
	} {
		g.line("")
		g.line(".globl " + e.sym)
		g.line(".type " + e.sym + ", @function")
		g.label(e.sym)
		g.emit("push rbp")
		g.emit("mov rbp, rsp")
		g.emit("push rbx") // path
		g.emit("push r12") // handle / errno scratch
		g.emit("mov rbx, rdi") // path
		// openat(AT_FDCWD, path, flags, mode)
		g.emit("mov edi, -100")
		g.emit("mov rsi, rbx")
		g.emit(fmt.Sprintf("mov edx, %d", e.flags))
		g.emit(fmt.Sprintf("mov r10d, %d", e.mode))
		g.emit("mov eax, 257") // openat
		g.emit("syscall")
		g.emit("test rax, rax")
		g.emit("js .Lorw_err_" + e.sym)
		// Success: alloc handle, store fd, wrap in Ok box.
		g.emit("mov edi, eax")
		g.emit("call __lang_make_handle")
		g.emit("mov r12, rax") // handle ptr in callee-save
		g.emit("mov edi, 16")
		g.emit("call __lang_alloc")
		g.emit("mov dword ptr [rax], 0") // tag=0 (Ok)
		g.emit("mov [rax + 8], r12")
		g.emit("jmp .Lorw_ret_" + e.sym)
		g.label(".Lorw_err_" + e.sym)
		g.emit("neg rax")
		g.emit("mov edi, eax")    // errno
		g.emit("mov rsi, rbx")    // path
		g.emit("call __lang_io_error")
		g.emit("mov r12, rax")    // IoError ptr
		g.emit("mov edi, 16")
		g.emit("call __lang_alloc")
		g.emit("mov dword ptr [rax], 1") // Err
		g.emit("mov [rax + 8], r12")
		g.label(".Lorw_ret_" + e.sym)
		g.emit("pop r12")
		g.emit("pop rbx")
		g.emit("pop rbp")
		g.emit("ret")
		g.line(".size " + e.sym + ", .-" + e.sym)
	}

	// __lang_reader_read_line(reader_ptr) → Option[string].
	g.line("")
	g.line(".globl __lang_reader_read_line")
	g.line(".type __lang_reader_read_line, @function")
	g.label("__lang_reader_read_line")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // fd
	g.emit("push r12") // buf base
	g.emit("push r13") // bytes_read
	g.emit("push r14") // last byte
	g.emit("mov ebx, [rdi]") // fd
	g.emit("lea r12, [rip + __lang_read_line_buf]")
	g.emit("xor r13, r13")
	g.label(".Lrrl_loop")
	g.emit("cmp r13, 4096")
	g.emit("jge .Lrrl_done")
	g.emit("mov edi, ebx")
	g.emit("lea rsi, [r12 + r13]")
	g.emit("mov edx, 1")
	g.emit("xor eax, eax")
	g.emit("syscall")
	g.emit("cmp rax, 1")
	g.emit("jl .Lrrl_done")
	g.emit("movzx r14d, byte ptr [r12 + r13]")
	g.emit("inc r13")
	g.emit("cmp r14d, 10")
	g.emit("je .Lrrl_done")
	g.emit("jmp .Lrrl_loop")
	g.label(".Lrrl_done")
	g.emit("test r13, r13")
	g.emit("jne .Lrrl_some")
	// None
	g.emit("mov edi, 4")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1")
	g.emit("jmp .Lrrl_ret")
	g.label(".Lrrl_some")
	g.emit("lea rdi, [r13 + 5]")
	g.emit("call __lang_alloc")
	g.emit("mov [rax], r13d") // length prefix
	g.emit("lea r14, [rax + 4]") // data ptr
	g.emit("mov rdi, r14")
	g.emit("mov rsi, r12")
	g.emit("mov rdx, r13")
	g.emit("call __lang_memcpy")
	// trailing NUL
	g.emit("mov byte ptr [r14 + r13], 0")
	g.emit("mov rbx, r14")    // stash str ptr (rbx no longer needed for fd)
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0")
	g.emit("mov [rax + 8], rbx")
	g.label(".Lrrl_ret")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_reader_read_line, .-__lang_reader_read_line")

	// __lang_reader_read_chunk(reader_ptr, n) → Option[string].
	g.line("")
	g.line(".globl __lang_reader_read_chunk")
	g.line(".type __lang_reader_read_chunk, @function")
	g.label("__lang_reader_read_chunk")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // fd
	g.emit("push r12") // n
	g.emit("push r13") // base ptr
	g.emit("sub rsp, 8")
	g.emit("mov ebx, [rdi]") // fd
	g.emit("mov r12, rsi")    // n
	// alloc n + 4
	g.emit("lea rdi, [r12 + 4]")
	g.emit("call __lang_alloc")
	g.emit("mov r13, rax")
	// read(fd, base+4, n)
	g.emit("mov edi, ebx")
	g.emit("lea rsi, [r13 + 4]")
	g.emit("mov rdx, r12")
	g.emit("xor eax, eax")
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("jle .Lrrc_none")
	g.emit("mov [r13], eax")
	g.emit("mov r12, rax") // r12 = bytes_read
	g.emit("lea rbx, [r13 + 4]") // data ptr
	g.emit("mov byte ptr [rbx + r12], 0") // trailing NUL within alloc
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0")
	g.emit("mov [rax + 8], rbx")
	g.emit("jmp .Lrrc_ret")
	g.label(".Lrrc_none")
	g.emit("mov edi, 4")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1")
	g.label(".Lrrc_ret")
	g.emit("add rsp, 8")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_reader_read_chunk, .-__lang_reader_read_chunk")

	// __lang_writer_write(writer_ptr, s) → Option[IoError].
	g.line("")
	g.line(".globl __lang_writer_write")
	g.line(".type __lang_writer_write, @function")
	g.label("__lang_writer_write")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // fd
	g.emit("push r12") // s data ptr
	g.emit("push r13") // remaining bytes
	g.emit("push r14") // bytes_written
	g.emit("mov ebx, [rdi]") // fd
	g.emit("mov r12, rsi")
	g.emitStrLen("r13d", "r12") // len
	g.emit("xor r14, r14")
	g.label(".Lww_loop")
	g.emit("cmp r14, r13")
	g.emit("jge .Lww_done")
	g.emit("mov edi, ebx")
	g.emit("lea rsi, [r12 + r14]")
	g.emit("mov rdx, r13")
	g.emit("sub rdx, r14")
	g.emit("mov eax, 1") // write
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lww_err")
	g.emit("add r14, rax")
	g.emit("jmp .Lww_loop")
	g.label(".Lww_done")
	g.emit("mov edi, 4")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1") // None
	g.emit("jmp .Lww_ret")
	g.label(".Lww_err")
	g.emit("neg rax")
	g.emit("mov edi, eax")
	g.emit("lea rsi, [rip + .LStr_ioerr_empty]")
	g.emit("call __lang_io_error")
	g.emit("mov r12, rax")
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0") // Some
	g.emit("mov [rax + 8], r12")
	g.label(".Lww_ret")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_writer_write, .-__lang_writer_write")

	// __lang_close_fd_box(handle_ptr) → Option[IoError].
	// Shared by Reader.close + Writer.close.
	g.line("")
	g.line(".globl __lang_close_fd_box")
	g.line(".type __lang_close_fd_box, @function")
	g.label("__lang_close_fd_box")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("sub rsp, 8")
	g.emit("mov edi, [rdi]") // fd
	g.emit("mov eax, 3") // close
	g.emit("syscall")
	g.emit("test rax, rax")
	g.emit("js .Lcfb_err")
	g.emit("mov edi, 4")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 1") // None
	g.emit("jmp .Lcfb_ret")
	g.label(".Lcfb_err")
	g.emit("neg rax")
	g.emit("mov edi, eax")
	g.emit("lea rsi, [rip + .LStr_ioerr_empty]")
	g.emit("call __lang_io_error")
	g.emit("mov rbx, rax")
	g.emit("mov edi, 16")
	g.emit("call __lang_alloc")
	g.emit("mov dword ptr [rax], 0") // Some
	g.emit("mov [rax + 8], rbx")
	g.label(".Lcfb_ret")
	g.emit("add rsp, 8")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __lang_close_fd_box, .-__lang_close_fd_box")
}

// stateWidthBytes returns the storage width (4 or 8 bytes) for
// a state-var of the given lang type. Wide scalars (i64 / f64)
// take 8 bytes; pointer-shaped types take ptrW (=8 on x86-64);
// everything else takes 4. Matches arm64's helper and
// `payloadSlotSize` in the IR.
func stateWidthBytes(t ast.Type) int {
	if t == nil {
		return 4
	}
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return 8
	}
	if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
		return 8
	}
	if ast.IsPointerType(t) {
		return 8
	}
	return 4
}

// lookupStateType resolves a state-var name to its declared
// lang type, for width selection at OpLoadGlobal / OpStoreGlobal.
func (g *generator) lookupStateType(name string) ast.Type {
	if g.info == nil {
		return nil
	}
	return g.info.StateVars[name]
}

// stateInitLiteralBits returns the raw bit pattern of a literal
// state initialiser. Mirrors arm64.
func stateInitLiteralBits(e ast.Expr, t ast.Type) (uint64, bool) {
	switch v := e.(type) {
	case *ast.NumberLit:
		return uint64(v.Value), true
	case *ast.FloatLit:
		w := 32
		if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
			w = 64
		}
		if w == 64 {
			return math.Float64bits(v.Value), true
		}
		return uint64(math.Float32bits(float32(v.Value))), true
	case *ast.BoolLit:
		if v.Value {
			return 1, true
		}
		return 0, true
	case *ast.Unary:
		if v.Op == "-" {
			if inner, ok := stateInitLiteralBits(v.Operand, t); ok {
				w := stateWidthBytes(t)
				if w == 8 {
					return uint64(-int64(inner)), true
				}
				return uint64(uint32(-int32(inner))), true
			}
		}
	}
	return 0, false
}

// emitStateGlobals writes one labelled slot per state-block
// var (program-lifetime no-op interpretation; see arm64
// generator + docs/BACKEND-PARITY.md). Literal initialisers
// are baked into .data; non-literal ones land in .bss and
// __state_init writes the real value at startup.
func (g *generator) emitStateGlobals() {
	if len(g.stateDecls) == 0 {
		return
	}
	hasData, hasBss := false, false
	for _, sd := range g.stateDecls {
		for _, v := range sd.Vars {
			if _, ok := stateInitLiteralBits(v.Init, v.Type); ok {
				hasData = true
			} else {
				hasBss = true
			}
		}
	}
	if hasData {
		g.line("")
		g.line(".section .data")
		for _, sd := range g.stateDecls {
			for _, v := range sd.Vars {
				bits, ok := stateInitLiteralBits(v.Init, v.Type)
				if !ok {
					continue
				}
				w := stateWidthBytes(v.Type)
				if w == 8 {
					g.line(".align 8")
					g.label(".Lstate_" + v.Name)
					g.line(fmt.Sprintf("\t.quad %d", int64(bits)))
				} else {
					g.line(".align 4")
					g.label(".Lstate_" + v.Name)
					g.line(fmt.Sprintf("\t.long %d", int32(bits)))
				}
			}
		}
	}
	if hasBss {
		g.line("")
		g.line(".section .bss")
		for _, sd := range g.stateDecls {
			for _, v := range sd.Vars {
				if _, ok := stateInitLiteralBits(v.Init, v.Type); ok {
					continue
				}
				w := stateWidthBytes(v.Type)
				if w == 8 {
					g.line(".align 8")
				} else {
					g.line(".align 4")
				}
				g.label(".Lstate_" + v.Name)
				g.line(fmt.Sprintf("\t.zero %d", w))
			}
		}
	}
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
