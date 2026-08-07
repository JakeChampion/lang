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
//	push rbp                    ; save caller's frame pointer
//	mov  rbp, rsp               ; rbp = saved-pair top
//	sub  rsp, <localsSize>      ; reserve locals
//	mov  [rbp-8],  rdi          ; spill register args
//	mov  [rbp-16], rsi          ; ...
//	...
//
// Local slot `i` lives at `[rbp - 8*(i+1)]` — 8 bytes per slot,
// same encoding shape as arm64's `[x29, #-(i+1)*8]`.
//
// Syscalls: see Linux x86-64 asm-generic table —
//
//	read=0  write=1  close=3  mmap=9  socket=41  accept=43
//	bind=49 listen=50 exit_group=231
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

// Seccomp sandbox constants (#6071). See linux/seccomp.h,
// linux/filter.h and linux/audit.h.
const (
	prSetNoNewPrivs       = 38         // PR_SET_NO_NEW_PRIVS
	seccompSetModeFilter  = 1          // SECCOMP_SET_MODE_FILTER
	auditArchX8664        = 0xC000003E // AUDIT_ARCH_X86_64
	seccompRetKillProcess = 0x80000000 // SECCOMP_RET_KILL_PROCESS
	seccompRetAllow       = 0x7FFF0000 // SECCOMP_RET_ALLOW

	// BPF opcodes, pre-combined: BPF_LD|BPF_W|BPF_ABS, BPF_JMP|BPF_JEQ|BPF_K,
	// BPF_RET|BPF_K.
	bpfLdWAbs = 0x20
	bpfJeqK   = 0x15
	bpfRetK   = 0x06

	// Offsets into `struct seccomp_data { int nr; __u32 arch; ... }`.
	seccompDataNr   = 0
	seccompDataArch = 4
)

// emitSeccompRuntime emits `__fern_seccomp_install` and the BPF program
// it installs (#6071). Must be called AFTER every other emitter, since
// the allowlist is g.syscalls and that is only complete once all code
// has been emitted.
//
// The filter is the obvious shape: verify the audit arch (a 32-bit
// process making the same-numbered syscall means something else
// entirely, so a mismatch is fatal rather than merely unmatched), load
// the syscall number, compare against each allowed value, and fall
// through to kill. Kill rather than errno because it matches the
// existing crash-only posture — a sandbox violation is a bug or an
// attack, and neither is improved by letting the program continue with
// an unexpected -EPERM it has no code path to handle.
//
// Jump arithmetic: ALLOW sits at index 4+n+1 and the fall-through KILL
// at 4+n, so the comparison at index 4+i takes jt = n-i to reach ALLOW.
// jt is a u8, which bounds this at 255 allowed syscalls; the whole
// backend emits ~20, and emitSyscall is the only way that grows, so the
// bound is checked rather than assumed.
func (g *generator) emitSeccompRuntime() {
	// Snapshot the allowlist BEFORE emitting this helper's own prctl /
	// seccomp calls, so neither ends up permitted. That is deliberate,
	// not incidental: both run before the filter takes effect (the
	// filter applies from the seccomp(2) return onwards), so excluding
	// them costs nothing and denies hijacked control flow the ability to
	// install a filter of its own choosing.
	//
	// rt_sigreturn is likewise absent, which is a classic seccomp
	// footgun — it is required whenever a signal handler returns. Fern
	// installs no signal handlers, and an unhandled fatal signal kills
	// the process without ever returning, so there is nothing to permit.
	// Adding a handler would mean adding rt_sigreturn here.
	allowed := make([]int, 0, len(g.syscalls))
	for n := range g.syscalls {
		allowed = append(allowed, n)
	}
	sort.Ints(allowed)
	n := len(allowed)
	if n > 255 {
		// jt is a u8; past this the jump arithmetic silently wraps and
		// the filter would permit the wrong things. Fail loudly instead.
		panic(fmt.Sprintf("seccomp allowlist has %d syscalls; the u8 jump offset caps it at 255", n))
	}

	g.line("")
	g.line(".globl __fern_seccomp_install")
	g.line(".type __fern_seccomp_install, @function")
	g.label("__fern_seccomp_install")
	// prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) — required before
	// seccomp(2) without CAP_SYS_ADMIN, and the thing that makes the
	// filter survive execve without privilege escalation.
	g.emit(fmt.Sprintf("mov edi, %d", prSetNoNewPrivs))
	g.emit("mov esi, 1")
	g.emit("xor edx, edx")
	g.emit("xor r10d, r10d")
	g.emit("xor r8d, r8d")
	g.emitSyscall(sysPrctl)
	// seccomp(SECCOMP_SET_MODE_FILTER, 0, &prog), with
	// `struct sock_fprog { unsigned short len; struct sock_filter *filter; }`
	// built on the stack — 16 bytes, len at +0 and the pointer at +8
	// after padding. Built here rather than kept in .rodata because the
	// pointer field would otherwise need a relocation, and under -pie
	// the self-reloc prologue would have to write into a read-only
	// section to apply it.
	g.emit("sub rsp, 16")
	g.emit(fmt.Sprintf("mov word ptr [rsp], %d", 4+n+2)) // total BPF insn count
	g.emit("lea rax, [rip + .Lseccomp_filter]")
	g.emit("mov [rsp + 8], rax")
	g.emit(fmt.Sprintf("mov edi, %d", seccompSetModeFilter))
	g.emit("xor esi, esi")
	g.emit("mov rdx, rsp")
	g.emitSyscall(sysSeccomp)
	g.emit("add rsp, 16")
	// A failure here is deliberately NOT fatal: a kernel without
	// CONFIG_SECCOMP_FILTER, or a seccomp-blocking sandbox we are
	// already inside, returns an error and the program runs unhardened
	// rather than refusing to start. Hardening that turns a working
	// deployment into a boot loop would not survive contact with users,
	// and the compile-time capability system is still in force either
	// way.
	g.emit("ret")
	g.line(".size __fern_seccomp_install, .-__fern_seccomp_install")

	// The BPF program. Each sock_filter is { u16 code; u8 jt; u8 jf;
	// u32 k } — 8 bytes, little-endian.
	g.line(".section .rodata")
	g.line(".align 8")
	g.label(".Lseccomp_filter")
	// No inline comments on these directives: GNU as reads `//` as
	// division rather than a comment, so an annotated `.4byte` fails to
	// assemble on the -cc path even though the in-process assembler
	// accepts it. The program's shape is documented above and decoded
	// by TestSeccompFilterShape.
	bpf := func(code, jt, jf, k int) {
		g.line(fmt.Sprintf("\t.2byte %d", code))
		g.line(fmt.Sprintf("\t.byte %d", jt))
		g.line(fmt.Sprintf("\t.byte %d", jf))
		g.line(fmt.Sprintf("\t.4byte %d", uint32(k)))
	}
	bpf(bpfLdWAbs, 0, 0, seccompDataArch)     // A = arch
	bpf(bpfJeqK, 1, 0, auditArchX8664)        // skip the kill when it matches
	bpf(bpfRetK, 0, 0, seccompRetKillProcess) // wrong arch
	bpf(bpfLdWAbs, 0, 0, seccompDataNr)       // A = syscall nr
	for i, s := range allowed {
		bpf(bpfJeqK, n-i, 0, s)
	}
	bpf(bpfRetK, 0, 0, seccompRetKillProcess) // unmatched: deny
	bpf(bpfRetK, 0, 0, seccompRetAllow)
	g.line(".text")
}

// emitSyscall emits `mov eax, n` + `syscall` and records n in the
// generator's syscall set. Every syscall the backend emits goes
// through here or emitSyscallPreloaded, which is what makes
// g.syscalls exact rather than a best-effort inventory: there is no
// other way to emit the instruction, so a new syscall cannot be added
// without landing in the set. TestNoBareSyscallEmit enforces that.
//
// The recorded set is the ground truth for `-syscalls` and, later, for
// the seccomp-bpf filter (#6071) — a filter derived from a
// hand-maintained table would silently kill legitimate paths the
// moment the runtime grew a syscall nobody remembered to list.
func (g *generator) emitSyscall(n int) {
	g.emit(fmt.Sprintf("mov eax, %d", n))
	g.emit("syscall")
	g.syscalls[n] = true
}

// emitSyscallPreloaded emits `syscall` alone, for the handful of sites
// that load eax by other means — either several instructions earlier
// (the abort path interleaves its argument setup) or via `xor eax, eax`
// for read=0, which is a byte shorter than the immediate. It records n
// identically. Callers pass the number the site actually issues; a
// wrong argument here is the one way the set could lie, so the sites
// are few and each keeps the eax-load visible directly above it.
func (g *generator) emitSyscallPreloaded(n int) {
	g.emit("syscall")
	g.syscalls[n] = true
}

// Linux x86-64 syscall numbers. See the asm-generic table
// for the full set.
const (
	sysRead      = 0
	sysWrite     = 1
	sysClose     = 3
	sysMmap      = 9
	sysSocket    = 41
	sysConnect   = 42
	sysAccept    = 43
	sysBind      = 49
	sysListen    = 50
	sysExitGroup = 231
	sysGetrandom = 318
	// prctl(2) / seccomp(2): x86-64 syscalls 157 / 317. Used only by
	// __fern_seccomp_install under ast.SandboxEnabled (#6071).
	sysPrctl   = 157
	sysSeccomp = 317
	// clock_gettime(2): x86-64 syscall 228.
	// Used by `__fern_now_unix_ms` / `__fern_monotonic_ns` for the
	// clock-now surface (docs/STDLIB-DESIGN-RESEARCH.md Rec §4 Phase 2).
	sysClockGettime = 228
	// nanosleep(2): x86-64 syscall 35. Used by `__fern_sleep_ms`.
	sysNanosleep = 35
	// fork(2) / wait4(2): x86-64 syscalls 57 / 61. Back
	// `__fern_proc_fork` / `__fern_proc_waitpid` — the crash-only
	// supervision primitives (docs/CRASH-ONLY-SERVE.md D2').
	sysFork   = 57
	sysExecve = 59
	sysWait4  = 61
	// poll(2): x86-64 syscall 7. Used by `__fern_poll` — the readiness
	// multiplexer behind the std/task reactor
	// (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1). Waits on a set of
	// fds for POLLIN.
	sysPoll = 7
	// timerfd_create(2) / timerfd_settime(2): x86-64 syscalls 283 / 286.
	// Back `__fern_timer_fd(ms)` — a CLOCK_MONOTONIC timerfd that
	// becomes readable after `ms`, for reactor timeouts + deterministic
	// readiness tests (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c).
	sysTimerfdCreate  = 283
	sysTimerfdSettime = 286
)

// Options tunes the emit.
type Options struct {
	// PIE emits a static position-independent (ET_DYN) executable: a
	// self-relocation prologue at `_start` applies the R_X86_64_RELATIVE
	// entries in .rela.dyn (the `.quad <symbol>` slots) before the program
	// runs, so it is correct at the arbitrary base the kernel loads it at.
	// Pair with x86_64.AssembleProgramPIE + elf.StaticPieExecutableX86.
	PIE bool

	// Exports are function names kept as tree-shake roots (in addition to
	// `main`) so a `-shared` .so can export functions the program never
	// calls itself — e.g. JNI entry points, which only the JVM invokes.
	Exports []string

	// NoPeephole disables the streaming output peephole (see generator.put).
	// The peephole is a pure size optimisation and on by default; this only
	// exists so asm-shape tests can assert the un-collapsed emission.
	NoPeephole bool

	// DebugLines makes the lowering emit OpLine markers and the backend emit
	// DWARF `.loc` directives, so the assembler can build a .debug_line
	// source-line table (#5537 slice 2). Set under `fern -g`.
	DebugLines bool
}

// Emit produces assembly text for prog targeting x86-64 Linux.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithSyscalls is EmitWithOptions plus the exact set of Linux
// syscall numbers the emitted text can issue, ascending.
//
// "Exact" is a real claim, not a best effort: every syscall the backend
// emits goes through emitSyscall / emitSyscallPreloaded, so the set is
// accumulated by construction rather than recovered afterwards, and
// TestNoBareSyscallEmit fails the build if a site bypasses them. Nor is
// it a whole-language over-approximation — treeshake and dead-code
// elimination have already run, so the set describes THIS program: a
// binary that never opens a file does not carry `openat`.
//
// This is the input the seccomp-bpf filter is derived from (#6071).
// Deriving it from the emitted text rather than from capability
// declarations is what removes the under-approximation hazard: a
// filter built from a hand-maintained table would kill the process the
// moment the runtime grew a syscall nobody remembered to list, whereas
// this cannot omit a syscall the program can actually make.
func EmitWithSyscalls(prog *ast.Program, info *checker.Info, opts Options) (string, []int, error) {
	asm, syscalls, err := emitCollecting(prog, info, opts)
	return asm, syscalls, err
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
	asm, _, err := emitCollecting(prog, info, opts)
	return asm, err
}

// emitCollecting is the body of EmitWithOptions, additionally returning
// the generator's recorded syscall set (see EmitWithSyscalls).
func emitCollecting(prog *ast.Program, info *checker.Info, opts Options) (string, []int, error) {
	// Acquire `ast.CodegenMu` so `ir.LowerWith`'s read of
	// `ast.TwoWordOverride` isn't races against a concurrent
	// `arm64.Emit` that's mid-toggle. x86_64 doesn't write the
	// flag (it always wants the single-word string ABI on
	// ptrW=8) but it READS it transitively via the IR's
	// `twoWordStrings()` helper. Without the lock, parallel
	// `TestDifferential` seeds running x86_64 + arm64
	// simultaneously would let arm64's mid-emit `true` leak
	// into x86_64's lowering — producing mixed-ABI code.
	ast.CodegenMu.Lock()
	defer ast.CodegenMu.Unlock()
	// `dyn Trait` vtable impl methods are reachable only through the
	// runtime vtable (OpConstVtable names them by string), never via a
	// static call the AST walker / IR reachability can see — pin them as
	// tree-shake roots so they survive (mirrors the wasm build path).
	// See docs/DYN-TRAITS.md §4.2.2.
	dynRoots := append(treeshake.DynCoercionImplMethods(info), treeshake.DowncastImplMethods(prog, info)...)
	dynRoots = append(dynRoots, opts.Exports...) // -shared exports survive tree-shaking
	treeshake.Run(prog, dynRoots...)
	// x86-64 supports boxed one-word `dyn Trait` values
	// (docs/DYN-TRAITS.md §4.2.2): DynSupported lifts the dispatch gate.
	// It ALSO reclaims them (Perceus RC, slice 4b — docs/DYN-TRAITS.md
	// §4.4): DynRcSupported lifts the RC path (the trailing vtable drop
	// slot + the per-set __drop_dyn_<set> helper + the dec/drop sweep
	// arms). arm64 passes only DynSupported (dispatch) — its RC slice 4c
	// hasn't landed, so it keeps leaking `dyn` (harmless).
	lowerOpts := []ir.LowerOption{ir.DynSupported(), ir.DynRcSupported()}
	if opts.DebugLines {
		lowerOpts = append(lowerOpts, ir.EmitLineMarkers())
	}
	ip, err := ir.LowerWith(prog, info, 8, lowerOpts...)
	if err != nil {
		return "", nil, err
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
	// IR pass battery (#4377) — per-function rewrites the wasm backend runs
	// that neither add nor remove functions, so the emitFunc AST↔IR
	// parallel-index walk below stays valid. FuseTee fuses store+reload into
	// OpTeeLocal (both natives already emit it); FlattenBranches drops
	// `if (false) { … }` bodies before they reach asm; EliminateDeadCode trims
	// ops after a terminator (slice 1, #4678).
	//
	// OptimizeCleanup (the copyprop/constprop/Fold/strength fixpoint) now runs
	// on the natives too (slice 1b, #4377). The ir.Fold emitter crash it used
	// to hit is fixed (the index zero-extend in emitInlineIdxHelper above — a
	// folded-constant array index otherwise carried dirty upper bits into a
	// scaled `lea` past the 32-bit bounds check), and the fixpoint's old
	// up-to-8× whole-program convergence snapshot is gone (each sub-pass now
	// reports a changed bool), so it no longer balloons self-host build time.
	// ir.Inline + the whole-function cull remain slice 2 (name-keyed walk).
	ir.FuseTee(ip)
	ir.FlattenBranches(ip)
	ir.EliminateDeadCode(ip)
	ir.OptimizeCleanup(ip)
	g := &generator{info: info, stringLabel: map[string]string{}, funcs: map[string]*ast.FuncDecl{}, vtables: ip.Vtables, pie: opts.PIE, noPeephole: opts.NoPeephole, syscalls: map[int]bool{}}
	// Pre-scan call sites for runtime-helper use-flags before
	// touching any code emission, so emitDataSections + the
	// runtime emitters below know which helpers to include
	// (and the .bss reservations match the helpers).
	for _, fn := range prog.Funcs {
		g.funcs[fn.Name] = fn
	}
	for _, fn := range ip.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == ir.OpCallDirect ||
				op.Kind == ir.OpRcInc || op.Kind == ir.OpRcDec || op.Kind == ir.OpRcIsUnique {
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
				// come from __fern_alloc.
				g.usesAlloc = true
			}
			if op.Kind == ir.OpBoxDyn {
				// The boxed `dyn Trait` cell is a normal heap object
				// allocated via __fern_alloc (docs/DYN-TRAITS.md §4.2.2).
				g.usesAlloc = true
			}
		}
	}
	g.line(".intel_syntax noprefix")
	g.line(".text")
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", nil, err
		}
		// A function's IR is dead the moment it is emitted — nothing after
		// this loop reads ip.Funcs — but holding the whole slice keeps the
		// entire program's ops (~160 B each, tens of millions for the
		// self-host drivers) live until the last function is done, right
		// when the output buffer is at its largest. Releasing each entry
		// as we go lets the GC reclaim the IR incrementally, cutting the
		// emit's live-heap peak by roughly the IR's size on driver-scale
		// programs. Output is unaffected (the walk is forward-only).
		ip.Funcs[i] = nil
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
	// Fatal-abort diagnostics (#5538): __fern_report writes an abort's
	// cause to stderr before exit_group, so a bounds / arena / slice abort
	// names itself instead of exiting silently. Emitted unconditionally —
	// the abort sites jmp here, and label resolution is order-independent.
	g.emitAbortRuntime()
	// Runtime helpers — gated on use-flags so unused programs
	// pay nothing extra in binary size.
	if g.usesAlloc {
		g.emitAllocRuntime()
		// __fern_alloc_box piggybacks on __fern_alloc: the
		// enum-box runtime helpers (Option / Result / IoError
		// builders) call it to get the static-sentinel rc
		// header. Emit it whenever alloc is present so those
		// helpers can reach it.
		g.emitAllocBoxRuntime()
		// __fern_alloc_rc1 — same header shape as __fern_alloc_box
		// but stores a live rc=1 instead of the immortal sentinel.
		// Closure env blocks / pairs use it so they can be dropped
		// at rc=0 once enum-ii-style predicate widening tracks
		// FuncType locals.
		g.emitAllocRc1Runtime()
	}
	if g.usesFree {
		g.emitFreeRuntime()
	}
	if g.usesArrDec {
		g.emitArrDecRuntime()
	}
	if g.usesAllocReuse {
		g.emitAllocReuseRuntime()
	}
	if g.usesMapDrop {
		g.emitMapDropRuntime()
	}
	if g.usesBoxFree {
		g.emitBoxFreeRuntime()
	}
	if g.usesClosureDrop {
		g.emitClosureDropRuntime()
	}
	if g.usesStrDec {
		g.emitStrDecRuntime()
	}
	if g.usesMemcpy {
		g.emitMemcpyRuntime()
	}
	for n := range g.usesCCall {
		if g.usesCCall[n] {
			g.emitCCallRuntime(n)
		}
		if g.usesCCallF32[n] {
			g.emitCCallRuntimeSuffixed(n, "_f32")
		}
		if g.usesCCallF64[n] {
			g.emitCCallRuntimeSuffixed(n, "_f64")
		}
	}
	if g.usesRcInc {
		g.emitRcIncRuntime()
	}
	if g.usesRcDec {
		g.emitRcDecRuntime()
	}
	if g.usesRcUnderflowCount {
		g.emitRcUnderflowCountRuntime()
	}
	if g.usesArrPushSharedCount {
		g.emitArrPushSharedCountRuntime()
	}
	if g.usesArrPushSharedBytes {
		g.emitArrPushSharedBytesRuntime()
	}
	if g.usesMapHashSeed {
		g.emitMapHashSeedRuntime()
	}
	if g.usesHeapBumpBytes {
		g.emitHeapBumpBytesRuntime()
	}
	if g.usesHeapMark {
		g.emitHeapMarkRuntime()
	}
	if g.usesArrPushGrow {
		g.emitArrPushGrowRuntime()
	}
	if g.usesArrPushGrowPtr {
		g.emitArrPushGrowPtrRuntime(false)
	}
	if g.usesArrPushGrowMovePtr {
		g.emitArrPushGrowPtrRuntime(true)
	}
	if g.usesArrCowInPlace {
		g.emitArrCowInPlaceRuntime()
	}
	if g.usesArrCowInPlacePtr {
		g.emitArrCowInPlacePtrRuntime()
	}
	if g.usesDropArrPtr {
		g.emitDropArrPtrRuntime()
	}
	if g.usesDropArrStr {
		g.emitDropArrStrRuntime()
	}
	if g.usesRcIsUnique {
		g.emitRcIsUniqueRuntime()
	}
	if g.usesSliceMake {
		g.emitSliceMakeRuntime()
	}
	if g.usesSliceRange {
		g.emitSliceRangeRuntime()
	}
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	if g.usesStrAppend {
		g.emitStrAppendRuntime()
	}
	if g.usesStrcmp {
		g.emitStrcmpRuntime()
	}
	if g.usesMemchr {
		g.emitMemchrRuntime()
	}
	if g.usesF64Trans {
		g.emitFloatTranscendentalsRuntime()
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
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): the _start epilogue always
		// calls the report, so the helper (and its BSS counters +
		// .rodata labels, see emitDataSections) is unconditional under
		// the flag — a no-alloc program just reports zeros.
		g.emitLcReportRuntime()
	}
	if ast.RcTrace {
		// Heap event tracer (#6068). Both hook sites live inside
		// __fern_alloc / __fern_free, so the helper is only reachable
		// when those are, but emit it under the flag alone — the
		// gating that matters is the flag, and an unreferenced helper
		// in a diagnostic build costs nothing worth a use-flag.
		g.emitRctRuntime()
	}
	if g.usesStrBuf {
		g.emitStrBufRuntime()
	}
	if g.usesNowUnixMs {
		g.emitNowUnixMsRuntime()
	}
	if g.usesMonotonicNs {
		g.emitMonotonicNsRuntime()
	}
	if g.usesNowNs {
		g.emitNowNsRuntime()
	}
	if g.usesSleepMs {
		g.emitSleepMsRuntime()
	}
	if g.usesProcFork {
		g.emitProcForkRuntime()
	}
	if g.usesProcWaitpid {
		g.emitProcWaitpidRuntime()
	}
	if g.usesProcExec {
		g.emitProcExecRuntime()
	}
	if g.usesTcp {
		g.emitTcpListenRuntime()
		g.emitTcpAcceptRuntime()
		g.emitTcpRecvRuntime()
		g.emitTcpSendRuntime()
		g.emitTcpCloseRuntime()
		g.emitTcpConnectRuntime()
		g.emitTcpPollableRuntime()
	}
	if g.usesPoll {
		g.emitPollRuntime()
	}
	if g.usesWasmPollableDrop {
		g.emitWasmPollableDropRuntime()
	}
	if g.usesWasmBlock {
		g.emitWasmBlockRuntime()
	}
	if g.usesWasmTimerPollable {
		g.emitWasmTimerPollableRuntime()
	}
	if g.usesWasmPoll {
		g.emitWasmPollRuntime()
	}
	if g.usesTimerFd {
		g.emitTimerFdRuntime()
	}
	if g.usesEnv {
		g.emitEnvRuntime()
	}
	if g.usesArgs {
		g.emitArgsRuntime()
	}
	if g.usesRandomBytes {
		g.emitRandomBytesRuntime()
	}
	if g.usesRandomI32 {
		g.emitRandomI32Runtime()
	}
	if g.usesAsBytes {
		g.emitStringAsBytesRuntime()
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
	if g.usesWriteFileExec {
		g.emitWriteFileRuntimeMode("__fern_write_file_exec", "0755", "x", "0755")
	}
	if g.usesRemoveDirAll {
		g.emitRemoveDirAllRuntime()
	}
	if g.usesRemoveFile {
		g.emitRemoveFileRuntime()
	}
	if g.usesTempDir {
		g.emitTempDirRuntime()
	}
	if g.usesReadDir {
		g.emitReadDirRuntime()
	}
	if g.usesStat {
		g.emitStatRuntime()
	}
	if g.usesReaderWriter {
		// Bundle: open_reader/writer/appender + stdin/stdout/
		// stderr handle constructors + Reader.read_line /
		// read_chunk / close + Writer.write / close +
		// __fern_make_handle + __fern_close_fd_box. Shares
		// __fern_read_line_buf with the stdin-only read_line
		// helper (4 KiB scratch).
		g.emitReaderWriterRuntime()
	}
	if ast.SandboxEnabled {
		// Last code emitter: the filter's allowlist is g.syscalls, which
		// is only complete once every other emitter has run.
		g.emitSeccompRuntime()
	}
	g.emitDataSections()
	// ELF non-executable-stack marker. Without this the
	// linker warns (or refuses, on hardened distros) about
	// the binary having an implicit executable stack.
	g.line(".section .note.GNU-stack,\"\",@progbits")
	g.flushPeep()
	nums := make([]int, 0, len(g.syscalls))
	for n := range g.syscalls {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return g.out.String(), nums, nil
}

// generator carries the running output buffer plus per-
// program state.
type generator struct {
	info *checker.Info
	out  strings.Builder
	// pie emits the static-PIE self-relocation prologue at `_start`
	// (see Options.PIE).
	pie bool
	// noPeephole disables the streaming output peephole (Options.NoPeephole).
	noPeephole bool
	// peepWin is the streaming peephole's sliding window of recently
	// emitted logical lines (no trailing newline), held back from `out`
	// until they can no longer participate in a rewrite. See put / flushPeep.
	peepWin []string
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
	usesAlloc  bool
	usesMemcpy bool
	// usesCCall[n] gates the `__c_call<n>` FFI shim (call a C-ABI function
	// pointer with n integer args). The F32/F64 variants gate byte-identical
	// shims whose only difference is a checker FuncSig declaring an f32/f64
	// result, so the call site reads the FP register. See emitCCallRuntime.
	usesCCall    [5]bool
	usesCCallF32 [5]bool
	usesCCallF64 [5]bool
	usesStrcat   bool
	// usesStrAppend gates `__fern_str_append` — the in-place-when-unique
	// string self-append the IR emits for `s = s + piece` (#5637). Pulls in
	// __fern_strcat (its copy path) and __fern_str_dec (the release it
	// takes over from the assignment's dec-on-overwrite).
	usesStrAppend bool
	usesStrcmp    bool
	// usesMemchr gates the SSE2 byte-search kernel (__fern_memchr).
	usesMemchr bool
	// usesF64Trans gates the f64 transcendental bundle —
	// __fern_{exp,log,sin,cos,pow}_f64 and its shared .rodata
	// coefficient table. One flag for all five because `pow` is
	// built from `log` + `exp`, so they are never independent.
	usesF64Trans bool
	usesPuts     bool
	usesWrite    bool
	usesPutchar  bool
	// usesEprint / usesExit — eprint(s) → stderr write+newline;
	// exit(code) → direct exit_group syscall. Both mirror arm64.
	usesEprint bool
	usesExit   bool
	// usesStrBuf — strbuf_reset / strbuf_append / strbuf_take —
	// global mutable scratch buffer primitive for O(1) amortised
	// append (escape hatch from O(N²) `s.out + text`). 64 MiB BSS
	// region + 8-byte len counter. Single-threaded; one builder
	// at a time. See checker.go for the user-facing spec.
	usesStrBuf bool
	// usesNowUnixMs pulls in `__fern_now_unix_ms()` — wall-
	// clock-ms via the x86_64 `clock_gettime(CLOCK_REALTIME,
	// &ts)` syscall (#228). Returns
	// `tv_sec * 1000 + tv_nsec / 1_000_000` in rax as i64.
	// Backs `time.instant_now()` on x86_64 Linux.
	usesNowUnixMs bool
	// usesMonotonicNs pulls in `__fern_monotonic_ns()` — monotonic
	// nanoseconds via `clock_gettime(CLOCK_MONOTONIC, &ts)` (#228),
	// returning `tv_sec * 1e9 + tv_nsec` in rax as i64.
	usesMonotonicNs bool
	// usesNowNs pulls in `__fern_now_ns()` — wall-clock nanoseconds since
	// the Unix epoch via `clock_gettime(CLOCK_REALTIME, &ts)` (#228),
	// returning `tv_sec * 1e9 + tv_nsec` in rax as i64. The nanosecond
	// counterpart of `now_unix_ms`.
	usesNowNs bool
	// usesSleepMs pulls in `__fern_sleep_ms(ms)` — best-effort sleep
	// for `ms` milliseconds via `nanosleep(&req, NULL)` (#35); ms <= 0
	// returns immediately. Void.
	usesSleepMs bool
	// usesProcExec pulls in `__fern_proc_exec(path, args)` — execve(2),
	// the leg that lets a forked child become another program. Shares the
	// `proc` capability with fork / waitpid and needs the allocator (it
	// materialises NUL-terminated copies for the C ABI).
	usesProcExec bool
	// usesProcFork / usesProcWaitpid pull in `__fern_proc_fork()` —
	// fork(2) (#57): 0 in child, pid in parent, -errno on failure —
	// and `__fern_proc_waitpid(pid)` — wait4(2) (#61) + status-word
	// decode: exit code 0..255 for a normal exit, 128+signal for a
	// signal death, -errno passthrough. The crash-only supervision
	// primitives (docs/CRASH-ONLY-SERVE.md D2').
	usesProcFork          bool
	usesProcWaitpid       bool
	usesTcp               bool
	usesEnv               bool
	usesArgs              bool
	usesAllocU8           bool
	usesStringFromBytes   bool
	usesStrSlice          bool
	usesSliceMake         bool
	usesSliceRange        bool
	usesRandomBytes       bool
	usesRandomI32         bool
	usesPoll              bool
	usesWasmPollableDrop  bool
	usesWasmTimerPollable bool
	usesWasmPoll          bool
	usesWasmBlock         bool
	usesTimerFd           bool
	usesAsBytes           bool
	usesReadLine          bool
	// usesStrIdx tracks whether any code emits the SSO-aware
	// inlined __str_idx helper, which spills inline-tagged
	// strings to the .bss `__fern_str_idx_scratch` slot before
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
	// vtables holds the per-(trait,concrete) dispatch tables for
	// `dyn Trait` values (ir.collectVtables). OpConstVtable looks up
	// the method list here to emit the .rodata vtable cell. See
	// docs/DYN-TRAITS.md §4.2.2.
	vtables []ir.VtableDecl
	// dynVtableCells tracks the (trait, concrete) pairs referenced via
	// OpConstVtable. Each gets a `.rodata` symbol holding `len(methods)`
	// 8-byte absolute function pointers (interned per pair). Key is
	// "<trait>/<concrete>".
	dynVtableCells map[string]bool
	// usesArrEmpty gates the `.LArr_Empty` sentinel — a shared
	// static 4-byte `[length=0]` buffer that __alloc_u8(0)
	// returns instead of allocating a fresh length-only block.
	// Mirrors the .LStr_Empty pattern for the array seam.
	usesArrEmpty bool
	usesStdin    bool
	// usesRawIntPokes pulls in `__store_i32` / `__load_i32` /
	// `__store_ptr` / `__load_ptr` / `__ptr_width` — primitives
	// the lang Map runtime uses for its mixed bucket-index +
	// entries buffer. Single mov + ret each.
	usesRawIntPokes bool
	// usesMemset gates emission of the byte-grain
	// `__memset(dst, byte, n)` helper the Map clear path uses.
	usesMemset bool
	// usesRcInc / usesRcDec gate the refcount inc/dec runtime
	// helpers. Set when an OpCallDirect with target
	// "__fern_rc_inc" / "__fern_rc_dec" is reached. The IR
	// hasn't started emitting them yet (Phase 1c) but the
	// helpers + sentinel value (`.LArr_Empty`'s rc word =
	// 0x80000000) are in place so subsequent phases can wire
	// them in without touching the asm seam again. See
	// docs/RC-PERCEUS-PLAN.md.
	usesRcInc bool
	usesRcDec bool
	// rcInlineOK gates the #4402 opt-2b inline rc fast path per function
	// (arm64 parity — the arm64 backend carries the same field). Inlining
	// expands each OpRcInc / OpRcDec / OpRcIsUnique from a single `call`
	// into ~10 instructions; in the self-host compiler's largest lowering
	// function (irlower__lower_expr, ~9.75M IR ops with ~1.66M rc ops) that
	// bloat balloons the emitted `.s` — the inlined rc sequences alone add
	// hundreds of MB, and GNU `as` on the resulting ~1 GB driver `.s` peaks
	// at ~11 GB RSS, which is what forced the swap file the test harness
	// used to need. Set false for such a function (see rcInlineMaxOps) so
	// its rc ops fall back to the `call` form the runtime helper already
	// provides (behaviour-identical — the inline path mirrors the helper
	// instruction-for-instruction), shrinking the `.s` and its assembler
	// footprint. Every normal function (all user code, and every self-host
	// function but the one monster) stays on the inline fast path. Unlike
	// arm64 — where the same field also dodges the ±128 MB branch-reach
	// overflow — x86-64's rel32 jumps never overflow, so here the sole
	// motive is `.s` size / assembler memory.
	rcInlineOK bool

	// syscalls is the exact set of Linux syscall numbers this program's
	// emitted text can issue, accumulated by emitSyscall /
	// emitSyscallPreloaded. Exact rather than approximate because those
	// two are the only way to emit the instruction — see their comments.
	// Read back by Syscalls() after Emit.
	syscalls map[int]bool
	// usesRcUnderflowCount gates the Phase 3 detector reader
	// `__fern_rc_underflow_count` (returns the BSS over-release
	// counter __fern_rc_dec bumps). Set when the IR emits the
	// matching OpCallDirect.
	usesRcUnderflowCount bool
	// usesArrPushSharedCount gates the reader
	// `__fern_arr_push_shared_count` (returns the BSS counter
	// __fern_arr_push_grow bumps when it copies a buffer that had room).
	// Set when the IR emits the matching OpCallDirect.
	usesArrPushSharedCount bool
	// usesArrPushSharedBytes gates the reader
	// `__fern_arr_push_shared_bytes` (returns the BSS accumulator
	// __fern_arr_push_grow adds to at the same cliff the counter counts).
	// Set when the IR emits the matching OpCallDirect.
	usesArrPushSharedBytes bool
	// usesMapHashSeed gates `__fern_map_hash_seed` — core/map's
	// per-process string-hash seed (#6194). Set when the IR emits the
	// matching OpCallDirect; also pulls in __fern_random_i32, which fills
	// the cached word on first call.
	usesMapHashSeed bool
	// usesHeapBumpBytes gates the Phase 6 measurement reader
	// `__fern_heap_bump_bytes` (returns __fern_heap_ptr − __fern_heap_base,
	// the bump high-water mark). Set when the IR emits the matching
	// OpCallDirect; also pulls in the allocator (it reads its cursor).
	usesHeapBumpBytes bool
	// usesHeapMark gates the one-level arena checkpoint pair
	// `__fern_heap_mark` / `__fern_heap_release_to`. Set when the IR emits
	// either matching OpCallDirect; also pulls in the allocator (the pair
	// rewinds its cursor and snapshots its freelist heads).
	usesHeapMark bool
	// usesArrPushGrow gates `__fern_arr_push_grow` — the Phase 2
	// helper called by `emitArrayPush` to decide between in-
	// place mutation (rc==1 + cap available) and copy-into-new-
	// buffer (rc>1 OR cap exhausted). See
	// docs/RC-PERCEUS-PLAN.md "Phase 2".
	usesArrPushGrow bool
	// usesArrPushGrowPtr gates `__fern_arr_push_grow_ptr` — the
	// rc-tracked-element variant of __fern_arr_push_grow, used by the
	// IR's `.append` lowering when the element is a single-word
	// rc-tracked pointer (string / struct / enum / array / tuple /
	// closure). On the COPY path (rc>1 or capacity exhausted) it inc's
	// each copied element so the fresh buffer independently owns them.
	// The plain helper's raw memcpy left the copy sharing the old
	// buffer's element pointers at unchanged rc; when the old buffer's
	// walk-drop (__fern_drop_arr_str / __drop_arr_struct_<E>) later ran
	// at rc==1 it freed elements the grown copy still referenced — the
	// #3425 self-host-driver heap corruption (poison-mode-confirmed
	// use-after-free on EmitState.needed's "struct_drop:<T>" keys).
	usesArrPushGrowPtr bool
	// usesArrPushGrowMovePtr gates `__fern_arr_push_grow_move_ptr` — the
	// self-append (`a = a.append(v)`) sibling of __fern_arr_push_grow_ptr.
	// It retains the copied elements only when the incoming rc != 1, i.e.
	// only when the assign's buffer-only __fern_arr_dec will leave the old
	// buffer alive under an alias. At rc==1 that dec frees the old buffer
	// without walking, so the elements transfer and a retain would leak
	// one reference each (#3457).
	usesArrPushGrowMovePtr bool
	// usesArrCowInPlace gates `__fern_arr_cow_inplace` — the
	// Phase 2b helper called by the IR's `arr[i] = v` lowering
	// for local-ident array targets. See arm64's mirror.
	usesArrCowInPlace bool
	// usesArrCowInPlacePtr gates `__fern_arr_cow_inplace_ptr` — the
	// pointer-element variant of __fern_arr_cow_inplace, used by the
	// IR's `.with` / `arr[i]=v` lowering when the array element is a
	// single-word rc-tracked pointer (struct / enum / array / tuple /
	// closure). On the COPY path it inc's each copied element so the
	// fresh buffer independently owns them (the plain helper's raw
	// memcpy would leave the copy sharing the receiver's elements at
	// unchanged rc — a use-after-free once either array is dropped).
	usesArrCowInPlacePtr bool
	// usesDropArrPtr gates `__fern_drop_arr_ptr` — the Phase 3
	// drop handler for arrays of pointer-shaped rc-tracked
	// elements. See arm64's mirror + the wasm runtime.
	usesDropArrPtr bool
	// usesDropArrStr gates `__fern_drop_arr_str` — the native single-word
	// string[] drop (per-element __fern_str_dec then free the buffer).
	usesDropArrStr bool
	// usesRcIsUnique gates `__fern_rc_is_unique` — the guarded
	// "last reference?" check used by the Phase 3 struct drop.
	usesRcIsUnique bool
	// usesFree gates `__fern_free` — the Phase 3 step-4 freelist
	// return path. Pulls in __fern_alloc (shares the freelist BSS).
	usesFree bool
	// usesArrDec gates `__fern_arr_dec` — the size-aware array dec
	// that frees the buffer at rc==0 (plain-array scope-exit +
	// array dec-on-overwrite). Pulls in __fern_free.
	usesArrDec bool
	// usesAllocReuse gates `__fern_alloc_reuse` — the Phase 5
	// drop-reuse (FBIP) primitive `(token, tokenSize, size) -> ptr`
	// that reuses a dropped block's storage in place when its size
	// class matches, else frees it and allocates afresh. Pulls in
	// __fern_alloc + __fern_free.
	usesAllocReuse bool
	// usesMapDrop gates `__fern_map_drop` — the Phase 3 map
	// reclamation handler that frees the buf + handle at rc==1
	// (Map scope-exit). Pulls in __fern_free when the flag is on.
	usesMapDrop bool
	// usesBoxFree gates `__fern_box_free` — the Phase 3 box
	// reclamation helper `(data, size) -> data` that returns a
	// struct/enum box (base = data-8) to the freelist. The IR
	// pre-gates it on rc==1 (is_unique), so the helper is just a
	// uniform-result __free wrapper. Pulls in __fern_free.
	usesBoxFree bool
	// usesClosureDrop gates `__fern_closure_drop` — the closure
	// env/pair reclamation helper. At a FuncType local's last
	// reference (rc==1) it frees the rc1 block (size at data-4,
	// stashed by __fern_alloc_rc1); otherwise it dec's. Tail-calls
	// __fern_box_free / __fern_rc_dec, so it pulls both in.
	usesClosureDrop bool
	// usesStrDec gates `__fern_str_dec` — the single-word (x86-64)
	// heap-string reclamation helper. At a string value's last
	// reference (rc==1) it frees the rc1 block (size at data-4,
	// stashed by __fern_alloc_rc1 inside __fern_strcat et al.);
	// inline-SSO / literal / sentinel / shared values defer to
	// __fern_rc_dec. Mirrors __fern_closure_drop, so it pulls in
	// __fern_box_free / __fern_rc_dec / the freelist BSS.
	usesStrDec bool
	// usesReadFile / usesWriteFile pull in the file-I/O
	// runtimes; usesIoError pulls in the shared
	// `__fern_io_error(errno, path) → IoError box` helper.
	usesReadFile  bool
	usesWriteFile bool
	// usesWriteFileExec is write_file_exec — write_file with the
	// executable bit. Its own flag so a program that never asks for
	// one does not carry the second helper body (#6133).
	usesWriteFileExec bool
	// usesRemoveDirAll pulls in the recursive `rm -rf` runtime
	// (`__fern_remove_dir_all(path) → Option[IoError]`) — the
	// x86-64 sibling of arm64-ssa's emitRemoveDirAllHelper. It's
	// what std/test's TestRunner.finish() needs to clean up its
	// temp dirs when a TAP program links through the native CLI.
	usesRemoveDirAll bool
	// usesRemoveFile / usesTempDir / usesReadDir / usesStat pull in
	// the rest of the filesystem-op family (#5372): unlinkat /
	// mkdirat / getdents64 / newfstatat runtimes returning the same
	// Option[IoError] / Result[_, IoError] box shapes as the file-I/O
	// helpers above.
	usesRemoveFile bool
	usesTempDir    bool
	usesReadDir    bool
	usesStat       bool
	usesIoError    bool

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
	case "__c_call0":
		g.usesCCall[0] = true
	case "__c_call1":
		g.usesCCall[1] = true
	case "__c_call2":
		g.usesCCall[2] = true
	case "__c_call3":
		g.usesCCall[3] = true
	case "__c_call4":
		g.usesCCall[4] = true
	case "__c_call0_f32":
		g.usesCCallF32[0] = true
	case "__c_call1_f32":
		g.usesCCallF32[1] = true
	case "__c_call2_f32":
		g.usesCCallF32[2] = true
	case "__c_call3_f32":
		g.usesCCallF32[3] = true
	case "__c_call4_f32":
		g.usesCCallF32[4] = true
	case "__c_call0_f64":
		g.usesCCallF64[0] = true
	case "__c_call1_f64":
		g.usesCCallF64[1] = true
	case "__c_call2_f64":
		g.usesCCallF64[2] = true
	case "__c_call3_f64":
		g.usesCCallF64[3] = true
	case "__c_call4_f64":
		g.usesCCallF64[4] = true
	case "__memcpy":
		g.usesMemcpy = true
	case "__fern_rc_inc":
		g.usesRcInc = true
	case "__fern_rc_dec":
		g.usesRcDec = true
	case "__fern_closure_drop":
		g.usesClosureDrop = true
		g.usesBoxFree = true // tail-called on rc==1
		g.usesFree = true    // box_free → __fern_free
		g.usesAlloc = true   // shares the freelist BSS
		g.usesRcDec = true   // tail-called otherwise
	case "__fern_str_dec":
		g.usesStrDec = true
		g.usesBoxFree = true // tail-called on rc==1
		g.usesFree = true    // box_free → __fern_free
		g.usesAlloc = true   // shares the freelist BSS
		g.usesRcDec = true   // deferred to otherwise
	case "__fern_str_append":
		g.usesStrAppend = true
		g.usesStrcat = true  // the copy path calls it
		g.usesMemcpy = true  // both paths copy bytes
		g.usesAlloc = true   // strcat's fresh buffer + the freelist BSS
		g.usesStrDec = true  // releases the consumed accumulator
		g.usesBoxFree = true // str_dec → box_free at rc==1
		g.usesFree = true
		g.usesRcDec = true
	case "__fern_rc_underflow_count":
		g.usesRcUnderflowCount = true
	case "__fern_arr_push_shared_count":
		g.usesArrPushSharedCount = true
	case "__fern_arr_push_shared_bytes":
		g.usesArrPushSharedBytes = true
	case "__fern_map_hash_seed":
		g.usesMapHashSeed = true
		g.usesRandomI32 = true // the lazy first-call draw
	case "__fern_memchr":
		g.usesMemchr = true
	case "__fern_heap_bump_bytes":
		g.usesHeapBumpBytes = true
		g.usesAlloc = true // reads __fern_heap_ptr / __fern_heap_base
	case "__heap_mark", "__heap_release_to":
		g.usesHeapMark = true
		g.usesAlloc = true // rewinds __fern_heap_ptr; shadows the freelist heads
	case "__fern_arr_push_grow":
		g.usesArrPushGrow = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "__fern_arr_push_grow_ptr":
		g.usesArrPushGrowPtr = true
		g.usesAlloc = true
		g.usesMemcpy = true
		g.usesRcInc = true
	case "__fern_arr_push_grow_move_ptr":
		g.usesArrPushGrowMovePtr = true
		g.usesAlloc = true
		g.usesMemcpy = true
		g.usesRcInc = true
	case "__fern_arr_cow_inplace":
		g.usesArrCowInPlace = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "__fern_arr_cow_inplace_ptr":
		g.usesArrCowInPlacePtr = true
		g.usesAlloc = true
		g.usesMemcpy = true
		g.usesRcInc = true
	case "__fern_drop_arr_ptr":
		g.usesDropArrPtr = true
		g.usesRcDec = true
		if ast.RcFreeEnabled {
			// Flag-on, the drop frees the buffer at rc==1.
			g.usesFree = true
			g.usesAlloc = true
		}
	case "__fern_drop_arr_str":
		g.usesDropArrStr = true
		g.usesStrDec = true // per-element free
		g.usesRcDec = true  // plain-dec fallback + str_dec defer
		g.usesBoxFree = true
		if ast.RcFreeEnabled {
			g.usesFree = true
			g.usesAlloc = true
		}
	case "__fern_rc_is_unique":
		g.usesRcIsUnique = true
	case "__alloc":
		g.usesAlloc = true
	case "__free":
		g.usesFree = true
		g.usesAlloc = true
	case "__alloc_reuse":
		g.usesAllocReuse = true
		g.usesFree = true
		g.usesAlloc = true
	case "__fern_arr_dec":
		g.usesArrDec = true
		g.usesFree = true
		g.usesAlloc = true
	case "__fern_map_drop":
		g.usesMapDrop = true
		if ast.RcFreeEnabled {
			g.usesFree = true
			g.usesAlloc = true
		}
	case "__fern_box_free":
		g.usesBoxFree = true
		g.usesFree = true
		g.usesAlloc = true
	case "__slice_make":
		g.usesSliceMake = true
		g.usesAlloc = true
	case "__slice_range":
		g.usesSliceRange = true
	case "__fern_strcat":
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
	case "strbuf_reset", "strbuf_append", "strbuf_take":
		g.usesStrBuf = true
		if target == "strbuf_take" {
			g.usesAlloc = true
			g.usesMemcpy = true
		}
		if target == "strbuf_append" {
			g.usesMemcpy = true
		}
	case "now_unix_ms":
		g.usesNowUnixMs = true
	case "monotonic_ns":
		g.usesMonotonicNs = true
	case "now_ns":
		g.usesNowNs = true
	case "sleep_ms":
		g.usesSleepMs = true
	case "proc_exec":
		g.usesProcExec = true
		g.usesAlloc = true // NUL-terminated argv copies
		// __fern_envp (its .bss slot AND the _start capture) is gated on
		// usesEnv, and execve passes it as the child's environment. That
		// pulls in the env READER too, which strcats — hence memcpy.
		g.usesEnv = true
		g.usesMemcpy = true
	case "proc_fork":
		g.usesProcFork = true
	case "proc_waitpid":
		g.usesProcWaitpid = true
	case "tcp_listen", "tcp_accept", "tcp_recv", "tcp_send", "tcp_close", "tcp_connect", "tcp_pollable":
		g.usesTcp = true
		// usesTcp always emits the __fern_tcp_recv helper, which calls
		// __fern_alloc_rc1 for its read buffer — so any tcp builtin
		// needs the alloc runtime present, even a connect-only program.
		g.usesAlloc = true
	case "wasm_pollable_drop":
		// On native a pollable is just an fd (no separate resource to
		// drop), so this is a no-op helper — present so std/async's
		// fetch_future (which drops the wasm pollable before close)
		// compiles + runs portably.
		g.usesWasmPollableDrop = true
	case "wasm_block":
		// On native there's no pollable to wait on (a deadline comes from
		// poll(2)'s own timeout arg — wasm_timer_pollable returns -1), so
		// blocking is a no-op returning 0. Present so std/async's
		// with_deadline (which blocks on a timer pollable on wasm) is portable.
		g.usesWasmBlock = true
	case "wasm_timer_pollable":
		// On native there's no pollable to make — a deadline comes from
		// poll(2)'s own timeout arg — so this returns -1 (an fd poll(2)
		// ignores). Present so std/async's with_deadline (which appends a
		// timer pollable to the poll set on wasm) is portable.
		g.usesWasmTimerPollable = true
	case "wasm_poll":
		// wasm_poll(pollables) — the wasm reactor's readiness multiplexer
		// (wasi:io/poll.poll on wasm). Native has no real pollables (a
		// timer pollable is -1 and native uses poll(2) directly), so this
		// returns -1 (nothing ready). Present so std/async's wasm reactor
		// path is portable.
		g.usesWasmPoll = true
	case "poll":
		// poll(fds, timeout_ms) — readiness multiplex. The runtime
		// helper allocates a scratch pollfd buffer.
		g.usesPoll = true
		g.usesAlloc = true
	case "timer_fd":
		// timer_fd(ms) — a timerfd readable after `ms`.
		g.usesTimerFd = true
	case "env":
		g.usesEnv = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "args":
		g.usesArgs = true
		g.usesAlloc = true
		g.usesMemcpy = true
	case "__alloc_u8":
		g.usesAllocU8 = true
		g.usesAlloc = true
	case "string_from_bytes_unchecked":
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
	case "random_i32":
		g.usesRandomI32 = true
	case "__method_string_as_bytes":
		g.usesAsBytes = true
		g.usesSliceMake = true
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
	case "__store_i32", "__load_i32", "__store_ptr", "__load_ptr", "__ptr_width", "__load_i64", "__store_i64":
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
	case "write_file_exec":
		g.usesWriteFileExec = true
		g.usesAlloc = true
		g.usesIoError = true
	case "remove_dir_all":
		g.usesRemoveDirAll = true
		g.usesAlloc = true
		g.usesIoError = true
	case "remove_file":
		g.usesRemoveFile = true
		g.usesAlloc = true
		g.usesIoError = true
	case "temp_dir":
		g.usesTempDir = true
		g.usesMonotonicNs = true
		g.usesAlloc = true
		g.usesIoError = true
	case "read_dir":
		g.usesReadDir = true
		g.usesAlloc = true
		g.usesIoError = true
	case "stat":
		g.usesStat = true
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
// callReturnsVoid reports whether a function returns void —
// i.e. the OpCallDirect emit should NOT push rax onto the
// operand stack after `call`. Looks up user functions in
// g.funcs and built-in helpers via g.info.FuncSigs.
//
// Without this gate, void-returning helpers leave a phantom
// rax value on the operand stack — System V x86-64 callees
// don't promise to clear rax, so the unconditional push
// corrupts subsequent OpStore pops. Hit by `arr.push(v)`
// inside a struct literal field initialiser: the inner
// `__memcpy` call left a phantom slot that the outer
// struct-lit's OpStore consumed instead of the field address.
func (g *generator) callReturnsVoid(name string) bool {
	if callee, ok := g.funcs[name]; ok && callee != nil {
		_, isVoid := callee.ReturnType.(ast.VoidType)
		return isVoid
	}
	if g.info != nil {
		if sig, ok := g.info.FuncSigs[name]; ok && sig != nil {
			_, isVoid := sig.Result.(ast.VoidType)
			return isVoid
		}
	}
	return false
}

// ccallFloatRetWidth reports the FP-return width (32 / 64) of a
// `__c_call<n>_f32` / `_f64` FFI shim, or 0 for any other call. These shims
// tail-jump to a C-ABI function, so an FP result comes back in xmm0 (the C
// convention) — but Fern keeps f32/f64 operand-stack values in rax (the
// regular Fern-call convention). After such a call the result must be moved
// xmm0→rax before it's pushed, or it's used as garbage. Regular Fern
// FP-returning functions already deliver their result in rax, so the move is
// specific to these C-ABI shims.
func ccallFloatRetWidth(name string) int {
	if !strings.HasPrefix(name, "__c_call") {
		return 0
	}
	if strings.HasSuffix(name, "_f64") {
		return 64
	}
	if strings.HasSuffix(name, "_f32") {
		return 32
	}
	return 0
}

// invariant the rest of the code expects.
// emitPieSelfReloc emits the static-PIE self-relocation prologue at the top
// of `_start`. It derives the kernel-chosen load base via __ehdr_start
// (vaddr 0, so [rip+__ehdr_start] yields base), then walks the
// R_X86_64_RELATIVE entries in [__rela_start, __rela_end) — the
// `.quad <symbol>` slots — applying each as *(base + r_offset) =
// base + r_addend. rip-relative code needs no relocation, so reloc-free
// programs run an empty loop. Uses rax/rsi/rdi/rcx/rdx (the entry-time
// rdx atexit pointer is unused under -nostdlib) and leaves rsp untouched,
// so the argv-reading startup that follows is unchanged.
func (g *generator) emitPieSelfReloc() {
	g.emit("lea rax, [rip + __ehdr_start]") // rax = load base
	g.emit("lea rsi, [rip + __rela_start]") // rsi = &.rela.dyn (cursor)
	g.emit("lea rdi, [rip + __rela_end]")   // rdi = end
	g.label(".Lfern_reloc_loop")
	g.emit("cmp rsi, rdi")
	g.emit("jae .Lfern_reloc_done")
	g.emit("mov rcx, [rsi]")       // r_offset
	g.emit("mov rdx, [rsi + 16]")  // r_addend (r_info at +8 is RELATIVE)
	g.emit("add rdx, rax")         // base + addend
	g.emit("mov [rax + rcx], rdx") // *(base + r_offset) = base + addend
	g.emit("add rsi, 24")          // advance one Elf64_Rela (24 bytes)
	g.emit("jmp .Lfern_reloc_loop")
	g.label(".Lfern_reloc_done")
}

func (g *generator) emitStartRuntime() {
	g.line("")
	g.line(".globl _start")
	g.label("_start")
	if g.pie {
		g.emitPieSelfReloc()
	}
	if ast.SandboxEnabled {
		// Seccomp sandbox (#6071): install before anything else runs, so
		// the filter covers the whole program including the arena mmap.
		// The helper is emitted LATE (emitSeccompRuntime, after all other
		// code) because its allowlist is g.syscalls, which is not complete
		// until every emitter has run. Label resolution is
		// order-independent, so calling it from here is fine.
		//
		// Deliberately before the argv/envp stash rather than after: those
		// are plain loads, but keeping the filter first means any future
		// startup step is covered by default instead of by remembering.
		g.emit("call __fern_seccomp_install")
	}
	// argc is at [rsp+0]; argv starts at [rsp+8]; envp at
	// [rsp + 8 + (argc+1)*8] (NULL-terminator after argv).
	// Stash whichever of (argc, argv, envp) the program
	// actually needs — gated so trivial programs don't pay
	// for the extra mov chain.
	if g.usesArgs {
		g.emit("mov rax, [rsp]")
		g.emit("mov [rip + __fern_argc], rax")
		g.emit("lea rcx, [rsp + 8]")
		g.emit("mov [rip + __fern_argv], rcx")
	}
	if g.usesEnv {
		g.emit("mov rax, [rsp]")             // argc
		g.emit("lea rdi, [rsp + 8]")         // rdi = &argv[0]
		g.emit("lea rdi, [rdi + rax*8 + 8]") // skip argv + NULL terminator
		g.emit("mov [rip + __fern_envp], rdi")
	}
	g.emit("call main")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): print the alloc/free summary
		// before exiting. main's return value parks in rbx (callee-save,
		// and _start has no caller to preserve it for; the report helper
		// itself only touches caller-saved registers) so the exit code
		// survives the report's syscalls.
		g.emit("mov ebx, eax")
		g.emit("call __fern_lc_report")
		g.emit("mov edi, ebx")
	} else {
		g.emit("mov edi, eax") // exit code = main's return value
	}
	g.emitSyscall(sysExitGroup)
}

// rcInlineMaxOps is the per-function IR-op ceiling for the opt-2b inline rc
// fast path (see the rcInlineOK field). Matches the arm64 backend's threshold
// so both backends flip exactly the same function (irlower__lower_expr,
// ~9.75M ops) to the `call` form: 1M sits ~2× above the largest normal
// self-host function (~0.5M ops) and ~10× below lower_expr, so every
// user-scale function keeps the inline win. A var (not a const) only so the
// backend's own tests can lower it to exercise the fall-back on a small
// function; production never reassigns it.
var rcInlineMaxOps = 1_000_000

// emitFunc lowers one function to assembly. Per-function
// scope-tracking state lives in `scope` (currently unused —
// PR 1 has no block / loop / if ops to dispatch).
// x86RegisterName is the set of tokens the assembler — both this project's
// pure-Go one (internal/native/x86_64) and GNU as — resolves to a register.
// A user function whose name matches one would be mis-assembled: `call ch`
// reads `ch` as register CH and encodes an indirect `call rbp` through
// garbage (SIGSEGV), and `.size ch, .-ch` / `.quad ch` fail to evaluate.
var x86RegisterName = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{
		"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
		"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d",
		"ax", "cx", "dx", "bx", "sp", "bp", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w",
		"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b",
		"ah", "ch", "dh", "bh", "st",
	} {
		m[n] = true
	}
	for i := 0; i < 16; i++ {
		m[fmt.Sprintf("xmm%d", i)] = true
	}
	return m
}()

// asmFnName returns the asm symbol for a Fern function `name`, escaping
// names that collide with an x86 register mnemonic (register names are
// case-insensitive to the assembler, so the check folds case). The `$`
// suffix is collision-proof: Fern identifiers cannot contain `$`, so no
// real function name can equal an escaped one, and both assemblers accept
// `$` in a symbol. Non-colliding names — the overwhelming majority,
// including every `__fern_*` runtime helper — pass through unchanged, so
// applying it to any function symbol (user or runtime) is safe. It must be
// used at EVERY site that emits a function name as an asm token
// (definition, call, and `.quad` pointer), or the definition and its
// references disagree and the link fails with an undefined symbol.
func asmFnName(name string) string {
	if x86RegisterName[strings.ToLower(name)] {
		return name + "$fn"
	}
	return name
}

func (g *generator) emitFunc(fn *ast.FuncDecl, irFn *ir.Func) error {
	// #4402 opt 2b: inline rc ops only when the function is small enough that
	// the ~10-instruction-per-op expansion doesn't balloon the emitted `.s`
	// (and the assembler's peak RSS). Only the self-host compiler's largest
	// lowering function exceeds this; see the rcInlineOK field comment.
	g.rcInlineOK = len(irFn.Ops) <= rcInlineMaxOps

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

	sym := asmFnName(fn.Name)
	g.line("")
	g.line(fmt.Sprintf(".globl %s", sym))
	g.line(fmt.Sprintf(".type %s, @function", sym))
	g.label(sym)
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
	for i := 0; i < len(irFn.Ops); i++ {
		// Compare-and-branch fusion (#4378): an integer comparison whose
		// result flows directly into the OpIf / OpBrIf that follows it
		// (through zero or more OpNots) emits `cmp; jcc` instead of
		// materialising the boolean with setcc/movzx and re-testing it.
		// Safe because the IR is single-use: the branch is the sole
		// consumer of the comparison's pushed value.
		if adv, ok := g.tryFuseCmpBranch(irFn.Ops, i, &scope); ok {
			i += adv
			continue
		}
		if err := g.emitOp(irFn.Ops[i], retLabel, &scope); err != nil {
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
	g.line(fmt.Sprintf(".size %s, .-%s", sym, sym))
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
	case ir.OpLine:
		// Source-line marker → DWARF `.loc` directive (file 1). Emits no
		// machine code; the assembler records the current text offset as a
		// .debug_line row. `.loc` never matches a peephole instruction
		// pattern, so it only ever prevents a fusion at a line boundary —
		// never corrupts one.
		g.line(fmt.Sprintf("\t.loc 1 %d %d", op.Pos.Line, op.Pos.Col))
	case ir.OpConstI32:
		// Materialise the immediate into rax, then push.
		// `mov eax, imm32` zero-extends the high 32 bits of
		// rax, keeping the encoding compact for the common
		// case. Negative values are written as `mov eax, N`
		// with N's two's-complement bit pattern (assembler
		// accepts negative imm32 directly).
		//
		// Zero takes `xor eax, eax` instead: 2 bytes against
		// mov's 5, and it also zero-extends into rax. Literal
		// zero is everywhere (every `0` in source, every
		// zero-init, every implicit `return 0`), so the 3
		// bytes compound (#4380 lever 2) — ~1% of emitted code
		// across the examples.
		//
		// `xor` clobbers FLAGS where `mov` does not. That is safe
		// because FLAGS are never live across an IR-op boundary in
		// this backend: every flag producer is emitted together
		// with its consumer inside a single op's expansion (OpEq
		// and friends emit `cmp` + `setcc` back to back), and the
		// cmp/branch fusion peephole only rewrites a pair that is
		// ALREADY adjacent. A const materialisation is its own IR
		// op, so it can never land between a flag-setter and its
		// reader. (Do not weaken that invariant without revisiting
		// this.)
		if op.I32 == 0 {
			g.emit("xor eax, eax")
		} else {
			g.emit(fmt.Sprintf("mov eax, %d", op.I32))
		}
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
		g.pop() // payload → rax
		g.emit("mov rdx, rax")
		g.pop() // tag → rax
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
		// Skip the top operand-stack slot.
		g.emit(fmt.Sprintf("add rsp, %d", slotBytes))

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
		g.emitIntDivRem(op, false)
	case ir.OpRemS:
		g.emitIntDivRem(op, true)
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
		// in rcx, so we can shift directly. The destination
		// register width must match the integer width: a 32-bit
		// value rides zero-extended in the low half of rax, so a
		// 64-bit `shl rax` would mask the count to 0..63 and let
		// bits spill above bit 31 — `shl eax` masks to 0..31 and
		// keeps the result in the canonical i32 lane (matching the
		// wasm / interp shift-count semantics).
		g.binPop()
		g.emit(fmt.Sprintf("shl %s, cl", g.aRegForWidth(op.Width)))
		g.push()
	case ir.OpShrS:
		// sar (arithmetic right shift) preserves the sign
		// bit for signed values; shr (logical) zero-fills
		// for unsigned. Width matters for `sar`: a negative i32
		// rides zero-extended in rax (high half all zero), so
		// `sar rax` would read bit 63 (= 0) as the sign and
		// produce a logical-looking result. `sar eax` reads the
		// real i32 sign bit (bit 31). Both forms also mask the
		// count to the width (0..31 for i32, 0..63 for i64).
		g.binPop()
		reg := g.aRegForWidth(op.Width)
		if op.Unsigned {
			g.emit(fmt.Sprintf("shr %s, cl", reg))
		} else {
			g.emit(fmt.Sprintf("sar %s, cl", reg))
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
		g.cmpForWidth(op.Width)
		g.emit("sete al")
		g.emit("movzx eax, al")
		g.push()
	case ir.OpNe:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("setne al")
		g.emit("movzx eax, al")
		g.push()
	case ir.OpLtS:
		g.binPop()
		g.cmpForWidth(op.Width)
		if op.Unsigned {
			g.emit("setb al")
		} else {
			g.emit("setl al")
		}
		g.emit("movzx eax, al")
		g.push()
	case ir.OpLeS:
		g.binPop()
		g.cmpForWidth(op.Width)
		if op.Unsigned {
			g.emit("setbe al")
		} else {
			g.emit("setle al")
		}
		g.emit("movzx eax, al")
		g.push()
	case ir.OpGtS:
		g.binPop()
		g.cmpForWidth(op.Width)
		if op.Unsigned {
			g.emit("seta al")
		} else {
			g.emit("setg al")
		}
		g.emit("movzx eax, al")
		g.push()
	case ir.OpGeS:
		g.binPop()
		g.cmpForWidth(op.Width)
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
		// semantics: an unordered result (either operand
		// NaN) sets ZF=CF=PF=1. setcc on ZF/CF alone would
		// then misreport NaN comparisons (e.g. `sete` is true
		// for NaN==NaN, `setb` true for NaN<x), so we fold in
		// the parity flag to get IEEE-correct unordered
		// behaviour matching interp / arm64 / wasm: eq/lt/le
		// require "not unordered" (PF=0); ne is true when
		// unordered (PF=1). gt/ge use seta/setae, which read
		// CF and are already false on unordered — no fixup.
		g.fbinPop(op.Width)
		if op.Width == 64 {
			g.emit("ucomisd xmm1, xmm0")
		} else {
			g.emit("ucomiss xmm1, xmm0")
		}
		switch op.Kind {
		case ir.OpFEq:
			g.emit("sete al")
			g.emit("setnp cl")
			g.emit("and al, cl")
		case ir.OpFNe:
			g.emit("setne al")
			g.emit("setp cl")
			g.emit("or al, cl")
		case ir.OpFLt:
			g.emit("setb al")
			g.emit("setnp cl")
			g.emit("and al, cl")
		case ir.OpFLe:
			g.emit("setbe al")
			g.emit("setnp cl")
			g.emit("and al, cl")
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
	case ir.OpReinterpretI32F32, ir.OpReinterpretF32I32,
		ir.OpReinterpretI64F64, ir.OpReinterpretF64I64:
		// Bit-cast between f32 and i32. The operand stack
		// already stores both as raw 32-bit values (see
		// OpConstF32 — the f32 bit pattern goes onto the
		// stack via `mov eax, <bits>`), and the consuming
		// op picks the right register bank (general-purpose
		// vs XMM) via `movd` when needed. Nothing to emit.
	case ir.OpITruncF32, ir.OpITruncF64:
		// f32 / f64 → i32 / i64, saturating (NaN → 0, out-of-range
		// clamps to the destination min/max). See emitFloatToIntSat.
		g.pop()
		isF64 := op.Kind == ir.OpITruncF64
		if isF64 {
			g.emit("movq xmm0, rax")
		} else {
			g.emit("movd xmm0, eax")
		}
		g.emitFloatToIntSat(isF64, op.Width, op.Unsigned)
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

	// Bit counting: one instruction each, no branch. The target CPU
	// baseline is Haswell-class (2013), which covers BMI1's LZCNT/TZCNT
	// as well as SSE4.2's POPCNT.
	//
	// LZCNT and TZCNT are DEFINED at a zero input — they return the
	// operand width, which is exactly what the IR op defines — so the
	// zero branch the old bsr/bsf lowering needed is gone rather than
	// merely predicted. That definedness is the entire reason to prefer
	// them: bsr/bsf leave the destination UNDEFINED at zero.
	//
	// The flip side is a failure mode worth naming: below the baseline
	// these decode as bsr/bsf with an ignored F3 prefix, so a too-old
	// CPU answers a different question SILENTLY instead of faulting the
	// way POPCNT does.
	case ir.OpClz:
		g.pop()
		if op.Width == 64 {
			g.emit("lzcnt rax, rax")
		} else {
			g.emit("lzcnt eax, eax")
		}
		g.push()
	case ir.OpCtz:
		g.pop()
		if op.Width == 64 {
			g.emit("tzcnt rax, rax")
		} else {
			g.emit("tzcnt eax, eax")
		}
		g.push()
	case ir.OpPopcount:
		g.pop()
		if op.Width == 64 {
			g.emit("popcnt rax, rax")
		} else {
			g.emit("popcnt eax, eax")
		}
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

	case ir.OpAlloc:
		// Single i32 arg (byte count) — translate to a call
		// to the bump allocator. Recorded use-flag at scan
		// time so __fern_alloc actually gets emitted.
		g.pop()
		g.emit("mov rdi, rax")
		g.emit("call __fern_alloc")
		g.push()

	case ir.OpStrConcat:
		// The IR's `+` between strings lowers directly to
		// OpStrConcat (rather than going through
		// OpCallDirect __fern_strcat) so codegen owns the
		// dispatch. Stack: [a, b], top = b. Pop into rsi /
		// rdi to match the System V `__fern_strcat(a, b)`
		// signature, call, push result.
		g.binPop() // rcx = b, rax = a
		g.emit("mov rdi, rax")
		g.emit("mov rsi, rcx")
		g.emit("call __fern_strcat")
		g.push()

	case ir.OpStrEq:
		// String equality reduces to `__fern_strcmp(a, b) == 0`.
		// Pop both, call, test result for zero, push 0 / 1.
		g.binPop()
		g.emit("mov rdi, rax")
		g.emit("mov rsi, rcx")
		g.emit("call __fern_strcmp")
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
	// their runtime symbols (`print → __fern_puts`, etc.),
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
		g.emit(fmt.Sprintf("call %s", asmFnName(op.Str)))
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
		//      slot (since __fern_alloc clobbers caller-save
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
		g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
		g.emit("mov r11, [r10]")     // r11 = fn_ptr (= [pair + 0])
		g.emit("mov rax, [r10 + 8]") // rax = env_ptr (= [pair + 8])
		// Push env_ptr onto the operand stack so the args-load
		// helper picks it up in the (argc+1)th register slot.
		g.emit(fmt.Sprintf("sub rsp, %d", slotBytes))
		g.emit("mov [rsp], rax")
		g.emitCallArgsLoad(argc + 1)
		g.emit("call r11")
		g.emitCallArgsCleanup(argc + 1)
		g.push()

	case ir.OpConstVtable:
		// Materialise the address of the static (trait, concrete)
		// vtable into rax. The vtable is a `.rodata` array of absolute
		// `__method_*` function pointers, one per non-associated trait
		// method in declaration order (docs/DYN-TRAITS.md §4.2.2). On
		// natives the vtable holds POINTERS, not table indices (the wasm
		// form): OpCallDyn loads slot k and `call`s it directly.
		key := op.Str + "/" + op.Str2()
		if g.dynVtableCells == nil {
			g.dynVtableCells = map[string]bool{}
		}
		g.dynVtableCells[key] = true
		g.emit(fmt.Sprintf("lea rax, [rip + %s]", dynVtableLabel(op.Str, op.Str2())))
		g.push()

	case ir.OpBoxDyn:
		// Pack a boxed one-word `dyn Trait` value (docs/DYN-TRAITS.md
		// §4.2.2). Operand stack on entry: [data, vtable] (vtable on
		// top). Allocate a 16-byte {data @0, vtable @8} cell via the
		// normal __fern_alloc path, store both words, and push the cell
		// pointer. The cell is a plain heap object; precise RC of the
		// box is out of scope (it leaks — the interp doesn't RC trait
		// objects either).
		//
		// data/vtable must survive `call __fern_alloc`, which on its
		// heap-init / heap-grow path does an mmap `syscall` — that
		// clobbers r11 (the CPU stashes RFLAGS there) and uses r10 as a
		// syscall arg. So caller-save scratch (the old r10/r11) is NOT
		// safe across the call: when the box is the allocation that
		// triggers the grow (e.g. a `dyn` over a primitive, whose value
		// isn't separately heap-allocated, so the box is the program's
		// first alloc) the stored words came back as RFLAGS garbage and
		// the trait object segfaulted on first dispatch. Park them in
		// callee-saved rbx/r12 across the call instead (the x86-64 mirror
		// of arm64's x19/x20 choice in OpBoxDyn). Pop BOTH operands
		// first, into caller-save rax/rcx, BEFORE pushing rbx/r12 — a
		// push between the pops would shift rsp under the operand stack
		// and the second pop would read the saved register.
		g.pop()                // rax = vtable (top)
		g.emit("mov rcx, rax") // rcx = vtable (caller-save; no call before the save)
		g.pop()                // rax = data
		g.emit("push rbx")     // save callee-saved (2 pushes keep rsp 16-aligned)
		g.emit("push r12")
		g.emit("mov r12, rax") // r12 = data
		g.emit("mov rbx, rcx") // rbx = vtable
		g.emit("mov rdi, 16")  // cell size = 2 * ptrW
		g.emit("call __fern_alloc")
		g.emit("mov [rax], r12")     // cell[0] = data  (survived the call)
		g.emit("mov [rax + 8], rbx") // cell[8] = vtable
		g.emit("pop r12")            // restore callee-saved
		g.emit("pop rbx")
		g.push()

	case ir.OpCallDyn:
		// Dispatch a `dyn Trait` method call (docs/DYN-TRAITS.md
		// §4.2.2). Operand stack on entry: [data, args..., vtable]
		// (vtable on top). Pop the vtable, load slot `op.I32`'s 8-byte
		// function pointer (`vtable + slot*8`), then do an indirect call
		// with [data, args...] as the SysV args (receiver-first, plain —
		// no closure env). op.Sig() is the receiver-first method
		// signature; argc = len(params) (= 1 receiver + method args),
		// void iff Result == nil.
		if op.Sig() == nil {
			return fmt.Errorf("x86_64: OpCallDyn missing op.Sig()")
		}
		argc := len(op.Sig().Params)
		g.pop()                // rax = vtable (top)
		g.emit("mov r10, rax") // r10 = vtable base
		if op.I32 != 0 {
			g.emit(fmt.Sprintf("mov r11, [r10 + %d]", int(op.I32)*8))
		} else {
			g.emit("mov r11, [r10]")
		}
		// r11 = fn pointer. Stash it below the args while we load arg
		// registers (emitCallArgsLoad consumes operand-stack slots, so
		// r11 — a caller-save scratch the loader doesn't touch — is safe
		// to hold across it).
		g.emitCallArgsLoad(argc)
		g.emit("call r11")
		g.emitCallArgsCleanup(argc)
		if op.Sig().Result == nil {
			break
		}
		g.push()

	case ir.OpRcInc, ir.OpRcDec:
		// #4402 opt 2b: inline the rc fast path — the hot no-op guards
		// (null / SSO tag / below-heap / static sentinel) and the RMW
		// happen without a call or caller-save spills. Semantics mirror
		// emitRcIncRuntime / emitRcDecRuntime instruction-for-
		// instruction, including rc_dec's underflow-counter bump (the
		// helpers stay emitted: runtime code tail-calls them and the
		// debug build below still calls out). rc ops are pass-through —
		// rax holds the pointer and doubles as the result.
		if ast.RcFreeDebug || !g.rcInlineOK {
			// Debug builds keep the call: the helpers carry the
			// RcPoison use-after-free trap the inline path omits.
			// !rcInlineOK falls back to the call in functions too large to
			// absorb the inline bloat (see the rcInlineOK field) — the
			// helper is behaviour-identical to the inline sequence.
			g.emitCallArgsLoad(1)
			g.emit(fmt.Sprintf("call %s", asmFnName(op.Str)))
			g.push()
			return nil
		}
		done := fmt.Sprintf(".Lrcop_done_%d", g.labelCounter)
		g.labelCounter++
		g.pop()
		g.emit("test rax, rax")
		g.emit(fmt.Sprintf("jz %s", done))
		g.emit("test al, 1")
		g.emit(fmt.Sprintf("jnz %s", done))
		g.emit("cmp rax, 0x10000000")
		g.emit(fmt.Sprintf("jb %s", done))
		g.emit("mov ecx, dword ptr [rax - 8]")
		g.emit("test ecx, ecx")
		g.emit(fmt.Sprintf("js %s", done))
		if op.Kind == ir.OpRcInc {
			g.emit("add ecx, 1")
		} else {
			// Underflow detector: a healthy dec sees rc >= 1; rc <= 0
			// here is an over-release — bump the counter, then still
			// decrement (mirrors the helper).
			decLbl := fmt.Sprintf(".Lrcop_dec_%d", g.labelCounter)
			g.labelCounter++
			g.emit("cmp ecx, 0")
			g.emit(fmt.Sprintf("jg %s", decLbl))
			g.emit("add dword ptr [rip + __fern_rc_underflow], 1")
			g.rcUnderflowTrap()
			g.label(decLbl)
			g.emit("sub ecx, 1")
		}
		g.emit("mov dword ptr [rax - 8], ecx")
		g.label(done)
		g.push()

	case ir.OpRcIsUnique:
		// #4402 opt 2b: inline is_unique — load, sentinel test, ==1
		// compare. Mirrors emitRcIsUniqueRuntime (note its guards
		// differ from inc/dec: low bound 0x10000, no SSO-tag test).
		if ast.RcFreeDebug || !g.rcInlineOK {
			// !rcInlineOK falls back to the behaviour-identical helper in
			// oversized functions (see the rcInlineOK field). The pre-scan
			// recorded the "__fern_rc_is_unique" use, so the helper is
			// emitted regardless of inline-vs-call.
			g.emitCallArgsLoad(1)
			g.emit(fmt.Sprintf("call %s", asmFnName(op.Str)))
			g.push()
			return nil
		}
		uniqDone := fmt.Sprintf(".Lrcop_uniq_%d", g.labelCounter)
		g.labelCounter++
		g.pop()
		g.emit("xor ecx, ecx")
		g.emit("test rax, rax")
		g.emit(fmt.Sprintf("jz %s", uniqDone))
		g.emit("cmp rax, 0x10000")
		g.emit(fmt.Sprintf("jb %s", uniqDone))
		g.emit("mov edx, dword ptr [rax - 8]")
		g.emit("test edx, edx")
		g.emit(fmt.Sprintf("js %s", uniqDone))
		g.emit("cmp edx, 1")
		g.emit("sete cl")
		g.label(uniqDone)
		g.emit("mov eax, ecx")
		g.push()

	case ir.OpCallDirect:
		target := op.Str
		// Cheap f64 math intrinsics lower inline — no libm. The f64
		// argument rides the operand stack as raw bits (same as
		// OpFNeg); the result goes back in rax before push.
		if g.emitF64UnaryIntrinsic(target) {
			g.push()
			return nil
		}
		if target == "__pow_f64" {
			g.emitF64Pow()
			g.push()
			return nil
		}
		switch target {
		case "__alloc":
			target = "__fern_alloc"
		case "__free":
			target = "__fern_free"
		case "__alloc_reuse":
			target = "__fern_alloc_reuse"
		case "__memcpy":
			target = "__fern_memcpy"
		case "__slice_make":
			target = "__fern_slice_make"
		case "__slice_range":
			target = "__fern_slice_range"
		case "print":
			target = "__fern_puts"
		case "write":
			target = "__fern_write"
		case "putchar":
			target = "__fern_putchar"
		case "eprint":
			target = "__fern_eprint"
		case "exit":
			target = "__fern_exit"
		case "__heap_mark":
			target = "__fern_heap_mark"
		case "__heap_release_to":
			target = "__fern_heap_release_to"
		case "strbuf_reset":
			target = "__fern_strbuf_reset"
		case "strbuf_append":
			target = "__fern_strbuf_append"
		case "strbuf_take":
			target = "__fern_strbuf_take"
		case "now_unix_ms":
			target = "__fern_now_unix_ms"
		case "monotonic_ns":
			target = "__fern_monotonic_ns"
		case "now_ns":
			target = "__fern_now_ns"
		case "sleep_ms":
			target = "__fern_sleep_ms"
		case "proc_exec":
			target = "__fern_proc_exec"
		case "proc_fork":
			target = "__fern_proc_fork"
		case "proc_waitpid":
			target = "__fern_proc_waitpid"
		case "tcp_listen":
			target = "__fern_tcp_listen"
		case "tcp_accept":
			target = "__fern_tcp_accept"
		case "tcp_recv":
			target = "__fern_tcp_recv"
		case "poll":
			target = "__fern_poll"
		case "timer_fd":
			target = "__fern_timer_fd"
		case "tcp_send":
			target = "__fern_tcp_send"
		case "tcp_connect":
			target = "__fern_tcp_connect"
		case "tcp_pollable":
			target = "__fern_tcp_pollable"
		case "wasm_pollable_drop":
			target = "__fern_wasm_pollable_drop"
		case "wasm_block":
			target = "__fern_wasm_block"
		case "wasm_timer_pollable":
			target = "__fern_wasm_timer_pollable"
		case "wasm_poll":
			target = "__fern_wasm_poll"
		case "tcp_close":
			target = "__fern_tcp_close"
		case "env":
			target = "__fern_env"
		case "args":
			target = "__fern_args"
		case "read_file":
			target = "__fern_read_file"
		case "write_file":
			target = "__fern_write_file"
		case "write_file_exec":
			target = "__fern_write_file_exec"
		case "remove_dir_all":
			target = "__fern_remove_dir_all"
		case "remove_file":
			target = "__fern_remove_file"
		case "temp_dir":
			target = "__fern_temp_dir"
		case "read_dir":
			target = "__fern_read_dir"
		case "stat":
			target = "__fern_stat"
		case "random_bytes":
			target = "__fern_random_bytes"
		case "random_i32":
			target = "__fern_random_i32"
		case "read_line":
			target = "__fern_read_line"
		case "__method_Reader_read_line":
			target = "__fern_reader_read_line"
		case "__method_Reader_read_chunk":
			target = "__fern_reader_read_chunk"
		case "__method_Reader_close",
			"__method_Writer_close":
			target = "__fern_close_fd_box"
		case "__method_Writer_write":
			target = "__fern_writer_write"
		case "open_reader":
			target = "__fern_open_reader"
		case "open_writer":
			target = "__fern_open_writer"
		case "open_appender":
			target = "__fern_open_appender"
		case "stdin":
			target = "__fern_stdin"
		case "stdout":
			target = "__fern_stdout"
		case "stderr":
			target = "__fern_stderr"
		case "__str_idx", "__arr_idx", "__arr_idx_1", "__arr_idx_8",
			"__arr_idx_nc", "__arr_idx_1_nc", "__arr_idx_8_nc",
			"__slice_idx", "__slice_idx_1", "__slice_idx_8":
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
		// in the stdlib under `_impl`-suffixed names;
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
		// Struct/enum (keyKind-3) keys: the `_keyed` variants take the
		// key type's derived hash/eq as trailing fn-value args (#2671).
		case "__method_Map_has_keyed":
			target = "__map_has_keyed_impl"
		case "__method_Map_get_keyed":
			target = "__map_get_keyed_impl"
		case "__method_Map_get_or_keyed":
			target = "__map_get_or_keyed_impl"
		case "__method_Map_set_keyed":
			target = "__map_set_keyed_impl"
		case "__method_Map_delete_keyed":
			target = "__map_delete_keyed_impl"
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
		g.emit(fmt.Sprintf("call %s", asmFnName(target)))
		g.emitCallArgsCleanup(argc)
		// Void-returning callees push NOTHING. Without this gate,
		// helpers like `__memcpy` / `__memset` leave a phantom
		// rax value on the operand stack that corrupts the
		// subsequent OpStore / OpStoreLocal pops. Hit by
		// `arr.push(v)` inside a struct literal field
		// initialiser. Mirrors arm64's returnIsVoid gate.
		if g.callReturnsVoid(op.Str) {
			break
		}
		// __c_call*_f32/_f64 return their result in xmm0 (C ABI); move it
		// into rax so it lands on the operand stack in Fern's FP convention.
		if w := ccallFloatRetWidth(op.Str); w == 64 {
			g.emit("movq rax, xmm0")
		} else if w == 32 {
			g.emit("movd eax, xmm0")
		}
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
		g.emit(fmt.Sprintf("call %s", asmFnName(op.Str)))
		g.emitCallArgsCleanup(argc)
		g.emit("mov r10, rdx") // stash payload (rdx is volatile)
		g.push()               // push rax (tag)
		g.emit("mov rax, r10")
		g.push() // push payload

	default:
		return fmt.Errorf("x86_64: unsupported IR op %s", op.Kind)
	}
	return nil
}

// emitF64UnaryIntrinsic lowers the cheap f64 math builtins inline,
// matching the arm64 backend's single-instruction lowerings (no
// libm). The f64 argument is already popped off the operand stack
// into rax as raw bits; the result is left in rax and the caller
// pushes it. Returns false (leaving rax untouched) for any name
// that isn't a recognised intrinsic.
//
// abs/sqrt/floor/ceil/trunc map straight onto SSE: abs clears the
// sign bit, sqrt is sqrtsd, and floor/ceil/trunc are roundsd with
// the matching rounding-mode immediate (1=−∞, 2=+∞, 3=toward-zero).
//
// round is the one that needs care: it's round-half-AWAY-from-zero
// (Go's math.Round, arm64's frinta), but x86's roundsd nearest mode
// (imm 0) rounds ties to EVEN. So we compute r = trunc(x), take the
// exact fractional part frac = x − r (exact by construction, so no
// x+0.5 representability hazard), and bump r by copysign(1, x) when
// |frac| ≥ 0.5. That reproduces ties-away for every input.
func (g *generator) emitF64UnaryIntrinsic(name string) bool {
	switch name {
	case "__abs_f64", "__sqrt_f64", "__floor_f64", "__ceil_f64", "__trunc_f64", "__round_f64",
		"__sin_f64", "__cos_f64", "__exp_f64", "__log_f64":
	default:
		return false
	}
	g.pop() // f64 bits → rax
	switch name {
	case "__abs_f64":
		g.emit("movabs rcx, 0x7fffffffffffffff")
		g.emit("and rax, rcx")
	case "__sqrt_f64":
		g.emit("movq xmm0, rax")
		g.emit("sqrtsd xmm0, xmm0")
		g.emit("movq rax, xmm0")
	case "__floor_f64":
		g.emit("movq xmm0, rax")
		g.emit("roundsd xmm0, xmm0, 1")
		g.emit("movq rax, xmm0")
	case "__ceil_f64":
		g.emit("movq xmm0, rax")
		g.emit("roundsd xmm0, xmm0, 2")
		g.emit("movq rax, xmm0")
	case "__trunc_f64":
		g.emit("movq xmm0, rax")
		g.emit("roundsd xmm0, xmm0, 3")
		g.emit("movq rax, xmm0")
	case "__round_f64":
		done := fmt.Sprintf(".Lround_done_%d", g.labelCounter)
		g.labelCounter++
		g.emit("movq xmm0, rax")        // x
		g.emit("roundsd xmm1, xmm0, 3") // r = trunc(x)
		g.emit("movapd xmm2, xmm0")     // frac = x - r (exact)
		g.emit("subsd xmm2, xmm1")      //   "
		g.emit("movq rcx, xmm2")        // |frac| bits
		g.emit("movabs rdx, 0x7fffffffffffffff")
		g.emit("and rcx, rdx")                   //   "
		g.emit("movq xmm3, rcx")                 // |frac|
		g.emit("movabs rcx, 0x3fe0000000000000") // 0.5
		g.emit("movq xmm4, rcx")
		g.emit("comisd xmm3, xmm4") // |frac| vs 0.5
		g.emit("jb " + done)        // < 0.5 → r unchanged
		g.emit("movq rcx, xmm0")    // copysign(1.0, x):
		g.emit("movabs rdx, 0x8000000000000000")
		g.emit("and rcx, rdx")                   //   sign(x)
		g.emit("movabs rdx, 0x3ff0000000000000") // | bits(1.0)
		g.emit("or rcx, rdx")
		g.emit("movq xmm5, rcx")
		g.emit("addsd xmm1, xmm5") // r += copysign(1, x)
		g.label(done)
		g.emit("movq rax, xmm1")
	case "__sin_f64", "__cos_f64", "__exp_f64", "__log_f64":
		// Transcendentals call the SSE2 polynomial helpers — no libm,
		// and no x87. Argument and result both ride xmm0, matching
		// arm64's d0 convention for the same five helpers.
		g.usesF64Trans = true
		g.emit("movq xmm0, rax")
		g.emit("call __fern_" + name[2:]) // "__sin_f64" → "__fern_sin_f64"
		g.emit("movq rax, xmm0")
	}
	return true
}

// emitF64Pow lowers __pow_f64(x, y) = exp(y·ln x) through the SSE2
// helper bundle. Both args ride the operand stack; binPop leaves x in
// rax and y in rcx. Result is left in rax (caller pushes).
func (g *generator) emitF64Pow() {
	g.binPop() // rax = x, rcx = y
	g.usesF64Trans = true
	g.emit("movq xmm0, rax") // x
	g.emit("movq xmm1, rcx") // y
	g.emit("call __fern_pow_f64")
	g.emit("movq rax, xmm0")
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
	_ = width              // payload width handled by the in-register move below
	g.pop()                // payload → rax
	g.emit("mov rcx, rax") // save payload
	g.emit(fmt.Sprintf("mov rax, %d", tag))
	g.push()               // push tag
	g.emit("mov rax, rcx") // restore payload
	g.push()               // push payload
}

// isFusableCompare reports whether kind is an integer comparison whose
// result is a 0/1 boolean the compare-and-branch fusion can turn into a
// direct conditional jump. Float compares are excluded: NaN breaks the
// negation identity (`!(a < b)` is not `a >= b` when either is NaN), so
// their setcc/movzx materialisation stays.
func isFusableCompare(k ir.OpKind) bool {
	switch k {
	case ir.OpEq, ir.OpNe, ir.OpLtS, ir.OpLeS, ir.OpGtS, ir.OpGeS:
		return true
	}
	return false
}

// jccMnemonic maps an integer comparison to the x86 conditional-jump
// mnemonic that fires when the comparison holds (whenTrue) or when it
// does not (its negation). `unsigned` selects the below/above family
// over less/greater. cmpForWidth has already emitted `cmp lhs, rhs`, so
// the flags reflect lhs vs rhs.
func jccMnemonic(k ir.OpKind, unsigned, whenTrue bool) string {
	// direct = jump when the comparison is TRUE; neg = its inverse.
	var direct, neg string
	switch k {
	case ir.OpEq:
		direct, neg = "je", "jne"
	case ir.OpNe:
		direct, neg = "jne", "je"
	case ir.OpLtS:
		if unsigned {
			direct, neg = "jb", "jae"
		} else {
			direct, neg = "jl", "jge"
		}
	case ir.OpLeS:
		if unsigned {
			direct, neg = "jbe", "ja"
		} else {
			direct, neg = "jle", "jg"
		}
	case ir.OpGtS:
		if unsigned {
			direct, neg = "ja", "jbe"
		} else {
			direct, neg = "jg", "jle"
		}
	case ir.OpGeS:
		if unsigned {
			direct, neg = "jae", "jb"
		} else {
			direct, neg = "jge", "jl"
		}
	}
	if whenTrue {
		return direct
	}
	return neg
}

// tryFuseCmpBranch fuses `cmp (Not)* {If|BrIf}` into `cmp; jcc`.
// Returns the number of EXTRA ops consumed past index i (the caller
// advances i by that amount) and whether the fusion fired. When it does
// not fire the op stream is untouched and the normal per-op emission
// runs. See the loop comment at the call site for the safety argument.
func (g *generator) tryFuseCmpBranch(ops []ir.Op, i int, scope *[]irScope) (int, bool) {
	cmp := ops[i]
	if !isFusableCompare(cmp.Kind) {
		return 0, false
	}
	j := i + 1
	nots := 0
	for j < len(ops) && ops[j].Kind == ir.OpNot {
		nots++
		j++
	}
	if j >= len(ops) {
		return 0, false
	}
	br := ops[j]
	// OpBrIf jumps when the effective condition is TRUE; OpIf jumps to
	// its else-label when the effective condition is FALSE. Each OpNot
	// flips which comparison outcome that is.
	whenTrue := br.Kind == ir.OpBrIf
	if nots%2 == 1 {
		whenTrue = !whenTrue
	}
	switch br.Kind {
	case ir.OpIf:
		g.binPop()
		g.cmpForWidth(int(cmp.Width))
		elseL := g.freshLabel("ifElse")
		endL := g.freshLabel("ifEnd")
		g.emit(fmt.Sprintf("%s %s", jccMnemonic(cmp.Kind, cmp.Unsigned, whenTrue), elseL))
		*scope = append(*scope, irScope{kind: ir.OpIf, brTarget: endL, endLabel: endL, elseLabel: elseL})
		return j - i, true
	case ir.OpBrIf:
		g.binPop()
		g.cmpForWidth(int(cmp.Width))
		target := (*scope)[len(*scope)-1-int(br.I32)].brTarget
		g.emit(fmt.Sprintf("%s %s", jccMnemonic(cmp.Kind, cmp.Unsigned, whenTrue), target))
		return j - i, true
	}
	return 0, false
}

func (g *generator) binPop() {
	g.emit("mov rcx, [rsp]") // rhs (top of stack)
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
	g.emit("mov rax, [rsp]") // lhs (next)
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
}

// cmpForWidth emits a `cmp` whose operand size matches the
// integer width — `cmp rax, rcx` for i64 / u64 / usize (width
// 64 or pointer-width), `cmp eax, ecx` for i32 and narrower.
// The 32-bit form silently truncated i64 operands to their
// lower 32 bits and mis-compared values whose upper bits
// matter — see the matching arm64 helper for the diagnosis.
func (g *generator) cmpForWidth(width int) {
	if width == 64 || width == ir.WidthPtr {
		g.emit("cmp rax, rcx")
		return
	}
	g.emit("cmp eax, ecx")
}

// aRegForWidth names the accumulator register (rax) sized to the
// integer width: `rax` for i64 / u64 / usize (64 or pointer-width),
// `eax` for i32 and narrower. Operations whose result depends on the
// register width — notably `sar` (the sign bit position) and the
// shift-count mask (0..31 vs 0..63) — must select the matching form.
func (g *generator) aRegForWidth(width int) string {
	if width == 64 || width == ir.WidthPtr {
		return "rax"
	}
	return "eax"
}

// emitIntDivRem lowers OpDivS / OpRemS (signed + unsigned, i32 /
// i64) with the never-trap integer-division contract: x / 0 = 0,
// x % 0 = x, and (signed) INT_MIN / -1 = INT_MIN, INT_MIN % -1 = 0.
// x86's `idiv` / `div` raise #DE on a zero divisor AND on the
// INT_MIN / -1 overflow, so both are branch-guarded — the hardware
// divide only runs on operands it can't fault on. After binPop the
// dividend is in rax and the divisor in rcx; the result lands in
// rax. Width-aware: i32 / u32 use eax/ecx/edx, i64 / u64 / usize
// use rax/rcx/rdx (cdq / cqo sign-extend into edx / rdx; the
// unsigned forms clear it).
func (g *generator) emitIntDivRem(op ir.Op, isRem bool) {
	g.binPop()
	// WidthPtr (usize) is pointer-width: 64 bits on x86-64. Use the
	// 64-bit register form so a usize dividend isn't truncated to its low
	// 32 bits. See docs/ADVERSARIAL-REVIEW-2026-06.md (B1).
	w64 := op.Width == 64 || op.Width == ir.WidthPtr
	a, c, d := "eax", "ecx", "edx"
	if w64 {
		a, c, d = "rax", "rcx", "rdx"
	}
	n := g.labelCounter
	g.labelCounter++
	lZero := fmt.Sprintf(".Ldiv_zero_%d", n)
	lNorm := fmt.Sprintf(".Ldiv_norm_%d", n)
	lOvf := fmt.Sprintf(".Ldiv_ovf_%d", n)
	lDone := fmt.Sprintf(".Ldiv_done_%d", n)

	g.emit(fmt.Sprintf("test %s, %s", c, c))
	g.emit(fmt.Sprintf("jz %s", lZero))
	if !op.Unsigned {
		// INT_MIN / -1 overflow: only when divisor == -1 and
		// dividend == INT_MIN. Both faults are routed away from idiv.
		g.emit(fmt.Sprintf("cmp %s, -1", c))
		g.emit(fmt.Sprintf("jne %s", lNorm))
		if w64 {
			g.emit("mov r8, 0x8000000000000000")
			g.emit("cmp rax, r8")
		} else {
			g.emit("cmp eax, -2147483648")
		}
		g.emit(fmt.Sprintf("je %s", lOvf))
	}
	g.label(lNorm)
	if op.Unsigned {
		g.emit(fmt.Sprintf("xor %s, %s", d, d))
		g.emit(fmt.Sprintf("div %s", c))
	} else if w64 {
		g.emit("cqo")
		g.emit("idiv rcx")
	} else {
		g.emit("cdq")
		g.emit("idiv ecx")
	}
	if isRem {
		g.emit(fmt.Sprintf("mov %s, %s", a, d))
	}
	g.emit(fmt.Sprintf("jmp %s", lDone))

	g.label(lZero)
	if isRem {
		// x % 0 = x: the dividend is already in rax — nothing to do.
	} else {
		g.emit("xor eax, eax") // x / 0 = 0
	}
	g.emit(fmt.Sprintf("jmp %s", lDone))

	if !op.Unsigned {
		g.label(lOvf)
		if isRem {
			g.emit("xor eax, eax") // INT_MIN % -1 = 0
		}
		// INT_MIN / -1 = INT_MIN: the dividend is already INT_MIN in
		// rax — nothing to do.
		g.emit(fmt.Sprintf("jmp %s", lDone))
	}
	g.label(lDone)
	g.push()
}

// emitFloatToIntSat lowers a saturating float→int truncation (the
// IR's OpITruncF32 / OpITruncF64) with the source in xmm0 and the
// result left in rax. x86's `cvtt*2si` returns the "integer
// indefinite" (INT_MIN) for *every* invalid input — NaN, ±Inf, and
// out-of-range — so on its own it neither saturates nor zeroes NaN.
// Float-domain compares + cmov fix that up to the wasm `trunc_sat`
// / arm64 fcvtz contract: NaN → 0, +overflow → MAX, −overflow → MIN
// (unsigned: < 0 / NaN → 0, overflow → the all-ones max). The
// `cvtt` sentinel is already the wanted INT_MIN for the signed
// −overflow / −Inf cases, so only +overflow and NaN need a fixup.
func (g *generator) emitFloatToIntSat(isF64 bool, width int, unsigned bool) {
	suf := "sd"
	if !isF64 {
		suf = "ss"
	}
	// loadXmm1 materialises a float constant (given its f64 / f32
	// bit-pattern) into xmm1 via a GPR — x86 has no float-immediate
	// move.
	loadXmm1 := func(bitsF64 uint64, bitsF32 uint32) {
		if isF64 {
			g.emit(fmt.Sprintf("mov rcx, 0x%X", bitsF64))
			g.emit("movq xmm1, rcx")
		} else {
			g.emit(fmt.Sprintf("mov ecx, 0x%X", bitsF32))
			g.emit("movd xmm1, ecx")
		}
	}

	// loadZeroXmm1 puts +0.0 into xmm1 (no float-immediate move;
	// the all-zero bit pattern is +0.0 for both widths).
	loadZeroXmm1 := func() {
		if isF64 {
			g.emit("mov rcx, 0")
			g.emit("movq xmm1, rcx")
		} else {
			g.emit("mov ecx, 0")
			g.emit("movd xmm1, ecx")
		}
	}
	lbl := func(s string) string { return fmt.Sprintf(".Lf2i_%d_%s", g.labelCounter, s) }
	g.labelCounter++

	if !unsigned {
		reg, maxConst := "eax", "0x7FFFFFFF"
		if width == 64 {
			reg, maxConst = "rax", "0x7FFFFFFFFFFFFFFF"
		}
		// cvtt yields the desired INT_MIN sentinel for −overflow /
		// −Inf, and a correct value for in-range x; only NaN and
		// +overflow need a fixup.
		g.emit(fmt.Sprintf("cvtt%s2si %s, xmm0", suf, reg))
		g.emit(fmt.Sprintf("ucomi%s xmm0, xmm0", suf)) // NaN → PF
		g.emit(fmt.Sprintf("jp %s", lbl("nan")))
		if width == 64 {
			loadXmm1(0x43E0000000000000, 0x5F000000) // 2^63
		} else {
			loadXmm1(0x41E0000000000000, 0x4F000000) // 2^31
		}
		g.emit(fmt.Sprintf("ucomi%s xmm0, xmm1", suf))
		g.emit(fmt.Sprintf("jae %s", lbl("max"))) // x >= 2^(w-1)
		g.emit(fmt.Sprintf("jmp %s", lbl("done")))
		g.label(lbl("nan"))
		g.emit(fmt.Sprintf("mov %s, 0", reg))
		g.emit(fmt.Sprintf("jmp %s", lbl("done")))
		g.label(lbl("max"))
		g.emit(fmt.Sprintf("mov %s, %s", reg, maxConst))
		g.label(lbl("done"))
		return
	}

	if width == 32 {
		// Convert with 64-bit headroom (room for the whole u32
		// range), then clamp to [0, 2^32-1]. x < 0 / NaN → 0.
		g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
		loadZeroXmm1()
		g.emit(fmt.Sprintf("ucomi%s xmm0, xmm1", suf))
		g.emit(fmt.Sprintf("jb %s", lbl("zero"))) // x < 0 or NaN (CF set)
		loadXmm1(0x41F0000000000000, 0x4F800000)  // 2^32
		g.emit(fmt.Sprintf("ucomi%s xmm0, xmm1", suf))
		g.emit(fmt.Sprintf("jae %s", lbl("max"))) // x >= 2^32
		g.emit(fmt.Sprintf("jmp %s", lbl("done")))
		g.label(lbl("zero"))
		g.emit("mov eax, 0")
		g.emit(fmt.Sprintf("jmp %s", lbl("done")))
		g.label(lbl("max"))
		g.emit("mov eax, 0xFFFFFFFF")
		g.label(lbl("done"))
		return
	}

	// Unsigned 64. Preserve the original x in xmm2 (the 2^63 trick
	// mutates xmm0), convert [0, 2^64) via the trick, then clamp
	// the edges against the saved value: x < 0 / NaN → 0, x >= 2^64
	// → all-ones.
	g.emit("movaps xmm2, xmm0")
	if isF64 {
		g.emit("mov rcx, 0x43E0000000000000") // 2^63
		g.emit("movq xmm1, rcx")
	} else {
		g.emit("mov ecx, 0x5F000000")
		g.emit("movd xmm1, ecx")
	}
	g.emit(fmt.Sprintf("ucomi%s xmm0, xmm1", suf))
	g.emit(fmt.Sprintf("jae %s", lbl("big")))
	g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
	g.emit(fmt.Sprintf("jmp %s", lbl("sat")))
	g.label(lbl("big"))
	if isF64 {
		g.emit("subsd xmm0, xmm1")
	} else {
		g.emit("subss xmm0, xmm1")
	}
	g.emit(fmt.Sprintf("cvtt%s2si rax, xmm0", suf))
	// Add 2^63 back. The converted (x − 2^63) is in [0, 2^63) so
	// bit 63 is clear; xor-ing it sets the bit (= +2^63) without
	// needing `btc`, which the in-process assembler doesn't carry.
	g.emit("mov rcx, 0x8000000000000000")
	g.emit("xor rax, rcx")
	g.label(lbl("sat"))
	loadZeroXmm1()
	g.emit(fmt.Sprintf("ucomi%s xmm2, xmm1", suf))
	g.emit(fmt.Sprintf("jb %s", lbl("zero"))) // x < 0 or NaN
	loadXmm1(0x43F0000000000000, 0x5F800000)  // 2^64
	g.emit(fmt.Sprintf("ucomi%s xmm2, xmm1", suf))
	g.emit(fmt.Sprintf("jae %s", lbl("umax"))) // x >= 2^64
	g.emit(fmt.Sprintf("jmp %s", lbl("done")))
	g.label(lbl("zero"))
	g.emit("mov rax, 0")
	g.emit(fmt.Sprintf("jmp %s", lbl("done")))
	g.label(lbl("umax"))
	g.emit("mov rax, -1")
	g.label(lbl("done"))
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
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
	g.emit("mov rax, [rsp]") // lhs
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
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
// __fern_write / __fern_puts / __fern_eprint / __fern_env /
// __fern_tcp_send / __str_slice / __fern_write_file / WASI
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
// value: 1 means inline, 0 means heap pointer. __fern_alloc returns
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

// emitStrLenStore writes the i32 length in srcReg to the 4-byte
// little-endian length prefix at `[dstReg - 4]`, where dstReg is
// the new string's *data pointer* (one past the prefix). Inverse
// of emitStrLen and the second half of the SSO encoding seam:
// string-producing runtime helpers (strcat / str_slice /
// string_from_bytes_unchecked / random_bytes / env / tcp_recv / read_line)
// all flow through this one site so future encoding changes that
// affect string construction (e.g. tagged-pointer inline-when-
// short) have a single function to update per backend. Array-
// length stores (`__alloc_u8`, `__fern_args` outer array) stay
// open-coded since arrays may diverge.
func (g *generator) emitStrLenStore(srcReg, dstReg string) {
	g.emit(fmt.Sprintf("mov [%s - 4], %s", dstReg, srcReg))
}

// emitStrEmpty materialises the data pointer of the canonical
// empty-string sentinel into dstReg. The sentinel lives in
// .rodata as a length-prefixed string with length=0, shared
// across all callers and the program lifetime. Used by the
// string-constructing runtime helpers (strcat / str_slice /
// string_from_bytes_unchecked) to short-circuit the alloc + memcpy +
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
// Used by __alloc_u8's siblings and string_from_bytes_unchecked's input
// length read.
func (g *generator) emitArrayLen(dstReg, srcReg string) {
	g.emit(fmt.Sprintf("mov %s, [%s - 4]", dstReg, srcReg))
}

// emitArrayLenStore writes the i32 length in srcReg to the 4-byte
// little-endian length prefix at `[dstReg - 4]`, where dstReg is
// the new array's *data pointer* (one past the prefix). Inverse
// of emitArrayLen. Used by __alloc_u8 and __fern_args (outer
// string[] container). String length stores stay on
// emitStrLenStore.
func (g *generator) emitArrayLenStore(srcReg, dstReg string) {
	g.emit(fmt.Sprintf("mov [%s - 4], %s", dstReg, srcReg))
}

// emitCallArgsLoad places `argc` operand-stack values into
// System V argument slots. First 6 args go to rdi/rsi/rdx/rcx/
// r8/r9; the rest land on the call stack at [rsp+0], [rsp+8],
// ... in source order. The operand stack uses `slotBytes`-byte
// slots; the call stack always uses 8-byte slots, so overflow
// args get compressed via a call-stack overflow area allocated
// below the operand-stack args. (Today `slotBytes == 16` so
// the compress is real; flipping it to 8 makes it a no-op.)
//
// After this call returns, the caller is responsible for the
// `call` / `call r11` and then `emitCallArgsCleanup` to drop
// both the call-stack overflow AND the operand-stack args.
func (g *generator) emitCallArgsLoad(argc int) {
	regs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
	if argc <= len(regs) {
		for i := argc - 1; i >= 0; i-- {
			g.emit(fmt.Sprintf("mov %s, [rsp]", regs[i]))
			g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
		}
		return
	}
	overflow := argc - len(regs)
	// Round overflow*8 to a multiple of 16 to keep rsp
	// 16-aligned at the call site (System V requirement).
	stackSize := ((overflow*8 + 15) / 16) * 16
	g.emit(fmt.Sprintf("sub rsp, %d", stackSize))
	// Register args: arg i at [rsp + stackSize + slotBytes*(argc-1-i)].
	for i := 0; i < len(regs); i++ {
		g.emit(fmt.Sprintf("mov %s, [rsp + %d]", regs[i], stackSize+slotBytes*(argc-1-i)))
	}
	// Overflow args: copy operand-slot value to call-stack 8-byte
	// slot. arg i (i >= 6) at operand offset
	// stackSize + slotBytes*(argc-1-i), goes to call-stack
	// [rsp + 8*(i-6)].
	for i := len(regs); i < argc; i++ {
		g.emit(fmt.Sprintf("mov rax, [rsp + %d]", stackSize+slotBytes*(argc-1-i)))
		g.emit(fmt.Sprintf("mov [rsp + %d], rax", 8*(i-len(regs))))
	}
}

// emitCallArgsCleanup undoes emitCallArgsLoad's stack
// allocation. Caller passes the same argc.
func (g *generator) emitCallArgsCleanup(argc int) {
	regs := []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}
	if argc <= len(regs) {
		// Args were already popped via per-arg `add rsp, slotBytes`.
		return
	}
	overflow := argc - len(regs)
	stackSize := ((overflow*8 + 15) / 16) * 16
	// Drop call-stack overflow + operand-stack args.
	g.emit(fmt.Sprintf("add rsp, %d", stackSize+slotBytes*argc))
}

func (g *generator) line(s string) {
	g.put(s)
}

func (g *generator) label(name string) {
	g.put(name + ":")
}

func (g *generator) emit(s string) {
	g.put("\t" + s)
}

// peepWindow is how many recently emitted logical lines are held back from
// `out` so the streaming peephole can rewrite the tail in place. The longest
// pattern is 4 lines; 6 leaves margin while bounding held memory to O(1) —
// crucial because a self-host `.s` is hundreds of MB and a whole-text
// post-pass would spike RAM.
const peepWindow = 6

// put appends one logical output line (without its trailing newline) to the
// peephole window, applies the safe local rewrites at the tail, then flushes
// any line that has aged out of the window to `out`. All emission funnels
// through here (line / label / emit), so the peephole sees every line in
// emission order regardless of which helper produced it.
func (g *generator) put(s string) {
	if g.noPeephole {
		g.out.WriteString(s)
		g.out.WriteByte('\n')
		return
	}
	g.peepWin = append(g.peepWin, s)
	g.peepholeTail()
	for len(g.peepWin) > peepWindow {
		g.out.WriteString(g.peepWin[0])
		g.out.WriteByte('\n')
		g.peepWin = g.peepWin[1:]
	}
}

// flushPeep drains the remaining window to `out`. Call once, right before
// returning the assembled text.
func (g *generator) flushPeep() {
	for _, l := range g.peepWin {
		g.out.WriteString(l)
		g.out.WriteByte('\n')
	}
	g.peepWin = g.peepWin[:0]
}

// peepholeTail applies the two safe stack-machine rewrites at the tail of the
// window. Both are purely local (≤4 contiguous lines) so they never touch a
// genuinely-live stack slot — those are left for the register allocator.
func (g *generator) peepholeTail() {
	w := g.peepWin
	n := len(w)

	// P1 — redundant store/reload: a push immediately followed by the
	// matching pop. push() emits `sub rsp, N` / `mov [rsp], rax`; pop()
	// emits `mov DST, [rsp]` / `add rsp, N`. When adjacent, the slot is
	// allocated and freed within the four lines and nothing else reads it,
	// so the net effect is just `DST := rax`:
	//   sub rsp, N / mov [rsp], rax / mov DST, [rsp] / add rsp, N
	//     => mov DST, rax     (or nothing when DST == rax)
	if n >= 4 {
		if k, ok := matchRspDelta(w[n-4], "sub"); ok && w[n-3] == "\tmov [rsp], rax" {
			if dst, ok2 := matchPopDst(w[n-2]); ok2 {
				if k2, ok3 := matchRspDelta(w[n-1], "add"); ok3 && k2 == k {
					if dst == "rax" {
						g.peepWin = w[:n-4]
					} else {
						g.peepWin = append(w[:n-4], "\tmov "+dst+", rax")
					}
					return
				}
			}
		}
	}

	// P2 — dead jump: `jmp L` immediately followed by the label `L:` is a
	// no-op fall-through. Drop the jmp; the label stays for other jumps.
	if n >= 2 {
		last := w[n-1]
		if len(last) > 1 && last[len(last)-1] == ':' && last[0] != '\t' && !strings.ContainsRune(last, ' ') {
			if w[n-2] == "\tjmp "+last[:len(last)-1] {
				w[n-2] = last
				g.peepWin = w[:n-1]
			}
		}
	}
}

// matchRspDelta matches a `\t<op> rsp, <n>` line and returns the <n> token.
func matchRspDelta(line, op string) (string, bool) {
	pfx := "\t" + op + " rsp, "
	if strings.HasPrefix(line, pfx) {
		return line[len(pfx):], true
	}
	return "", false
}

// matchPopDst matches a `\tmov <reg>, [rsp]` pop into a single register and
// returns <reg>. rsp is excluded: `mov rsp, [rsp]` would not be equivalent to
// `mov rsp, rax` once the trailing `add rsp, N` is removed.
func matchPopDst(line string) (string, bool) {
	const pfx = "\tmov "
	const sfx = ", [rsp]"
	if strings.HasPrefix(line, pfx) && strings.HasSuffix(line, sfx) {
		reg := line[len(pfx) : len(line)-len(sfx)]
		if reg != "" && reg != "rsp" && !strings.ContainsAny(reg, " []") {
			return reg, true
		}
	}
	return "", false
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
		// {fn_ptr, env=0, drop_fn=0, env=0}. The 32-byte 4-slot
		// shape matches the captured case so a generic holder
		// (__drop_arr_closure) reads the drop-fn slot uniformly;
		// a zero-capture closure has no env to free, so drop_fn
		// is 0 (the generic drop guards drop_fn!=0).
		g.emit("mov edi, 32")
		g.emit("call __fern_alloc_rc1")
		g.emit(fmt.Sprintf("lea rcx, [rip + %s]", op.Str))
		g.emit("mov [rax], rcx")
		g.emit("mov qword ptr [rax + 8], 0")
		g.emit("mov qword ptr [rax + 16], 0")
		g.emit("mov qword ptr [rax + 24], 0")
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
		envSize = ast.CaptureAlign(envSize, cap.Type, 8)
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
	// But __fern_alloc clobbers caller-save regs and we need
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
	// need them across __fern_alloc.
	g.emit("push r12")
	g.emit("push r13")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r12, rax") // r12 = env_ptr (= base + 8 header)
	// Captures sit on the operand stack just above the
	// pushed callee-saves: we pushed `r12` and `r13` above
	// (8 bytes each = 16 bytes total), so the operand-stack
	// values shifted down by `calleeSaveOff = 16`. The Nth
	// (last) capture is at [rsp + calleeSaveOff], the
	// (N-1)th at [rsp + calleeSaveOff + slotBytes], and so
	// on; the first capture is at [rsp + calleeSaveOff +
	// slotBytes*(n-1)].
	const calleeSaveOff = 16 // 2 × push (r12, r13) above the operand stack
	for i, s := range slots {
		stkOff := int32(calleeSaveOff + int32(n-1-i)*int32(slotBytes))
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
	g.emit(fmt.Sprintf("add rsp, %d", n*slotBytes))
	g.emit("mov rax, r12") // env_ptr in rax
	g.emit("pop r13")
	g.emit("pop r12")

	if envOnly {
		g.push()
		return nil
	}
	// OpMakeClosure: also allocate the closure pair. Stash
	// env_ptr on the operand stack via g.push() so the slot
	// layout is uniform with everything else (slotBytes-aware).
	// __fern_alloc preserves callee-saves + rsp per SysV, so
	// the saved value still sits at [rsp] when the call
	// returns.
	g.push() // env_ptr → operand stack
	g.emit("mov edi, 32")
	g.emit("call __fern_alloc_rc1")
	// rax = pair ptr (= base + 8 header). Pair is 32 bytes:
	// {fn_ptr, env_ptr, drop_fn, env_ptr}. The duplicated env_ptr
	// at +24 makes {drop_fn@16, env@24} a callable sub-pair so a
	// generic holder can free the env via the embedded drop-fn
	// pointer without static closure identity.
	// drop_fn = &__closure_drop_<name> when the IR generated the thunk
	// (only under RcFreeEnabled — the thunk references free-gated drop
	// helpers). Decide structurally on its presence in prog.Funcs, never
	// by re-reading the flag in codegen, so a free-OFF build (or a flag
	// toggled by a concurrent test) stores 0 instead of a dangling label.
	g.emit(fmt.Sprintf("lea rcx, [rip + %s]", op.Str))
	g.emit("mov [rax], rcx")
	g.emit("mov rcx, [rsp]") // env_ptr from the operand-stack save
	g.emit("mov [rax + 8], rcx")
	if _, ok := g.funcs["__closure_drop_"+op.Str]; ok {
		g.emit(fmt.Sprintf("lea rdx, [rip + __closure_drop_%s]", op.Str))
		g.emit("mov [rax + 16], rdx")
	} else {
		g.emit("mov qword ptr [rax + 16], 0")
	}
	g.emit("mov [rax + 24], rcx")                 // duplicate env_ptr
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes)) // drop env_ptr save
	g.push()                                      // pair ptr
	return nil
}

// emitArrBoundsCheck emits the array index bounds check shared by
// every `__arr_idx*` variant: with the array base in rax and the
// element index in rcx, the length prefix at [rax-4] is compared
// and an out-of-range index aborts with exit code 134 (matching the
// string-slice trap and wasm's `unreachable`). A single unsigned
// compare catches a negative index (huge as unsigned) and index >=
// len; rdx is scratch.
func (g *generator) emitArrBoundsCheck() {
	ok := g.freshLabel(".Larr_ok")
	g.emit("mov edx, [rax - 4]") // len prefix
	g.emit("cmp ecx, edx")
	g.emit(fmt.Sprintf("jb %s", ok)) // unsigned idx < len → in bounds
	g.emitAbort("__fern_msg_arr_oob")
	g.label(ok)
}

// emitSliceBoundsCheck is emitArrBoundsCheck for a slice: the len
// is in the slice header at [rax+8] (8-byte data_ptr at [rax+0]),
// read before the helper overwrites rax with the data pointer. rdx
// is scratch.
func (g *generator) emitSliceBoundsCheck() {
	ok := g.freshLabel(".Lslice_ok")
	g.emit("mov edx, [rax + 8]") // len at [slice+8] (after 8-byte data_ptr)
	g.emit("cmp ecx, edx")
	g.emit(fmt.Sprintf("jb %s", ok))
	g.emitAbort("__fern_msg_slice_oob")
	g.label(ok)
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
	// Bounds-check elision (#4380 lever 3): a `_nc` suffix names the same
	// address compute minus the len-load + compare + trap. Strip it and
	// remember to skip emitArrBoundsCheck for the array cases below.
	checked := true
	if strings.HasSuffix(name, "_nc") {
		checked = false
		name = strings.TrimSuffix(name, "_nc")
	}
	arrBounds := func() {
		if checked {
			g.emitArrBoundsCheck()
		}
	}
	// Pop in the order the OpCallDirect dispatch would
	// use: rhs (idx, top of stack) first, lhs (base, next)
	// second.
	g.emit("mov rcx, [rsp]") // idx
	// Zero-extend the index to 32 bits. Fern indices are i32 and the bounds
	// checks below compare the low 32 bits (`ecx`), but the address `lea`s
	// use the full 64-bit `rcx`, so any stale garbage in bits 32..63 of the
	// slot would pass the check yet produce a wild scaled address. An i32
	// value can carry dirty upper bits (e.g. a materialised constant that
	// didn't come through a 32-bit ALU op that would have zeroed them — the
	// #4377 `ir.Fold`-exposed miscompile), so mask it here. `mov ecx,ecx`
	// zeroes rcx's upper 32 bits; a no-op for already-clean indices.
	g.emit("mov ecx, ecx")
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
	g.emit("mov rax, [rsp]") // base
	g.emit(fmt.Sprintf("add rsp, %d", slotBytes))
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
		g.emit("mov [rip + __fern_str_idx_scratch], rax")
		g.emit("lea rax, [rip + __fern_str_idx_scratch]")
		g.emit("add rax, rcx")
		g.emit("add rax, 1")
		g.label(fmt.Sprintf(".Lstridx_done_%d", id))
	case "__arr_idx_1":
		// Stride-1 byte-array indexing: byte address = base +
		// idx. Split from __str_idx so the string helper can
		// own the SSO inline-spill dispatch without forcing
		// byte arrays through the same `test rax, 1` check.
		arrBounds()
		g.emit("lea rax, [rax + rcx]")
	case "__arr_idx":
		arrBounds()
		g.emit("lea rax, [rax + rcx*4]")
	case "__arr_idx_8":
		arrBounds()
		g.emit("lea rax, [rax + rcx*8]")
	// Slice indexing first bounds-checks `i` against the slice
	// header's len (at [slice+8]), then dereferences its data_ptr
	// field (8-byte pointer at [slice+0]). After the deref it's the
	// same stride-add shape as the array helpers.
	case "__slice_idx_1":
		g.emitSliceBoundsCheck()
		g.emit("mov rax, [rax]") // data_ptr (8-byte pointer)
		g.emit("add rax, rcx")
	case "__slice_idx":
		g.emitSliceBoundsCheck()
		g.emit("mov rax, [rax]")
		g.emit("lea rax, [rax + rcx*4]")
	case "__slice_idx_8":
		g.emitSliceBoundsCheck()
		g.emit("mov rax, [rax]")
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

// dynVtableLabel returns the GAS symbol for the (trait-set, concrete)
// `dyn Trait` vtable cell. Single-trait keys are Fern identifiers, so the
// joined symbol is a valid assembler label as-is. A merged multi-trait
// key (ir.dynVtableSetKey joins with '+', e.g. "A+B") is sanitized: '+' →
// "_x_" so the label stays a valid GAS identifier. The IR's
// OpConstVtable.Str carries the same key, so coercion (which stores the
// vtable address) and dispatch / downcast (which reference it) agree.
func dynVtableLabel(trait, concrete string) string {
	return "__vtable_" + strings.ReplaceAll(trait, "+", "_x_") + "_" + concrete
}

// splitPair undoes the "<trait>/<concrete>" key used by dynVtableCells.
func splitPair(key string) (string, string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
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
	// Static `dyn Trait` vtables: one `.rodata` cell per (trait,
	// concrete) pair referenced via OpConstVtable, holding
	// `len(methods)` absolute `__method_*` function pointers in trait
	// declaration order (docs/DYN-TRAITS.md §4.2.2). OpCallDyn loads
	// slot k (`vtable + k*8`) and calls through it.
	if len(g.dynVtableCells) > 0 {
		g.line("")
		g.line(".section .rodata")
		keys := make([]string, 0, len(g.dynVtableCells))
		for k := range g.dynVtableCells {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		byPair := map[string]ir.VtableDecl{}
		for _, vt := range g.vtables {
			byPair[vt.Trait+"/"+vt.Concrete] = vt
		}
		for _, key := range keys {
			vt, ok := byPair[key]
			if !ok {
				// Should not happen: OpConstVtable only names pairs that
				// collectVtables produced. Emit an empty (but labelled)
				// cell so the link doesn't fail with an undefined symbol.
				g.line(".align 8")
				g.label(dynVtableLabel(splitPair(key)))
				continue
			}
			g.line(".align 8")
			g.label(dynVtableLabel(vt.Trait, vt.Concrete))
			for _, m := range vt.Methods {
				g.line(fmt.Sprintf("\t.quad %s", asmFnName(m.Func)))
			}
			// Trailing drop slot at index len(Methods) (docs/DYN-TRAITS.md
			// §4.4, slice 4b): the concrete type's drop fn as an absolute
			// pointer, or a null sentinel (0) when it needs none. The boxed
			// __drop_dyn_<set> helper reads this slot and calls it to run the
			// erased concrete destructor before freeing the cell. Appended
			// trailing so the method slot indices (0..n-1) are unchanged —
			// OpCallDyn's slot math is untouched. Mirrors wasm internVtable.
			if vt.Drop != "" {
				g.line(fmt.Sprintf("\t.quad %s", asmFnName(vt.Drop)))
			} else {
				g.line("\t.quad 0")
			}
		}
	}
	if len(g.constFuncCells) > 0 {
		g.line("")
		g.line(".section .rodata")
		names := make([]string, 0, len(g.constFuncCells))
		for n := range g.constFuncCells {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			// 8-byte immortal rc header (0x80000000 at cell-8) so
			// __fern_rc_inc/dec short-circuit on these static
			// function-value cells once FuncType locals are
			// rc-tracked. The label still points at the fn_ptr
			// (data view), so OpCallIndirect's [+0]/[+8] reads are
			// unchanged.
			g.line(".align 8")
			g.line("\t.4byte 0x80000000") // rc header (static sentinel)
			g.line("\t.4byte 0")          // pad
			g.label(fmt.Sprintf("__closure_cell_%s", name))
			g.line(fmt.Sprintf("\t.quad %s", asmFnName(name)))
			g.line("\t.quad 0")
		}
	}
	needsEmpty := g.usesStrcat || g.usesStrSlice || g.usesStringFromBytes || g.usesRemoveDirAll
	needsEnumSentinels := len(g.enumSentinelTags) > 0
	if len(g.stringOrder) > 0 || g.usesPuts || g.usesEprint || needsEmpty || needsEnumSentinels || g.usesArrEmpty || ast.LeakCheckEnabled || ast.RcTrace {
		g.line("")
		g.line(".section .rodata")
		for _, s := range g.stringOrder {
			// L2 layout w/ rc-sentinel header (prereq 2): 8-byte header
			// (rc sentinel + length) followed by `.asciz` data. Pointers
			// handed to user code address the data byte (.LStr_N); `len()`
			// reads `[ptr - 4]` (unchanged from the pre-header layout —
			// length stays at data-4, just rebased onto the rc header).
			// The 0x80000000 sentinel at data-8 makes __fern_rc_inc/dec
			// short-circuit on literals so the future string-dec can safely
			// run over container-stored / aliased literals without a
			// fragile address-range guard. Mirrors the wasm internString
			// sentinel header.
			g.line(".align 8")
			g.line("\t.4byte 0x80000000") // rc sentinel at data-8
			g.line(fmt.Sprintf("\t.4byte %d", len(s)))
			g.label(g.stringLabel[s])
			g.line("\t.asciz " + escapeForGAS(s))
		}
		if g.usesPuts || g.usesEprint {
			// Trailing newline byte shared by __fern_puts and
			// __fern_eprint. Stored in the same section as the
			// string literals so the loader maps it read-only.
			g.label(".LLangNewline")
			g.line(`	.asciz "\n"`)
		}
		if needsEmpty {
			// Empty-string sentinel. String-constructing runtime
			// helpers (__fern_strcat, __str_slice,
			// string_from_bytes_unchecked) skip the alloc + memcpy when the
			// result is zero bytes and return this static data
			// pointer instead. L2 layout w/ rc-sentinel header: 8-byte
			// rc-sentinel header + length=0 + trailing NUL.
			g.line(".align 8")
			g.line("\t.4byte 0x80000000") // rc sentinel at data-8
			g.line("\t.4byte 0")          // length = 0
			g.label(".LStr_Empty")
			g.line(`	.asciz ""`)
		}
		if g.usesArrEmpty {
			// Empty u8[] sentinel — __alloc_u8(0) returns this
			// address instead of allocating a fresh header-only
			// buffer. 16-byte header matches the new Phase
			// 2-prep layout:
			//   [data - 16] = pad
			//   [data - 12] = capacity (= 0)
			//   [data - 8] = rc slot, set to 0x80000000 — the
			//                "static, never touch" sentinel that
			//                __fern_rc_inc / __fern_rc_dec branch
			//                on (high bit set ⇒ no-op).
			//   [data - 4] = length (= 0)
			//   [.LArr_Empty] = data (a single byte for safety)
			// See docs/RC-PERCEUS-PLAN.md.
			g.line(".align 16")
			g.line("\t.4byte 0")          // pad
			g.line("\t.4byte 0")          // cap = 0
			g.line("\t.4byte 0x80000000") // rc = SENTINEL_STATIC
			g.line("\t.4byte 0")          // length = 0
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
				// Phase 1e-enums-ii: each sentinel carries the same
				// 8-byte rc header as a heap box (rc=0x80000000 at
				// [ptr-8], pad at [ptr-4]) so __fern_rc_inc/dec
				// short-circuit on the high bit when the enum-ii
				// predicate widening starts dec'ing enum locals that
				// hold a payloadless variant. Without it the dec
				// would read .rodata at [ptr-8] and attempt a write
				// (segfault on the read-only section).
				g.line(".align 4")
				g.line("\t.4byte 0x80000000") // rc header (static sentinel)
				g.line("\t.4byte 0")          // pad
				g.line(fmt.Sprintf(".LEnumSentinel_%d:", t))
				g.line(fmt.Sprintf("\t.4byte %d", t))
			}
		}
		if ast.RcTrace {
			// Heap event tracer (#6068): the fixed text of a
			// __fern_rct_ev line. The writer passes exact lengths, so
			// the `.asciz` NULs are never emitted — the separator and
			// newline labels are one byte each as far as write(2) is
			// concerned.
			g.label(".Lrct_str_pre")
			g.line(`	.asciz "rctrace "`)
			g.label(".Lrct_str_sp")
			g.line(`	.asciz " "`)
			g.label(".Lrct_str_nl")
			g.line(`	.asciz "\n"`)
		}
		if ast.LeakCheckEnabled {
			// Leak detector (#5362 slice 1): the fixed text of
			// __fern_lc_report's summary line. `.asciz` for uniformity
			// with the literals above; the report writes exact lengths,
			// so the trailing NULs are never emitted.
			g.label(".Llc_str_allocs")
			g.line(`	.asciz "leakcheck: allocs="`)
			g.label(".Llc_str_frees")
			g.line(`	.asciz " frees="`)
			g.label(".Llc_str_live")
			g.line(`	.asciz " live_bytes="`)
			g.label(".Llc_str_nl")
			g.line(`	.asciz "\n"`)
			if ast.SanitizeEnabled {
				// The sanitizer's leak VERDICT (#5545), printed after
				// the summary above when live_bytes > 0. The summary is
				// three numbers a reader has to compare; this says
				// whether the run was clean, which is the question a
				// sanitizer exists to answer.
				g.label(".Lsan_str_leak")
				g.line(fmt.Sprintf("	.asciz %q", sanLeakPrefix))
				g.label(".Lsan_str_bytesin")
				g.line(fmt.Sprintf("	.asciz %q", sanLeakMiddle))
				g.label(".Lsan_str_blocks")
				g.line(fmt.Sprintf("	.asciz %q", sanLeakSuffix))
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
	if g.usesAlloc || g.usesEnv || g.usesArgs || g.usesReadLine || g.usesReaderWriter || g.usesStrIdx || g.usesRcDec || g.usesRcUnderflowCount || g.usesMapHashSeed || ast.LeakCheckEnabled {
		g.line("")
		g.line(".section .bss")
		if ast.LeakCheckEnabled {
			// Leak detector counters (#5362 slice 1). alloc_count /
			// alloc_bytes tick in __fern_alloc (post-16-rounding, both
			// the freelist-pop and bump paths); free_count / free_bytes
			// in __fern_free (same rounding). __fern_lc_report prints
			// them at exit; live_bytes = alloc_bytes − free_bytes.
			g.line(".align 8")
			g.label("__fern_lc_alloc_count")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__fern_lc_alloc_bytes")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__fern_lc_free_count")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__fern_lc_free_bytes")
			g.line("\t.quad 0")
		}
		if g.usesAlloc {
			// Single-cursor bump allocator. See the x86-64
			// emitAllocRuntime comment. (The persistent cursor +
			// mode byte that used to back the `state` feature are
			// gone.)
			g.line(".align 8")
			g.label("__fern_heap_ptr")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__fern_heap_end")
			g.line("\t.quad 0")
			// Phase 6: the region base captured at the lazy mmap, so
			// __fern_heap_bump_bytes can report (cursor − base) — the
			// bump high-water mark. Zero until the first allocation.
			g.line(".align 8")
			g.label("__fern_heap_base")
			g.line("\t.quad 0")
		}
		if g.usesEnv {
			g.line(".align 8")
			g.label("__fern_envp")
			g.line("\t.quad 0")
		}
		if g.usesStrIdx {
			g.line(".align 8")
			g.label("__fern_str_idx_scratch")
			g.line("\t.quad 0")
		}
		if g.usesArgs {
			g.line(".align 8")
			g.label("__fern_argc")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__fern_argv")
			g.line("\t.quad 0")
			g.line(".align 8")
			g.label("__fern_args_cache")
			g.line("\t.quad 0")
		}
		if g.usesReadLine || g.usesReaderWriter {
			// 4 KiB scratch buffer for the byte-by-byte
			// read loop. Shared by stdin-only
			// __fern_read_line and the Reader-receiving
			// __fern_reader_read_line.
			g.line(".align 8")
			g.label("__fern_read_line_buf")
			g.line("\t.space 4096")
		}
		if g.usesRcDec || g.usesRcUnderflowCount || g.usesArrDec || g.usesMapDrop {
			// Phase 3 rc-underflow detector counter (i32 in the
			// low word). __fern_rc_dec bumps it on an over-release;
			// __fern_rc_underflow_count reads it back.
			g.line(".align 8")
			g.label("__fern_rc_underflow")
			g.line("\t.quad 0")
		}
		if g.usesArrPushGrow || g.usesArrPushSharedCount || g.usesArrPushSharedBytes {
			// The rc==1 cliff counter (i32 in the low word).
			// __fern_arr_push_grow bumps it when it copies a buffer
			// that had SPARE CAPACITY — i.e. the copy was forced by an
			// extra reference, not by a full buffer.
			g.line(".align 8")
			g.label("__fern_arr_push_shared")
			g.line("\t.quad 0")
			// The same crossings WEIGHTED by the bytes they copied
			// (oldLen * stride, summed as an i64). See
			// emitArrPushSharedBytesRuntime for why the count alone is
			// not a ranking signal.
			g.label("__fern_arr_push_copied")
			g.line("\t.quad 0")
		}
		if g.usesMapHashSeed {
			// core/map's per-process string-hash seed (#6194), i32 in
			// the low word. Zero means "not yet drawn" AND is core/map's
			// "unseeded" sentinel, so __fern_map_hash_seed forces the
			// drawn value nonzero and the two readings can't collide.
			g.line(".align 8")
			g.label("__fern_map_seed")
			g.line("\t.quad 0")
		}
		if ast.RcFreeEnabled && g.usesAlloc {
			// Two-tier segregated freelist heads (256 slots).
			//   0..127  — small tier: 16-byte exact-fit classes; head i is
			//             the freelist for blocks of size (i+1)*16 (16..2048).
			//   128+b   — large tier: power-of-two class; head 128+b holds
			//             freed blocks of capacity 2^b (b = 12..30, i.e.
			//             4 KiB..1 GiB). See __fern_alloc for the binning.
			// Each free block stores its successor's pointer in its first 8
			// bytes; zero = empty class.
			g.line(".align 8")
			g.label("__fern_freelist_heads")
			g.line("\t.space 2048")
			// Shadow copy for the one-level arena checkpoint
			// (__fern_heap_mark / __fern_heap_release_to). Not gated on the
			// mark helpers being used: it is 2 KiB of .bss, and gating it
			// would couple the alloc BSS layout to an unrelated flag.
			g.label("__fern_freelist_shadow")
			g.line("\t.space 2048")
		}
	}
}

// emitAllocRuntime emits `__fern_alloc(size: i64) -> i64`,
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
// Single-cursor bump allocator: every allocation bumps the one
// `__fern_heap_ptr` / `__fern_heap_end` pair. A second
// "persistent" region (selected by a `__fern_alloc_mode` byte)
// used to back the removed `state` feature; with `state` and the
// per-request arena reset both gone, the mode flag and the
// persistent cursors were deleted. See the arm64 generator's
// `emitAllocRuntime` comment for the full rationale.
// abortMessages are the fatal-abort diagnostics (#5538): a fixed message
// written to stderr before the process exits, so a bounds / arena / slice
// abort names its cause instead of exiting with a bare code. Ordered (not a
// map) so emission stays deterministic (byte-identical output).
//
// `when`, if non-nil, gates the message on a debug build mode: the sanitizer
// diagnostics (#5545) exist only where their detector does, so a release
// build's .rodata is byte-for-byte what it was before the mode existed.
var abortMessages = []struct {
	label, text string
	code        int
	when        func() bool
}{
	{"__fern_msg_arr_oob", "fern: array index out of range\n", 134, nil},
	{"__fern_msg_slice_oob", "fern: slice index out of range\n", 134, nil},
	{"__fern_msg_oom", "fern: out of memory (heap arena exhausted)\n", ExitArenaExhausted, nil},
	{"__fern_msg_slice_range", "fern: slice range out of bounds\n", 134, nil},
	{"__fern_msg_str_slice", "fern: string index out of range\n", 134, nil},
	{sanDoubleFreeMsg, "fern-sanitizer: rc over-release (double free)\n", ExitSanitizer, func() bool { return ast.RcUnderflowTrap }},
	{sanUseAfterFreeMsg, "fern-sanitizer: use-after-free (touched a quarantined block)\n", ExitSanitizer, func() bool { return ast.RcFreeDebug }},
}

// The two sanitizer diagnostics (#5545). Named constants because each is
// written at one emission site and read at several trap sites, and a typo in
// the label would be a link error in a build mode nothing routinely compiles.
const (
	sanDoubleFreeMsg   = "__fern_msg_san_double_free"
	sanUseAfterFreeMsg = "__fern_msg_san_uaf"
)

// ExitSanitizer is the status a sanitizer build exits with when a heap
// memory-safety check fires (#5545).
//
// Picked on ExitArenaExhausted's reasoning: clear of the whole 128+signal
// range, so no signal death can forge it, and distinct from 125 so a
// sanitizer abort and an arena trap are told apart by status alone — they
// have different causes and different fixes. The `fern-sanitizer:` line on
// stderr and the backtrace under it remain the primary diagnostic; this only
// makes the status sufficient for a harness that captures neither.
const ExitSanitizer = 124

// The sanitizer's exit-time leak verdict (#5545) — three fixed segments
// around two decimal numbers, appended to the leakcheck summary when the
// run ends with live bytes outstanding:
//
//	fern-sanitizer: leak <live_bytes> bytes in <allocs-frees> blocks
//
// A clean run prints nothing extra, so "no fern-sanitizer: line on stderr"
// is the whole pass condition — the summary's three numbers are still there
// for anyone who wants them, but reading them is no longer required to know
// whether the program leaked. Emitted only under ast.SanitizeEnabled: plain
// FERN_LEAKCHECK=1 keeps its exactly-one-line stderr contract.
//
// The arm64 backend carries the identical segments; a program's sanitizer
// output must not depend on which native it was built for.
const (
	sanLeakPrefix = "fern-sanitizer: leak "
	sanLeakMiddle = " bytes in "
	sanLeakSuffix = " blocks"
)

// ExitArenaExhausted is the status a Fern binary exits with when __fern_alloc's
// bounds check trips — the fixed bump arena is full.
//
// Deliberately NOT 137, which is what this used to be. 137 is 128+9, the status
// a shell reports for a SIGKILL, so an arena trap was indistinguishable from the
// kernel OOM-killer reaping the process. The two have opposite causes and
// opposite fixes: an arena trap is a REAL, reproducible failure in the program
// (usually a leak) that recurs on the next run, while a SIGKILL means the HOST
// ran out of RAM and the run should be retried with a smaller budget. Telling
// them apart cost a manual investigation every time, and three harness sites had
// given up and were treating any 137 as infra — silently swallowing genuine
// compiler regressions.
//
// 125 is clear of the whole 128+signal range, so no signal death can forge it,
// and under the 126 ceiling WASI imposes on exit statuses, so the value survives
// being reported through wasmtime. The stderr message is unchanged and remains
// the primary diagnostic; this only makes the status alone sufficient.
const ExitArenaExhausted = 125

func abortMsg(label string) (text string, code int) {
	for _, m := range abortMessages {
		if m.label == label {
			return m.text, m.code
		}
	}
	panic("x86_64: unknown abort message " + label)
}

// emitAbort routes a fatal abort site through __fern_report: it points rsi/edx
// at the named diagnostic, sets the exit code, and tail-jumps to the reporter
// (which writes to stderr, then exit_group). Replaces a bare, silent
// `mov edi, code; syscall` so the failure names its cause (#5538).
func (g *generator) emitAbort(label string) {
	text, code := abortMsg(label)
	g.emit(fmt.Sprintf("lea rsi, [rip + %s]", label))
	g.emit(fmt.Sprintf("mov edx, %d", len(text)))
	g.emit(fmt.Sprintf("mov edi, %d", code))
	g.emit("jmp __fern_report")
}

// emitAbortRuntime emits __fern_report — write(2, msg, len) then
// exit_group(code) — plus the abort message strings. Emitted unconditionally
// (once): the bounds / arena / slice sites jmp here instead of exiting
// silently (#5538). The messages sit in .rodata; the reporter's write length
// excludes the .asciz NUL.
const abortBacktraceMsg = "backtrace:\n"

func (g *generator) emitAbortRuntime() {
	g.line("")
	g.line(".globl __fern_report")
	g.line(".type __fern_report, @function")
	g.label("__fern_report") // rsi = msg ptr, edx = length, edi = exit code
	g.emit("mov r15d, edi")  // save exit code (r15 survives the writes below; we never return)
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("mov edi, 2")             // fd = stderr
	g.emitSyscallPreloaded(sysWrite) // write(2, msg, len)
	// Backtrace (#5538): walk the frame-pointer chain and print each return
	// address in hex. With `-g` (the .symtab) they resolve to functions via
	// addr2line / nm. Bounded to 64 frames; terminates at rbp == 0 (main's
	// saved rbp, since the kernel zeroes rbp at entry).
	g.emit("lea rsi, [rip + __fern_msg_bt]")
	g.emit(fmt.Sprintf("mov edx, %d", len(abortBacktraceMsg)))
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("mov edi, 2")
	g.emitSyscallPreloaded(sysWrite)
	g.emit("mov rbx, rbp") // rbx = frame pointer (survives __fern_print_hex + syscalls)
	g.emit("mov r14d, 64") // frame budget
	g.label(".Lbt_loop")
	g.emit("test rbx, rbx")
	g.emit("jz .Lbt_done")
	g.emit("test r14d, r14d")
	g.emit("jz .Lbt_done")
	g.emit("mov rsi, [rbx + 8]") // return address
	g.emit("test rsi, rsi")
	g.emit("jz .Lbt_done")
	g.emit("call __fern_print_hex")
	g.emit("mov rbx, [rbx]") // next frame
	g.emit("dec r14d")
	g.emit("jmp .Lbt_loop")
	g.label(".Lbt_done")
	g.emit("mov edi, r15d")
	g.emitSyscall(sysExitGroup) // exit_group(code)
	g.line(".size __fern_report, .-__fern_report")

	// __fern_print_hex(rsi = value) writes "  0x<16 hex>\n" to stderr. Uses
	// only rax/rcx/rdx/rdi/rsi so the caller's rbx/r14/r15 survive. No rol
	// (the native assembler lacks it) — nibbles come off the low end via
	// shr, written right-to-left.
	g.line("")
	g.line(".globl __fern_print_hex")
	g.line(".type __fern_print_hex, @function")
	g.label("__fern_print_hex")
	g.emit("sub rsp, 32")
	g.emit("mov byte ptr [rsp], 32")      // ' '
	g.emit("mov byte ptr [rsp + 1], 32")  // ' '
	g.emit("mov byte ptr [rsp + 2], 48")  // '0'
	g.emit("mov byte ptr [rsp + 3], 120") // 'x'
	g.emit("mov byte ptr [rsp + 20], 10") // '\n'
	g.emit("mov ecx, 16")
	g.emit("lea rdi, [rsp + 19]") // last hex digit slot
	g.label(".Lph_loop")
	g.emit("mov rax, rsi")
	g.emit("and eax, 15") // low nibble
	g.emit("cmp al, 10")
	g.emit("jb .Lph_dec")
	g.emit("add al, 87") // 'a' - 10
	g.emit("jmp .Lph_put")
	g.label(".Lph_dec")
	g.emit("add al, 48") // '0'
	g.label(".Lph_put")
	g.emit("mov [rdi], al")
	g.emit("dec rdi")
	g.emit("shr rsi, 4")
	g.emit("dec ecx")
	g.emit("jnz .Lph_loop")
	g.emit(fmt.Sprintf("mov eax, %d", sysWrite))
	g.emit("mov edi, 2") // stderr
	g.emit("mov rsi, rsp")
	g.emit("mov edx, 21")
	g.emitSyscallPreloaded(sysWrite)
	g.emit("add rsp, 32")
	g.emit("ret")
	g.line(".size __fern_print_hex, .-__fern_print_hex")

	g.line(".section .rodata")
	for _, m := range abortMessages {
		if m.when != nil && !m.when() {
			continue
		}
		g.label(m.label)
		g.emit(fmt.Sprintf(".asciz %q", m.text))
	}
	g.label("__fern_msg_bt")
	g.emit(fmt.Sprintf(".asciz %q", abortBacktraceMsg))
	g.line(".text")
}

// emitRcTraceEvent emits the ast.RcTrace (FERN_RC_TRACE=1) hook that
// reports one heap event: kind 'a' (alloc) or 'f' (free), with the
// block pointer in ptrReg and its 16-rounded size in sizeReg. No-op
// when the flag is off, so an untraced build is byte-identical.
//
// The site reported is `[rsp]` — the caller's return address — read
// BEFORE the saves below move the stack pointer. At an alloc this hook
// sits after the epilogue's pops (so [rsp] is __fern_alloc's own return
// address); at a free it sits at the leaf entry, where [rsp] is
// likewise the caller's. Either way that address belongs to the code
// that asked for or released the memory, which is the only thing worth
// naming — every block on the heap came from the same two helpers.
//
// ptrReg/sizeReg are saved and restored around the call rather than
// left to the callee, because at both hook sites they carry values the
// surrounding code still needs (__fern_alloc's result, __fern_free's
// two arguments) and the argument setup below overwrites the argument
// registers themselves. Two pushes keeps rsp 16-byte aligned.
func (g *generator) emitRcTraceEvent(kind byte, ptrReg, sizeReg string) {
	if !ast.RcTrace {
		return
	}
	g.emit("mov rcx, [rsp]") // site: caller return address, before any push
	g.emit(fmt.Sprintf("push %s", ptrReg))
	g.emit(fmt.Sprintf("push %s", sizeReg))
	g.emit(fmt.Sprintf("mov rdx, %s", sizeReg)) // arg 3: size
	g.emit(fmt.Sprintf("mov rsi, %s", ptrReg))  // arg 2: ptr
	g.emit(fmt.Sprintf("mov edi, %d", kind))    // arg 1: 'a' | 'f'
	g.emit("call __fern_rct_ev")
	g.emit(fmt.Sprintf("pop %s", sizeReg))
	g.emit(fmt.Sprintf("pop %s", ptrReg))
}

// emitRctRuntime emits `__fern_rct_ev(kind, ptr, size, site)` — the
// ast.RcTrace (FERN_RC_TRACE=1) event writer. One line to stderr:
//
//	rctrace <a|f> <ptr> <size> <site>
//
// with each number fixed-width 16 hex digits (see ast.RcTrace for why
// fixed-width). System V: edi = kind char, rsi = ptr, rdx = size,
// rcx = site.
//
// Every register the helper touches is saved, because it is injected
// mid-flow at sites that have live values in caller-saved registers —
// including rcx and r11, which `syscall` itself clobbers, so the three
// numbers are parked in rbx/r12/r13/r14 before the first write rather
// than re-read from argument registers between syscalls. Like
// __fern_lc_report the formatting is self-contained (a hex loop into a
// stack buffer): the language's own i64-to-string paths are Fern-level
// and cannot be assumed present in an arbitrary program.
func (g *generator) emitRctRuntime() {
	g.line("")
	g.line(".globl __fern_rct_ev")
	g.line(".type __fern_rct_ev, @function")
	g.label("__fern_rct_ev")
	saved := []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "rbx", "r12", "r13", "r14"}
	for _, r := range saved {
		g.emit("push " + r)
	}
	g.emit("mov rbx, rdi") // kind char
	g.emit("mov r12, rsi") // ptr
	g.emit("mov r13, rdx") // size
	// Round to the same (size+15)&-16 __fern_alloc applies, so an `a`
	// line and the `f` line that retires the same block report the
	// same size — the alloc hook sits after that rounding but the free
	// hook sits at the leaf entry, ahead of __fern_free's own copy of
	// it. Rounding an already-rounded size is identity, so doing it
	// here once covers both sites. Also keeps the trace's arithmetic
	// agreeing with leakcheck's, which rounds identically.
	g.emit("add r13, 15")
	g.emit("and r13, -16")
	g.emit("mov r14, rcx") // site
	g.emit("lea rsi, [rip + .Lrct_str_pre]")
	g.emit("mov edx, 8")
	g.emit("call .Lrct_write")
	// The kind char is a value, not a literal, so it needs a byte of
	// memory to point write(2) at: borrow 16 bytes of stack.
	g.emit("sub rsp, 16")
	g.emit("mov [rsp], bl")
	g.emit("mov rsi, rsp")
	g.emit("mov edx, 1")
	g.emit("call .Lrct_write")
	g.emit("add rsp, 16")
	for _, r := range []string{"r12", "r13", "r14"} {
		g.emit("lea rsi, [rip + .Lrct_str_sp]")
		g.emit("mov edx, 1")
		g.emit("call .Lrct_write")
		g.emit("mov rdi, " + r)
		g.emit("call .Lrct_wrhex")
	}
	g.emit("lea rsi, [rip + .Lrct_str_nl]")
	g.emit("mov edx, 1")
	g.emit("call .Lrct_write")
	for i := len(saved) - 1; i >= 0; i-- {
		g.emit("pop " + saved[i])
	}
	g.emit("ret")
	// .Lrct_write(rsi = buf, edx = len): one write(2) to stderr.
	g.label(".Lrct_write")
	g.emit("mov edi, 2")
	g.emitSyscall(sysWrite)
	g.emit("ret")
	// .Lrct_wrhex(rdi = value): 16 hex digits, most-significant first,
	// built into a stack buffer then written in one write(2). Fixed
	// width, so no leading-zero suppression and no length bookkeeping.
	g.label(".Lrct_wrhex")
	g.emit("sub rsp, 32")
	g.emit("mov rcx, 16")
	g.emit("mov rax, rdi")
	g.label(".Lrct_wrhex_loop")
	g.emit("mov rdx, rax")
	g.emit("and rdx, 15")
	g.emit("cmp rdx, 10")
	g.emit("jb .Lrct_wrhex_dig")
	g.emit("add rdx, 39") // 'a' - '0' - 10, i.e. skip ':'..'`'
	g.label(".Lrct_wrhex_dig")
	g.emit("add rdx, 48") // → ASCII
	g.emit("mov byte ptr [rsp + rcx - 1], dl")
	g.emit("shr rax, 4")
	g.emit("sub rcx, 1")
	g.emit("jnz .Lrct_wrhex_loop")
	g.emit("mov rsi, rsp")
	g.emit("mov edx, 16")
	g.emit("call .Lrct_write")
	g.emit("add rsp, 32")
	g.emit("ret")
	g.line(".size __fern_rct_ev, .-__fern_rct_ev")
}

func (g *generator) emitAllocRuntime() {
	// heapBytes is the per-region bump arena: 16 GiB, sized so a cmd/fern-built
	// self-host compiler can bootstrap-compile the WHOLE self-host source in one
	// process. Raised from 8 GiB when the stage-2 x86 self-compile crossed that
	// ceiling — the compiler's live set grows with every compiler-source
	// addition, and at ~0.6% headroom an IR-widening change tipped it. Kept in
	// lockstep with the arm64 backend and with the self-host emitters' own
	// heap_size (asm_ir.fern / asm_arm64_ir.fern).
	//
	// The mmap is MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE (0x22|0x4000), so the
	// wider reservation is exempt from Linux overcommit accounting and costs
	// nothing until touched: only the arena-exhaustion ceiling moves, not the
	// resident footprint. Exhaustion exits 125 (ExitArenaExhausted), which is
	// clear of the 128+signal range so a host OOM-kill (137) stays
	// distinguishable.
	//
	// Addressing: this arena is an mmap region held in a REGISTER, not a static
	// .bss block, so it has no RIP-relative / imm32 displacement ceiling and
	// base+size may exceed 2 GiB. The length loads via `movabs rsi` (64-bit
	// imm), and the heap END is built `movabs rcx, heapBytes` + `add rcx, rax`
	// rather than a signed-disp32 `lea [base + heapBytes]`, which caps at
	// 0x7FFFFFFF.
	const heapBytes = 17179869184 // 0x400000000, 16 GiB
	g.line("")
	g.line(".globl __fern_alloc")
	g.line(".type __fern_alloc, @function")
	g.label("__fern_alloc")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	// rbx, r12, r13 are callee-save in System V — save all
	// three up-front so we can use them as scratch without
	// stepping on the caller. r13 in particular holds the
	// mmap address hint between the label-pick and the
	// (possibly skipped) mmap call.
	g.emit("push rbx") // holds &__fern_heap_ptr
	g.emit("push r12") // holds &__fern_heap_end
	g.emit("push r13") // holds mmap address hint
	g.emit("add rdi, 15")
	g.emit("and rdi, -16")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): count every allocation — the
		// freelist-pop and bump paths both flow through here — at the
		// 16-rounded size, the same rounding __fern_free's counter
		// applies, so a block's alloc and eventual free cancel exactly.
		// (The large tier's further round-up to a power-of-two-ish
		// capacity is deliberately NOT counted: free is called with the
		// logical size and never sees that capacity, so counting it
		// would drift live_bytes by the internal waste.)
		g.emit("add qword ptr [rip + __fern_lc_alloc_count], 1")
		g.emit("add qword ptr [rip + __fern_lc_alloc_bytes], rdi")
	}
	if ast.RcFreeEnabled {
		// Two-tier segregated freelist — reuse a freed block before bumping.
		//
		// Small tier (16..2048 B): 16-byte exact-fit classes 0..127,
		// idx = (rdi>>4)-1. Perfect reuse for the many identically-sized
		// small structs / strings / boxes — unchanged from the original
		// Phase-3 design.
		//
		// Large tier (>2048 B): power-of-two classes. The request is
		// rounded UP to the next power of two — the bytes actually bumped —
		// and binned by that power's bit position + 128. Because every
		// block in a class is bumped at the class's power-of-two capacity, a
		// popped block always fits any later same-class request, so reuse
		// tolerates the size *variance* that exact-fit cannot: a 12 KiB and
		// a 13 KiB array both land in the 16 KiB class and recycle each
		// other. This is what lets a whole-compiler self-compile reclaim its
		// per-function array churn (instruction / block / value lists grow
		// past 2 KiB and vary per function) instead of leaking it. Cost is
		// ≤2x internal waste on large blocks — bounded, demand-paged, and
		// vastly cheaper than the exact-fit alternative, which reclaims none
		// of it. Blocks >1 GiB skip the freelist (bump-only) so the class
		// index can never run off the heads array.
		g.emit("cmp rdi, 16")
		g.emit("jb .Lalloc_bump")
		g.emit("cmp rdi, 2048")
		g.emit("ja .Lalloc_large")
		g.emit("mov rax, rdi")
		g.emit("shr rax, 4")
		g.emit("sub rax, 1") // small class index 0..127
		g.emit("jmp .Lalloc_fltry")
		g.label(".Lalloc_large")
		g.emit("cmp rdi, 0x40000000") // >1 GiB: bump-only, never freelisted
		g.emit("ja .Lalloc_bump")
		// Round the request UP to 3 significant bits (1 leading + 2 mantissa)
		// instead of the next power of two — ≤25% internal waste vs ≤2x. The
		// grid spacing at magnitude 2^e is 2^(e-2): round rdi up to a multiple
		// of that, giving the bytes to bump (rdi), then derive the class from
		// the rounded capacity so alloc and free agree.
		g.emit("bsr rcx, rdi")      // rcx = e = floor(log2(size)) >= 11
		g.emit("lea r8, [rcx - 2]") // r8 = e-2 = grid-spacing exponent
		g.emit("mov r9, 1")
		g.emit("mov rcx, r8")
		g.emit("shl r9, cl") // r9 = gran = 1<<(e-2)
		g.emit("lea rax, [rdi + r9 - 1]")
		g.emit("neg r9")
		g.emit("and rax, r9")  // rax = cap = roundup(size, gran)
		g.emit("mov rdi, rax") // rdi = cap = bytes to bump
		// class = (e2-11)*4 + (mant-4) + 128, where e2 = bsr(cap) (recomputed
		// so a round-up that carried into a new power of two is binned right)
		// and mant = cap>>(e2-2) ∈ {4,5,6,7}. Folds to 4*(e2-2) + mant + 88.
		g.emit("bsr rcx, rax")      // rcx = e2 = floor(log2(cap))
		g.emit("lea r8, [rcx - 2]") // r8 = e2-2
		g.emit("mov rdx, rax")
		g.emit("mov rcx, r8")
		g.emit("shr rdx, cl")                // rdx = mant = cap>>(e2-2)
		g.emit("lea rax, [rdx + r8*4 + 88]") // large class index
		g.label(".Lalloc_fltry")
		g.emit("lea rcx, [rip + __fern_freelist_heads]")
		g.emit("mov rdx, [rcx + rax*8]") // head
		g.emit("test rdx, rdx")
		g.emit("jz .Lalloc_bump")
		g.emit("mov r8, [rdx]")         // head.next
		g.emit("mov [rcx + rax*8], r8") // heads[idx] = next
		g.emit("mov rax, rdx")          // return reused block
		g.emit("pop r13")
		g.emit("pop r12")
		g.emit("pop rbx")
		g.emit("pop rbp")
		g.emitRcTraceEvent('a', "rax", "rdi")
		g.emit("ret")
		g.label(".Lalloc_bump")
	}
	// Single bump region: rbx/r12 = heap cursor/end.
	g.emit("lea rbx, [rip + __fern_heap_ptr]")
	g.emit("lea r12, [rip + __fern_heap_end]")
	g.emit("mov r13d, 0x10000000") // mmap address hint
	g.emit("mov rax, [rbx]")
	g.emit("test rax, rax")
	g.emit("jnz .Lalloc_have_heap")
	// Lazy mmap. Stash size across the syscall.
	g.emit("push rdi")
	g.emit("sub rsp, 8") // 16-byte align with the four pushes above
	g.emit("mov rdi, r13")
	g.emit(fmt.Sprintf("movabs rsi, %d", heapBytes))
	g.emit("mov edx, 3")
	// MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE (0x22|0x4000): exempt the
	// big lazy arena from Linux's overcommit accounting — without it the
	// heuristic refuses the single 8 GiB anonymous map outright on hosts
	// with RAM+swap below the arena size, failing every binary AT STARTUP
	// (the arm64 backend does the same; its comment has the full story).
	g.emit("mov r10d, 0x4022")
	g.emit("mov r8d, -1")
	g.emit("xor r9d, r9d")
	g.emitSyscall(sysMmap)
	g.emit("add rsp, 8")
	g.emit("pop rdi")
	g.emit("cmp rax, 0")
	g.emit("jl .Lalloc_oom")
	g.emit("mov [rbx], rax")
	// Phase 6: record the region base for __fern_heap_bump_bytes.
	g.emit("mov [rip + __fern_heap_base], rax")
	// heap-END = base + heapBytes. Build via 64-bit imm + add rather
	// than `lea [rax + heapBytes]` (a signed disp32 that caps at 0x7FFFFFFF)
	// so heapBytes may exceed 4 GiB.
	g.emit(fmt.Sprintf("movabs rcx, %d", heapBytes))
	g.emit("add rcx, rax")
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
	g.emitRcTraceEvent('a', "rax", "rdi")
	g.emit("ret")
	g.label(".Lalloc_oom")
	g.emitAbort("__fern_msg_oom")
	g.line(".size __fern_alloc, .-__fern_alloc")
}

// emitFreeRuntime emits `__fern_free(base: i64, size: i64)` — the
// freelist return path. When the freelist is enabled it pushes the
// `size`-byte block at `base` onto its size class's intrusive
// freelist (the successor pointer lives in the block's first 8
// bytes), using the same two-tier classing as __fern_alloc: 16-byte
// exact-fit for 16..2048 B, next-power-of-two for 2048 B..1 GiB.
// Blocks <16 B or >1 GiB are dropped (the bump region keeps them).
// When the freelist is disabled the helper is a no-op so a stray
// `__free` call in a non-freeing build is harmless.
//
// Under ast.RcFreeDebug the push is omitted entirely: NOTHING is ever
// recycled, so a quarantined block can't be handed back to a fresh
// allocation that would overwrite its RcPoison and turn a detectable
// use-after-free back into silent corruption. The leak accounting above
// still runs, which is what lets the leak census and the UAF detector
// be on at the same time — a quarantine is a release, and counting it
// where the release happens rather than where the memory is recycled
// keeps a correctly-freed array off the leak report. The cost is that
// the heap only ever grows; that is the price of the mode.
//
// System V: rdi = base, rsi = size. Leaf; no frame.
func (g *generator) emitFreeRuntime() {
	g.line("")
	g.line(".globl __fern_free")
	g.line(".type __fern_free, @function")
	g.label("__fern_free")
	g.emitRcTraceEvent('f', "rdi", "rsi")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): every reclamation site funnels
		// through this helper (box_free / arr_dec / map_drop /
		// drop_arr_ptr / drop_arr_str / alloc_reuse's mismatch path /
		// the __free builtin — the freelist push below is the only
		// other freelist writer and it's in this same function), so
		// counting here covers them all. Count at the same
		// (size+15)&-16 rounding __fern_alloc counted, in a scratch reg
		// so the RcFreeEnabled body's own rounding of rsi is untouched
		// (and the counters still tick when the freelist is compiled
		// out). __fern_alloc_reuse's in-place path calls neither
		// __fern_alloc nor __fern_free — in-place reuse counts as
		// NEITHER an alloc nor a free, which is exact: its class match
		// requires equal rounded sizes, so the block's original alloc
		// count still cancels against its eventual free.
		g.emit("lea rax, [rsi + 15]")
		g.emit("and rax, -16")
		g.emit("add qword ptr [rip + __fern_lc_free_count], 1")
		g.emit("add qword ptr [rip + __fern_lc_free_bytes], rax")
	}
	if ast.RcFreeEnabled && !ast.RcFreeDebug {
		g.emit("add rsi, 15")
		g.emit("and rsi, -16") // round size to the class granularity
		g.emit("cmp rsi, 16")
		g.emit("jb .Lfree_ret")
		g.emit("cmp rsi, 2048")
		g.emit("ja .Lfree_large")
		g.emit("mov rax, rsi")
		g.emit("shr rax, 4")
		g.emit("sub rax, 1") // small class index 0..127
		g.emit("jmp .Lfree_push")
		g.label(".Lfree_large")
		// Mirror __fern_alloc's large tier exactly: round the logical size up
		// to 3 significant bits and bin by the rounded capacity, so a block
		// returns to the class whose capacity it was bumped at. >1 GiB is
		// dropped (alloc never freelisted it).
		g.emit("cmp rsi, 0x40000000")
		g.emit("ja .Lfree_ret")
		g.emit("bsr rcx, rsi")
		g.emit("lea r8, [rcx - 2]")
		g.emit("mov r9, 1")
		g.emit("mov rcx, r8")
		g.emit("shl r9, cl") // r9 = gran
		g.emit("lea rax, [rsi + r9 - 1]")
		g.emit("neg r9")
		g.emit("and rax, r9") // rax = cap
		g.emit("bsr rcx, rax")
		g.emit("lea r8, [rcx - 2]")
		g.emit("mov rdx, rax")
		g.emit("mov rcx, r8")
		g.emit("shr rdx, cl")                // rdx = mant
		g.emit("lea rax, [rdx + r8*4 + 88]") // class
		g.label(".Lfree_push")
		g.emit("lea rcx, [rip + __fern_freelist_heads]")
		g.emit("mov rdx, [rcx + rax*8]") // old head
		g.emit("mov [rdi], rdx")         // base.next = old head
		g.emit("mov [rcx + rax*8], rdi") // heads[idx] = base
		g.label(".Lfree_ret")
	}
	g.emit("ret")
	g.line(".size __fern_free, .-__fern_free")
}

// emitAllocReuseRuntime emits
// `__fern_alloc_reuse(token: i64, tokenSize: i64, size: i64) -> i64`
// — the Phase 5 drop-reuse (FBIP) primitive. When `token` is a live
// block whose 16-byte size class matches `size`'s, it hands the block
// straight back (in-place reuse: no free, no alloc, no re-init of the
// fields the constructor is about to overwrite). When `token` is null,
// or the classes differ, it degrades to a plain allocation — freeing
// the (non-null) dropped block first so nothing leaks — which is why a
// mispaired reuse is only ever slower, never unsound. The class
// arithmetic mirrors __fern_alloc / __fern_free exactly
// ((sz+15)&-16, exact-fit 16..2048 classes), so a match guarantees the
// reused block is wide enough for the new value.
//
// System V: rdi = token, rsi = tokenSize, rdx = size. Non-leaf only on
// the class-mismatch path (it calls __fern_free); the reuse and the
// fresh-alloc paths tail into __fern_alloc with no frame.
func (g *generator) emitAllocReuseRuntime() {
	g.line("")
	g.line(".globl __fern_alloc_reuse")
	g.line(".type __fern_alloc_reuse, @function")
	g.label("__fern_alloc_reuse")
	g.emit("test rdi, rdi")
	g.emit("jz .Lreuse_fresh") // null token → plain alloc(size)
	// class(tokenSize) in rax, class(size) in rcx
	g.emit("mov rax, rsi")
	g.emit("add rax, 15")
	g.emit("and rax, -16")
	g.emit("mov rcx, rdx")
	g.emit("add rcx, 15")
	g.emit("and rcx, -16")
	g.emit("cmp rax, rcx")
	g.emit("jne .Lreuse_mismatch")
	// Classes match: reuse the block in place — return token.
	g.emit("mov rax, rdi")
	g.emit("ret")
	g.label(".Lreuse_mismatch")
	// Free the dropped block (rdi=token, rsi=tokenSize), preserving
	// size (rdx) across the call, then fall into the fresh alloc.
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rdx")   // save size
	g.emit("sub rsp, 8") // 16-byte align for the call
	g.emit("call __fern_free")
	g.emit("add rsp, 8")
	g.emit("pop rdx") // restore size
	g.emit("pop rbp")
	g.label(".Lreuse_fresh")
	g.emit("mov rdi, rdx")     // size
	g.emit("jmp __fern_alloc") // tail call
	g.line(".size __fern_alloc_reuse, .-__fern_alloc_reuse")
}

// emitArrDecRuntime emits `__fern_arr_dec(data: i64, stride: i64)`
// — the Phase 3 step-4 size-aware array dec. Decrements the array's
// rc and, on the last reference (rc==1), returns the BUFFER to the
// freelist (it does NOT walk elements — plain-array elements aren't
// rc-tracked, and on a push copy-grow the old buffer's pointer
// elements were transferred to the new buffer; the rc-tracked-
// element scope-exit case goes through __fern_drop_arr_ptr, which
// walks then frees). base = data - headerBytes, headerBytes =
// max(16, stride), size = headerBytes + cap*stride (cap at
// data-12). Same null / low-address / sentinel / underflow guards
// as __fern_rc_dec. Only emitted/used when the flag is on. The
// return value is discarded by the caller's OpDrop.
//
// System V: rdi = data, rsi = stride.
func (g *generator) emitArrDecRuntime() {
	g.line("")
	g.line(".globl __fern_arr_dec")
	g.line(".type __fern_arr_dec, @function")
	g.label("__fern_arr_dec")
	g.emit("push rbp") // 16-align for the __fern_free call
	g.emit("mov rbp, rsp")
	g.emit("mov rax, rdi") // default return = data
	g.emit("test rdi, rdi")
	g.emit("jz .Larrdec_ret")
	g.emit("cmp rdi, 0x10000")
	g.emit("jb .Larrdec_ret")
	g.emit("mov ecx, dword ptr [rdi - 8]") // rc
	g.emit("test ecx, ecx")
	g.emit("js .Larrdec_ret") // static sentinel
	g.emit("cmp ecx, 0")
	g.emit("jg .Larrdec_pos")
	g.emit("add dword ptr [rip + __fern_rc_underflow], 1") // over-release
	g.rcUnderflowTrap()
	g.emit("jmp .Larrdec_dec")
	g.label(".Larrdec_pos")
	g.emit("cmp ecx, 1")
	g.emit("jne .Larrdec_dec")
	// rc == 1 → free the buffer.
	g.quarantine("rdi")
	g.emit("mov r8, rsi") // stride
	g.emit("cmp r8, 16")
	g.emit("jae .Larrdec_hdr")
	g.emit("mov r8, 16")
	g.label(".Larrdec_hdr")
	g.emit("mov ecx, dword ptr [rdi - 12]") // cap (zero-extended)
	g.emit("mov rax, rcx")
	g.emit("imul rax, rsi") // cap * stride
	g.emit("add rax, r8")   // + headerBytes = size
	g.emit("sub rdi, r8")   // data - headerBytes = base (arg1)
	g.emit("mov rsi, rax")  // size (arg2)
	g.emit("call __fern_free")
	g.emit("jmp .Larrdec_ret")
	g.label(".Larrdec_dec")
	g.emit("sub ecx, 1")
	g.emit("mov dword ptr [rdi - 8], ecx")
	g.label(".Larrdec_ret")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_arr_dec, .-__fern_arr_dec")
}

// emitMapDropRuntime emits `__fern_map_drop(m) -> m` — the Phase 3
// map reclamation handler, the Map analogue of __fern_arr_dec. A Map
// handle `m` is a heap cell whose rc lives at [m-8] and whose buf
// pointer (the buckets+entries buffer) lives at [m+0]. On the LAST
// reference (rc==1) the handle's storage is returned to the freelist:
// first the buf (size = 24 + cap*(4+entryStride), cap at [buf+0],
// entryStride = 2*ptrW = 16 on x86-64), then the 16-byte handle cell
// itself (base = m-8). Entry KEYS / VALUES are NOT walked here — their
// rc accounting is untouched, so they leak exactly as before (a
// follow-up slice converts map.set to retain-on-store and frees
// array-typed values). On rc>1 the handle is just dec'd. Same null /
// low-address / sentinel / underflow guards as __fern_arr_dec; only
// emitted/used when the flag is on. The return value is discarded by
// the caller's OpDrop.
//
// System V: rdi = m. Returns m in rax.
func (g *generator) emitMapDropRuntime() {
	g.line("")
	g.line(".globl __fern_map_drop")
	g.line(".type __fern_map_drop, @function")
	g.label("__fern_map_drop")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")     // 16-byte alignment padding for the __fern_free calls
	g.emit("mov rbx, rdi") // rbx = m (callee-save across __fern_free)
	g.emit("mov rax, rdi") // default return = m
	g.emit("test rdi, rdi")
	g.emit("jz .Lmapdrop_ret")
	g.emit("cmp rdi, 0x10000")
	g.emit("jb .Lmapdrop_ret")
	g.emit("mov ecx, dword ptr [rbx - 8]") // handle rc
	g.emit("test ecx, ecx")
	g.emit("js .Lmapdrop_ret") // static sentinel
	g.emit("cmp ecx, 0")
	g.emit("jg .Lmapdrop_pos")
	g.emit("add dword ptr [rip + __fern_rc_underflow], 1") // over-release
	g.rcUnderflowTrap()
	g.emit("jmp .Lmapdrop_dec")
	g.label(".Lmapdrop_pos")
	g.emit("cmp ecx, 1")
	g.emit("jne .Lmapdrop_dec")
	// rc == 1 → free buf, then the handle cell.
	g.quarantine("rbx")
	g.emit("mov rdx, [rbx]") // buf = load_ptr(m)
	g.emit("test rdx, rdx")
	g.emit("jz .Lmapdrop_freehandle")
	g.emit("cmp rdx, 0x10000")
	g.emit("jb .Lmapdrop_freehandle")
	g.emit("mov ecx, dword ptr [rdx]") // cap (zero-extended)
	g.emit("imul rcx, rcx, 20")        // cap * (4 + entryStride=16)
	// ... plus the kv header, giving the buf's total size.
	g.emit(fmt.Sprintf("add rcx, %d", ast.MapHeaderBytes))
	g.emit("mov rsi, rcx") // size (arg2)
	g.emit("mov rdi, rdx") // base = buf (arg1)
	g.emit("call __fern_free")
	g.label(".Lmapdrop_freehandle")
	g.emit("mov rdi, rbx")
	g.emit("sub rdi, 8")  // handle alloc base = m - 8
	g.emit("mov esi, 16") // handle size = 16
	g.emit("call __fern_free")
	g.emit("mov rax, rbx")
	g.emit("jmp .Lmapdrop_ret")
	g.label(".Lmapdrop_dec")
	g.emit("sub ecx, 1")
	g.emit("mov dword ptr [rbx - 8], ecx")
	g.label(".Lmapdrop_ret")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_map_drop, .-__fern_map_drop")
}

// emitBoxFreeRuntime emits `__fern_box_free(data, size) -> data` — the
// Phase 3 struct/enum box reclamation helper. The IR pre-gates the
// call on rc==1 (an __fern_rc_is_unique block) and has already dropped
// the box's rc-tracked fields/payloads, so this helper just returns
// the box (base = data - 8 rc header) to the freelist. Returning data
// gives the uniform "OpCallDirect pushes one result" shape every
// backend relies on (so the IR can OpDrop it), which a direct
// void-returning __free call cannot on wasm. NULL / low-address guards
// keep a stray call safe. System V: rdi = data, rsi = size.
func (g *generator) emitBoxFreeRuntime() {
	g.line("")
	g.line(".globl __fern_box_free")
	g.line(".type __fern_box_free, @function")
	g.label("__fern_box_free")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")     // save caller rbx; holds data across the __fern_free call
	g.emit("push r12")     // 16-byte alignment padding for the call
	g.emit("mov rax, rdi") // default return = data
	g.emit("test rdi, rdi")
	g.emit("jz .Lboxfree_ret")
	g.emit("cmp rdi, 0x10000")
	g.emit("jb .Lboxfree_ret")
	g.quarantine("rdi")
	g.emit("mov rbx, rdi") // preserve data across __fern_free
	g.emit("sub rdi, 8")   // base = data - 8 rc header (arg1)
	g.emit("add rsi, 8")   // size + 8 rc header (arg2)
	g.emit("call __fern_free")
	g.emit("mov rax, rbx")
	g.label(".Lboxfree_ret")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_box_free, .-__fern_box_free")
}

// emitClosureDropRuntime emits `__fern_closure_drop(f) -> f` — the
// closure env/pair reclamation handler. A FuncType local holds a
// bare env block, a 16-byte closure pair, or a static function-
// value cell. On the LAST reference (rc==1) the rc1 block is freed
// via __fern_box_free (the payload size sits at [f-4], stashed by
// __fern_alloc_rc1); otherwise (rc>1, or the high-bit static
// sentinel) it routes to __fern_rc_dec, which carries the
// sentinel / low-address / underflow guards. NULL / low-address
// guarded. Captured pointer TARGETS (and, for a pair, the env it
// points to) are not walked here — they leak for now, the same
// one-level reclamation the other drop helpers do. System V:
// rdi = f. Returns f (via the tail-called helper) in rax.
func (g *generator) emitClosureDropRuntime() {
	g.line("")
	g.line(".globl __fern_closure_drop")
	g.line(".type __fern_closure_drop, @function")
	g.label("__fern_closure_drop")
	g.emit("mov rax, rdi") // default return = f
	g.emit("test rdi, rdi")
	g.emit("jz .Lcd_ret")
	g.emit("cmp rdi, 0x10000")
	g.emit("jb .Lcd_ret")
	g.emit("mov ecx, dword ptr [rdi - 8]") // rc
	g.emit("cmp ecx, 1")
	g.emit("jne .Lcd_dec") // rc != 1 (shared, or static sentinel) → dec
	// rc == 1 → free the env/pair block; payload size at [f-4].
	g.emit("mov esi, dword ptr [rdi - 4]")
	g.emit("jmp __fern_box_free") // tail-call: box_free(f, size) -> f
	g.label(".Lcd_dec")
	g.emit("jmp __fern_rc_dec") // tail-call: rc_dec(f) -> f
	g.label(".Lcd_ret")
	g.emit("ret")
	g.line(".size __fern_closure_drop, .-__fern_closure_drop")
}

// emitStrDecRuntime emits `__fern_str_dec(data) -> data` — the
// single-word (x86-64) heap-string reclamation helper, the string
// analogue of __fern_closure_drop. A heap string allocated by
// __fern_strcat (and the other string-producing runtimes) goes
// through __fern_alloc_rc1, so it carries a live rc at [data-8] and
// its payload size at [data-4]. On the LAST reference (rc==1) the
// block is returned to the freelist via __fern_box_free(data, size);
// every other source defers to __fern_rc_dec, which carries the
// underflow guard.
//
// Three guards keep non-freeable sources safe — they mirror the
// guards __fern_rc_inc/dec already apply to strings:
//   - NULL: a zero pointer returns immediately.
//   - inline SSO (low bit set): a string ≤7 bytes is a packed
//     register value, not a heap pointer; never deref it.
//   - below-heap (< 0x1000_0000): string LITERALS live in .rodata
//     under the heap base, so they can never reach the rc==1 free
//     path and have their read-only storage handed to the freelist.
//
// The high-bit static sentinel (the shared empty string) reads as a
// negative rc, so the rc!=1 branch routes it to __fern_rc_dec, which
// short-circuits on the sentinel. System V: rdi = data. Returns the
// input (via the tail-called helper) in rax.
func (g *generator) emitStrDecRuntime() {
	g.line("")
	g.line(".globl __fern_str_dec")
	g.line(".type __fern_str_dec, @function")
	g.label("__fern_str_dec")
	g.emit("mov rax, rdi") // default return = data
	g.emit("test rdi, rdi")
	g.emit("jz .Lstrdec_ret")
	g.emit("test dil, 1") // inline SSO packed value → not a heap ptr
	g.emit("jnz .Lstrdec_ret")
	g.emit("cmp rdi, 0x10000000") // below the heap base → literal/.rodata
	g.emit("jb .Lstrdec_ret")
	g.emit("mov ecx, dword ptr [rdi - 8]") // rc
	g.emit("cmp ecx, 1")
	g.emit("jne .Lstrdec_dec") // rc != 1 (shared, sentinel, or under) → dec
	// rc == 1 → free the rc1 block. [data-4] holds the string LENGTH, but
	// every heap-string producer requests length+1 from __fern_alloc_rc1 (the
	// trailing-NUL byte), so the box was size-classed at length+1+8. Free it
	// with the SAME length+1 payload, else the freed box lands in a smaller
	// class than its re-allocation looks up (the len≡8 (mod 16) straddle that
	// stranded freed strings). See docs/IR-SELFCOMPILE-OOM-FINDINGS.md.
	g.emit("mov esi, dword ptr [rdi - 4]")
	g.emit("add esi, 1")          // length -> allocated payload (length + NUL)
	g.emit("jmp __fern_box_free") // tail-call: box_free(data, size) -> data
	g.label(".Lstrdec_dec")
	g.emit("jmp __fern_rc_dec") // tail-call: rc_dec(data) -> data
	g.label(".Lstrdec_ret")
	g.emit("ret")
	g.line(".size __fern_str_dec, .-__fern_str_dec")
}

// emitSliceMakeRuntime emits `__fern_slice_make(data, len)`:
// allocate a 16-byte slice header [data_ptr, len] on the bump
// heap and return its address. The IR's slice-construction path
// (per `*ast.SliceExpr` and `*ast.IndexExpr` write side) calls
// this helper to materialise the header — element indexing is
// inlined as a stride-aware `data_ptr + i * N` via the existing
// __slice_idx_N inline helpers, so there's no per-stride
// dispatch needed here.
//
// Header layout: an 8-byte (pointer-width) data_ptr at +0, the
// i32 len at +ptrW (=8 here), 16 bytes total (the trailing 4 are
// padding). The full-width data pointer is what makes a slice
// over `.rodata` in a PIE shared object correct — a 32-bit field
// truncated high addresses (the as_bytes-in-.so bug). The IR's
// `len(slice)` shape reads `[slice + ptrW]`, so wasm32 (ptrW=4)
// keeps its 8-byte {i32 data, i32 len} layout unchanged while the
// native backends use the widened one.
//
// Calling convention: rdi = data_ptr (post-stride-offset),
// rsi = len. Returns slice header address in rax. Calls
// __fern_alloc which clobbers rcx / rdx / rsi / rdi (caller-
// save), so we stash both inputs in r12 / r13 around the alloc
// — same trick the strcat / env / args helpers use.
func (g *generator) emitSliceMakeRuntime() {
	g.line("")
	g.line(".globl __fern_slice_make")
	g.line(".type __fern_slice_make, @function")
	g.label("__fern_slice_make")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("mov r12, rdi") // save data_ptr (full 8 bytes)
	g.emit("mov r13, rsi") // save len
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc")
	g.emit("mov [rax], r12")      // [+0..+7] data_ptr (8-byte pointer)
	g.emit("mov [rax + 8], r13d") // [+8..+11] len (i32)
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("ret")
	g.line(".size __fern_slice_make, .-__fern_slice_make")
}

// emitSliceRangeRuntime emits `__fern_slice_range(lo, hi, len)` — the
// slice-construction bounds check (#5419). Traps with exit 134 unless
// 0 <= lo <= hi <= len, then returns the slice length hi - lo in eax.
// Two unsigned compares on the sign-extended values cover all four
// conditions: a negative bound sign-extends to a huge unsigned 64-bit
// value, so `hi > len` catches hi < 0 and `lo > hi` catches lo < 0.
// The movsxd normalisation is the same #5294 fix as __str_slice: an
// i32 bound can arrive with dirty high bits.
//
// Calling convention: edi = lo, esi = hi, edx = len (i32s).
func (g *generator) emitSliceRangeRuntime() {
	g.line("")
	g.line(".globl __fern_slice_range")
	g.line(".type __fern_slice_range, @function")
	g.label("__fern_slice_range")
	g.emit("movsxd rdi, edi") // lo (sign-extended from i32)
	g.emit("movsxd rsi, esi") // hi
	g.emit("movsxd rdx, edx") // len
	g.emit("cmp rsi, rdx")
	g.emit("ja .Lslicerange_trap") // hi > len (unsigned)
	g.emit("cmp rdi, rsi")
	g.emit("ja .Lslicerange_trap") // lo > hi (unsigned)
	g.emit("mov eax, esi")
	g.emit("sub eax, edi")
	g.emit("ret")
	g.label(".Lslicerange_trap")
	g.emitAbort("__fern_msg_slice_range")
	g.line(".size __fern_slice_range, .-__fern_slice_range")
}

// emitMemcpyRuntime emits `__fern_memcpy(dst, src, n)` —
// AAPCS-style return-the-dst contract (matches arm64). Uses
// `rep movsb` for the copy. Simple, correct, and on modern
// x86-64 CPUs the microcoded fast-string path is competitive
// with hand-rolled 8-byte loops for the buffer sizes the lang
// runtime sees (HTTP buffers, JSON, map entries).
func (g *generator) emitMemcpyRuntime() {
	g.line("")
	g.line(".globl __fern_memcpy")
	g.line(".type __fern_memcpy, @function")
	g.label("__fern_memcpy")
	g.emit("mov rax, rdi") // save dst for return
	g.emit("mov rcx, rdx") // count → rcx for `rep movsb`
	g.emit("cld")          // direction-flag = forward
	g.emit("rep movsb")    // [rdi++] = [rsi++], rcx times
	g.emit("ret")
	g.line(".size __fern_memcpy, .-__fern_memcpy")
}

// emitCCallRuntime emits `__c_call<n>(fn, a0..a{n-1})` — the FFI shim that
// invokes a C-ABI function pointer. Fern uses the System V integer-arg
// convention (rdi, rsi, rdx, rcx), the same as the C ABI, so the shim only
// has to drop the leading `fn` argument: save fn, slide each real arg down
// one register, and tail-jump to fn. The tail `jmp` preserves the entry
// stack alignment (rsp ≡ 8 mod 16, exactly a C call site), and fn's `ret`
// returns straight to the Fern caller with the result already in rax.
func (g *generator) emitCCallRuntime(n int) {
	g.emitCCallRuntimeSuffixed(n, "")
}

// emitCCallRuntimeSuffixed is emitCCallRuntime parameterised by a return-type
// suffix. The shim body is identical regardless of return type — it's a tail
// jump, so the callee's result lands in whichever register its ABI dictates
// (rax for integer, xmm0 for f32/f64) and flows straight back to the Fern
// caller. The only thing that differs is the symbol name (so a distinct
// checker FuncSig can declare the FP result type, making the call site read
// xmm0). Suffix is "" / "_f32" / "_f64".
func (g *generator) emitCCallRuntimeSuffixed(n int, suffix string) {
	name := fmt.Sprintf("__c_call%d%s", n, suffix)
	g.line("")
	g.line(".globl " + name)
	g.line(".type " + name + ", @function")
	g.label(name)
	g.emit("mov r11, rdi") // r11 = fn (preserved across the arg slide)
	// Slide a0..a{n-1} from (rsi,rdx,rcx) down to (rdi,rsi,rdx).
	regs := []string{"rdi", "rsi", "rdx", "rcx", "r8"}
	for i := 0; i < n; i++ {
		g.emit(fmt.Sprintf("mov %s, %s", regs[i], regs[i+1]))
	}
	g.emit("jmp r11") // tail-call fn; its ret returns to our caller, result in rax/xmm0
	g.line(".size " + name + ", .-" + name)
}

// emitRcIncRuntime emits `__fern_rc_inc(ptr) -> ptr` —
// increment the refcount at `[ptr - 8]` and return the input
// pointer unchanged. NULL-safe and sentinel-aware: if the rc
// word's high bit is set (0x80000000 = "static, never touch"),
// the helper returns the input pointer without modifying
// anything. The only static sentinel today is the shared
// empty-array (.LArr_Empty); string-literal heads will pick
// up the same treatment when Phase 1e widens the rc layout
// to strings.
//
// Returning the input pointer (rather than void) lets the IR
// codegen splice an inc into an expression evaluation chain
// without spilling to a temp local: `evaluate RHS; call
// __fern_rc_inc; store LHS` becomes a straight-line sequence.
//
// See docs/RC-PERCEUS-PLAN.md "Core operations".
func (g *generator) emitRcIncRuntime() {
	g.line("")
	g.line(".globl __fern_rc_inc")
	g.line(".type __fern_rc_inc, @function")
	g.label("__fern_rc_inc")
	g.emit("mov rax, rdi") // return value = input ptr
	g.emit("test rdi, rdi")
	g.emit("jz .Lrcinc_ret")
	// SSO inline-tag guard: native strings ≤7 bytes are packed inline
	// with bit 0 set (tag). Treating them as pointers would mis-read
	// [data-8] as an rc word and corrupt memory. Heap pointers from
	// __fern_alloc / __fern_alloc_rc1 are always 8-byte aligned (low
	// bit clear), so this guard is a no-op for every other caller
	// (arrays / structs / enums / closures / map handles / etc.).
	g.emit("test dil, 1")
	g.emit("jnz .Lrcinc_ret")
	// Below-heap guard (see emitRcDecRuntime): only heap objects carry an
	// rc word at [ptr-8]. The exit-dec sweep / sharing can hand this helper
	// a below-heap value — a no-capture closure's bare code pointer, static
	// data, or a non-pointer scalar — and inc'ing it would write [ptr-8]
	// into read-only .text/.rodata. The heap lives at/above the 0x1000_0000
	// mmap hint, so skip anything lower.
	g.emit("cmp rdi, 0x10000000")
	g.emit("jb .Lrcinc_ret")
	g.emit("mov ecx, dword ptr [rdi - 8]")
	if ast.RcFreeDebug {
		// UAF detector: a poisoned rc word means this block was
		// freed (quarantined) — touching it now is a use-after-free.
		g.emit(fmt.Sprintf("cmp ecx, %d", ast.RcPoison))
		g.emit("jne .Lrcinc_live")
		g.emitAbort(sanUseAfterFreeMsg) // names the stale holder in the backtrace
		g.label(".Lrcinc_live")
	}
	g.emit("test ecx, ecx")
	g.emit("js .Lrcinc_ret") // bit 31 set ⇒ static sentinel
	g.emit("add ecx, 1")
	g.emit("mov dword ptr [rdi - 8], ecx")
	g.label(".Lrcinc_ret")
	g.emit("ret")
	g.line(".size __fern_rc_inc, .-__fern_rc_inc")
}

// emitRcDecRuntime emits `__fern_rc_dec(ptr)` — decrement the
// refcount at `[ptr - 8]`. NULL-safe and sentinel-aware (see
// emitRcIncRuntime). Phase-1 simplification: on rc == 1 the
// helper still decrements to 0 instead of calling a
// type-specific drop handler + freelist push. The bump
// allocator leaks; Phase 3 introduces the real freelist and
// Phase 1e introduces the drop handlers. Until then, "freeing"
// just leaves the slot at rc = 0 so accidental re-inc /
// re-dec stays observable for the leak detector that phase 1
// testing will rely on.
func (g *generator) emitRcDecRuntime() {
	g.line("")
	g.line(".globl __fern_rc_dec")
	g.line(".type __fern_rc_dec, @function")
	g.label("__fern_rc_dec")
	g.emit("mov rax, rdi") // return value = input ptr (matches arm64)
	g.emit("test rdi, rdi")
	g.emit("jz .Lrcdec_ret")
	// SSO inline-tag guard — see __fern_rc_inc above. Heap pointers
	// are always 8-byte aligned (low bit clear), so this is a no-op
	// for every non-string caller; for native strings ≤7 bytes the
	// pointer is actually a packed inline value and must not be deref'd.
	g.emit("test dil, 1")
	g.emit("jnz .Lrcdec_ret")
	// Below-heap guard: Phase 1d-v's exit-dec sweep decrements every
	// local slot, including ones holding non-heap values (a non-pointer
	// scalar, static data, or a no-capture closure's bare code pointer).
	// Only heap-allocated objects carry an rc word at [ptr-8], and the
	// heap lives at/above the 0x1000_0000 mmap hint, so skip anything
	// lower — writing [ptr-8] of a .text/.rodata address would corrupt
	// read-only memory. (The old guard only rejected < 0x10000, letting
	// code/rodata/static addresses through.)
	g.emit("cmp rdi, 0x10000000")
	g.emit("jb .Lrcdec_ret")
	g.emit("mov ecx, dword ptr [rdi - 8]")
	if ast.RcFreeDebug {
		g.emit(fmt.Sprintf("cmp ecx, %d", ast.RcPoison))
		g.emit("jne .Lrcdec_live")
		g.emitAbort(sanUseAfterFreeMsg) // UAF: dec of a freed (quarantined) block
		g.label(".Lrcdec_live")
	}
	g.emit("test ecx, ecx")
	g.emit("js .Lrcdec_ret") // bit 31 set ⇒ static sentinel
	// Phase 3 underflow detector: a healthy dec operates on rc >= 1.
	// If rc <= 0 here this dec over-releases — bump the counter.
	g.emit("cmp ecx, 0")
	g.emit("jg .Lrcdec_dec")
	g.emit("add dword ptr [rip + __fern_rc_underflow], 1")
	g.rcUnderflowTrap()
	g.label(".Lrcdec_dec")
	g.emit("sub ecx, 1")
	g.emit("mov dword ptr [rdi - 8], ecx")
	g.label(".Lrcdec_ret")
	g.emit("ret")
	g.line(".size __fern_rc_dec, .-__fern_rc_dec")
}

// quarantine emits the ast.RcFreeDebug poison store: the rc word of the
// block whose DATA pointer is in dataReg is overwritten with
// ast.RcPoison, so __fern_rc_inc / __fern_rc_dec fault the moment a
// stale reference touches it and the backtrace names the holder whose
// count was wrong.
//
// Control falls THROUGH to the site's ordinary reclamation path. That
// path computes the block's size and calls __fern_free, which accounts
// the release for the leak census and (in this mode) declines to push
// the block onto a freelist — so the block is both never recycled and
// never mistaken for a leak. Nothing between here and the call writes
// [data-8], so the poison survives to the next toucher.
//
// No-op when the detector is off, which is what keeps the release build
// byte-identical.
func (g *generator) quarantine(dataReg string) {
	if ast.RcFreeDebug {
		g.emit(fmt.Sprintf("mov dword ptr [%s - 8], %d", dataReg, ast.RcPoison))
	}
}

// rcUnderflowTrap emits the fatal report that follows every
// __fern_rc_underflow bump under ast.RcUnderflowTrap
// (FERN_RC_UNDERFLOW_TRAP=1, or FERN_SANITIZE=1), so an over-release
// dies AT the offending dec instead of only incrementing a counter
// nobody reads.
//
// It routes through __fern_report (#5538) rather than dying on a bare
// `ud2`: the process now names its cause on stderr and prints the
// frame-pointer backtrace under it, so an over-release is diagnosable
// from a plain run. These helpers are leaves that never push rbp, so
// the walk starts at the caller's frame and the first address named is
// the function whose dec was wrong — the same answer the old
// SIGILL-plus-gdb recipe gave, without the gdb. (`break __fern_report`
// still stops there if you want the live registers.)
//
// No-op when the flag is off — the emitted asm is byte-identical to a
// build without the feature.
func (g *generator) rcUnderflowTrap() {
	if ast.RcUnderflowTrap {
		g.emitAbort(sanDoubleFreeMsg)
	}
}

// emitRcUnderflowCountRuntime emits `__fern_rc_underflow_count()
// -> i32` — returns the Phase 3 over-release counter that
// __fern_rc_dec bumps in __fern_rc_underflow. Mirrors arm64 + the
// wasm linear-memory reader.
func (g *generator) emitRcUnderflowCountRuntime() {
	g.line("")
	g.line(".globl __fern_rc_underflow_count")
	g.line(".type __fern_rc_underflow_count, @function")
	g.label("__fern_rc_underflow_count")
	g.emit("mov eax, dword ptr [rip + __fern_rc_underflow]")
	g.emit("ret")
	g.line(".size __fern_rc_underflow_count, .-__fern_rc_underflow_count")
}

// emitArrPushSharedCountRuntime emits `__fern_arr_push_shared_count() -> i32`
// — returns the count of appends that copied the whole buffer even though it
// had room, i.e. the copy was bought by an extra reference alone.
//
// This is the diagnostic for __fern_arr_push_grow's rc==1 cliff. That cliff is
// a performance CORRECTNESS boundary with no diagnostic of its own: one stray
// retain anywhere upstream — a return-transfer inc on an `own` param, a
// consumed-param entry retain, a caller-side alias inc — turns every append in
// a threaded accumulator into a full copy, and the program stays correct while
// going quadratic. Three separate regressions of exactly that shape landed and
// were each found somewhere else entirely, as an arena exhaustion or an OOM.
// A counter that says "you copied N times with room to spare" names the cliff
// at the point it is crossed. Mirrors __fern_rc_underflow_count.
func (g *generator) emitArrPushSharedCountRuntime() {
	g.line("")
	g.line(".globl __fern_arr_push_shared_count")
	g.line(".type __fern_arr_push_shared_count, @function")
	g.label("__fern_arr_push_shared_count")
	g.emit("mov eax, dword ptr [rip + __fern_arr_push_shared]")
	g.emit("ret")
	g.line(".size __fern_arr_push_shared_count, .-__fern_arr_push_shared_count")
}

// emitMapHashSeedRuntime emits `__fern_map_hash_seed() -> i32` — the
// per-process string-hash seed core/map mixes into its FNV basis so an
// attacker who controls key strings cannot precompute a colliding set
// offline (#6194).
//
// PER PROCESS, NOT PER MAP: the draw is a getrandom syscall, which a program
// that creates maps freely must not pay repeatedly. The cached word makes
// every map after the first a load.
//
// The cache flag and the value are the SAME word: zero means "not yet
// drawn", and `or eax, 1` forces the drawn value nonzero so a legitimately
// drawn seed can never read back as undrawn. That also lines up with
// core/map's own convention, where a zero seed means "unseeded" and selects
// the caller-supplied hash instead of the seeded string hash — a seed of 0
// escaping into the buffer would silently disable seeding for that map.
func (g *generator) emitMapHashSeedRuntime() {
	g.line("")
	g.line(".globl __fern_map_hash_seed")
	g.line(".type __fern_map_hash_seed, @function")
	g.label("__fern_map_hash_seed")
	g.emit("mov eax, dword ptr [rip + __fern_map_seed]")
	g.emit("test eax, eax")
	g.emit("jnz .Lmapseed_ret")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("call __fern_random_i32")
	g.emit("or eax, 1") // never 0 — see the doc above
	g.emit("mov dword ptr [rip + __fern_map_seed], eax")
	g.emit("pop rbp")
	g.label(".Lmapseed_ret")
	g.emit("ret")
	g.line(".size __fern_map_hash_seed, .-__fern_map_hash_seed")
}

// emitArrPushSharedBytesRuntime emits `__fern_arr_push_shared_bytes() -> i64`
// — the same cliff the counter counts, weighted by the bytes each crossing
// copied (oldLen * stride, summed).
//
// The count answers "did anything cross the cliff"; only the weight answers
// "does it matter". Measured 2026-08-04: a whole-module compile of
// checker.fern by the self-host compiler crosses the cliff 188 times and
// copies 812 bytes doing it — the crossings are 4-byte loop-depth stacks, and
// a conversion that eliminated every one of them would save under a kilobyte.
// The same reading for one threaded accumulator over 20k appends is 2.3 GB.
// Two rounds of accumulator work were scoped against the unweighted count and
// aimed at sites that could not have paid; this is the number that ranks them.
// Mirrors __fern_heap_bump_bytes (i64, leaf, no frame).
func (g *generator) emitArrPushSharedBytesRuntime() {
	g.line("")
	g.line(".globl __fern_arr_push_shared_bytes")
	g.line(".type __fern_arr_push_shared_bytes, @function")
	g.label("__fern_arr_push_shared_bytes")
	g.emit("mov rax, qword ptr [rip + __fern_arr_push_copied]")
	g.emit("ret")
	g.line(".size __fern_arr_push_shared_bytes, .-__fern_arr_push_shared_bytes")
}

// emitHeapBumpBytesRuntime emits `__fern_heap_bump_bytes() -> i64`,
// the Phase 6 measurement reader: returns the bump high-water mark
// (__fern_heap_ptr − __fern_heap_base) in bytes, or 0 before the first
// allocation seeds the cursor. Leaf; no frame.
func (g *generator) emitHeapBumpBytesRuntime() {
	g.line("")
	g.line(".globl __fern_heap_bump_bytes")
	g.line(".type __fern_heap_bump_bytes, @function")
	g.label("__fern_heap_bump_bytes")
	g.emit("mov rax, [rip + __fern_heap_ptr]")
	g.emit("test rax, rax") // never allocated → cursor 0 → 0
	g.emit("jz .Lheap_bump_zero")
	g.emit("sub rax, [rip + __fern_heap_base]")
	g.emit("ret")
	g.label(".Lheap_bump_zero")
	g.emit("xor eax, eax")
	g.emit("ret")
	g.line(".size __fern_heap_bump_bytes, .-__fern_heap_bump_bytes")
}

// emitHeapMarkRuntime emits `__fern_heap_mark() -> i64` and
// `__fern_heap_release_to(mark: i64)` — a one-level arena checkpoint.
//
// The pair exists so a batch-shaped workload can reclaim a phase's whole
// allocation set at once on a bump arena that otherwise never gives memory
// back: the self-host per-module emit accumulates ~0.4 GB per unit that
// nothing frees, so emitting ~35 units in one process walks off the end of
// the 16 GiB arena (__fern_alloc's bounds check, exit 137). Marking before a
// unit and releasing after writing it out keeps the peak at one unit.
//
// Releasing is only sound when NOTHING allocated after the mark is still
// reachable — the caller owns that invariant, exactly as it owns malloc/free
// pairing. Two details make the reset safe rather than merely fast:
//
//   - The freelist heads are snapshotted, not cleared. A block allocated AND
//     freed inside the window leaves a head pointing above the mark; after the
//     cursor rewinds, a later pop and a later bump would both hand out that
//     same address. Restoring the pre-mark heads drops precisely those
//     entries, while keeping the ones that predate the mark reusable. An entry
//     popped during the window is legitimately re-added: whatever was
//     allocated into it died at the release.
//   - A pre-mark block freed during the window is forgotten (its head is
//     restored to the older value). That is a bounded leak, not corruption.
//
// One live mark at a time — the shadow is a single fixed buffer, so marks do
// not nest. mark==0 (taken before the first allocation seeded the cursor) is
// treated as "no checkpoint" by release_to, so a stray release cannot zero the
// cursor and hand out the arena base.
func (g *generator) emitHeapMarkRuntime() {
	g.line("")
	g.line(".globl __fern_heap_mark")
	g.line(".type __fern_heap_mark, @function")
	g.label("__fern_heap_mark")
	g.emit("mov rax, [rip + __fern_heap_ptr]")
	if ast.RcFreeEnabled && g.usesAlloc {
		// rep movsq clobbers rcx/rsi/rdi. Every other reader of the arena
		// globals (__fern_heap_bump_bytes) touches only rax, so the emitted
		// code around a call here may well be holding live values in the
		// argument registers — restore them rather than assume the SysV
		// caller-saved rule is what the backend relies on.
		g.emit("push rcx")
		g.emit("push rsi")
		g.emit("push rdi")
		g.emit("lea rsi, [rip + __fern_freelist_heads]")
		g.emit("lea rdi, [rip + __fern_freelist_shadow]")
		g.emit("mov ecx, 256")
		g.emit("rep movsq")
		g.emit("pop rdi")
		g.emit("pop rsi")
		g.emit("pop rcx")
	}
	g.emit("ret")
	g.line(".size __fern_heap_mark, .-__fern_heap_mark")

	g.line("")
	g.line(".globl __fern_heap_release_to")
	g.line(".type __fern_heap_release_to, @function")
	g.label("__fern_heap_release_to")
	g.emit("test rdi, rdi")
	g.emit("jz .Lheap_rel_done")               // mark 0 = no checkpoint; leave the cursor alone
	g.emit("mov [rip + __fern_heap_ptr], rdi") // read the arg before clobbering rdi
	if ast.RcFreeEnabled && g.usesAlloc {
		g.emit("push rcx")
		g.emit("push rsi")
		g.emit("push rdi")
		g.emit("lea rsi, [rip + __fern_freelist_shadow]")
		g.emit("lea rdi, [rip + __fern_freelist_heads]")
		g.emit("mov ecx, 256")
		g.emit("rep movsq")
		g.emit("pop rdi")
		g.emit("pop rsi")
		g.emit("pop rcx")
	}
	g.label(".Lheap_rel_done")
	g.emit("ret")
	g.line(".size __fern_heap_release_to, .-__fern_heap_release_to")
}

// emitAllocBoxRuntime emits `__fern_alloc_box(size) -> data`
// — allocate a heap box with an 8-byte rc header carrying the
// static-sentinel 0x80000000 at `[base + 0]`, returning the
// data pointer `base + 8`. Used by every runtime helper that
// builds an Option / Result / IoError box so Phase 1e's
// predicate widening can call __fern_rc_inc/dec on enum
// values safely: the inc/dec helpers see the high bit at
// `[data - 8]` and short-circuit, leaving the runtime-owned
// box untouched.
//
// The caller passes the payload size (the same value it used
// to pass to __fern_alloc); this helper adds the header. All
// the caller's subsequent `[rax + off]` tag / payload stores
// stay at their existing offsets — they're relative to the
// returned data pointer, which already points past the header.
func (g *generator) emitAllocBoxRuntime() {
	g.line("")
	g.line(".globl __fern_alloc_box")
	g.line(".type __fern_alloc_box, @function")
	g.label("__fern_alloc_box")
	g.emit("add edi, 8") // size + rc header
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("call __fern_alloc")
	g.emit("pop rbp")
	g.emit("mov dword ptr [rax], 0x80000000") // static sentinel
	g.emit("add rax, 8")                      // return base + 8 (= data)
	g.emit("ret")
	g.line(".size __fern_alloc_box, .-__fern_alloc_box")
}

// emitAllocRc1Runtime emits `__fern_alloc_rc1(size) -> data` —
// identical to __fern_alloc_box but writes a live rc=1 at
// `[base+0]` instead of the immortal 0x80000000 sentinel. Used
// by closure env-block / pair allocations so the value is a
// real refcounted object (droppable at rc=0 in Phase 3) rather
// than an immortal one. The caller passes the payload size; the
// helper adds the 8-byte header and returns base+8, so all the
// caller's `[rax + off]` stores stay at their existing offsets.
func (g *generator) emitAllocRc1Runtime() {
	g.line("")
	g.line(".globl __fern_alloc_rc1")
	g.line(".type __fern_alloc_rc1, @function")
	g.label("__fern_alloc_rc1")
	g.emit("add edi, 8") // size + rc header
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")                  // scratch slot (keeps 16-alignment)
	g.emit("mov dword ptr [rbp - 8], edi") // save size+8 across the call
	g.emit("call __fern_alloc")
	g.emit("mov edi, dword ptr [rbp - 8]") // restore size+8
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("mov dword ptr [rax], 1") // live rc = 1 at base (= data-8)
	// Stash the payload size at base+4 (= data-4, the unused half of
	// the rc1 header) so a drop site can free the block without a
	// separate size header — the closure-env reclamation path reads
	// it. Harmless for every other rc1 user (nothing else reads it).
	g.emit("sub edi, 8")                   // recover payload size
	g.emit("mov dword ptr [rax + 4], edi") // size at base+4 (= data-4)
	g.emit("add rax, 8")                   // return base + 8 (= data)
	g.emit("ret")
	g.line(".size __fern_alloc_rc1, .-__fern_alloc_rc1")
}

// emitArrPushGrowRuntime emits `__fern_arr_push_grow(arr,
// oldLen, stride) -> new_data` — System V counterpart of the
// arm64 helper. Inputs rdi=arr, esi=oldLen, edx=stride.
// Returns new data pointer in rax. See arm64.go's
// emitArrPushGrowRuntime + docs/RC-PERCEUS-PLAN.md "Phase 2".
func (g *generator) emitArrPushGrowRuntime() {
	g.line("")
	g.line(".globl __fern_arr_push_grow")
	g.line(".type __fern_arr_push_grow, @function")
	g.label("__fern_arr_push_grow")
	// Fast path: rc == 1 AND oldLen < cap.
	g.emit("mov eax, dword ptr [rdi - 8]") // rc
	g.emit("cmp eax, 1")
	g.emit("jne .Lpush_shared")
	g.emit("mov eax, dword ptr [rdi - 12]") // cap
	g.emit("cmp esi, eax")
	g.emit("jge .Lpush_copy")
	// In place: rc = 2, len = oldLen + 1, return arr.
	g.emit("mov dword ptr [rdi - 8], 2")
	g.emit("lea eax, [rsi + 1]")
	g.emit("mov dword ptr [rdi - 4], eax")
	g.emit("mov rax, rdi")
	g.emit("ret")
	// rc != 1. If the buffer ALSO had spare capacity then the copy below is
	// bought entirely by the extra reference — the rc==1 cliff. Count it, so
	// an accumulator that has silently gone from O(n) to O(n²) can be seen
	// rather than inferred from an arena exhaustion somewhere downstream.
	// Off the fast path, so this costs one compare on a run that is about to
	// memcpy the whole buffer anyway.
	g.label(".Lpush_shared")
	g.emit("mov eax, dword ptr [rdi - 12]") // cap
	g.emit("cmp esi, eax")
	g.emit("jge .Lpush_copy") // genuinely full: not the cliff
	g.emit("add dword ptr [rip + __fern_arr_push_shared], 1")
	// Weight the crossing by what it actually costs: oldLen * stride bytes
	// about to be memcpy'd. rax / rcx are caller-saved and dead here (rax
	// held the capacity the compare above consumed), and the multiply is
	// 64-bit so a buffer past 4 GiB still accumulates exactly.
	g.emit("mov eax, esi") // zero-extend oldLen into rax
	g.emit("mov ecx, edx") // zero-extend stride into rcx
	g.emit("imul rax, rcx")
	g.emit("add qword ptr [rip + __fern_arr_push_copied], rax")
	g.label(".Lpush_copy")
	// Copy path. Stash arr / oldLen / stride / newLen / newCap /
	// headerBytes / new_data in callee-saves so they survive the
	// __fern_alloc + __fern_memcpy calls.
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("push r15")
	g.emit("sub rsp, 8")    // pad to 16-byte alignment
	g.emit("mov rbx, rdi")  // rbx = arr
	g.emit("mov r12d, esi") // r12d = oldLen
	g.emit("mov r13d, edx") // r13d = stride
	g.emit("mov r14d, esi")
	g.emit("add r14d, 1") // r14d = newLen = oldLen + 1
	// newCap = max(2 * newLen, 4)
	g.emit("mov r15d, r14d")
	g.emit("shl r15d, 1")
	g.emit("cmp r15d, 4")
	g.emit("jge .Lpush_cap_ok")
	g.emit("mov r15d, 4")
	g.label(".Lpush_cap_ok")
	// headerBytes = max(16, stride). Use ecx as scratch.
	g.emit("mov ecx, 16")
	g.emit("cmp r13d, 16")
	g.emit("jle .Lpush_hdr_set")
	g.emit("mov ecx, r13d")
	g.label(".Lpush_hdr_set")
	g.emit("push rcx")   // stash headerBytes (rsp now off by 8 again)
	g.emit("sub rsp, 8") // re-pad to 16 alignment (24 + 8 = 32, /16 = aligned)
	// allocSize = headerBytes + newCap * stride. eax scratch.
	g.emit("mov eax, r15d")
	g.emit("imul eax, r13d")
	g.emit("add eax, ecx")
	g.emit("mov edi, eax")
	g.emit("call __fern_alloc")
	// rax = base. new_data = base + headerBytes. Reload
	// headerBytes from stack (it was rcx, but rcx is caller-save
	// and __fern_alloc may have clobbered it).
	g.emit("mov rcx, qword ptr [rsp + 8]") // reload headerBytes
	g.emit("lea r11, [rax + rcx]")         // r11 = new_data (caller-save, OK)
	// Store cap at [base + headerBytes - 12]
	g.emit("lea rdx, [rax + rcx - 12]")
	g.emit("mov dword ptr [rdx], r15d")
	// Store rc = 1 at [base + headerBytes - 8]
	g.emit("lea rdx, [rax + rcx - 8]")
	g.emit("mov dword ptr [rdx], 1")
	// Store len = newLen at [base + headerBytes - 4]
	g.emit("lea rdx, [rax + rcx - 4]")
	g.emit("mov dword ptr [rdx], r14d")
	// memcpy(new_data, arr, oldLen * stride)
	g.emit("mov rdi, r11")
	g.emit("mov rsi, rbx")
	g.emit("mov eax, r12d")
	g.emit("imul eax, r13d")
	g.emit("mov edx, eax")
	g.emit("mov qword ptr [rsp], r11") // stash new_data across the call
	g.emit("call __fern_memcpy")
	g.emit("mov rax, qword ptr [rsp]") // reload new_data
	// Tear down. We pushed THREE 8-byte slots beyond the 6
	// callee-saves: prolog pad + inner push rcx + inner pad.
	// All three live above the saved r15, so undo 24 bytes
	// before popping the callee-saves.
	g.emit("add rsp, 24")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_arr_push_grow, .-__fern_arr_push_grow")
}

// emitArrPushGrowPtrRuntime emits `__fern_arr_push_grow_ptr(arr,
// oldLen, stride) -> new_data` — the rc-tracked-pointer-element
// variant of __fern_arr_push_grow. Identical fast path (rc==1 +
// capacity → in-place, no element traffic: the buffer lives on with
// its counts intact). On the COPY path, after the memcpy it walks the
// oldLen copied elements and __fern_rc_inc's each so the fresh buffer
// independently OWNS its references — __fern_rc_inc's null / SSO
// low-bit / below-heap / static-sentinel guards make the walk safe
// for every element category this is routed for (single-word strings
// included). Without the retain the copy shared the old buffer's
// element pointers at unchanged rc; the old buffer's later walk-drop
// at rc==1 (__fern_drop_arr_str / __drop_arr_struct_<E> / deep struct
// drops) freed elements the grown copy still referenced — the #3425
// heap corruption. Mirrors __fern_arr_cow_inplace_ptr's element-retain
// loop (#4187), which fixed the same gap for `.with`.
//
// moveForm emits `__fern_arr_push_grow_move_ptr` instead: the same
// helper with the retain loop SKIPPED when the incoming rc is 1. That
// is the self-append form's contract (`a = a.append(v)`), whose
// overwrite reclaim is a buffer-only __fern_arr_dec: at rc==1 that dec
// frees the old buffer without walking, so the copy legitimately
// inherits its element references and a retain would leak one per
// element. At rc>1 the old buffer SURVIVES that dec — an alias still
// owns it — so the two buffers share every element pointer under a
// single count and both walk-drops release it, which is the #3457
// over-release. "The old buffer survives this grow" is exactly the
// rc != 1 test, so one helper covers both.
//
// System V inputs: rdi=arr, esi=oldLen, edx=stride. Returns new data
// pointer in rax.
func (g *generator) emitArrPushGrowPtrRuntime(moveForm bool) {
	name, lbl := "__fern_arr_push_grow_ptr", ".Lpushp"
	if moveForm {
		name, lbl = "__fern_arr_push_grow_move_ptr", ".Lpushmp"
	}
	g.line("")
	g.line(".globl " + name)
	g.line(".type " + name + ", @function")
	g.label(name)
	// Fast path: rc == 1 AND oldLen < cap → in place (rc = 2, len++).
	g.emit("mov eax, dword ptr [rdi - 8]") // rc
	g.emit("cmp eax, 1")
	g.emit("jne " + lbl + "_copy")
	g.emit("mov eax, dword ptr [rdi - 12]") // cap
	g.emit("cmp esi, eax")
	g.emit("jge " + lbl + "_copy")
	g.emit("mov dword ptr [rdi - 8], 2")
	g.emit("lea eax, [rsi + 1]")
	g.emit("mov dword ptr [rdi - 4], eax")
	g.emit("mov rax, rdi")
	g.emit("ret")
	g.label(lbl + "_copy")
	// Copy path — same register plan as __fern_arr_push_grow.
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("push r15")
	g.emit("sub rsp, 8")    // pad to 16-byte alignment
	g.emit("mov rbx, rdi")  // rbx = arr
	g.emit("mov r12d, esi") // r12d = oldLen
	g.emit("mov r13d, edx") // r13d = stride
	g.emit("mov r14d, esi")
	g.emit("add r14d, 1") // r14d = newLen = oldLen + 1
	// newCap = max(2 * newLen, 4)
	g.emit("mov r15d, r14d")
	g.emit("shl r15d, 1")
	g.emit("cmp r15d, 4")
	g.emit("jge " + lbl + "_cap_ok")
	g.emit("mov r15d, 4")
	g.label(lbl + "_cap_ok")
	// headerBytes = max(16, stride).
	g.emit("mov ecx, 16")
	g.emit("cmp r13d, 16")
	g.emit("jle " + lbl + "_hdr_set")
	g.emit("mov ecx, r13d")
	g.label(lbl + "_hdr_set")
	g.emit("push rcx")   // stash headerBytes
	g.emit("sub rsp, 8") // re-pad to 16 alignment
	// allocSize = headerBytes + newCap * stride.
	g.emit("mov eax, r15d")
	g.emit("imul eax, r13d")
	g.emit("add eax, ecx")
	g.emit("mov edi, eax")
	g.emit("call __fern_alloc")
	g.emit("mov rcx, qword ptr [rsp + 8]") // reload headerBytes
	g.emit("lea r11, [rax + rcx]")         // r11 = new_data
	g.emit("lea rdx, [rax + rcx - 12]")
	g.emit("mov dword ptr [rdx], r15d") // cap
	g.emit("lea rdx, [rax + rcx - 8]")
	g.emit("mov dword ptr [rdx], 1") // rc = 1
	g.emit("lea rdx, [rax + rcx - 4]")
	g.emit("mov dword ptr [rdx], r14d") // len = newLen
	// memcpy(new_data, arr, oldLen * stride)
	g.emit("mov rdi, r11")
	g.emit("mov rsi, rbx")
	g.emit("mov eax, r12d")
	g.emit("imul eax, r13d")
	g.emit("mov edx, eax")
	g.emit("mov qword ptr [rsp], r11") // stash new_data across the call
	g.emit("call __fern_memcpy")
	if moveForm {
		// The copy path leaves the OLD buffer's rc untouched, so rbx (still
		// arr here) reads the incoming count. rc==1 means the caller's
		// buffer-only __fern_arr_dec is about to free it and the elements
		// transfer; skip the retain. r14d (newLen) is dead — already stored.
		g.emit("mov r14d, dword ptr [rbx - 8]")
	}
	g.emit("mov rbx, qword ptr [rsp]") // rbx = new_data (survives rc_inc)
	if moveForm {
		g.emit("cmp r14d, 1")
		g.emit("je " + lbl + "_inc_done")
	}
	// Element-retain loop: inc each copied pointer element so the fresh
	// buffer owns its own reference. r12 = oldLen, r13 = stride survive
	// __fern_rc_inc (callee-saved). r15 = i.
	g.emit("xor r15, r15")
	g.label(lbl + "_inc_loop")
	g.emit("cmp r15d, r12d")
	g.emit("jge " + lbl + "_inc_done")
	g.emit("mov rax, r15")
	g.emit("imul rax, r13")
	g.emit("mov rdi, qword ptr [rbx + rax]") // element pointer (8-byte)
	g.emit("call __fern_rc_inc")             // guards null / SSO / low / sentinel
	g.emit("inc r15")
	g.emit("jmp " + lbl + "_inc_loop")
	g.label(lbl + "_inc_done")
	g.emit("mov rax, rbx") // return new_data
	// Tear down: three 8-byte slots above the callee-saves (prolog pad
	// + pushed headerBytes + inner pad).
	g.emit("add rsp, 24")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size " + name + ", .-" + name)
}

// emitArrCowInPlaceRuntime emits `__fern_arr_cow_inplace(arr,
// stride) -> buf` — System V counterpart of arm64's helper.
// Inputs: rdi=arr, esi=stride. Returns new data ptr in rax.
// See arm64.go's emitArrCowInPlaceRuntime + docs/RC-PERCEUS-PLAN.md
// for the contract (helper internalises rc bookkeeping so the
// IR-side emit doesn't have to coordinate with the
// __fern_rc_dec low-address guard).
func (g *generator) emitArrCowInPlaceRuntime() {
	g.line("")
	g.line(".globl __fern_arr_cow_inplace")
	g.line(".type __fern_arr_cow_inplace, @function")
	g.label("__fern_arr_cow_inplace")
	// Fast path: rc == 1 → return arr.
	g.emit("mov eax, dword ptr [rdi - 8]")
	g.emit("cmp eax, 1")
	g.emit("jne .Lcow_slow")
	g.emit("mov rax, rdi")
	g.emit("ret")
	g.label(".Lcow_slow")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("push r15")
	g.emit("sub rsp, 8")                     // pad to 16-byte align
	g.emit("mov rbx, rdi")                   // rbx = arr
	g.emit("mov r12d, esi")                  // r12d = stride
	g.emit("mov r13d, dword ptr [rdi - 4]")  // r13d = len
	g.emit("mov r14d, dword ptr [rdi - 12]") // r14d = cap
	// Decrement arr's rc (taking the caller's reference as we
	// copy). Skip when the rc word has its high bit set
	// (static-sentinel marker).
	g.emit("mov eax, dword ptr [rbx - 8]")
	g.emit("test eax, eax")
	g.emit("js .Lcow_skip_dec")
	g.emit("sub eax, 1")
	g.emit("mov dword ptr [rbx - 8], eax")
	g.label(".Lcow_skip_dec")
	// headerBytes = max(16, stride) in r15d.
	g.emit("mov r15d, 16")
	g.emit("cmp r12d, 16")
	g.emit("jle .Lcow_hdr_set")
	g.emit("mov r15d, r12d")
	g.label(".Lcow_hdr_set")
	// allocSize = headerBytes + cap * stride
	g.emit("mov eax, r14d")
	g.emit("imul eax, r12d")
	g.emit("add eax, r15d")
	g.emit("mov edi, eax")
	g.emit("call __fern_alloc")
	// rax = base. new_data = base + headerBytes (in r15d → rcx).
	g.emit("mov ecx, r15d")
	g.emit("lea r11, [rax + rcx]")
	// Store cap at [base + headerBytes - 12]
	g.emit("lea rdx, [rax + rcx - 12]")
	g.emit("mov dword ptr [rdx], r14d")
	// Store rc = 1 at [base + headerBytes - 8]
	g.emit("lea rdx, [rax + rcx - 8]")
	g.emit("mov dword ptr [rdx], 1")
	// Store len at [base + headerBytes - 4]
	g.emit("lea rdx, [rax + rcx - 4]")
	g.emit("mov dword ptr [rdx], r13d")
	// memcpy(new_data, arr, len * stride). Stash new_data in
	// the 8-byte pad slot so memcpy doesn't lose it.
	g.emit("mov rdi, r11")
	g.emit("mov rsi, rbx")
	g.emit("mov eax, r13d")
	g.emit("imul eax, r12d")
	g.emit("mov edx, eax")
	g.emit("mov qword ptr [rsp], r11")
	g.emit("call __fern_memcpy")
	g.emit("mov rax, qword ptr [rsp]")
	// Tear down. Mirror emitArrPushGrowRuntime: free the 8-byte
	// pad above r15 before popping callee-saves.
	g.emit("add rsp, 8")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_arr_cow_inplace, .-__fern_arr_cow_inplace")
}

// emitArrCowInPlacePtrRuntime emits `__fern_arr_cow_inplace_ptr(arr,
// stride) -> buf` — the pointer-element variant of
// __fern_arr_cow_inplace. Identical fast path (rc==1 → return arr
// unchanged, in-place mutation). On the COPY path (rc>1) it does the
// same alloc + memcpy, then walks the `len` elements and __fern_rc_inc's
// each so the fresh buffer independently OWNS them. The plain helper's
// raw memcpy leaves the copy sharing the receiver's element pointers at
// unchanged rc; dropping either array then frees elements the other
// still references (a use-after-free). stride is the pointer width, so
// every element is a single-word pointer loaded 8 bytes wide.
//
// System V inputs: rdi=arr, esi=stride. Returns new data ptr in rax.
func (g *generator) emitArrCowInPlacePtrRuntime() {
	g.line("")
	g.line(".globl __fern_arr_cow_inplace_ptr")
	g.line(".type __fern_arr_cow_inplace_ptr, @function")
	g.label("__fern_arr_cow_inplace_ptr")
	// Fast path: rc == 1 → return arr (in-place; elements already owned).
	g.emit("mov eax, dword ptr [rdi - 8]")
	g.emit("cmp eax, 1")
	g.emit("jne .Lcowp_slow")
	g.emit("mov rax, rdi")
	g.emit("ret")
	g.label(".Lcowp_slow")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("push r15")
	g.emit("sub rsp, 8")                     // pad to 16-byte align
	g.emit("mov rbx, rdi")                   // rbx = arr
	g.emit("mov r12d, esi")                  // r12d = stride
	g.emit("mov r13d, dword ptr [rdi - 4]")  // r13d = len
	g.emit("mov r14d, dword ptr [rdi - 12]") // r14d = cap
	// Decrement arr's rc (taking the caller's reference as we copy).
	// Skip when the rc word has its high bit set (static sentinel).
	g.emit("mov eax, dword ptr [rbx - 8]")
	g.emit("test eax, eax")
	g.emit("js .Lcowp_skip_dec")
	g.emit("sub eax, 1")
	g.emit("mov dword ptr [rbx - 8], eax")
	g.label(".Lcowp_skip_dec")
	// headerBytes = max(16, stride) in r15d.
	g.emit("mov r15d, 16")
	g.emit("cmp r12d, 16")
	g.emit("jle .Lcowp_hdr_set")
	g.emit("mov r15d, r12d")
	g.label(".Lcowp_hdr_set")
	// allocSize = headerBytes + cap * stride
	g.emit("mov eax, r14d")
	g.emit("imul eax, r12d")
	g.emit("add eax, r15d")
	g.emit("mov edi, eax")
	g.emit("call __fern_alloc")
	// rax = base. new_data = base + headerBytes (in r15d → rcx).
	g.emit("mov ecx, r15d")
	g.emit("lea r11, [rax + rcx]")
	// Store cap at [base + headerBytes - 12]
	g.emit("lea rdx, [rax + rcx - 12]")
	g.emit("mov dword ptr [rdx], r14d")
	// Store rc = 1 at [base + headerBytes - 8]
	g.emit("lea rdx, [rax + rcx - 8]")
	g.emit("mov dword ptr [rdx], 1")
	// Store len at [base + headerBytes - 4]
	g.emit("lea rdx, [rax + rcx - 4]")
	g.emit("mov dword ptr [rdx], r13d")
	// memcpy(new_data, arr, len * stride). Stash new_data in the pad.
	g.emit("mov rdi, r11")
	g.emit("mov rsi, rbx")
	g.emit("mov eax, r13d")
	g.emit("imul eax, r12d")
	g.emit("mov edx, eax")
	g.emit("mov qword ptr [rsp], r11")
	g.emit("call __fern_memcpy")
	g.emit("mov rbx, qword ptr [rsp]") // rbx = new_data (survives rc_inc)
	// Element-retain loop: inc each copied pointer element so the fresh
	// buffer owns its own reference. r12 = stride, r13 = len both survive
	// __fern_rc_inc (callee-saved). r15 = i.
	g.emit("xor r15, r15")
	g.label(".Lcowp_inc_loop")
	g.emit("cmp r15d, r13d")
	g.emit("jge .Lcowp_inc_done")
	g.emit("mov rax, r15")
	g.emit("imul rax, r12")
	g.emit("mov rdi, qword ptr [rbx + rax]") // element pointer (8-byte)
	g.emit("call __fern_rc_inc")             // guards null / low / sentinel
	g.emit("inc r15")
	g.emit("jmp .Lcowp_inc_loop")
	g.label(".Lcowp_inc_done")
	g.emit("mov rax, rbx") // return new_data
	// Tear down (mirror the pad free before popping callee-saves).
	g.emit("add rsp, 8")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_arr_cow_inplace_ptr, .-__fern_arr_cow_inplace_ptr")
}

// emitDropArrPtrRuntime emits `__fern_drop_arr_ptr(ptr, stride)
// -> ptr` — Phase 3 drop handler for an array whose elements are
// pointer-shaped rc-tracked values. Mirrors the wasm
// buildDropArrPtrBody: NULL + low-address + static-sentinel
// guards, then on the LAST reference (rc == 1) walk the `len`
// elements and dec each via __fern_rc_dec before dec'ing the
// array itself. Returns the input ptr (matching __fern_rc_dec's
// contract).
//
// System V inputs: rdi = ptr, rsi = stride. Live values kept in
// callee-saved regs across the __fern_rc_dec calls: rbx = ptr,
// r12 = len, r13 = i, r14 = stride.
func (g *generator) emitDropArrPtrRuntime() {
	g.line("")
	g.line(".globl __fern_drop_arr_ptr")
	g.line(".type __fern_drop_arr_ptr, @function")
	g.label("__fern_drop_arr_ptr")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	// rsp is now 16-aligned at the call sites below.
	g.emit("mov rbx, rdi") // rbx = ptr
	g.emit("mov r14, rsi") // r14 = stride
	// NULL guard.
	g.emit("test rbx, rbx")
	g.emit("jz .Ldrop_ret_ptr")
	// Low-address guard — mirror __fern_rc_dec. The exit dec sweep
	// can visit array-typed slots that actually hold a non-pointer
	// (an enum tag, a small i32, stack garbage from a never-taken
	// branch's Var decl). Reading [ptr-8] / [ptr-4] on such a value
	// would fault; treat the low 64 KiB as "not a heap object".
	g.emit("cmp rbx, 0x10000")
	g.emit("jb .Ldrop_ret_ptr")
	// Static-sentinel guard: high bit of rc word set ⇒ never recurse.
	g.emit("mov ecx, dword ptr [rbx - 8]")
	g.emit("test ecx, ecx")
	g.emit("js .Ldrop_ret_ptr")
	// Only the last reference walks elements (rc == 1).
	g.emit("cmp ecx, 1")
	g.emit("jne .Ldrop_decarr")
	g.emit("mov r12d, dword ptr [rbx - 4]") // r12 = len
	g.emit("xor r13, r13")                  // i = 0
	g.label(".Ldrop_loop")
	g.emit("cmp r13, r12")
	g.emit("jge .Ldrop_decarr")
	// rdi = mem[ptr + i*stride] (8-byte element load).
	g.emit("mov rax, r13")
	g.emit("imul rax, r14")
	g.emit("mov rdi, qword ptr [rbx + rax]")
	g.emit("call __fern_rc_dec")
	g.emit("inc r13")
	g.emit("jmp .Ldrop_loop")
	g.label(".Ldrop_decarr")
	if ast.RcFreeEnabled {
		// Phase 3 step-4: on the last reference (rc==1) the array's
		// elements have been dec'd above, so return the buffer to
		// the freelist instead of just dec'ing to 0. base = data -
		// headerBytes; headerBytes = max(16, stride); size =
		// headerBytes + cap*stride (cap at data-12). rc reloaded —
		// the element-walk's __fern_rc_dec calls clobbered ecx.
		g.emit("mov ecx, dword ptr [rbx - 8]")
		g.emit("cmp ecx, 1")
		g.emit("jne .Ldrop_plaindec")
		g.quarantine("rbx")
		g.emit("mov r8, r14") // stride
		g.emit("cmp r8, 16")
		g.emit("jae .Ldrop_hdr")
		g.emit("mov r8, 16")
		g.label(".Ldrop_hdr")
		g.emit("mov ecx, dword ptr [rbx - 12]") // cap (zero-extended)
		g.emit("mov rax, rcx")
		g.emit("imul rax, r14") // cap * stride
		g.emit("add rax, r8")   // + headerBytes = size
		g.emit("mov rsi, rax")  // arg2 = size
		g.emit("mov rdi, rbx")
		g.emit("sub rdi, r8") // arg1 = base = data - headerBytes
		g.emit("call __fern_free")
		g.emit("mov rax, rbx") // return ptr (matches contract)
		g.emit("jmp .Ldrop_done")
		g.label(".Ldrop_plaindec")
	}
	// Dec the array itself; __fern_rc_dec returns the ptr in rax.
	g.emit("mov rdi, rbx")
	g.emit("call __fern_rc_dec")
	g.emit("jmp .Ldrop_done")
	g.label(".Ldrop_ret_ptr")
	g.emit("mov rax, rbx")
	g.label(".Ldrop_done")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_drop_arr_ptr, .-__fern_drop_arr_ptr")
}

// emitDropArrStrRuntime emits `__fern_drop_arr_str(ptr, stride) -> ptr`
// — the native single-word (x86-64) string[] drop handler. Identical to
// __fern_drop_arr_ptr except each element is reclaimed via __fern_str_dec
// (free the heap-string buffer at the element's rc==1) instead of the
// plain __fern_rc_dec; then the array buffer itself is returned to the
// freelist. The elements were retained on store (the same inc that
// __fern_drop_arr_ptr's per-element rc_dec already balanced), so freeing
// them here is balanced; inline-SSO / literal / sentinel / shared
// elements short-circuit inside __fern_str_dec. The two-word ABIs have
// their own __fern_drop_arr_str; this is the single-word sibling.
//
// System V: rdi = ptr, rsi = stride. Returns ptr in rax.
func (g *generator) emitDropArrStrRuntime() {
	g.line("")
	g.line(".globl __fern_drop_arr_str")
	g.line(".type __fern_drop_arr_str, @function")
	g.label("__fern_drop_arr_str")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("push r13")
	g.emit("push r14")
	g.emit("mov rbx, rdi") // rbx = ptr
	g.emit("mov r14, rsi") // r14 = stride
	g.emit("test rbx, rbx")
	g.emit("jz .Ldrops_ret")
	g.emit("cmp rbx, 0x10000")
	g.emit("jb .Ldrops_ret")
	g.emit("mov ecx, dword ptr [rbx - 8]")
	g.emit("test ecx, ecx")
	g.emit("js .Ldrops_ret") // static sentinel
	g.emit("cmp ecx, 1")
	g.emit("jne .Ldrops_decarr")            // shared array → no element walk
	g.emit("mov r12d, dword ptr [rbx - 4]") // len
	g.emit("xor r13, r13")                  // i = 0
	g.label(".Ldrops_loop")
	g.emit("cmp r13, r12")
	g.emit("jge .Ldrops_decarr")
	g.emit("mov rax, r13")
	g.emit("imul rax, r14")
	g.emit("mov rdi, qword ptr [rbx + rax]") // element i (a string ptr)
	g.emit("call __fern_str_dec")            // free the element's buffer at its rc==1
	g.emit("inc r13")
	g.emit("jmp .Ldrops_loop")
	g.label(".Ldrops_decarr")
	if ast.RcFreeEnabled {
		g.emit("mov ecx, dword ptr [rbx - 8]")
		g.emit("cmp ecx, 1")
		g.emit("jne .Ldrops_plaindec")
		g.quarantine("rbx")
		g.emit("mov r8, r14")
		g.emit("cmp r8, 16")
		g.emit("jae .Ldrops_hdr")
		g.emit("mov r8, 16")
		g.label(".Ldrops_hdr")
		g.emit("mov ecx, dword ptr [rbx - 12]") // cap
		g.emit("mov rax, rcx")
		g.emit("imul rax, r14")
		g.emit("add rax, r8")
		g.emit("mov rsi, rax")
		g.emit("mov rdi, rbx")
		g.emit("sub rdi, r8")
		g.emit("call __fern_free")
		g.emit("mov rax, rbx")
		g.emit("jmp .Ldrops_done")
		g.label(".Ldrops_plaindec")
	}
	g.emit("mov rdi, rbx")
	g.emit("call __fern_rc_dec")
	g.emit("jmp .Ldrops_done")
	g.label(".Ldrops_ret")
	g.emit("mov rax, rbx")
	g.label(".Ldrops_done")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_drop_arr_str, .-__fern_drop_arr_str")
}

// emitRcIsUniqueRuntime emits `__fern_rc_is_unique(ptr) -> i32` —
// returns 1 iff ptr is a real, uniquely-owned heap value
// (non-null, above the low-address guard, not a static sentinel,
// rc == 1); else 0. Same guard chain as __fern_rc_dec, so it is
// safe on a slot that might hold a non-pointer scalar. Used by the
// Phase 3 struct drop to gate recursive field decs on "this is the
// last reference". Leaf (no calls), so no frame.
func (g *generator) emitRcIsUniqueRuntime() {
	g.line("")
	g.line(".globl __fern_rc_is_unique")
	g.line(".type __fern_rc_is_unique, @function")
	g.label("__fern_rc_is_unique")
	g.emit("xor eax, eax")
	g.emit("test rdi, rdi")
	g.emit("jz .Lisuniq_ret")
	g.emit("cmp rdi, 0x10000")
	g.emit("jb .Lisuniq_ret")
	g.emit("mov ecx, dword ptr [rdi - 8]")
	g.emit("test ecx, ecx")
	g.emit("js .Lisuniq_ret") // bit 31 set ⇒ static sentinel
	g.emit("cmp ecx, 1")
	g.emit("jne .Lisuniq_ret")
	g.emit("mov eax, 1")
	g.label(".Lisuniq_ret")
	g.emit("ret")
	g.line(".size __fern_rc_is_unique, .-__fern_rc_is_unique")
}

// emitStrcatRuntime emits `__fern_strcat(a, b)` — concat two
// length-prefixed strings into a fresh allocation. a / b
// are data pointers (post-prefix); 4-byte length lives at
// `[ptr - 4]`. Returns the new data pointer.
//
// Uses callee-save r12..r15 to keep state across the calls
// to __fern_alloc and __fern_memcpy.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".globl __fern_strcat")
	g.line(".type __fern_strcat, @function")
	g.label("__fern_strcat")
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
	g.emit("call __fern_memcpy")
	// memcpy([rbp - 63 + la], b_data, lb).
	g.emit("lea rdi, [rbp - 63]")
	g.emit("add rdi, r14")
	g.emit("mov rsi, r13")
	g.emit("mov rdx, r15")
	g.emit("call __fern_memcpy")
	// Load the full 8-byte inline value (length byte + 7 data
	// bytes + zero padding) into rax.
	g.emit("mov rax, [rbp - 64]")
	g.emit("jmp .Lstrcat_ret")
	g.label(".Lstrcat_heap")
	// --- Heap output path (L2 rc-header layout) ---
	// Layout: [base+0 rc][base+4 length (shares rc1's payload-size slot)]
	// [base+8.. data .. base+8+la+lb-1][base+8+la+lb NUL]. data = base+8.
	// Payload requested from alloc_rc1 = la+lb+1 (data + NUL); length and
	// rc share the 8-byte header (length lands at data-4 = base+4 via
	// emitStrLenStore, clobbering rc1's stashed payload-size slot — fine
	// for strings since the eventual string-drop computes alloc size from
	// length, not from data-4). RC-STRINGS-PLAN.md prereq 1.
	g.emit("lea rdi, [r14 + r15 + 1]")
	g.emit("call __fern_alloc_rc1")
	// rax = data (base+8). Stash in rbx (callee-save) so it survives both
	// __fern_memcpy calls, then return it at the end.
	g.emit("mov rbx, rax")
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
	g.emit("call __fern_memcpy")
	// memcpy(dst + la, b, lb)
	g.emit("lea rdi, [rbx + r14]")
	g.emit("mov rsi, r13")
	g.emit("mov rdx, r15")
	g.emit("call __fern_memcpy")
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
	g.line(".size __fern_strcat, .-__fern_strcat")
}

// emitStrAppendRuntime emits `__fern_str_append(a, b) -> data` — the
// in-place-when-unique string self-append behind `s = s + piece` (#5637).
// It CONSUMES `a` (the IR only emits it where the assignment was about to
// overwrite and reclaim that slot, so its dec-on-overwrite is suppressed):
//
//   - Fast path — `a` is a uniquely-held heap buffer (LSB clear, at/above
//     the heap base, rc==1) whose grown length still lands in the SAME
//     allocator size class: memcpy b's bytes into the slack past a's data,
//     restamp the length prefix + trailing NUL, and hand the same buffer
//     back. No allocation, no re-copy of the accumulated prefix.
//   - Slow path — anything else (inline SSO / literal / shared / the class
//     boundary crossed): plain __fern_strcat, then __fern_str_dec(a) to end
//     the old binding. Note __fern_str_dec, not the __fern_rc_dec the
//     suppressed overwrite used: rc_dec decrements without freeing, so the
//     accumulator's intermediates were leaked outright on this ABI. Freeing
//     is the same authorisation the exit sweep already has for these
//     locals (freeEligible, which the IR checks before emitting the call).
//
// Same-class is the exact capacity test rather than a heuristic: every heap
// string is __fern_alloc_rc1(len + 1) (data + NUL) and __fern_str_dec frees
// it at the CURRENT len, so a growth that keeps `(len + 9 + 15) & -16`
// unchanged both fits the block and still frees back to the class it was
// bumped at. This is the same rounded-size class match __fern_alloc_reuse
// uses, so alloc / free / reuse / append all agree by construction. (The IR
// only emits calls here under ast.RcFreeEnabled, which is also what makes
// the trailing __fern_str_dec a real reclaim rather than a bare decrement.)
//
// The slack is the allocator's 16-byte granularity, so an accumulator
// absorbs ~8-16 short appends per allocation instead of one allocation and
// a full re-copy each. It is NOT amortised growth — there is no capacity
// slot in the 8-byte [rc][len] header to hold one — so a long accumulator
// still re-copies once per class step; the geometric fix is the string
// builder of #5637 option 2.
//
// System V: rdi = a, rsi = b. Returns the data pointer in rax.
func (g *generator) emitStrAppendRuntime() {
	g.line("")
	g.line(".globl __fern_str_append")
	g.line(".type __fern_str_append, @function")
	g.label("__fern_str_append")
	// Frame: rbp + rbx + r12 saves, then 16 bytes of scratch —
	//   [rbp - 24]: emitStrDataPtr spill slot for an inline `b`
	//   [rbp - 32]: total length, held across the __fern_memcpy call
	// 8 (ret) + 8 (rbp) + 16 (saves) + 16 (scratch) = 48, so rsp is
	// 16-aligned at every call below.
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("sub rsp, 16")
	g.emit("mov rbx, rdi") // rbx = a
	g.emit("mov r12, rsi") // r12 = b
	// --- in-place eligibility on `a` (mirrors __fern_str_dec's guards) ---
	g.emit("test dil, 1") // inline SSO packed value → not a heap buffer
	g.emit("jnz .Lstrapp_copy")
	g.emit("cmp rdi, 0x10000000") // below the heap base → .rodata literal
	g.emit("jb .Lstrapp_copy")
	g.emit("mov eax, dword ptr [rdi - 8]") // rc
	g.emit("cmp eax, 1")
	g.emit("jne .Lstrapp_copy") // shared, sentinel, or already released
	g.emitStrLen("rcx", "rdi")  // la
	g.emitStrLen("rdx", "rsi")  // lb (b may still be inline)
	g.emit("mov r8d, ecx")
	g.emit("add r8d, edx") // total = la + lb
	// Same size class? class(len) = (len + 1 + 8 + 15) & -16.
	g.emit("lea r9d, [rcx + 24]")
	g.emit("and r9d, -16")
	g.emit("lea r10d, [r8 + 24]")
	g.emit("and r10d, -16")
	g.emit("cmp r9d, r10d")
	g.emit("jne .Lstrapp_copy")
	// --- in place: memcpy(a + la, b_data, lb) ---
	g.emit("mov [rbp - 32], r8") // total survives the call
	g.emitStrDataPtr("rsi", "r12", "[rbp - 24]")
	g.emit("lea rdi, [rbx + rcx]") // dst = a + la; rdx already holds lb
	g.emit("call __fern_memcpy")
	g.emit("mov r8, [rbp - 32]")
	g.emitStrLenStore("r8d", "rbx") // [a - 4] = total
	g.emit("lea rdi, [rbx + r8]")
	g.emit("mov byte ptr [rdi], 0") // trailing NUL
	g.emit("mov rax, rbx")
	g.emit("jmp .Lstrapp_ret")
	g.label(".Lstrapp_copy")
	g.emit("mov rdi, rbx")
	g.emit("mov rsi, r12")
	g.emit("call __fern_strcat")
	g.emit("mov r12, rax") // out
	g.emit("mov rdi, rbx")
	g.emit("call __fern_str_dec") // release the consumed accumulator
	g.emit("mov rax, r12")
	g.label(".Lstrapp_ret")
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_str_append, .-__fern_str_append")
}

// emitStrcmpRuntime emits `__fern_strcmp(a, b)` — returns 0
// if a and b have the same length AND the same bytes, else 1.
// Pure equality comparator (no lex ordering), matching arm64.
// emitFloatTranscendentalsRuntime emits the f64 transcendental bundle —
// __fern_{exp,log,sin,cos,pow}_f64 — plus the shared .rodata table of
// coefficients. x86-64 has no usable hardware transcendental (the x87
// fsin / fyl2x / f2xm1 these replace are microcoded legacy from before
// SSE), so each is an argument reduction followed by a polynomial.
//
// The kernels are fdlibm's, and the accuracy that buys is the point:
// measured against the correctly-rounded reference over 20k samples per
// range, these are <= 1 ulp everywhere, where the Taylor kernels arm64
// and wasm have carried are 3.2e10 ulp (sin), 4.5e7 (exp) and 9844
// (log). "A few ulp" was never true of the old ones.
//
// Two choices worth recording:
//
//   - The reduction is Cody-Waite: the constant is split into a head
//     with its low mantissa bits zeroed plus a tail, so `x - k*hi` is
//     EXACT and only the much smaller `k*tail` rounds. Reducing against
//     a single rounded pi/2 is what made the old sin lose ~7 digits by
//     |x| ~ 10.
//   - exp keeps fdlibm's division. The division-free alternative needs a
//     degree-13 Taylor to reach 1 ulp (degree 11 lands at 55 ulp), whose
//     dependent chain is longer than the divide it avoids.
//
// Convention: argument and result in xmm0 (pow also takes y in xmm1),
// mirroring arm64's d0/d1. All scratch is caller-saved under SysV.
func (g *generator) emitFloatTranscendentalsRuntime() {
	ldc := func(reg, lbl string) { g.emit("movsd " + reg + ", [rip+" + lbl + "]") }
	// mul by a coefficient, then add the next one down: one Horner step.
	horner := func(acc, x, lbl string) {
		g.emit("mulsd " + acc + ", " + x)
		g.emit("addsd " + acc + ", [rip+" + lbl + "]")
	}
	fn := func(name string) {
		g.line("")
		g.line(".globl " + name)
		g.line(".type " + name + ", @function")
		g.label(name)
	}

	g.line("")
	g.line(".section .rodata")
	g.line(".align 8")
	for _, c := range []struct{ lbl, val string }{
		{".Lfc_one", "1.0"},
		{".Lfc_half", "0.5"},
		{".Lfc_two", "2.0"},
		{".Lfc_sqrt2", "1.4142135623730951"},
		// Cody-Waite splits. ln2_hi / pio2_1 carry zeroed low mantissa
		// bits so the first subtraction is exact.
		{".Lfc_invln2", "1.44269504088896338700e+00"},
		{".Lfc_ln2hi", "6.93147180369123816490e-01"},
		{".Lfc_ln2lo", "1.90821492927058770002e-10"},
		{".Lfc_2opi", "6.36619772367581382433e-01"},
		// pi/2 as THREE 33-bit chunks (~99 bits). Two chunks leave sin(pi)
		// 285k ulp out: near a zero of sin the reduced argument IS the
		// answer, so the reduction's absolute error becomes the result's
		// relative error. A fourth chunk makes it worse, not better — it
		// perturbs the cancellation the third one sets up.
		{".Lfc_pio2h", "1.57079632673412561417e+00"},
		{".Lfc_pio2m", "6.07710050630396597660e-11"},
		{".Lfc_pio2l", "2.02226624879595063154e-21"},
		// sin kernel, |r| <= pi/4
		{".Lfc_s1", "-1.66666666666666324348e-01"},
		{".Lfc_s2", "8.33333333332248946124e-03"},
		{".Lfc_s3", "-1.98412698298579493134e-04"},
		{".Lfc_s4", "2.75573137070700676789e-06"},
		{".Lfc_s5", "-2.50507602534068634195e-08"},
		{".Lfc_s6", "1.58969099521155010221e-10"},
		// cos kernel, |r| <= pi/4
		{".Lfc_c1", "4.16666666666666019037e-02"},
		{".Lfc_c2", "-1.38888888888741095749e-03"},
		{".Lfc_c3", "2.48015872894767294178e-05"},
		{".Lfc_c4", "-2.75573143513906633035e-07"},
		{".Lfc_c5", "2.08757232129817482790e-09"},
		{".Lfc_c6", "-1.13596475577881948265e-11"},
		// exp kernel
		{".Lfc_p1", "1.66666666666666019037e-01"},
		{".Lfc_p2", "-2.77777777770155933842e-03"},
		{".Lfc_p3", "6.61375632143793436117e-05"},
		{".Lfc_p4", "-1.65339022054652515390e-06"},
		{".Lfc_p5", "4.13813679705723846039e-08"},
		// log kernel
		{".Lfc_lg1", "6.666666666666735130e-01"},
		{".Lfc_lg2", "3.999999999940941908e-01"},
		{".Lfc_lg3", "2.857142874366239149e-01"},
		{".Lfc_lg4", "2.222219843214978396e-01"},
		{".Lfc_lg5", "1.818357216161805012e-01"},
		{".Lfc_lg6", "1.531383769920937332e-01"},
		{".Lfc_lg7", "1.479819860511658591e-01"},
		// exp's finite range. Above the first, e^x is not representable;
		// below the second it rounds to zero. Both are needed BEFORE the
		// 2^k reconstruction, which builds the exponent field as
		// (k+1023)<<52 and silently overflows into the SIGN bit otherwise
		// — exp(1000) came out as -6.1e-183 rather than +Inf.
		{".Lfc_expovf", "709.782712893383973096"},
		{".Lfc_expunf", "-745.133219101941108420"},
	} {
		g.label(c.lbl)
		g.line("\t.double " + c.val)
	}
	g.line(".text")

	// retNaN / retInf / retZero leave the named value in xmm0 and return.
	// Built from bit patterns rather than .rodata: an assembler `.double`
	// has no spelling for infinity.
	retBits := func(bits string) {
		g.emit("movabs rax, " + bits)
		g.emit("movq xmm0, rax")
		g.emit("ret")
	}
	// nanGuard emits "if x is NaN, return it unchanged". NaN is the one
	// value where a compare is UNORDERED, which x86 reports in the parity
	// flag — ZF alone cannot distinguish it from equality.
	nanGuard := func(lbl string) {
		g.emit("ucomisd xmm0, xmm0")
		g.emit("jp " + lbl)
	}
	// trigGuard: NaN returns itself; ±Inf becomes NaN, matching the
	// reference. There is no meaningful reduction of an infinite argument —
	// x*2/pi is Inf, and the quadrant falls out as garbage.
	trigGuard := func() {
		ret, nan := g.freshLabel("trigRet"), g.freshLabel("trigNaN")
		nanGuard(ret)
		g.emit("movq rax, xmm0")
		g.emit("movabs rcx, 0x7fffffffffffffff")
		g.emit("and rax, rcx")
		g.emit("movabs rcx, 0x7ff0000000000000")
		g.emit("cmp rax, rcx")
		g.emit("jae " + nan) // exponent all ones, mantissa 0 → ±Inf
		done := g.freshLabel("trigOk")
		g.emit("jmp " + done)
		g.label(ret)
		g.emit("ret")
		g.label(nan)
		retBits("0x7ff8000000000000")
		g.label(done)
	}

	// __fern_ksin(xmm0=r, |r| <= pi/4) → sin r. Internal, not exported:
	// sin and cos both reach it after reduction, so the kernel exists
	// once rather than once per quadrant arm.
	//   z = r*r; v = z*r; sin = r + v*(S1 + z*(S2+z*(S3+z*(S4+z*(S5+z*S6)))))
	g.line("")
	g.label("__fern_ksin")
	g.emit("movsd xmm1, xmm0")
	g.emit("mulsd xmm1, xmm0") // z
	g.emit("movsd xmm2, xmm1")
	g.emit("mulsd xmm2, xmm0") // v = z*r
	ldc("xmm3", ".Lfc_s6")
	horner("xmm3", "xmm1", ".Lfc_s5")
	horner("xmm3", "xmm1", ".Lfc_s4")
	horner("xmm3", "xmm1", ".Lfc_s3")
	horner("xmm3", "xmm1", ".Lfc_s2")
	horner("xmm3", "xmm1", ".Lfc_s1")
	g.emit("mulsd xmm3, xmm2")
	g.emit("addsd xmm0, xmm3")
	g.emit("ret")

	// __fern_kcos(xmm0=r, |r| <= pi/4) → cos r.
	//   z = r*r; p = C1+z*(C2+…+z*C6); hz = z/2; w = 1-hz
	//   cos = w + (((1-w) - hz) + z*(z*p))
	// The (1-w)-hz dance recovers the bits 1-hz threw away; computing
	// 1 - hz + z*z*p directly loses them and costs ~2 ulp.
	g.line("")
	g.label("__fern_kcos")
	g.emit("movsd xmm1, xmm0")
	g.emit("mulsd xmm1, xmm0") // z
	ldc("xmm3", ".Lfc_c6")
	horner("xmm3", "xmm1", ".Lfc_c5")
	horner("xmm3", "xmm1", ".Lfc_c4")
	horner("xmm3", "xmm1", ".Lfc_c3")
	horner("xmm3", "xmm1", ".Lfc_c2")
	horner("xmm3", "xmm1", ".Lfc_c1")
	g.emit("mulsd xmm3, xmm1") // z*p
	g.emit("mulsd xmm3, xmm1") // z*(z*p)
	g.emit("movsd xmm4, xmm1")
	g.emit("mulsd xmm4, [rip+.Lfc_half]") // hz
	ldc("xmm5", ".Lfc_one")
	g.emit("subsd xmm5, xmm4") // w = 1-hz
	ldc("xmm6", ".Lfc_one")
	g.emit("subsd xmm6, xmm5") // 1-w
	g.emit("subsd xmm6, xmm4") // (1-w)-hz
	g.emit("addsd xmm6, xmm3") // + z*z*p
	g.emit("movsd xmm0, xmm5")
	g.emit("addsd xmm0, xmm6")
	g.emit("ret")

	// emitPio2Reduce: xmm0 = x → rax = quadrant (k&3), xmm0 = r.
	pio2Reduce := func() {
		ldc("xmm1", ".Lfc_2opi")
		g.emit("mulsd xmm1, xmm0")
		g.emit("roundsd xmm1, xmm1, 0") // k, to nearest
		g.emit("cvttsd2si rax, xmm1")
		g.emit("movsd xmm2, xmm1")
		g.emit("mulsd xmm2, [rip+.Lfc_pio2h]")
		g.emit("subsd xmm0, xmm2") // exact
		g.emit("movsd xmm2, xmm1")
		g.emit("mulsd xmm2, [rip+.Lfc_pio2m]")
		g.emit("subsd xmm0, xmm2")
		g.emit("mulsd xmm1, [rip+.Lfc_pio2l]")
		g.emit("subsd xmm0, xmm1") // r
		g.emit("and rax, 3")
	}

	// __fern_sin_f64: quadrant 0..3 → sin r, cos r, −sin r, −cos r.
	// Odd quadrant picks the cos kernel, quadrant >= 2 flips the sign —
	// so exactly one kernel runs, where the old code evaluated both and
	// selected.
	fn("__fern_sin_f64")
	trigGuard()
	pio2Reduce()
	sinCos, sinNeg := g.freshLabel("sinUseCos"), g.freshLabel("sinNeg")
	g.emit("test rax, 1")
	g.emit("jnz " + sinCos)
	g.emit("call __fern_ksin")
	g.emit("jmp " + sinNeg)
	g.label(sinCos)
	g.emit("call __fern_kcos")
	g.label(sinNeg)
	sinDone := g.freshLabel("sinDone")
	g.emit("cmp rax, 2")
	g.emit("jb " + sinDone)
	g.emitF64Negate("xmm0")
	g.label(sinDone)
	g.emit("ret")
	g.line(".size __fern_sin_f64, .-__fern_sin_f64")

	// __fern_cos_f64: quadrant 0..3 → cos r, −sin r, −cos r, sin r.
	// Even quadrant picks the cos kernel; quadrants 1 and 2 flip sign.
	fn("__fern_cos_f64")
	trigGuard()
	pio2Reduce()
	cosSin, cosChk := g.freshLabel("cosUseSin"), g.freshLabel("cosChk")
	g.emit("test rax, 1")
	g.emit("jnz " + cosSin)
	g.emit("call __fern_kcos")
	g.emit("jmp " + cosChk)
	g.label(cosSin)
	g.emit("call __fern_ksin")
	g.label(cosChk)
	cosDone := g.freshLabel("cosDone")
	g.emit("cmp rax, 1")
	g.emit("jb " + cosDone) // q0 → +cos
	g.emit("cmp rax, 2")
	g.emit("ja " + cosDone) // q3 → +sin
	g.emitF64Negate("xmm0")
	g.label(cosDone)
	g.emit("ret")
	g.line(".size __fern_cos_f64, .-__fern_cos_f64")

	// __fern_exp_f64(xmm0=x) → e^x.
	//   k = round(x/ln2); hi = x - k*ln2_hi; lo = k*ln2_lo; r = hi - lo
	//   c = r - t*(P1+t*(P2+…)), t = r*r
	//   e^r = 1 - ((lo - (r*c)/(2-c)) - hi);  e^x = e^r * 2^k
	fn("__fern_exp_f64")
	expRet, expInf, expZero := g.freshLabel("expRet"), g.freshLabel("expInf"), g.freshLabel("expZero")
	// Domain guards. Without them exp(1000) overflowed the exponent field
	// into the sign bit and returned -6.1e-183, and exp(±Inf) fell through
	// the polynomial as NaN. +Inf trips the overflow branch and -Inf the
	// underflow one, so only NaN needs testing separately.
	nanGuard(expRet)
	g.emit("ucomisd xmm0, [rip+.Lfc_expovf]")
	g.emit("ja " + expInf)
	g.emit("ucomisd xmm0, [rip+.Lfc_expunf]")
	g.emit("jb " + expZero)
	ldc("xmm1", ".Lfc_invln2")
	g.emit("mulsd xmm1, xmm0")
	g.emit("roundsd xmm1, xmm1, 0")
	g.emit("cvttsd2si rax, xmm1") // k
	g.emit("movsd xmm2, xmm1")
	g.emit("mulsd xmm2, [rip+.Lfc_ln2hi]")
	g.emit("movsd xmm3, xmm0")
	g.emit("subsd xmm3, xmm2")             // hi
	g.emit("mulsd xmm1, [rip+.Lfc_ln2lo]") // lo
	g.emit("movsd xmm0, xmm3")
	g.emit("subsd xmm0, xmm1") // r
	g.emit("movsd xmm4, xmm0")
	g.emit("mulsd xmm4, xmm0") // t
	ldc("xmm5", ".Lfc_p5")
	horner("xmm5", "xmm4", ".Lfc_p4")
	horner("xmm5", "xmm4", ".Lfc_p3")
	horner("xmm5", "xmm4", ".Lfc_p2")
	horner("xmm5", "xmm4", ".Lfc_p1")
	g.emit("mulsd xmm5, xmm4") // t*(…)
	g.emit("movsd xmm6, xmm0")
	g.emit("subsd xmm6, xmm5") // c
	g.emit("movsd xmm7, xmm0")
	g.emit("mulsd xmm7, xmm6") // r*c
	ldc("xmm2", ".Lfc_two")
	g.emit("subsd xmm2, xmm6") // 2-c
	g.emit("divsd xmm7, xmm2")
	g.emit("movsd xmm2, xmm1")
	g.emit("subsd xmm2, xmm7") // lo - …
	g.emit("subsd xmm2, xmm3") // - hi
	ldc("xmm0", ".Lfc_one")
	g.emit("subsd xmm0, xmm2")
	// 2^k by assembling the exponent field directly.
	g.emit("add rax, 1023")
	g.emit("shl rax, 52")
	g.emit("movq xmm1, rax")
	g.emit("mulsd xmm0, xmm1")
	g.label(expRet)
	g.emit("ret")
	g.label(expInf)
	retBits("0x7ff0000000000000")
	g.label(expZero)
	g.emit("xorpd xmm0, xmm0")
	g.emit("ret")
	g.line(".size __fern_exp_f64, .-__fern_exp_f64")

	// __fern_log_f64(xmm0=x) → ln x (x>0). x = 2^k·m, m normalised to
	// [sqrt2/2, sqrt2); f = m-1; s = f/(2+f).
	//   R = t1+t2 over two INDEPENDENT chains in w = z², z = s², so they
	//   issue in parallel instead of one 7-deep Horner.
	//   ln x = k·ln2_hi - ((hfsq - (s·(hfsq+R) + k·ln2_lo)) - f)
	fn("__fern_log_f64")
	logRet, logNaN, logNegInf := g.freshLabel("logRet"), g.freshLabel("logNaN"), g.freshLabel("logNegInf")
	// Domain guards. The bit-twiddling below happily extracts an exponent
	// from 0 or +Inf and carries on, so log(0) returned -709.09 and
	// log(+Inf) returned 709.78 — finite garbage, not the -Inf / +Inf the
	// values call for. log(-0) == log(0) == -Inf, which the equality
	// branch already covers.
	nanGuard(logRet)
	g.emit("xorpd xmm1, xmm1")
	g.emit("ucomisd xmm0, xmm1")
	g.emit("jb " + logNaN)    // x < 0
	g.emit("je " + logNegInf) // x == ±0
	g.emit("movabs rax, 0x7ff0000000000000")
	g.emit("movq xmm1, rax")
	g.emit("ucomisd xmm0, xmm1")
	g.emit("je " + logRet) // x == +Inf → itself
	g.emit("movq rax, xmm0")
	g.emit("mov rcx, rax")
	g.emit("shr rcx, 52")
	g.emit("and rcx, 0x7ff")
	g.emit("sub rcx, 1023") // k
	g.emit("movabs rdx, 0xfffffffffffff")
	g.emit("and rax, rdx")
	g.emit("movabs rdx, 0x3ff0000000000000")
	g.emit("or rax, rdx")
	g.emit("movq xmm1, rax") // m in [1,2)
	noAdj := g.freshLabel("logNoAdj")
	ldc("xmm2", ".Lfc_sqrt2")
	g.emit("comisd xmm1, xmm2")
	g.emit("jb " + noAdj)
	g.emit("mulsd xmm1, [rip+.Lfc_half]")
	g.emit("add rcx, 1")
	g.label(noAdj)
	g.emit("subsd xmm1, [rip+.Lfc_one]") // f
	ldc("xmm2", ".Lfc_two")
	g.emit("addsd xmm2, xmm1") // 2+f
	g.emit("movsd xmm3, xmm1")
	g.emit("divsd xmm3, xmm2") // s
	g.emit("movsd xmm4, xmm3")
	g.emit("mulsd xmm4, xmm3") // z
	g.emit("movsd xmm5, xmm4")
	g.emit("mulsd xmm5, xmm4") // w
	ldc("xmm6", ".Lfc_lg6")
	horner("xmm6", "xmm5", ".Lfc_lg4")
	horner("xmm6", "xmm5", ".Lfc_lg2")
	g.emit("mulsd xmm6, xmm5") // t1
	ldc("xmm7", ".Lfc_lg7")
	horner("xmm7", "xmm5", ".Lfc_lg5")
	horner("xmm7", "xmm5", ".Lfc_lg3")
	horner("xmm7", "xmm5", ".Lfc_lg1")
	g.emit("mulsd xmm7, xmm4") // t2
	g.emit("addsd xmm6, xmm7") // R
	g.emit("movsd xmm2, xmm1")
	g.emit("mulsd xmm2, xmm1")
	g.emit("mulsd xmm2, [rip+.Lfc_half]") // hfsq
	g.emit("cvtsi2sd xmm0, rcx")          // kf
	g.emit("movsd xmm5, xmm0")
	g.emit("mulsd xmm5, [rip+.Lfc_ln2lo]") // k*ln2_lo
	g.emit("addsd xmm6, xmm2")             // hfsq+R
	g.emit("mulsd xmm6, xmm3")             // s*(hfsq+R)
	g.emit("addsd xmm6, xmm5")
	g.emit("subsd xmm2, xmm6") // hfsq - (…)
	g.emit("subsd xmm2, xmm1") // - f
	g.emit("mulsd xmm0, [rip+.Lfc_ln2hi]")
	g.emit("subsd xmm0, xmm2")
	g.label(logRet)
	g.emit("ret")
	g.label(logNaN)
	retBits("0x7ff8000000000000")
	g.label(logNegInf)
	retBits("0xfff0000000000000")
	g.line(".size __fern_log_f64, .-__fern_log_f64")

	// __fern_pow_f64(xmm0=x, xmm1=y) → x^y = exp(y·ln x), x>0. The only
	// non-leaf helper: SysV has no callee-saved xmm registers, so y is
	// stashed on the stack across the log call, where arm64 can use
	// callee-saved d8.
	fn("__fern_pow_f64")
	powGen, powLoop, powSkip, powDone := g.freshLabel("powGeneral"), g.freshLabel("powLoop"), g.freshLabel("powSkip"), g.freshLabel("powDone")
	// Integer-exponent fast path. exp(y*ln x) CANNOT return exactly 9 for
	// pow(3,2): a 1-ulp error in ln 3 is amplified by the exponential to
	// ~4e-15 on a result of 9, so it lands just under and truncates to 8.
	// Repeated squaring is exact wherever the result is representable, and
	// cheaper than two transcendental calls for small |n|.
	//
	// Integrality is tested by an i64 round-trip rather than a compare
	// against trunc(y), so a NaN or out-of-range y falls out as a huge |n|
	// and is caught by the range check instead of needing a parity branch.
	g.emit("cvttsd2si rax, xmm1")
	g.emit("cvtsi2sd xmm2, rax")
	g.emit("ucomisd xmm2, xmm1")
	g.emit("jne " + powGen)
	// |n|, branch-free: (n ^ (n>>63)) - (n>>63).
	g.emit("mov rcx, rax")
	g.emit("mov rdx, rax")
	g.emit("sar rdx, 63")
	g.emit("xor rcx, rdx")
	g.emit("sub rcx, rdx")
	g.emit("cmp rcx, 64")
	g.emit("ja " + powGen)
	ldc("xmm3", ".Lfc_one") // accumulator
	g.emit("movsd xmm4, xmm0")
	g.label(powLoop)
	g.emit("test rcx, 1")
	g.emit("jz " + powSkip)
	g.emit("mulsd xmm3, xmm4")
	g.label(powSkip)
	g.emit("mulsd xmm4, xmm4")
	g.emit("shr rcx, 1")
	g.emit("jnz " + powLoop)
	g.emit("test rax, rax")
	g.emit("jns " + powDone)
	ldc("xmm5", ".Lfc_one") // negative exponent: reciprocal
	g.emit("divsd xmm5, xmm3")
	g.emit("movsd xmm3, xmm5")
	g.label(powDone)
	g.emit("movsd xmm0, xmm3")
	g.emit("ret")
	// General case: x^y = exp(y*ln x), x>0. The only non-leaf helper;
	// SysV has no callee-saved xmm registers, so y is stashed on the
	// stack across the log call where arm64 can use callee-saved d8.
	g.label(powGen)
	g.emit("sub rsp, 16")
	g.emit("movsd [rsp], xmm1")
	g.emit("call __fern_log_f64")
	g.emit("movsd xmm1, [rsp]")
	g.emit("mulsd xmm0, xmm1")
	g.emit("call __fern_exp_f64")
	g.emit("add rsp, 16")
	g.emit("ret")
	g.line(".size __fern_pow_f64, .-__fern_pow_f64")
}

// emitF64Negate flips the sign bit of an xmm register's low double.
// x86-64 has no scalar fneg, so it is an xor with the sign mask.
func (g *generator) emitF64Negate(reg string) {
	g.emit("movabs rdx, 0x8000000000000000")
	g.emit("movq xmm8, rdx")
	g.emit("xorpd " + reg + ", xmm8")
}

// emitMemchrRuntime emits `__fern_memchr(s, byte, from) -> i32`: the index of
// the first occurrence of `byte` in `s` at or after `from`, or -1.
//
// THE FIRST VECTOR KERNEL (docs/ATLAS-PLATFORM-PLAN.md §3). SSE2, 16 bytes
// per iteration: splat the needle with movd/punpcklbw/punpcklwd/pshufd, then
// movdqu / pcmpeqb / pmovmskb / bsf. Measured **278ms -> 22ms, ~12x** against
// the scalar version it replaces (20,000 scans of a 14.4 KB haystack with the
// needle only at the very end, three runs each).
//
// It needs no CPU feature detection. SSE2 is inside the declared x86-64
// baseline (Haswell-class, SSE4.2 + BMI1), so a selected instruction is a
// hard requirement rather than a fast path and there is nothing to dispatch
// on — §1.1 of the plan.
//
// It also satisfies §3.1's contract, which is what lets it exist without a
// vector register class in the IR: the operands arriving are scalars (a
// string value, a byte, an index), the result is a scalar, and no xmm value
// is live across an op boundary, a call, or a branch out of this body.
//
// WHAT THIS COST THAT THE PLAN DID NOT PREDICT. §3 argued the fused design
// needs no vector register class, no regalloc and no ABI change — all true —
// while silently assuming the assemblers could already ENCODE vector
// instructions. They could not: `internal/native/x86_64`, the in-process
// assembler `-target x86-64` uses unless `-cc` opts out, had no vector
// surface at all, only the scalar float ops the code generator uses to
// shuttle f64 through xmm. This kernel shipped scalar first for exactly that
// reason. The encodings landed alongside it (see TestEncodeVectorSurface,
// pinned byte-for-byte against GNU `as`). The gap turned out to be universal:
// arm64's assembler had no NEON either, and `internal/wasm` had no v128 —
// three assemblers, three prerequisite PRs.
//
// The `from` argument is clamped rather than validated — negative behaves as
// 0, past-the-end finds nothing — matching the interpreter reference and the
// scan loops in std/string this is intended to replace.
func (g *generator) emitMemchrRuntime() {
	g.line("")
	g.line(".globl __fern_memchr")
	g.line(".type __fern_memchr, @function")
	g.label("__fern_memchr")
	// rdi = string, esi = byte, edx = from.
	// Frame: 16 bytes of emitStrDataPtr scratch (the operand may be an
	// inline SSO string, which has to be spilled to get an address).
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")
	g.emitStrLen("ecx", "rdi") // ecx = len
	g.emitStrDataPtr("rdi", "rdi", "[rbp - 16]")
	// Clamp `from` into [0, len]. Anything at or past len finds nothing.
	g.emit("test edx, edx")
	g.emit("jns .Lmemchr_from_ok")
	g.emit("xor edx, edx")
	g.label(".Lmemchr_from_ok")
	g.emit("cmp edx, ecx")
	g.emit("jge .Lmemchr_miss")
	// A byte outside 0..255 can never occur. Checked once, so neither the
	// vector nor the scalar loop needs a per-iteration guard.
	g.emit("cmp esi, 255")
	g.emit("ja .Lmemchr_miss")
	// r8 = cursor, r9 = end.
	g.emit("mov r8d, edx")
	g.emit("add r8, rdi") // cursor = data + from
	g.emit("mov r9d, ecx")
	g.emit("add r9, rdi") // end = data + len
	// Broadcast the needle byte across xmm1. movd + punpcklbw + punpcklwd
	// + pshufd is the SSE2 splat; pshufb would be one instruction but is
	// SSSE3, outside the declared baseline.
	g.emit("movd xmm1, esi")
	g.emit("punpcklbw xmm1, xmm1")
	g.emit("punpcklwd xmm1, xmm1")
	g.emit("pshufd xmm1, xmm1, 0")
	// Vector loop: 16 bytes per iteration while at least 16 remain.
	g.label(".Lmemchr_vec")
	g.emit("mov rax, r9")
	g.emit("sub rax, r8")
	g.emit("cmp rax, 16")
	g.emit("jl .Lmemchr_tail")
	// Unaligned load is deliberate. Aligning first needs a scalar prologue
	// whose branch costs more than movdqu does on any CPU in the baseline,
	// and the pointer comes from the allocator rather than the caller, so a
	// 16-byte read starting inside the string cannot cross into an unmapped
	// page.
	g.emit("movdqu xmm0, [r8]")
	g.emit("pcmpeqb xmm0, xmm1")
	g.emit("pmovmskb eax, xmm0")
	g.emit("test eax, eax")
	g.emit("jnz .Lmemchr_hit")
	g.emit("add r8, 16")
	g.emit("jmp .Lmemchr_vec")
	g.label(".Lmemchr_hit")
	// bsf gives the lane index of the lowest set mask bit — the first
	// matching byte in this block. NOT tzcnt: that is BMI1, and below the
	// baseline its F3 prefix is ignored so it degrades silently to bsf
	// rather than faulting.
	g.emit("bsf eax, eax")
	g.emit("add r8, rax")
	g.emit("sub r8, rdi") // back to an index into the string
	g.emit("mov eax, r8d")
	g.emit("jmp .Lmemchr_ret")
	// Scalar tail: fewer than 16 bytes left. Also the whole algorithm for
	// short strings, which is the common case in a search family.
	g.label(".Lmemchr_tail")
	g.emit("cmp r8, r9")
	g.emit("jge .Lmemchr_miss")
	g.emit("movzx eax, byte ptr [r8]")
	g.emit("cmp eax, esi")
	g.emit("je .Lmemchr_tail_hit")
	g.emit("inc r8")
	g.emit("jmp .Lmemchr_tail")
	g.label(".Lmemchr_tail_hit")
	g.emit("mov rax, r8")
	g.emit("sub rax, rdi")
	g.emit("jmp .Lmemchr_ret")
	g.label(".Lmemchr_miss")
	g.emit("mov eax, -1")
	g.label(".Lmemchr_ret")
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_memchr, .-__fern_memchr")
}

func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".globl __fern_strcmp")
	g.line(".type __fern_strcmp, @function")
	g.label("__fern_strcmp")
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
	g.line(".size __fern_strcmp, .-__fern_strcmp")
}

// emitPutsRuntime emits `__fern_puts(s)` — write the string,
// then a single trailing newline. Two write(2) calls keeps
// the code simple at the cost of one extra syscall per call;
// per-call cost is dominated by the syscall itself either
// way. Preserves r12 across the second write so we can
// return the original data pointer for libc-puts
// consistency.
func (g *generator) emitPutsRuntime() {
	g.line("")
	g.line(".globl __fern_puts")
	g.line(".type __fern_puts, @function")
	g.label("__fern_puts")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push r12")
	g.emit("sub rsp, 16")  // 8 bytes scratch for emitStrDataPtr + 8 alignment
	g.emit("mov r12, rdi") // r12 = original string value (saved for return)
	// write(1, s, len(s))
	g.emitStrLen("edx", "rdi") // length
	g.emitStrDataPtr("rsi", "rdi", "[rbp - 16]")
	g.emit("mov edi, 1") // fd = stdout
	g.emitSyscall(sysWrite)
	// write(1, "\n", 1)
	g.emit("lea rsi, [rip + .LLangNewline]")
	g.emit("mov edx, 1")
	g.emit("mov edi, 1")
	g.emitSyscall(sysWrite)
	g.emit("mov rax, r12") // return the original string value (heap or inline)
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_puts, .-__fern_puts")
}

// emitWriteRuntime emits `__fern_write(s)` — write the
// string with no trailing newline. Single write(2) syscall.
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".globl __fern_write")
	g.line(".type __fern_write, @function")
	g.label("__fern_write")
	// Frame: 16 bytes — 8 bytes scratch slot for emitStrDataPtr
	// + 8 bytes for the saved original string value (so we can
	// return it after the syscall clobbers caller-save regs).
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")
	g.emit("mov [rbp - 8], rdi") // save original
	g.emitStrLen("edx", "rdi")   // length
	g.emitStrDataPtr("rsi", "rdi", "[rbp - 16]")
	g.emit("mov edi, 1") // fd = stdout
	g.emitSyscall(sysWrite)
	g.emit("mov rax, [rbp - 8]") // return original
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_write, .-__fern_write")
}

// emitEprintRuntime emits `__fern_eprint(s)` — stderr
// counterpart to print(). Two write(2)s to fd=2: the string,
// then a newline. Mirrors __fern_puts modulo the fd.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".globl __fern_eprint")
	g.line(".type __fern_eprint, @function")
	g.label("__fern_eprint")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push r12")
	g.emit("sub rsp, 16")  // 8 bytes scratch for emitStrDataPtr + 8 alignment
	g.emit("mov r12, rdi") // r12 = original string value (preserved for return)
	g.emitStrLen("edx", "rdi")
	g.emitStrDataPtr("rsi", "rdi", "[rbp - 16]")
	g.emit("mov edi, 2") // fd = stderr
	g.emitSyscall(sysWrite)
	g.emit("lea rsi, [rip + .LLangNewline]")
	g.emit("mov edx, 1")
	g.emit("mov edi, 2")
	g.emitSyscall(sysWrite)
	g.emit("mov rax, r12")
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_eprint, .-__fern_eprint")
}

// emitStrBufRuntime emits the three global mutable-string-builder
// helpers and the BSS scratch they share.
//
//	__fern_strbuf_data: .skip 64 MiB
//	__fern_strbuf_len:  .quad 0 (current byte count)
//
//	__fern_strbuf_reset()       — len = 0
//	__fern_strbuf_append(s)     — memcpy s past current tail, bump len
//	__fern_strbuf_take() -> str — allocate fresh string of accumulated
//	                              bytes, copy, reset len, return it
//
// Built for the asm self-host backend's emit_module — the
// `s = s.out + text` per write pattern allocates O(N²) bytes
// through the bump heap, which can't compile asm.fern through itself
// (~60 GB needed). With the strbuf the same loop is O(N).
//
// Single-threaded; only one strbuf active at a time. The 64 MiB cap
// is generous for the asm-self-host use case (asm.fern's expected
// output is ~2 MB) but documented.
func (g *generator) emitStrBufRuntime() {
	g.line("")
	g.line(".section .bss")
	g.line(".align 8")
	g.line("__fern_strbuf_len: .skip 8")
	g.line(".align 8")
	g.line("__fern_strbuf_data: .skip 67108864") // 64 MiB
	g.line(".section .text")

	// __fern_strbuf_reset(): len = 0
	g.line("")
	g.line(".globl __fern_strbuf_reset")
	g.line(".type __fern_strbuf_reset, @function")
	g.label("__fern_strbuf_reset")
	g.emit("mov qword ptr [rip + __fern_strbuf_len], 0")
	g.emit("ret")
	g.line(".size __fern_strbuf_reset, .-__fern_strbuf_reset")

	// __fern_strbuf_append(s): rdi = string (may be inline-tagged).
	// Reads len via emitStrLen, materialises data ptr via
	// emitStrDataPtr (spilling inline form to a frame slot), then
	// memcpys bytes to __fern_strbuf_data + __fern_strbuf_len and
	// bumps the counter. No bounds check — overflow is UB (caller
	// keeps total under 64 MiB).
	g.line("")
	g.line(".globl __fern_strbuf_append")
	g.line(".type __fern_strbuf_append, @function")
	g.label("__fern_strbuf_append")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("sub rsp, 16") // 8 spill for inline data + 8 align
	g.emit("mov r12, rdi")
	g.emitStrLen("ebx", "r12")                   // ebx = src len
	g.emitStrDataPtr("r12", "r12", "[rbp - 32]") // r12 = src data ptr
	// dst = strbuf_data + strbuf_len
	g.emit("mov rcx, qword ptr [rip + __fern_strbuf_len]")
	g.emit("lea rdi, [rip + __fern_strbuf_data]")
	g.emit("add rdi, rcx")
	g.emit("mov rsi, r12")
	g.emit("mov edx, ebx")
	g.emit("call __fern_memcpy")
	// strbuf_len += src len
	g.emit("mov rcx, qword ptr [rip + __fern_strbuf_len]")
	g.emit("add ecx, ebx")
	g.emit("mov qword ptr [rip + __fern_strbuf_len], rcx")
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_strbuf_append, .-__fern_strbuf_append")

	// __fern_strbuf_take(): allocates a fresh `[prefix(4) + data(N) +
	// NUL(1)]` block, copies the accumulated bytes into it, writes
	// the length prefix, NUL-terminates, resets the strbuf, returns
	// the data pointer.
	g.line("")
	g.line(".globl __fern_strbuf_take")
	g.line(".type __fern_strbuf_take, @function")
	g.label("__fern_strbuf_take")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("push r12")
	g.emit("sub rsp, 8")                                   // align
	g.emit("mov r12, qword ptr [rip + __fern_strbuf_len]") // r12 = current len
	// L2 rc-header layout (see __fern_strcat): payload = len data + 1 NUL.
	g.emit("lea rdi, [r12 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov rbx, rax") // rbx = data ptr (= base+8)
	// length prefix
	g.emit("mov ecx, r12d")
	g.emitStrLenStore("ecx", "rbx")
	// memcpy(rbx, &__fern_strbuf_data, r12)
	g.emit("mov rdi, rbx")
	g.emit("lea rsi, [rip + __fern_strbuf_data]")
	g.emit("mov edx, r12d")
	g.emit("call __fern_memcpy")
	// NUL terminator at rbx + r12
	g.emit("lea rdi, [rbx + r12]")
	g.emit("mov byte ptr [rdi], 0")
	// reset
	g.emit("mov qword ptr [rip + __fern_strbuf_len], 0")
	g.emit("mov rax, rbx")
	g.emit("add rsp, 8")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_strbuf_take, .-__fern_strbuf_take")
}

// emitExitRuntime emits `__fern_exit(code)` — direct exit
// syscall. rdi already holds the user-supplied exit code from
// the System V arg-pop. exit_group never returns; the trailing
// `ret` is assembler-completeness only.
func (g *generator) emitExitRuntime() {
	g.line("")
	g.line(".globl __fern_exit")
	g.line(".type __fern_exit, @function")
	g.label("__fern_exit")
	// rdi already holds the exit code (System V arg 1).
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): the exit() builtin bypasses the
		// _start epilogue, so report here too. The code parks in rbx —
		// clobbering a callee-save is fine on a path that never returns.
		g.emit("mov ebx, edi")
		g.emit("call __fern_lc_report")
		g.emit("mov edi, ebx")
	}
	g.emitSyscall(sysExitGroup)
	g.emit("ret")
	g.line(".size __fern_exit, .-__fern_exit")
}

// emitLcReportRuntime emits `__fern_lc_report()` — the leak detector's
// (#5362 slice 1) exit-time summary. Writes one line to stderr:
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// where K = __fern_lc_alloc_bytes − __fern_lc_free_bytes (signed — an
// over-free would show negative rather than wrapping). Only emitted
// when ast.LeakCheckEnabled; called from the _start epilogue and
// __fern_exit, which park the exit code in rbx across the call, so the
// helper (and its two local subroutines) must touch caller-saved
// registers only. The decimal formatting is a self-contained
// divide-by-10 loop into a stack buffer (.Llc_wrnum) — the language's
// i64-to-string paths are Fern-level and can't be assumed present.
func (g *generator) emitLcReportRuntime() {
	g.line("")
	g.line(".globl __fern_lc_report")
	g.line(".type __fern_lc_report, @function")
	g.label("__fern_lc_report")
	g.emit("lea rsi, [rip + .Llc_str_allocs]")
	g.emit("mov edx, 18")
	g.emit("call .Llc_write")
	g.emit("mov rdi, [rip + __fern_lc_alloc_count]")
	g.emit("call .Llc_wrnum")
	g.emit("lea rsi, [rip + .Llc_str_frees]")
	g.emit("mov edx, 7")
	g.emit("call .Llc_write")
	g.emit("mov rdi, [rip + __fern_lc_free_count]")
	g.emit("call .Llc_wrnum")
	g.emit("lea rsi, [rip + .Llc_str_live]")
	g.emit("mov edx, 12")
	g.emit("call .Llc_write")
	g.emit("mov rdi, [rip + __fern_lc_alloc_bytes]")
	g.emit("sub rdi, [rip + __fern_lc_free_bytes]")
	g.emit("call .Llc_wrnum")
	g.emit("lea rsi, [rip + .Llc_str_nl]")
	g.emit("mov edx, 1")
	g.emit("call .Llc_write")
	if ast.SanitizeEnabled {
		// Sanitizer leak verdict (#5545). Only a POSITIVE balance is a
		// leak: zero is clean and a negative one is an over-free, which
		// the rc over-release detector reports at the offending dec —
		// naming it a leak here would be wrong twice over.
		g.emit("mov rax, [rip + __fern_lc_alloc_bytes]")
		g.emit("sub rax, [rip + __fern_lc_free_bytes]")
		g.emit("test rax, rax")
		g.emit("jle .Lsan_leak_done")
		g.emit("lea rsi, [rip + .Lsan_str_leak]")
		g.emit(fmt.Sprintf("mov edx, %d", len(sanLeakPrefix)))
		g.emit("call .Llc_write")
		g.emit("mov rdi, [rip + __fern_lc_alloc_bytes]")
		g.emit("sub rdi, [rip + __fern_lc_free_bytes]")
		g.emit("call .Llc_wrnum")
		g.emit("lea rsi, [rip + .Lsan_str_bytesin]")
		g.emit(fmt.Sprintf("mov edx, %d", len(sanLeakMiddle)))
		g.emit("call .Llc_write")
		g.emit("mov rdi, [rip + __fern_lc_alloc_count]")
		g.emit("sub rdi, [rip + __fern_lc_free_count]")
		g.emit("call .Llc_wrnum")
		g.emit("lea rsi, [rip + .Lsan_str_blocks]")
		g.emit(fmt.Sprintf("mov edx, %d", len(sanLeakSuffix)))
		g.emit("call .Llc_write")
		g.emit("lea rsi, [rip + .Llc_str_nl]")
		g.emit("mov edx, 1")
		g.emit("call .Llc_write")
		g.label(".Lsan_leak_done")
	}
	g.emit("ret")
	// .Llc_write(rsi = buf, edx = len): one write(2) to stderr.
	g.label(".Llc_write")
	g.emit("mov edi, 2")
	g.emitSyscall(sysWrite)
	g.emit("ret")
	// .Llc_wrnum(rdi = signed i64): decimal itoa, digits built
	// backwards from the end of a 32-byte stack buffer (an i64 is at
	// most 19 digits + sign), then one write(2) to stderr.
	g.label(".Llc_wrnum")
	g.emit("sub rsp, 40") // 32-byte digit buffer + 8 spare
	g.emit("lea rcx, [rsp + 32]")
	g.emit("mov r8, 10")
	g.emit("xor r9d, r9d") // sign flag
	g.emit("mov rax, rdi")
	g.emit("test rax, rax")
	g.emit("jns .Llc_wrnum_loop")
	g.emit("neg rax")
	g.emit("mov r9d, 1")
	g.label(".Llc_wrnum_loop")
	g.emit("xor edx, edx")
	g.emit("div r8")
	g.emit("add edx, 48") // remainder → ASCII digit
	g.emit("sub rcx, 1")
	g.emit("mov byte ptr [rcx], dl")
	g.emit("test rax, rax")
	g.emit("jnz .Llc_wrnum_loop")
	g.emit("test r9d, r9d")
	g.emit("jz .Llc_wrnum_emit")
	g.emit("sub rcx, 1")
	g.emit("mov byte ptr [rcx], 45") // '-'
	g.label(".Llc_wrnum_emit")
	g.emit("lea rdx, [rsp + 32]")
	g.emit("sub rdx, rcx") // len
	g.emit("mov rsi, rcx")
	g.emit("mov edi, 2")
	g.emitSyscall(sysWrite)
	g.emit("add rsp, 40")
	g.emit("ret")
	g.line(".size __fern_lc_report, .-__fern_lc_report")
}

// emitNowUnixMsRuntime emits `__fern_now_unix_ms()` — wall-
// clock milliseconds since the Unix epoch via x86_64
// `clock_gettime(CLOCK_REALTIME, &ts)` (syscall 228). The
// kernel writes a `struct timespec { i64 tv_sec; i64 tv_nsec }`
// to the caller-provided pointer; we compute
// `tv_sec * 1000 + tv_nsec / 1_000_000` and return it in rax.
//
// Stack frame: 16-byte aligned per AMD64 ABI — sub rsp,24
// reserves 16 bytes for timespec + 8 for alignment. rbp save
// adds another 8, total 32 from the call's misaligned-by-8
// rsp entry point.
//
// Errno is ignored (same as arm64): the realistic failure
// modes (EFAULT / EINVAL) can't trigger here since we control
// both the clock id and the buffer.
func (g *generator) emitNowUnixMsRuntime() {
	g.line("")
	g.line(".globl __fern_now_unix_ms")
	g.line(".type __fern_now_unix_ms, @function")
	g.label("__fern_now_unix_ms")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 24")  // 16 timespec + 8 alignment
	g.emit("xor edi, edi") // CLOCK_REALTIME = 0
	g.emit("mov rsi, rsp") // &timespec
	g.emitSyscall(sysClockGettime)
	g.emit("mov r10, [rsp]")      // r10 = tv_sec
	g.emit("imul r10, r10, 1000") // sec * 1000
	g.emit("xor edx, edx")        // clear high for div
	g.emit("mov rax, [rsp + 8]")  // rax = tv_nsec (positive)
	g.emit("mov rcx, 1000000")
	g.emit("div rcx")      // rax = nsec / 1e6
	g.emit("add rax, r10") // result
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_now_unix_ms, .-__fern_now_unix_ms")
}

// emitMonotonicNsRuntime emits `__fern_monotonic_ns()` —
// monotonic nanoseconds via x86_64
// `clock_gettime(CLOCK_MONOTONIC, &ts)` (syscall 228). The
// kernel writes `struct timespec { i64 tv_sec; i64 tv_nsec }`;
// we return `tv_sec * 1e9 + tv_nsec` in rax. The monotonic
// clock counts from an unspecified fixed reference, so only
// the DELTA between two readings is meaningful — exactly what
// benchmark timing wants (NTP-jump-immune, unlike now_unix_ms).
//
// Stack frame mirrors now_unix_ms: sub rsp,24 = 16 timespec +
// 8 alignment. Errno ignored (we control clock id + buffer).
func (g *generator) emitMonotonicNsRuntime() {
	g.line("")
	g.line(".globl __fern_monotonic_ns")
	g.line(".type __fern_monotonic_ns, @function")
	g.label("__fern_monotonic_ns")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 24")  // 16 timespec + 8 alignment
	g.emit("mov edi, 1")   // CLOCK_MONOTONIC = 1
	g.emit("mov rsi, rsp") // &timespec
	g.emitSyscall(sysClockGettime)
	g.emit("mov rax, [rsp]")            // rax = tv_sec
	g.emit("imul rax, rax, 1000000000") // sec * 1e9
	g.emit("add rax, [rsp + 8]")        // + tv_nsec
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_monotonic_ns, .-__fern_monotonic_ns")
}

// emitNowNsRuntime emits `__fern_now_ns()` — wall-clock
// nanoseconds since the Unix epoch via x86_64
// `clock_gettime(CLOCK_REALTIME, &ts)` (syscall 228); returns
// `tv_sec * 1e9 + tv_nsec` in rax. The nanosecond-resolution
// twin of now_unix_ms (same realtime clock). Stack frame +
// errno handling identical to monotonic_ns; only the clock id
// (CLOCK_REALTIME = 0) differs.
func (g *generator) emitNowNsRuntime() {
	g.line("")
	g.line(".globl __fern_now_ns")
	g.line(".type __fern_now_ns, @function")
	g.label("__fern_now_ns")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 24")  // 16 timespec + 8 alignment
	g.emit("xor edi, edi") // CLOCK_REALTIME = 0
	g.emit("mov rsi, rsp") // &timespec
	g.emitSyscall(sysClockGettime)
	g.emit("mov rax, [rsp]")            // rax = tv_sec
	g.emit("imul rax, rax, 1000000000") // sec * 1e9
	g.emit("add rax, [rsp + 8]")        // + tv_nsec
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_now_ns, .-__fern_now_ns")
}

// emitSleepMsRuntime emits `__fern_sleep_ms(ms)` — pause for
// `ms` milliseconds (System V arg 1 in rdi, an i64). ms <= 0
// returns immediately. Splits ms into a `struct timespec
// { tv_sec = ms / 1000; tv_nsec = (ms % 1000) * 1e6 }` on the
// stack and calls `nanosleep(&req, NULL)` (syscall 35). Void —
// the operand stack push is gated off by callReturnsVoid.
//
// Stack frame: sub rsp,32 = 16 timespec + 16 alignment/pad.
// Errno / early-wake remainder ignored (best-effort sleep —
// matches the self-host __fern_sleep_ms and the interpreter).
func (g *generator) emitSleepMsRuntime() {
	g.line("")
	g.line(".globl __fern_sleep_ms")
	g.line(".type __fern_sleep_ms, @function")
	g.label("__fern_sleep_ms")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 32")
	g.emit("cmp rdi, 0")
	g.emit("jle .Lsleep_ms_done")
	g.emit("mov rax, rdi")
	g.emit("xor edx, edx") // clear high for div
	g.emit("mov rcx, 1000")
	g.emit("div rcx")        // rax = ms/1000 (sec), rdx = ms%1000 (rem)
	g.emit("mov [rsp], rax") // tv_sec
	g.emit("mov rax, 1000000")
	g.emit("imul rax, rdx") // rem * 1e6 = tv_nsec
	g.emit("mov [rsp + 8], rax")
	g.emit("mov rdi, rsp") // &req
	g.emit("xor esi, esi") // rem = NULL
	g.emitSyscall(sysNanosleep)
	g.label(".Lsleep_ms_done")
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_sleep_ms, .-__fern_sleep_ms")
}

// emitProcForkRuntime emits `__fern_proc_fork()` — fork(2)
// (syscall 57, no args). The kernel's return shape is already
// the builtin's contract: 0 in the child, the child's pid in
// the parent, -errno on failure — so this is a bare syscall
// wrapper. Result in eax (i32). The crash-only supervision
// primitive (docs/CRASH-ONLY-SERVE.md D2').
func (g *generator) emitProcForkRuntime() {
	g.line("")
	g.line(".globl __fern_proc_fork")
	g.line(".type __fern_proc_fork, @function")
	g.label("__fern_proc_fork")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emitSyscall(sysFork)
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_proc_fork, .-__fern_proc_fork")
}

// emitProcExecRuntime emits `__fern_proc_exec(path, args) -> i32` —
// execve(2) (syscall 59), the third leg of the crash-only process trio
// alongside __fern_proc_fork / __fern_proc_waitpid, and the piece that lets a
// forked child become another program.
//
// It replaces the calling process, so on SUCCESS it does not return at all.
// The i32 result therefore only ever carries failure: -errno, exactly as the
// kernel hands it back, matching proc_fork's "the syscall's return shape IS
// the builtin's contract" convention.
//
// argv is built as [path, args[0], ..., args[n-1], NULL] — the callee's argv[0]
// is the program path, the convention every exec'd program expects, so callers
// pass only the real arguments. envp is inherited via the __fern_envp global
// captured at _start, so the child sees the parent's environment.
//
// Fern strings are length-prefixed and NOT guaranteed NUL-terminated (only the
// argv-derived ones are), and execve needs C strings, so the path and every
// argument are copied into fresh NUL-terminated buffers. Those allocations are
// deliberately never freed: on the success path the address space is replaced,
// and on the failure path the caller is about to report an error and exit.
func (g *generator) emitProcExecRuntime() {
	g.line("")
	g.line(".globl __fern_proc_exec")
	g.line(".type __fern_proc_exec, @function")
	g.label("__fern_proc_exec")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // rbp-8:  args box
	g.emit("push r12") // rbp-16: argc
	g.emit("push r13") // rbp-24: argv
	g.emit("push r14") // rbp-32: loop index
	g.emit("push r15") // rbp-40: scratch pointer
	// Spill slots start BELOW the pushed callee-saved registers (rbp-8 ..
	// rbp-40); writing at or above rbp-40 would corrupt the caller's rbx /
	// r12..r15. 72 keeps rsp 16-aligned for the __fern_alloc calls
	// (rbp-40-72 = rbp-112).
	g.emit("sub rsp, 72")
	//   [rbp-48] source bytes (path, then each element)
	//   [rbp-56] path cstr
	//   [rbp-64] element length
	//   [rbp-80] emitStrDataPtr scratch — for a SMALL string that helper
	//            returns a pointer INTO this slot, so nothing else may use it.

	g.emit("mov rbx, rsi") // rbx = args box (string[] data ptr)
	// Path -> NUL-terminated copy.
	g.emitStrLen("r12d", "rdi")                  // r12 = path length
	g.emitStrDataPtr("r15", "rdi", "[rbp - 80]") // r15 = path bytes
	g.emit("mov [rbp - 48], r15")
	g.emit("lea rdi, [r12 + 1]")
	g.emit("call __fern_alloc")
	g.emit("mov [rbp - 56], rax") // path cstr
	g.emit("mov r15, [rbp - 48]") // path bytes
	g.emit("xor ecx, ecx")
	g.label(".Lpexec_pcopy")
	g.emit("cmp rcx, r12")
	g.emit("jge .Lpexec_pcopy_done")
	g.emit("mov dl, [r15 + rcx]")
	g.emit("mov [rax + rcx], dl")
	g.emit("inc rcx")
	g.emit("jmp .Lpexec_pcopy")
	g.label(".Lpexec_pcopy_done")
	g.emit("mov byte ptr [rax + r12], 0")

	// argv = alloc((argc + 2) * 8); argv[0] = path cstr.
	g.emitArrayLen("r12d", "rbx") // r12 = argc
	g.emit("lea rdi, [r12 + 2]")
	g.emit("shl rdi, 3")
	g.emit("call __fern_alloc")
	g.emit("mov r13, rax")        // r13 = argv
	g.emit("mov rax, [rbp - 56]") // path cstr
	g.emit("mov [r13], rax")      // argv[0]
	g.emit("xor r14d, r14d")      // i = 0

	g.label(".Lpexec_arg")
	g.emit("cmp r14, r12")
	g.emit("jge .Lpexec_arg_done")
	g.emit("mov r15, [rbx + r14*8]") // element string box
	g.emitStrLen("ecx", "r15")
	g.emit("mov [rbp - 64], rcx")                // element length
	g.emitStrDataPtr("r15", "r15", "[rbp - 80]") // element bytes
	g.emit("mov [rbp - 48], r15")
	g.emit("mov rdi, [rbp - 64]")
	g.emit("inc rdi")
	g.emit("call __fern_alloc")
	g.emit("mov r15, [rbp - 48]") // src bytes
	g.emit("mov rdx, [rbp - 64]") // length
	g.emit("xor ecx, ecx")
	g.label(".Lpexec_acopy")
	g.emit("cmp rcx, rdx")
	g.emit("jge .Lpexec_acopy_done")
	g.emit("mov r8b, [r15 + rcx]")
	g.emit("mov [rax + rcx], r8b")
	g.emit("inc rcx")
	g.emit("jmp .Lpexec_acopy")
	g.label(".Lpexec_acopy_done")
	g.emit("mov byte ptr [rax + rdx], 0")
	g.emit("lea rcx, [r14 + 1]")
	g.emit("mov [r13 + rcx*8], rax") // argv[i + 1]
	g.emit("inc r14")
	g.emit("jmp .Lpexec_arg")
	g.label(".Lpexec_arg_done")
	g.emit("lea rcx, [r12 + 1]")
	g.emit("mov qword ptr [r13 + rcx*8], 0") // argv[argc + 1] = NULL

	g.emit("mov rdi, [rbp - 56]") // path cstr
	g.emit("mov rsi, r13")        // argv
	g.emit("mov rdx, [rip + __fern_envp]")
	g.emitSyscall(sysExecve)
	// Only reachable on failure; rax holds -errno.
	g.emit("add rsp, 72")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_proc_exec, .-__fern_proc_exec")
}

// emitProcWaitpidRuntime emits `__fern_proc_waitpid(pid)` —
// blocking wait4(2) (syscall 61: pid, &status, options=0,
// rusage=NULL; status on the stack) plus the status-word decode:
//
//	WIFEXITED  ((status & 0x7f) == 0) → (status >> 8) & 0xff
//	else (signal death)               → 128 + (status & 0x7f)
//
// (the shell convention — a bounds-trap worker surfaces as its
// raw exit code, e.g. 134). A negative rax from the syscall
// (-errno, e.g. -ECHILD) returns as-is. Result in eax (i32).
func (g *generator) emitProcWaitpidRuntime() {
	g.line("")
	g.line(".globl __fern_proc_waitpid")
	g.line(".type __fern_proc_waitpid, @function")
	g.label("__fern_proc_waitpid")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")     // status slot (16 keeps rsp 16-aligned)
	g.emit("movsxd rdi, edi") // pid
	g.emit("mov rsi, rsp")    // &status
	g.emit("xor edx, edx")    // options = 0
	g.emit("xor r10d, r10d")  // rusage = NULL
	g.emitSyscall(sysWait4)
	g.emit("test rax, rax")
	g.emit("js .Lproc_wait_done")      // -errno → return as-is
	g.emit("mov ecx, dword ptr [rsp]") // status word
	g.emit("mov eax, ecx")
	g.emit("and eax, 0x7f")
	g.emit("jnz .Lproc_wait_sig")
	// Normal exit: (status >> 8) & 0xff.
	g.emit("mov eax, ecx")
	g.emit("shr eax, 8")
	g.emit("and eax, 0xff")
	g.emit("jmp .Lproc_wait_done")
	g.label(".Lproc_wait_sig")
	// Signal death: 128 + signal (eax already holds status & 0x7f).
	g.emit("add eax, 128")
	g.label(".Lproc_wait_done")
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_proc_waitpid, .-__fern_proc_waitpid")
}

// emitPutcharRuntime emits `__fern_putchar(c)` — write a
// single byte to stdout. Stash on the stack, write(1, &c, 1).
func (g *generator) emitPutcharRuntime() {
	g.line("")
	g.line(".globl __fern_putchar")
	g.line(".type __fern_putchar, @function")
	g.label("__fern_putchar")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")    // 1 byte slot + alignment
	g.emit("mov [rsp], dil") // byte value
	g.emit("mov edi, 1")     // fd = stdout
	g.emit("mov rsi, rsp")   // buf = &slot
	g.emit("mov edx, 1")     // count = 1
	g.emitSyscall(sysWrite)
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_putchar, .-__fern_putchar")
}

// emitTcpListenRuntime emits `__fern_tcp_listen(port)` —
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
	g.line(".globl __fern_tcp_listen")
	g.line(".type __fern_tcp_listen, @function")
	g.label("__fern_tcp_listen")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")      // callee-save scratch
	g.emit("push r12")      // callee-save: port
	g.emit("sub rsp, 16")   // sockaddr_in (16 bytes) — also keeps rsp 16-aligned for the syscall
	g.emit("mov r12d, edi") // r12 = port
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	g.emit("mov edi, 2")
	g.emit("mov esi, 1")
	g.emit("xor edx, edx")
	g.emitSyscall(sysSocket)
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
	g.emit("xchg al, ah") // htons low 16
	g.emit("mov word ptr [rsp+2], ax")
	g.emit("mov dword ptr [rsp+4], 0")
	g.emit("mov qword ptr [rsp+8], 0")
	// bind(fd, sa, 16)
	g.emit("mov edi, ebx")
	g.emit("mov rsi, rsp")
	g.emit("mov edx, 16")
	g.emitSyscall(sysBind)
	g.emit("test eax, eax")
	g.emit("js .Ltcp_lst_err")
	// listen(fd, 128)
	g.emit("mov edi, ebx")
	g.emit("mov esi, 128")
	g.emitSyscall(sysListen)
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
	g.line(".size __fern_tcp_listen, .-__fern_tcp_listen")
}

// emitTcpConnectRuntime emits `__fern_tcp_connect(host_be, port)` — the
// outbound client primitive (the upstream-fetch half of the
// edge-handler use case). `host_be` is the IPv4 address already in
// network byte order packed into an i32 (e.g. `ipv4(a,b,c,d)` =
// a | b<<8 | c<<16 | d<<24), so it drops straight into sin_addr.
// Returns the connected socket fd, or -errno. Mirrors
// __fern_tcp_listen's socket + sockaddr_in construction.
func (g *generator) emitTcpConnectRuntime() {
	g.line("")
	g.line(".globl __fern_tcp_connect")
	g.line(".type __fern_tcp_connect, @function")
	g.label("__fern_tcp_connect")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")      // fd
	g.emit("push r12")      // port
	g.emit("push r13")      // host_be
	g.emit("sub rsp, 16")   // sockaddr_in
	g.emit("mov r13d, edi") // host_be
	g.emit("mov r12d, esi") // port
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	g.emit("mov edi, 2")
	g.emit("mov esi, 1")
	g.emit("xor edx, edx")
	g.emitSyscall(sysSocket)
	g.emit("test eax, eax")
	g.emit("js .Ltcp_con_err")
	g.emit("mov ebx, eax") // fd
	// sockaddr_in { sin_family=AF_INET, sin_port=htons(port),
	//              sin_addr=host_be, sin_zero=0 }
	g.emit("mov word ptr [rsp], 2")
	g.emit("mov eax, r12d")
	g.emit("xchg al, ah") // htons
	g.emit("mov word ptr [rsp+2], ax")
	g.emit("mov dword ptr [rsp+4], r13d") // sin_addr (already network order)
	g.emit("mov qword ptr [rsp+8], 0")
	// connect(fd, sa, 16)
	g.emit("mov edi, ebx")
	g.emit("mov rsi, rsp")
	g.emit("mov edx, 16")
	g.emitSyscall(sysConnect)
	g.emit("test eax, eax")
	g.emit("js .Ltcp_con_err")
	g.emit("mov eax, ebx") // return fd
	g.emit("jmp .Ltcp_con_done")
	g.label(".Ltcp_con_err")
	// rax holds -errno from the failing syscall.
	g.label(".Ltcp_con_done")
	g.emit("add rsp, 16")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_tcp_connect, .-__fern_tcp_connect")
}

// emitTcpAcceptRuntime emits `__fern_tcp_accept(listener)` —
// blocks waiting for a connection. Returns the new client fd
// or -errno. accept(fd, NULL, NULL) discards peer address.
func (g *generator) emitTcpAcceptRuntime() {
	g.line("")
	g.line(".globl __fern_tcp_accept")
	g.line(".type __fern_tcp_accept, @function")
	g.label("__fern_tcp_accept")
	// fd in rdi; pass NULL/NULL for addr/addrlen.
	g.emit("xor esi, esi")
	g.emit("xor edx, edx")
	g.emitSyscall(sysAccept)
	g.emit("ret")
	g.line(".size __fern_tcp_accept, .-__fern_tcp_accept")
}

// emitTcpRecvRuntime emits `__fern_tcp_recv(fd, max)` —
// reads up to `max` bytes from the socket fd into a fresh
// length-prefixed lang string. EOF / error → length 0.
// Saves r12 across the syscall so the data pointer survives.
func (g *generator) emitTcpRecvRuntime() {
	g.line("")
	g.line(".globl __fern_tcp_recv")
	g.line(".type __fern_tcp_recv, @function")
	g.label("__fern_tcp_recv")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")      // fd
	g.emit("push r12")      // max
	g.emit("push r13")      // data ptr
	g.emit("sub rsp, 8")    // align
	g.emit("mov ebx, edi")  // rbx = fd
	g.emit("mov r12d, esi") // r12 = max
	// L2 rc-header layout (see __fern_strcat): payload = max data + 1 NUL.
	g.emit("lea edi, [r12 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r13, rax") // r13 = data ptr (= base+8)
	// read(fd, data, max)
	g.emit("mov edi, ebx")
	g.emit("mov rsi, r13")
	g.emit("mov edx, r12d")
	g.emitSyscall(sysRead)
	// Clamp to >= 0 (read returns -errno or 0 on EOF).
	g.emit("test rax, rax")
	g.emit("jns .Ltcp_recv_ok")
	g.emit("xor eax, eax")
	g.label(".Ltcp_recv_ok")
	g.emitStrLenStore("eax", "r13")       // length prefix
	g.emit("mov byte ptr [r13 + rax], 0") // trailing NUL
	g.emit("mov rax, r13")
	g.emit("add rsp, 8")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_tcp_recv, .-__fern_tcp_recv")
}

// emitTcpSendRuntime emits `__fern_tcp_send(fd, data)` —
// writes the full length-prefixed string to the socket.
// Returns the byte count or -errno on the first write.
// Single write(2) call — no buffering / partial-write loop;
// callers needing >page-sized payloads should chunk
// themselves.
func (g *generator) emitTcpSendRuntime() {
	g.line("")
	g.line(".globl __fern_tcp_send")
	g.line(".type __fern_tcp_send, @function")
	g.label("__fern_tcp_send")
	// Frame: 16 bytes — 8 scratch slot for emitStrDataPtr + 8
	// alignment. The materialisation lets an inline-tagged
	// `data` value yield a real byte pointer for the syscall.
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 16")
	g.emitStrLen("edx", "rsi")                  // length from data
	g.emitStrDataPtr("rsi", "rsi", "[rbp - 8]") // byte pointer for syscall
	g.emitSyscall(sysWrite)
	g.emit("add rsp, 16")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_tcp_send, .-__fern_tcp_send")
}

// emitWasmTimerPollableRuntime emits `__fern_wasm_timer_pollable(ns)` — on
// native there is no pollable to create (a deadline is enforced by poll(2)'s
// own timeout arg), so this returns -1, an fd that poll(2) ignores. Lets
// std/async's with_deadline append a "timer" to the poll set portably; on wasm
// this symbol is the real subscribe-duration pollable instead.
func (g *generator) emitWasmTimerPollableRuntime() {
	g.line("")
	g.line(".globl __fern_wasm_timer_pollable")
	g.line(".type __fern_wasm_timer_pollable, @function")
	g.label("__fern_wasm_timer_pollable")
	g.emit("mov eax, -1") // no native pollable; -1 is ignored by poll(2)
	g.emit("ret")
	g.line(".size __fern_wasm_timer_pollable, .-__fern_wasm_timer_pollable")
}

// emitWasmPollRuntime emits `__fern_wasm_poll(pollables)` — on native there are
// no real pollables (a timer pollable is -1 and native readiness rides poll(2)
// directly), so this returns -1 (nothing ready), ignoring its array arg. On wasm
// this symbol is the real wasi:io/poll.poll(list<pollable>) multiplexer instead.
func (g *generator) emitWasmPollRuntime() {
	g.line("")
	g.line(".globl __fern_wasm_poll")
	g.line(".type __fern_wasm_poll, @function")
	g.label("__fern_wasm_poll")
	g.emit("mov eax, -1") // no native pollables; nothing ready
	g.emit("ret")
	g.line(".size __fern_wasm_poll, .-__fern_wasm_poll")
}

// emitWasmPollableDropRuntime emits `__fern_wasm_pollable_drop(p)` — a no-op
// on native (a pollable is just an fd; there's no separate wasi resource to
// drop — the socket fd is closed via tcp_close). Returns 0. Lets std/async's
// fetch_future drop the wasm pollable portably before closing the socket.
func (g *generator) emitWasmPollableDropRuntime() {
	g.line("")
	g.line(".globl __fern_wasm_pollable_drop")
	g.line(".type __fern_wasm_pollable_drop, @function")
	g.label("__fern_wasm_pollable_drop")
	g.emit("xor eax, eax") // return 0 (no-op)
	g.emit("ret")
	g.line(".size __fern_wasm_pollable_drop, .-__fern_wasm_pollable_drop")
}

// emitWasmBlockRuntime emits `__fern_wasm_block(p)` — a no-op on native (there's
// no pollable to wait on; a deadline comes from poll(2)'s own timeout arg).
// Returns 0. Lets std/async's with_deadline block on a timer pollable portably;
// on wasm this symbol is the real wasi:io/poll.[method]pollable.block instead.
func (g *generator) emitWasmBlockRuntime() {
	g.line("")
	g.line(".globl __fern_wasm_block")
	g.line(".type __fern_wasm_block, @function")
	g.label("__fern_wasm_block")
	g.emit("xor eax, eax") // return 0 (no-op)
	g.emit("ret")
	g.line(".size __fern_wasm_block, .-__fern_wasm_block")
}

// emitTcpPollableRuntime emits `__fern_tcp_pollable(fd)` — on native the
// readiness token for a socket IS its file descriptor (poll(2) takes fds
// directly), so this is the identity: return the fd unchanged. It exists
// so `std/async`'s `fetch_future` can build a portable `Pending(tcp_pollable(fd),
// …)` — on wasm `tcp_pollable` returns a real wasi:io/poll pollable handle;
// on native the fd is its own token.
func (g *generator) emitTcpPollableRuntime() {
	g.line("")
	g.line(".globl __fern_tcp_pollable")
	g.line(".type __fern_tcp_pollable, @function")
	g.label("__fern_tcp_pollable")
	g.emit("mov eax, edi") // return the fd argument unchanged
	g.emit("ret")
	g.line(".size __fern_tcp_pollable, .-__fern_tcp_pollable")
}

// emitTcpCloseRuntime emits `__fern_tcp_close(fd)` —
// closes the socket via the close syscall. Returns 0 or
// -errno.
func (g *generator) emitTcpCloseRuntime() {
	g.line("")
	g.line(".globl __fern_tcp_close")
	g.line(".type __fern_tcp_close, @function")
	g.label("__fern_tcp_close")
	g.emitSyscall(sysClose)
	g.emit("ret")
	g.line(".size __fern_tcp_close, .-__fern_tcp_close")
}

// emitEnvRuntime emits `__fern_env(name)` — walks the envp
// vector for NAME=VALUE entries. Returns Option[string]: a
// 16-byte heap object [tag:i32, _pad:i32, str_ptr:i64].
// Payload offset 8 matches the IR's PR #267 layout (8-byte-
// aligned pointer payload).
func (g *generator) emitEnvRuntime() {
	g.line("")
	g.line(".globl __fern_env")
	g.line(".type __fern_env, @function")
	g.label("__fern_env")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")                           // envp cursor
	g.emit("push r12")                           // name byte ptr (materialised, see below)
	g.emit("push r13")                           // name length
	g.emit("push r14")                           // value data ptr (post-strcat)
	g.emit("push r15")                           // value length
	g.emit("sub rsp, 24")                        // 8 bytes scratch for emitStrDataPtr + 16 padding
	g.emitStrLen("r13d", "rdi")                  // r13 = name length (rdi = caller's value)
	g.emitStrDataPtr("r12", "rdi", "[rbp - 48]") // r12 = name byte pointer
	g.emit("mov rbx, [rip + __fern_envp]")
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
	// L2 rc-header layout (see __fern_strcat): payload = N data + 1 NUL.
	g.emit("lea edi, [r15 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov rdi, rax") // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("r15d", "rdi")
	g.emit("mov rsi, r14")
	g.emit("mov rdx, r15")
	g.emit("call __fern_memcpy")
	// rax = data ptr returned from memcpy. Build Option[string]:
	//   16 bytes [tag=0, pad, ptr]
	g.emit("mov r14, rax") // stash str ptr
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // tag = 0 (Some)
	g.emit("mov [rax + 8], r14")     // payload at +8 (8-byte slot)
	g.emit("jmp .Lenv_done")
	g.label(".Lenv_next")
	g.emit("add rbx, 8")
	g.emit("jmp .Lenv_loop")
	g.label(".Lenv_none")
	g.emit("mov edi, 8")
	g.emit("call __fern_alloc_box")
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
	g.line(".size __fern_env, .-__fern_env")
}

// emitArgsRuntime emits `__fern_args()` — returns a length-
// prefixed `string[]` materialised from the argc / argv pair
// captured at `_start`. Each entry is a fresh length-prefixed
// string with a trailing NUL preserved (for libc-shape
// consumers like `puts`). Result is cached in
// `__fern_args_cache` so repeat calls are O(1). Same shape
// arm64 uses (PR #267 ptr-width-stride layout):
//
//	[pad:4 | len:4 | argv0_ptr:8 | argv1_ptr:8 | ...]
//
// data ptr = base + 8 (8-aligned). length prefix at
// `data - 4`. Element stride 8 bytes, one full pointer per
// argv entry.
func (g *generator) emitArgsRuntime() {
	g.line("")
	g.line(".globl __fern_args")
	g.line(".type __fern_args, @function")
	g.label("__fern_args")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")   // argc
	g.emit("push r12")   // argv (char**)
	g.emit("push r13")   // i (loop)
	g.emit("push r14")   // result data ptr
	g.emit("push r15")   // current argv[i] / strlen
	g.emit("sub rsp, 8") // align
	// Fast path: cached?
	g.emit("mov rax, [rip + __fern_args_cache]")
	g.emit("test rax, rax")
	g.emit("jnz .Largs_ret")
	// argc / argv from globals captured by _start.
	g.emit("mov rbx, [rip + __fern_argc]")
	g.emit("mov r12, [rip + __fern_argv]")
	// alloc(argc * 8 + 16) — 16-byte header (pad / cap / rc /
	// len, 4 bytes each) keeps element 0 at a 16-aligned
	// offset; canonical cap / rc / len at data-12 / -8 / -4
	// (Phase 2-prep layout).
	g.emit("lea rdi, [rbx * 8 + 16]")
	g.emit("call __fern_alloc")
	g.emit("lea r14, [rax + 16]")           // r14 = data ptr (16-aligned)
	g.emit("mov dword ptr [r14 - 12], ebx") // cap = argc (Phase 2-prep)
	g.emit("mov dword ptr [r14 - 8], 1")    // rc = 1
	// Outer string[] container length via the array seam (the
	// per-element string stores in the loop below use
	// emitStrLenStore).
	g.emitArrayLenStore("ebx", "r14")
	g.emit("xor r13d, r13d") // i = 0
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
	// L2 rc-header layout (see __fern_strcat): payload = strlen + 1 NUL.
	g.emit("mov rdx, rcx") // save strlen (r15 is C ptr; need it for memcpy)
	g.emit("lea edi, [rcx + 1]")
	g.emit("push rdx")
	g.emit("call __fern_alloc_rc1")
	g.emit("pop rdx")
	g.emit("mov rdi, rax")          // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("edx", "rdi") // length prefix at data-4
	// memcpy(data, argv[i], strlen + 1) — include NUL.
	g.emit("mov rsi, r15") // src = argv[i]
	g.emit("lea rdx, [rdx + 1]")
	g.emit("push rax") // save data ptr across memcpy
	g.emit("call __fern_memcpy")
	g.emit("pop rax") // rax = data ptr again
	// result[i] = data ptr (full 8 bytes — pointer-stride).
	g.emit("mov [r14 + r13*8], rax")
	g.emit("inc r13")
	g.emit("jmp .Largs_loop")
	g.label(".Largs_done")
	g.emit("mov [rip + __fern_args_cache], r14")
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
	g.line(".size __fern_args, .-__fern_args")
}

// emitRandomBytesRuntime emits `__fern_random_bytes(n)` —
// allocates a fresh length-prefixed lang string of n bytes
// and fills it with kernel CSPRNG output via a single
// `getrandom(buf, n, 0)` syscall (Linux x86-64 #318;
// blocks at most very briefly until the urandom pool is
// initialised; flags=0). Returns the data pointer.
func (g *generator) emitRandomBytesRuntime() {
	g.line("")
	g.line(".globl __fern_random_bytes")
	g.line(".type __fern_random_bytes, @function")
	g.label("__fern_random_bytes")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")     // n
	g.emit("push r12")     // data ptr
	g.emit("mov ebx, edi") // rbx = n
	// L2 rc-header layout (see __fern_strcat): payload = n data + 1 NUL.
	g.emit("lea edi, [rbx + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r12, rax")          // r12 = data ptr (= base+8)
	g.emitStrLenStore("ebx", "r12") // length prefix at data-4
	// getrandom(buf=r12, n=rbx, flags=0)
	g.emit("mov rdi, r12")
	g.emit("mov rsi, rbx")
	g.emit("xor edx, edx")
	g.emitSyscall(sysGetrandom)
	// Trailing NUL at data + n. (getrandom doesn't write
	// past the requested length.)
	g.emit("mov byte ptr [r12 + rbx], 0")
	g.emit("mov rax, r12")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_random_bytes, .-__fern_random_bytes")
}

// emitPollRuntime emits `__fern_poll(fds, timeout_ms)` — the readiness
// multiplexer behind the std/task reactor
// (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1). `fds` is a length-
// prefixed i32[] of file descriptors; the helper builds a transient
// `struct pollfd[]` (each 8 bytes: i32 fd, i16 events, i16 revents),
// requests POLLIN on every fd, calls poll(2), and returns the INDEX of
// the first fd that became readable, or -1 on timeout / no readiness.
// The scheduler calls it repeatedly to drain ready fds. The pollfd
// scratch is bump-allocated (reclaimed with the per-request arena).
func (g *generator) emitPollRuntime() {
	const pollin = 1 // POLLIN
	g.line("")
	g.line(".globl __fern_poll")
	g.line(".type __fern_poll, @function")
	g.label("__fern_poll")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // nfds
	g.emit("push r12") // fds data ptr
	g.emit("push r13") // pollfd buffer
	g.emit("push r14") // timeout_ms
	g.emit("push r15") // loop index
	// rdi = fds data ptr, rsi = timeout_ms.
	g.emit("mov r12, rdi")
	g.emit("mov r14, rsi")
	g.emitArrayLen("ebx", "r12") // nfds = [fds - 4]
	// Empty set → nothing to wait on; return -1.
	g.emit("test ebx, ebx")
	g.emit("jle .Lpoll_none")
	// buf = alloc(nfds * 8)
	g.emit("mov edi, ebx")
	g.emit("shl edi, 3")
	g.emit("call __fern_alloc")
	g.emit("mov r13, rax")
	// Marshal: pollfd[i] = { fd=[fds+i*4], events=POLLIN, revents=0 }.
	g.emit("xor r15, r15")
	g.label(".Lpoll_fill")
	g.emit("cmp r15, rbx")
	g.emit("jge .Lpoll_filled")
	g.emit("mov eax, [r12 + r15*4]")                                  // fd
	g.emit("mov [r13 + r15*8], eax")                                  // pollfd.fd
	g.emit(fmt.Sprintf("mov word ptr [r13 + r15*8 + 4], %d", pollin)) // .events
	g.emit("mov word ptr [r13 + r15*8 + 6], 0")                       // .revents
	g.emit("inc r15")
	g.emit("jmp .Lpoll_fill")
	g.label(".Lpoll_filled")
	// poll(buf, nfds, timeout_ms)
	g.emit("mov rdi, r13")
	g.emit("mov esi, ebx")
	g.emit("mov edx, r14d")
	g.emitSyscall(sysPoll)
	// Scan revents for the first POLLIN-ready fd; return its index.
	g.emit("xor r15, r15")
	g.label(".Lpoll_scan")
	g.emit("cmp r15, rbx")
	g.emit("jge .Lpoll_none")
	g.emit("movzx eax, word ptr [r13 + r15*8 + 6]") // revents
	g.emit(fmt.Sprintf("test eax, %d", pollin))
	g.emit("jnz .Lpoll_found")
	g.emit("inc r15")
	g.emit("jmp .Lpoll_scan")
	g.label(".Lpoll_found")
	g.emit("mov rax, r15")
	g.emit("jmp .Lpoll_ret")
	g.label(".Lpoll_none")
	g.emit("mov rax, -1")
	g.label(".Lpoll_ret")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_poll, .-__fern_poll")
}

// emitTimerFdRuntime emits `__fern_timer_fd(ms)` — create a
// CLOCK_MONOTONIC timerfd that becomes readable once after `ms`
// milliseconds, and return its fd (poll/std/reactor can then wait on
// it). Used for reactor timeouts and deterministic readiness tests
// (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c). Negative on error.
func (g *generator) emitTimerFdRuntime() {
	const clockMonotonic = 1
	g.line("")
	g.line(".globl __fern_timer_fd")
	g.line(".type __fern_timer_fd, @function")
	g.label("__fern_timer_fd")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")     // ms
	g.emit("push r12")     // fd
	g.emit("sub rsp, 32")  // struct itimerspec { it_interval, it_value }
	g.emit("mov rbx, rdi") // ms
	// fd = timerfd_create(CLOCK_MONOTONIC, 0)
	g.emit(fmt.Sprintf("mov edi, %d", clockMonotonic))
	g.emit("xor esi, esi")
	g.emitSyscall(sysTimerfdCreate)
	g.emit("mov r12, rax")
	g.emit("test rax, rax")
	g.emit("js .Ltimerfd_ret") // create failed → return -errno
	// it_interval = {0, 0} (one-shot)
	g.emit("xor eax, eax")
	g.emit("mov [rsp], rax")
	g.emit("mov [rsp + 8], rax")
	// it_value = { ms/1000, (ms%1000)*1e6 }
	g.emit("mov rax, rbx")
	g.emit("xor edx, edx")
	g.emit("mov rcx, 1000")
	g.emit("div rcx") // rax = sec, rdx = rem ms
	g.emit("mov [rsp + 16], rax")
	g.emit("mov rax, 1000000")
	g.emit("imul rax, rdx")
	g.emit("mov [rsp + 24], rax")
	// timerfd_settime(fd, 0, &its, NULL)
	g.emit("mov edi, r12d")
	g.emit("xor esi, esi")
	g.emit("mov rdx, rsp")
	g.emit("xor r10d, r10d") // 4th syscall arg is r10
	g.emitSyscall(sysTimerfdSettime)
	g.emit("mov rax, r12") // return fd
	g.label(".Ltimerfd_ret")
	g.emit("add rsp, 32")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_timer_fd, .-__fern_timer_fd")
}

// emitRandomI32Runtime emits `__fern_random_i32()` — returns a
// single cryptographic-quality i32 via one `getrandom(buf, 4, 0)`
// syscall into a 4-byte stack slot, reloaded as a sign-extended
// i32 in eax. Mirrors the interp's `crypto/rand` 4-byte read and
// the wasm `random_get` path. Pointer-clean: the buffer lives in
// the red zone below rsp so no frame setup is needed beyond the
// syscall.
func (g *generator) emitRandomI32Runtime() {
	g.line("")
	g.line(".globl __fern_random_i32")
	g.line(".type __fern_random_i32, @function")
	g.label("__fern_random_i32")
	// getrandom(buf=rsp-8, n=4, flags=0). The 128-byte red zone
	// below rsp is ours; getrandom won't write past n=4 bytes.
	g.emit("lea rdi, [rsp - 8]")
	g.emit("mov esi, 4")
	g.emit("xor edx, edx")
	g.emitSyscall(sysGetrandom)
	g.emit("mov eax, [rsp - 8]") // sign-extends into rax via 32-bit load semantics
	g.emit("ret")
	g.line(".size __fern_random_i32, .-__fern_random_i32")
}

// emitStringAsBytesRuntime emits `__method_string_as_bytes(s)` —
// builds an 8-byte slice header `(data_ptr, len)` aliasing the
// receiver string's bytes (the non-copying `.as_bytes()` view).
// Heap-form strings (LSB tag 0) reuse their data pointer directly;
// SSO inline strings (LSB tag 1) are first promoted to a fresh
// heap buffer so the slice header points at real linear memory
// that outlives the call. Returns the slice header pointer in rax.
// Mirrors wasm's buildStringAsBytesBody.
//
// The heap path is genuinely zero-copy: __fern_slice_make now stores a full
// 8-byte data pointer, so aliasing a string LITERAL's .rodata address works
// even in a PIE shared object loaded at a high base (the earlier 32-bit
// slice field truncated it; superseded by the 64-bit slice header).
func (g *generator) emitStringAsBytesRuntime() {
	g.line("")
	g.line(".globl __method_string_as_bytes")
	g.line(".type __method_string_as_bytes, @function")
	g.label("__method_string_as_bytes")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("sub rsp, 32") // scratch: [rbp-8] inline spill, [rbp-16] len, [rbp-24] data
	g.emit("push rbx")    // callee-saved scratch (preserve 16-byte alignment via even pushes below)
	g.emit("push r12")
	g.emit("mov rbx, rdi") // rbx = string value (inline or heap ptr)
	id := g.labelCounter
	g.labelCounter++
	g.emit("test bl, 1")
	g.emit(fmt.Sprintf("jnz .Lasbytes_inline_%d", id))
	// Heap form: data ptr = value, len = [value-4]. Zero-copy — the slice
	// header carries the full 8-byte pointer.
	g.emit("mov r12, rbx")       // data ptr
	g.emit("mov esi, [rbx - 4]") // len
	g.emit(fmt.Sprintf("jmp .Lasbytes_make_%d", id))
	g.label(fmt.Sprintf(".Lasbytes_inline_%d", id))
	// Inline form: length in bits 1..3, bytes 1..7 of the value.
	g.emit("mov rax, rbx")
	g.emit("shr rax, 1")
	g.emit("and eax, 7")
	g.emit("mov [rbp - 16], eax") // stash len
	// Promote: alloc(len), copy the inline bytes in.
	g.emit("mov edi, eax")
	g.emit("call __fern_alloc")
	g.emit("mov r12, rax")       // r12 = fresh buffer
	g.emit("mov [rbp - 8], rbx") // spill the 8-byte inline value
	// Copy len bytes from [rbp-8 + 1] (skip the tag/len byte) to r12.
	g.emit("mov rdi, r12")
	g.emit("lea rsi, [rbp - 7]")  // &inline_value + 1
	g.emit("mov ecx, [rbp - 16]") // count
	g.emit("cld")
	g.emit("rep movsb")
	g.emit("mov esi, [rbp - 16]") // len for the slice header
	g.label(fmt.Sprintf(".Lasbytes_make_%d", id))
	// __fern_slice_make(data=r12, len=esi) -> header in rax.
	g.emit("mov rdi, r12")
	g.emit("call __fern_slice_make")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("mov rsp, rbp")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __method_string_as_bytes, .-__method_string_as_bytes")
}

// emitReadLineRuntime emits `__fern_read_line()` — reads
// stdin one byte at a time into the 4 KiB
// `__fern_read_line_buf` (.bss), stops at '\n' (kept in
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
	g.line(".globl __fern_read_line")
	g.line(".type __fern_read_line, @function")
	g.label("__fern_read_line")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // buf base
	g.emit("push r12") // bytes-read counter
	g.emit("push r13") // stash (str ptr across alloc, etc.)
	g.emit("sub rsp, 8")
	g.emit("lea rbx, [rip + __fern_read_line_buf]")
	g.emit("xor r12d, r12d") // bytes read = 0
	g.label(".Lrl_loop")
	g.emit("cmp r12, 4096")
	g.emit("jge .Lrl_done")
	// read(0, buf + r12, 1)
	g.emit("xor edi, edi")
	g.emit("lea rsi, [rbx + r12]")
	g.emit("mov edx, 1")
	g.emitSyscall(sysRead)
	// EOF (0) or error (<0) → finish.
	g.emit("cmp rax, 1")
	g.emit("jl .Lrl_done")
	// Examine the just-read byte. r12 not yet incremented;
	// access via [rbx + r12].
	g.emit("mov al, [rbx + r12]")
	g.emit("inc r12")
	g.emit("cmp al, 10") // '\n'
	g.emit("je .Lrl_done")
	g.emit("jmp .Lrl_loop")
	g.label(".Lrl_done")
	// EOF before any byte → return None.
	g.emit("test r12, r12")
	g.emit("jnz .Lrl_some")
	g.emit("mov edi, 4")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // tag = 1 (None)
	g.emit("jmp .Lrl_ret")
	g.label(".Lrl_some")
	// L2 rc-header layout (see __fern_strcat): payload = N data + 1 NUL.
	g.emit("lea edi, [r12 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r13, rax")           // r13 = data ptr (= base+8)
	g.emitStrLenStore("r12d", "r13") // length prefix at data-4
	// memcpy(r13, rbx, r12)
	g.emit("mov rdi, r13")
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, r12")
	g.emit("call __fern_memcpy")
	// Trailing NUL.
	g.emit("mov byte ptr [r13 + r12], 0")
	// Build Option[string]: 16 bytes [tag=0, pad, ptr@+8].
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // tag = 0 (Some)
	g.emit("mov [rax + 8], r13")     // payload at +8 (8-byte slot)
	g.label(".Lrl_ret")
	g.emit("add rsp, 8")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_read_line, .-__fern_read_line")
}

// emitStdinRuntime emits `__fern_stdin()` — a 1-instruction
// stub returning 0. The checker requires `stdin()` to be
// callable but the backend doesn't yet model per-fd
// Readers, so the receiver value is unused; any sentinel
// works. Matches arm64's shape.
func (g *generator) emitStdinRuntime() {
	g.line("")
	g.line(".globl __fern_stdin")
	g.line(".type __fern_stdin, @function")
	g.label("__fern_stdin")
	g.emit("xor eax, eax")
	g.emit("ret")
	g.line(".size __fern_stdin, .-__fern_stdin")
}

// emitAllocU8Runtime emits `__alloc_u8(n)` — allocates a
// fresh length-prefixed `u8[]` of n bytes. Returns the data
// pointer (header + 8); length lives at `[data - 4]`, refcount
// slot at `[data - 8]` (reserved for phase 1; not initialised
// here yet — see docs/RC-PERCEUS-PLAN.md).
func (g *generator) emitAllocU8Runtime() {
	g.line("")
	g.line(".globl __alloc_u8")
	g.line(".type __alloc_u8, @function")
	g.label("__alloc_u8")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("sub rsp, 8")
	g.emit("mov ebx, edi") // rbx = n
	// Short-circuit on n == 0: return the shared static empty-
	// array sentinel rather than allocating a fresh header-only
	// buffer.
	g.emit("test ebx, ebx")
	g.emit("jnz .Lallocu8_alloc")
	g.usesArrEmpty = true
	g.emit("lea rax, [rip + .LArr_Empty]")
	g.emit("jmp .Lallocu8_ret")
	g.label(".Lallocu8_alloc")
	g.emit("lea edi, [rbx + 16]")
	g.emit("call __fern_alloc")
	g.emit("lea rax, [rax + 16]")           // rax = data ptr (past 16-byte header)
	g.emit("mov dword ptr [rax - 12], ebx") // cap = n (Phase 2-prep)
	g.emit("mov dword ptr [rax - 8], 1")    // rc = 1 (phase 1 of RC rollout)
	g.emitArrayLenStore("ebx", "rax")
	// Zero the n data bytes. __fern_alloc may hand back a reused freelist
	// block carrying stale bytes; the interpreter returns a zero-filled
	// `u8[]`, so the AOT backends must too (issue #2768) — code that reads
	// before writing (e.g. SHA padding) depends on it. `rep stosb` from the
	// data pointer; save/restore rax since it's both the cursor seed and the
	// return value.
	g.emit("mov rdx, rax") // save data ptr (return value)
	g.emit("mov rdi, rax")
	g.emit("mov ecx, ebx") // count = n
	g.emit("xor eax, eax") // store byte 0
	g.emit("cld")
	g.emit("rep stosb")
	g.emit("mov rax, rdx") // restore data ptr
	g.label(".Lallocu8_ret")
	g.emit("add rsp, 8")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __alloc_u8, .-__alloc_u8")
}

// emitStringFromBytesRuntime emits `string_from_bytes_unchecked(bs)` —
// copies a `u8[]` payload into a fresh length-prefixed
// string. Round-trip companion to `s.bytes()`.
func (g *generator) emitStringFromBytesRuntime() {
	g.line("")
	g.line(".globl string_from_bytes_unchecked")
	g.line(".type string_from_bytes_unchecked, @function")
	g.label("string_from_bytes_unchecked")
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
	g.emit("call __fern_memcpy")
	g.emit("mov rax, [rbp - 24]")
	g.emit("jmp .Lsfb_ret")
	g.label(".Lsfb_heap")
	// L2 rc-header layout — see __fern_strcat. Request length+1 so the box's
	// size class matches __fern_str_dec's length+1 free (docs/IR-SELFCOMPILE-OOM).
	g.emit("lea edi, [r12 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov rdi, rax")           // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("r12d", "rdi") // length prefix at data-4
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, r12")
	g.emit("push rdi")   // save data ptr across memcpy
	g.emit("sub rsp, 8") // align
	g.emit("call __fern_memcpy")
	g.emit("add rsp, 8")
	g.emit("pop rax") // rax = data ptr (return value)
	g.label(".Lsfb_ret")
	g.emit("add rsp, 16")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size string_from_bytes_unchecked, .-string_from_bytes_unchecked")
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
	g.emit("mov rbx, rdi") // base (possibly inline-tagged)
	// Sign-extend the i32 bounds from their low 32 bits (#5294): a negative
	// i32 constant materialises zero-extended (mov eax, N), so `len + (-2)`
	// reaches here as 0x1_0000_0003 and the (partly-unsigned) 64-bit bounds
	// compares below miss the trap — the slice then reads out of bounds.
	// movsxd is a no-op for a clean bound, so a correct slice is unchanged.
	g.emit("movsxd r12, esi")   // low (sign-extended from i32)
	g.emit("movsxd r13, edx")   // high (sign-extended from i32)
	g.emitStrLen("r14d", "rbx") // src_len
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
	g.emit("call __fern_memcpy")
	g.emit("mov rax, [rbp - 48]")
	g.emit("jmp .Lstrslice_ret")
	g.label(".Lstrslice_heap")
	// --- Heap output path (L2 rc-header layout — see __fern_strcat). ---
	// alloc_rc1(new_len): rc + length share the 8-byte header, data at
	// base+8 = rax. emitStrLenStore writes length at [data-4] = base+4,
	// clobbering rc1's stashed payload-size slot (string-drop computes alloc
	// size from length+1, not from data-4). Request length+1 to match.
	g.emit("lea edi, [r14 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov rdi, rax")           // rdi = data ptr (= memcpy dst)
	g.emitStrLenStore("r14d", "rdi") // length prefix at data-4
	g.emit("lea rsi, [rbx + r12]")   // src = base_byte_ptr + low
	g.emit("mov rdx, r14")
	g.emit("push rdi")   // save data ptr
	g.emit("sub rsp, 8") // align
	g.emit("call __fern_memcpy")
	g.emit("add rsp, 8")
	g.emit("pop rax") // rax = data ptr
	g.label(".Lstrslice_ret")
	g.emit("add rsp, 16")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.label(".Lstrslice_trap")
	g.emitAbort("__fern_msg_str_slice")
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

	// `__load_i64` / `__store_i64` — 8-byte load / store.
	// Used by the Map runtime's wide-scalar-boxed key path
	// (keyKind=2) to dereference an i64 / u64 / f64 key
	// from a heap cell. On x86-64 a usize is already 8 bytes
	// so the lang-level Map[i64, _] path stays on keyKind=0
	// without these — the symbols still need linkable
	// bodies because the stdlib references them by name
	// regardless of target.
	g.line("")
	g.line(".globl __load_i64")
	g.line(".type __load_i64, @function")
	g.label("__load_i64")
	g.emit("mov rax, [rdi]")
	g.emit("ret")
	g.line(".size __load_i64, .-__load_i64")

	g.line("")
	g.line(".globl __store_i64")
	g.line(".type __store_i64, @function")
	g.label("__store_i64")
	g.emit("mov [rdi], rsi")
	g.emit("ret")
	g.line(".size __store_i64, .-__store_i64")

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
	g.emit("movzx ecx, sil") // ecx = byte (zero-extended)
	g.emit("mov rax, 0x0101010101010101")
	g.emit("imul rax, rcx") // rax = byte replicated 8x
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

// emitIoErrorRuntime emits `__fern_io_error(errno, path) → ptr`
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
	g.line(".globl __fern_io_error")
	g.line(".type __fern_io_error, @function")
	g.label("__fern_io_error")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")     // callee-save
	g.emit("push r12")     // callee-save
	g.emit("sub rsp, 8")   // 16-byte align
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
	g.emit("call __fern_alloc_box")
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
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 4")
	g.emit("jmp .Lioe_done")

	g.label(".Lioe_with_path")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc")
	g.emit("mov [rax], ebx")     // tag
	g.emit("mov [rax + 8], r12") // path

	g.label(".Lioe_done")
	g.emit("add rsp, 8")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_io_error, .-__fern_io_error")

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

// emitReadFileRuntime emits `__fern_read_file(path) →
// Result[string, IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_RDONLY) → fstat → alloc length-prefixed buffer → read-loop
// → close → Result.Ok(string). Syscall errors short-circuit to
// Result.Err via __fern_io_error.
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
	g.line(".globl __fern_read_file")
	g.line(".type __fern_read_file, @function")
	g.label("__fern_read_file")
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
	g.emit("mov [rbp - 56], rdi")                // save original path string value for the err path
	g.emitStrDataPtr("rbx", "rdi", "[rbp - 48]") // path byte ptr for openat

	// openat(AT_FDCWD=-100, path, O_RDONLY=0, 0)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("xor edx, edx")
	g.emit("xor r10d, r10d")
	g.emitSyscall(257)
	g.emit("test rax, rax")
	g.emit("js .Lrf_err_open")
	g.emit("mov r12, rax") // fd

	// fstat(fd, [rsp]) — statbuf at top of stack (152 bytes).
	g.emit("mov edi, r12d")
	g.emit("mov rsi, rsp")
	g.emitSyscall(5)
	g.emit("test rax, rax")
	g.emit("js .Lrf_err_close")
	g.emit("mov r14, [rsp + 48]") // st_size

	// L2 rc-header layout (see __fern_strcat): payload = size data + NUL slack
	// so the box class matches __fern_str_dec's length+1 free.
	g.emit("lea edi, [r14 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r13, rax") // r13 = data ptr (= base+8)
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
	g.emitSyscallPreloaded(sysRead)
	g.emit("test rax, rax")
	g.emit("js .Lrf_err_close")
	g.emit("jz .Lrf_done") // EOF (file shrunk between fstat and read)
	g.emit("add r15, rax")
	g.emit("jmp .Lrf_loop")

	g.label(".Lrf_done")
	g.emit("mov edi, r12d")
	g.emitSyscall(3)
	// Result.Ok(string): 16-byte box, tag=0 @0, str_ptr @8.
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0")
	g.emit("mov [rax + 8], r13") // r13 is already the data ptr
	g.emit("jmp .Lrf_return")

	g.label(".Lrf_err_close")
	// errno = -rax, then close fd.
	g.emit("neg rax")
	g.emit("mov r13, rax") // r13 = errno (buf base no longer needed)
	g.emit("mov edi, r12d")
	g.emitSyscall(3)
	g.emit("jmp .Lrf_err_dispatch")

	g.label(".Lrf_err_open")
	g.emit("neg rax")
	g.emit("mov r13, rax")

	g.label(".Lrf_err_dispatch")
	// __fern_io_error(errno, path) → rax = IoError box.
	g.emit("mov edi, r13d")
	g.emit("mov rsi, [rbp - 56]") // original path string value (heap or inline)
	g.emit("call __fern_io_error")
	g.emit("mov r13, rax") // stash IoError box across the next alloc
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
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
	g.line(".size __fern_read_file, .-__fern_read_file")
}

// emitWriteFileRuntime emits `__fern_write_file(path, content)
// → Option[IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_WRONLY|O_CREAT|O_TRUNC=577, 0644) → write-loop → close →
// None. Errors → Some(IoError).
//
// Option[IoError] layout:
//
//	tag=0 (Some) → payload@+8 = IoError box ptr
//	tag=1 (None) → 8-byte box, no payload
func (g *generator) emitWriteFileRuntime() {
	g.emitWriteFileRuntimeMode("__fern_write_file", "0644", "", "")
}

// emitWriteFileRuntimeMode is emitWriteFileRuntime parameterised by symbol and
// creation mode, so `write_file` (0644) and `write_file_exec` (0755) share one
// body rather than a copy that can drift. `mode` is an octal literal, as GAS
// reads a leading zero. `sfx` keeps the two copies' `.Lwf*` labels distinct —
// without it a program using both emits each label twice, and the assembler
// either rejects it or, worse, resolves the second copy's branches into the
// first copy's body (#6133).
func (g *generator) emitWriteFileRuntimeMode(sym, mode, sfx, fixupMode string) {
	g.line("")
	g.line(".globl " + sym)
	g.line(".type " + sym + ", @function")
	g.label(sym)
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")                           // path byte ptr (materialised)
	g.emit("push r12")                           // content byte ptr (materialised)
	g.emit("push r13")                           // fd
	g.emit("push r14")                           // content_len
	g.emit("push r15")                           // bytes_written
	g.emit("sub rsp, 24")                        // 16 bytes scratch (path + content emitStrDataPtr) + 8 for original path value.
	g.emit("mov [rbp - 64], rdi")                // save original path string value for __fern_io_error
	g.emitStrLen("r14d", "rsi")                  // content_len (from caller's rsi before materialise)
	g.emitStrDataPtr("rbx", "rdi", "[rbp - 48]") // path byte ptr
	g.emitStrDataPtr("r12", "rsi", "[rbp - 56]") // content byte ptr

	// openat(AT_FDCWD, path, O_WRONLY|O_CREAT|O_TRUNC=577, 0644)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("mov edx, 577")
	g.emit("mov r10d, " + mode)
	g.emitSyscall(257)
	g.emit("test rax, rax")
	g.emit("js .Lwf_err_open" + sfx)
	g.emit("mov r13, rax") // fd

	g.emit("xor r15, r15")
	g.label(".Lwf_loop" + sfx)
	g.emit("cmp r15, r14")
	g.emit("jge .Lwf_done" + sfx)
	g.emit("mov edi, r13d")
	g.emit("lea rsi, [r12 + r15]")
	g.emit("mov rdx, r14")
	g.emit("sub rdx, r15")
	g.emitSyscall(1)
	g.emit("test rax, rax")
	g.emit("js .Lwf_err_close" + sfx)
	g.emit("add r15, rax")
	g.emit("jmp .Lwf_loop" + sfx)

	g.label(".Lwf_done" + sfx)
	if fixupMode != "" {
		// openat's mode applies only when it CREATES the file, so writing
		// over a stale output leaves the old mode — for write_file_exec
		// that means an unrunnable binary, the very failure it removes.
		// fchmod(fd, mode) before close; its result is ignored, as the
		// bytes are already written and a mode failure must not turn a
		// successful write into an Err. Plain write_file passes "" here
		// and keeps preserving an existing file's mode, matching
		// os.WriteFile in the interpreter. (#6133)
		g.emit("mov edi, r13d")
		g.emit("mov esi, " + fixupMode)
		g.emitSyscall(91) // fchmod
	}
	g.emit("mov edi, r13d")
	g.emitSyscall(3)
	// Result.Ok(()): 16-byte box, tag=0, unit payload @+8. The unit
	// value occupies a payload slot like any other value — the reader
	// loads it by the declared layout — so the success arm cannot be
	// the 8-byte tag-only box the Option shape used.
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0")     // tag = 0 (Ok)
	g.emit("mov qword ptr [rax + 8], 0") // unit payload
	g.emit("jmp .Lwf_return" + sfx)

	g.label(".Lwf_err_close" + sfx)
	g.emit("neg rax")
	g.emit("mov r14, rax") // errno
	g.emit("mov edi, r13d")
	g.emitSyscall(3)
	g.emit("jmp .Lwf_err_dispatch" + sfx)

	g.label(".Lwf_err_open" + sfx)
	g.emit("neg rax")
	g.emit("mov r14, rax")

	g.label(".Lwf_err_dispatch" + sfx)
	g.emit("mov edi, r14d")
	g.emit("mov rsi, [rbp - 64]") // original path string value (heap or inline)
	g.emit("call __fern_io_error")
	g.emit("mov r14, rax") // stash IoError box
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // tag = 1 (Err)
	g.emit("mov [rax + 8], r14")

	g.label(".Lwf_return" + sfx)
	g.emit("add rsp, 24")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size " + sym + ", .-" + sym)
}

// emitRemoveDirAllRuntime emits
// `__fern_remove_dir_all(path) → Option[IoError]` — a recursive
// `rm -rf`. It's the x86-64 sibling of arm64-ssa's
// emitRemoveDirAllHelper: syscalls are inlined and the helper
// self-recurses per directory entry, so it pulls in no separate
// read_dir/stat helpers. Pipeline:
//
//	openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY, 0)
//	  fd >= 0        → it's a directory: drain entries, recurse
//	                   on each non-dot child, close, rmdir → None
//	  -ENOENT (-2)   → already gone → None
//	  -ENOTDIR (-20) → it's a file: unlinkat(file) → None
//	  else           → Some(IoError) via __fern_io_error
//
// Option[IoError] layout matches write_file: None = 8-byte box
// tag=1; Some = 16-byte box tag=0 with the IoError box @+8.
//
// The path is copied into a NUL-terminated heap buffer (pathz,
// rbx) once at entry — handling both inline-SSO and heap string
// inputs — and every syscall + child-path build reads from pathz.
// Child paths "pathz/name" are freshly-allocated single-word rc
// strings passed to the recursion (leaked one-level, same as the
// arm64 helper and the other drop paths). Callee-saved across the
// recursion: rbx=pathz, r12=dir fd, r13=dirent buf, r14=total,
// r15=offset. System V: rdi = path string value.
func (g *generator) emitRemoveDirAllRuntime() {
	g.line("")
	g.line(".globl __fern_remove_dir_all")
	g.line(".type __fern_remove_dir_all, @function")
	g.label("__fern_remove_dir_all")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // pathz (NUL-terminated path buffer)
	g.emit("push r12") // dir fd
	g.emit("push r13") // dirent buffer base
	g.emit("push r14") // total bytes drained
	g.emit("push r15") // iteration offset
	// Frame (5 pushes ⇒ rsp≡8 mod 16; sub 40 realigns to 0). The
	// scratch slots live BELOW the saved registers (which occupy
	// rbp-8..rbp-40), so they start at rbp-48:
	//   [rbp-48] emitStrDataPtr inline-spill scratch
	//   [rbp-56] child name ptr    (across __fern_alloc_rc1)
	//   [rbp-64] plen = strlen(pathz)
	//   [rbp-72] nlen = strlen(name)
	g.emit("sub rsp, 40")

	// Materialise the incoming path into pathz (NUL-terminated).
	// r12/r13 are callee-saved so they survive the __fern_alloc.
	g.emitStrLen("r13d", "rdi")                  // r13 = path len
	g.emitStrDataPtr("r12", "rdi", "[rbp - 48]") // r12 = path byte ptr
	g.emit("lea edi, [r13 + 1]")                 // len + NUL
	g.emit("call __fern_alloc")
	g.emit("mov rbx, rax") // rbx = pathz
	g.emit("xor ecx, ecx")
	g.label(".Lrda_cp")
	g.emit("cmp rcx, r13")
	g.emit("jae .Lrda_cpd")
	g.emit("mov al, [r12 + rcx]")
	g.emit("mov [rbx + rcx], al")
	g.emit("add rcx, 1")
	g.emit("jmp .Lrda_cp")
	g.label(".Lrda_cpd")
	g.emit("mov byte ptr [rbx + r13], 0") // NUL-terminate

	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY=0x10000, 0)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("mov edx, 0x10000")
	g.emit("xor r10d, r10d")
	g.emitSyscall(257)
	g.emit("test rax, rax")
	g.emit("jns .Lrda_dir") // fd >= 0 → directory
	g.emit("cmp rax, -2")   // -ENOENT → already gone
	g.emit("je .Lrda_none")
	g.emit("cmp rax, -20") // -ENOTDIR → it's a file
	g.emit("jne .Lrda_some")
	// unlinkat(AT_FDCWD, pathz, 0) — remove the file.
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("xor edx, edx")
	g.emitSyscall(263)
	g.emit("jmp .Lrda_none")

	g.label(".Lrda_dir")
	g.emit("mov r12, rax") // r12 = dir fd
	// Allocate a 1 KiB dirent buffer (r13) and drain the directory.
	g.emit("mov edi, 1024")
	g.emit("call __fern_alloc")
	g.emit("mov r13, rax")
	g.emit("xor r14, r14") // total
	g.label(".Lrda_g")
	g.emit("mov edx, 1024")
	g.emit("sub rdx, r14")
	g.emit("jz .Lrda_gd") // buffer full → stop (small-tree cap)
	g.emit("mov edi, r12d")
	g.emit("lea rsi, [r13 + r14]")
	g.emitSyscall(217)
	g.emit("test rax, rax")
	g.emit("jle .Lrda_gd") // 0 (end) or <0 (error) → stop draining
	g.emit("add r14, rax")
	g.emit("jmp .Lrda_g")

	g.label(".Lrda_gd")
	g.emit("xor r15, r15") // offset
	g.label(".Lrda_it")
	g.emit("cmp r15, r14")
	g.emit("jae .Lrda_itd")
	g.emit("lea rax, [r13 + r15]")
	g.emit("lea rsi, [rax + 19]") // d_name ptr
	g.emit("movzx ecx, byte ptr [rsi]")
	g.emit("cmp cl, 46") // '.'
	g.emit("jne .Lrda_ch")
	g.emit("movzx ecx, byte ptr [rsi + 1]")
	g.emit("test cl, cl")
	g.emit("jz .Lrda_adv") // "."
	g.emit("cmp cl, 46")
	g.emit("jne .Lrda_ch")
	g.emit("movzx ecx, byte ptr [rsi + 2]")
	g.emit("test cl, cl")
	g.emit("jz .Lrda_adv") // ".."
	g.label(".Lrda_ch")
	g.emit("mov [rbp - 56], rsi") // name ptr (survives __fern_alloc_rc1)
	// plen = strlen(pathz)
	g.emit("xor rcx, rcx")
	g.label(".Lrda_pl")
	g.emit("cmp byte ptr [rbx + rcx], 0")
	g.emit("je .Lrda_pld")
	g.emit("add rcx, 1")
	g.emit("jmp .Lrda_pl")
	g.label(".Lrda_pld")
	g.emit("mov [rbp - 64], rcx") // plen
	// nlen = strlen(name)
	g.emit("xor rdx, rdx")
	g.label(".Lrda_nl")
	g.emit("cmp byte ptr [rsi + rdx], 0")
	g.emit("je .Lrda_nld")
	g.emit("add rdx, 1")
	g.emit("jmp .Lrda_nl")
	g.label(".Lrda_nld")
	g.emit("mov [rbp - 72], rdx") // nlen
	// childlen = plen + 1 + nlen; alloc single-word rc string (childlen+NUL).
	g.emit("lea edi, [rcx + rdx + 2]") // childlen + 1
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r8, rax")              // child data ptr (r8 caller-saved, dead by recursion)
	g.emit("mov rcx, [rbp - 64]")      // plen
	g.emit("mov rdx, [rbp - 72]")      // nlen
	g.emit("lea eax, [rcx + rdx + 1]") // childlen
	g.emitStrLenStore("eax", "r8")
	// copy pathz[0..plen]
	g.emit("xor r9, r9")
	g.label(".Lrda_c1")
	g.emit("cmp r9, rcx")
	g.emit("jae .Lrda_c1d")
	g.emit("mov al, [rbx + r9]")
	g.emit("mov [r8 + r9], al")
	g.emit("add r9, 1")
	g.emit("jmp .Lrda_c1")
	g.label(".Lrda_c1d")
	g.emit("mov byte ptr [r8 + rcx], 47") // '/'
	// copy name at plen+1
	g.emit("mov rsi, [rbp - 56]") // name ptr
	g.emit("xor r9, r9")
	g.label(".Lrda_c2")
	g.emit("cmp r9, rdx")
	g.emit("jae .Lrda_c2d")
	g.emit("mov al, [rsi + r9]")
	g.emit("lea r10, [rcx + r9 + 1]")
	g.emit("mov [r8 + r10], al")
	g.emit("add r9, 1")
	g.emit("jmp .Lrda_c2")
	g.label(".Lrda_c2d")
	g.emit("lea r10, [rcx + rdx + 1]") // childlen
	g.emit("mov byte ptr [r8 + r10], 0")
	// recurse: remove_dir_all(child).
	g.emit("mov rdi, r8")
	g.emit("call __fern_remove_dir_all")
	g.label(".Lrda_adv")
	g.emit("movzx eax, word ptr [r13 + r15 + 16]") // d_reclen
	g.emit("add r15, rax")
	g.emit("jmp .Lrda_it")

	g.label(".Lrda_itd")
	// close(fd), then rmdir the now-empty directory.
	g.emit("mov edi, r12d")
	g.emitSyscall(3)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("mov edx, 512") // AT_REMOVEDIR
	g.emitSyscall(263)

	g.label(".Lrda_none")
	// Result.Ok(()): 16-byte box, tag=0, unit payload @+8. The unit
	// value occupies a payload slot like any other value — the reader
	// loads it by the declared layout — so the success arm cannot be
	// the 8-byte tag-only box the Option shape used.
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0")     // tag = 0 (Ok)
	g.emit("mov qword ptr [rax + 8], 0") // unit payload
	g.emit("jmp .Lrda_return")

	g.label(".Lrda_some")
	g.emit("neg rax")
	g.emit("mov r12, rax") // errno (r12 free — never opened a fd on this path)
	g.emit("mov edi, r12d")
	g.emitStrEmpty("rsi") // io_error path arg (empty, as arm64 does)
	g.emit("call __fern_io_error")
	g.emit("mov r12, rax") // stash IoError box across the alloc
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // tag = 1 (Err)
	g.emit("mov [rax + 8], r12")

	g.label(".Lrda_return")
	g.emit("add rsp, 40")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_remove_dir_all, .-__fern_remove_dir_all")
}

// emitRemoveFileRuntime emits `__fern_remove_file(path) →
// Option[IoError]` — unlinkat(AT_FDCWD, path, 0). None on
// success; Some(IoError) on failure (removing a missing file IS
// an error, matching the checker's contract — unlike
// remove_dir_all's silent-ENOENT). Box shapes match write_file:
// None = 8-byte box tag=1; Some = 16-byte box tag=0 with the
// IoError box @+8. System V: rdi = path string value.
func (g *generator) emitRemoveFileRuntime() {
	g.line("")
	g.line(".globl __fern_remove_file")
	g.line(".type __fern_remove_file, @function")
	g.label("__fern_remove_file")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // path byte ptr
	g.emit("push r12") // path len / errno
	g.emit("push r13") // pathz
	// 4 pushes ⇒ rsp≡8 mod 16; sub 24 realigns. Slots:
	//   [rbp-32] emitStrDataPtr inline-spill scratch
	//   [rbp-40] original path string value (io_error arg)
	g.emit("sub rsp, 24")
	g.emit("mov [rbp - 40], rdi")
	g.emitStrLen("r12d", "rdi")
	g.emitStrDataPtr("rbx", "rdi", "[rbp - 32]")
	// pathz = NUL-terminated heap copy of the path.
	g.emit("lea edi, [r12 + 1]")
	g.emit("call __fern_alloc")
	g.emit("mov r13, rax")
	g.emit("xor ecx, ecx")
	g.label(".Lrmf_cp")
	g.emit("cmp rcx, r12")
	g.emit("jae .Lrmf_cpd")
	g.emit("mov al, [rbx + rcx]")
	g.emit("mov [r13 + rcx], al")
	g.emit("add rcx, 1")
	g.emit("jmp .Lrmf_cp")
	g.label(".Lrmf_cpd")
	g.emit("mov byte ptr [r13 + r12], 0")
	// unlinkat(AT_FDCWD=-100, pathz, 0)
	g.emit("mov edi, -100")
	g.emit("mov rsi, r13")
	g.emit("xor edx, edx")
	g.emitSyscall(263)
	g.emit("test rax, rax")
	g.emit("js .Lrmf_some")
	// Result.Ok(()): 16-byte box, tag=0, unit payload @+8. The unit
	// value occupies a payload slot like any other value — the reader
	// loads it by the declared layout — so the success arm cannot be
	// the 8-byte tag-only box the Option shape used.
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0")     // tag = 0 (Ok)
	g.emit("mov qword ptr [rax + 8], 0") // unit payload
	g.emit("jmp .Lrmf_return")

	g.label(".Lrmf_some")
	g.emit("neg rax")
	g.emit("mov r12, rax")
	g.emit("mov edi, r12d")
	g.emit("mov rsi, [rbp - 40]")
	g.emit("call __fern_io_error")
	g.emit("mov r12, rax") // stash IoError box across the alloc
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // tag = 1 (Err)
	g.emit("mov [rax + 8], r12")

	g.label(".Lrmf_return")
	g.emit("add rsp, 24")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_remove_file, .-__fern_remove_file")
}

// emitTempDirRuntime emits `__fern_temp_dir(prefix) →
// Result[string, IoError]` — creates "/tmp/<prefix>-<ns>" (ns =
// __fern_monotonic_ns, decimal digits, so concurrent runs don't
// clash) via mkdirat and returns Ok(path). The path is built in a
// plain scratch buffer first, then copied into an exactly-sized
// rc=1 string so the Ok payload's length prefix matches its
// allocation (the box-free path sizes the block from data-4).
// Result box: Ok = 16-byte tag=0 + string data ptr @+8; Err =
// 16-byte tag=1 + IoError box @+8 (same as read_file).
// System V: rdi = prefix string value.
func (g *generator) emitTempDirRuntime() {
	g.line("")
	g.line(".globl __fern_temp_dir")
	g.line(".type __fern_temp_dir, @function")
	g.label("__fern_temp_dir")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // prefix data ptr / final string
	g.emit("push r12") // prefix len / errno
	g.emit("push r13") // scratch path buffer
	g.emit("push r14") // total path len
	g.emit("push r15") // ns
	// 6 pushes ⇒ rsp≡8 mod 16; sub 40 realigns. Slots:
	//   [rbp-56] emitStrDataPtr inline-spill scratch
	//   [rbp-64] original prefix string value (io_error arg)
	g.emit("sub rsp, 40")
	g.emit("mov [rbp - 64], rdi")
	g.emitStrLen("r12d", "rdi")
	g.emitStrDataPtr("rbx", "rdi", "[rbp - 56]")
	g.emit("call __fern_monotonic_ns")
	g.emit("mov r15, rax")
	// Scratch: 5 ("/tmp/") + plen + 1 ('-') + 20 (max digits) + 1 NUL.
	g.emit("lea edi, [r12 + 27]")
	g.emit("call __fern_alloc")
	g.emit("mov r13, rax")
	g.emit("mov byte ptr [r13], 47")      // '/'
	g.emit("mov byte ptr [r13 + 1], 116") // 't'
	g.emit("mov byte ptr [r13 + 2], 109") // 'm'
	g.emit("mov byte ptr [r13 + 3], 112") // 'p'
	g.emit("mov byte ptr [r13 + 4], 47")  // '/'
	g.emit("mov r14d, 5")
	// Append the prefix bytes.
	g.emit("xor ecx, ecx")
	g.label(".Ltd_pcp")
	g.emit("cmp rcx, r12")
	g.emit("jae .Ltd_pcpd")
	g.emit("mov al, [rbx + rcx]")
	g.emit("mov [r13 + r14], al")
	g.emit("add r14, 1")
	g.emit("add rcx, 1")
	g.emit("jmp .Ltd_pcp")
	g.label(".Ltd_pcpd")
	g.emit("mov byte ptr [r13 + r14], 45") // '-'
	g.emit("add r14, 1")
	// Count decimal digits of ns into r9 (do-while: ns=0 → 1).
	g.emit("mov rax, r15")
	g.emit("xor r9d, r9d")
	g.label(".Ltd_cnt")
	g.emit("xor edx, edx")
	g.emit("mov ecx, 10")
	g.emit("div rcx")
	g.emit("add r9, 1")
	g.emit("test rax, rax")
	g.emit("jnz .Ltd_cnt")
	// Write the digits least-significant-first into
	// [r14 .. r14+r9-1], then advance the cursor.
	g.emit("lea r10, [r14 + r9 - 1]")
	g.emit("mov rax, r15")
	g.label(".Ltd_wr")
	g.emit("xor edx, edx")
	g.emit("mov ecx, 10")
	g.emit("div rcx")
	g.emit("add dl, 48")
	g.emit("mov [r13 + r10], dl")
	g.emit("sub r10, 1")
	g.emit("test rax, rax")
	g.emit("jnz .Ltd_wr")
	g.emit("add r14, r9")
	g.emit("mov byte ptr [r13 + r14], 0") // NUL
	// mkdirat(AT_FDCWD=-100, pathz, 0700=448)
	g.emit("mov edi, -100")
	g.emit("mov rsi, r13")
	g.emit("mov edx, 448")
	g.emitSyscall(258)
	g.emit("test rax, rax")
	g.emit("jnz .Ltd_err")
	// Ok: copy the path into an exactly-sized rc=1 string.
	g.emit("lea edi, [r14 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov rbx, rax")
	g.emitStrLenStore("r14d", "rbx")
	g.emit("xor ecx, ecx")
	g.label(".Ltd_ccp")
	g.emit("cmp rcx, r14")
	g.emit("jae .Ltd_ccpd")
	g.emit("mov al, [r13 + rcx]")
	g.emit("mov [rbx + rcx], al")
	g.emit("add rcx, 1")
	g.emit("jmp .Ltd_ccp")
	g.label(".Ltd_ccpd")
	g.emit("mov byte ptr [rbx + r14], 0")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // Ok
	g.emit("mov [rax + 8], rbx")
	g.emit("jmp .Ltd_return")

	g.label(".Ltd_err")
	g.emit("neg rax")
	g.emit("mov r12, rax")
	g.emit("mov edi, r12d")
	g.emit("mov rsi, [rbp - 64]")
	g.emit("call __fern_io_error")
	g.emit("mov r12, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // Err
	g.emit("mov [rax + 8], r12")

	g.label(".Ltd_return")
	g.emit("add rsp, 40")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_temp_dir, .-__fern_temp_dir")
}

// emitReadDirRuntime emits `__fern_read_dir(path) →
// Result[string[], IoError]` — lists the non-recursive children
// of `path` as base names (unsorted). Pipeline: openat(O_RDONLY|
// O_DIRECTORY) → getdents64-drain into a 1 MiB heap buffer →
// close → pass 1 counts entries (skipping "." / "..") → array
// alloc (canonical layout: 16-byte header, cap@data-12,
// rc=1@data-8, len@data-4, 8-byte string-ptr elements) → pass 2
// fills with fresh rc=1 strings. openat failure → Err(IoError).
// System V: rdi = path string value.
func (g *generator) emitReadDirRuntime() {
	g.line("")
	g.line(".globl __fern_read_dir")
	g.line(".type __fern_read_dir, @function")
	g.label("__fern_read_dir")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // pathz, then array data ptr
	g.emit("push r12") // path byte ptr / fd / count / index
	g.emit("push r13") // path len / dirent buf
	g.emit("push r14") // total bytes drained
	g.emit("push r15") // iteration offset
	// 6 pushes ⇒ rsp≡8 mod 16; sub 40 realigns. Slots:
	//   [rbp-56] emitStrDataPtr inline-spill scratch
	//   [rbp-64] original path string value (io_error arg)
	//   [rbp-72] nlen / name ptr scratch
	//   [rbp-80] name ptr (across __fern_alloc_rc1)
	g.emit("sub rsp, 40")
	g.emit("mov [rbp - 64], rdi")
	g.emitStrLen("r13d", "rdi")
	g.emitStrDataPtr("r12", "rdi", "[rbp - 56]")
	// pathz = NUL-terminated heap copy.
	g.emit("lea edi, [r13 + 1]")
	g.emit("call __fern_alloc")
	g.emit("mov rbx, rax")
	g.emit("xor ecx, ecx")
	g.label(".Lrdd_cp")
	g.emit("cmp rcx, r13")
	g.emit("jae .Lrdd_cpd")
	g.emit("mov al, [r12 + rcx]")
	g.emit("mov [rbx + rcx], al")
	g.emit("add rcx, 1")
	g.emit("jmp .Lrdd_cp")
	g.label(".Lrdd_cpd")
	g.emit("mov byte ptr [rbx + r13], 0")
	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY=0x10000, 0)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("mov edx, 0x10000")
	g.emit("xor r10d, r10d")
	g.emitSyscall(257)
	g.emit("test rax, rax")
	g.emit("js .Lrdd_err")
	g.emit("mov r12, rax") // fd
	// 1 MiB dirent buffer (mirrors the self-host helper's cap).
	g.emit("mov edi, 1048576")
	g.emit("call __fern_alloc")
	g.emit("mov r13, rax")
	g.emit("xor r14, r14")
	g.label(".Lrdd_g")
	g.emit("mov edx, 1048576")
	g.emit("sub rdx, r14")
	g.emit("jz .Lrdd_gd")
	g.emit("mov edi, r12d")
	g.emit("lea rsi, [r13 + r14]")
	g.emitSyscall(217)
	g.emit("test rax, rax")
	g.emit("jle .Lrdd_gd")
	g.emit("add r14, rax")
	g.emit("jmp .Lrdd_g")
	g.label(".Lrdd_gd")
	g.emit("mov edi, r12d")
	g.emitSyscall(3)
	// Pass 1: count entries that aren't "." / "..".
	g.emit("xor r12d, r12d") // count (fd is closed)
	g.emit("xor r15, r15")   // offset
	g.label(".Lrdd_c1")
	g.emit("cmp r15, r14")
	g.emit("jae .Lrdd_c1d")
	g.emit("lea rsi, [r13 + r15 + 19]") // d_name ptr
	g.emit("movzx ecx, byte ptr [rsi]")
	g.emit("cmp cl, 46") // '.'
	g.emit("jne .Lrdd_c1n")
	g.emit("movzx ecx, byte ptr [rsi + 1]")
	g.emit("test cl, cl")
	g.emit("jz .Lrdd_c1s") // "."
	g.emit("cmp cl, 46")
	g.emit("jne .Lrdd_c1n")
	g.emit("movzx ecx, byte ptr [rsi + 2]")
	g.emit("test cl, cl")
	g.emit("jz .Lrdd_c1s") // ".."
	g.label(".Lrdd_c1n")
	g.emit("add r12, 1")
	g.label(".Lrdd_c1s")
	g.emit("movzx eax, word ptr [r13 + r15 + 16]") // d_reclen
	g.emit("add r15, rax")
	g.emit("jmp .Lrdd_c1")
	g.label(".Lrdd_c1d")
	// Array alloc: 16-byte header + count * 8. pathz (rbx) is dead
	// past openat, so rbx becomes the array data ptr.
	g.emit("lea rdi, [r12 * 8 + 16]")
	g.emit("call __fern_alloc")
	g.emit("lea rbx, [rax + 16]")
	g.emit("mov dword ptr [rbx - 12], r12d") // cap = count
	g.emit("mov dword ptr [rbx - 8], 1")     // rc = 1
	g.emitArrayLenStore("r12d", "rbx")
	// Pass 2: fill with fresh rc=1 strings.
	g.emit("xor r12d, r12d") // element index
	g.emit("xor r15, r15")   // offset
	g.label(".Lrdd_p2")
	g.emit("cmp r15, r14")
	g.emit("jae .Lrdd_p2d")
	g.emit("lea rsi, [r13 + r15 + 19]") // d_name ptr
	g.emit("movzx ecx, byte ptr [rsi]")
	g.emit("cmp cl, 46")
	g.emit("jne .Lrdd_p2t")
	g.emit("movzx ecx, byte ptr [rsi + 1]")
	g.emit("test cl, cl")
	g.emit("jz .Lrdd_p2a")
	g.emit("cmp cl, 46")
	g.emit("jne .Lrdd_p2t")
	g.emit("movzx ecx, byte ptr [rsi + 2]")
	g.emit("test cl, cl")
	g.emit("jz .Lrdd_p2a")
	g.label(".Lrdd_p2t")
	g.emit("mov [rbp - 80], rsi") // name ptr (survives the alloc)
	// nlen = strlen(name)
	g.emit("xor rdx, rdx")
	g.label(".Lrdd_nl")
	g.emit("cmp byte ptr [rsi + rdx], 0")
	g.emit("je .Lrdd_nld")
	g.emit("add rdx, 1")
	g.emit("jmp .Lrdd_nl")
	g.label(".Lrdd_nld")
	g.emit("mov [rbp - 72], rdx") // nlen
	g.emit("lea edi, [rdx + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov rdx, [rbp - 72]")
	g.emitStrLenStore("edx", "rax")
	g.emit("mov rsi, [rbp - 80]")
	g.emit("xor ecx, ecx")
	g.label(".Lrdd_nc")
	g.emit("cmp rcx, rdx")
	g.emit("jae .Lrdd_ncd")
	g.emit("mov r9b, [rsi + rcx]")
	g.emit("mov [rax + rcx], r9b")
	g.emit("add rcx, 1")
	g.emit("jmp .Lrdd_nc")
	g.label(".Lrdd_ncd")
	g.emit("mov byte ptr [rax + rdx], 0")
	g.emit("mov [rbx + r12 * 8], rax")
	g.emit("add r12, 1")
	g.label(".Lrdd_p2a")
	g.emit("movzx eax, word ptr [r13 + r15 + 16]") // d_reclen
	g.emit("add r15, rax")
	g.emit("jmp .Lrdd_p2")
	g.label(".Lrdd_p2d")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // Ok
	g.emit("mov [rax + 8], rbx")
	g.emit("jmp .Lrdd_return")

	g.label(".Lrdd_err")
	g.emit("neg rax")
	g.emit("mov r12, rax")
	g.emit("mov edi, r12d")
	g.emit("mov rsi, [rbp - 64]")
	g.emit("call __fern_io_error")
	g.emit("mov r12, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // Err
	g.emit("mov [rax + 8], r12")

	g.label(".Lrdd_return")
	g.emit("add rsp, 40")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_read_dir, .-__fern_read_dir")
}

// emitStatRuntime emits `__fern_stat(path) →
// Result[FileStat, IoError]` — newfstatat(AT_FDCWD, path, buf, 0)
// into a 144-byte stack buffer; st_mode is the u32 at offset 24,
// st_size the i64 at offset 48 (Linux x86-64 struct stat). The
// FileStat box uses the native structFieldLayout offsets —
// is_file (i32) @0, is_dir (i32) @4, size (i64) @8 — 16 bytes via
// __fern_alloc_box (immortal, same class as the Result boxes).
// System V: rdi = path string value.
func (g *generator) emitStatRuntime() {
	g.line("")
	g.line(".globl __fern_stat")
	g.line(".type __fern_stat, @function")
	g.label("__fern_stat")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // pathz
	g.emit("push r12") // path byte ptr / is_file
	g.emit("push r13") // path len / IoError box
	g.emit("push r14") // is_dir
	g.emit("push r15") // st_size
	// 6 pushes ⇒ rsp≡8 mod 16; sub 168 realigns — 144-byte stat
	// buf at [rsp..143] + slots:
	//   [rbp-56] emitStrDataPtr inline-spill scratch
	//   [rbp-64] original path string value (io_error arg)
	g.emit("sub rsp, 168")
	g.emit("mov [rbp - 64], rdi")
	g.emitStrLen("r13d", "rdi")
	g.emitStrDataPtr("r12", "rdi", "[rbp - 56]")
	// pathz = NUL-terminated heap copy.
	g.emit("lea edi, [r13 + 1]")
	g.emit("call __fern_alloc")
	g.emit("mov rbx, rax")
	g.emit("xor ecx, ecx")
	g.label(".Lst_cp")
	g.emit("cmp rcx, r13")
	g.emit("jae .Lst_cpd")
	g.emit("mov al, [r12 + rcx]")
	g.emit("mov [rbx + rcx], al")
	g.emit("add rcx, 1")
	g.emit("jmp .Lst_cp")
	g.label(".Lst_cpd")
	g.emit("mov byte ptr [rbx + r13], 0")
	// newfstatat(AT_FDCWD=-100, pathz, statbuf, 0)
	g.emit("mov edi, -100")
	g.emit("mov rsi, rbx")
	g.emit("mov rdx, rsp")
	g.emit("xor r10d, r10d")
	g.emitSyscall(262)
	g.emit("test rax, rax")
	g.emit("js .Lst_err")
	g.emit("mov eax, [rsp + 24]") // st_mode
	g.emit("and eax, 61440")      // S_IFMT
	g.emit("xor r12d, r12d")
	g.emit("cmp eax, 32768") // S_IFREG
	g.emit("jne .Lst_nf")
	g.emit("mov r12d, 1")
	g.label(".Lst_nf")
	g.emit("xor r14d, r14d")
	g.emit("cmp eax, 16384") // S_IFDIR
	g.emit("jne .Lst_nd")
	g.emit("mov r14d, 1")
	g.label(".Lst_nd")
	g.emit("mov r15, [rsp + 48]") // st_size
	// FileStat box: is_file @0, is_dir @4, size @8.
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov [rax], r12d")
	g.emit("mov [rax + 4], r14d")
	g.emit("mov [rax + 8], r15")
	g.emit("mov r13, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // Ok
	g.emit("mov [rax + 8], r13")
	g.emit("jmp .Lst_return")

	g.label(".Lst_err")
	g.emit("neg rax")
	g.emit("mov r13, rax")
	g.emit("mov edi, r13d")
	g.emit("mov rsi, [rbp - 64]")
	g.emit("call __fern_io_error")
	g.emit("mov r13, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // Err
	g.emit("mov [rax + 8], r13")

	g.label(".Lst_return")
	g.emit("add rsp, 168")
	g.emit("pop r15")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_stat, .-__fern_stat")
}

// emitReaderWriterRuntime emits the full Reader / Writer
// runtime bundle on x86-64. Mirrors arm64's helper of the same
// name — same handle layout (4-byte i32 fd at +0), same wasm-
// shaped Result/Option boxes, same shared __fern_io_error
// error path. See the arm64 generator's comment for the
// design rationale.
func (g *generator) emitReaderWriterRuntime() {
	// __fern_make_handle(fd) → ptr to {fd:i32 @0}.
	//
	// Phase 1e-runtime: 8-byte rc header at `[ptr - 8]`
	// holds the static-sentinel 0x80000000 so
	// `__fern_rc_inc/dec` no-op when a Reader/Writer alias is
	// inc'd. Alloc bumps by 8; data lives at `base + 8`.
	g.line("")
	g.line(".globl __fern_make_handle")
	g.line(".type __fern_make_handle, @function")
	g.label("__fern_make_handle")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("sub rsp, 8")
	g.emit("mov ebx, edi") // stash fd
	g.emit("mov edi, 12")  // size + 8-byte rc header
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0x80000000") // static sentinel at base + 0
	g.emit("mov [rax + 8], ebx")              // fd at base + 8 (= data + 0)
	g.emit("add rax, 8")                      // return base + 8 (= data)
	g.emit("add rsp, 8")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_make_handle, .-__fern_make_handle")

	// __fern_stdin / __fern_stdout / __fern_stderr.
	for _, e := range []struct {
		sym string
		fd  int
	}{
		{"__fern_stdin", 0},
		{"__fern_stdout", 1},
		{"__fern_stderr", 2},
	} {
		g.line("")
		g.line(".globl " + e.sym)
		g.line(".type " + e.sym + ", @function")
		g.label(e.sym)
		g.emit(fmt.Sprintf("mov edi, %d", e.fd))
		g.emit("jmp __fern_make_handle") // tail-call
		g.line(".size " + e.sym + ", .-" + e.sym)
	}

	// open_reader / open_writer / open_appender.
	for _, e := range []struct {
		sym   string
		flags int
		mode  int
	}{
		{"__fern_open_reader", 0, 0},
		{"__fern_open_writer", 577, 0644},
		{"__fern_open_appender", 1089, 0644},
	} {
		g.line("")
		g.line(".globl " + e.sym)
		g.line(".type " + e.sym + ", @function")
		g.label(e.sym)
		g.emit("push rbp")
		g.emit("mov rbp, rsp")
		g.emit("push rbx")     // path
		g.emit("push r12")     // handle / errno scratch
		g.emit("mov rbx, rdi") // path
		// openat(AT_FDCWD, path, flags, mode)
		g.emit("mov edi, -100")
		g.emit("mov rsi, rbx")
		g.emit(fmt.Sprintf("mov edx, %d", e.flags))
		g.emit(fmt.Sprintf("mov r10d, %d", e.mode))
		g.emitSyscall(257)
		g.emit("test rax, rax")
		g.emit("js .Lorw_err_" + e.sym)
		// Success: alloc handle, store fd, wrap in Ok box.
		g.emit("mov edi, eax")
		g.emit("call __fern_make_handle")
		g.emit("mov r12, rax") // handle ptr in callee-save
		g.emit("mov edi, 16")
		g.emit("call __fern_alloc_box")
		g.emit("mov dword ptr [rax], 0") // tag=0 (Ok)
		g.emit("mov [rax + 8], r12")
		g.emit("jmp .Lorw_ret_" + e.sym)
		g.label(".Lorw_err_" + e.sym)
		g.emit("neg rax")
		g.emit("mov edi, eax") // errno
		g.emit("mov rsi, rbx") // path
		g.emit("call __fern_io_error")
		g.emit("mov r12, rax") // IoError ptr
		g.emit("mov edi, 16")
		g.emit("call __fern_alloc_box")
		g.emit("mov dword ptr [rax], 1") // Err
		g.emit("mov [rax + 8], r12")
		g.label(".Lorw_ret_" + e.sym)
		g.emit("pop r12")
		g.emit("pop rbx")
		g.emit("pop rbp")
		g.emit("ret")
		g.line(".size " + e.sym + ", .-" + e.sym)
	}

	// __fern_reader_read_line(reader_ptr) → Option[string].
	g.line("")
	g.line(".globl __fern_reader_read_line")
	g.line(".type __fern_reader_read_line, @function")
	g.label("__fern_reader_read_line")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")       // fd
	g.emit("push r12")       // buf base
	g.emit("push r13")       // bytes_read
	g.emit("push r14")       // last byte
	g.emit("mov ebx, [rdi]") // fd
	g.emit("lea r12, [rip + __fern_read_line_buf]")
	g.emit("xor r13, r13")
	g.label(".Lrrl_loop")
	g.emit("cmp r13, 4096")
	g.emit("jge .Lrrl_done")
	g.emit("mov edi, ebx")
	g.emit("lea rsi, [r12 + r13]")
	g.emit("mov edx, 1")
	g.emit("xor eax, eax")
	g.emitSyscallPreloaded(sysRead)
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
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1")
	g.emit("jmp .Lrrl_ret")
	g.label(".Lrrl_some")
	// L2 rc-header layout (see __fern_strcat): payload = N data + 1 NUL.
	g.emit("lea rdi, [r13 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r14, rax")        // r14 = data ptr (= base+8)
	g.emit("mov [r14 - 4], r13d") // length prefix at data-4
	g.emit("mov rdi, r14")
	g.emit("mov rsi, r12")
	g.emit("mov rdx, r13")
	g.emit("call __fern_memcpy")
	// trailing NUL
	g.emit("mov byte ptr [r14 + r13], 0")
	g.emit("mov rbx, r14") // stash str ptr (rbx no longer needed for fd)
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0")
	g.emit("mov [rax + 8], rbx")
	g.label(".Lrrl_ret")
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_reader_read_line, .-__fern_reader_read_line")

	// __fern_reader_read_chunk(reader_ptr, n) → Option[string].
	g.line("")
	g.line(".globl __fern_reader_read_chunk")
	g.line(".type __fern_reader_read_chunk, @function")
	g.label("__fern_reader_read_chunk")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx") // fd
	g.emit("push r12") // n
	g.emit("push r13") // base ptr
	g.emit("sub rsp, 8")
	g.emit("mov ebx, [rdi]") // fd
	g.emit("mov r12, rsi")   // n
	// L2 rc-header layout (see __fern_strcat): payload = n data + NUL slack so
	// the box class matches __fern_str_dec's length+1 free.
	g.emit("lea edi, [r12 + 1]")
	g.emit("call __fern_alloc_rc1")
	g.emit("mov r13, rax") // r13 = data ptr (= base+8)
	// read(fd, data, n)
	g.emit("mov edi, ebx")
	g.emit("mov rsi, r13")
	g.emit("mov rdx, r12")
	g.emit("xor eax, eax")
	g.emitSyscallPreloaded(sysRead)
	g.emit("test rax, rax")
	g.emit("jle .Lrrc_none")
	g.emit("mov [r13 - 4], eax")          // length prefix at data-4
	g.emit("mov r12, rax")                // r12 = bytes_read
	g.emit("mov rbx, r13")                // data ptr
	g.emit("mov byte ptr [rbx + r12], 0") // trailing NUL within alloc
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0")
	g.emit("mov [rax + 8], rbx")
	g.emit("jmp .Lrrc_ret")
	g.label(".Lrrc_none")
	g.emit("mov edi, 4")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1")
	g.label(".Lrrc_ret")
	g.emit("add rsp, 8")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_reader_read_chunk, .-__fern_reader_read_chunk")

	// __fern_writer_write(writer_ptr, s) → Option[IoError].
	g.line("")
	g.line(".globl __fern_writer_write")
	g.line(".type __fern_writer_write, @function")
	g.label("__fern_writer_write")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")       // fd
	g.emit("push r12")       // s data ptr
	g.emit("push r13")       // remaining bytes
	g.emit("push r14")       // bytes_written
	g.emit("sub rsp, 16")    // 8-byte scratch for emitStrDataPtr SSO spill + 8 align
	g.emit("mov ebx, [rdi]") // fd
	// Length comes from the ORIGINAL tagged value (rsi) — emitStrLen
	// is SSO-aware. Only then convert to a real byte pointer: for an
	// inline (SSO) string the bytes live in the register, so
	// emitStrDataPtr spills them to the frame scratch slot and hands
	// back a pointer into it. Treating the raw inline value as an
	// address (the old `mov r12, rsi`) wrote from a garbage pointer
	// and trapped EFAULT for every <=7-byte string.
	g.emitStrLen("r13d", "rsi")                  // len (SSO-aware)
	g.emitStrDataPtr("r12", "rsi", "[rbp - 40]") // r12 = data byte ptr
	g.emit("xor r14, r14")
	g.label(".Lww_loop")
	g.emit("cmp r14, r13")
	g.emit("jge .Lww_done")
	g.emit("mov edi, ebx")
	g.emit("lea rsi, [r12 + r14]")
	g.emit("mov rdx, r13")
	g.emit("sub rdx, r14")
	g.emitSyscall(1)
	g.emit("test rax, rax")
	g.emit("js .Lww_err")
	g.emit("add r14, rax")
	g.emit("jmp .Lww_loop")
	g.label(".Lww_done")
	g.emit("mov edi, 4")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // None
	g.emit("jmp .Lww_ret")
	g.label(".Lww_err")
	g.emit("neg rax")
	g.emit("mov edi, eax")
	g.emit("lea rsi, [rip + .LStr_ioerr_empty]")
	g.emit("call __fern_io_error")
	g.emit("mov r12, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // Some
	g.emit("mov [rax + 8], r12")
	g.label(".Lww_ret")
	g.emit("add rsp, 16") // drop SSO scratch
	g.emit("pop r14")
	g.emit("pop r13")
	g.emit("pop r12")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_writer_write, .-__fern_writer_write")

	// __fern_close_fd_box(handle_ptr) → Option[IoError].
	// Shared by Reader.close + Writer.close.
	g.line("")
	g.line(".globl __fern_close_fd_box")
	g.line(".type __fern_close_fd_box, @function")
	g.label("__fern_close_fd_box")
	g.emit("push rbp")
	g.emit("mov rbp, rsp")
	g.emit("push rbx")
	g.emit("sub rsp, 8")
	g.emit("mov edi, [rdi]") // fd
	g.emitSyscall(3)
	g.emit("test rax, rax")
	g.emit("js .Lcfb_err")
	g.emit("mov edi, 4")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 1") // None
	g.emit("jmp .Lcfb_ret")
	g.label(".Lcfb_err")
	g.emit("neg rax")
	g.emit("mov edi, eax")
	g.emit("lea rsi, [rip + .LStr_ioerr_empty]")
	g.emit("call __fern_io_error")
	g.emit("mov rbx, rax")
	g.emit("mov edi, 16")
	g.emit("call __fern_alloc_box")
	g.emit("mov dword ptr [rax], 0") // Some
	g.emit("mov [rax + 8], rbx")
	g.label(".Lcfb_ret")
	g.emit("add rsp, 8")
	g.emit("pop rbx")
	g.emit("pop rbp")
	g.emit("ret")
	g.line(".size __fern_close_fd_box, .-__fern_close_fd_box")
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
